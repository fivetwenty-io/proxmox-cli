package cluster

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

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
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "restart-bulk",
		Short: "Rolling-restart one Ceph daemon type across the cluster (destructive)",
		Long: "Restart every Ceph daemon of one type across the cluster, one at a time (POST " +
			"/cluster/ceph/restart-bulk), waiting for each to come back and for the cluster to settle " +
			"before the next. --service-type selects mon, mgr, mds, or osd and is required.\n\n" +
			"--only-outdated applies to OSDs only and restarts just those whose running version differs " +
			"from the ceph-osd binary installed on their host, for a post-upgrade roll. The server refuses " +
			"--only-outdated on any node where the installed ceph-osd version cannot be read. --force " +
			"proceeds past HEALTH_WARN with non-benign checks such as PG_DEGRADED, SLOW_OPS, or MON_DOWN; " +
			"HEALTH_ERR is always fatal.\n\n" +
			"--dry-run logs the plan (which daemons, in what order) to the worker task without restarting " +
			"anything, and this command prints that log even when the worker refuses; it does not require " +
			"--yes. Every other invocation refuses to run without --yes/-y.\n\n" +
			"--timeout is the server's per-daemon bound, 30 to 1800 seconds (default 600); it also bounds " +
			"the wait for daemons on remote nodes. The global --wait-timeout flag bounds how long this " +
			"command waits for the whole task: 0, the default, waits until the task ends; a positive " +
			"value stops waiting after that many seconds while the server keeps running the roll, and " +
			"the error names the task to follow. The global --async flag prints the task UPID and " +
			"returns immediately instead.",
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
			opts := cli.WaitOptionsFor(deps.WaitTimeout)
			if dryRun {
				return cli.RenderDryRunLog(cmd, deps, upid, opts, "ceph rolling restart dry run")
			}
			done := fmt.Sprintf("Ceph %s daemons restarted across the cluster.", serviceType)
			if err := cli.RenderTaskWait(cmd, deps, upid, done, opts); err != nil {
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
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "service-type")
	return cmd
}
