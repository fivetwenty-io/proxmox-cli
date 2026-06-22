package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newDeleteCmd builds `pve qemu delete <vmid>`.
func newDeleteCmd() *cobra.Command {
	var (
		async                    bool
		yes                      bool
		purge                    bool
		skiplock                 bool
		destroyUnreferencedDisks bool
	)
	cmd := &cobra.Command{
		Use:   "delete <vmid|name>",
		Short: "Destroy a VM and its configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if !yes {
				return fmt.Errorf("refusing to delete VM %s without confirmation: pass --yes/-y", vmid)
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.DeleteQemuParams{}
			if cmd.Flags().Changed("purge") {
				params.Purge = boolPtr(purge)
			}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
			}
			if cmd.Flags().Changed("destroy-unreferenced-disks") {
				params.DestroyUnreferencedDisks = boolPtr(destroyUnreferencedDisks)
			}

			resp, err := deps.API.Nodes.DeleteQemu(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("delete VM %s on node %q: %w", vmid, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp), fmt.Sprintf("VM %s deleted.", vmid))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm destruction without prompting")
	cmd.Flags().BoolVar(&purge, "purge", false, "remove VMID from backup/replication jobs and HA")
	cmd.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
	cmd.Flags().BoolVar(&destroyUnreferencedDisks, "destroy-unreferenced-disks", false,
		"also destroy unreferenced disks matching this VMID")
	return cmd
}
