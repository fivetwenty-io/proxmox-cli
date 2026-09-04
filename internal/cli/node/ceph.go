package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cephview"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// renderRawMessage renders a *json.RawMessage response by inspecting the JSON
// shape at runtime: object → Single key/value map, array → scan table,
// string → Message text, other → Message with raw bytes.
func renderRawMessage(cmd *cobra.Command, deps *cli.Deps, resp *json.RawMessage) error {
	if resp == nil {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{}, deps.Format)
	}
	raw := []byte(*resp)
	// Try object.
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		single := make(map[string]string, len(obj))
		for k, v := range obj {
			single[k] = output.Cell(v)
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: obj}, deps.Format)
	}
	// Try array.
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return renderScan(cmd, deps, arr, arr)
	}
	// Try string (text content, e.g. ceph.conf or CRUSH map text).
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: s, Raw: s}, deps.Format)
	}
	// Fallback: render raw bytes as-is.
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: string(raw), Raw: raw}, deps.Format)
}

// renderCephView renders a Ceph payload through one of the curated views in
// internal/cephview. Every one of these endpoints answers with Ceph's own
// nested JSON, which the generic renderer can only flatten; the view selects
// what the command is asked for and leaves the whole payload to -o json.
func renderCephView(cmd *cobra.Command, deps *cli.Deps,
	view func(any) (output.Result, error), resp any, what string) error {
	res, err := view(resp)
	if err != nil {
		return fmt.Errorf("decode %s on node %q: %w", what, deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// renderCephTask renders the asynchronous task started by a Ceph operation. The
// write endpoints return a worker UPID; honour --async and otherwise block on
// the task, but tolerate a non-UPID or empty body by falling back to a plain
// success message.
func renderCephTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
	}
	return renderCephTaskWait(cmd, deps, upid, doneMsg, nil)
}

// renderCephTaskWait waits for the Ceph worker identified by upid and names
// the node in any error the wait produces. The waiting and the rendering are
// cli.RenderTaskWait's, so under --async this prints the UPID and returns, and
// otherwise it prints doneMsg once the task finishes. All this function adds
// is the "ceph operation on node" prefix around that failure.
//
// The opts argument bounds the wait. Every ordinary Ceph verb passes nil,
// so the operator's --wait-timeout applies and the wait runs until the task ends
// unless the operator asked for a shorter bound. The rolling restart builds
// its own opts from that same flag, because it has to name the bound again in
// the deadline message it writes when the wait runs out.
func renderCephTaskWait(cmd *cobra.Command, deps *cli.Deps, upid, doneMsg string, opts *tasks.WaitOptions) error {
	if err := cli.RenderTaskWait(cmd, deps, upid, doneMsg, opts); err != nil {
		return fmt.Errorf("ceph operation on node %q: %w", deps.Node, err)
	}
	return nil
}

func newCephCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ceph",
		Short: "Inspect and manage the node's Ceph services",
		Long: "Show Ceph cluster status and configuration, manage OSDs, pools, monitors, " +
			"managers, metadata servers, and filesystems, and control Ceph services on the resolved node. " +
			"Every create, delete, and service-control verb is destructive and requires --yes.",
	}
	cmd.AddCommand(
		newCephStatusCmd(),
		newCephReleasesCmd(),
		newCephCmdSafetyCmd(),
		newCephCfgCmd(),
		newCephLogCmd(),
		newCephRulesCmd(),
		newCephCrushCmd(),
		newCephOsdCmd(),
		newCephPoolCmd(),
		newCephMonCmd(),
		newCephMdsCmd(),
		newCephMgrCmd(),
		newCephFsCmd(),
		newCephInitCmd(),
		newCephStartCmd(),
		newCephStopCmd(),
		newCephRestartCmd(),
		newCephRestartBulkCmd(),
	)
	return cmd
}

func newCephStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show the Ceph cluster status",
		Long:    "Report the overall Ceph cluster health, monitor quorum, OSD map, and placement-group state as seen from the resolved node.",
		Example: `  pmx pve node ceph status`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephStatus(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get Ceph status on node %q: %w", deps.Node, err)
			}
			return renderCephView(cmd, deps, cephview.Status, resp, "Ceph status")
		},
	}
}

