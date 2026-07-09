package pdm

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// acmePluginSecretKeys are the ACME challenge-plugin fields that must never
// be echoed back to the user. "data" is the plugin's DNS API configuration
// blob (base64-encoded), which in practice carries the DNS provider's API
// token/credential; the PDM API's GET /config/acme/plugins{,/{id}} schema
// declares it fully readable (pdm-apidoc.json, verified 2026-07-08: no
// write-only annotation) and GetAcmePluginsResponse.Data (config_gen.go:1739,
// v3.6.0) is a populated *string, so the server does echo it back — unlike
// remote.go's `token` (remote.go:31-34), stripping it here is a defensive
// choice rather than compensating for a value the API always withholds.
//
// This intentionally diverges from the pbs acme.go precedent (internal/cli/
// pbs/acme.go:341-358), which keeps "data" in show/Raw output and only
// excludes it from the `plugin ls` table columns for readability. Given this
// project's standing rule to never echo secret material (see CONVENTIONS),
// and remote.go's established precedent of stripping credential fields the
// API does return, PDM's acme plugin commands strip "data" everywhere
// (ls/show, Single and Raw) rather than following PBS's narrower carve-out.
var acmePluginSecretKeys = []string{"data"}

// stripAcmePluginSecrets deletes every key in acmePluginSecretKeys from
// fields, in place.
func stripAcmePluginSecrets(fields map[string]any) {
	for _, k := range acmePluginSecretKeys {
		delete(fields, k)
	}
}

// newConfigAcmeCmd builds `pmx pdm config acme` — manage ACME accounts and
// challenge plugins used to issue this Proxmox Datacenter Manager's own TLS
// certificate (/config/acme/account and /config/acme/plugins CRUD), and read
// the read-only ACME reference data PDM ships (challenge-plugin schema,
// known directory endpoints, and a directory's Terms of Service URL).
func newConfigAcmeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acme",
		Short: "Manage ACME accounts and challenge plugins",
		Long: "List, inspect, create, update, and delete ACME accounts and challenge " +
			"plugins used to automatically request and renew this instance's own TLS " +
			"certificate, and read ACME reference data: the challenge-plugin schema, " +
			"known directory endpoints, and a directory's Terms of Service URL.",
	}
	cmd.AddCommand(
		newConfigAcmeAccountCmd(),
		newConfigAcmePluginCmd(),
		newConfigAcmeDirectoriesCmd(),
		newConfigAcmeChallengeSchemaCmd(),
		newConfigAcmeTosCmd(),
	)
	return cmd
}

// ===========================================================================
// account
// ===========================================================================

// newConfigAcmeAccountCmd builds `pmx pdm config acme account` — manage ACME
// accounts (/config/acme/account CRUD). Register/update/deactivate all
// return a UPID (config_gen.go: CreateAcmeAccountResponse/
// UpdateAcmeAccountResponse/DeleteAcmeAccountResponse are each a
// `= json.RawMessage` alias, config_gen.go:1384,1435,1527, v3.6.0; the PDM
// API schema's returns.pattern for POST/PUT/DELETE
// /config/acme/account{,/{name}} is the UPID regex), so all three run as
// asynchronous tasks — unlike PBS, whose equivalent endpoints perform the
// ACME provider round-trip synchronously and return null (pbs/acme.go:52-56)
// — matching PVE cluster's own ACME account endpoints
// (internal/cli/cluster/acme.go:158-162, which treats the identical
// RawMessage shape as a UPID via finishAcmeAsync). PDM's own appliance runs
// the same pvedaemon worker-task mechanism as PVE, so it queues account
// operations the same way.
func newConfigAcmeAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage ACME accounts",
		Long: "List, inspect, register, update, and deactivate ACME accounts " +
			"(/config/acme/account). Register/update/deactivate run as asynchronous " +
			"tasks; the command blocks until each finishes unless --async is set.",
	}
	cmd.AddCommand(
		newConfigAcmeAccountLsCmd(),
		newConfigAcmeAccountShowCmd(),
		newConfigAcmeAccountAddCmd(),
		newConfigAcmeAccountUpdateCmd(),
		newConfigAcmeAccountDeleteCmd(),
	)
	return cmd
}

