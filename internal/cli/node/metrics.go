package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// newRrddataCmd builds `pmx node rrddata` — fetches structured time-series
// performance metrics for the resolved node from the PVE RRD database.
func newRrddataCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrddata",
		Short: "Show time-series performance metrics for the node",
		Long: "Fetch structured time-series performance metrics (CPU, memory, network, " +
			"disk) for the resolved node from the PVE RRD database. --timeframe is " +
			"required; --cf is optional.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListRrddataParams{Timeframe: timeframe}
			if cmd.Flags().Changed("cf") {
				params.Cf = &cf
			}
			resp, err := deps.API.Nodes.ListRrddata(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("get rrddata on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&timeframe, "timeframe", "",
		"time frame for the metrics: hour, day, week, month, or year (required)")
	cmd.Flags().StringVar(&cf, "cf",
		"", "RRD consolidation function: AVERAGE or MAX")
	cli.MustMarkRequired(cmd, "timeframe")
	return cmd
}
