package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// lifecycleCall describes a single lifecycle sub-command: how it invokes the API
// and what success message it prints when blocking.
type lifecycleCall func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error)

// newLifecycleCmd builds a standard lifecycle command with a --async flag.
func newLifecycleCmd(use, short, doneMsg string, call lifecycleCall, addFlags func(*cobra.Command)) *cobra.Command {
	var async bool
	cmd := &cobra.Command{
		Use:   use + " <vmid>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			// The local --async flag overrides only when explicitly set.
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}
			vmid := args[0]

			raw, err := call(cmd, deps, node, vmid)
			if err != nil {
				return err
			}
			return finishAsync(cmd, deps, raw, fmt.Sprintf(doneMsg, vmid))
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	if addFlags != nil {
		addFlags(cmd)
	}
	return cmd
}

// newStartCmd builds `pve qemu start <vmid>`.
func newStartCmd() *cobra.Command {
	var (
		timeout      int64
		migratedfrom string
		stateuri     string
	)
	cmd := newLifecycleCmd("start", "Start a VM", "VM %s started.",
		func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
			params := &nodes.CreateQemuStatusStartParams{}
			if cmd.Flags().Changed("timeout") {
				params.Timeout = int64Ptr(timeout)
			}
			if cmd.Flags().Changed("migratedfrom") {
				params.Migratedfrom = strPtr(migratedfrom)
			}
			if cmd.Flags().Changed("stateuri") {
				params.Stateuri = strPtr(stateuri)
			}
			resp, err := deps.API.Nodes.CreateQemuStatusStart(cmd.Context(), node, vmid, params)
			if err != nil {
				return nil, fmt.Errorf("start VM %s on node %q: %w", vmid, node, err)
			}
			return json.RawMessage(*resp), nil
		},
		func(c *cobra.Command) {
			c.Flags().Int64Var(&timeout, "timeout", 300, "wait maximal timeout seconds")
			c.Flags().StringVar(&migratedfrom, "migratedfrom", "", "source cluster node name")
			c.Flags().StringVar(&stateuri, "stateuri", "", "saved-state URI to restore from")
		})
	return cmd
}

// newStopCmd builds `pve qemu stop <vmid>`.
func newStopCmd() *cobra.Command {
	var (
		timeout    int64
		skiplock   bool
		keepActive bool
	)
	cmd := newLifecycleCmd("stop", "Stop a VM (hard power off)", "VM %s stopped.",
		func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
			params := &nodes.CreateQemuStatusStopParams{}
			if cmd.Flags().Changed("timeout") {
				params.Timeout = int64Ptr(timeout)
			}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
			}
			if cmd.Flags().Changed("keepActive") {
				params.KeepActive = boolPtr(keepActive)
			}
			resp, err := deps.API.Nodes.CreateQemuStatusStop(cmd.Context(), node, vmid, params)
			if err != nil {
				return nil, fmt.Errorf("stop VM %s on node %q: %w", vmid, node, err)
			}
			return json.RawMessage(*resp), nil
		},
		func(c *cobra.Command) {
			c.Flags().Int64Var(&timeout, "timeout", 300, "wait maximal timeout seconds")
			c.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
			c.Flags().BoolVar(&keepActive, "keepActive", false, "do not deactivate storage volumes")
		})
	return cmd
}

// newRebootCmd builds `pve qemu reboot <vmid>`.
func newRebootCmd() *cobra.Command {
	var timeout int64
	cmd := newLifecycleCmd("reboot", "Reboot a VM (graceful)", "VM %s rebooted.",
		func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
			params := &nodes.CreateQemuStatusRebootParams{}
			if cmd.Flags().Changed("timeout") {
				params.Timeout = int64Ptr(timeout)
			}
			resp, err := deps.API.Nodes.CreateQemuStatusReboot(cmd.Context(), node, vmid, params)
			if err != nil {
				return nil, fmt.Errorf("reboot VM %s on node %q: %w", vmid, node, err)
			}
			return json.RawMessage(*resp), nil
		},
		func(c *cobra.Command) {
			c.Flags().Int64Var(&timeout, "timeout", 300, "wait maximal timeout seconds for the shutdown")
		})
	return cmd
}