// acmeAccountListEntry is the decoded shape of one element returned by
// `config acme account ls` (GET /config/acme/account). Per the PDM API
// schema this entry currently only carries the account's name.
type acmeAccountListEntry struct {
	Name string `json:"name"`
}

// newConfigAcmeAccountLsCmd builds `pmx pdm config acme account ls` — list
// every registered ACME account (GET /config/acme/account).
func newConfigAcmeAccountLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACME accounts",
		Long:  "List every ACME account registered on this Proxmox Datacenter Manager (GET /config/acme/account).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAcmeAccount(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme accounts: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[acmeAccountListEntry](items, "acme account")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"NAME"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.Entry.Name})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigAcmeAccountShowCmd builds `pmx pdm config acme account show
// <name>` — show one ACME account's provider data (GET
// /config/acme/account/{name}). The "account" field is the ACME provider's
// own dynamic account object (its shape is defined by the provider, not
// PDM), and is rendered losslessly (nested, unflattened) in JSON/YAML output.
func newConfigAcmeAccountShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one ACME account's provider data",
		Long: "Show the directory URL, account location, agreed Terms of Service, and " +
			"the ACME provider's own account data for one registered account " +
			"(GET /config/acme/account/{name}).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			resp, err := deps.PDM.Config.GetAcmeAccount(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show acme account %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode acme account %q: %w", name, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigAcmeAccountAddCmd builds `pmx pdm config acme account add <name>`
// — register a new ACME account (POST /config/acme/account). Runs as an
// asynchronous task (see newConfigAcmeAccountCmd's doc comment).
func newConfigAcmeAccountAddCmd() *cobra.Command {
	var (
		contact    string
		directory  string
		eabKid     string
		eabHmacKey string
		tosUrl     string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register an ACME account",
		Long: "Register a new ACME account with a certificate authority (POST " +
			"/config/acme/account). --contact is required (comma-separated list of " +
			"email addresses); every other flag is optional and only forwarded when " +
			"explicitly set. Pass --tos-url to indicate agreement with the CA's " +
			"Terms of Service (see 'pmx pdm config acme tos show'). Runs as an " +
			"asynchronous task; the command blocks until it finishes unless --async " +
			"(persistent flag) is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			params := &pdmconfig.CreateAcmeAccountParams{
				Name:    strPtr(name),
				Contact: contact,
			}

			fl := cmd.Flags()
			if fl.Changed("directory") {
				params.Directory = strPtr(directory)
			}
			if fl.Changed("eab-kid") {
				params.EabKid = strPtr(eabKid)
			}
			if fl.Changed("eab-hmac-key") {
				params.EabHmacKey = strPtr(eabHmacKey)
			}
			if fl.Changed("tos-url") {
				params.TosUrl = strPtr(tosUrl)
			}

			resp, err := deps.PDM.Config.CreateAcmeAccount(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("register acme account %q: %w", name, err)
			}
			if resp == nil {
				return fmt.Errorf("register acme account %q: nil response from PDM", name)
			}

			return finishAsync(cmd, deps, json.RawMessage(*resp), fmt.Sprintf("ACME account %q registered.", name))
		},
	}
	f := cmd.Flags()
	f.StringVar(&contact, "contact", "", "comma-separated list of contact email addresses (required)")
	f.StringVar(&directory, "directory", "", "URL of the ACME directory to register with (default: Let's Encrypt)")
	f.StringVar(&eabKid, "eab-kid", "", "key identifier for External Account Binding")
	f.StringVar(&eabHmacKey, "eab-hmac-key", "", "HMAC key for External Account Binding")
	f.StringVar(&tosUrl, "tos-url", "", "CA Terms of Service URL — setting this indicates agreement")
	cli.MustMarkRequired(cmd, "contact")
	return cmd
}

