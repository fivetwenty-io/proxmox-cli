package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

func newCephPoolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Inspect and manage Ceph pools",
		Long:  "List Ceph pools, inspect a pool's parameters and status, and create, update, or destroy pools.",
	}
	cmd.AddCommand(
		newCephPoolListCmd(),
		newCephPoolGetCmd(),
		newCephPoolStatusCmd(),
		newCephPoolCreateCmd(),
		newCephPoolSetCmd(),
		newCephPoolDeleteCmd(),
	)
	return cmd
}

func newCephPoolListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Ceph pools",
		Long:  "List every Ceph pool defined on the cluster as seen from the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephPool(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list Ceph pools on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

// newCephPoolGetCmd builds `pve node ceph pool get <name>`.
//
// GET /nodes/{node}/ceph/pool/{name} is only a directory index (status); the
// pool parameters live at the status child endpoint.
func newCephPoolGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show a Ceph pool's parameters",
		Long:  "Show the configured parameters for the given Ceph pool on the resolved node.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephPoolStatus(cmd.Context(), deps.Node, args[0],
				&nodes.ListCephPoolStatusParams{})
			if err != nil {
				return fmt.Errorf("get Ceph pool %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newCephPoolStatusCmd() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show a Ceph pool's status",
		Long:  "Show runtime status for the given Ceph pool, optionally including detailed statistics with --verbose.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListCephPoolStatusParams{}
			if cmd.Flags().Changed("verbose") {
				params.Verbose = &verbose
			}
			resp, err := deps.API.Nodes.ListCephPoolStatus(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("get Ceph pool %q status on node %q: %w", args[0], deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "include additional statistics")
	return cmd
}

func newCephPoolCreateCmd() *cobra.Command {
	var (
		application     string
		crushRule       string
		erasureCoding   string
		pgAutoscaleMode string
		targetSize      string
		minSize         int64
		size            int64
		pgNum           int64
		pgNumMin        int64
		targetSizeRatio float64
		addStorages     bool
		yes             bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a Ceph pool (destructive)",
		Long:  "Create a new Ceph pool. Pool creation consumes cluster capacity and may reconfigure storage.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("create Ceph pool %q", args[0])); err != nil {
				return err
			}
			params := &nodes.CreateCephPoolParams{Name: args[0]}
			fl := cmd.Flags()
			if fl.Changed("application") {
				params.Application = &application
			}
			if fl.Changed("crush-rule") {
				params.CrushRule = &crushRule
			}
			if fl.Changed("erasure-coding") {
				params.ErasureCoding = &erasureCoding
			}
			if fl.Changed("pg-autoscale-mode") {
				params.PgAutoscaleMode = &pgAutoscaleMode
			}
			if fl.Changed("target-size") {
				params.TargetSize = &targetSize
			}
			if fl.Changed("min-size") {
				params.MinSize = &minSize
			}
			if fl.Changed("size") {
				params.Size = &size
			}
			if fl.Changed("pg-num") {
				params.PgNum = &pgNum
			}
			if fl.Changed("pg-num-min") {
				params.PgNumMin = &pgNumMin
			}
			if fl.Changed("target-size-ratio") {
				params.TargetSizeRatio = &targetSizeRatio
			}
			if fl.Changed("add-storages") {
				params.AddStorages = &addStorages
			}
			resp, err := deps.API.Nodes.CreateCephPool(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create Ceph pool %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph pool %q created on node %q.", args[0], deps.Node))
		},
	}
	registerCephPoolFlags(cmd, &application, &crushRule, &pgAutoscaleMode, &targetSize,
		&minSize, &size, &pgNum, &pgNumMin, &targetSizeRatio)
	f := cmd.Flags()
	f.StringVar(&erasureCoding, "erasure-coding", "", "create an erasure-coded pool with a replicated metadata pool")
	f.BoolVar(&addStorages, "add-storages", false, "configure VM and CT storage using the new pool")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newCephPoolSetCmd() *cobra.Command {
	var (
		application     string
		crushRule       string
		pgAutoscaleMode string
		targetSize      string
		minSize         int64
		size            int64
		pgNum           int64
		pgNumMin        int64
		targetSizeRatio float64
		yes             bool
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update a Ceph pool's parameters (destructive)",
		Long:  "Update one or more parameters of an existing Ceph pool. Pass at least one field flag.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			poolFields := []string{
				"application", "crush-rule", "pg-autoscale-mode", "target-size",
				"min-size", "size", "pg-num", "pg-num-min", "target-size-ratio",
			}
			if !anyFlagChanged(fl, poolFields...) {
				return fmt.Errorf("no changes to set: pass at least one field flag")
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("update Ceph pool %q", args[0])); err != nil {
				return err
			}
			params := &nodes.UpdateCephPoolParams{}
			if fl.Changed("application") {
				params.Application = &application
			}
			if fl.Changed("crush-rule") {
				params.CrushRule = &crushRule
			}
			if fl.Changed("pg-autoscale-mode") {
				params.PgAutoscaleMode = &pgAutoscaleMode
			}
			if fl.Changed("target-size") {
				params.TargetSize = &targetSize
			}
			if fl.Changed("min-size") {
				params.MinSize = &minSize
			}
			if fl.Changed("size") {
				params.Size = &size
			}
			if fl.Changed("pg-num") {
				params.PgNum = &pgNum
			}
			if fl.Changed("pg-num-min") {
				params.PgNumMin = &pgNumMin
			}
			if fl.Changed("target-size-ratio") {
				params.TargetSizeRatio = &targetSizeRatio
			}
			resp, err := deps.API.Nodes.UpdateCephPool(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("update Ceph pool %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph pool %q updated on node %q.", args[0], deps.Node))
		},
	}
	registerCephPoolFlags(cmd, &application, &crushRule, &pgAutoscaleMode, &targetSize,
		&minSize, &size, &pgNum, &pgNumMin, &targetSizeRatio)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the operation without prompting")
	return cmd
}

