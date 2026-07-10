package qemu

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newStatusCmd builds `pmx pve qemu status <vmid>`.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <vmid|name>",
		Short: "Show the current status of a VM",
		Long: "Show the VM's current runtime status: power state, QMP status, CPU and memory " +
			"usage, uptime, PID, and lock state (when set). Resolves the VM by numeric vmid " +
			"or name.",
		Example: `  pmx pve qemu status 100
  pmx pve qemu status web1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListQemuStatusCurrent(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get status for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get status for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{
				"VMID":   strconv.FormatInt(resp.Vmid.Int(), 10),
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
				single["MEM"] = strconv.FormatInt(resp.Mem.Int(), 10)
			}
			if resp.Maxmem != nil {
				single["MAXMEM"] = strconv.FormatInt(resp.Maxmem.Int(), 10)
			}
			if resp.Maxdisk != nil {
				single["MAXDISK"] = strconv.FormatInt(resp.Maxdisk.Int(), 10)
			}
			if resp.Uptime != nil {
				single["UPTIME"] = strconv.FormatInt(resp.Uptime.Int(), 10)
			}
			if resp.Pid != nil {
				single["PID"] = strconv.FormatInt(resp.Pid.Int(), 10)
			}
			if resp.Lock != nil {
				single["LOCK"] = *resp.Lock
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}