// newConfigAcmeAccountUpdateCmd builds `pmx pdm config acme account update
// <name>` — update an ACME account's contact addresses (PUT
// /config/acme/account/{name}). Runs as an asynchronous task (see
// newConfigAcmeAccountCmd's doc comment).
func newConfigAcmeAccountUpdateCmd() *cobra.Command {
	var contact string
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an ACME account's contact addresses",
		Long: "Update the contact email addresses registered with an ACME account (PUT " +
			"/config/acme/account/{name}). Runs as an asynchronous task; the command " +
			"blocks until it finishes unless --async (persistent flag) is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update acme account %q: no changes requested: pass --contact", name)
			}

			params := &pdmconfig.UpdateAcmeAccountParams{}
			if cmd.Flags().Changed("contact") {
				params.Contact = strPtr(contact)
			}

			resp, err := deps.PDM.Config.UpdateAcmeAccount(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update acme account %q: %w", name, err)
			}
			if resp == nil {
				return fmt.Errorf("update acme account %q: nil response from PDM", name)
			}

			return finishAsync(cmd, deps, json.RawMessage(*resp), fmt.Sprintf("ACME account %q updated.", name))
		},
	}
	cmd.Flags().StringVar(&contact, "contact", "", "comma-separated list of contact email addresses")
	return cmd
}

// newConfigAcmeAccountDeleteCmd builds `pmx pdm config acme account delete
// <name>` — deactivate an ACME account (DELETE
// /config/acme/account/{name}). Runs as an asynchronous task (see
// newConfigAcmeAccountCmd's doc comment).
func newConfigAcmeAccountDeleteCmd() *cobra.Command {
	var (
		force bool
		yes   bool
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Deactivate an ACME account",
		Long: "Deactivate an ACME account with its provider and remove its local " +
			"configuration (DELETE /config/acme/account/{name}). --force removes the " +
			"local configuration even if the provider refuses the deactivation " +
			"request. Runs as an asynchronous task; the command blocks until it " +
			"finishes unless --async (persistent flag) is set. This is destructive: " +
			"pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !yes {
				return fmt.Errorf("refusing to deactivate acme account %q without confirmation: pass --yes/-y", name)
			}

			params := &pdmconfig.DeleteAcmeAccountParams{}
			if cmd.Flags().Changed("force") {
				params.Force = boolPtr(force)
			}

			resp, err := deps.PDM.Config.DeleteAcmeAccount(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("deactivate acme account %q: %w", name, err)
			}
			if resp == nil {
				return fmt.Errorf("deactivate acme account %q: nil response from PDM", name)
			}

			return finishAsync(cmd, deps, json.RawMessage(*resp), fmt.Sprintf("ACME account %q deactivated.", name))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove local account data even if the provider refuses deactivation")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ===========================================================================
// plugin
// ===========================================================================

// newConfigAcmePluginCmd builds `pmx pdm config acme plugin` — manage ACME
// DNS challenge plugins (/config/acme/plugins CRUD). Create/update/delete
// all return null on the wire (config_gen.go:1687-1823, v3.6.0 —
// CreateAcmePlugins/UpdateAcmePlugins/DeleteAcmePlugins each return only
// `error`, no RawMessage), matching the PDM API schema's returns.type
// "null" for POST/PUT/DELETE /config/acme/plugins{,/{id}}: plugin
// definitions are stored locally and never contact the ACME provider, so
// these run synchronously.
func newConfigAcmePluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage ACME challenge plugins",
		Long: "List, inspect, create, update, and delete ACME DNS challenge-plugin " +
			"configurations (/config/acme/plugins).",
	}
	cmd.AddCommand(
		newConfigAcmePluginLsCmd(),
		newConfigAcmePluginShowCmd(),
		newConfigAcmePluginAddCmd(),
		newConfigAcmePluginUpdateCmd(),
		newConfigAcmePluginDeleteCmd(),
	)
	return cmd
}

