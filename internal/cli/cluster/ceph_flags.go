package cluster

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newCephCmd builds the `pmx cluster ceph` sub-tree. Today it exposes the
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
		newCephStatusCmd(),
	)
	return cmd
}

// newCephStatusCmd builds `pmx cluster ceph status` (GET /cluster/ceph/status),
// a cluster-wide Ceph health and capacity summary. Requires a configured Ceph
// cluster; on nodes without Ceph the API returns an error, surfaced verbatim.
func newCephStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the cluster-wide Ceph status summary",
		Long: "Show the cluster-wide Ceph status: health, monitors, OSDs, pools, and " +
			"capacity. Requires a configured Ceph cluster.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListCephStatus(cmd.Context())
			if err != nil {
				return fmt.Errorf("get ceph status: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode ceph status: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
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
		newCephFlagsSetAllCmd(),
	)
	return cmd
}

// cephFlagSpec maps a CLI flag name to a setter on UpdateCephFlagsParams.
type cephFlagSpec struct {
	name  string
	help  string
	apply func(p *pvecluster.UpdateCephFlagsParams, v bool)
}

// cephFlagSpecs enumerates the cluster-wide Ceph OSD flags that the bulk set-all
// command can toggle, in the order they appear in the help text.
var cephFlagSpecs = []cephFlagSpec{
	{"nobackfill", "suspend backfilling of PGs", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Nobackfill = &v }},
	{"nodeep-scrub", "disable deep scrubbing", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.NodeepScrub = &v }},
	{"nodown", "ignore OSD failure reports", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Nodown = &v }},
	{"noin", "do not mark previously-out OSDs back in", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Noin = &v }},
	{"noout", "do not mark OSDs out after the interval", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Noout = &v }},
	{"norebalance", "suspend rebalancing of PGs", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Norebalance = &v }},
	{"norecover", "suspend recovery of PGs", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Norecover = &v }},
	{"noscrub", "disable scrubbing", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Noscrub = &v }},
	{"notieragent", "suspend cache tiering activity", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Notieragent = &v }},
	{"noup", "do not allow OSDs to start", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Noup = &v }},
	{"pause", "pause reads and writes", func(p *pvecluster.UpdateCephFlagsParams, v bool) { p.Pause = &v }},
}

// newCephFlagsSetAllCmd builds `pmx cluster ceph flags set-all`, which sets
// several cluster-wide Ceph OSD flags in a single atomic request. Only the
// flags passed are changed.
func newCephFlagsSetAllCmd() *cobra.Command {
	vals := make([]bool, len(cephFlagSpecs))
	cmd := &cobra.Command{
		Use:   "set-all",
		Short: "Set multiple cluster-wide Ceph flags atomically",
		Long: "Enable or disable several cluster-wide Ceph OSD flags in a single request, " +
			"for example 'set-all --noout=true --norebalance=true' during maintenance. " +
			"Only the flags you pass are changed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.UpdateCephFlagsParams{}
			changed := 0
			for i, spec := range cephFlagSpecs {
				if fl.Changed(spec.name) {
					spec.apply(params, vals[i])
					changed++
				}
			}
			if changed == 0 {
				return fmt.Errorf("no flags to set: pass at least one flag, for example --noout=true")
			}
			if _, err := deps.API.Cluster.UpdateCephFlags(cmd.Context(), params); err != nil {
				return fmt.Errorf("set ceph flags: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%d ceph flag(s) updated.", changed)}, deps.Format)
		},
	}
	for i, spec := range cephFlagSpecs {
		cmd.Flags().BoolVar(&vals[i], spec.name, false, spec.help)
	}
	return cmd
}

func newCephFlagsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all cluster-wide Ceph flags and their state",
		Long: "List all cluster-wide Ceph OSD flags and whether each is currently set. " +
			"Requires a configured Ceph cluster.",
		Example: `  pmx pve cluster ceph flags list`,
		Args:    cobra.NoArgs,
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
		Long: "Show whether a single cluster-wide Ceph OSD flag is currently set, for " +
			"example noout or noscrub. Requires a configured Ceph cluster.",
		Example: `  pmx pve cluster ceph flags get noout`,
		Args:    cobra.ExactArgs(1),
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

// newCephMetadataCmd builds `pmx cluster ceph metadata`.
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
