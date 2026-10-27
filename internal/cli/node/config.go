package node

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newNodeConfigCmd builds the `pmx pve node config` sub-tree for the node-level
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

// newNodeConfigDescribeCmd builds `pmx pve node config describe`, an offline
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
		CommandHint:         "pmx pve node config describe",
		SubKeyRowsInCatalog: true,
	})
}

// nodeConfigPath is the API path of a node's configuration.
func nodeConfigPath(node string) string {
	return "/nodes/" + url.PathEscape(node) + "/config"
}

// newNodeConfigGetCmd builds `pmx pve node config get`.
//
// The raw API response is read directly (deps.API.Raw.GetCtx) instead of
// through nodes.ListConfig because the generated ListConfigResponse cannot
// represent the indexed ACME domain keys: its field for them is tagged with
// the literal placeholder json:"acmedomain[n]", which never matches a real
// key such as "acmedomain0", so decoding into it silently drops every
// configured ACME domain. Keys the struct does not know about at all would
// be lost the same way. nodeConfigObject still decodes the response through
// the struct, so the keys it does know keep their typed form.
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
		Example: `  pmx pve node config get
  pmx pve node config get --defaults`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := map[string]any{}
			if cmd.Flags().Changed("property") {
				params["property"] = property
			}
			resp, err := deps.API.Raw.GetCtx(cmd.Context(), nodeConfigPath(deps.Node), params)
			if err != nil {
				return fmt.Errorf("get config for node %q: %w", deps.Node, err)
			}
			obj, err := nodeConfigObject(resp)
			if err != nil {
				return fmt.Errorf("get config for node %q: %w", deps.Node, err)
			}
			single, raw, err := objectToSingle(obj)
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

// nodeConfigObject returns the node configuration in resp, the raw response
// of GET /nodes/{node}/config, with every key it holds. PVE returns some
// integer options, such as startall-onboot-delay and ballooning-target, as
// JSON strings. The keys nodes.ListConfigResponse knows therefore take the
// value it decodes, so an integer reaches JSON and YAML output as a number,
// as it did when the command decoded through the struct alone. Every other
// key, including the indexed acmedomainN keys the struct cannot hold, keeps
// the value PVE sent. A nil response is an empty configuration.
func nodeConfigObject(resp any) (map[string]any, error) {
	if resp == nil {
		return map[string]any{}, nil
	}
	obj, ok := resp.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response shape %T", resp)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	var typed nodes.ListConfigResponse
	if err := json.Unmarshal(data, &typed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	typedData, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("encode typed response: %w", err)
	}
	var typedObj map[string]any
	if err := json.Unmarshal(typedData, &typedObj); err != nil {
		return nil, fmt.Errorf("decode typed response: %w", err)
	}

	merged := maps.Clone(obj)
	for k, v := range typedObj {
		if _, present := obj[k]; present {
			merged[k] = v
		}
	}
	return merged, nil
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
		Use:     "set",
		Short:   "Update the node configuration",
		Long:    "Update the node-level configuration. Only the flags you pass are changed.",
		Example: `  pmx pve node config set --description "Primary compute node"`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			if !anyFlagChanged(fl, setFlags...) {
				return fmt.Errorf("no changes requested: pass at least one flag")
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
