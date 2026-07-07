package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newMetricsCmd builds `pve qemu metrics <vmid> --timeframe FRAME [--cf AVG|MAX]`.
// It returns time-series data points from the PVE RRD store for cpu, mem,
// disk, and network counters. Use `pve qemu rrd` for the PNG-filename endpoint.
func newMetricsCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "metrics <vmid|name>",
		Short: "Show time-series metrics for a VM",
		Long: "Retrieve RRD time-series data points (cpu, memory, disk I/O, network) " +
			"for a VM. --timeframe is required. Optional --cf selects the RRD " +
			"consolidation function (AVERAGE or MAX); the server default is AVERAGE.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.ListQemuRrddataParams{Timeframe: timeframe}
			if cmd.Flags().Changed("cf") {
				params.Cf = strPtr(cf)
			}

			resp, err := deps.API.Nodes.ListQemuRrddata(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("metrics for VM %s on node %q: %w", vmid, node, err)
			}

			// ListQemuRrddataResponse is []json.RawMessage; decode each entry into
			// a map for table rendering. Raw preserves the full numeric structure.
			points := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range *resp {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("metrics for VM %s: decode entry: %w", vmid, err)
					}
					points = append(points, m)
				}
			}

			// One column per RRD field relevant to a VM. Block I/O is reported as
			// diskread/diskwrite rates; maxdisk is the allocated size.
			headers := []string{"TIME", "CPU", "MEM", "MAXMEM", "MAXDISK", "DISKREAD", "DISKWRITE", "NETIN", "NETOUT"}
			keys := []string{"time", "cpu", "mem", "maxmem", "maxdisk", "diskread", "diskwrite", "netin", "netout"}
			rows := make([][]string, 0, len(points))
			for _, m := range points {
				row := make([]string, len(keys))
				for i, k := range keys {
					if v, ok := m[k]; ok && v != nil {
						row[i] = stringifyValue(v)
					} else {
						row[i] = "-"
					}
				}
				rows = append(rows, row)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: points}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&timeframe, "timeframe", "", "time frame: hour, day, week, month, or year (required)")
	cmd.Flags().StringVar(&cf, "cf", "", "RRD consolidation function: AVERAGE or MAX")
	cli.MustMarkRequired(cmd, "timeframe")
	return cmd
}
