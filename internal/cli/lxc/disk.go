package lxc

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newDiskCmd builds the `pmx lxc disk` sub-tree (resize, move).
func newDiskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disk",
		Short: "Manage LXC container volumes",
		Long:  "Grow and relocate the rootfs and mount-point volumes attached to an LXC container.",
	}
	cmd.AddCommand(
		newDiskResizeCmd(),
		newDiskMoveCmd(),
	)
	return cmd
}

// newDiskResizeCmd builds `pmx lxc disk resize <vmid> --disk rootfs --size +5G`.
//
// Resize is normally a synchronous operation that returns no task; some storage
// back-ends instead schedule a worker and return a UPID, in which case the
// command waits for that task (unless --async is set). Shrinking is rejected by
// PVE; use a leading `+` to grow relative to the current size.
func newDiskResizeCmd() *cobra.Command {
	var (
		async bool
		disk  string
		size  string
	)
	cmd := &cobra.Command{
		Use:   "resize <vmid|name>",
		Short: "Grow an LXC container volume",
		Long: "Increase the size of an attached volume such as rootfs or mp0. Use an absolute " +
			"size such as `16G` or a relative increment such as `+5G`. Shrinking is not supported by PVE.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("disk") {
				return fmt.Errorf("--disk is required: provide the volume to resize (for example, rootfs)")
			}
			if !cmd.Flags().Changed("size") {
				return fmt.Errorf("--size is required: provide an absolute (16G) or relative (+5G) size")
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.UpdateLxcResizeParams{Disk: disk, Size: size}

			resp, err := deps.API.Nodes.UpdateLxcResize(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("resize volume %s on container %s (node %q): %w", disk, vmid, node, err)
			}
			msg := fmt.Sprintf("Container %s volume %s resized to %s.", vmid, disk, size)
			raw := json.RawMessage(*resp)
			if _, uerr := apiclient.UPIDFromRaw(raw); uerr == nil {
				return emitTask(cmd, deps, raw, msg)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting (worker storages only)")
	cmd.Flags().StringVar(&disk, "disk", "", "volume to resize, for example rootfs or mp0 (required)")
	cmd.Flags().StringVar(&size, "size", "", "new size: absolute (16G) or relative increment (+5G) (required)")
	return cmd
}

// newDiskMoveCmd builds `pmx lxc disk move <vmid> --volume rootfs --storage X [--delete]`.
//
// Moving a volume is an asynchronous PVE task (UPID); the command blocks until the
// task completes unless --async is set. By default the source volume is retained
// as an unused volume entry; pass --delete to remove it after a successful copy.
func newDiskMoveCmd() *cobra.Command {
	var (
		async        bool
		volume       string
		storage      string
		targetVMID   int64
		targetVolume string
		bwlimit      float64
		del          bool
		digest       string
		targetDigest string
	)
	cmd := &cobra.Command{
		Use:   "move <vmid|name>",
		Short: "Relocate an LXC container volume",
		Long: "Move an attached volume to a different storage, or reassign it to another " +
			"container. The command blocks until the move task completes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("volume") {
				return fmt.Errorf("--volume is required: provide the volume to move (for example, rootfs)")
			}
			if !cmd.Flags().Changed("storage") && !cmd.Flags().Changed("target-vmid") {
				return fmt.Errorf("--storage or --target-vmid is required: provide a target storage or target container")
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateLxcMoveVolumeParams{Volume: volume}
			fl := cmd.Flags()
			if fl.Changed("storage") {
				params.Storage = &storage
			}
			if fl.Changed("target-vmid") {
				params.TargetVmid = &targetVMID
			}
			if fl.Changed("target-volume") {
				params.TargetVolume = &targetVolume
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("target-digest") {
				params.TargetDigest = &targetDigest
			}

			resp, err := deps.API.Nodes.CreateLxcMoveVolume(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("move volume %s on container %s (node %q): %w", volume, vmid, node, err)
			}
			return emitTask(cmd, deps, *resp,
				fmt.Sprintf("Container %s volume %s moved.", vmid, volume))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&volume, "volume", "", "volume to move, for example rootfs or mp0 (required)")
	cmd.Flags().StringVar(&storage, "storage", "", "target storage for the volume")
	cmd.Flags().Int64Var(&targetVMID, "target-vmid", 0, "move the volume to another container with this VMID")
	cmd.Flags().StringVar(&targetVolume, "target-volume", "", "config key the volume will take on the target, for example mp1")
	cmd.Flags().Float64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	cmd.Flags().BoolVar(&del, "delete", false, "remove the source volume after a successful copy")
	cmd.Flags().StringVar(&digest, "digest", "", "only apply if the source config matches this SHA1 digest")
	cmd.Flags().StringVar(&targetDigest, "target-digest", "", "only apply if the target config matches this SHA1 digest")
	return cmd
}