// acmePluginEntry is the decoded shape of one element returned by
// `config acme plugin ls` (GET /config/acme/plugins), and (with its ID
// under "plugin" instead of "id") the shape of the single object returned
// by `config acme plugin show` (GET /config/acme/plugins/{id}). Both
// endpoints declare the identical object schema in pdm-apidoc.json, so this
// mirrors GetAcmePluginsResponse's tolerant field types (config_gen.go:1735,
// v3.6.0: Disable *client.PVEBool, ValidationDelay *client.PVEInt) rather
// than plain *bool/*int64, in case list entries share the same PVE-style
// 0/1 wire encoding as the singleton get.
type acmePluginEntry struct {
	Api             *string            `json:"api,omitempty"`
	Data            *string            `json:"data,omitempty"`
	Disable         *pveclient.PVEBool `json:"disable,omitempty"`
	Plugin          string             `json:"plugin"`
	Type            string             `json:"type"`
	ValidationDelay *pveclient.PVEInt  `json:"validation-delay,omitempty"`
}

// newConfigAcmePluginLsCmd builds `pmx pdm config acme plugin ls` — list
// every configured ACME challenge plugin (GET /config/acme/plugins). The
// plugin's DNS API configuration data is stripped from both the table and
// Raw output (see acmePluginSecretKeys).
func newConfigAcmePluginLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACME challenge plugins",
		Long:  "List every configured ACME challenge plugin (GET /config/acme/plugins).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAcmePlugins(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme plugins: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[acmePluginEntry](items, "acme plugin")
			if err != nil {
				return err
			}
			for i := range table {
				stripAcmePluginSecrets(table[i].Raw)
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Plugin < table[j].Entry.Plugin })

			headers := []string{"PLUGIN", "TYPE", "API", "DISABLE", "VALIDATION-DELAY"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Plugin, e.Type, strPtrString(e.Api), pveBoolPtrString(e.Disable), pveIntPtrString(e.ValidationDelay),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigAcmePluginShowCmd builds `pmx pdm config acme plugin show <id>` —
// show one ACME challenge plugin's full configuration (GET
// /config/acme/plugins/{id}). The plugin's DNS API configuration data is
// stripped from both Single and Raw output (see acmePluginSecretKeys).
func newConfigAcmePluginShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one ACME challenge plugin's configuration",
		Long: "Show the configuration of one ACME challenge plugin " +
			"(GET /config/acme/plugins/{id}). The DNS API configuration data is " +
			"credential material and is never rendered.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			resp, err := deps.PDM.Config.GetAcmePlugins(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("show acme plugin %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode acme plugin %q: %w", id, err)
			}
			stripAcmePluginSecrets(fields)

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// acmePluginArgs holds the flag values shared by `plugin add` and
// `plugin update`, covering every field CreateAcmePluginsParams and
// UpdateAcmePluginsParams share (create additionally requires --type, which
// cannot be changed after creation).
type acmePluginArgs struct {
	api             string
	data            string
	disable         bool
	validationDelay int64
}

// registerAcmePluginArgs binds the plugin attribute flags shared by add and
// update onto cmd.
func registerAcmePluginArgs(cmd *cobra.Command, a *acmePluginArgs) {
	f := cmd.Flags()
	f.StringVar(&a.api, "api", "", "DNS API plugin ID")
	f.StringVar(&a.data, "data", "", "DNS plugin configuration data, base64-encoded with padding")
	f.BoolVar(&a.disable, "disable", false, "disable this plugin configuration")
	f.Int64Var(&a.validationDelay, "validation-delay", 0,
		"extra seconds to wait before requesting validation, to cope with long DNS record TTLs")
}

