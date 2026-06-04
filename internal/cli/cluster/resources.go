package cluster

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// clusterResourceEntry is the decoded shape of one entry from
// cluster.ListResources. Numeric usage fields are pointers so an absent value
// renders as an empty cell rather than a misleading zero.
type clusterResourceEntry struct {
	Type   string   `json:"type"`
	ID     string   `json:"id"`
	Node   string   `json:"node"`
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Cpu    *float64 `json:"cpu"`
	Mem    *int64   `json:"mem"`
	Disk   *int64   `json:"disk"`
	Uptime *int64   `json:"uptime"`
}

// newResourcesCmd builds `pve cluster resources`.
func newResourcesCmd() *cobra.Command {
	var typeFilter string

	cmd := &cobra.Command{
		Use:   "resources",
		Short: "List cluster-wide resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			params := &pvecluster.ListResourcesParams{}
			if typeFilter != "" {
				params.Type = &typeFilter
			}

			resp, err := deps.API.Cluster.ListResources(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list cluster resources: %w", err)
			}

			headers := []string{"TYPE", "ID", "NODE", "NAME", "STATUS", "CPU", "MEM", "DISK", "UPTIME"}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e clusterResourceEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode cluster resource entry: %w", err)
					}
					rows = append(rows, []string{
						e.Type,
						e.ID,
						e.Node,
						e.Name,
						e.Status,
						formatFloatPtr(e.Cpu),
						formatIntPtr(e.Mem),
						formatIntPtr(e.Disk),
						formatIntPtr(e.Uptime),
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

	cmd.Flags().StringVar(&typeFilter, "type", "", "filter by resource type: vm|lxc|storage|node|sdn")
	return cmd
}

// formatFloatPtr renders a *float64 as a 3-decimal string, or empty if nil.
func formatFloatPtr(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 3, 64)
}

// formatIntPtr renders an *int64 as a decimal string, or empty if nil.
func formatIntPtr(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}
