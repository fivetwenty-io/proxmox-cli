package lxc

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newConfigCmd builds `pve lxc config` and its get/set/pending sub-commands.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Get or set container configuration",
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigPendingCmd())
	return cmd
}

// newConfigGetCmd builds `pve lxc config get <vmid>`.
func newConfigGetCmd() *cobra.Command {
	var snapshot string
	var current bool

	cmd := &cobra.Command{
		Use:   "get <vmid>",
		Short: "Show the configuration of a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := getDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			params := &nodes.ListLxcConfigParams{}
			if snapshot != "" {
				params.Snapshot = &snapshot
			}
			if current {
				params.Current = &current
			}

			resp, err := deps.API.Nodes.ListLxcConfig(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("get config for container %s: %w", vmid, err)
			}

			single, err := structToStringMap(resp)
			if err != nil {
				return err
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&snapshot, "snapshot", "", "fetch config values from the given snapshot")
	cmd.Flags().BoolVar(&current, "current", false, "show current (committed) values instead of pending")
	return cmd
}

// newConfigSetCmd builds `pve lxc config set <vmid>`.
func newConfigSetCmd() *cobra.Command {
	var (
		hostname    string
		memory      int64
		swap        int64
		cores       int64
		cpulimit    string
		cpuunits    int64
		ostype      string
		rootfs      string
		description string
		deleteKeys  string
		revertKeys  string
	)

	cmd := &cobra.Command{
		Use:   "set <vmid>",
		Short: "Update the configuration of a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := getDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			params := &nodes.UpdateLxcConfigParams{}
			set := false
			fl := cmd.Flags()

			if fl.Changed("hostname") {
				params.Hostname = &hostname
				set = true
			}
			if fl.Changed("memory") {
				params.Memory = &memory
				set = true
			}
			if fl.Changed("swap") {
				params.Swap = &swap
				set = true
			}
			if fl.Changed("cores") {
				params.Cores = &cores
				set = true
			}
			if fl.Changed("cpulimit") {
				params.Cpulimit = parseFloatPtr(cpulimit)
				set = true
			}
			if fl.Changed("cpuunits") {
				params.Cpuunits = &cpuunits
				set = true
			}
			if fl.Changed("ostype") {
				params.Ostype = &ostype
				set = true
			}
			if fl.Changed("rootfs") {
				params.Rootfs = &rootfs
				set = true
			}
			if fl.Changed("description") {
				params.Description = &description
				set = true
			}
			if fl.Changed("delete") {
				params.Delete = &deleteKeys
				set = true
			}
			if fl.Changed("revert") {
				params.Revert = &revertKeys
				set = true
			}

			if !set {
				return fmt.Errorf("no configuration fields given: specify at least one --hostname/--memory/--cores/... flag")
			}

			if err := deps.API.Nodes.UpdateLxcConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update config for container %s: %w", vmid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Container %s config updated.", vmid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&hostname, "hostname", "", "set the container hostname")
	fl.Int64Var(&memory, "memory", 0, "amount of RAM in MB")
	fl.Int64Var(&swap, "swap", 0, "amount of SWAP in MB")
	fl.Int64Var(&cores, "cores", 0, "number of CPU cores")
	fl.StringVar(&cpulimit, "cpulimit", "", "CPU usage limit (0 = unlimited)")
	fl.Int64Var(&cpuunits, "cpuunits", 0, "CPU weight")
	fl.StringVar(&ostype, "ostype", "", "OS type")
	fl.StringVar(&rootfs, "rootfs", "", "root filesystem volume")
	fl.StringVar(&description, "description", "", "container description")
	fl.StringVar(&deleteKeys, "delete", "", "comma-separated list of settings to delete")
	fl.StringVar(&revertKeys, "revert", "", "comma-separated list of pending changes to revert")
	return cmd
}

// lxcPendingEntry is one element from the ListLxcPending response array. Each
// element holds the current committed value and any pending (next-reboot) value
// for a single config key.
type lxcPendingEntry struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Pending any    `json:"pending"`
	Delete  int    `json:"delete"`
}

// newConfigPendingCmd builds `pve lxc config pending <vmid>`.
//
// Returns the diff between the currently committed configuration and any
// changes that take effect after the next container restart.
func newConfigPendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pending <vmid>",
		Short: "Show pending configuration changes for a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := getDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			resp, err := deps.API.Nodes.ListLxcPending(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get pending config for container %s on node %q: %w", vmid, node, err)
			}

			headers := []string{"KEY", "VALUE", "PENDING-VALUE"}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e lxcPendingEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode pending config entry: %w", err)
					}
					rows = append(rows, []string{
						e.Key,
						fmt.Sprintf("%v", e.Value),
						fmt.Sprintf("%v", e.Pending),
					})
				}
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// structToStringMap marshals a typed response struct or decoded value to a flat
// string map for key-value rendering, skipping empty/nil fields. It is shared by
// the config, console, and firewall renderers, so its errors name the response
// generically rather than any one endpoint.
func structToStringMap(v any) (map[string]string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	out := make(map[string]string, len(generic))
	for k, val := range generic {
		if val == nil {
			continue
		}
		s := fmt.Sprintf("%v", val)
		if s == "" {
			continue
		}
		out[k] = s
	}
	return out, nil
}

// parseFloatPtr converts a string flag value to a *float64, returning nil on a
// parse failure so an invalid value is simply omitted from the request.
func parseFloatPtr(s string) *float64 {
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return nil
	}
	return &f
}
