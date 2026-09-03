package cluster

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cephview"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newCephCmd builds the `pmx pve cluster ceph` sub-tree: the cluster-wide
// Ceph OSD flags (noout, noscrub, pause, and so on), status and metadata
// summaries, and a rolling restart of one daemon type across the cluster.
// These commands require a configured Ceph cluster; on nodes without Ceph the
// API returns an error which is surfaced verbatim.
func newCephCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ceph",
		Short: "Manage cluster-wide Ceph settings and rolling daemon restarts",
		Long:  "Manage cluster-wide Ceph settings. Requires a configured Ceph cluster.",
	}
	cmd.AddCommand(
		newCephFlagsCmd(),
		newCephMetadataCmd(),
		newCephStatusCmd(),
		newCephRestartBulkCmd(),
	)
	return cmd
}

// newCephStatusCmd builds `pmx pve cluster ceph status` (GET /cluster/ceph/status),
// a cluster-wide Ceph health and capacity summary. Requires a configured Ceph
// cluster; on nodes without Ceph the API returns an error, surfaced verbatim.
func newCephStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the cluster-wide Ceph status summary",
		Long: "Show the cluster-wide Ceph status: health, monitors, OSDs, pools, and " +
			"capacity. Requires a configured Ceph cluster.",
		Example: `  pmx pve cluster ceph status`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListCephStatus(cmd.Context())
			if err != nil {
				return fmt.Errorf("get ceph status: %w", err)
			}
			res, err := cephview.Status(resp)
			if err != nil {
				return fmt.Errorf("decode ceph status: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
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

// newCephFlagsSetAllCmd builds `pmx pve cluster ceph flags set-all`, which sets
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
		Example: `  pmx pve cluster ceph flags set-all --noout=true --norebalance=true`,
		Args:    cobra.NoArgs,
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
		Example: `  pmx pve cluster ceph flags set noout true`,
		Args:    cobra.ExactArgs(2),
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

// newCephMetadataCmd builds `pmx pve cluster ceph metadata`.
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
		Example: `  pmx pve cluster ceph metadata
  pmx pve cluster ceph metadata --scope osd`,
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
			res, err := cephview.ClusterMetadata(resp)
			if err != nil {
				return fmt.Errorf("decode ceph metadata: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "",
		"filter metadata by daemon type: mon, osd, mds, mgr, or all")
	return cmd
}

// cephRestartBulkServiceTypes are the daemon types POST /cluster/ceph/restart-bulk accepts.
var cephRestartBulkServiceTypes = []string{"mon", "mgr", "mds", "osd"}

// newCephRestartBulkCmd builds `pmx pve cluster ceph restart-bulk`
// (POST /cluster/ceph/restart-bulk): a rolling restart of one Ceph daemon
// type across the whole cluster, one daemon at a time.
func newCephRestartBulkCmd() *cobra.Command {
	var (
		serviceType  string
		dryRun       bool
		force        bool
		onlyOutdated bool
		timeout      int64
		waitTimeout  int64
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "restart-bulk",
		Short: "Rolling-restart one Ceph daemon type across the cluster (destructive)",
		Long: "Restart every Ceph daemon of one type across the cluster, one at a time (POST " +
			"/cluster/ceph/restart-bulk), waiting for each to come back and for the cluster to settle " +
			"before the next. --service-type selects mon, mgr, mds, or osd and is required.\n\n" +
			"--only-outdated applies to OSDs only and restarts just those whose running version differs " +
			"from the ceph-osd binary installed on their host, for a post-upgrade roll. --force proceeds " +
			"past HEALTH_WARN with non-benign checks such as PG_DEGRADED, SLOW_OPS, or MON_DOWN; HEALTH_ERR " +
			"is always fatal.\n\n" +
			"--dry-run logs the plan (which daemons, in what order) to the worker task without restarting " +
			"anything, and this command prints that log even when the worker refuses; it does not require " +
			"--yes. Every other invocation refuses to run without --yes/-y.\n\n" +
			"--timeout is the server's per-daemon bound, 30 to 1800 seconds (default 600); it also bounds " +
			"the wait for daemons on remote nodes. --wait-timeout is this command's bound on waiting for " +
			"the whole task: 0, the default, waits until the task ends; a positive value stops waiting " +
			"after that many seconds while the server keeps running the roll, and the error names the " +
			"task to follow. The global --async flag prints the task UPID and returns immediately instead.",
		Example: `  pmx pve cluster ceph restart-bulk --service-type mon --dry-run
  pmx pve cluster ceph restart-bulk --service-type mon --yes
  pmx pve cluster ceph restart-bulk --service-type osd --only-outdated --yes
  pmx pve cluster ceph restart-bulk --service-type osd --timeout 900 --wait-timeout 14400 --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			if !slices.Contains(cephRestartBulkServiceTypes, serviceType) {
				return fmt.Errorf("invalid --service-type %q: want one of %s",
					serviceType, strings.Join(cephRestartBulkServiceTypes, ", "))
			}
			if onlyOutdated && serviceType != "osd" {
				return fmt.Errorf("--only-outdated applies to --service-type osd only")
			}
			if waitTimeout < 0 {
				return fmt.Errorf("invalid --wait-timeout %d: want 0 (wait until the task ends) or a positive "+
					"number of seconds", waitTimeout)
			}
			if fl.Changed("timeout") && (timeout < 30 || timeout > 1800) {
				return fmt.Errorf("invalid --timeout %d: want 30 to 1800 seconds", timeout)
			}
			if !dryRun && !yes {
				return fmt.Errorf("refusing to rolling-restart cluster-wide Ceph %s daemons without confirmation: "+
					"pass --yes/-y", serviceType)
			}
			params := &pvecluster.CreateCephRestartBulkParams{ServiceType: serviceType}
			if fl.Changed("dry-run") {
				params.DryRun = &dryRun
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("only-outdated") {
				params.OnlyOutdated = &onlyOutdated
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}
			resp, err := deps.API.Cluster.CreateCephRestartBulk(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("rolling-restart ceph %s daemons: %w", serviceType, err)
			}
			upid, err := apiclient.UPIDFromRaw(rawOrEmpty(resp))
			if err != nil {
				return fmt.Errorf("rolling-restart ceph %s daemons: server returned no task handle: %w",
					serviceType, err)
			}
			opts := cli.WaitOptionsFor(waitTimeout)
			if dryRun {
				return renderCephBulkDryRun(cmd, deps, upid, opts)
			}
			done := fmt.Sprintf("Ceph %s daemons restarted across the cluster.", serviceType)
			if err := renderBulkTaskWait(cmd, deps, upid, done, opts); err != nil {
				return cli.RollingWaitError(
					fmt.Errorf("rolling-restart ceph %s daemons: %w", serviceType, err), opts, upid)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&serviceType, "service-type", "", "Ceph daemon type to restart cluster-wide: mon, mgr, mds, or osd")
	f.BoolVar(&dryRun, "dry-run", false, "log the plan to the task without restarting anything, then print it")
	f.BoolVar(&force, "force", false,
		"proceed past a HEALTH_WARN with non-benign checks such as PG_DEGRADED or MON_DOWN (HEALTH_ERR is fatal)")
	f.BoolVar(&onlyOutdated, "only-outdated", false,
		"OSDs only: restart only OSDs whose running version differs from the installed ceph-osd binary")
	f.Int64Var(&timeout, "timeout", 0,
		"per-daemon seconds to wait for it to come back, 30 to 1800 (server default: 600)")
	f.Int64Var(&waitTimeout, "wait-timeout", 0,
		"seconds to wait for the whole task: 0 waits until it ends; N stops waiting after N seconds while the "+
			"server keeps running the roll")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "service-type")
	return cmd
}

// renderCephBulkDryRun waits for a dry-run worker and prints its log, which
// is where the API writes the plan and, when it refuses, the reason. The log
// is fetched whether or not the worker succeeded, and the wait error is
// returned afterwards. --async prints the UPID instead.
func renderCephBulkDryRun(cmd *cobra.Command, deps *cli.Deps, upid string, opts *tasks.WaitOptions) error {
	if deps.Async {
		return renderClusterUPID(cmd, deps, upid)
	}
	waitErr := apiclient.WaitTask(cmd.Context(), deps.API, upid, opts)
	if waitErr != nil {
		waitErr = cli.RollingWaitError(fmt.Errorf("ceph rolling restart dry run: %w", waitErr), opts, upid)
	}
	parsed, err := tasks.ParseUPID(upid)
	if err != nil {
		return errors.Join(fmt.Errorf("ceph rolling restart dry run: %w", err), waitErr)
	}
	res, err := cli.TaskLogResult(cmd.Context(), deps, parsed.Node, upid, nil)
	if err != nil {
		return errors.Join(err, waitErr)
	}
	if err := deps.Out.Render(cmd.OutOrStdout(), res, deps.Format); err != nil {
		return errors.Join(err, waitErr)
	}
	return waitErr
}