// registerCephPoolFlags registers the parameter flags shared by pool create and
// pool set.
func registerCephPoolFlags(cmd *cobra.Command,
	application, crushRule, pgAutoscaleMode, targetSize *string,
	minSize, size, pgNum, pgNumMin *int64, targetSizeRatio *float64,
) {
	f := cmd.Flags()
	f.StringVar(application, "application", "", "application of the pool (rbd, cephfs, rgw)")
	f.StringVar(crushRule, "crush-rule", "", "CRUSH rule for object placement")
	f.StringVar(pgAutoscaleMode, "pg-autoscale-mode", "", "automatic PG scaling mode (on, off, warn)")
	f.StringVar(targetSize, "target-size", "", "estimated target size for the PG autoscaler")
	f.Int64Var(minSize, "min-size", 0, "minimum number of replicas per object")
	f.Int64Var(size, "size", 0, "number of replicas per object")
	f.Int64Var(pgNum, "pg-num", 0, "number of placement groups")
	f.Int64Var(pgNumMin, "pg-num-min", 0, "minimum number of placement groups")
	f.Float64Var(targetSizeRatio, "target-size-ratio", 0, "estimated target ratio for the PG autoscaler")
}

func newCephPoolDeleteCmd() *cobra.Command {
	var (
		force           bool
		removeEcprofile bool
		removeStorages  bool
		yes             bool
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Destroy a Ceph pool (destructive)",
		Long:  "Destroy the given Ceph pool. All data in the pool is permanently lost.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("destroy Ceph pool %q", args[0])); err != nil {
				return err
			}
			params := &nodes.DeleteCephPoolParams{}
			fl := cmd.Flags()
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("remove-ecprofile") {
				params.RemoveEcprofile = &removeEcprofile
			}
			if fl.Changed("remove-storages") {
				params.RemoveStorages = &removeStorages
			}
			resp, err := deps.API.Nodes.DeleteCephPool(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("destroy Ceph pool %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph pool %q destroyed on node %q.", args[0], deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "destroy the pool even if it is in use")
	f.BoolVar(&removeEcprofile, "remove-ecprofile", false, "remove the erasure-code profile, if applicable")
	f.BoolVar(&removeStorages, "remove-storages", false, "remove pveceph-managed storages configured for this pool")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
