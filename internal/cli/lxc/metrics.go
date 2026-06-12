package lxc

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newMetricsCmd builds `pve lxc metrics <vmid> --timeframe FRAME [--cf CF]`.
//
// The endpoint returns a time-series array of RRD data points (cpu, mem, disk,
// net). Each data point is a free-form JSON object; the command renders the
// raw slice as JSON and as a table of stringified rows when --format=table.
func newMetricsCmd() *cobra.Command {
	var timeframe, cf string

	cmd := &cobra.Command{
		Use:   "metrics <vmid>",
		Short: "Show time-series metrics for a container (RRD data)",
		Long: "Retrieve time-series RRD data points for a container. " +
			"--timeframe is required; supported values are hour, day, week, month, year. " +
			"--cf selects the consolidation function (AVERAGE or MAX).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			if !cmd.Flags().Changed("timeframe") {
				return fmt.Errorf("--timeframe is required: specify hour, day, week, month, or year")
			}

			params := &nodes.ListLxcRrddataParams{Timeframe: timeframe}
			if cmd.Flags().Changed("cf") {
				params.Cf = &cf
			}

			resp, err := deps.API.Nodes.ListLxcRrddata(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("get metrics for container %s on node %q: %w", vmid, node, err)
			}

			// Decode each raw element into a map for table rendering.
			type dataPoint map[string]any
			points := make([]dataPoint, 0)
			if resp != nil {
				for _, raw := range *resp {
					var dp dataPoint
					if err := json.Unmarshal(raw, &dp); err != nil {
						return fmt.Errorf("decode metrics data point: %w", err)
					}
					points = append(points, dp)
				}
			}

			// Derive a stable column set from the first data point. All PVE RRD
			// data points for a single request share the same key set.
			headers := []string{"TIME", "CPU", "MEM", "MAXMEM", "DISK", "MAXDISK", "NETIN", "NETOUT"}
			rows := make([][]string, 0, len(points))
			for _, dp := range points {
				row := make([]string, len(headers))
				for i, h := range headers {
					key := colKey(h)
					if v, ok := dp[key]; ok && v != nil {
						row[i] = fmt.Sprintf("%v", v)
					} else {
						row[i] = "-"
					}
				}
				rows = append(rows, row)
			}

			res := output.Result{
				Headers: headers,
				Rows:    rows,
				Raw:     points,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&timeframe, "timeframe", "", "time frame: hour, day, week, month, year (required)")
	cmd.Flags().StringVar(&cf, "cf", "", "RRD consolidation function: AVERAGE or MAX")
	return cmd
}

// colKey maps an upper-case table header to the lower-case JSON field key
// returned by the PVE rrddata endpoint.
func colKey(header string) string {
	switch header {
	case "TIME":
		return "time"
	case "CPU":
		return "cpu"
	case "MEM":
		return "mem"
	case "MAXMEM":
		return "maxmem"
	case "DISK":
		return "disk"
	case "MAXDISK":
		return "maxdisk"
	case "NETIN":
		return "netin"
	case "NETOUT":
		return "netout"
	default:
		return header
	}
}
