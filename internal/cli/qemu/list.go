package qemu

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
)

// qemuListEntry is the minimal decoded shape of one entry from nodes.ListQemu
// or a cluster/resources VM entry. Type is only present in cluster/resources
// entries and is used to keep qemu guests and drop lxc ones.
type qemuListEntry struct {
	VMID     int64  `json:"vmid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Mem      int64  `json:"mem"`
	Bootdisk string `json:"bootdisk"`
	PID      int64  `json:"pid"`
	Node     string `json:"node"`
	Type     string `json:"type"`
}

// newListCmd builds `pmx qemu list`.
//
// Without --cluster the command lists VMs on the node resolved from --node /
// PMX_NODE / config. With --cluster it calls the cluster-wide endpoint and
// shows all VMs across every cluster node; --node is not required in that mode.
func newListCmd() *cobra.Command {
	var (
		full    bool
		cluster bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List QEMU virtual machines on a node",
		Long: "List QEMU virtual machines on the configured node. Pass --cluster to " +
			"list VMs across every cluster node without specifying --node.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if cluster && cmd.Flags().Changed("full") {
				return fmt.Errorf("--full is node-scoped and cannot be combined with --cluster")
			}

			headers := []string{"VMID", "NAME", "STATUS", "MEM", "BOOTDISK", "PID", "NODE"}
			entries := make([]qemuListEntry, 0)
			rawAll := make([]json.RawMessage, 0)
			rows := make([][]string, 0)

			buildRows := func(rawList []json.RawMessage, defaultNode string) error {
				for _, raw := range rawList {
					var e qemuListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode VM entry: %w", err)
					}
					// cluster/resources type=vm returns both qemu and lxc guests;
					// keep only qemu. Node-scoped entries carry no type field.
					if e.Type != "" && e.Type != "qemu" {
						continue
					}
					// Cluster entries populate Node; node endpoint entries do not.
					if e.Node == "" {
						e.Node = defaultNode
					}
					entries = append(entries, e)
					rawAll = append(rawAll, raw)
					pid := ""
					if e.PID != 0 {
						pid = strconv.FormatInt(e.PID, 10)
					}
					rows = append(rows, []string{
						strconv.FormatInt(e.VMID, 10),
						e.Name,
						e.Status,
						strconv.FormatInt(e.Mem, 10),
						e.Bootdisk,
						pid,
						e.Node,
					})
				}
				return nil
			}

			if cluster {
				// GET /cluster/qemu is only a directory index (cpu-flags,
				// custom-cpu-models), not a VM list; the cluster-wide guest
				// inventory lives in /cluster/resources filtered to VMs.
				typeVM := "vm"
				resp, err := deps.API.Cluster.ListResources(cmd.Context(),
					&pvecluster.ListResourcesParams{Type: &typeVM})
				if err != nil {
					return fmt.Errorf("list VMs cluster-wide: %w", err)
				}
				if resp != nil {
					if err := buildRows(*resp, ""); err != nil {
						return err
					}
				}
			} else {
				node, err := resolveNode(deps)
				if err != nil {
					return err
				}
				params := &nodes.ListQemuParams{}
				if cmd.Flags().Changed("full") {
					params.Full = boolPtr(full)
				}
				resp, err := deps.API.Nodes.ListQemu(cmd.Context(), node, params)
				if err != nil {
					return fmt.Errorf("list VMs on node %q: %w", node, err)
				}
				if resp != nil {
					if err := buildRows(*resp, node); err != nil {
						return err
					}
				}
			}

			// Raw carries the verbatim endpoint entries so JSON/YAML output keeps
			// every field the API returned (the table uses the decoded subset).
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: rawAll}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "determine the full status of active VMs (node-scoped only)")
	cmd.Flags().BoolVar(&cluster, "cluster", false, "list VMs across all cluster nodes")
	return cmd
}
