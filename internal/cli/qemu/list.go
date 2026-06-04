package qemu

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// qemuListEntry is the minimal decoded shape of one entry from nodes.ListQemu.
type qemuListEntry struct {
	VMID     int64  `json:"vmid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Mem      int64  `json:"mem"`
	Bootdisk string `json:"bootdisk"`
	PID      int64  `json:"pid"`
}

// newListCmd builds `pve qemu list`.
func newListCmd() *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List QEMU virtual machines on a node",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
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

			headers := []string{"VMID", "NAME", "STATUS", "MEM", "BOOTDISK", "PID", "NODE"}
			entries := make([]qemuListEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e qemuListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode VM entry: %w", err)
					}
					entries = append(entries, e)
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
						node,
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "determine the full status of active VMs")
	return cmd
}
