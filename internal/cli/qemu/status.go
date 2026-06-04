package qemu

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newStatusCmd builds `pve qemu status <vmid>`.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <vmid>",
		Short: "Show the current status of a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			resp, err := deps.API.Nodes.ListQemuStatusCurrent(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get status for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get status for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{
				"VMID":   strconv.FormatInt(resp.Vmid, 10),
				"STATUS": resp.Status,
				"NODE":   node,
			}
			if resp.Name != nil {
				single["NAME"] = *resp.Name
			}
			if resp.Qmpstatus != nil {
				single["QMPSTATUS"] = *resp.Qmpstatus
			}
			if resp.Cpu != nil {
				single["CPU"] = strconv.FormatFloat(resp.Cpu.Float(), 'f', 3, 64)
			}
			if resp.Mem != nil {
				single["MEM"] = strconv.FormatInt(*resp.Mem, 10)
			}
			if resp.Maxmem != nil {
				single["MAXMEM"] = strconv.FormatInt(*resp.Maxmem, 10)
			}
			if resp.Maxdisk != nil {
				single["MAXDISK"] = strconv.FormatInt(*resp.Maxdisk, 10)
			}
			if resp.Uptime != nil {
				single["UPTIME"] = strconv.FormatInt(*resp.Uptime, 10)
			}
			if resp.Pid != nil {
				single["PID"] = strconv.FormatInt(*resp.Pid, 10)
			}
			if resp.Lock != nil {
				single["LOCK"] = *resp.Lock
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}
