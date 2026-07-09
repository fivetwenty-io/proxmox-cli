package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newRealmAdCmd builds `pmx pbs realm ad` — manage Active Directory realm
// configurations (/config/access/ad CRUD).
func newRealmAdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ad",
		Short: "Manage Active Directory realm configurations",
		Long:  "List, inspect, create, update, and delete Active Directory (AD) authentication realm configurations.",
	}
	cmd.AddCommand(
		newRealmAdLsCmd(),
		newRealmAdShowCmd(),
		newRealmAdAddCmd(),
		newRealmAdUpdateCmd(),
		newRealmAdDeleteCmd(),
	)
	return cmd
}

// realmAdListEntry is the decoded shape of one element of GET
// /config/access/ad: the AD realm's full configuration (minus the
// write-only password field).
type realmAdListEntry struct {
	BaseDn              *string      `json:"base-dn,omitempty"`
	BindDn              *string      `json:"bind-dn,omitempty"`
	Capath              *string      `json:"capath,omitempty"`
	Comment             *string      `json:"comment,omitempty"`
	Default             *pve.PVEBool `json:"default,omitempty"`
	Filter              *string      `json:"filter,omitempty"`
	Mode                *string      `json:"mode,omitempty"`
	Port                *int64       `json:"port,omitempty"`
	Realm               string       `json:"realm"`
	Server1             string       `json:"server1"`
	Server2             *string      `json:"server2,omitempty"`
	SyncAttributes      *string      `json:"sync-attributes,omitempty"`
	SyncDefaultsOptions *string      `json:"sync-defaults-options,omitempty"`
	UserClasses         *string      `json:"user-classes,omitempty"`
	Verify              *pve.PVEBool `json:"verify,omitempty"`
}

