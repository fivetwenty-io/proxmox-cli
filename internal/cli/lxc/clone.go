package lxc

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newCloneCmd builds `pmx pve lxc clone <vmid> --newid N [flags]`.
//
// The clone is created as an asynchronous PVE task (UPID). The command blocks
// until the task reaches a terminal state unless --async is given. Only flags
// the caller explicitly sets are forwarded to the API; unset optional fields
// remain nil so PVE applies its own defaults (a linked clone for templates, the
// source node for --target-node, etc.).
func newCloneCmd() *cobra.Command {
	var (
		async       bool
		newid       int64
		hostname    string
		target      string
		full        bool
		pool        string
		storage     string
		description string
		snapname    string
		bwlimit     float64
	)
	cmd := &cobra.Command{
		Use:   "clone <vmid|name>",
		Short: "Clone an LXC container",
		Long: "Clone an existing LXC container or template to a new container. " +
			"By default a linked clone is created when the source is a template; " +
			"pass --full to force a full disk copy. " +
			"The command blocks until the clone task completes unless --async is set.",
		Example: `  pmx pve lxc clone 200 --newid 201
  pmx pve lxc clone 200 --newid 201 --full --storage local-lvm --async`,
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

			params := &nodes.CreateLxcCloneParams{Newid: newid}
			fl := cmd.Flags()
			if fl.Changed("hostname") {
				params.Hostname = &hostname
			}
			if fl.Changed("target-node") {
				params.Target = &target
			}
			if fl.Changed("full") {
				params.Full = &full
			}
			if fl.Changed("pool") {
				params.Pool = &pool
			}
			if fl.Changed("storage") {
				params.Storage = &storage
			}
			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("snapname") {
				params.Snapname = &snapname
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}

			resp, err := deps.API.Nodes.CreateLxcClone(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("clone container %s on node %q: %w", vmid, node, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s cloned to %d.", vmid, newid))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().Int64Var(&newid, "newid", 0, "VMID for the new clone (required)")
	cmd.Flags().StringVar(&hostname, "hostname", "", "hostname for the new container")
	cmd.Flags().StringVar(&target, "target-node", "", "target node for the clone (only valid when the source is on shared storage)")
	cmd.Flags().BoolVar(&full, "full", false, "create a full copy of all disks (always true for non-template containers)")
	cmd.Flags().StringVar(&pool, "pool", "", "resource pool to place the new container in")
	cmd.Flags().StringVar(&storage, "storage", "", "target storage for the cloned volumes (full clone only)")
	cmd.Flags().StringVar(&description, "description", "", "description for the new container")
	cmd.Flags().StringVar(&snapname, "snapname", "", "snapshot to clone from")
	cmd.Flags().Float64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	return cmd
}