// newConfigAcmePluginAddCmd builds `pmx pdm config acme plugin add <id>` —
// create an ACME challenge plugin configuration (POST /config/acme/plugins).
func newConfigAcmePluginAddCmd() *cobra.Command {
	var (
		a        acmePluginArgs
		pluginTy string
	)
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create an ACME challenge plugin",
		Long: "Create a new ACME DNS challenge-plugin configuration (POST " +
			"/config/acme/plugins). --type, --api, and --data are required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			params := &pdmconfig.CreateAcmePluginsParams{
				Id:   id,
				Type: pluginTy,
				Api:  a.api,
				Data: a.data,
			}

			fl := cmd.Flags()
			if fl.Changed("disable") {
				params.Disable = boolPtr(a.disable)
			}
			if fl.Changed("validation-delay") {
				params.ValidationDelay = int64Ptr(a.validationDelay)
			}

			err := deps.PDM.Config.CreateAcmePlugins(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create acme plugin %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("ACME plugin %q created.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&pluginTy, "type", "", "ACME challenge plugin type, e.g. 'dns' (required)")
	registerAcmePluginArgs(cmd, &a)
	cli.MustMarkRequired(cmd, "type")
	cli.MustMarkRequired(cmd, "api")
	cli.MustMarkRequired(cmd, "data")
	return cmd
}

// newConfigAcmePluginUpdateCmd builds `pmx pdm config acme plugin update
// <id>` — update an ACME challenge plugin configuration (PUT
// /config/acme/plugins/{id}).
func newConfigAcmePluginUpdateCmd() *cobra.Command {
	var (
		a      acmePluginArgs
		digest string
		del    []string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an ACME challenge plugin",
		Long: "Update an existing ACME DNS challenge-plugin configuration (PUT " +
			"/config/acme/plugins/{id}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead. The plugin's " +
			"--type cannot be changed after creation.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update acme plugin %q: no changes requested: pass at least one flag", id)
			}

			if fl.Changed("delete") {
				for _, propName := range del {
					if propName == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pdmconfig.UpdateAcmePluginsParams{}
			if fl.Changed("api") {
				params.Api = strPtr(a.api)
			}
			if fl.Changed("data") {
				params.Data = strPtr(a.data)
			}
			if fl.Changed("disable") {
				params.Disable = boolPtr(a.disable)
			}
			if fl.Changed("validation-delay") {
				params.ValidationDelay = int64Ptr(a.validationDelay)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}

			err := deps.PDM.Config.UpdateAcmePlugins(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update acme plugin %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("ACME plugin %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerAcmePluginArgs(cmd, &a)
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	return cmd
}

// newConfigAcmePluginDeleteCmd builds `pmx pdm config acme plugin delete
// <id>` — remove an ACME challenge plugin configuration (DELETE
// /config/acme/plugins/{id}). This binding takes no request parameters at
// all (no digest guard).
func newConfigAcmePluginDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an ACME challenge plugin",
		Long: "Remove an ACME DNS challenge-plugin configuration (DELETE " +
			"/config/acme/plugins/{id}). This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete acme plugin %q without confirmation: pass --yes/-y", id)
			}

			err := deps.PDM.Config.DeleteAcmePlugins(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("delete acme plugin %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("ACME plugin %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ===========================================================================
// directories
// ===========================================================================

// newConfigAcmeDirectoriesCmd builds `pmx pdm config acme directories` —
// list the known ACME directory endpoints PDM ships (GET
// /config/acme/directories).
func newConfigAcmeDirectoriesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "directories",
		Short: "Show known ACME directory endpoints",
		Long:  "List the named ACME directory endpoints PDM ships, for use with 'config acme account add --directory'.",
	}
	cmd.AddCommand(newConfigAcmeDirectoriesLsCmd())
	return cmd
}

// acmeDirectoryEntry is the decoded shape of one element returned by
// `config acme directories ls` (GET /config/acme/directories).
type acmeDirectoryEntry struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

// newConfigAcmeDirectoriesLsCmd builds `pmx pdm config acme directories ls`
// — list the known ACME directory endpoints (GET /config/acme/directories).
func newConfigAcmeDirectoriesLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List known ACME directory endpoints",
		Long:  "List every named ACME directory endpoint PDM ships (GET /config/acme/directories).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAcmeDirectories(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme directories: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[acmeDirectoryEntry](items, "acme directory")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"NAME", "URL"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.Entry.Name, t.Entry.Url})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// ===========================================================================
// challenge-schema
// ===========================================================================

// newConfigAcmeChallengeSchemaCmd builds `pmx pdm config acme
// challenge-schema` — read the parameter schema for every known ACME
// challenge-plugin type (GET /config/acme/challenge-schema).
func newConfigAcmeChallengeSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "challenge-schema",
		Short: "Show ACME challenge-plugin parameter schemas",
		Long:  "Read the parameter schema for every known ACME challenge-plugin type.",
	}
	cmd.AddCommand(newConfigAcmeChallengeSchemaLsCmd())
	return cmd
}

