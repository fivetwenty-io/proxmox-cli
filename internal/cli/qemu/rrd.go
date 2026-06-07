package qemu

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newRrdCmd builds `pve qemu rrd <vmid> --ds DS --timeframe FRAME [--cf CF]`.
// The endpoint returns only a PNG filename on the PVE server; use
// `pve qemu metrics` to obtain numeric time-series data instead.
func newRrdCmd() *cobra.Command {
	var (
		ds        string
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrd <vmid>",
		Short: "Get the RRD PNG filename for a VM metric",
		Long: "Return the server-side path of the generated RRD graph PNG for one " +
			"or more data sources. The file is created on the PVE node; the command " +
			"prints its path. Use `pve qemu metrics` for numeric time-series data.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
				return err
			}

			params := &nodes.ListQemuRrdParams{Ds: ds, Timeframe: timeframe}
			if cmd.Flags().Changed("cf") {
				params.Cf = strPtr(cf)
			}

			resp, err := deps.API.Nodes.ListQemuRrd(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("rrd for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("rrd for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{"filename": resp.Filename}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&ds, "ds", "", "data source name(s) to graph, comma-separated (required)")
	cmd.Flags().StringVar(&timeframe, "timeframe", "", "time frame: hour, day, week, month, or year (required)")
	cmd.Flags().StringVar(&cf, "cf", "", "RRD consolidation function: AVERAGE or MAX")
	if err := cmd.MarkFlagRequired("ds"); err != nil {
		panic(fmt.Sprintf("rrd: mark --ds required: %v", err))
	}
	if err := cmd.MarkFlagRequired("timeframe"); err != nil {
		panic(fmt.Sprintf("rrd: mark --timeframe required: %v", err))
	}
	return cmd
}
