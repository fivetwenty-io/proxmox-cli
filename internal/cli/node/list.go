package node

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeListEntry is the minimal decoded shape of one entry from nodes.ListNodes.
type nodeListEntry struct {
	Node           string  `json:"node"`
	Status         string  `json:"status"`
	Cpu            float64 `json:"cpu"`
	Maxcpu         int64   `json:"maxcpu"`
	Mem            int64   `json:"mem"`
	Maxmem         int64   `json:"maxmem"`
	Uptime         int64   `json:"uptime"`
	SSLFingerprint string  `json:"ssl_fingerprint"`
}

// newListCmd builds `pmx pve node list`.
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cluster nodes",
		Long: "List every node in the Proxmox VE cluster with its status, CPU and memory " +
			"usage, uptime, and SSL certificate fingerprint.",
		Example: `  pmx pve node list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Nodes.ListNodes(cmd.Context())
			if err != nil {
				return fmt.Errorf("list nodes: %w", err)
			}

			headers := []string{"NODE", "STATUS", "CPU", "MAXCPU", "MEM", "MAXMEM", "UPTIME", "SSL-FINGERPRINT"}
			entries := make([]nodeListEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e nodeListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode node entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{
						e.Node,
						e.Status,
						strconv.FormatFloat(e.Cpu, 'f', 3, 64),
						strconv.FormatInt(e.Maxcpu, 10),
						strconv.FormatInt(e.Mem, 10),
						strconv.FormatInt(e.Maxmem, 10),
						strconv.FormatInt(e.Uptime, 10),
						e.SSLFingerprint,
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}
