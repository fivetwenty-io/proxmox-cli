package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newAcmeCmd builds `pve pbs acme` — manage ACME accounts and challenge
// plugins used to automatically request and renew TLS certificates
// (/config/acme/account and /config/acme/plugins CRUD), and read the
// read-only ACME reference data PBS ships (challenge-plugin schema, known
// directory endpoints, and a directory's Terms of Service URL).
//
// GET /config/acme (Config.ListAcme) is not exposed as a command: per the PBS
// API schema it is a directory-index endpoint whose declared return type is
// "null" — it carries no data of its own, only routing to the /account,
// /plugins, /challenge-schema, /directories, and /tos children below it.
// Rendering it as a data-bearing command would misrepresent an empty
// response as a real result.
func newAcmeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acme",
		Short: "Manage ACME accounts and challenge plugins",
		Long: "List, inspect, create, update, and delete ACME accounts and challenge " +
			"plugins used to automatically request and renew TLS certificates, and " +
			"read ACME reference data: the challenge-plugin schema, known directory " +
			"endpoints, and a directory's Terms of Service URL.",
	}
	cmd.AddCommand(
		newAcmeAccountCmd(),
		newAcmePluginCmd(),
		newAcmeChallengeSchemaCmd(),
		newAcmeDirectoriesCmd(),
		newAcmeTosCmd(),
	)
	return cmd
}

// ===========================================================================
// account
// ===========================================================================

// newAcmeAccountCmd builds `pve pbs acme account` — manage ACME accounts
// (/config/acme/account CRUD). Account create/update/delete return null on
// the wire: unlike PVE, where the equivalent endpoints queue a background
// task, PBS performs the ACME provider round-trip synchronously and returns
// once it completes, so these verbs render a plain success message rather
// than waiting on a UPID.
func newAcmeAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage ACME accounts",
		Long: "List, inspect, register, update, and deactivate ACME accounts " +
			"(/config/acme/account).",
	}
	cmd.AddCommand(
		newAcmeAccountLsCmd(),
		newAcmeAccountShowCmd(),
		newAcmeAccountAddCmd(),
		newAcmeAccountUpdateCmd(),
		newAcmeAccountDeleteCmd(),
	)
	return cmd
}

// acmeAccountListEntry is the decoded shape of one element returned by
// `acme account ls` (GET /config/acme/account). Per the PBS API schema this
// entry currently only carries the account's name.
type acmeAccountListEntry struct {
	Name string `json:"name"`
}

// newAcmeAccountLsCmd builds `pve pbs acme account ls` — list every
// registered ACME account (GET /config/acme/account).
func newAcmeAccountLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACME accounts",
		Long:  "List every ACME account registered on this Proxmox Backup Server (GET /config/acme/account).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListAcmeAccount(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme accounts: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]acmeAccountListEntry, 0, len(items))

			for _, raw := range items {
				var e acmeAccountListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode acme account entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{e.Name})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newAcmeAccountShowCmd builds `pve pbs acme account show <name>` — show one
