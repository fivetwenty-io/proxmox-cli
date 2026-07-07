package storage

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newVolumeCmd builds `pmx storage volume` — inspect and manage individual
// volumes (a backup, disk image, ISO, or template) stored on a storage.
func newVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Inspect and manage individual storage volumes",
		Long: "Show a volume's attributes, update its notes and protection flag, " +
			"or copy it to another volume. Volumes are addressed by their full " +
			"identifier, e.g. local:backup/vzdump-qemu-100-2026_01_01.vma.zst.",
	}
	cmd.AddCommand(
		newVolumeGetCmd(),
		newVolumeSetCmd(),
		newVolumeCopyCmd(),
		newVolumeDeleteCmd(),
		newVolumeAllocCmd(),
	)
	return cmd
}

// storageOfVolume returns the storage identifier prefix of a full volume ID
// (the portion before the first colon), or an error if the volume ID is not in
// the expected "<storage>:<path>" form.
func storageOfVolume(volume string) (string, error) {
	storage, _, ok := strings.Cut(volume, ":")
	if !ok || storage == "" {
		return "", fmt.Errorf("invalid volume ID %q: expected <storage>:<path>", volume)
	}
	return storage, nil
}

// newVolumeGetCmd builds `pmx storage volume get <volume>`.
func newVolumeGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <volume>",
		Short: "Show a single volume's attributes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			volume := args[0]
			storage, err := storageOfVolume(volume)
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.GetStorageContent(cmd.Context(), deps.Node, storage, volume)
			if err != nil {
				return fmt.Errorf("get volume %q on node %q: %w", volume, deps.Node, err)
			}

			fields := map[string]any{}
			raw, mErr := json.Marshal(resp)
			if mErr == nil {
				_ = json.Unmarshal(raw, &fields)
			}
			single := make(map[string]string, len(fields))
			for k, v := range fields {
				single[k] = scalarString(v)
			}
			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newVolumeSetCmd builds `pmx storage volume set <volume>`.
