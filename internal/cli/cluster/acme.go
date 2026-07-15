package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newAcmeCmd builds the `pmx pve cluster acme` sub-tree for managing ACME (e.g.
// Let's Encrypt) accounts and DNS-challenge plugins. Account registration,
// update, and deregistration contact the configured ACME certificate authority
// and run as asynchronous tasks; plugin definitions are stored locally.
func newAcmeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acme",
		Short: "Manage cluster ACME accounts and challenge plugins",
		Long: "Manage ACME certificate-authority accounts and DNS-challenge plugins " +
			"used to issue node certificates. Account operations contact the ACME CA " +
			"and run asynchronously; plugin definitions are stored in the cluster config.",
	}
	cmd.AddCommand(
		newAcmeAccountCmd(),
		newAcmePluginCmd(),
		newAcmeDirectoriesCmd(),
		newAcmeChallengeSchemaCmd(),
	)
	return cmd
}

// finishAcmeAsync renders the result of an asynchronous ACME account operation.
// The API returns a task UPID; the command blocks until the task completes
// unless --async was set, in which case the UPID is returned immediately.
func finishAcmeAsync(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, msg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return err
	}
	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// --- accounts ---------------------------------------------------------------

var acmeAccountListColumns = []string{"name"}

func newAcmeAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage ACME accounts",
		Long: "Manage the ACME certificate-authority accounts used to issue node " +
			"certificates. Registering, updating, and deregistering an account contacts " +
			"the ACME CA and runs asynchronously.",
	}
	cmd.AddCommand(
		newAcmeAccountListCmd(),
		newAcmeAccountGetCmd(),
		newAcmeAccountCreateCmd(),
		newAcmeAccountSetCmd(),
		newAcmeAccountDeleteCmd(),
	)
	return cmd
}

func newAcmeAccountListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List registered ACME accounts",
		Long:    "List the ACME accounts registered on the cluster by name.",
		Example: `  pmx pve cluster acme account list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListAcmeAccount(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme accounts: %w", err)
			}
			res, err := rawFixedColumnsResult(derefRawList(resp), acmeAccountListColumns)
			if err != nil {
				return fmt.Errorf("list acme accounts: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newAcmeAccountGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show a single ACME account",
		Long: "Show the full registration details of a single ACME account, including its " +
			"contact address, directory URL, and terms-of-service status.",
		Example: `  pmx pve cluster acme account get default`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			resp, err := deps.API.Cluster.GetAcmeAccount(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("get acme account %q: %w", name, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get acme account %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newAcmeAccountCreateCmd() *cobra.Command {
	var (
		contact   string
		directory string
		eabKid    string
		eabHmac   string
		tosURL    string
		async     bool
	)
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Register a new ACME account",
		Long: "Register a new ACME account with the certificate authority. This " +
			"contacts the ACME directory and runs as an asynchronous task. The " +
			"optional positional name is the account config file name (default 'default').",
		Example: `  pmx pve cluster acme account create --contact admin@example.com
  pmx pve cluster acme account create default --contact admin@example.com \
  --directory https://acme-staging-v02.api.letsencrypt.org/directory`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			if fl.Changed("async") {
				deps.Async = async
			}
			params := &pvecluster.CreateAcmeAccountParams{Contact: contact}
			if len(args) == 1 {
				params.Name = &args[0]
			}
			if fl.Changed("directory") {
				params.Directory = &directory
			}
			if fl.Changed("eab-kid") {
				params.EabKid = &eabKid
			}
			if fl.Changed("eab-hmac-key") {
				params.EabHmacKey = &eabHmac
			}
			if fl.Changed("tos-url") {
				params.TosUrl = &tosURL
			}
			resp, err := deps.API.Cluster.CreateAcmeAccount(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create acme account: %w", err)
			}
			return finishAcmeAsync(cmd, deps, json.RawMessage(*resp), "ACME account registered.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&contact, "contact", "", "contact email address(es), comma-separated (required)")
	f.StringVar(&directory, "directory", "", "URL of the ACME CA directory endpoint")
	f.StringVar(&eabKid, "eab-kid", "", "key identifier for External Account Binding")
	f.StringVar(&eabHmac, "eab-hmac-key", "", "HMAC key for External Account Binding (sensitive)")
	f.StringVar(&tosURL, "tos-url", "", "URL of the CA Terms of Service; setting it indicates agreement")
	f.BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cli.MustMarkRequired(cmd, "contact")
	return cmd
}