// ACME account's provider data (GET /config/acme/account/{name}). The
// "account" field is the ACME provider's own dynamic account object (its
// shape is defined by the provider, not PBS), and is rendered losslessly
// (nested, unflattened) in JSON/YAML output.
func newAcmeAccountShowCmd() *cobra.Command {
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
			if name == "" {
				return fmt.Errorf("account name must not be empty")
			}

			resp, err := deps.PBS.Config.GetAcmeAccount(cmd.Context(), name)
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

// newAcmeAccountAddCmd builds `pve pbs acme account add <name>` — register a
// new ACME account (POST /config/acme/account).
func newAcmeAccountAddCmd() *cobra.Command {
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
			"Terms of Service (see 'pve pbs acme tos show').",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("account name must not be empty")
			}

			if contact == "" {
				return fmt.Errorf("--contact is required")
			}

			params := &pbsconfig.CreateAcmeAccountParams{
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

			err := deps.PBS.Config.CreateAcmeAccount(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("register acme account %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("ACME account %q registered.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
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

// newAcmeAccountUpdateCmd builds `pve pbs acme account update <name>` —
// update an ACME account's contact addresses (PUT /config/acme/account/{name}).
func newAcmeAccountUpdateCmd() *cobra.Command {
	var contact string
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an ACME account's contact addresses",
		Long: "Update the contact email addresses registered with an ACME account (PUT " +
			"/config/acme/account/{name}).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("account name must not be empty")
			}

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update acme account %q: no changes given: pass --contact", name)
			}

			params := &pbsconfig.UpdateAcmeAccountParams{}
			if fl.Changed("contact") {
				params.Contact = strPtr(contact)
			}

			err := deps.PBS.Config.UpdateAcmeAccount(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update acme account %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("ACME account %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&contact, "contact", "", "comma-separated list of contact email addresses")
	return cmd
}

// newAcmeAccountDeleteCmd builds `pve pbs acme account delete <name>` —
// deactivate an ACME account (DELETE /config/acme/account/{name}).
func newAcmeAccountDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Deactivate an ACME account",
		Long: "Deactivate an ACME account with its provider and remove its local " +
			"configuration (DELETE /config/acme/account/{name}). --force removes the " +
			"local configuration even if the provider refuses the deactivation request.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("account name must not be empty")
			}

			params := &pbsconfig.DeleteAcmeAccountParams{}
			if cmd.Flags().Changed("force") {
				params.Force = boolPtr(force)
			}

			err := deps.PBS.Config.DeleteAcmeAccount(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("deactivate acme account %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("ACME account %q deactivated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove local account data even if the provider refuses deactivation")
	return cmd
}

// ===========================================================================
// plugin
// ===========================================================================

// newAcmePluginCmd builds `pve pbs acme plugin` — manage ACME DNS challenge
// plugins (/config/acme/plugins CRUD).
func newAcmePluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage ACME challenge plugins",
		Long: "List, inspect, create, update, and delete ACME DNS challenge-plugin " +
			"configurations (/config/acme/plugins).",
	}
	cmd.AddCommand(
		newAcmePluginLsCmd(),
		newAcmePluginShowCmd(),
		newAcmePluginAddCmd(),
		newAcmePluginUpdateCmd(),
		newAcmePluginDeleteCmd(),
	)
	return cmd
}

// acmePluginEntry is the decoded shape of one element returned by
// `acme plugin ls` (GET /config/acme/plugins), and (with its ID under
// "plugin" instead of "id") the shape of the single object returned by
// `acme plugin show` (GET /config/acme/plugins/{id}).
type acmePluginEntry struct {
	Api             *string `json:"api,omitempty"`
	Data            *string `json:"data,omitempty"`
	Disable         *bool   `json:"disable,omitempty"`
	Plugin          string  `json:"plugin"`
	Type            string  `json:"type"`
	ValidationDelay *int64  `json:"validation-delay,omitempty"`
}

// newAcmePluginLsCmd builds `pve pbs acme plugin ls` — list every configured
// ACME challenge plugin (GET /config/acme/plugins). The plugin's DNS API
// configuration data (potentially large and provider-credential-bearing) is
// intentionally excluded from the table; it remains available via
// `acme plugin show` and in JSON/YAML output.
func newAcmePluginLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACME challenge plugins",
		Long:  "List every configured ACME challenge plugin (GET /config/acme/plugins).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListAcmePlugins(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme plugins: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]acmePluginEntry, 0, len(items))

			for _, raw := range items {
				var e acmePluginEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode acme plugin entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Plugin < entries[j].Plugin })

			headers := []string{"PLUGIN", "TYPE", "API", "DISABLE", "VALIDATION-DELAY"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Plugin,
					e.Type,
					pbsFormatOptionalString(e.Api),
					metricsFormatOptionalBool(e.Disable),
					pbsFormatOptionalInt64(e.ValidationDelay),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newAcmePluginShowCmd builds `pve pbs acme plugin show <id>` — show one
// ACME challenge plugin's full configuration (GET /config/acme/plugins/{id}).
func newAcmePluginShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one ACME challenge plugin's configuration",
		Long: "Show the full configuration of one ACME challenge plugin " +
			"(GET /config/acme/plugins/{id}).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("plugin id must not be empty")
			}

			resp, err := deps.PBS.Config.GetAcmePlugins(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("show acme plugin %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode acme plugin %q: %w", id, err)
			}

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

// newAcmePluginAddCmd builds `pve pbs acme plugin add <id>` — create an ACME
// challenge plugin configuration (POST /config/acme/plugins).
func newAcmePluginAddCmd() *cobra.Command {
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
			if id == "" {
				return fmt.Errorf("plugin id must not be empty")
			}

			if pluginTy == "" {
				return fmt.Errorf("--type is required")
			}

			if a.api == "" {
				return fmt.Errorf("--api is required")
			}

			if a.data == "" {
				return fmt.Errorf("--data is required")
			}

			params := &pbsconfig.CreateAcmePluginsParams{
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

			err := deps.PBS.Config.CreateAcmePlugins(cmd.Context(), params)
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

// newAcmePluginUpdateCmd builds `pve pbs acme plugin update <id>` — update an
// ACME challenge plugin configuration (PUT /config/acme/plugins/{id}).
func newAcmePluginUpdateCmd() *cobra.Command {
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
			if id == "" {
				return fmt.Errorf("plugin id must not be empty")
			}

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update acme plugin %q: no changes given: pass at least one flag", id)
			}

			if fl.Changed("delete") {
				for _, propName := range del {
					if propName == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateAcmePluginsParams{}

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

			err := deps.PBS.Config.UpdateAcmePlugins(cmd.Context(), id, params)
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

// newAcmePluginDeleteCmd builds `pve pbs acme plugin delete <id>` — remove an
// ACME challenge plugin configuration (DELETE /config/acme/plugins/{id}).
// This binding takes no request parameters at all (no digest guard).
func newAcmePluginDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an ACME challenge plugin",
		Long:  "Remove an ACME DNS challenge-plugin configuration (DELETE /config/acme/plugins/{id}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("plugin id must not be empty")
			}

			err := deps.PBS.Config.DeleteAcmePlugins(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("delete acme plugin %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("ACME plugin %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// ===========================================================================
// challenge-schema
// ===========================================================================

// newAcmeChallengeSchemaCmd builds `pve pbs acme challenge-schema` — read the
// parameter schema for every known ACME challenge-plugin type
// (GET /config/acme/challenge-schema).
func newAcmeChallengeSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "challenge-schema",
		Short: "Show ACME challenge-plugin parameter schemas",
		Long:  "Read the parameter schema for every known ACME challenge-plugin type.",
	}
	cmd.AddCommand(newAcmeChallengeSchemaLsCmd())
	return cmd
}

// acmeChallengeSchemaEntry is the decoded shape of one element returned by
// `acme challenge-schema ls` (GET /config/acme/challenge-schema). The
// per-plugin parameter schema is dynamic (its shape depends on the plugin
// type), so it is kept as raw JSON rather than flattened, and excluded from
// the table but preserved losslessly in JSON/YAML output.
type acmeChallengeSchemaEntry struct {
	Id     string          `json:"id"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Type   string          `json:"type"`
}

// newAcmeChallengeSchemaLsCmd builds `pve pbs acme challenge-schema ls` —
// list the parameter schema for every known ACME challenge-plugin type
// (GET /config/acme/challenge-schema).
func newAcmeChallengeSchemaLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACME challenge-plugin schemas",
		Long: "List every known ACME challenge-plugin type together with its parameter " +
			"schema (GET /config/acme/challenge-schema).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListAcmeChallengeSchema(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme challenge schemas: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]acmeChallengeSchemaEntry, 0, len(items))

			for _, raw := range items {
				var e acmeChallengeSchemaEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode acme challenge schema entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{"ID", "NAME", "TYPE"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{e.Id, e.Name, e.Type})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// ===========================================================================
// directories
// ===========================================================================

// newAcmeDirectoriesCmd builds `pve pbs acme directories` — list the known
// ACME directory endpoints PBS ships (GET /config/acme/directories).
func newAcmeDirectoriesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "directories",
		Short: "Show known ACME directory endpoints",
		Long:  "List the named ACME directory endpoints PBS ships, for use with 'acme account add --directory'.",
	}
	cmd.AddCommand(newAcmeDirectoriesLsCmd())
	return cmd
}

// acmeDirectoryEntry is the decoded shape of one element returned by
// `acme directories ls` (GET /config/acme/directories).
type acmeDirectoryEntry struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

// newAcmeDirectoriesLsCmd builds `pve pbs acme directories ls` — list the
// known ACME directory endpoints (GET /config/acme/directories).
func newAcmeDirectoriesLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List known ACME directory endpoints",
		Long:  "List every named ACME directory endpoint PBS ships (GET /config/acme/directories).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListAcmeDirectories(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme directories: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]acmeDirectoryEntry, 0, len(items))

			for _, raw := range items {
				var e acmeDirectoryEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode acme directory entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "URL"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{e.Name, e.Url})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// ===========================================================================
// tos
// ===========================================================================

// newAcmeTosCmd builds `pve pbs acme tos` — read an ACME directory's Terms
// of Service URL (GET /config/acme/tos).
func newAcmeTosCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tos",
		Short: "Show an ACME directory's Terms of Service URL",
		Long:  "Read the Terms of Service URL for an ACME directory, for use with 'acme account add --tos-url'.",
	}
	cmd.AddCommand(newAcmeTosShowCmd())
	return cmd
}

// newAcmeTosShowCmd builds `pve pbs acme tos show` — show the Terms of
// Service URL for an ACME directory (GET /config/acme/tos). Some directories
// have no Terms of Service; in that case PBS returns no data, and this
// renders a message rather than an error.
func newAcmeTosShowCmd() *cobra.Command {
	var directory string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show an ACME directory's Terms of Service URL",
		Long: "Get the Terms of Service URL for an ACME directory (GET /config/acme/tos). " +
			"Without --directory, PBS returns the ToS for its default ACME directory " +
			"(Let's Encrypt).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsconfig.ListAcmeTosParams{}
			if cmd.Flags().Changed("directory") {
				params.Directory = strPtr(directory)
			}

			resp, err := deps.PBS.Config.ListAcmeTos(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get acme terms of service url: %w", err)
			}

			if resp == nil || len(*resp) == 0 {
				res := output.Result{Message: "No Terms of Service URL is published for this ACME directory."}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			tos, err := nodeDecodeText(*resp)
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
	cmd.Flags().StringVar(&directory, "directory", "", "ACME directory URL to query (default: PBS's configured default directory)")
	return cmd
}
