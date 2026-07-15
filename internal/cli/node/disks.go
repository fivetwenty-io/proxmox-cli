package node

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newDisksCmd builds the `pmx node disks` sub-tree: physical-disk inventory,
// SMART health, the read-only storage-type listing/get verbs, the destructive
// delete verbs, and the disk-initialization verbs (create, init-gpt, wipe).
func newDisksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disks",
		Short: "Inspect and initialize physical disks on a node",
		Long: "List the physical disks attached to the resolved node, read their SMART " +
			"health, list existing storage pools by type, and initialize disks into " +
			"storage. The initialization and delete verbs are destructive and require --yes.",
	}
	cmd.AddCommand(
		newDisksListCmd(),
		newDisksSmartCmd(),
		newDisksLsCmd(),
		newDisksGetCmd(),
		newDisksCreateCmd(),
		newDisksDeleteCmd(),
		newDisksInitGptCmd(),
		newDisksWipeCmd(),
	)
	return cmd
}

// diskListEntry is the curated subset of a disks/list element rendered in the
// table. The full element (including wwn, gpt, rpm, vendor, and partitions) is
// preserved in the JSON/Raw output.
type diskListEntry struct {
	Devpath string  `json:"devpath"`
	Type    string  `json:"type"`
	Size    float64 `json:"size"`
	Model   string  `json:"model"`
	Serial  string  `json:"serial"`
	Health  string  `json:"health"`
	Used    string  `json:"used"`
}

func diskSizeCell(size float64) string {
	if size <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", int64(size))
}

// ---- list ------------------------------------------------------------------

func newDisksListCmd() *cobra.Command {
	var (
		includePartitions bool
		skipSmart         bool
		diskType          string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the physical disks attached to the node",
		Long: "List the block devices on the resolved node. SIZE is reported in bytes; " +
			"USED indicates how the disk is currently consumed (for example LVM, ZFS, " +
			"or a partition table).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListDisksListParams{}
			fl := cmd.Flags()
			if fl.Changed("include-partitions") {
				params.IncludePartitions = &includePartitions
			}
			if fl.Changed("skip-smart") {
				params.Skipsmart = &skipSmart
			}
			if fl.Changed("type") {
				params.Type = &diskType
			}
			resp, err := deps.API.Nodes.ListDisksList(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list disks on node %q: %w", deps.Node, err)
			}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e diskListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode disk entry: %w", err)
					}
					rows = append(rows, []string{
						e.Devpath, e.Type, diskSizeCell(e.Size), e.Model, e.Serial, e.Health, e.Used,
					})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Headers: []string{"DEVPATH", "TYPE", "SIZE", "MODEL", "SERIAL", "HEALTH", "USED"},
					Rows:    rows,
					Raw:     resp,
				}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&includePartitions, "include-partitions", false, "also list partitions")
	f.BoolVar(&skipSmart, "skip-smart", false, "skip the SMART health probe (faster)")
	f.StringVar(&diskType, "type", "", "only list disks of this type (unused, journal, ceph, lvm, ...)")
	return cmd
}

// ---- smart -----------------------------------------------------------------

func newDisksSmartCmd() *cobra.Command {
	var (
		disk       string
		healthOnly bool
	)
	cmd := &cobra.Command{
		Use:   "smart",
		Short: "Show the SMART health of a disk",
		Long: "Read the SMART health data of a single block device on the resolved node. " +
			"--disk is required; pass --health-only to return just the overall health status.",
		Example: `  pmx pve node disks smart --disk /dev/sda
  pmx pve node disks smart --disk /dev/sda --health-only`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListDisksSmartParams{Disk: disk}
			if cmd.Flags().Changed("health-only") {
				params.Healthonly = &healthOnly
			}
			resp, err := deps.API.Nodes.ListDisksSmart(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("read SMART data for disk %q on node %q: %w", disk, deps.Node, err)
			}
			single, _, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("render SMART data: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&disk, "disk", "", "block device name, for example /dev/sda (required)")
	f.BoolVar(&healthOnly, "health-only", false, "return only the overall health status")
	cli.MustMarkRequired(cmd, "disk")
	return cmd
}

// ---- create (initialize a disk into storage) -------------------------------

func newDisksCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Initialize a disk into storage (destructive)",
		Long: "Create an LVM volume group, LVM-thin pool, ZFS pool, or directory storage " +
			"from one or more disks. These operations format the target device(s) and " +
			"are irreversible, so they require --yes.",
	}
	cmd.AddCommand(
		newDisksCreateLvmCmd(),
		newDisksCreateLvmthinCmd(),
		newDisksCreateZfsCmd(),
		newDisksCreateDirectoryCmd(),
	)
	return cmd
}