func newVolumeSetCmd() *cobra.Command {
	var (
		notes     string
		protected bool
	)
	cmd := &cobra.Command{
		Use:   "set <volume>",
		Short: "Update a volume's notes or protection flag",
		Long: "Set the notes attached to a volume, or toggle its protection flag. " +
			"Protection currently applies to backups only and prevents them from being pruned. " +
			"Pass --notes \"\" to clear existing notes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			volume := args[0]
			storage, err := storageOfVolume(volume)
			if err != nil {
				return err
			}

			fl := cmd.Flags()
			if !fl.Changed("notes") && !fl.Changed("protected") {
				return fmt.Errorf("nothing to update: pass --notes and/or --protected")
			}
			params := &nodes.UpdateStorageContentParams{}
			if fl.Changed("notes") {
				params.Notes = &notes
			}
			if fl.Changed("protected") {
				params.Protected = &protected
			}

			if err := deps.API.Nodes.UpdateStorageContent(cmd.Context(), deps.Node, storage, volume, params); err != nil {
				return fmt.Errorf("update volume %q on node %q: %w", volume, deps.Node, err)
			}
			res := output.Result{Message: fmt.Sprintf("Volume %q updated.", volume)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&notes, "notes", "", "notes to attach to the volume (use \"\" to clear)")
	fl.BoolVar(&protected, "protected", false, "protect the volume from pruning (backups only)")
	return cmd
}

// newVolumeDeleteCmd builds `pmx storage volume delete <volume>`.
func newVolumeDeleteCmd() *cobra.Command {
	var (
		yes   bool
		delay int64
	)
	cmd := &cobra.Command{
		Use:   "delete <volume>",
		Short: "Delete a volume from a storage",
		Long: "Permanently remove a volume from its storage. " +
			"The volume identifier must be in <storage>:<path> form, " +
			"e.g. local:backup/vzdump-qemu-100-2026_01_01.vma.zst. " +
			"Pass --yes to confirm the deletion.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			volume := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete volume %q without --yes", volume)
			}
			storage, err := storageOfVolume(volume)
			if err != nil {
				return err
			}

			fl := cmd.Flags()
			params := &nodes.DeleteStorageContentParams{}
			if fl.Changed("delay") {
				params.Delay = &delay
			}

			resp, err := deps.API.Nodes.DeleteStorageContent(cmd.Context(), deps.Node, storage, volume, params)
			if err != nil {
				return fmt.Errorf("delete volume %q on node %q: %w", volume, deps.Node, err)
			}

			// The API may return a UPID for asynchronous deletion, or null for
			// a synchronous one. Treat a nil/empty response as immediate success.
			if resp == nil || len(*resp) == 0 || string(*resp) == "null" {
				res := output.Result{Message: fmt.Sprintf("Volume %q deleted.", volume)}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			// Attempt to parse a UPID and wait; if the value is not a UPID
			// (e.g. the plugin returned a bare string), treat as done.
			upid, parseErr := apiclient.UPIDFromRaw(json.RawMessage(*resp))
			if parseErr != nil {
				upid = ""
			}
			return renderStorageTask(cmd, deps, upid,
				fmt.Sprintf("Volume %q deleted.", volume))
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	fl.Int64Var(&delay, "delay", 0, "seconds to wait for task completion before returning null")
	return cmd
}

// newVolumeAllocCmd builds `pmx storage volume alloc`.
func newVolumeAllocCmd() *cobra.Command {
	var (
		vmid     int64
		filename string
		size     string
		format   string
	)
	cmd := &cobra.Command{
		Use:   "alloc",
		Short: "Allocate a new empty volume on a storage",
		Long: "Create a new, empty disk image on a storage. " +
			"Requires --vmid (owner VM/CT id), --filename (volume name), " +
			"and --size (e.g. 4G, 1024M, 2048). " +
			"--format selects the image format (raw, qcow2, vmdk) when the " +
			"storage plugin supports multiple formats.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}

			// Extract <storage> from the filename prefix (e.g. "local-lvm:vm-100-disk-0").
			storage, err := storageOfVolume(filename)
			if err != nil {
				return fmt.Errorf("--filename must be in <storage>:<name> form: %w", err)
			}

			fl := cmd.Flags()
			params := &nodes.CreateStorageContentParams{
				Filename: filename,
				Size:     size,
				Vmid:     vmid,
			}
			if fl.Changed("format") {
				params.Format = &format
			}

			resp, err := deps.API.Nodes.CreateStorageContent(cmd.Context(), deps.Node, storage, params)
			if err != nil {
				return fmt.Errorf("alloc volume %q on node %q: %w", filename, deps.Node, err)
			}

			// The API returns the new volume ID as a raw JSON string.
			single := map[string]string{}
			if resp != nil && len(*resp) > 0 {
				var volid string
				if jsonErr := json.Unmarshal(*resp, &volid); jsonErr == nil && volid != "" {
					single["volid"] = volid
				}
			}
			if single["volid"] == "" {
				single["volid"] = filename
			}
			res := output.Result{
				Single:  single,
				Raw:     resp,
				Message: fmt.Sprintf("Volume %q allocated on storage %q.", single["volid"], storage),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.Int64Var(&vmid, "vmid", 0, "owner VM or CT id (required)")
	fl.StringVar(&filename, "filename", "", "volume name in <storage>:<name> form (required)")
	fl.StringVar(&size, "size", "", "volume size, e.g. 4G, 1024M, 2048 (kilobytes; required)")
	fl.StringVar(&format, "format", "", "image format: raw|qcow2|vmdk")
	cli.MustMarkRequired(cmd, "vmid")
	cli.MustMarkRequired(cmd, "filename")
	cli.MustMarkRequired(cmd, "size")
	return cmd
}

// newVolumeCopyCmd builds `pmx storage volume copy <volume>`.
func newVolumeCopyCmd() *cobra.Command {
	var (
		target     string
		targetNode string
	)
	cmd := &cobra.Command{
		Use:   "copy <volume>",
		Short: "Copy a volume to another volume",
		Long: "Copy a volume to a new target volume, optionally on a different node. " +
			"The copy runs as an asynchronous task; the command blocks until it finishes " +
			"unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			volume := args[0]
			storage, err := storageOfVolume(volume)
			if err != nil {
				return err
			}

			fl := cmd.Flags()
			params := &nodes.CreateStorageContent2Params{Target: target}
			if fl.Changed("target-node") {
				params.TargetNode = &targetNode
			}

			resp, err := deps.API.Nodes.CreateStorageContent2(cmd.Context(), deps.Node, storage, volume, params)
			if err != nil {
				return fmt.Errorf("copy volume %q to %q on node %q: %w", volume, target, deps.Node, err)
			}

			doneMsg := fmt.Sprintf("Copied volume %q to %q on node %q.", volume, target, deps.Node)
			var raw json.RawMessage
			if resp != nil {
				raw = json.RawMessage(*resp)
			}
			// A copy may complete synchronously and return a volume ID rather
			// than a worker UPID; treat an unparseable response as a finished
			// operation instead of an error.
			upid, err := apiclient.UPIDFromRaw(raw)
			if err != nil {
				upid = ""
			}
			return renderStorageTask(cmd, deps, upid, doneMsg)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&target, "target-volume", "", "target volume identifier (required)")
	fl.StringVar(&targetNode, "target-node", "", "target node (default: the resolved node)")
	cli.MustMarkRequired(cmd, "target-volume")
	return cmd
}
