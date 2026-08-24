package cluster

import (
	"encoding/json"
	"fmt"
	"strconv"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// clusterStatusEntry is the decoded shape of one entry from cluster.ListStatus.
// Entries are either of type "cluster" (quorum summary) or "node" (per-node
// membership). Fields not relevant to a given entry type stay at their zero
// value.
type clusterStatusEntry struct {
	Type    string     `json:"type"`
	Name    string     `json:"name"`
	ID      string     `json:"id"`
	Online  pve.PVEInt `json:"online"`
	Level   string     `json:"level"`
	NodeID  pve.PVEInt `json:"nodeid"`
	Nodes   pve.PVEInt `json:"nodes"`
	Quorate pve.PVEInt `json:"quorate"`
}

// newStatusCmd builds `pmx pve cluster status`.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show cluster and node membership status",
		Long: "Show cluster-wide quorum status and per-node membership: each node's online state, " +
			"node ID, and the cluster's quorate flag.",
		Example: `  pmx pve cluster status`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Cluster.ListStatus(cmd.Context())
			if err != nil {
				return fmt.Errorf("get cluster status: %w", err)
			}

			headers := []string{"NAME", "TYPE", "ID", "ONLINE", "LEVEL", "NODEID", "NODES", "QUORATE"}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e clusterStatusEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode cluster status entry: %w", err)
					}
					rows = append(rows, []string{
						e.Name,
						e.Type,
						e.ID,
						strconv.Itoa(int(e.Online)),
						e.Level,
						strconv.Itoa(int(e.NodeID)),
						strconv.Itoa(int(e.Nodes)),
						strconv.Itoa(int(e.Quorate)),
					})
				}
			}

			result := output.Result{Headers: headers, Rows: rows}
			if resp != nil {
				// Preserve the full typed response so json/yaml output is lossless.
				result.Raw = *resp
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}