// requireDiskYes enforces the confirmation gate shared by every destructive
// disk verb.
func requireDiskYes(deps *cli.Deps, yes bool) error {
	if !yes {
		return fmt.Errorf("refusing to initialize disks on node %q without confirmation: pass --yes/-y", deps.Node)
	}
	return nil
}

func newDisksCreateLvmCmd() *cobra.Command {
	var (
		device     string
		name       string
		addStorage bool
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "lvm",
		Short: "Create an LVM volume group on a disk (destructive)",
		Long: "Format --device and create an LVM volume group named --name on the resolved " +
			"node. This is irreversible, so it requires --yes.",
		Example: `  pmx pve node disks create lvm --device /dev/sdb --name data --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireDiskYes(deps, yes); err != nil {
				return err
			}
			params := &nodes.CreateDisksLvmParams{Device: device, Name: name}
			if cmd.Flags().Changed("add-storage") {
				params.AddStorage = &addStorage
			}
			resp, err := deps.API.Nodes.CreateDisksLvm(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create LVM volume group %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("LVM volume group %q created on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&device, "device", "", "block device to use, for example /dev/sdb (required)")
	f.StringVar(&name, "name", "", "storage identifier for the new volume group (required)")
	f.BoolVar(&addStorage, "add-storage", false, "configure storage using the new volume group")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "device")
	cli.MustMarkRequired(cmd, "name")
	return cmd
}

func newDisksCreateLvmthinCmd() *cobra.Command {
	var (
		device     string
		name       string
		addStorage bool
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "lvmthin",
		Short: "Create an LVM-thin pool on a disk (destructive)",
		Long: "Format --device and create an LVM-thin pool named --name on the resolved " +
			"node. This is irreversible, so it requires --yes.",
		Example: `  pmx pve node disks create lvmthin --device /dev/sdb --name data --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireDiskYes(deps, yes); err != nil {
				return err
			}
			params := &nodes.CreateDisksLvmthinParams{Device: device, Name: name}
			if cmd.Flags().Changed("add-storage") {
				params.AddStorage = &addStorage
			}
			resp, err := deps.API.Nodes.CreateDisksLvmthin(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create LVM-thin pool %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("LVM-thin pool %q created on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&device, "device", "", "block device to use, for example /dev/sdb (required)")
	f.StringVar(&name, "name", "", "storage identifier for the new thin pool (required)")
	f.BoolVar(&addStorage, "add-storage", false, "configure storage using the new thin pool")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "device")
	cli.MustMarkRequired(cmd, "name")
	return cmd
}

func newDisksCreateZfsCmd() *cobra.Command {
	var (
		devices     string
		name        string
		raidlevel   string
		ashift      int64
		compression string
		draidConfig string
		addStorage  bool
		yes         bool
	)
	cmd := &cobra.Command{
		Use:   "zfs",
		Short: "Create a ZFS pool on one or more disks (destructive)",
		Long: "Format --devices and create a ZFS pool named --name at the given " +
			"--raidlevel on the resolved node. This is irreversible, so it requires --yes.",
		Example: `  pmx pve node disks create zfs --devices /dev/sdb,/dev/sdc --name tank --raidlevel mirror --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireDiskYes(deps, yes); err != nil {
				return err
			}
			params := &nodes.CreateDisksZfsParams{Devices: devices, Name: name, Raidlevel: raidlevel}
			fl := cmd.Flags()
			if fl.Changed("ashift") {
				params.Ashift = &ashift
			}
			if fl.Changed("compression") {
				params.Compression = &compression
			}
			if fl.Changed("draid-config") {
				params.DraidConfig = &draidConfig
			}
			if fl.Changed("add-storage") {
				params.AddStorage = &addStorage
			}
			resp, err := deps.API.Nodes.CreateDisksZfs(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create ZFS pool %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("ZFS pool %q created on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&devices, "devices", "", "comma-separated block devices, for example /dev/sdb,/dev/sdc (required)")
	f.StringVar(&name, "name", "", "storage identifier for the new pool (required)")
	f.StringVar(&raidlevel, "raidlevel", "", "RAID level: single, mirror, raid10, raidz, raidz2, raidz3 (required)")
	f.Int64Var(&ashift, "ashift", 12, "pool sector size exponent")
	f.StringVar(&compression, "compression", "", "compression algorithm: on, off, lz4, lzjb, zle, gzip, zstd")
	f.StringVar(&draidConfig, "draid-config", "",
		"dRAID configuration, for example data=4,spares=1 (only with a draid raidlevel)")
	f.BoolVar(&addStorage, "add-storage", false, "configure storage using the new pool")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "devices")
	cli.MustMarkRequired(cmd, "name")
	cli.MustMarkRequired(cmd, "raidlevel")
	return cmd
}

func newDisksCreateDirectoryCmd() *cobra.Command {
	var (
		device     string
		name       string
		filesystem string
		addStorage bool
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "directory",
		Short: "Create a filesystem on a disk and mount it as directory storage (destructive)",
		Long: "Format --device with a filesystem and mount it as directory storage named " +
			"--name on the resolved node. This is irreversible, so it requires --yes.",
		Example: `  pmx pve node disks create directory --device /dev/sdb --name backups --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireDiskYes(deps, yes); err != nil {
				return err
			}
			params := &nodes.CreateDisksDirectoryParams{Device: device, Name: name}
			fl := cmd.Flags()
			if fl.Changed("filesystem") {
				params.Filesystem = &filesystem
			}
			if fl.Changed("add-storage") {
				params.AddStorage = &addStorage
			}
			resp, err := deps.API.Nodes.CreateDisksDirectory(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create directory storage %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Directory storage %q created on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&device, "device", "", "block device to use, for example /dev/sdb (required)")
	f.StringVar(&name, "name", "", "storage identifier for the new directory storage (required)")
	f.StringVar(&filesystem, "filesystem", "", "filesystem to create: ext4 or xfs")
	f.BoolVar(&addStorage, "add-storage", false, "configure storage using the new directory")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "device")
	cli.MustMarkRequired(cmd, "name")
	return cmd
}

// ---- pools (list storage-type pools) ---------------------------------------

// newDisksLsCmd builds the `pmx node disks pools <type>` sub-group that lists
// existing storage pools by type: directory, lvm, lvmthin, or zfs.
func newDisksLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pools",
		Short: "List existing disk-backed storage pools by type",
		Long: "List the disk-backed storage pools of a given type on the resolved node. " +
			"Choose one of: directory, lvm, lvmthin, or zfs.",
	}
	cmd.AddCommand(
		newDisksLsDirectoryCmd(),
		newDisksLsLvmCmd(),
		newDisksLsLvmthinCmd(),
		newDisksLsZfsCmd(),
	)
	return cmd
}

func newDisksLsDirectoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "directory",
		Short: "List directory storage mounts on the node",
		Long:  "List the directory-backed storage mounts already configured on the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListDisksDirectory(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list directory disks on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newDisksLsLvmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lvm",
		Short: "List LVM volume groups on the node",
		Long:  "List the LVM volume groups already configured on the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListDisksLvm(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list LVM disks on node %q: %w", deps.Node, err)
			}
			if resp == nil {
				return renderScan(cmd, deps, nil, resp)
			}
			return renderScan(cmd, deps, resp.Children, resp)
		},
	}
}

func newDisksLsLvmthinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lvmthin",
		Short: "List LVM-thin pools on the node",
		Long:  "List the LVM-thin pools already configured on the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListDisksLvmthin(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list LVM-thin disks on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newDisksLsZfsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "zfs",
		Short: "List ZFS pools on the node",
		Long:  "List the ZFS pools already configured on the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListDisksZfs(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list ZFS disks on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

// ---- get (single ZFS pool detail) ------------------------------------------

// newDisksGetCmd builds `pmx node disks get zfs <name>`.
func newDisksGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get detailed information about a specific disk pool",
		Long:  "Get detailed information about a specific disk pool. Currently supports zfs.",
	}
	cmd.AddCommand(newDisksGetZfsCmd())
	return cmd
}

func newDisksGetZfsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "zfs <name>",
		Short:   "Show detailed information about a ZFS pool",
		Long:    "Show detailed information about a single ZFS pool by name on the resolved node.",
		Example: `  pmx pve node disks get zfs tank`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			name := args[0]
			resp, err := deps.API.Nodes.GetDisksZfs(cmd.Context(), deps.Node, name)
			if err != nil {
				return fmt.Errorf("get ZFS pool %q on node %q: %w", name, deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

// ---- delete (destroy storage pools) ----------------------------------------

// newDisksDeleteCmd builds the `pmx node disks delete <type> <name>` sub-group.
// All delete verbs are destructive and require --yes.
func newDisksDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a disk-backed storage pool (destructive)",
		Long: "Remove a disk-backed storage pool from the node. These operations destroy " +
			"the pool and its data and are irreversible, so they require --yes.",
	}
	cmd.AddCommand(
		newDisksDeleteDirectoryCmd(),
		newDisksDeleteLvmCmd(),
		newDisksDeleteLvmthinCmd(),
		newDisksDeleteZfsCmd(),
	)
	return cmd
}

