package lxc

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
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

// newListCmd builds `pve lxc list`.
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List LXC containers on a node",
		Args:  cobra.NoArgs,
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

// newStatusCmd builds `pve lxc status <vmid|name>`.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <vmid|name>",
		Short: "Show the current status of a container",
		Args:  cobra.ExactArgs(1),
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
				"mem":     fmtBytes(derefInt(resp.Mem)),
				"maxmem":  fmtBytes(derefInt(resp.Maxmem)),
				"maxswap": fmtBytes(derefInt(resp.Maxswap)),
				"disk":    fmtBytes(derefInt(resp.Disk)),
				"maxdisk": fmtBytes(derefInt(resp.Maxdisk)),
				"uptime":  fmtUptime(derefInt(resp.Uptime)),
			}
			if resp.Lock != nil && *resp.Lock != "" {
				single["lock"] = *resp.Lock
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
