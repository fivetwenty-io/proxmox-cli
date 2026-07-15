package node

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeMemUsage decodes the memory/rootfs sub-objects of a node status response.
type nodeMemUsage struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

// nodeCPUInfo decodes the cpuinfo sub-object of a node status response.
type nodeCPUInfo struct {
	Model string `json:"model"`
	Cpus  int64  `json:"cpus"`
}

// nodeKernel decodes the current-kernel sub-object of a node status response.
type nodeKernel struct {
	Sysname string `json:"sysname"`
	Release string `json:"release"`
	Version string `json:"version"`
	Machine string `json:"machine"`
}

// newStatusCmd builds `pmx pve node <node> status`.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <node>",
		Short: "Show node status",
		Long: "Show a cluster node's current status: CPU, load average, memory and disk " +
			"usage, kernel, and Proxmox VE version.",
		Example: `  pmx pve node status pve1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.API.Nodes.ListStatus(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get status for node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get status for node %q: empty response", node)
			}

			single := map[string]string{
				"NODE":        node,
				"STATUS":      "online",
				"CPU":         strconv.FormatFloat(resp.Cpu.Float(), 'f', 3, 64),
				"PVE-VERSION": resp.Pveversion,
			}
			if len(resp.Loadavg) > 0 {
				single["LOADAVG"] = resp.Loadavg[0]
			}
			if mem, ok := decodeMemUsage(resp.Memory); ok {
				single["MEM"] = fmt.Sprintf("%s / %s", fmtBytes(mem.Used), fmtBytes(mem.Total))
			}
			if disk, ok := decodeMemUsage(resp.Rootfs); ok {
				single["DISK"] = fmt.Sprintf("%s / %s", fmtBytes(disk.Used), fmtBytes(disk.Total))
			}
			if ci, ok := decodeCPUInfo(resp.Cpuinfo); ok {
				single["CPUINFO"] = fmt.Sprintf("%s (%d cpus)", ci.Model, ci.Cpus)
			}
			if k, ok := decodeKernel(resp.CurrentKernel); ok {
				single["KERNEL"] = kernelString(k)
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodePowerCmd builds a node power-control command (reboot or shutdown) that
// wraps POST /nodes/{node}/status with the matching command. Both actions are
// disruptive, so each is gated behind --yes. long and example are threaded
// per-verb by the caller since the two verbs share this builder but need
// distinct docs.
func newNodePowerCmd(verb, short, long, example string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     verb,
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to %s node %q without confirmation: pass --yes/-y", verb, deps.Node)
			}
			params := &nodes.CreateStatusParams{Command: verb}
			if err := deps.API.Nodes.CreateStatus(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("%s node %q: %w", verb, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Node %q %s initiated.", deps.Node, verb)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the disruptive operation without prompting")
	return cmd
}

// newRebootCmd builds `pmx pve node reboot`.
func newRebootCmd() *cobra.Command {
	return newNodePowerCmd("reboot", "Reboot the node",
		"Reboot the resolved node. This disrupts every guest and service running on it, "+
			"so it refuses to run without --yes/-y. The command returns once the reboot is "+
			"initiated; it does not wait for the node to come back up.",
		`  pmx pve node reboot --yes`)
}

// newShutdownCmd builds `pmx pve node shutdown`.
func newShutdownCmd() *cobra.Command {
	return newNodePowerCmd("shutdown", "Shut down the node",
		"Shut down the resolved node. This disrupts every guest and service running on "+
			"it, so it refuses to run without --yes/-y. The command returns once the "+
			"shutdown is initiated.",
		`  pmx pve node shutdown --yes`)
}

// decodeMemUsage unmarshals a memory/rootfs total+used sub-object. It returns
// false when the raw payload is empty or cannot be decoded.
func decodeMemUsage(raw json.RawMessage) (nodeMemUsage, bool) {
	var m nodeMemUsage
	if len(raw) == 0 {
		return m, false
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, false
	}
	return m, true
}

// decodeCPUInfo unmarshals the cpuinfo sub-object.
func decodeCPUInfo(raw json.RawMessage) (nodeCPUInfo, bool) {
	var c nodeCPUInfo
	if len(raw) == 0 {
		return c, false
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, false
	}
	return c, true
}

// decodeKernel unmarshals the current-kernel sub-object.
func decodeKernel(raw json.RawMessage) (nodeKernel, bool) {
	var k nodeKernel
	if len(raw) == 0 {
		return k, false
	}
	if err := json.Unmarshal(raw, &k); err != nil {
		return k, false
	}
	return k, true
}

// kernelString renders a node kernel sub-object into a one-line summary.
func kernelString(k nodeKernel) string {
	if k.Sysname != "" && k.Release != "" {
		return fmt.Sprintf("%s %s", k.Sysname, k.Release)
	}
	if k.Release != "" {
		return k.Release
	}
	return k.Version
}