func newDisksDeleteDirectoryCmd() *cobra.Command {
	var (
		cleanupConfig bool
		cleanupDisks  bool
		yes           bool
	)
	cmd := &cobra.Command{
		Use:   "directory <name>",
		Short: "Delete a directory storage mount (destructive)",
		Long: "Remove the named directory storage mount from the resolved node. Pass " +
			"--cleanup-config to also remove its storage configuration, and " +
			"--cleanup-disks to wipe the underlying disk. Refuses to run without --yes/-y.",
		Example: `  pmx pve node disks delete directory backups --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			name := args[0]
			if err := requireSystemYes(deps.Node, yes,
				fmt.Sprintf("delete directory storage %q", name)); err != nil {
				return err
			}
			params := &nodes.DeleteDisksDirectoryParams{}
			fl := cmd.Flags()
			if fl.Changed("cleanup-config") {
				params.CleanupConfig = &cleanupConfig
			}
			if fl.Changed("cleanup-disks") {
				params.CleanupDisks = &cleanupDisks
			}
			resp, err := deps.API.Nodes.DeleteDisksDirectory(cmd.Context(), deps.Node, name, params)
			if err != nil {
				return fmt.Errorf("delete directory storage %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Directory storage %q deleted on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&cleanupConfig, "cleanup-config", false, "remove associated storage configuration for this node")
	f.BoolVar(&cleanupDisks, "cleanup-disks", false, "wipe the underlying disk so it can be reused")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newDisksDeleteLvmCmd() *cobra.Command {
	var (
		cleanupConfig bool
		cleanupDisks  bool
		yes           bool
	)
	cmd := &cobra.Command{
		Use:   "lvm <name>",
		Short: "Delete an LVM volume group (destructive)",
		Long: "Remove the named LVM volume group from the resolved node. Pass " +
			"--cleanup-config to also remove its storage configuration, and " +
			"--cleanup-disks to wipe the underlying disks. Refuses to run without --yes/-y.",
		Example: `  pmx pve node disks delete lvm data --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			name := args[0]
			if err := requireSystemYes(deps.Node, yes,
				fmt.Sprintf("delete LVM volume group %q", name)); err != nil {
				return err
			}
			params := &nodes.DeleteDisksLvmParams{}
			fl := cmd.Flags()
			if fl.Changed("cleanup-config") {
				params.CleanupConfig = &cleanupConfig
			}
			if fl.Changed("cleanup-disks") {
				params.CleanupDisks = &cleanupDisks
			}
			resp, err := deps.API.Nodes.DeleteDisksLvm(cmd.Context(), deps.Node, name, params)
			if err != nil {
				return fmt.Errorf("delete LVM volume group %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("LVM volume group %q deleted on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&cleanupConfig, "cleanup-config", false, "remove associated storage configuration for this node")
	f.BoolVar(&cleanupDisks, "cleanup-disks", false, "wipe the underlying disks so they can be reused")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newDisksDeleteLvmthinCmd() *cobra.Command {
	var (
		volumeGroup   string
		cleanupConfig bool
		cleanupDisks  bool
		yes           bool
	)
	cmd := &cobra.Command{
		Use:   "lvmthin <name>",
		Short: "Delete an LVM-thin pool (destructive)",
		Long: "Remove the named LVM-thin pool from --volume-group on the resolved node. " +
			"Pass --cleanup-config to also remove its storage configuration, and " +
			"--cleanup-disks to wipe the underlying disks. Refuses to run without --yes/-y.",
		Example: `  pmx pve node disks delete lvmthin data --volume-group pve --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			name := args[0]
			if err := requireSystemYes(deps.Node, yes,
				fmt.Sprintf("delete LVM-thin pool %q", name)); err != nil {
				return err
			}
			params := &nodes.DeleteDisksLvmthinParams{VolumeGroup: volumeGroup}
			fl := cmd.Flags()
			if fl.Changed("cleanup-config") {
				params.CleanupConfig = &cleanupConfig
			}
			if fl.Changed("cleanup-disks") {
				params.CleanupDisks = &cleanupDisks
			}
			resp, err := deps.API.Nodes.DeleteDisksLvmthin(cmd.Context(), deps.Node, name, params)
			if err != nil {
				return fmt.Errorf("delete LVM-thin pool %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("LVM-thin pool %q deleted on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&volumeGroup, "volume-group", "", "LVM volume group containing the thin pool (required)")
	f.BoolVar(&cleanupConfig, "cleanup-config", false, "remove associated storage configuration for this node")
	f.BoolVar(&cleanupDisks, "cleanup-disks", false, "wipe the underlying disks so they can be reused")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "volume-group")
	return cmd
}

func newDisksDeleteZfsCmd() *cobra.Command {
	var (
		cleanupConfig bool
		cleanupDisks  bool
		yes           bool
	)
	cmd := &cobra.Command{
		Use:   "zfs <name>",
		Short: "Delete a ZFS pool (destructive)",
		Long: "Remove the named ZFS pool from the resolved node. Pass --cleanup-config to " +
			"also remove its storage configuration, and --cleanup-disks to wipe the " +
			"underlying disks. Refuses to run without --yes/-y.",
		Example: `  pmx pve node disks delete zfs tank --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			name := args[0]
			if err := requireSystemYes(deps.Node, yes,
				fmt.Sprintf("delete ZFS pool %q", name)); err != nil {
				return err
			}
			params := &nodes.DeleteDisksZfsParams{}
			fl := cmd.Flags()
			if fl.Changed("cleanup-config") {
				params.CleanupConfig = &cleanupConfig
			}
			if fl.Changed("cleanup-disks") {
				params.CleanupDisks = &cleanupDisks
			}
			resp, err := deps.API.Nodes.DeleteDisksZfs(cmd.Context(), deps.Node, name, params)
			if err != nil {
				return fmt.Errorf("delete ZFS pool %q on node %q: %w", name, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("ZFS pool %q deleted on node %q.", name, deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&cleanupConfig, "cleanup-config", false, "remove associated storage configuration for this node")
	f.BoolVar(&cleanupDisks, "cleanup-disks", false, "wipe the underlying disks so they can be reused")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ---- init-gpt --------------------------------------------------------------

func newDisksInitGptCmd() *cobra.Command {
	var (
		disk string
		uuid string
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "init-gpt",
		Short: "Write a fresh GPT partition table to a disk (destructive)",
		Long: "Write a new, empty GPT partition table to --disk on the resolved node, " +
			"destroying its existing partition table. Refuses to run without --yes/-y.",
		Example: `  pmx pve node disks init-gpt --disk /dev/sdb --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireDiskYes(deps, yes); err != nil {
				return err
			}
			params := &nodes.CreateDisksInitgptParams{Disk: disk}
			if cmd.Flags().Changed("uuid") {
				params.Uuid = &uuid
			}
			resp, err := deps.API.Nodes.CreateDisksInitgpt(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("initialize GPT on disk %q on node %q: %w", disk, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("GPT label written to disk %q on node %q.", disk, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&disk, "disk", "", "block device name, for example /dev/sdb (required)")
	f.StringVar(&uuid, "uuid", "", "UUID for the new GPT table")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "disk")
	return cmd
}

// ---- wipe ------------------------------------------------------------------

func newDisksWipeCmd() *cobra.Command {
	var (
		disk string
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "wipe",
		Short: "Wipe all data and partition tables from a disk (destructive)",
		Long: "Erase all data and partition tables from --disk on the resolved node. " +
			"Refuses to run without --yes/-y.",
		Example: `  pmx pve node disks wipe --disk /dev/sdb --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireDiskYes(deps, yes); err != nil {
				return err
			}
			params := &nodes.UpdateDisksWipediskParams{Disk: disk}
			resp, err := deps.API.Nodes.UpdateDisksWipedisk(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("wipe disk %q on node %q: %w", disk, deps.Node, err)
			}
			return renderDiskTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Disk %q wiped on node %q.", disk, deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&disk, "disk", "", "block device name, for example /dev/sdb (required)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "disk")
	return cmd
}

// ---- task rendering --------------------------------------------------------

// renderDiskTask renders the result of an asynchronous disk operation. The
// endpoints return a task UPID; with --async the UPID is printed immediately,
// otherwise the command blocks on the task. A non-UPID payload (for example an
// empty body) falls back to the plain success message.
func renderDiskTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
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
		return fmt.Errorf("disk operation on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}