func newAcmeAccountSetCmd() *cobra.Command {
	var (
		contact string
		async   bool
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update an ACME account contact",
		Long: "Update the contact email address(es) of an ACME account. This contacts " +
			"the ACME CA and runs as an asynchronous task.",
		Example: `  pmx pve cluster acme account set default --contact admin@example.com`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			fl := cmd.Flags()
			if fl.Changed("async") {
				deps.Async = async
			}
			params := &pvecluster.UpdateAcmeAccountParams{Contact: &contact}
			resp, err := deps.API.Cluster.UpdateAcmeAccount(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update acme account %q: %w", name, err)
			}
			return finishAcmeAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("ACME account %s updated.", name))
		},
	}
	f := cmd.Flags()
	f.StringVar(&contact, "contact", "", "contact email address(es), comma-separated (required)")
	f.BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cli.MustMarkRequired(cmd, "contact")
	return cmd
}

func newAcmeAccountDeleteCmd() *cobra.Command {
	var (
		yes   bool
		async bool
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Deactivate and remove an ACME account",
		Long: "Deactivate an ACME account at the CA and remove it locally. This " +
			"contacts the ACME CA and runs as an asynchronous task.",
		Example: `  pmx pve cluster acme account delete default --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			fl := cmd.Flags()
			if !yes {
				return fmt.Errorf("refusing to delete acme account %q without confirmation: pass --yes/-y", name)
			}
			if fl.Changed("async") {
				deps.Async = async
			}
			resp, err := deps.API.Cluster.DeleteAcmeAccount(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete acme account %q: %w", name, err)
			}
			return finishAcmeAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("ACME account %s deleted.", name))
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	f.BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	return cmd
}

// --- plugins ----------------------------------------------------------------

var acmePluginListColumns = []string{"plugin", "type", "api"}

func newAcmePluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage ACME challenge plugins",
		Long: "Manage ACME challenge plugins, which tell the ACME client how to answer " +
			"domain-validation challenges, for example dns-01 through a DNS provider API. " +
			"Plugin definitions are stored in the cluster configuration.",
	}
	cmd.AddCommand(
		newAcmePluginListCmd(),
		newAcmePluginGetCmd(),
		newAcmePluginCreateCmd(),
		newAcmePluginSetCmd(),
		newAcmePluginDeleteCmd(),
	)
	return cmd
}

func newAcmePluginListCmd() *cobra.Command {
	var pluginType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List ACME challenge plugins",
		Long: "List the configured ACME challenge plugins with their type and DNS API " +
			"provider. Pass --type to show only plugins of a given challenge type.",
		Example: `  pmx pve cluster acme plugin list
  pmx pve cluster acme plugin list --type dns`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &pvecluster.ListAcmePluginsParams{}
			if cmd.Flags().Changed("type") {
				params.Type = &pluginType
			}
			resp, err := deps.API.Cluster.ListAcmePlugins(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list acme plugins: %w", err)
			}
			res, err := rawFixedColumnsResult(derefRawList(resp), acmePluginListColumns)
			if err != nil {
				return fmt.Errorf("list acme plugins: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&pluginType, "type", "", "only list plugins of a specific type (e.g. dns, standalone)")
	return cmd
}

func newAcmePluginGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Short:   "Show a single ACME plugin",
		Long:    "Show the full configuration of a single ACME challenge plugin by its id.",
		Example: `  pmx pve cluster acme plugin get my-dns`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.GetAcmePlugins(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get acme plugin %q: %w", id, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get acme plugin %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newAcmePluginCreateCmd() *cobra.Command {
	var (
		pluginType string
		api        string
		data       string
		nodes      string
		disable    bool
		valDelay   int64
	)
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create an ACME challenge plugin",
		Long: "Create an ACME challenge plugin. For dns-01 plugins, --api selects the " +
			"DNS provider and --data carries its base64-encoded credential block.",
		Example: `  pmx pve cluster acme plugin create my-dns --type dns --api cloudflare --data '${ACME_DNS_DATA}'`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			params := &pvecluster.CreateAcmePluginsParams{Id: id, Type: pluginType}
			if fl.Changed("api") {
				params.Api = &api
			}
			if fl.Changed("data") {
				params.Data = &data
			}
			if fl.Changed("nodes") {
				params.Nodes = &nodes
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("validation-delay") {
				params.ValidationDelay = &valDelay
			}
			if err := deps.API.Cluster.CreateAcmePlugins(cmd.Context(), params); err != nil {
				return fmt.Errorf("create acme plugin %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("acme plugin %s created.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&pluginType, "type", "", "ACME challenge type, e.g. dns or standalone (required)")
	f.StringVar(&api, "api", "", "DNS API provider name (dns-01 plugins)")
	f.StringVar(&data, "data", "", "base64-encoded DNS plugin credential data (sensitive)")
	f.StringVar(&nodes, "nodes", "", "comma-separated list of nodes the plugin applies to")
	f.BoolVar(&disable, "disable", false, "create the plugin in a disabled state")
	f.Int64Var(&valDelay, "validation-delay", 0, "seconds to wait before requesting validation")
	cli.MustMarkRequired(cmd, "type")
	return cmd
}

func newAcmePluginSetCmd() *cobra.Command {
	var (
		api      string
		data     string
		nodes    string
		disable  bool
		valDelay int64
		digest   string
		del      string
	)
	cmd := &cobra.Command{
		Use:     "set <id>",
		Short:   "Update an ACME challenge plugin",
		Long:    "Update an ACME challenge plugin. Only flags that are passed are changed.",
		Example: `  pmx pve cluster acme plugin set my-dns --data '${ACME_DNS_DATA}'`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "api", "data", "nodes", "disable", "validation-delay", "delete") {
				return fmt.Errorf("no changes requested: pass at least one field flag")
			}
			params := &pvecluster.UpdateAcmePluginsParams{}
			if fl.Changed("api") {
				params.Api = &api
			}
			if fl.Changed("data") {
				params.Data = &data
			}
			if fl.Changed("nodes") {
				params.Nodes = &nodes
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("validation-delay") {
				params.ValidationDelay = &valDelay
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if err := deps.API.Cluster.UpdateAcmePlugins(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update acme plugin %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("acme plugin %s updated.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&api, "api", "", "DNS API provider name (dns-01 plugins)")
	f.StringVar(&data, "data", "", "base64-encoded DNS plugin credential data (sensitive)")
	f.StringVar(&nodes, "nodes", "", "comma-separated list of nodes the plugin applies to")
	f.BoolVar(&disable, "disable", false, "disable the plugin")
	f.Int64Var(&valDelay, "validation-delay", 0, "seconds to wait before requesting validation")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to reset to default")
	return cmd
}

func newAcmePluginDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an ACME challenge plugin",
		Long: "Delete an ACME challenge plugin from the cluster configuration. Requires " +
			"--yes/-y to confirm; without it the command refuses to delete.",
		Example: `  pmx pve cluster acme plugin delete my-dns --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete acme plugin %q without confirmation: pass --yes/-y", id)
			}
			if err := deps.API.Cluster.DeleteAcmePlugins(cmd.Context(), id); err != nil {
				return fmt.Errorf("delete acme plugin %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("acme plugin %s deleted.", id)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// --- read-only directories / challenge schema -------------------------------

func newAcmeDirectoriesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "directories",
		Short: "List known ACME CA directory endpoints",
		Long: "List the well-known ACME certificate-authority directory endpoints that " +
			"Proxmox VE ships, such as the Let's Encrypt production and staging URLs.",
		Example: `  pmx pve cluster acme directories`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListAcmeDirectories(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme directories: %w", err)
			}
			res, err := rawUnionResult(derefRawList(resp))
			if err != nil {
				return fmt.Errorf("list acme directories: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newAcmeChallengeSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "challenge-schema",
		Short: "List available ACME challenge plugin schemas",
		Long: "List the available ACME challenge plugin schemas, describing the DNS API " +
			"providers and the fields each one expects. Use this to discover valid --api " +
			"values and plugin data when creating a dns-01 plugin.",
		Example: `  pmx pve cluster acme challenge-schema`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListAcmeChallengeSchema(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acme challenge schema: %w", err)
			}
			res, err := rawUnionResult(derefRawList(resp))
			if err != nil {
				return fmt.Errorf("list acme challenge schema: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
