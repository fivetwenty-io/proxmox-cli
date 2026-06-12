package cluster

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
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
	cmd.AddCommand(
		newCephFlagsCmd(),
		newCephMetadataCmd(),
	)
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
			deps := cli.GetDeps(cmd)
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
			deps := cli.GetDeps(cmd)
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
			deps := cli.GetDeps(cmd)
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

// newCephMetadataCmd builds `pve cluster ceph metadata`.
// It calls GET /cluster/ceph/metadata and returns per-node Ceph daemon metadata
// (monitors, OSD, MDS, managers). The optional --scope flag filters to a specific
// daemon type. On a node without a configured Ceph cluster the API returns an
// error, which is surfaced verbatim — the command itself is correct.
func newCephMetadataCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "metadata",
		Short: "Show per-node Ceph daemon metadata",
		Long: "Show per-node Ceph daemon metadata including monitors, OSDs, MDS, and managers. " +
			"Requires a configured Ceph cluster; returns an error on nodes without Ceph.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.ListCephMetadataParams{}
			if fl.Changed("scope") {
				params.Scope = &scope
			}
			resp, err := deps.API.Cluster.ListCephMetadata(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get ceph metadata: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode ceph metadata: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "",
		"filter metadata by daemon type: mon, osd, mds, mgr, or all")
	return cmd
}