// newCephReleasesCmd builds `pmx pve node ceph releases`
// (GET /nodes/{node}/ceph/releases): the Ceph releases this node's package
// sources offer, with availability, default, and support flags.
func newCephReleasesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "releases",
		Short: "List the Ceph releases available to install on the node",
		Long: "List the Ceph releases the node's package sources know about (GET " +
			"/nodes/{node}/ceph/releases). AVAILABLE says whether the release has packages " +
			"for this node's architecture and Proxmox VE version, DEFAULT marks the release " +
			"recommended for new installations, and UNSUPPORTED marks a release past support. " +
			"Read-only; does not require a configured Ceph cluster.",
		Example: `  pmx pve node ceph releases
  pmx pve node ceph releases -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephReleases(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list ceph releases on node %q: %w", deps.Node, err)
			}
			return renderCephView(cmd, deps, cephview.Releases, resp, "ceph releases")
		},
	}
}

func newCephCmdSafetyCmd() *cobra.Command {
	var (
		action  string
		id      string
		service string
	)
	cmd := &cobra.Command{
		Use:   "cmd-safety",
		Short: "Check whether a Ceph action is safe to perform",
		Long: "Ask Ceph's own heuristics whether the given action on the given service would be " +
			"safe to perform right now, as seen from the resolved node.",
		Example: `  pmx pve node ceph cmd-safety --action stop --id osd.0 --service osd`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephCmdSafety(cmd.Context(), deps.Node,
				&nodes.ListCephCmdSafetyParams{Action: action, Id: id, Service: service})
			if err != nil {
				return fmt.Errorf("check Ceph command safety on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	f := cmd.Flags()
	f.StringVar(&action, "action", "", "action to check: stop or destroy (required)")
	f.StringVar(&id, "id", "", "ID of the service to check, for example osd.0 or mon.pve1 (required)")
	f.StringVar(&service, "service", "", "service type to check: osd, mon, or mds (required)")
	cli.MustMarkRequired(cmd, "action")
	cli.MustMarkRequired(cmd, "id")
	cli.MustMarkRequired(cmd, "service")
	return cmd
}

func newCephCfgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cfg",
		Short: "Inspect the Ceph configuration database and files",
		Long: "Show the Ceph configuration index, the config-DB overrides, the raw ceph.conf text, " +
			"or individual per-section configuration values.\n\n" +
			"With no subcommand, lists the configuration index (same as `cfg index`).",
		Example: `  pmx pve node ceph cfg`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCephCfgIndex(cmd)
		},
	}
	cmd.AddCommand(
		newCephCfgIndexCmd(),
		newCephCfgDbCmd(),
		newCephCfgRawCmd(),
		newCephCfgValueCmd(),
	)
	return cmd
}

// runCephCfgIndex lists the Ceph configuration sections and keys for the
// resolved node. Shared by the `cfg` group (no subcommand) and `cfg index`.
func runCephCfgIndex(cmd *cobra.Command) error {
	deps := cli.GetDeps(cmd)
	if err := requireNode(deps); err != nil {
		return err
	}
	resp, err := deps.API.Nodes.ListCephCfg(cmd.Context(), deps.Node)
	if err != nil {
		return fmt.Errorf("list Ceph configuration on node %q: %w", deps.Node, err)
	}
	return renderScan(cmd, deps, derefRaws(resp), resp)
}

// newCephCfgIndexCmd preserves the original `cfg` leaf behaviour: lists the
// Ceph configuration sections and keys from the config API.
func newCephCfgIndexCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "index",
		Short:   "List all Ceph configuration sections and keys",
		Long:    "Show the Ceph configuration entries (ceph.conf and the config database) as seen from the resolved node.",
		Example: `  pmx pve node ceph cfg index`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCephCfgIndex(cmd)
		},
	}
}

func newCephCfgDbCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "db",
		Short:   "Show the Ceph configuration-DB overrides",
		Long:    "List the per-daemon configuration overrides stored in the Ceph config-DB on the resolved node.",
		Example: `  pmx pve node ceph cfg db`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephCfgDb(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get Ceph config-DB on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newCephCfgRawCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "raw",
		Short:   "Show the raw ceph.conf text",
		Long:    "Retrieve the full contents of ceph.conf as plain text from the resolved node.",
		Example: `  pmx pve node ceph cfg raw`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephCfgRaw(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get raw Ceph configuration on node %q: %w", deps.Node, err)
			}
			return renderRawMessage(cmd, deps, resp)
		},
	}
}

func newCephCfgValueCmd() *cobra.Command {
	var keys string
	cmd := &cobra.Command{
		Use:   "value",
		Short: "Look up specific Ceph configuration values",
		Long: "Retrieve the value of one or more Ceph configuration keys. Each key must be " +
			"given as <section>:<key>; separate multiple keys with a semicolon, comma, or space.",
		Example: `  pmx pve node ceph cfg value --keys global:fsid`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephCfgValue(cmd.Context(), deps.Node,
				&nodes.ListCephCfgValueParams{ConfigKeys: keys})
			if err != nil {
				return fmt.Errorf("get Ceph configuration values on node %q: %w", deps.Node, err)
			}
			return renderRawMessage(cmd, deps, resp)
		},
	}
	cmd.Flags().StringVar(&keys, "keys", "",
		"configuration keys to look up, each as <section>:<key>, separated by semicolon, comma, or space (required)")
	cli.MustMarkRequired(cmd, "keys")
	return cmd
}

// newCephLogCmd builds `pmx pve node ceph log`.
func newCephLogCmd() *cobra.Command {
	var (
		limit int64
		start int64
	)
	cmd := &cobra.Command{
		Use:     "log",
		Short:   "Show the Ceph cluster log",
		Long:    "Retrieve recent Ceph log lines from the resolved node. Use --limit and --start to page through the log.",
		Example: `  pmx pve node ceph log --limit 50`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListCephLogParams{}
			fl := cmd.Flags()
			if fl.Changed("limit") {
				params.Limit = &limit
			}
			if fl.Changed("start") {
				params.Start = &start
			}
			resp, err := deps.API.Nodes.ListCephLog(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("get Ceph log on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&limit, "limit", 0, "maximum number of log lines to return")
	f.Int64Var(&start, "start", 0, "first log line index to return")
	return cmd
}

// newCephRulesCmd builds `pmx pve node ceph rules`.
func newCephRulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rules",
		Short:   "List CRUSH rules in the Ceph cluster",
		Long:    "Show all CRUSH rules defined in the Ceph cluster as seen from the resolved node.",
		Example: `  pmx pve node ceph rules`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephRules(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list Ceph CRUSH rules on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

// newCephCrushCmd builds `pmx pve node ceph crush`.
func newCephCrushCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "crush",
		Short:   "Show the CRUSH map text",
		Long:    "Retrieve the full CRUSH map as plain text from the resolved node.",
		Example: `  pmx pve node ceph crush`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephCrush(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get Ceph CRUSH map on node %q: %w", deps.Node, err)
			}
			return renderRawMessage(cmd, deps, resp)
		},
	}
}

func newCephInitCmd() *cobra.Command {
	var (
		network        string
		clusterNetwork string
		size           int64
		minSize        int64
		pgBits         int64
		disableCephx   bool
		yes            bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the Ceph cluster configuration on the node (destructive)",
		Long: "Create the initial Ceph configuration on the resolved node. This is a one-time, " +
			"cluster-wide operation that lays down ceph.conf and the cluster keys.",
		Example: `  pmx pve node ceph init --yes
  pmx pve node ceph init --size 3 --min-size 2 --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "initialize Ceph"); err != nil {
				return err
			}
			params := &nodes.CreateCephInitParams{}
			fl := cmd.Flags()
			if fl.Changed("network") {
				params.Network = &network
			}
			if fl.Changed("cluster-network") {
				params.ClusterNetwork = &clusterNetwork
			}
			if fl.Changed("size") {
				params.Size = &size
			}
			if fl.Changed("min-size") {
				params.MinSize = &minSize
			}
			if fl.Changed("pg-bits") {
				params.PgBits = &pgBits
			}
			if fl.Changed("disable-cephx") {
				params.DisableCephx = &disableCephx
			}
			if err := deps.API.Nodes.CreateCephInit(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("initialize Ceph on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Ceph initialized on node %q.", deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "", "use a specific network for all Ceph traffic")
	f.StringVar(&clusterNetwork, "cluster-network", "", "separate cluster network for OSD replication and recovery traffic")
	f.Int64Var(&size, "size", 0, "targeted number of replicas per object")
	f.Int64Var(&minSize, "min-size", 0, "minimum number of available replicas to allow I/O")
	f.Int64Var(&pgBits, "pg-bits", 0, "placement-group bits (deprecated)")
	f.BoolVar(&disableCephx, "disable-cephx", false, "disable cephx authentication (insecure; only on a trusted private network)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newCephCtlCmd builds a Ceph service-control verb (start/stop/restart). Each
// targets an optional service and returns a worker UPID. long and example are
// threaded per-verb by the caller since the three verbs share this builder but
// need distinct docs.
func newCephCtlCmd(use, short, long, example, action string,
	do func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error),
) *cobra.Command {
	var (
		service string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, action); err != nil {
				return err
			}
			var svc *string
			if cmd.Flags().Changed("service") {
				svc = &service
			}
			resp, err := do(deps, cmd.Context(), svc)
			if err != nil {
				return fmt.Errorf("%s on node %q: %w", action, deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph %s issued on node %q.", use, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&service, "service", "", "restrict to a specific Ceph service, for example osd.0 or mon.pve")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the service-control operation without prompting")
	return cmd
}

func newCephStartCmd() *cobra.Command {
	return newCephCtlCmd("start", "Start Ceph services on the node (destructive)",
		"Start Ceph services on the resolved node. Refuses to run without --yes/-y.\n\n"+
			"Restrict the action to one service with --service, naming it as osd.0 or "+
			"mon.pve. Without that flag, every Ceph service on the node is started.\n\n"+
			"The command submits a worker task and blocks until it completes; the global "+
			"--async flag prints the task UPID and returns immediately.",
		`  pmx pve node ceph start --yes
  pmx pve node ceph start --service osd.0 --yes`,
		"start Ceph services",
		func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error) {
			return deps.API.Nodes.CreateCephStart(ctx, deps.Node, &nodes.CreateCephStartParams{Service: service})
		})
}

func newCephStopCmd() *cobra.Command {
	return newCephCtlCmd("stop", "Stop Ceph services on the node (destructive)",
		"Stop Ceph services on the resolved node. Refuses to run without --yes/-y.\n\n"+
			"Restrict the action to one service with --service, naming it as osd.0 or "+
			"mon.pve. Without that flag, every Ceph service on the node is stopped.\n\n"+
			"The command submits a worker task and blocks until it completes; the global "+
			"--async flag prints the task UPID and returns immediately.",
		`  pmx pve node ceph stop --yes
  pmx pve node ceph stop --service osd.0 --yes`,
		"stop Ceph services",
		func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error) {
			return deps.API.Nodes.CreateCephStop(ctx, deps.Node, &nodes.CreateCephStopParams{Service: service})
		})
}

