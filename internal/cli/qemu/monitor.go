package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newMonitorCmd builds `pmx pve qemu monitor <vmid> --command CMD`.
// The QEMU monitor can execute arbitrary low-level commands against the
// hypervisor, some of which are destructive (e.g. device_del, drive_del).
// The command therefore requires --yes confirmation.
func newMonitorCmd() *cobra.Command {
	var (
		command string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "monitor <vmid|name>",
		Short: "Send a raw QEMU monitor command to a VM",
		Long: "Execute a raw QEMU monitor command against the running VM. " +
			"Some monitor commands are destructive or service-affecting. " +
			"--yes is required to confirm the operation. " +
			"This command is typically restricted to root.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf(
					"refusing to send monitor command to VM %s without confirmation: pass --yes/-y",
					vmid,
				)
			}

			params := &nodes.CreateQemuMonitorParams{Command: command}
			resp, err := deps.API.Nodes.CreateQemuMonitor(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("monitor command for VM %s on node %q: %w", vmid, node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = *resp
			}
			return renderAgentResult(cmd, deps, raw,
				fmt.Sprintf("Monitor command sent to VM %s.", vmid))
		},
	}

	cmd.Flags().StringVar(&command, "command", "", "QEMU monitor command to execute (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm sending a potentially destructive monitor command")
	cli.MustMarkRequired(cmd, "command")
	return cmd
}
