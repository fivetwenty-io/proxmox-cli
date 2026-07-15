package pdm

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newRealmCmd builds `pmx pdm realm` — list authentication realms, trigger
// realm user-sync, and manage the per-type realm configurations (AD, LDAP,
// OpenID, PAM, PDM) backing them (/access/domains and
// /config/access/{ad,ldap,openid,pam,pdm}).
func newRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "realm",
		Short: "Manage authentication realms",
		Long: "List authentication realms, synchronize realm users from a directory " +
			"service, and manage the per-type realm configurations (AD, LDAP, " +
			"OpenID, PAM, PDM) that back them.",
	}
	cmd.AddCommand(
		newRealmLsCmd(),
		newRealmSyncCmd(),
		newRealmAdCmd(),
		newRealmLdapCmd(),
		newRealmOpenidCmd(),
		newRealmPamCmd(),
		newRealmPdmCmd(),
	)
	return cmd
}

// realmDomainEntry is the decoded shape of one element of GET /access/domains:
// the basic realm-index info (not the full per-type config — see
// `realm ad|ldap|openid|pam|pdm show` for that).
type realmDomainEntry struct {
	Comment *string            `json:"comment,omitempty"`
	Default *pveclient.PVEBool `json:"default,omitempty"`
	Realm   string             `json:"realm"`
	Type    string             `json:"type"`
}

// newRealmLsCmd builds `pmx pdm realm ls` — list every authentication realm
// with its type (GET /access/domains).
func newRealmLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List authentication realms",
		Long:    "List every authentication realm configured on this Proxmox Datacenter Manager, with its type (GET /access/domains).",
		Example: "  pmx pdm realm ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Access.ListDomains(cmd.Context())
			if err != nil {
				return fmt.Errorf("list realms: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[realmDomainEntry](items, "realm")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Realm < table[j].Entry.Realm })

			headers := []string{"REALM", "TYPE", "DEFAULT", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Realm, e.Type, pveBoolPtrString(e.Default), strPtrString(e.Comment),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRealmSyncCmd builds `pmx pdm realm sync <realm>` — synchronize users of
// a realm from its backing directory service (POST
// /access/domains/{realm}/sync). CreateDomainsSyncResponse is a
// json.RawMessage carrying a UPID string (access_gen.go:290,
// v3.6.0 — "CreateDomainsSyncResponse is the raw JSON returned by POST
// /access/domains/{realm}/sync"), so this always runs as an asynchronous
// task; the command blocks until it finishes unless --async is set.
//
// CreateDomainsSyncParams (access_gen.go:280-287, v3.6.0) carries only
// dry-run, enable-new, and remove-vanished; the PDM API schema for this
// endpoint has no further tunables, so this command exposes exactly those three.
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
		Example: "  pmx pdm realm sync company --dry-run",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]
			if realm == "" {
				return fmt.Errorf("realm name must not be empty")
			}

			params := &pdmaccess.CreateDomainsSyncParams{}

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

			resp, err := deps.PDM.Access.CreateDomainsSync(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("sync realm %q: %w", realm, err)
			}

			if resp == nil {
				return fmt.Errorf("sync realm %q: nil response from PDM", realm)
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