func newCephRestartCmd() *cobra.Command {
	return newCephCtlCmd("restart", "Restart Ceph services on the node (destructive)",
		"Restart Ceph services on the resolved node. Refuses to run without --yes/-y.\n\n"+
			"Restrict the action to one service with --service, naming it as osd.0 or "+
			"mon.pve. Without that flag, every Ceph service on the node is restarted.\n\n"+
			"The command submits a worker task and blocks until it completes; the global "+
			"--async flag prints the task UPID and returns immediately.",
		`  pmx pve node ceph restart --yes
  pmx pve node ceph restart --service osd.0 --yes`,
		"restart Ceph services",
		func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error) {
			return deps.API.Nodes.CreateCephRestart(ctx, deps.Node, &nodes.CreateCephRestartParams{Service: service})
		})
}

// newCephRestartBulkCmd builds `pmx pve node ceph restart-bulk`
// (POST /nodes/{node}/ceph/restart-bulk): a rolling restart of the node's
// OSDs, one at a time, waiting for each to come back and for recovery to
// quiesce before moving on. The node is resolved like every other node ceph
// write verb (--node, $PMX_NODE, or the context default) and --yes gates it.
func newCephRestartBulkCmd() *cobra.Command {
	var (
		serviceType  string
		dryRun       bool
		force        bool
		onlyOutdated bool
		resume       bool
		setNoout     bool
		timeout      int64
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "restart-bulk",
		Short: "Rolling-restart the node's Ceph OSDs one at a time (destructive)",
		Long: "Restart the resolved node's Ceph OSDs one at a time (POST /nodes/{node}/ceph/restart-bulk), " +
			"waiting for each OSD to come back up and for recovery to quiesce before the next. Only OSDs " +
			"can be rolling-restarted per node; use 'pmx pve cluster ceph restart-bulk' for monitors, " +
			"managers, metadata servers, or every OSD in the cluster.\n\n" +
			"The server sets the noout flag on each targeted OSD for the duration of the run and clears it " +
			"afterwards; pass --set-noout=false to leave the flag alone. --only-outdated restarts only OSDs " +
			"whose running version differs from the installed ceph-osd binary, for a post-upgrade roll. " +
			"--resume continues an aborted run from the checkpoint stored in Ceph's config-key store; " +
			"--resume replays the saved plan, so --set-noout and --only-outdated are ignored when resuming. " +
			"Without --resume the server refuses to start while a checkpoint from an aborted run exists, " +
			"and --only-outdated refuses when the installed ceph-osd version cannot be read. --force " +
			"proceeds past HEALTH_WARN with non-benign checks such as PG_DEGRADED or SLOW_OPS; " +
			"HEALTH_ERR is always fatal.\n\n" +
			"--dry-run logs the plan (which OSDs, in what order) to the worker task without restarting " +
			"anything, and this command prints that log even when the worker refuses; it does not require " +
			"--yes. Every other invocation refuses to run without --yes/-y.\n\n" +
			"--timeout is the server's per-OSD bound, 30 to 1800 seconds (default 600). The global " +
			"--wait-timeout flag bounds how long this command waits for the whole task: 0, the default, " +
			"waits until the task ends; a positive value stops waiting after that many seconds while the " +
			"server keeps running the roll, and the error names the task to follow. The global --async " +
			"flag prints the task UPID and returns immediately instead.",
		Example: `  pmx pve node ceph restart-bulk --dry-run
  pmx pve node ceph restart-bulk --yes
  pmx pve node ceph restart-bulk --only-outdated --yes
  pmx pve node ceph restart-bulk --resume --yes
  pmx pve node ceph restart-bulk --timeout 900 --wait-timeout 14400 --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			if serviceType != "osd" {
				return fmt.Errorf("invalid --service-type %q: only osd can be rolling-restarted per node "+
					"(use 'pmx pve cluster ceph restart-bulk' for mon, mgr, or mds)", serviceType)
			}
			if fl.Changed("timeout") && (timeout < 30 || timeout > 1800) {
				return fmt.Errorf("invalid --timeout %d: want 30 to 1800 seconds", timeout)
			}
			if !dryRun {
				if err := requireSystemYes(deps.Node, yes, "rolling-restart Ceph OSDs"); err != nil {
					return err
				}
			}
			params := &nodes.CreateCephRestartBulkParams{ServiceType: serviceType}
			if fl.Changed("dry-run") {
				params.DryRun = &dryRun
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("only-outdated") {
				params.OnlyOutdated = &onlyOutdated
			}
			if fl.Changed("resume") {
				params.Resume = &resume
			}
			if fl.Changed("set-noout") {
				params.SetNoout = &setNoout
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}
			resp, err := deps.API.Nodes.CreateCephRestartBulk(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("rolling-restart ceph osds on node %q: %w", deps.Node, err)
			}
			upid, err := apiclient.UPIDFromRaw(rawOrNil(resp))
			if err != nil {
				return fmt.Errorf("rolling-restart ceph osds on node %q: server returned no task handle: %w",
					deps.Node, err)
			}
			opts := cli.WaitOptionsFor(deps.WaitTimeout)
			if dryRun {
				return cli.RenderDryRunLog(cmd, deps, upid, opts, fmt.Sprintf("ceph dry run on node %q", deps.Node))
			}
			done := fmt.Sprintf("Ceph OSDs on node %q restarted.", deps.Node)
			if err := renderCephTaskWait(cmd, deps, upid, done, opts); err != nil {
				return cli.RollingWaitError(err, opts, upid)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&serviceType, "service-type", "osd", "Ceph daemon type to restart; only osd is valid per node")
	f.BoolVar(&dryRun, "dry-run", false, "log the plan to the task without restarting anything, then print it")
	f.BoolVar(&force, "force", false,
		"proceed past a HEALTH_WARN with non-benign checks such as PG_DEGRADED or SLOW_OPS (HEALTH_ERR is fatal)")
	f.BoolVar(&onlyOutdated, "only-outdated", false,
		"restart only OSDs whose running version differs from the installed ceph-osd binary (ignored with --resume)")
	f.BoolVar(&resume, "resume", false,
		"resume an aborted run from its checkpoint in Ceph's config-key store, replaying the saved plan")
	f.BoolVar(&setNoout, "set-noout", true,
		"set noout on each targeted OSD for the run and clear it afterwards (server default: true; ignored with "+
			"--resume)")
	f.Int64Var(&timeout, "timeout", 0,
		"per-OSD seconds to wait for it to come back up and for recovery to quiesce, 30 to 1800 (server default: 600)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
