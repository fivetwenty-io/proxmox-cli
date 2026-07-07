package pbs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeDiskEntry mirrors one element of the JSON array PBS returns from
// GET /nodes/{node}/disks/list, per the PBS API's documented BlockDevice
// schema.
type nodeDiskEntry struct {
	Devpath string  `json:"devpath"`
	Used    *string `json:"used,omitempty"`
	Gpt     *bool   `json:"gpt,omitempty"`
	Size    *int64  `json:"size,omitempty"`
	Vendor  *string `json:"vendor,omitempty"`
	Model   *string `json:"model,omitempty"`
	Serial  *string `json:"serial,omitempty"`
	Rpm     *int64  `json:"rpm,omitempty"`
	Type    *string `json:"type,omitempty"`
	Wearout *int64  `json:"wearout,omitempty"`
	Health  *string `json:"health,omitempty"`
}

// nodeDirEntry mirrors one element of the JSON array PBS returns from
// GET /nodes/{node}/disks/directory, per the PBS API's documented
// StorageStatus schema.
type nodeDirEntry struct {
	Name   string  `json:"name"`
	Path   *string `json:"path,omitempty"`
	Device *string `json:"device,omitempty"`
	Type   *string `json:"type,omitempty"`
	Status *string `json:"status,omitempty"`
	Total  *int64  `json:"total,omitempty"`
	Used   *int64  `json:"used,omitempty"`
	Avail  *int64  `json:"avail,omitempty"`
}

// nodeZfsEntry mirrors one element of the JSON array PBS returns from
// GET /nodes/{node}/disks/zfs, per the PBS API's documented ZFSPoolInfo
// schema.
type nodeZfsEntry struct {
	Name    string  `json:"name"`
	Health  *string `json:"health,omitempty"`
	Size    *int64  `json:"size,omitempty"`
	Alloc   *int64  `json:"alloc,omitempty"`
	Free    *int64  `json:"free,omitempty"`
	Dedup   *string `json:"dedup,omitempty"`
	Fragmen *int64  `json:"frag,omitempty"`
}

// newNodeDisksCmd builds `pmx pbs node disks` and its
// ls/smart/initgpt/wipe/directory/zfs verbs (/nodes/{node}/disks...).
func newNodeDisksCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disks",
		Short: "Inspect and manage local disks on the node",
	}
	cmd.AddCommand(
		newNodeDisksLsCmd(nf),
		newNodeDisksSmartCmd(nf),
		newNodeDisksInitgptCmd(nf),
		newNodeDisksWipeCmd(nf),
		newNodeDisksDirectoryCmd(nf),
		newNodeDisksZfsCmd(nf),
	)
	return cmd
}

// newNodeDisksLsCmd builds `pmx pbs node disks ls` — list local disks
// (GET /nodes/{node}/disks/list).
func newNodeDisksLsCmd(nf *nodeFlags) *cobra.Command {
	var (
		includePartitions bool
		skipsmart         bool
		usageType         string
	)

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List local disks on the node",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pbsnodes.ListDisksListParams{}
			if fl.Changed("include-partitions") {
				params.IncludePartitions = &includePartitions
			}
			if fl.Changed("skip-smart") {
				params.Skipsmart = &skipsmart
			}
			if fl.Changed("usage-type") {
				params.UsageType = &usageType
			}

			resp, err := deps.PBS.Nodes.ListDisksList(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("list disks on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeDiskEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode disks on node %q: %w", nf.node, err)
			}

			headers := []string{"DEVPATH", "TYPE", "SIZE", "USED", "VENDOR", "MODEL", "SERIAL", "HEALTH"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Devpath, pbsFormatOptionalString(e.Type), pbsFormatOptionalInt64(e.Size),
					pbsFormatOptionalString(e.Used), pbsFormatOptionalString(e.Vendor),
					pbsFormatOptionalString(e.Model), pbsFormatOptionalString(e.Serial),
					pbsFormatOptionalString(e.Health),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&includePartitions, "include-partitions", false, "include partitions in the listing")
	f.BoolVar(&skipsmart, "skip-smart", false, "skip SMART health checks")
	f.StringVar(&usageType, "usage-type", "", "filter by what the disk is used for")

	return cmd
}

