package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

func newCephOsdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "osd",
		Short: "Inspect and manage Ceph OSDs",
		Long:  "Show the OSD tree, inspect a single OSD, create and destroy OSDs, toggle them in or out of the cluster, and trigger scrubs.",
	}
	cmd.AddCommand(
		newCephOsdListCmd(),
		newCephOsdGetCmd(),
		newCephOsdCreateCmd(),
		newCephOsdDeleteCmd(),
		newCephOsdInCmd(),
		newCephOsdOutCmd(),
		newCephOsdScrubCmd(),
	)
	return cmd
}

func newCephOsdListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the Ceph OSD tree",
		Long:  "Show the CRUSH/OSD tree and any cluster-wide OSD flags as seen from the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephOsd(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list Ceph OSDs on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newCephOsdGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <osdid>",
		Short: "Show details for a single OSD",
		Long:  "Show the metadata and runtime detail rows for the given OSD id on the resolved node.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.GetCephOsd(cmd.Context(), deps.Node, args[0])
			if err != nil {
				return fmt.Errorf("get Ceph OSD %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newCephOsdCreateCmd() *cobra.Command {
	var (
		dev              string
		crushDeviceClass string
		dbDev            string
		dbDevSize        float64
		walDev           string
		walDevSize       float64
		osdsPerDevice    int64
		encrypted        bool
		yes              bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Ceph OSD on a block device (destructive)",
		Long:  "Create a Ceph OSD backed by the given block device. The device is wiped and consumed by Ceph.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "create a Ceph OSD"); err != nil {
				return err
			}
			params := &nodes.CreateCephOsdParams{Dev: dev}
			fl := cmd.Flags()
			if fl.Changed("crush-device-class") {
				params.CrushDeviceClass = &crushDeviceClass
			}
			if fl.Changed("db-dev") {
				params.DbDev = &dbDev
			}
			if fl.Changed("db-dev-size") {
				params.DbDevSize = &dbDevSize
			}
			if fl.Changed("wal-dev") {
				params.WalDev = &walDev
			}
			if fl.Changed("wal-dev-size") {
				params.WalDevSize = &walDevSize
			}
			if fl.Changed("osds-per-device") {
				params.OsdsPerDevice = &osdsPerDevice
			}
			if fl.Changed("encrypted") {
				params.Encrypted = &encrypted
			}
			resp, err := deps.API.Nodes.CreateCephOsd(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create Ceph OSD on device %q on node %q: %w", dev, deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph OSD created on device %q on node %q.", dev, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&dev, "dev", "", "block device to consume, for example /dev/sdb (required)")
	f.StringVar(&crushDeviceClass, "crush-device-class", "", "set the OSD's device class in CRUSH")
	f.StringVar(&dbDev, "db-dev", "", "block device for block.db")
	f.Float64Var(&dbDevSize, "db-dev-size", 0, "size in GiB for block.db")
	f.StringVar(&walDev, "wal-dev", "", "block device for block.wal")
	f.Float64Var(&walDevSize, "wal-dev-size", 0, "size in GiB for block.wal")
	f.Int64Var(&osdsPerDevice, "osds-per-device", 0, "OSD services per physical device (fast NVMe only)")
	f.BoolVar(&encrypted, "encrypted", false, "enable encryption of the OSD")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	_ = cmd.MarkFlagRequired("dev")
	return cmd
}

func newCephOsdDeleteCmd() *cobra.Command {
	var (
		cleanup bool
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "delete <osdid>",
		Short: "Destroy a Ceph OSD (destructive)",
		Long:  "Remove the given OSD from the cluster. With --cleanup, also zap the underlying logical volumes and partitions.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("destroy Ceph OSD %q", args[0])); err != nil {
				return err
			}
			params := &nodes.DeleteCephOsdParams{}
			if cmd.Flags().Changed("cleanup") {
				params.Cleanup = &cleanup
			}
			resp, err := deps.API.Nodes.DeleteCephOsd(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("destroy Ceph OSD %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph OSD %q destroyed on node %q.", args[0], deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&cleanup, "cleanup", false, "also zap the underlying logical volumes and partitions")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newCephOsdInCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "in <osdid>",
		Short: "Mark an OSD as in (destructive)",
		Long:  "Mark the given OSD as 'in', allowing the cluster to place data on it. Triggers data movement.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("mark Ceph OSD %q in", args[0])); err != nil {
				return err
			}
			if err := deps.API.Nodes.CreateCephOsdIn(cmd.Context(), deps.Node, args[0]); err != nil {
				return fmt.Errorf("mark Ceph OSD %q in on node %q: %w", args[0], deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Ceph OSD %q marked in on node %q.", args[0], deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the operation without prompting")
	return cmd
}

func newCephOsdOutCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "out <osdid>",
		Short: "Mark an OSD as out (destructive)",
		Long:  "Mark the given OSD as 'out', draining data off it. Triggers data movement.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("mark Ceph OSD %q out", args[0])); err != nil {
				return err
			}
			if err := deps.API.Nodes.CreateCephOsdOut(cmd.Context(), deps.Node, args[0]); err != nil {
				return fmt.Errorf("mark Ceph OSD %q out on node %q: %w", args[0], deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Ceph OSD %q marked out on node %q.", args[0], deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the operation without prompting")
	return cmd
}

func newCephOsdScrubCmd() *cobra.Command {
	var (
		deep bool
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "scrub <osdid>",
		Short: "Trigger a scrub on an OSD (destructive)",
		Long:  "Instruct the given OSD to scrub. With --deep, perform a deep scrub. Scrubbing adds I/O load.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("scrub Ceph OSD %q", args[0])); err != nil {
				return err
			}
			params := &nodes.CreateCephOsdScrubParams{}
			if cmd.Flags().Changed("deep") {
				params.Deep = &deep
			}
			if err := deps.API.Nodes.CreateCephOsdScrub(cmd.Context(), deps.Node, args[0], params); err != nil {
				return fmt.Errorf("scrub Ceph OSD %q on node %q: %w", args[0], deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Scrub requested for Ceph OSD %q on node %q.", args[0], deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&deep, "deep", false, "perform a deep scrub instead of a normal one")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the operation without prompting")
	return cmd
}
