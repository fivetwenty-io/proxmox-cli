package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newCloneCmd builds `pmx pve qemu clone <vmid> --newid N [flags]`.
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
		format      string
		bwlimit     int64
		description string
		snapname    string
	)
	cmd := &cobra.Command{
		Use:   "clone <vmid|name>",
		Short: "Clone a QEMU virtual machine",
		Long: "Clone an existing QEMU VM or template to a new VM. " +
			"By default a linked clone is created when the source is a template; " +
			"pass --full to force a full disk copy. " +
			"The command blocks until the clone task completes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
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
			if fl.Changed("format") {
				params.Format = strPtr(format)
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = int64Ptr(bwlimit)
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
	cmd.Flags().StringVar(&format, "format", "", "target disk format, e.g. raw, qcow2, or vmdk (full clone only)")
	cmd.Flags().Int64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	cmd.Flags().StringVar(&description, "description", "", "description for the new VM")
	cmd.Flags().StringVar(&snapname, "snapname", "", "snapshot to clone from")
	return cmd
}
