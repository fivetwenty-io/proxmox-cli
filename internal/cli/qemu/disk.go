package qemu

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newDiskCmd builds the `pmx pve qemu disk` sub-tree (attach, resize, move, unlink).
func newDiskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disk",
		Short: "Manage QEMU virtual machine disks",
		Long:  "Attach, grow, relocate, and detach the disks attached to a QEMU virtual machine.",
	}
	cmd.AddCommand(
		newDiskAddCmd(),
		newDiskResizeCmd(),
		newDiskMoveCmd(),
		newDiskUnlinkCmd(),
	)
	return cmd
}

var diskAddSlotRE = regexp.MustCompile(`^(ide|sata|scsi|virtio)(\d+)$`)

// Per-bus maximum slot index, from the PVE qemu config schema
// (scsi 0-30, ide 0-3, sata 0-5, virtio 0-15).
var diskAddSlotMax = map[string]int{"ide": 3, "sata": 5, "scsi": 30, "virtio": 15}

// newDiskAddCmd attaches a newly allocated volume to a free disk slot via
// PUT /nodes/{node}/qemu/{vmid}/config using the STORAGE_ID:SIZE_IN_GiB
// allocation syntax. The PUT is synchronous (no task UPID). On a running VM
// without hotplug the disk lands as a pending change until the next restart.
func newDiskAddCmd() *cobra.Command {
	var (
		disk     string
		storage  string
		sizeGB   int
		options  string
		digest   string
		skiplock bool
	)
	cmd := &cobra.Command{
		Use:   "add <vmid|name>",
		Short: "Attach a new disk to a QEMU virtual machine",
		Long: "Allocate a new volume on a storage and attach it to a free disk slot. " +
			"Extra PVE disk properties (discard=on, ssd=1, serial=..., backup=0, iothread=1, " +
			"import-from=...) ride along verbatim via --options.",
		Example: `  pmx pve qemu disk add 100 --disk scsi2 --storage local-lvm --size-gb 100
  pmx pve qemu disk add 100 --disk scsi2 --storage tank-lab-ceph --size-gb 100 --options discard=on,ssd=1,backup=0,serial=osd0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			if !fl.Changed("disk") {
				return fmt.Errorf("--disk is required: provide the slot to attach (for example, scsi2)")
			}
			if !fl.Changed("storage") {
				return fmt.Errorf("--storage is required: provide the storage to allocate the volume on")
			}
			if !fl.Changed("size-gb") {
				return fmt.Errorf("--size-gb is required: provide the disk size as a whole number of GiB")
			}
			if sizeGB <= 0 {
				return fmt.Errorf("--size-gb must be a positive whole number of GiB")
			}
			m := diskAddSlotRE.FindStringSubmatch(disk)
			if m == nil {
				return fmt.Errorf("--disk %q is not a valid slot: use <bus><index>, for example scsi2, virtio1, sata0, or ide0", disk)
			}
			idx, _ := strconv.Atoi(m[2])
			if idx > diskAddSlotMax[m[1]] {
				return fmt.Errorf("--disk %s: index %d is out of range for %s (0 through %d)", disk, idx, m[1], diskAddSlotMax[m[1]])
			}

			cfg, cfgDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			if cur, ok := rawStr(cfg, disk); ok {
				return fmt.Errorf("disk %s already exists on VM %s (%s): use disk resize or disk move, or pick a free slot", disk, vmid, cur)
			}

			value := fmt.Sprintf("%s:%d", storage, sizeGB)
			if options != "" {
				value += "," + options
			}
			params := &nodes.UpdateQemuConfigParams{}
			slot := map[int]string{idx: value}
			switch m[1] {
			case "ide":
				params.Ide = slot
			case "sata":
				params.Sata = slot
			case "scsi":
				params.Scsi = slot
			case "virtio":
				params.Virtio = slot
			}
			applyDigest(params, fl, digest, cfgDigest)
			if fl.Changed("skiplock") {
				params.Skiplock = new(skiplock)
			}
			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("add disk %s on VM %s (node %q): %w", disk, vmid, node, err)
			}
			msg := fmt.Sprintf("VM %s disk %s added (%s).", vmid, disk, value)

			suffix, err := mutationSuffix(cmd, deps, vmid, node, false)
			if err != nil {
				return err
			}
			// discard=on and ssd=1 take effect only after a full power-off and
			// power-on, not a reboot (ceph-lab-plan §5): a reboot preserves the
			// guest's already-loaded view of hotplugged/reconfigured devices,
			// so already-booted VMs need the cold cycle to see the change.
			if strings.Contains(options, "discard") || strings.Contains(options, "ssd") {
				suffix += " Note: discard/ssd flags need a full power-off and power-on, not a reboot."
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Message: msg + suffix,
					Raw:     map[string]any{"vmid": vmid, "node": node, "disk": disk, "value": value},
				}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&disk, "disk", "", "target slot, for example scsi2 or virtio1 (required)")
	cmd.Flags().StringVar(&storage, "storage", "", "storage ID to allocate the new volume on (required)")
	cmd.Flags().IntVar(&sizeGB, "size-gb", 0, "disk size as a whole number of GiB (required)")
	cmd.Flags().StringVar(&options, "options", "", "extra PVE disk properties appended verbatim, comma-separated (for example discard=on,ssd=1,serial=osd0)")
	cmd.Flags().StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	cmd.Flags().BoolVar(&skiplock, "skiplock", false, "ignore VM locks (root only)")
	return cmd
}

// newDiskResizeCmd builds `pmx pve qemu disk resize <vmid> --disk scsi0 --size +10G`.
//
// Resize is normally a synchronous operation that returns no task; some storage
// back-ends instead schedule a worker and return a UPID, in which case the
// command waits for that task (unless --async is set). Shrinking is rejected by
// PVE; use a leading `+` to grow relative to the current size.
func newDiskResizeCmd() *cobra.Command {
	var (
		async    bool
		disk     string
		size     string
		skiplock bool
		digest   string
	)
	cmd := &cobra.Command{
		Use:   "resize <vmid|name>",
		Short: "Grow a QEMU virtual machine disk",
		Long: "Increase the size of an attached disk. Use an absolute size such as " +
			"`32G` or a relative increment such as `+10G`. Shrinking is not supported by PVE.",
		Example: `  pmx pve qemu disk resize 100 --disk scsi0 --size +10G`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("disk") {
				return fmt.Errorf("--disk is required: provide the disk to resize (for example, scsi0)")
			}
			if !cmd.Flags().Changed("size") {
				return fmt.Errorf("--size is required: provide an absolute (32G) or relative (+10G) size")
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.UpdateQemuResizeParams{Disk: disk, Size: size}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = new(skiplock)
			}
			if cmd.Flags().Changed("digest") {
				params.Digest = new(digest)
			}

			resp, err := deps.API.Nodes.UpdateQemuResize(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("resize disk %s on VM %s (node %q): %w", disk, vmid, node, err)
			}
			msg := fmt.Sprintf("VM %s disk %s resized to %s.", vmid, disk, size)
			raw := json.RawMessage(*resp)
			if _, uerr := apiclient.UPIDFromRaw(raw); uerr == nil {
				return finishAsync(cmd, deps, raw, msg)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting (worker storages only)")
	cmd.Flags().StringVar(&disk, "disk", "", "disk to resize, for example scsi0 or virtio0 (required)")
	cmd.Flags().StringVar(&size, "size", "", "new size: absolute (32G) or relative increment (+10G) (required)")
	cmd.Flags().BoolVar(&skiplock, "skiplock", false, "ignore VM locks (root only)")
	cmd.Flags().StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	return cmd
}

// newDiskMoveCmd builds `pmx pve qemu disk move <vmid> --disk scsi0 --storage X [--delete]`.
//
// Moving a disk is an asynchronous PVE task (UPID); the command blocks until the
// task completes unless --async is set. By default the source volume is retained
// as an unused disk; pass --delete to remove it after a successful copy.
func newDiskMoveCmd() *cobra.Command {
	var (
		async        bool
		disk         string
		storage      string
		targetDisk   string
		targetVMID   int64
		format       string
		bwlimit      int64
		del          bool
		digest       string
		targetDigest string
	)
	cmd := &cobra.Command{
		Use:   "move <vmid|name>",
		Short: "Relocate a QEMU virtual machine disk",
		Long: "Move an attached disk to a different storage, or reassign it to another " +
			"VM. The command blocks until the move task completes unless --async is set.",
		Example: `  pmx pve qemu disk move 100 --disk scsi0 --storage local-lvm
  pmx pve qemu disk move 100 --disk scsi0 --target-vmid 101`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("disk") {
				return fmt.Errorf("--disk is required: provide the disk to move (for example, scsi0)")
			}
			if !cmd.Flags().Changed("storage") && !cmd.Flags().Changed("target-vmid") {
				return fmt.Errorf("--storage or --target-vmid is required: provide a target storage or target VM")
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateQemuMoveDiskParams{Disk: disk}
			fl := cmd.Flags()
			if fl.Changed("storage") {
				params.Storage = new(storage)
			}
			if fl.Changed("target-disk") {
				params.TargetDisk = new(targetDisk)
			}
			if fl.Changed("target-vmid") {
				params.TargetVmid = new(targetVMID)
			}
			if fl.Changed("format") {
				params.Format = new(format)
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = new(bwlimit)
			}
			if fl.Changed("delete") {
				params.Delete = new(del)
			}
			if fl.Changed("digest") {
				params.Digest = new(digest)
			}
			if fl.Changed("target-digest") {
				params.TargetDigest = new(targetDigest)
			}

			resp, err := deps.API.Nodes.CreateQemuMoveDisk(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("move disk %s on VM %s (node %q): %w", disk, vmid, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("VM %s disk %s moved.", vmid, disk))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&disk, "disk", "", "disk to move, for example scsi0 or virtio0 (required)")
	cmd.Flags().StringVar(&storage, "storage", "", "target storage for the disk")
	cmd.Flags().StringVar(&targetDisk, "target-disk", "", "config key the disk will take on the target VM, for example scsi1")
	cmd.Flags().Int64Var(&targetVMID, "target-vmid", 0, "move the disk to another VM with this VMID")
	cmd.Flags().StringVar(&format, "format", "", "target disk format, for example raw or qcow2")
	cmd.Flags().Int64Var(&bwlimit, "bwlimit", 0, "I/O bandwidth limit in KiB/s")
	cmd.Flags().BoolVar(&del, "delete", false, "remove the source disk after a successful copy")
	cmd.Flags().StringVar(&digest, "digest", "", "only apply if the source config matches this SHA1 digest")
	cmd.Flags().StringVar(&targetDigest, "target-digest", "", "only apply if the target config matches this SHA1 digest")
	return cmd
}

// newDiskUnlinkCmd builds `pmx pve qemu disk unlink <vmid> --disk scsi0 [--force]`.
//
// Without --force the disk is detached from the VM configuration and retained as
// an `unused[n]` entry; with --force the underlying volume is physically removed.
func newDiskUnlinkCmd() *cobra.Command {
	var (
		disk  string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "unlink <vmid|name>",
		Short: "Detach a QEMU virtual machine disk",
		Long: "Detach one or more disks from a VM. By default each disk is kept as an " +
			"`unused[n]` config entry; pass --force to physically remove the underlying volume. " +
			"Multiple disks may be given as a comma-separated list.",
		Example: `  pmx pve qemu disk unlink 100 --disk scsi1
  pmx pve qemu disk unlink 100 --disk scsi1 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("disk") {
				return fmt.Errorf("--disk is required: provide the disk(s) to unlink, for example scsi0 or scsi0,scsi1")
			}

			params := &nodes.UpdateQemuUnlinkParams{Idlist: disk}
			if cmd.Flags().Changed("force") {
				params.Force = new(force)
			}

			if err := deps.API.Nodes.UpdateQemuUnlink(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("unlink disk %s on VM %s (node %q): %w", disk, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("VM %s disk %s unlinked.", vmid, disk)}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&disk, "disk", "", "disk(s) to unlink, for example scsi0 or scsi0,scsi1 (required)")
	cmd.Flags().BoolVar(&force, "force", false, "physically remove the underlying volume instead of keeping it as unused")
	return cmd
}
