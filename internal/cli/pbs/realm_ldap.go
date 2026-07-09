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

// newRealmLdapCmd builds `pmx pbs realm ldap` — manage LDAP realm
// configurations (/config/access/ldap CRUD).
func newRealmLdapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ldap",
		Short: "Manage LDAP realm configurations",
		Long:  "List, inspect, create, update, and delete LDAP authentication realm configurations.",
	}
	cmd.AddCommand(
		newRealmLdapLsCmd(),
		newRealmLdapShowCmd(),
		newRealmLdapAddCmd(),
		newRealmLdapUpdateCmd(),
		newRealmLdapDeleteCmd(),
	)
	return cmd
}

// realmLdapListEntry is the decoded shape of one element of GET
// /config/access/ldap: the LDAP realm's full configuration (minus the
// write-only password field).
type realmLdapListEntry struct {
	BaseDn              string       `json:"base-dn"`
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
	UserAttr            string       `json:"user-attr"`
	UserClasses         *string      `json:"user-classes,omitempty"`
	Verify              *pve.PVEBool `json:"verify,omitempty"`
}

// newRealmLdapLsCmd builds `pmx pbs realm ldap ls` — list configured LDAP
// realms (GET /config/access/ldap).
func newRealmLdapLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List LDAP realm configurations",
		Long:  "List the LDAP authentication realms configured on this server (GET /config/access/ldap).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListAccessLdap(cmd.Context())
			if err != nil {
				return fmt.Errorf("list LDAP realms: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]realmLdapListEntry, 0, len(items))

			for _, raw := range items {
				var e realmLdapListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode LDAP realm entry: %w", err)
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

// newRealmLdapShowCmd builds `pmx pbs realm ldap show <realm>` — show a
// single LDAP realm's configuration (GET /config/access/ldap/{realm}).
func newRealmLdapShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <realm>",
		Short: "Show a single LDAP realm's configuration",
		Long: "Show every populated field of a single LDAP realm configuration (GET " +
			"/config/access/ldap/{realm}). The bind password is write-only and is " +
			"never returned by the API. The API also omits options left at their " +
			"built-in defaults; pass --defaults to also list those, with the value " +
			"they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			resp, err := deps.PBS.Config.GetAccessLdap(cmd.Context(), realm)
			if err != nil {
				return fmt.Errorf("get LDAP realm %q: %w", realm, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode LDAP realm %q: %w", realm, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(realmLdapOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// realmLdapFlags collects the LDAP realm attribute flags shared by `ldap add`
// and `ldap update`. Every field maps directly onto a CreateAccessLdapParams
// / UpdateAccessLdapParams field of the same name.
type realmLdapFlags struct {
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
	userAttr            string
	userClasses         string
	verify              bool

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `ldap add` and
// `ldap update`, except base-dn, server1, and user-attr (required on add,
// optional on update — bound separately by each caller).
func (lf *realmLdapFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&lf.bindDn, "bind-dn", "", "LDAP bind domain used to authenticate the sync user")
	f.StringVar(&lf.capath, "capath", "", "path to a trusted CA certificate file or directory")
	f.StringVar(&lf.comment, "comment", "", "comment")
	f.BoolVar(&lf.isDefault, "default", false, "select this realm by default on login")
	f.StringVar(&lf.filter, "filter", "", "custom LDAP search filter for user sync")
	f.StringVar(&lf.mode, "mode", "", "LDAP connection type: ldap|ldap+starttls|ldaps")
	f.StringVar(&lf.password, "password", "", "LDAP bind password")
	f.Int64Var(&lf.port, "port", 0, "LDAP server port")
	f.StringVar(&lf.server2, "server2", "", "fallback LDAP server address")
	f.StringVar(&lf.syncAttributes, "sync-attributes", "",
		"comma-separated key=value pairs mapping LDAP attributes to PBS user fields")
	f.StringVar(&lf.syncDefaultsOptions, "sync-defaults-options", "", "default sync options applied by `realm sync`")
	f.StringVar(&lf.userClasses, "user-classes", "", "comma-separated list of allowed objectClass values")
	f.BoolVar(&lf.verify, "verify", false, "verify the server's TLS certificate")
}

// registerUpdate binds every flag `ldap update` accepts, including the
// update-only delete/digest fields and the optional base-dn, server1, and
// user-attr.
func (lf *realmLdapFlags) registerUpdate(cmd *cobra.Command) {
	lf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringVar(&lf.baseDn, "base-dn", "", "LDAP base domain for user search")
	f.StringVar(&lf.server1, "server1", "", "LDAP server address")
	f.StringVar(&lf.userAttr, "user-attr", "", "username attribute mapping a userid to an LDAP dn")
	f.StringArrayVar(&lf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&lf.digest, "digest", "", "only update if the current config digest matches")
}

// applyCreate builds the create payload, forwarding optional flags only when set.
func (lf *realmLdapFlags) applyCreate(cmd *cobra.Command, p *pbsconfig.CreateAccessLdapParams) {
	fl := cmd.Flags()
	if fl.Changed("bind-dn") {
		p.BindDn = &lf.bindDn
	}
	if fl.Changed("capath") {
		p.Capath = &lf.capath
	}
	if fl.Changed("comment") {
		p.Comment = &lf.comment
	}
	if fl.Changed("default") {
		p.Default = &lf.isDefault
	}
	if fl.Changed("filter") {
		p.Filter = &lf.filter
	}
	if fl.Changed("mode") {
		p.Mode = &lf.mode
	}
	if fl.Changed("password") {
		p.Password = &lf.password
	}
	if fl.Changed("port") {
		p.Port = &lf.port
	}
	if fl.Changed("server2") {
		p.Server2 = &lf.server2
	}
	if fl.Changed("sync-attributes") {
		p.SyncAttributes = &lf.syncAttributes
	}
	if fl.Changed("sync-defaults-options") {
		p.SyncDefaultsOptions = &lf.syncDefaultsOptions
	}
	if fl.Changed("user-classes") {
		p.UserClasses = &lf.userClasses
	}
	if fl.Changed("verify") {
		p.Verify = &lf.verify
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (lf *realmLdapFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateAccessLdapParams) {
	fl := cmd.Flags()
	if fl.Changed("base-dn") {
		p.BaseDn = &lf.baseDn
	}
	if fl.Changed("bind-dn") {
		p.BindDn = &lf.bindDn
	}
	if fl.Changed("capath") {
		p.Capath = &lf.capath
	}
	if fl.Changed("comment") {
		p.Comment = &lf.comment
	}
	if fl.Changed("default") {
		p.Default = &lf.isDefault
	}
	if fl.Changed("filter") {
		p.Filter = &lf.filter
	}
	if fl.Changed("mode") {
		p.Mode = &lf.mode
	}
	if fl.Changed("password") {
		p.Password = &lf.password
	}
	if fl.Changed("port") {
		p.Port = &lf.port
	}
	if fl.Changed("server1") {
		p.Server1 = &lf.server1
	}
	if fl.Changed("server2") {
		p.Server2 = &lf.server2
	}
	if fl.Changed("sync-attributes") {
		p.SyncAttributes = &lf.syncAttributes
	}
	if fl.Changed("sync-defaults-options") {
		p.SyncDefaultsOptions = &lf.syncDefaultsOptions
	}
	if fl.Changed("user-attr") {
		p.UserAttr = &lf.userAttr
	}
	if fl.Changed("user-classes") {
		p.UserClasses = &lf.userClasses
	}
	if fl.Changed("verify") {
		p.Verify = &lf.verify
	}
	if fl.Changed("delete") {
		p.Delete = lf.del
	}
	if fl.Changed("digest") {
		p.Digest = &lf.digest
	}
}

// newRealmLdapAddCmd builds `pmx pbs realm ldap add <realm>` — create an LDAP
// realm configuration (POST /config/access/ldap). --base-dn, --server1, and
// --user-attr are required; every other option is optional and only
// forwarded when explicitly set.
func newRealmLdapAddCmd() *cobra.Command {
	var lf realmLdapFlags
	cmd := &cobra.Command{
		Use:   "add <realm>",
		Short: "Create an LDAP realm configuration",
		Long: "Create a new LDAP authentication realm configuration (POST " +
			"/config/access/ldap). --base-dn, --server1, and --user-attr are " +
			"required; every other option is optional and only forwarded when " +
			"explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if lf.baseDn == "" {
				return fmt.Errorf("--base-dn is required")
			}

			if lf.server1 == "" {
				return fmt.Errorf("--server1 is required")
			}

			if lf.userAttr == "" {
				return fmt.Errorf("--user-attr is required")
			}

			params := &pbsconfig.CreateAccessLdapParams{
				Realm:    realm,
				BaseDn:   lf.baseDn,
				Server1:  lf.server1,
				UserAttr: lf.userAttr,
			}
			lf.applyCreate(cmd, params)

			err := deps.PBS.Config.CreateAccessLdap(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create LDAP realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("LDAP realm %q created.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	lf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringVar(&lf.baseDn, "base-dn", "", "LDAP base domain for user search (required)")
	f.StringVar(&lf.server1, "server1", "", "LDAP server address (required)")
	f.StringVar(&lf.userAttr, "user-attr", "", "username attribute mapping a userid to an LDAP dn (required)")
	cli.MustMarkRequired(cmd, "base-dn")
	cli.MustMarkRequired(cmd, "server1")
	cli.MustMarkRequired(cmd, "user-attr")
	return cmd
}

// newRealmLdapUpdateCmd builds `pmx pbs realm ldap update <realm>` — update
// an LDAP realm configuration (PUT /config/access/ldap/{realm}). Only flags
// explicitly set are sent; use --delete to reset properties to their default.
func newRealmLdapUpdateCmd() *cobra.Command {
	var lf realmLdapFlags
	cmd := &cobra.Command{
		Use:   "update <realm>",
		Short: "Update an LDAP realm configuration",
		Long: "Update an existing LDAP realm configuration (PUT " +
			"/config/access/ldap/{realm}). Only flags explicitly set are sent; " +
			"use --delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update LDAP realm %q: no changes requested: pass at least one flag", realm)
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range lf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateAccessLdapParams{}
			lf.applyUpdate(cmd, params)

			_, err := deps.PBS.Config.UpdateAccessLdap(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("update LDAP realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("LDAP realm %q updated.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	lf.registerUpdate(cmd)
	return cmd
}

// newRealmLdapDeleteCmd builds `pmx pbs realm ldap delete <realm>` — remove
// an LDAP realm configuration (DELETE /config/access/ldap/{realm}).
func newRealmLdapDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <realm>",
		Short: "Delete an LDAP realm configuration",
		Long: "Remove an LDAP realm configuration (DELETE /config/access/ldap/{realm}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete LDAP realm %q without confirmation: pass --yes/-y", realm)
			}

			params := &pbsconfig.DeleteAccessLdapParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteAccessLdap(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("delete LDAP realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("LDAP realm %q deleted.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
