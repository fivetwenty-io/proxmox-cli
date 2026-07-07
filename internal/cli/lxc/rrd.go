package lxc

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newRrdCmd builds `pve lxc rrd <vmid> --ds DS --timeframe FRAME [--cf CF]`.
//
// The endpoint returns a PNG filename on the PVE server. This command is a
// low-level helper; for time-series data points prefer `pve lxc metrics`.
func newRrdCmd() *cobra.Command {
	var ds, timeframe, cf string

	cmd := &cobra.Command{
		Use:   "rrd <vmid|name>",
		Short: "Get the RRD PNG filename for a container",
		Long: "Return the path to a server-side RRD graph PNG for a container. " +
			"--ds and --timeframe are required. For time-series data points use `pve lxc metrics` instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			fl := cmd.Flags()
			if !fl.Changed("ds") {
				return fmt.Errorf("--ds is required: specify a comma-separated list of RRD datasource names")
			}
			if !fl.Changed("timeframe") {
				return fmt.Errorf("--timeframe is required: specify hour, day, week, month, or year")
			}

			params := &nodes.ListLxcRrdParams{Ds: ds, Timeframe: timeframe}
			if fl.Changed("cf") {
				params.Cf = &cf
			}

			resp, err := deps.API.Nodes.ListLxcRrd(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("get RRD graph for container %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get RRD graph for container %s on node %q: empty response", vmid, node)
			}

			res := output.Result{
				Single: map[string]string{"filename": resp.Filename},
				Raw:    resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&ds, "ds", "", "comma-separated list of RRD datasource names (required)")
	cmd.Flags().StringVar(&timeframe, "timeframe", "", "time frame: hour, day, week, month, year (required)")
	cmd.Flags().StringVar(&cf, "cf", "", "RRD consolidation function: AVERAGE or MAX")
	return cmd
}
