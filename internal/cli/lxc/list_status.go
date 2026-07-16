package lxc

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// lxcListEntry is the subset of a /nodes/{node}/lxc list element or a
// /cluster/resources guest entry rendered in the container list table. Type is
// only present in cluster/resources entries and is used to keep lxc guests and
// drop qemu ones; Node is only populated by cluster/resources and is backfilled
// with the resolved node in node-scoped mode.
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
	Node    string      `json:"node,omitempty"`
	Type    string      `json:"type,omitempty"`
}

// newListCmd builds `pmx pve lxc list`.
//
// Without --cluster the command lists containers on the node resolved from
// --node / PMX_NODE / config. With --cluster it calls the cluster-wide
// endpoint and shows all containers across every cluster node; --node is not
// required in that mode.
func newListCmd() *cobra.Command {
	var cluster bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List LXC containers on a node",
		Long: "List the LXC containers on the node selected via --node, PMX_NODE, or the " +
			"active context's default node, with their status, memory, disk, and uptime. " +
			"Pass --cluster to list containers across every cluster node without specifying --node.",
		Example: `  pmx pve lxc list
  pmx pve lxc list --node pve1
  pmx pve lxc list --cluster`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			entries := make([]lxcListEntry, 0)
			decodeInto := func(rawList []json.RawMessage, defaultNode string) error {
				for _, raw := range rawList {
					var e lxcListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode container entry: %w", err)
					}
					// cluster/resources type=vm returns both qemu and lxc guests;
					// keep only lxc. Node-scoped entries carry no type field.
					if e.Type != "" && e.Type != "lxc" {
						continue
					}
					if e.Node == "" {
						e.Node = defaultNode
					}
					entries = append(entries, e)
				}
				return nil
			}

			if cluster {
				// The cluster-wide guest inventory lives in /cluster/resources
				// filtered to VMs; there is no /cluster/lxc list endpoint.
				typeVM := "vm"
				resp, err := deps.API.Cluster.ListResources(cmd.Context(),
					&pvecluster.ListResourcesParams{Type: &typeVM})
				if err != nil {
					return fmt.Errorf("list containers cluster-wide: %w", err)
				}
				if resp != nil {
					if err := decodeInto(*resp, ""); err != nil {
						return err
					}
				}
			} else {
				node, err := resolveNode(deps)
				if err != nil {
					return err
				}
				resp, err := deps.API.Nodes.ListLxc(cmd.Context(), node)
				if err != nil {
					return fmt.Errorf("list containers on node %s: %w", node, err)
				}
				if resp != nil {
					if err := decodeInto(*resp, node); err != nil {
						return err
					}
				}
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].VMID.String() < entries[j].VMID.String()
			})

			res := output.Result{
				Headers: []string{"VMID", "NAME", "STATUS", "MEM", "SWAP", "MAXMEM", "DISK", "UPTIME", "NODE"},
				Raw:     entries,
			}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{
					e.VMID.String(), e.Name, e.Status,
					fmtBytes(e.Mem), fmtBytes(e.Swap), fmtBytes(e.Maxmem),
					fmtBytes(e.Disk), fmtUptime(e.Uptime), e.Node,
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&cluster, "cluster", false, "list containers across all cluster nodes")
	return cmd
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
