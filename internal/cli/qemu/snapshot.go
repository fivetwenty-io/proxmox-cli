package qemu

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newSnapshotCmd builds the `pve qemu snapshot` sub-group.
func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage VM snapshots",
	}
	cmd.AddCommand(
		newSnapshotListCmd(),
		newSnapshotCreateCmd(),
		newSnapshotDeleteCmd(),
		newSnapshotRollbackCmd(),
	)
	return cmd
}

// snapshotEntry is the minimal decoded shape of one entry from nodes.ListQemuSnapshot.
type snapshotEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Snaptime    int64  `json:"snaptime"`
	Vmstate     int64  `json:"vmstate"`
	Parent      string `json:"parent"`
}

// newSnapshotListCmd builds `pve qemu snapshot list <vmid>`.
func newSnapshotListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vmid>",
		Short: "List snapshots of a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			resp, err := deps.API.Nodes.ListQemuSnapshot(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("list snapshots for VM %s on node %q: %w", vmid, node, err)
			}

			headers := []string{"SNAPNAME", "DESCRIPTION", "SNAPTIME", "VMSTATE", "PARENT"}
			entries := make([]snapshotEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e snapshotEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode snapshot entry: %w", err)
					}
					entries = append(entries, e)
					snaptime := ""
					if e.Snaptime != 0 {
						snaptime = strconv.FormatInt(e.Snaptime, 10)
					}
					rows = append(rows, []string{
						e.Name,
						e.Description,
						snaptime,
						strconv.FormatInt(e.Vmstate, 10),
						e.Parent,
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

// newSnapshotCreateCmd builds `pve qemu snapshot create <vmid> <snapname>`.
func newSnapshotCreateCmd() *cobra.Command {
	var (
		async       bool
		description string
		vmstate     bool
	)
	cmd := &cobra.Command{
		Use:   "create <vmid> <snapname>",
		Short: "Create a snapshot of a VM",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateQemuSnapshotParams{Snapname: snapname}
			if cmd.Flags().Changed("description") {
				params.Description = strPtr(description)
			}
			if cmd.Flags().Changed("vmstate") {
				params.Vmstate = boolPtr(vmstate)
			}

			resp, err := deps.API.Nodes.CreateQemuSnapshot(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("create snapshot %q for VM %s on node %q: %w", snapname, vmid, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("Snapshot %s of VM %s created.", snapname, vmid))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&description, "description", "", "snapshot description")
	cmd.Flags().BoolVar(&vmstate, "vmstate", false, "include the VM RAM state in the snapshot")
	return cmd
}

// newSnapshotDeleteCmd builds `pve qemu snapshot delete <vmid> <snapname>`.
func newSnapshotDeleteCmd() *cobra.Command {
	var (
		async bool
		yes   bool
		force bool
	)
	cmd := &cobra.Command{
		Use:   "delete <vmid> <snapname>",
		Short: "Delete a snapshot of a VM",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]
			if !yes {
				return fmt.Errorf("refusing to delete snapshot %q without confirmation: pass --yes/-y", snapname)
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.DeleteQemuSnapshotParams{}
			if cmd.Flags().Changed("force") {
				params.Force = boolPtr(force)
			}

			resp, err := deps.API.Nodes.DeleteQemuSnapshot(cmd.Context(), node, vmid, snapname, params)
			if err != nil {
				return fmt.Errorf("delete snapshot %q for VM %s on node %q: %w", snapname, vmid, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("Snapshot %s of VM %s deleted.", snapname, vmid))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().BoolVar(&force, "force", false, "remove from config even if removing disk snapshots fails")
	return cmd
}

// newSnapshotRollbackCmd builds `pve qemu snapshot rollback <vmid> <snapname>`.
func newSnapshotRollbackCmd() *cobra.Command {
	var (
		async bool
		start bool
	)
	cmd := &cobra.Command{
		Use:   "rollback <vmid> <snapname>",
		Short: "Roll a VM back to a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, snapname := args[0], args[1]
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateQemuSnapshotRollbackParams{}
			if cmd.Flags().Changed("start") {
				params.Start = boolPtr(start)
			}

			resp, err := deps.API.Nodes.CreateQemuSnapshotRollback(cmd.Context(), node, vmid, snapname, params)
			if err != nil {
				return fmt.Errorf("roll back VM %s to snapshot %q on node %q: %w", vmid, snapname, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("VM %s rolled back to snapshot %s.", vmid, snapname))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().BoolVar(&start, "start", false, "start the VM after a successful rollback")
	return cmd
}
