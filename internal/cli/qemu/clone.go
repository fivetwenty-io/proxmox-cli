package qemu

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
)

// newCloneCmd builds `pve qemu clone <vmid> --newid N [flags]`.
//
// The clone is created as an asynchronous PVE task (UPID). The command blocks
// until the task reaches a terminal state unless --async is given. Only flags
// that the caller explicitly sets are forwarded to the API; unset optional
// fields remain nil so PVE applies its own defaults (linked clone for
// templates, source node for --target, etc.).
func newCloneCmd() *cobra.Command {
	var (
		async       bool
		newid       int64
		name        string
		target      string
		full        bool
		pool        string
		storage     string
		description string
		snapname    string
	)
	cmd := &cobra.Command{
		Use:   "clone <vmid>",
		Short: "Clone a QEMU virtual machine",
		Long: "Clone an existing QEMU VM or template to a new VM. " +
			"By default a linked clone is created when the source is a template; " +
			"pass --full to force a full disk copy. " +
			"The command blocks until the clone task completes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if _, err := strconv.ParseInt(vmid, 10, 64); err != nil {
				return fmt.Errorf("invalid vmid %q: %w", vmid, err)
			}
			if !cmd.Flags().Changed("newid") {
				return fmt.Errorf("--newid is required: provide the VMID for the new clone")
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateQemuCloneParams{Newid: newid}
			fl := cmd.Flags()
			if fl.Changed("name") {
				params.Name = strPtr(name)
			}
			if fl.Changed("target-node") {
				params.Target = strPtr(target)
			}
			if fl.Changed("full") {
				params.Full = boolPtr(full)
			}
			if fl.Changed("pool") {
				params.Pool = strPtr(pool)
			}
			if fl.Changed("storage") {
				params.Storage = strPtr(storage)
			}
			if fl.Changed("description") {
				params.Description = strPtr(description)
			}
			if fl.Changed("snapname") {
				params.Snapname = strPtr(snapname)
			}

			resp, err := deps.API.Nodes.CreateQemuClone(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("clone VM %s on node %q: %w", vmid, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("VM %s cloned to %d.", vmid, newid))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().Int64Var(&newid, "newid", 0, "VMID for the new clone (required)")
	cmd.Flags().StringVar(&name, "name", "", "name for the new VM")
	cmd.Flags().StringVar(&target, "target-node", "", "target node for the clone (only valid when the source is on shared storage)")
	cmd.Flags().BoolVar(&full, "full", false, "create a full copy of all disks (always true for non-template VMs)")
	cmd.Flags().StringVar(&pool, "pool", "", "resource pool to place the new VM in")
	cmd.Flags().StringVar(&storage, "storage", "", "target storage for the cloned disks (full clone only)")
	cmd.Flags().StringVar(&description, "description", "", "description for the new VM")
	cmd.Flags().StringVar(&snapname, "snapname", "", "snapshot to clone from")
	return cmd
}