// newNodeDisksSmartCmd builds `pmx pbs node disks smart --disk X` — get
// SMART attributes and health for a disk (GET /nodes/{node}/disks/smart).
func newNodeDisksSmartCmd(nf *nodeFlags) *cobra.Command {
	var (
		disk       string
		healthonly bool
	)

	cmd := &cobra.Command{
		Use:   "smart",
		Short: "Show SMART attributes and health for a disk",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsnodes.ListDisksSmartParams{Disk: disk}
			if cmd.Flags().Changed("healthonly") {
				params.Healthonly = &healthonly
			}

			resp, err := deps.PBS.Nodes.ListDisksSmart(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("get smart status for disk %q on node %q: %w", disk, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get smart status for disk %q on node %q: empty response from server", disk, nf.node)
			}

			single := map[string]string{"disk": disk, "status": resp.Status}
			if resp.Wearout != nil {
				single["wearout"] = fmt.Sprintf("%g", resp.Wearout.Float())
			}
			single["attributes"] = fmt.Sprintf("%d", len(resp.Attributes))

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&disk, "disk", "", "block device name, e.g. sda (required)")
	f.BoolVar(&healthonly, "healthonly", false, "return only the health status")
	cli.MustMarkRequired(cmd, "disk")

	return cmd
}

// newNodeDisksInitgptCmd builds `pmx pbs node disks initgpt --disk X` —
// initialize an empty disk with a GPT table (POST /nodes/{node}/disks/initgpt).
func newNodeDisksInitgptCmd(nf *nodeFlags) *cobra.Command {
	var disk, uuid string

	cmd := &cobra.Command{
		Use:   "initgpt",
		Short: "Initialize an empty disk with a GPT partition table",
		Long: "Initialize an empty disk with a GPT partition table. Runs as an asynchronous " +
			"task; the command blocks until it finishes unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsnodes.CreateDisksInitgptParams{Disk: disk}
			if cmd.Flags().Changed("uuid") {
				params.Uuid = &uuid
			}

			resp, err := deps.PBS.Nodes.CreateDisksInitgpt(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("initialize gpt on disk %q on node %q: %w", disk, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("initialize gpt on disk %q on node %q: empty response from server", disk, nf.node)
			}

			msg := fmt.Sprintf("GPT partition table initialized on disk %q on node %q.", disk, nf.node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&disk, "disk", "", "block device name, e.g. sda (required)")
	f.StringVar(&uuid, "uuid", "", "UUID for the GPT table")
	cli.MustMarkRequired(cmd, "disk")

	return cmd
}

// newNodeDisksWipeCmd builds `pmx pbs node disks wipe --disk X` — wipe a
// disk (PUT /nodes/{node}/disks/wipedisk).
func newNodeDisksWipeCmd(nf *nodeFlags) *cobra.Command {
	var (
		disk string
		yes  bool
	)

	cmd := &cobra.Command{
		Use:   "wipe",
		Short: "Wipe a disk, destroying its data",
		Long: "Erase the partition table and filesystem signatures on a disk, destroying its " +
			"data. Runs as an asynchronous task; the command blocks until it finishes unless " +
			"--async is set. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to wipe disk %q without confirmation: pass --yes/-y", disk)
			}

			params := &pbsnodes.UpdateDisksWipediskParams{Disk: disk}

			resp, err := deps.PBS.Nodes.UpdateDisksWipedisk(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("wipe disk %q on node %q: %w", disk, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("wipe disk %q on node %q: empty response from server", disk, nf.node)
			}

			msg := fmt.Sprintf("Disk %q on node %q wiped.", disk, nf.node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&disk, "disk", "", "(partition) block device name, e.g. sda1 (required)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "disk")

	return cmd
}

// --- directory --------------------------------------------------------------

// newNodeDisksDirectoryCmd builds `pmx pbs node disks directory` and its
// ls/create/delete verbs (/nodes/{node}/disks/directory...).
func newNodeDisksDirectoryCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "directory",
		Short: "Manage directory-backed datastore mount units",
	}
	cmd.AddCommand(
		newNodeDisksDirectoryLsCmd(nf),
		newNodeDisksDirectoryCreateCmd(nf),
		newNodeDisksDirectoryDeleteCmd(nf),
	)
	return cmd
}

func newNodeDisksDirectoryLsCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List systemd datastore mount units",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListDisksDirectory(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("list directory mounts on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeDirEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode directory mounts on node %q: %w", nf.node, err)
			}

			headers := []string{"NAME", "PATH", "DEVICE", "TYPE", "STATUS", "TOTAL", "USED", "AVAIL"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, pbsFormatOptionalString(e.Path), pbsFormatOptionalString(e.Device),
					pbsFormatOptionalString(e.Type), pbsFormatOptionalString(e.Status),
					pbsFormatOptionalInt64(e.Total), pbsFormatOptionalInt64(e.Used), pbsFormatOptionalInt64(e.Avail),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeDisksDirectoryCreateCmd(nf *nodeFlags) *cobra.Command {
	var (
		disk               string
		filesystem         string
		addDatastore       bool
		removableDatastore bool
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a filesystem on an unused disk and mount it",
		Long: "Create a filesystem on --disk and mount it under /mnt/datastore/<name>. Runs as " +
			"an asynchronous task; the command blocks until it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			fl := cmd.Flags()

			params := &pbsnodes.CreateDisksDirectoryParams{Disk: disk, Name: name}
			if fl.Changed("filesystem") {
				params.Filesystem = &filesystem
			}
			if fl.Changed("add-datastore") {
				params.AddDatastore = &addDatastore
			}
			if fl.Changed("removable-datastore") {
				params.RemovableDatastore = &removableDatastore
			}

			resp, err := deps.PBS.Nodes.CreateDisksDirectory(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("create directory mount %q on node %q: %w", name, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("create directory mount %q on node %q: empty response from server", name, nf.node)
			}

			msg := fmt.Sprintf("Directory mount %q created on node %q.", name, nf.node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&disk, "disk", "", "block device name, e.g. sdb (required)")
	f.StringVar(&filesystem, "filesystem", "", "filesystem type to create")
	f.BoolVar(&addDatastore, "add-datastore", false, "configure a datastore using the new directory")
	f.BoolVar(&removableDatastore, "removable-datastore", false, "mark the added datastore as removable")
	cli.MustMarkRequired(cmd, "disk")

	return cmd
}

// newNodeDisksDirectoryDeleteCmd builds `pmx pbs node disks directory delete
// <name>` — remove a directory mount unit (DELETE
// /nodes/{node}/disks/directory/{name}).
//
// The generated Nodes.DeleteDisksDirectory binding discards its response
// body entirely (no data type at all in its signature), even though
// unmounting and removing a datastore mount unit is a background task on
// the equivalent PVE-side workflow. This bypasses it via the shared raw
// transport to recover the task UPID and support --async.
func newNodeDisksDirectoryDeleteCmd(nf *nodeFlags) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove a directory-backed datastore mount unit",
		Long: "Unmount and remove a filesystem mounted under /mnt/datastore/<name>. Runs as an " +
			"asynchronous task; the command blocks until it finishes unless --async is set. " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if !yes {
				return fmt.Errorf("refusing to remove directory mount %q without confirmation: pass --yes/-y", name)
			}

			path := fmt.Sprintf("/nodes/%s/disks/directory/%s", url.PathEscape(nf.node), url.PathEscape(name))
			msg := fmt.Sprintf("Directory mount %q removed on node %q.", name, nf.node)

			err := nodeFinishAsync(cmd, deps, http.MethodDelete, path, nil, msg)
			if err != nil {
				return fmt.Errorf("remove directory mount %q on node %q: %w", name, nf.node, err)
			}

			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}

// --- zfs ----------------------------------------------------------------

// newNodeDisksZfsCmd builds `pmx pbs node disks zfs` and its
// ls/show/create verbs (/nodes/{node}/disks/zfs...).
func newNodeDisksZfsCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zfs",
		Short: "Manage ZFS pool datastores",
	}
	cmd.AddCommand(newNodeDisksZfsLsCmd(nf), newNodeDisksZfsShowCmd(nf), newNodeDisksZfsCreateCmd(nf))
	return cmd
}

func newNodeDisksZfsLsCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List ZFS pools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListDisksZfs(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("list zfs pools on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeZfsEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode zfs pools on node %q: %w", nf.node, err)
			}

			headers := []string{"NAME", "HEALTH", "SIZE", "ALLOC", "FREE", "DEDUP", "FRAG"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, pbsFormatOptionalString(e.Health), pbsFormatOptionalInt64(e.Size),
					pbsFormatOptionalInt64(e.Alloc), pbsFormatOptionalInt64(e.Free),
					pbsFormatOptionalString(e.Dedup), pbsFormatOptionalInt64(e.Fragmen),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeDisksZfsShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show detailed zpool status for a ZFS pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			resp, err := deps.PBS.Nodes.GetDisksZfs(cmd.Context(), nf.node, name)
			if err != nil {
				return fmt.Errorf("get zpool status %q on node %q: %w", name, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get zpool status %q on node %q: empty response from server", name, nf.node)
			}

			var obj map[string]any

			err = json.Unmarshal(*resp, &obj)
			if err != nil {
				return fmt.Errorf("decode zpool status %q on node %q: %w", name, nf.node, err)
			}

			res := output.Result{Single: stringMap(obj), Raw: obj}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeDisksZfsCreateCmd(nf *nodeFlags) *cobra.Command {
	var (
		devices      string
		raidlevel    string
		ashift       int64
		compression  string
		addDatastore bool
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new ZFS pool and mount it",
		Long: "Create a new ZFS pool from --devices at --raidlevel, mounted under " +
			"/mnt/datastore/<name>. Runs as an asynchronous task; the command blocks until it " +
			"finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			fl := cmd.Flags()

			params := &pbsnodes.CreateDisksZfsParams{Devices: devices, Name: name, Raidlevel: raidlevel}
			if fl.Changed("ashift") {
				params.Ashift = &ashift
			}
			if fl.Changed("compression") {
				params.Compression = &compression
			}
			if fl.Changed("add-datastore") {
				params.AddDatastore = &addDatastore
			}

			resp, err := deps.PBS.Nodes.CreateDisksZfs(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("create zfs pool %q on node %q: %w", name, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("create zfs pool %q on node %q: empty response from server", name, nf.node)
			}

			msg := fmt.Sprintf("ZFS pool %q created on node %q.", name, nf.node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&devices, "devices", "", "comma-separated list of disk names (required)")
	f.StringVar(&raidlevel, "raidlevel", "", "ZFS RAID level, e.g. single|mirror|raidz (required)")
	f.Int64Var(&ashift, "ashift", 0, "pool sector size exponent")
	f.StringVar(&compression, "compression", "", "ZFS compression algorithm")
	f.BoolVar(&addDatastore, "add-datastore", false, "configure a datastore using the new pool")
	cli.MustMarkRequired(cmd, "devices")
	cli.MustMarkRequired(cmd, "raidlevel")

	return cmd
}
