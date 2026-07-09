package pdm

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// realmOpenidSecretKeys are the OpenID realm fields that must never be
// echoed back to the user. Unlike the AD/LDAP "password" bind credential
// (absent from GetAccessAd/LdapResponse and from the API schema's GET
// returns.properties), "client-key" IS declared in both
// GetAccessOpenidResponse (config_gen.go, v3.6.0) and the PDM API schema's
// GET /config/access/openid/{realm} returns.properties list, so the server
// can return it. stripRealmOpenidSecrets defensively removes it from
// show/ls output before rendering.
var realmOpenidSecretKeys = []string{"client-key"}

// stripRealmOpenidSecrets deletes every key in realmOpenidSecretKeys from
// fields, in place.
func stripRealmOpenidSecrets(fields map[string]any) {
	for _, k := range realmOpenidSecretKeys {
		delete(fields, k)
	}
}

// newRealmOpenidCmd builds `pmx pdm realm openid` — manage OpenID Connect
// realm configurations (/config/access/openid CRUD).
//
// The interactive auth-url/login sub-tree (POST /access/openid/auth-url,
// POST /access/openid/login) is intentionally not exposed here: it is a
// browser-redirect login flow, not a config-management operation, and does
// not fit a non-interactive CLI.
func newRealmOpenidCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openid",
		Short: "Manage OpenID Connect realm configurations",
		Long:  "List, inspect, create, update, and delete OpenID Connect authentication realm configurations.",
	}
	cmd.AddCommand(
		newRealmOpenidLsCmd(),
		newRealmOpenidShowCmd(),
		newRealmOpenidAddCmd(),
		newRealmOpenidUpdateCmd(),
		newRealmOpenidDeleteCmd(),
	)
	return cmd
}

// realmOpenidListEntry is the decoded shape of one element of GET
// /config/access/openid: the OpenID realm's full configuration, minus the
// client-key (see realmOpenidSecretKeys).
type realmOpenidListEntry struct {
	AcrValues     *string            `json:"acr-values,omitempty"`
	Audiences     *string            `json:"audiences,omitempty"`
	Autocreate    *pveclient.PVEBool `json:"autocreate,omitempty"`
	ClientId      string             `json:"client-id"`
	Comment       *string            `json:"comment,omitempty"`
	Default       *pveclient.PVEBool `json:"default,omitempty"`
	IssuerUrl     string             `json:"issuer-url"`
	Prompt        *string            `json:"prompt,omitempty"`
	Realm         string             `json:"realm"`
	Scopes        *string            `json:"scopes,omitempty"`
	UsernameClaim *string            `json:"username-claim,omitempty"`
}

