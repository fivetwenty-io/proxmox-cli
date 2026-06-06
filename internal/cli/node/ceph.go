package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// renderCephTask renders the asynchronous task started by a Ceph operation. The
// write endpoints return a worker UPID; honour --async and otherwise block on
// the task, but tolerate a non-UPID or empty body by falling back to a plain
// success message.
func renderCephTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
	}
	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return fmt.Errorf("ceph operation on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
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
		newCephCfgCmd(),
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
	)
	return cmd
}

func newCephStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the Ceph cluster status",
		Long:  "Report the overall Ceph cluster health, monitor quorum, OSD map, and placement-group state as seen from the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephStatus(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get Ceph status on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newCephCfgCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cfg",
		Short: "Show the Ceph configuration database",
		Long:  "Show the Ceph configuration entries (ceph.conf and the config database) as seen from the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephCfg(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get Ceph configuration on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
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
// targets an optional service and returns a worker UPID.
func newCephCtlCmd(use, short, action string,
	do func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error),
) *cobra.Command {
	var (
		service string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
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
	return newCephCtlCmd("start", "Start Ceph services on the node (destructive)", "start Ceph services",
		func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error) {
			return deps.API.Nodes.CreateCephStart(ctx, deps.Node, &nodes.CreateCephStartParams{Service: service})
		})
}

func newCephStopCmd() *cobra.Command {
	return newCephCtlCmd("stop", "Stop Ceph services on the node (destructive)", "stop Ceph services",
		func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error) {
			return deps.API.Nodes.CreateCephStop(ctx, deps.Node, &nodes.CreateCephStopParams{Service: service})
		})
}

func newCephRestartCmd() *cobra.Command {
	return newCephCtlCmd("restart", "Restart Ceph services on the node (destructive)", "restart Ceph services",
		func(deps *cli.Deps, ctx context.Context, service *string) (*json.RawMessage, error) {
			return deps.API.Nodes.CreateCephRestart(ctx, deps.Node, &nodes.CreateCephRestartParams{Service: service})
		})
}
