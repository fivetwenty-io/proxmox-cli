package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newNodeConfigCmd builds the `pmx node config` sub-tree for the node-level
// configuration: description, ACME settings, the wake-on-LAN MAC, the
// ballooning target, and the startall on-boot delay.
func newNodeConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and edit the node configuration",
		Long: "Show or update the node-level configuration: description, ACME account and " +
			"domains, wake-on-LAN MAC, ballooning target, and startall on-boot delay.",
	}
	cmd.AddCommand(
		newNodeConfigGetCmd(),
		newNodeConfigSetCmd(),
		newNodeConfigDescribeCmd(),
	)
	return cmd
}

// newNodeConfigDescribeCmd builds `pmx node config describe`, an offline
// catalog of every settable node configuration option from the PVE API
// schema (see config_schema_gen.go).
func newNodeConfigDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: configSchemas,
		Short:   "Describe all settable node configuration options and their defaults",
		Long: "List every settable node configuration option from the PVE API schema: " +
			"type, built-in default, allowed values, and the sub-keys of dict-encoded " +
			"options. Runs offline. Pass an option name to show only that option with " +
			"full descriptions.",
		CommandHint:         "pmx node config describe",
		SubKeyRowsInCatalog: true,
	})
}

func newNodeConfigGetCmd() *cobra.Command {
	var (
		property     string
		withDefaults bool
	)
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show the node configuration",
		Long: "Show the node configuration currently set. The PVE API omits options left " +
			"at their built-in defaults; pass --defaults to also list those with the " +
			"value they effectively have.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListConfigParams{}
			if cmd.Flags().Changed("property") {
				params.Property = &property
			}
			resp, err := deps.API.Nodes.ListConfig(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("get config for node %q: %w", deps.Node, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get config for node %q: %w", deps.Node, err)
			}
			if withDefaults {
				single, raw = optionschema.MergeDefaults(configSchemas, single, raw, optionschema.MergeOpts{})
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&property, "property", "", "return only this single configuration property")
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"also list unset options with their built-in default values")
	return cmd
}

func newNodeConfigSetCmd() *cobra.Command {
	var (
		description         string
		acme                string
		acmeDomain          []string
		wakeonlan           string
		ballooningTarget    int64
		startallOnbootDelay int64
		digest              string
		del                 string
		location            string
	)
	setFlags := []string{
		"description", "acme", "acme-domain", "wakeonlan", "ballooning-target",
		"startall-onboot-delay", "digest", "delete", "location",
	}
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update the node configuration",
		Long:  "Update the node-level configuration. Only the flags you pass are changed.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			if !anyFlagChanged(fl, setFlags...) {
				return fmt.Errorf("no changes to set: pass at least one flag")
			}
			params := &nodes.UpdateConfigParams{}
			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("acme") {
				params.Acme = &acme
			}
			if fl.Changed("acme-domain") {
				domains, err := cli.ParseIndexedValues(acmeDomain, "acme-domain")
				if err != nil {
					return err
				}
				params.Acmedomain = domains
			}
			if fl.Changed("wakeonlan") {
				params.Wakeonlan = &wakeonlan
			}
			if fl.Changed("ballooning-target") {
				params.BallooningTarget = &ballooningTarget
			}
			if fl.Changed("startall-onboot-delay") {
				params.StartallOnbootDelay = &startallOnbootDelay
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if fl.Changed("location") {
				params.Location = &location
			}
			if err := deps.API.Nodes.UpdateConfig(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("set config for node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Configuration updated on node %q.", deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&description, "description", "", "node description shown in the web UI (supports markdown)")
	f.StringVar(&acme, "acme", "", "node ACME configuration, for example domains=example.com,account=default")
	f.StringArrayVar(&acmeDomain, "acme-domain", nil,
		"ACME domain as INDEX=DOMAIN[,settings]; repeat for multiple domains (INDEX is a non-negative integer)")
	f.StringVar(&wakeonlan, "wakeonlan", "", "MAC address used to wake the node via wake-on-LAN")
	f.Int64Var(&ballooningTarget, "ballooning-target", 0,
		"RAM usage target percentage at which host ballooning starts (0 disables)")
	f.Int64Var(&startallOnbootDelay, "startall-onboot-delay", 0,
		"seconds to wait between starting guests on boot")
	f.StringVar(&digest, "digest", "",
		"SHA1 digest of the current configuration to guard against concurrent edits")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to reset to default")
	f.StringVar(&location, "location", "",
		"geographic location of the node, overrides the datacenter-level default")

	// Append generated schema detail (allowed values, defaults, ranges, dict
	// sub-keys) to each option flag's help text; see config_schema_gen.go.
	optionschema.EnrichFlags(f, configSchemas)
	return cmd
}
