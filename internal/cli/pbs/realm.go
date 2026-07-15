package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pbsaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newRealmCmd builds `pmx pbs realm` — list authentication realms, trigger
// realm user-sync, and manage the per-type realm configurations (AD, LDAP,
// OpenID, PAM, PBS) backing them (/access/domains and
// /config/access/{ad,ldap,openid,pam,pbs}).
func newRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "realm",
		Short: "Manage authentication realms",
		Long: "List authentication realms, synchronize realm users from a directory " +
			"service, and manage the per-type realm configurations (AD, LDAP, " +
			"OpenID, PAM, PBS) that back them.",
	}
	cmd.AddCommand(
		newRealmLsCmd(),
		newRealmSyncCmd(),
		newRealmAdCmd(),
		newRealmLdapCmd(),
		newRealmOpenidCmd(),
		newRealmPamCmd(),
		newRealmPbsCmd(),
	)
	return cmd
}

// realmDomainEntry is the decoded shape of one element of GET /access/domains:
// the basic realm-index info (not the full per-type config — see
// `realm ad|ldap|openid|pam|pbs show` for that).
type realmDomainEntry struct {
	Comment *string      `json:"comment,omitempty"`
	Default *pve.PVEBool `json:"default,omitempty"`
	Realm   string       `json:"realm"`
	Type    string       `json:"type"`
}

// realmFormatOptionalPVEBool dereferences a *pve.PVEBool for table rendering,
// returning "false" for nil (an unset optional PBS boolean defaults to
// false). PBS realm-config booleans are generated as client.PVEBool (tolerant
// of both native JSON booleans and the PVE 0/1 encoding), so this cannot
// reuse the *bool-typed pbsFormatOptionalBool helper from gc.go.
func realmFormatOptionalPVEBool(b *pve.PVEBool) string {
	if b == nil {
		return "false"
	}

	if b.Bool() {
		return "true"
	}

	return "false"
}

// newRealmLsCmd builds `pmx pbs realm ls` — list every authentication realm
// with its type (GET /access/domains). This endpoint requires no
// authentication on PBS itself (it backs the login box), but the CLI still
// requires a configured context to reach it.
func newRealmLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List authentication realms",
		Long:    "List every authentication realm configured on this server, with its type (GET /access/domains).",
		Example: "  pmx pbs realm ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Access.ListDomains(cmd.Context())
			if err != nil {
				return fmt.Errorf("list realms: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]realmDomainEntry, 0, len(items))

			for _, raw := range items {
				var e realmDomainEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode realm entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Realm < entries[j].Realm })

			headers := []string{"REALM", "TYPE", "DEFAULT", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Realm, e.Type, realmFormatOptionalPVEBool(e.Default), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRealmSyncCmd builds `pmx pbs realm sync <realm>` — synchronize users of
// a realm from its backing directory service (POST
// /access/domains/{realm}/sync). Runs as an asynchronous task; the command
// blocks until it finishes unless --async is set.
//
// CreateDomainsSyncParams (the generated binding's request payload) carries
// only dry-run, enable-new, and remove-vanished; the PBS API schema for this
// endpoint has no further tunables (e.g. no scope filter), so this command
// exposes exactly those three.
func newRealmSyncCmd() *cobra.Command {
	var (
		dryRun         bool
		enableNew      bool
		removeVanished string
	)
	cmd := &cobra.Command{
		Use:   "sync <realm>",
		Short: "Synchronize a realm's users from its directory service",
		Long: "Synchronize the users of an LDAP/AD/OpenID realm from its backing " +
			"directory service (POST /access/domains/{realm}/sync). Runs as an " +
			"asynchronous task; the command blocks until it finishes unless --async is set.",
		Example: "  pmx pbs realm sync company --enable-new",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]
			if realm == "" {
				return fmt.Errorf("realm name must not be empty")
			}

			params := &pbsaccess.CreateDomainsSyncParams{}

			fl := cmd.Flags()
			if fl.Changed("dry-run") {
				params.DryRun = boolPtr(dryRun)
			}
			if fl.Changed("enable-new") {
				params.EnableNew = boolPtr(enableNew)
			}
			if fl.Changed("remove-vanished") {
				if removeVanished == "" {
					return fmt.Errorf("--remove-vanished: value must not be empty")
				}
				params.RemoveVanished = strPtr(removeVanished)
			}

			resp, err := deps.PBS.Access.CreateDomainsSync(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("sync realm %q: %w", realm, err)
			}

			if resp == nil {
				return fmt.Errorf("sync realm %q: nil response from PBS", realm)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Sync of realm %q finished.", realm))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "report what would change, without creating or deleting anything")
	f.BoolVar(&enableNew, "enable-new", false, "enable newly synced users immediately")
	f.StringVar(&removeVanished, "remove-vanished", "",
		"semicolon-separated list of removal behaviors for vanished users: acl, entry, properties")
	return cmd
}