// newRealmAdLsCmd builds `pmx pbs realm ad ls` — list configured AD realms
// (GET /config/access/ad).
func newRealmAdLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List Active Directory realm configurations",
		Long:  "List the Active Directory authentication realms configured on this server (GET /config/access/ad).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListAccessAd(cmd.Context())
			if err != nil {
				return fmt.Errorf("list AD realms: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]realmAdListEntry, 0, len(items))

			for _, raw := range items {
				var e realmAdListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode AD realm entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Realm < entries[j].Realm })

			headers := []string{"REALM", "SERVER1", "SERVER2", "PORT", "MODE", "DEFAULT", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Realm, e.Server1, pbsFormatOptionalString(e.Server2), pbsFormatOptionalInt64(e.Port),
					pbsFormatOptionalString(e.Mode), realmFormatOptionalPVEBool(e.Default), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRealmAdShowCmd builds `pmx pbs realm ad show <realm>` — show a single AD
// realm's configuration (GET /config/access/ad/{realm}).
func newRealmAdShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <realm>",
		Short: "Show a single AD realm's configuration",
		Long: "Show every populated field of a single AD realm configuration (GET " +
			"/config/access/ad/{realm}). The bind password is write-only and is " +
			"never returned by the API. The API also omits options left at their " +
			"built-in defaults; pass --defaults to also list those, with the value " +
			"they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			resp, err := deps.PBS.Config.GetAccessAd(cmd.Context(), realm)
			if err != nil {
				return fmt.Errorf("get AD realm %q: %w", realm, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode AD realm %q: %w", realm, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(realmAdOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// realmAdFlags collects the AD realm attribute flags shared by `ad add` and
// `ad update`. Every field maps directly onto a CreateAccessAdParams /
// UpdateAccessAdParams field of the same name.
type realmAdFlags struct {
	baseDn              string
	bindDn              string
	capath              string
	comment             string
	isDefault           bool
	filter              string
	mode                string
	password            string
	port                int64
	server1             string
	server2             string
	syncAttributes      string
	syncDefaultsOptions string
	userClasses         string
	verify              bool

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `ad add` and
// `ad update`, except server1 (required on add, optional on update — bound
// separately by each caller).
func (af *realmAdFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&af.baseDn, "base-dn", "", "LDAP base domain for user search")
	f.StringVar(&af.bindDn, "bind-dn", "", "LDAP bind domain used to authenticate the sync user")
	f.StringVar(&af.capath, "capath", "", "path to a trusted CA certificate file or directory")
	f.StringVar(&af.comment, "comment", "", "comment")
	f.BoolVar(&af.isDefault, "default", false, "select this realm by default on login")
	f.StringVar(&af.filter, "filter", "", "custom LDAP search filter for user sync")
	f.StringVar(&af.mode, "mode", "", "LDAP connection type: ldap|ldap+starttls|ldaps")
	f.StringVar(&af.password, "password", "", "AD bind password")
	f.Int64Var(&af.port, "port", 0, "AD server port")
	f.StringVar(&af.server2, "server2", "", "fallback AD server address")
	f.StringVar(&af.syncAttributes, "sync-attributes", "",
		"comma-separated key=value pairs mapping LDAP attributes to PBS user fields")
	f.StringVar(&af.syncDefaultsOptions, "sync-defaults-options", "", "default sync options applied by `realm sync`")
	f.StringVar(&af.userClasses, "user-classes", "", "comma-separated list of allowed objectClass values")
	f.BoolVar(&af.verify, "verify", false, "verify the server's TLS certificate")
}

// registerUpdate binds every flag `ad update` accepts, including the
// update-only delete/digest fields and the optional server1.
func (af *realmAdFlags) registerUpdate(cmd *cobra.Command) {
	af.registerCommon(cmd)
	f := cmd.Flags()
	f.StringVar(&af.server1, "server1", "", "AD server address")
	f.StringArrayVar(&af.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&af.digest, "digest", "", "only update if the current config digest matches")
}

// applyCreate builds the create payload, forwarding optional flags only when set.
func (af *realmAdFlags) applyCreate(cmd *cobra.Command, p *pbsconfig.CreateAccessAdParams) {
	fl := cmd.Flags()
	if fl.Changed("base-dn") {
		p.BaseDn = &af.baseDn
	}
	if fl.Changed("bind-dn") {
		p.BindDn = &af.bindDn
	}
	if fl.Changed("capath") {
		p.Capath = &af.capath
	}
	if fl.Changed("comment") {
		p.Comment = &af.comment
	}
	if fl.Changed("default") {
		p.Default = &af.isDefault
	}
	if fl.Changed("filter") {
		p.Filter = &af.filter
	}
	if fl.Changed("mode") {
		p.Mode = &af.mode
	}
	if fl.Changed("password") {
		p.Password = &af.password
	}
	if fl.Changed("port") {
		p.Port = &af.port
	}
	if fl.Changed("server2") {
		p.Server2 = &af.server2
	}
	if fl.Changed("sync-attributes") {
		p.SyncAttributes = &af.syncAttributes
	}
	if fl.Changed("sync-defaults-options") {
		p.SyncDefaultsOptions = &af.syncDefaultsOptions
	}
	if fl.Changed("user-classes") {
		p.UserClasses = &af.userClasses
	}
	if fl.Changed("verify") {
		p.Verify = &af.verify
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (af *realmAdFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateAccessAdParams) {
	fl := cmd.Flags()
	if fl.Changed("base-dn") {
		p.BaseDn = &af.baseDn
	}
	if fl.Changed("bind-dn") {
		p.BindDn = &af.bindDn
	}
	if fl.Changed("capath") {
		p.Capath = &af.capath
	}
	if fl.Changed("comment") {
		p.Comment = &af.comment
	}
	if fl.Changed("default") {
		p.Default = &af.isDefault
	}
	if fl.Changed("filter") {
		p.Filter = &af.filter
	}
	if fl.Changed("mode") {
		p.Mode = &af.mode
	}
	if fl.Changed("password") {
		p.Password = &af.password
	}
	if fl.Changed("port") {
		p.Port = &af.port
	}
	if fl.Changed("server1") {
		p.Server1 = &af.server1
	}
	if fl.Changed("server2") {
		p.Server2 = &af.server2
	}
	if fl.Changed("sync-attributes") {
		p.SyncAttributes = &af.syncAttributes
	}
	if fl.Changed("sync-defaults-options") {
		p.SyncDefaultsOptions = &af.syncDefaultsOptions
	}
	if fl.Changed("user-classes") {
		p.UserClasses = &af.userClasses
	}
	if fl.Changed("verify") {
		p.Verify = &af.verify
	}
	if fl.Changed("delete") {
		p.Delete = af.del
	}
	if fl.Changed("digest") {
		p.Digest = &af.digest
	}
}

// newRealmAdAddCmd builds `pmx pbs realm ad add <realm>` — create an AD realm
// configuration (POST /config/access/ad). --server1 is required; every other
// option is optional and only forwarded when explicitly set.
func newRealmAdAddCmd() *cobra.Command {
	var af realmAdFlags
	cmd := &cobra.Command{
		Use:   "add <realm>",
		Short: "Create an AD realm configuration",
		Long: "Create a new Active Directory authentication realm configuration " +
			"(POST /config/access/ad). --server1 is required; every other option " +
			"is optional and only forwarded when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if af.server1 == "" {
				return fmt.Errorf("--server1 is required")
			}

			params := &pbsconfig.CreateAccessAdParams{
				Realm:   realm,
				Server1: af.server1,
			}
			af.applyCreate(cmd, params)

			err := deps.PBS.Config.CreateAccessAd(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create AD realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("AD realm %q created.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	af.registerCommon(cmd)
	cmd.Flags().StringVar(&af.server1, "server1", "", "AD server address (required)")
	cli.MustMarkRequired(cmd, "server1")
	return cmd
}

// newRealmAdUpdateCmd builds `pmx pbs realm ad update <realm>` — update an AD
// realm configuration (PUT /config/access/ad/{realm}). Only flags explicitly
// set are sent; use --delete to reset properties to their default.
func newRealmAdUpdateCmd() *cobra.Command {
	var af realmAdFlags
	cmd := &cobra.Command{
		Use:   "update <realm>",
		Short: "Update an AD realm configuration",
		Long: "Update an existing Active Directory realm configuration (PUT " +
			"/config/access/ad/{realm}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update AD realm %q: no changes requested: pass at least one flag", realm)
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range af.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateAccessAdParams{}
			af.applyUpdate(cmd, params)

			_, err := deps.PBS.Config.UpdateAccessAd(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("update AD realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("AD realm %q updated.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	af.registerUpdate(cmd)
	return cmd
}

// newRealmAdDeleteCmd builds `pmx pbs realm ad delete <realm>` — remove an AD
// realm configuration (DELETE /config/access/ad/{realm}).
func newRealmAdDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <realm>",
		Short: "Delete an AD realm configuration",
		Long: "Remove an Active Directory realm configuration (DELETE /config/access/ad/{realm}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete AD realm %q without confirmation: pass --yes/-y", realm)
			}

			params := &pbsconfig.DeleteAccessAdParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteAccessAd(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("delete AD realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("AD realm %q deleted.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
