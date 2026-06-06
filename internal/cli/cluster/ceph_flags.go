package cluster

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newCephCmd builds the `pve cluster ceph` sub-tree. Today it exposes the
// cluster-wide Ceph OSD flags (noout, noscrub, pause, and so on). These
// commands require a configured Ceph cluster; on nodes without Ceph the API
// returns an error which is surfaced verbatim.
func newCephCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ceph",
		Short: "Manage cluster-wide Ceph settings",
		Long:  "Manage cluster-wide Ceph settings. Requires a configured Ceph cluster.",
	}
	cmd.AddCommand(newCephFlagsCmd())
	return cmd
}

func newCephFlagsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flags",
		Short: "Inspect and set cluster-wide Ceph OSD flags",
		Long: "Inspect and set cluster-wide Ceph OSD flags such as noout, noscrub, " +
			"norebalance, and pause.",
	}
	cmd.AddCommand(
		newCephFlagsListCmd(),
		newCephFlagsGetCmd(),
		newCephFlagsSetCmd(),
	)
	return cmd
}

func newCephFlagsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all cluster-wide Ceph flags and their state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.ListCephFlags(cmd.Context())
			if err != nil {
				return fmt.Errorf("list ceph flags: %w", err)
			}
			res, err := rawUnionResult(derefRawList(resp))
			if err != nil {
				return fmt.Errorf("list ceph flags: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newCephFlagsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <flag>",
		Short: "Show the state of a single Ceph flag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			flag := args[0]
			resp, err := deps.API.Cluster.GetCephFlags(cmd.Context(), flag)
			if err != nil {
				return fmt.Errorf("get ceph flag %q: %w", flag, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get ceph flag %q: %w", flag, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newCephFlagsSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <flag> <true|false>",
		Short: "Enable or disable a single Ceph flag",
		Long: "Enable or disable a single cluster-wide Ceph flag, for example " +
			"'set noout true' to keep OSDs from being marked out during maintenance.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			flag := args[0]
			value, err := strconv.ParseBool(args[1])
			if err != nil {
				return fmt.Errorf("invalid flag value %q: want true or false", args[1])
			}
			params := &pvecluster.UpdateCephFlags2Params{Value: value}
			if err := deps.API.Cluster.UpdateCephFlags2(cmd.Context(), flag, params); err != nil {
				return fmt.Errorf("set ceph flag %q: %w", flag, err)
			}
			state := "disabled"
			if value {
				state = "enabled"
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("ceph flag %s %s.", flag, state)}, deps.Format)
		},
	}
}
