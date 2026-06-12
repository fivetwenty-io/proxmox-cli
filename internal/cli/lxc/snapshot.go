package lxc

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// lxcSnapshotEntry is the subset of a /nodes/{node}/lxc/{vmid}/snapshot element
// rendered in the snapshot list table.
type lxcSnapshotEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Snaptime    int64  `json:"snaptime"`
	Parent      string `json:"parent"`
}

// newSnapshotCmd builds `pve lxc snapshot` and its sub-commands.
func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage container snapshots",
	}
	cmd.AddCommand(
		newSnapshotListCmd(),
		newSnapshotCreateCmd(),
		newSnapshotDeleteCmd(),
		newSnapshotRollbackCmd(),
		newSnapshotShowCmd(),
		newSnapshotUpdateCmd(),
	)
	return cmd
}

// newSnapshotCreateCmd builds `pve lxc snapshot create <vmid> <snapname>`.
func newSnapshotCreateCmd() *cobra.Command {
	var (
		async       bool
		description string
	)
	cmd := &cobra.Command{
		Use:   "create <vmid> <snapname>",
		Short: "Create a snapshot of a container",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateLxcSnapshotParams{Snapname: snapname}
			if cmd.Flags().Changed("description") {
				params.Description = &description
			}

			resp, err := deps.API.Nodes.CreateLxcSnapshot(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("create snapshot %q for container %s: %w", snapname, vmid, err)
			}
			return emitTask(cmd, deps, *resp,
				fmt.Sprintf("Snapshot %s of container %s created.", snapname, vmid))
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&description, "description", "", "snapshot description")
	return cmd
}

// newSnapshotDeleteCmd builds `pve lxc snapshot delete <vmid> <snapname>`.
func newSnapshotDeleteCmd() *cobra.Command {
	var (
		async bool
		force bool
	)
	cmd := &cobra.Command{
		Use:   "delete <vmid> <snapname>",
		Short: "Delete a snapshot of a container",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.DeleteLxcSnapshotParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}

			resp, err := deps.API.Nodes.DeleteLxcSnapshot(cmd.Context(), node, vmid, snapname, params)
			if err != nil {
				return fmt.Errorf("delete snapshot %q for container %s: %w", snapname, vmid, err)
			}
			return emitTask(cmd, deps, *resp,
				fmt.Sprintf("Snapshot %s of container %s deleted.", snapname, vmid))
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().BoolVar(&force, "force", false, "remove the snapshot from config even if its removal fails")
	return cmd
}

// newSnapshotRollbackCmd builds `pve lxc snapshot rollback <vmid> <snapname>`.
func newSnapshotRollbackCmd() *cobra.Command {
	var (
		async bool
		start bool
	)
	cmd := &cobra.Command{
		Use:   "rollback <vmid> <snapname>",
		Short: "Roll a container back to a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateLxcSnapshotRollbackParams{}
			if cmd.Flags().Changed("start") {
				params.Start = &start
			}

			resp, err := deps.API.Nodes.CreateLxcSnapshotRollback(cmd.Context(), node, vmid, snapname, params)
			if err != nil {
				return fmt.Errorf("roll back container %s to snapshot %q: %w", vmid, snapname, err)
			}
			return emitTask(cmd, deps, *resp,
				fmt.Sprintf("Container %s rolled back to snapshot %s.", vmid, snapname))
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().BoolVar(&start, "start", false, "start the container after a successful rollback")
	return cmd
}

// newSnapshotListCmd builds `pve lxc snapshot list <vmid>`.
func newSnapshotListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vmid>",
		Short: "List snapshots of a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			resp, err := deps.API.Nodes.ListLxcSnapshot(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("list snapshots for container %s: %w", vmid, err)
			}

			entries := make([]lxcSnapshotEntry, 0, len(*resp))
			for _, raw := range *resp {
				var e lxcSnapshotEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode snapshot entry: %w", err)
				}
				entries = append(entries, e)
			}

			res := output.Result{
				Headers: []string{"SNAPNAME", "DESCRIPTION", "SNAPTIME", "PARENT"},
				Raw:     entries,
			}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{
					e.Name, e.Description, fmtSnaptime(e.Snaptime), e.Parent,
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newSnapshotShowCmd builds `pve lxc snapshot show <vmid> <snapname>`.
//
// Returns the configuration stored with a named snapshot.
func newSnapshotShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid> <snapname>",
		Short: "Show the configuration stored with a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]

			resp, err := deps.API.Nodes.ListLxcSnapshotConfig(cmd.Context(), node, vmid, snapname)
			if err != nil {
				return fmt.Errorf("get config for snapshot %q of container %s: %w", snapname, vmid, err)
			}

			// resp is a json.RawMessage alias; decode to a generic map for key/value rendering.
			var decoded map[string]any
			if resp != nil && len(*resp) > 0 {
				if err := json.Unmarshal(*resp, &decoded); err != nil {
					return fmt.Errorf("decode snapshot config: %w", err)
				}
			}
			single := make(map[string]string, len(decoded))
			for k, v := range decoded {
				if v != nil {
					single[k] = fmt.Sprintf("%v", v)
				}
			}

			var raw any
			if resp != nil {
				raw = *resp
			}
			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newSnapshotUpdateCmd builds `pve lxc snapshot update <vmid> <snapname> --description DESC`.
//
// Updates metadata (description) stored with an existing snapshot.
func newSnapshotUpdateCmd() *cobra.Command {
	var description string

	cmd := &cobra.Command{
		Use:   "update <vmid> <snapname>",
		Short: "Update the description of a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]

			if !cmd.Flags().Changed("description") {
				return fmt.Errorf("no fields specified: pass --description to update the snapshot")
			}

			params := &nodes.UpdateLxcSnapshotConfigParams{}
			params.Description = &description

			if err := deps.API.Nodes.UpdateLxcSnapshotConfig(cmd.Context(), node, vmid, snapname, params); err != nil {
				return fmt.Errorf("update snapshot %q for container %s: %w", snapname, vmid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Snapshot %s of container %s updated.", snapname, vmid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&description, "description", "", "new description for the snapshot")
	return cmd
}

// fmtSnaptime renders a unix snapshot timestamp as RFC3339, or "-" when zero.
func fmtSnaptime(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}