// acmeChallengeSchemaEntry is the decoded shape of one element returned by
// `config acme challenge-schema ls` (GET /config/acme/challenge-schema). The
// per-plugin parameter schema is dynamic (its shape depends on the plugin
// type), so it is kept as raw JSON rather than flattened, and excluded from
// the table but preserved losslessly in JSON/YAML output.
type acmeChallengeSchemaEntry struct {
	Id     string          `json:"id"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Type   string          `json:"type"`
}

// newConfigAcmeChallengeSchemaLsCmd builds `pmx pdm config acme
// challenge-schema ls` — list the parameter schema for every known ACME
// challenge-plugin type (GET /config/acme/challenge-schema).
func newConfigAcmeChallengeSchemaLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACME challenge-plugin schemas",
		Long: "List every known ACME challenge-plugin type together with its parameter " +
			"schema (GET /config/acme/challenge-schema).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAcmeChallengeSchema(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme challenge schemas: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[acmeChallengeSchemaEntry](items, "acme challenge schema")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Id < table[j].Entry.Id })

			headers := []string{"ID", "NAME", "TYPE"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{e.Id, e.Name, e.Type})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// ===========================================================================
// tos
// ===========================================================================

// newConfigAcmeTosCmd builds `pmx pdm config acme tos` — read an ACME
// directory's Terms of Service URL (GET /config/acme/tos).
func newConfigAcmeTosCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tos",
		Short: "Show an ACME directory's Terms of Service URL",
		Long:  "Read the Terms of Service URL for an ACME directory, for use with 'config acme account add --tos-url'.",
	}
	cmd.AddCommand(newConfigAcmeTosShowCmd())
	return cmd
}

// newConfigAcmeTosShowCmd builds `pmx pdm config acme tos show` — show the
// Terms of Service URL for an ACME directory (GET /config/acme/tos). Some
// directories have no Terms of Service; in that case PDM returns no data,
// and this renders a message rather than an error (ListAcmeTosResponse is a
// `= json.RawMessage` alias, config_gen.go:1832, v3.6.0, and the PDM API
// schema marks the string return "optional": 1).
func newConfigAcmeTosShowCmd() *cobra.Command {
	var directory string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show an ACME directory's Terms of Service URL",
		Long: "Get the Terms of Service URL for an ACME directory (GET /config/acme/tos). " +
			"Without --directory, PDM returns the ToS for its default ACME directory " +
			"(Let's Encrypt).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmconfig.ListAcmeTosParams{}
			if cmd.Flags().Changed("directory") {
				params.Directory = strPtr(directory)
			}

			resp, err := deps.PDM.Config.ListAcmeTos(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get acme terms of service url: %w", err)
			}

			if resp == nil || len(*resp) == 0 {
				res := output.Result{Message: "No Terms of Service URL is published for this ACME directory."}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			tos, err := decodeRawString(*resp)
			if err != nil {
				return fmt.Errorf("decode acme terms of service url: %w", err)
			}

			res := output.Result{
				Single:  map[string]string{"tos-url": tos},
				Raw:     map[string]string{"tos-url": tos},
				Message: tos,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&directory, "directory", "", "ACME directory URL to query (default: PDM's configured default directory)")
	return cmd
}
