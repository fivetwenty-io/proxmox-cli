package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
)

// newCloudinitCmd builds the `pmx pve qemu cloudinit` sub-group: inspect the pending
// cloud-init configuration, dump the generated config of a given type, and
// regenerate the cloud-init drive from the VM configuration.
func newCloudinitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloudinit",
		Short: "Inspect and regenerate VM cloud-init configuration",
		Long: "Inspect the pending cloud-init configuration, dump the generated config of a " +
			"given type, or regenerate the cloud-init drive from the VM's current " +
			"configuration. Cloud-init scalars themselves (ciuser, sshkeys, ipconfig, and " +
			"similar) are set via 'pmx pve qemu config set'.",
	}
	cmd.AddCommand(newCloudinitPendingCmd(), newCloudinitDumpCmd(), newCloudinitUpdateCmd())
	return cmd
}

// cloudinitPendingEntry is the decoded shape of one row from the cloud-init
// pending endpoint, which reports the current and pending value of each key.
type cloudinitPendingEntry struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Pending any    `json:"pending"`
	Delete  any    `json:"delete"`
}

// newCloudinitPendingCmd builds `pmx pve qemu cloudinit pending <vmid>`, listing the
// current and pending cloud-init configuration values
// (GET /nodes/{node}/qemu/{vmid}/cloudinit).
func newCloudinitPendingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pending <vmid|name>",
		Short: "Show pending cloud-init configuration changes",
		Long: "List every cloud-init key together with its current value and any pending " +
			"(not-yet-applied) value. Resolves the VM by numeric vmid or name.",
		Example: `  pmx pve qemu cloudinit pending 100
  pmx pve qemu cloudinit pending web1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListQemuCloudinit(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get cloud-init config for VM %s on node %q: %w", vmid, node, err)
			}

			headers := []string{"KEY", "VALUE", "PENDING", "DELETE"}
			entries := make([]cloudinitPendingEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e cloudinitPendingEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode cloud-init entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{
						e.Key,
						stringifyValue(e.Value),
						stringifyValue(e.Pending),
						stringifyValue(e.Delete),
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
	return cmd
}

// newCloudinitDumpCmd builds `pmx pve qemu cloudinit dump <vmid> --type <type>`,
// returning the generated cloud-init configuration of the requested type
// (GET /nodes/{node}/qemu/{vmid}/cloudinit/dump).
func newCloudinitDumpCmd() *cobra.Command {
	var ciType string
	cmd := &cobra.Command{
		Use:   "dump <vmid|name>",
		Short: "Dump the generated cloud-init configuration of a given type",
		Long: "Print the cloud-init config PVE generates for the VM, in the format cloud-init " +
			"itself consumes. --type selects which document to dump: user, network, or meta.",
		Example: `  pmx pve qemu cloudinit dump 100 --type user
  pmx pve qemu cloudinit dump web1 --type network`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.ListQemuCloudinitDumpParams{Type: ciType}
			resp, err := deps.API.Nodes.ListQemuCloudinitDump(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("dump %s cloud-init config for VM %s on node %q: %w", ciType, vmid, node, err)
			}

			// The dump endpoint returns the generated config as a single string.
			var dump string
			if resp != nil && len(*resp) > 0 {
				if err := json.Unmarshal(*resp, &dump); err != nil {
					// Not a bare string (unexpected); fall back to the raw body.
					dump = string(*resp)
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: dump, Raw: dump}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&ciType, "type", "user", "config type to dump: user, network, or meta")
	return cmd
}

// newCloudinitUpdateCmd builds `pmx pve qemu cloudinit update <vmid>`, regenerating
// the cloud-init drive from the current VM configuration
// (PUT /nodes/{node}/qemu/{vmid}/cloudinit). Only changed configuration is
// applied to the running guest's drive.
func newCloudinitUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <vmid|name>",
		Short: "Regenerate the cloud-init drive from the VM configuration",
		Long: "Regenerate the cloud-init drive so it reflects the VM's current configuration. " +
			"Run this after changing cloud-init settings (via 'pmx pve qemu config set') on a " +
			"running guest; only the changed configuration is applied to the drive. This call " +
			"is synchronous and does not start a background task.",
		Example: `  pmx pve qemu cloudinit update 100
  pmx pve qemu cloudinit update web1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if err := deps.API.Nodes.UpdateQemuCloudinit(cmd.Context(), node, vmid); err != nil {
				return fmt.Errorf("regenerate cloud-init drive for VM %s on node %q: %w", vmid, node, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Regenerated cloud-init drive for VM %s.", vmid)},
				deps.Format)
		},
	}
	return cmd
}