// newRealmOpenidLsCmd builds `pmx pdm realm openid ls` — list configured
// OpenID realms (GET /config/access/openid).
func newRealmOpenidLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List OpenID Connect realm configurations",
		Long:  "List the OpenID Connect authentication realms configured on this Proxmox Datacenter Manager (GET /config/access/openid).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAccessOpenid(cmd.Context())
			if err != nil {
				return fmt.Errorf("list OpenID realms: %w", err)
			}

			items := rawItemsOf(resp)
			type openidRow struct {
				entry realmOpenidListEntry
				raw   map[string]any
			}
			table := make([]openidRow, 0, len(items))

			for _, raw := range items {
				var e realmOpenidListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode OpenID realm entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode OpenID realm entry: %w", err)
				}
				stripRealmOpenidSecrets(m)

				table = append(table, openidRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Realm < table[j].entry.Realm })

			headers := []string{"REALM", "ISSUER-URL", "CLIENT-ID", "DEFAULT", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Realm, e.IssuerUrl, e.ClientId, pveBoolPtrString(e.Default), strPtrString(e.Comment),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRealmOpenidShowCmd builds `pmx pdm realm openid show <realm>` — show a
// single OpenID realm's configuration (GET /config/access/openid/{realm}).
func newRealmOpenidShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <realm>",
		Short: "Show a single OpenID realm's configuration",
		Long: "Show every populated field of a single OpenID realm configuration " +
			"(GET /config/access/openid/{realm}). The client key is write-only " +
			"credential material and is stripped from the output even if the API " +
			"returns it. The API also omits options left at their built-in " +
			"defaults; pass --defaults to also list those, with the value they " +
			"effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			resp, err := deps.PDM.Config.GetAccessOpenid(cmd.Context(), realm)
			if err != nil {
				return fmt.Errorf("get OpenID realm %q: %w", realm, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode OpenID realm %q: %w", realm, err)
			}
			stripRealmOpenidSecrets(fields)

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(realmOpenidOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// realmOpenidFlags collects the OpenID realm attribute flags shared by
// `openid add` and `openid update`. Every field maps directly onto a
// CreateAccessOpenidParams / UpdateAccessOpenidParams field of the same name.
//
// usernameClaim is create-only: UpdateAccessOpenidParams has no
// username-claim field (PDM does not allow changing it once the realm
// exists), so registerUpdate does not bind it.
type realmOpenidFlags struct {
	acrValues     string
	audiences     string
	autocreate    bool
	clientId      string
	clientKey     string
	comment       string
	isDefault     bool
	issuerUrl     string
	prompt        string
	scopes        string
	usernameClaim string

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `openid add` and
// `openid update`, except client-id and issuer-url (required on add,
// optional on update — bound separately by each caller).
func (of *realmOpenidFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&of.acrValues, "acr-values", "", "space-separated OpenID ACR values list")
	f.StringVar(&of.audiences, "audiences", "", "comma-separated list of additional allowed OpenID audiences")
	f.BoolVar(&of.autocreate, "autocreate", false, "automatically create users on first login if they do not exist")
	f.StringVar(&of.clientKey, "client-key", "", "OpenID client secret")
	f.StringVar(&of.comment, "comment", "", "comment")
	f.BoolVar(&of.isDefault, "default", false, "select this realm by default on login")
	f.StringVar(&of.prompt, "prompt", "", "OpenID prompt parameter")
	f.StringVar(&of.scopes, "scopes", "", "space-separated OpenID scopes list")
}

// registerUpdate binds every flag `openid update` accepts, including the
// update-only delete/digest fields and the optional client-id/issuer-url.
func (of *realmOpenidFlags) registerUpdate(cmd *cobra.Command) {
	of.registerCommon(cmd)
	f := cmd.Flags()
	f.StringVar(&of.clientId, "client-id", "", "OpenID client ID")
	f.StringVar(&of.issuerUrl, "issuer-url", "", "OpenID issuer URL")
	f.StringArrayVar(&of.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&of.digest, "digest", "", "only update if the current config digest matches")
}

// applyCreate builds the create payload, forwarding optional flags only when set.
func (of *realmOpenidFlags) applyCreate(cmd *cobra.Command, p *pdmconfig.CreateAccessOpenidParams) {
	fl := cmd.Flags()
	if fl.Changed("acr-values") {
		p.AcrValues = &of.acrValues
	}
	if fl.Changed("audiences") {
		p.Audiences = &of.audiences
	}
	if fl.Changed("autocreate") {
		p.Autocreate = &of.autocreate
	}
	if fl.Changed("client-key") {
		p.ClientKey = &of.clientKey
	}
	if fl.Changed("comment") {
		p.Comment = &of.comment
	}
	if fl.Changed("default") {
		p.Default = &of.isDefault
	}
	if fl.Changed("prompt") {
		p.Prompt = &of.prompt
	}
	if fl.Changed("scopes") {
		p.Scopes = &of.scopes
	}
	if fl.Changed("username-claim") {
		p.UsernameClaim = &of.usernameClaim
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (of *realmOpenidFlags) applyUpdate(cmd *cobra.Command, p *pdmconfig.UpdateAccessOpenidParams) {
	fl := cmd.Flags()
	if fl.Changed("acr-values") {
		p.AcrValues = &of.acrValues
	}
	if fl.Changed("audiences") {
		p.Audiences = &of.audiences
	}
	if fl.Changed("autocreate") {
		p.Autocreate = &of.autocreate
	}
	if fl.Changed("client-id") {
		p.ClientId = &of.clientId
	}
	if fl.Changed("client-key") {
		p.ClientKey = &of.clientKey
	}
	if fl.Changed("comment") {
		p.Comment = &of.comment
	}
	if fl.Changed("default") {
		p.Default = &of.isDefault
	}
	if fl.Changed("issuer-url") {
		p.IssuerUrl = &of.issuerUrl
	}
	if fl.Changed("prompt") {
		p.Prompt = &of.prompt
	}
	if fl.Changed("scopes") {
		p.Scopes = &of.scopes
	}
	if fl.Changed("delete") {
		p.Delete = of.del
	}
	if fl.Changed("digest") {
		p.Digest = &of.digest
	}
}

// newRealmOpenidAddCmd builds `pmx pdm realm openid add <realm>` — create an
// OpenID realm configuration (POST /config/access/openid). --client-id and
// --issuer-url are required; every other option is optional and only
// forwarded when explicitly set.
func newRealmOpenidAddCmd() *cobra.Command {
	var of realmOpenidFlags
	cmd := &cobra.Command{
		Use:   "add <realm>",
		Short: "Create an OpenID Connect realm configuration",
		Long: "Create a new OpenID Connect authentication realm configuration " +
			"(POST /config/access/openid). --client-id and --issuer-url are " +
			"required; every other option is optional and only forwarded when " +
			"explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if of.clientId == "" {
				return fmt.Errorf("--client-id is required")
			}

			if of.issuerUrl == "" {
				return fmt.Errorf("--issuer-url is required")
			}

			params := &pdmconfig.CreateAccessOpenidParams{
				Realm:     realm,
				ClientId:  of.clientId,
				IssuerUrl: of.issuerUrl,
			}
			of.applyCreate(cmd, params)

			err := deps.PDM.Config.CreateAccessOpenid(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create OpenID realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("OpenID realm %q created.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	of.registerCommon(cmd)
	f := cmd.Flags()
	f.StringVar(&of.clientId, "client-id", "", "OpenID client ID (required)")
	f.StringVar(&of.issuerUrl, "issuer-url", "", "OpenID issuer URL (required)")
	f.StringVar(&of.usernameClaim, "username-claim", "",
		"claim used as the unique user name (immutable after realm creation)")
	cli.MustMarkRequired(cmd, "client-id")
	cli.MustMarkRequired(cmd, "issuer-url")
	return cmd
}

// newRealmOpenidUpdateCmd builds `pmx pdm realm openid update <realm>` —
// update an OpenID realm configuration (PUT /config/access/openid/{realm}).
// Only flags explicitly set are sent; use --delete to reset properties to
// their default. --username-claim has no update flag: PDM does not allow
// changing it after realm creation.
func newRealmOpenidUpdateCmd() *cobra.Command {
	var of realmOpenidFlags
	cmd := &cobra.Command{
		Use:   "update <realm>",
		Short: "Update an OpenID Connect realm configuration",
		Long: "Update an existing OpenID Connect realm configuration (PUT " +
			"/config/access/openid/{realm}). Only flags explicitly set are sent; " +
			"use --delete to reset properties to their default instead. " +
			"username-claim cannot be changed after realm creation.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update OpenID realm %q: no changes requested: pass at least one flag", realm)
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range of.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pdmconfig.UpdateAccessOpenidParams{}
			of.applyUpdate(cmd, params)

			// UpdateAccessOpenid returns only an error (config_gen.go:76,
			// v3.6.0), unlike the PBS analog's UpdateAccessOpenid, which also
			// returns the updated realm object.
			err := deps.PDM.Config.UpdateAccessOpenid(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("update OpenID realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("OpenID realm %q updated.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	of.registerUpdate(cmd)
	return cmd
}

// newRealmOpenidDeleteCmd builds `pmx pdm realm openid delete <realm>` —
// remove an OpenID realm configuration (DELETE /config/access/openid/{realm}).
func newRealmOpenidDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <realm>",
		Short: "Delete an OpenID Connect realm configuration",
		Long: "Remove an OpenID Connect realm configuration (DELETE /config/access/openid/{realm}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			realm := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete OpenID realm %q without confirmation: pass --yes/-y", realm)
			}

			params := &pdmconfig.DeleteAccessOpenidParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Config.DeleteAccessOpenid(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("delete OpenID realm %q: %w", realm, err)
			}

			res := output.Result{Message: fmt.Sprintf("OpenID realm %q deleted.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