// newShutdownCmd builds `pve qemu shutdown <vmid>`.
func newShutdownCmd() *cobra.Command {
	var (
		timeout    int64
		forceStop  bool
		keepActive bool
	)
	cmd := newLifecycleCmd("shutdown", "Shut down a VM (graceful)", "VM %s shut down.",
		func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
			params := &nodes.CreateQemuStatusShutdownParams{}
			if cmd.Flags().Changed("timeout") {
				params.Timeout = int64Ptr(timeout)
			}
			if cmd.Flags().Changed("force-stop") {
				params.ForceStop = boolPtr(forceStop)
			}
			if cmd.Flags().Changed("keepActive") {
				params.KeepActive = boolPtr(keepActive)
			}
			resp, err := deps.API.Nodes.CreateQemuStatusShutdown(cmd.Context(), node, vmid, params)
			if err != nil {
				return nil, fmt.Errorf("shut down VM %s on node %q: %w", vmid, node, err)
			}
			return json.RawMessage(*resp), nil
		},
		func(c *cobra.Command) {
			c.Flags().Int64Var(&timeout, "timeout", 300, "wait maximal timeout seconds")
			c.Flags().BoolVar(&forceStop, "force-stop", false, "make sure the VM stops")
			c.Flags().BoolVar(&keepActive, "keepActive", false, "do not deactivate storage volumes")
		})
	return cmd
}

// newResetCmd builds `pve qemu reset <vmid>`.
func newResetCmd() *cobra.Command {
	var skiplock bool
	cmd := newLifecycleCmd("reset", "Reset a VM (hard reset)", "VM %s reset.",
		func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
			params := &nodes.CreateQemuStatusResetParams{}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
			}
			resp, err := deps.API.Nodes.CreateQemuStatusReset(cmd.Context(), node, vmid, params)
			if err != nil {
				return nil, fmt.Errorf("reset VM %s on node %q: %w", vmid, node, err)
			}
			return json.RawMessage(*resp), nil
		},
		func(c *cobra.Command) {
			c.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
		})
	return cmd
}

// newSuspendCmd builds `pve qemu suspend <vmid>`.
func newSuspendCmd() *cobra.Command {
	var (
		skiplock bool
		todisk   bool
	)
	cmd := newLifecycleCmd("suspend", "Suspend a VM", "VM %s suspended.",
		func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
			params := &nodes.CreateQemuStatusSuspendParams{}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
			}
			if cmd.Flags().Changed("todisk") {
				params.Todisk = boolPtr(todisk)
			}
			resp, err := deps.API.Nodes.CreateQemuStatusSuspend(cmd.Context(), node, vmid, params)
			if err != nil {
				return nil, fmt.Errorf("suspend VM %s on node %q: %w", vmid, node, err)
			}
			return json.RawMessage(*resp), nil
		},
		func(c *cobra.Command) {
			c.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
			c.Flags().BoolVar(&todisk, "todisk", false, "suspend to disk")
		})
	return cmd
}

// newResumeCmd builds `pve qemu resume <vmid>`.
func newResumeCmd() *cobra.Command {
	var (
		skiplock bool
		nocheck  bool
	)
	cmd := newLifecycleCmd("resume", "Resume a suspended VM", "VM %s resumed.",
		func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
			params := &nodes.CreateQemuStatusResumeParams{}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
			}
			if cmd.Flags().Changed("nocheck") {
				params.Nocheck = boolPtr(nocheck)
			}
			resp, err := deps.API.Nodes.CreateQemuStatusResume(cmd.Context(), node, vmid, params)
			if err != nil {
				return nil, fmt.Errorf("resume VM %s on node %q: %w", vmid, node, err)
			}
			return json.RawMessage(*resp), nil
		},
		func(c *cobra.Command) {
			c.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
			c.Flags().BoolVar(&nocheck, "nocheck", false, "skip the state file existence check")
		})
	return cmd
}
