package lxc

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// lxcListEntry is the subset of a /nodes/{node}/lxc list element rendered in the
// container list table. The PVE list response is an array of untyped objects.
type lxcListEntry struct {
	VMID    json.Number `json:"vmid"`
	Name    string      `json:"name"`
	Status  string      `json:"status"`
	Mem     int64       `json:"mem"`
	Swap    int64       `json:"swap"`
	Maxmem  int64       `json:"maxmem"`
	Disk    int64       `json:"disk"`
	Maxdisk int64       `json:"maxdisk"`
	Uptime  int64       `json:"uptime"`
}

// newListCmd builds `pmx pve lxc list`.
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List LXC containers on a node",
		Long: "List the LXC containers on the node selected via --node, PMX_NODE, or the " +
			"active context's default node, with their status, memory, disk, and uptime.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListLxc(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("list containers on node %s: %w", node, err)
			}

			entries := make([]lxcListEntry, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e lxcListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode container entry: %w", err)
					}
					entries = append(entries, e)
				}
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].VMID.String() < entries[j].VMID.String()
			})

			res := output.Result{
				Headers: []string{"VMID", "NAME", "STATUS", "MEM", "SWAP", "MAXMEM", "DISK", "UPTIME"},
				Raw:     entries,
			}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{
					e.VMID.String(), e.Name, e.Status,
					fmtBytes(e.Mem), fmtBytes(e.Swap), fmtBytes(e.Maxmem),
					fmtBytes(e.Disk), fmtUptime(e.Uptime),
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newStatusCmd builds `pmx pve lxc status <vmid|name>`.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <vmid|name>",
		Short: "Show the current status of a container",
		Long: "Show a container's current runtime status: state, CPU, memory, swap, disk " +
			"usage, uptime, and any lock held on it.",
		Example: `  pmx pve lxc status 200
  pmx pve lxc status web1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListLxcStatusCurrent(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get status for container %s: %w", vmid, err)
			}
			if resp == nil {
				return fmt.Errorf("get status for container %s: empty response", vmid)
			}

			single := map[string]string{
				"vmid":    vmid,
				"name":    derefStr(resp.Name),
				"status":  resp.Status,
				"cpu":     fmtFloat((*float64)(resp.Cpu)),
				"mem":     fmtBytes(derefInt((*int64)(resp.Mem))),
				"maxmem":  fmtBytes(derefInt((*int64)(resp.Maxmem))),
				"maxswap": fmtBytes(derefInt((*int64)(resp.Maxswap))),
				"disk":    fmtBytes(derefInt((*int64)(resp.Disk))),
				"maxdisk": fmtBytes(derefInt((*int64)(resp.Maxdisk))),
				"uptime":  fmtUptime(derefInt((*int64)(resp.Uptime))),
			}
			if resp.Lock != nil && *resp.Lock != "" {
				single["lock"] = *resp.Lock
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
