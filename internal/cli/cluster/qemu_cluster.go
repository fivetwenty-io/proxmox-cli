package cluster

import (
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newClusterQemuCmd builds the `pve cluster qemu` sub-tree: cluster-wide QEMU
// capability inspection commands.
func newClusterQemuCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qemu",
		Short: "Cluster-wide QEMU capabilities",
		Long: "Inspect cluster-wide QEMU capabilities. Currently exposes the set of " +
			"CPU flags available across the cluster.",
	}
	cmd.AddCommand(newClusterQemuCpuFlagsCmd())
	return cmd
}

// newClusterQemuCpuFlagsCmd builds `pve cluster qemu cpu-flags`. It calls
// GET /cluster/qemu/cpu-flags and renders the list of available CPU flags.
// Both --accel and --arch are optional; omitting them returns all flags.
func newClusterQemuCpuFlagsCmd() *cobra.Command {
	var (
		accel string
		arch  string
	)
	cmd := &cobra.Command{
		Use:   "cpu-flags",
		Short: "List cluster-wide QEMU CPU flags",
		Long: "List CPU flags available across the cluster. Pass --accel to filter by " +
			"acceleration type, or --arch to filter by virtual processor architecture.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &pvecluster.ListQemuCpuFlagsParams{}
			fl := cmd.Flags()
			if fl.Changed("accel") {
				params.Accel = &accel
			}
			if fl.Changed("arch") {
				params.Arch = &arch
			}
			resp, err := deps.API.Cluster.ListQemuCpuFlags(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list QEMU CPU flags: %w", err)
			}
			res, err := rawUnionResult(derefRawList(resp))
			if err != nil {
				return fmt.Errorf("list QEMU CPU flags: %w", err)
			}
			res.Raw = resp
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&accel, "accel", "", "filter by acceleration type (e.g. kvm)")
	cmd.Flags().StringVar(&arch, "arch", "", "filter by virtual processor architecture (e.g. x86_64)")
	return cmd
}
