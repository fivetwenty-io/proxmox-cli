package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newDiskCmd builds the `pve qemu disk` sub-tree (resize, move, unlink).
func newDiskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disk",
		Short: "Manage QEMU virtual machine disks",
		Long:  "Grow, relocate, and detach the disks attached to a QEMU virtual machine.",
	}
	cmd.AddCommand(
		newDiskResizeCmd(),
		newDiskMoveCmd(),
		newDiskUnlinkCmd(),
	)
	return cmd
}

// newDiskResizeCmd builds `pve qemu disk resize <vmid> --disk scsi0 --size +10G`.
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
		Args: cobra.ExactArgs(1),
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
				params.Skiplock = boolPtr(skiplock)
			}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
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

// newDiskMoveCmd builds `pve qemu disk move <vmid> --disk scsi0 --storage X [--delete]`.
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
				params.Storage = strPtr(storage)
			}
			if fl.Changed("target-disk") {
				params.TargetDisk = strPtr(targetDisk)
			}
			if fl.Changed("target-vmid") {
				params.TargetVmid = int64Ptr(targetVMID)
			}
			if fl.Changed("format") {
				params.Format = strPtr(format)
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = int64Ptr(bwlimit)
			}
			if fl.Changed("delete") {
				params.Delete = boolPtr(del)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("target-digest") {
				params.TargetDigest = strPtr(targetDigest)
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

// newDiskUnlinkCmd builds `pve qemu disk unlink <vmid> --disk scsi0 [--force]`.
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
				params.Force = boolPtr(force)
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
