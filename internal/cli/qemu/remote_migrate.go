package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newRemoteMigrateCmd builds `pve qemu remote-migrate <vmid> --target-endpoint EP
// --target-storage ST --target-bridge BR [flags]`.
//
// Remote migration crosses cluster boundaries; it is irreversible in the sense
// that the originating VM may be removed from the source cluster. The command
// therefore requires --yes confirmation and is marked as a deferred e2e class
// (no live CI test).
func newRemoteMigrateCmd() *cobra.Command {
	var (
		async          bool
		yes            bool
		targetEndpoint string
		targetStorage  string
		targetBridge   string
		online         bool
		deleteSource   bool
		bwlimit        int64
		targetVmid     int64
	)
	cmd := &cobra.Command{
		Use:   "remote-migrate <vmid|name>",
		Short: "Migrate a VM to a remote (different) Proxmox cluster",
		Long: "Migrate a QEMU VM to a different Proxmox VE cluster. " +
			"--target-endpoint, --target-storage, and --target-bridge are required. " +
			"The source VM may be deleted after migration if --delete is set. " +
			"Pass --yes to confirm this irreversible cross-cluster operation.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf(
					"refusing to remote-migrate VM %s without confirmation: pass --yes/-y",
					vmid,
				)
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateQemuRemoteMigrateParams{
				TargetEndpoint: targetEndpoint,
				TargetStorage:  targetStorage,
				TargetBridge:   targetBridge,
			}
			if cmd.Flags().Changed("online") {
				params.Online = boolPtr(online)
			}
			if cmd.Flags().Changed("delete") {
				params.Delete = boolPtr(deleteSource)
			}
			if cmd.Flags().Changed("bwlimit") {
				params.Bwlimit = int64Ptr(bwlimit)
			}
			if cmd.Flags().Changed("target-vmid") {
				params.TargetVmid = int64Ptr(targetVmid)
			}

			resp, err := deps.API.Nodes.CreateQemuRemoteMigrate(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("remote-migrate VM %s from node %q: %w", vmid, node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = *resp
			}
			return finishAsync(cmd, deps, raw,
				fmt.Sprintf("VM %s remote-migrated to %q.", vmid, targetEndpoint))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false,
		"confirm the cross-cluster migration (irreversible if --delete is set)")
	cmd.Flags().StringVar(&targetEndpoint, "target-endpoint", "",
		"remote cluster API endpoint (required)")
	cmd.Flags().StringVar(&targetStorage, "target-storage", "",
		"storage mapping: a single storage ID or '1' to map each source to itself (required)")
	cmd.Flags().StringVar(&targetBridge, "target-bridge", "",
		"bridge mapping: a single bridge ID or '1' to map each source to itself (required)")
	cmd.Flags().BoolVar(&online, "online", false,
		"use live (online) migration for running VMs")
	cmd.Flags().BoolVar(&deleteSource, "delete", false,
		"remove the source VM after successful migration")
	cmd.Flags().Int64Var(&bwlimit, "bwlimit", 0, "I/O bandwidth limit in KiB/s")
	cmd.Flags().Int64Var(&targetVmid, "target-vmid", 0,
		"VMID to use on the target cluster (defaults to source VMID)")

	cli.MustMarkRequired(cmd, "target-endpoint")
	cli.MustMarkRequired(cmd, "target-storage")
	cli.MustMarkRequired(cmd, "target-bridge")
	return cmd
}
