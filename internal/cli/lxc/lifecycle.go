package lxc

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// emitTask handles the block-by-default / --async UPID contract shared by every
// mutating container command. raw is the UPID-bearing response; successMsg is the
// confirmation printed after a blocking wait completes.
func emitTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, successMsg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return err
	}

	if deps.Async {
		res := output.Result{
			Single:  map[string]string{"upid": upid},
			Raw:     map[string]string{"upid": upid},
			Message: upid,
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return err
	}
	res := output.Result{Message: successMsg}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// newStartCmd builds `pve lxc start <vmid|name>`.
func newStartCmd() *cobra.Command {
	var skiplock, debug bool
	cmd := &cobra.Command{
		Use:   "start <vmid|name>",
		Short: "Start a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.CreateLxcStatusStartParams{}
			fl := cmd.Flags()
			if fl.Changed("skiplock") {
				params.Skiplock = &skiplock
			}
			if fl.Changed("debug") {
				params.Debug = &debug
			}

			resp, err := deps.API.Nodes.CreateLxcStatusStart(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("start container %s: %w", vmid, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s started.", vmid))
		},
	}
	cmd.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
	cmd.Flags().BoolVar(&debug, "debug", false, "enable verbose debug logging on start")
	return cmd
}

// newStopCmd builds `pve lxc stop <vmid|name>`.
func newStopCmd() *cobra.Command {
	var skiplock, overruleShutdown bool
	cmd := &cobra.Command{
		Use:   "stop <vmid|name>",
		Short: "Stop a container immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.CreateLxcStatusStopParams{}
			fl := cmd.Flags()
			if fl.Changed("skiplock") {
				params.Skiplock = &skiplock
			}
			if fl.Changed("overrule-shutdown") {
				params.OverruleShutdown = &overruleShutdown
			}

			resp, err := deps.API.Nodes.CreateLxcStatusStop(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("stop container %s: %w", vmid, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s stopped.", vmid))
		},
	}
	cmd.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
	cmd.Flags().BoolVar(&overruleShutdown, "overrule-shutdown", false, "abort active shutdown tasks before stopping")
	return cmd
}

// newRebootCmd builds `pve lxc reboot <vmid|name>`.
func newRebootCmd() *cobra.Command {
	var timeout int64
	cmd := &cobra.Command{
		Use:   "reboot <vmid|name>",
		Short: "Reboot a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.CreateLxcStatusRebootParams{}
			if cmd.Flags().Changed("timeout") {
				params.Timeout = &timeout
			}

			resp, err := deps.API.Nodes.CreateLxcStatusReboot(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("reboot container %s: %w", vmid, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s rebooted.", vmid))
		},
	}
	cmd.Flags().Int64Var(&timeout, "timeout", 300, "wait up to this many seconds for the shutdown")
	return cmd
}

// newShutdownCmd builds `pve lxc shutdown <vmid|name>`.
func newShutdownCmd() *cobra.Command {
	var timeout int64
	var forceStop bool
	cmd := &cobra.Command{
		Use:   "shutdown <vmid|name>",
		Short: "Gracefully shut down a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.CreateLxcStatusShutdownParams{}
			fl := cmd.Flags()
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}
			if fl.Changed("force-stop") {
				params.ForceStop = &forceStop
			}

			resp, err := deps.API.Nodes.CreateLxcStatusShutdown(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("shut down container %s: %w", vmid, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s shut down.", vmid))
		},
	}
	cmd.Flags().Int64Var(&timeout, "timeout", 300, "wait up to this many seconds")
	cmd.Flags().BoolVar(&forceStop, "force-stop", false, "force a hard stop if graceful shutdown fails")
	return cmd
}

// newSuspendCmd builds `pve lxc suspend <vmid|name>`. The LXC suspend endpoint takes
// no parameters.
func newSuspendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suspend <vmid|name>",
		Short: "Suspend a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.CreateLxcStatusSuspend(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("suspend container %s: %w", vmid, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s suspended.", vmid))
		},
	}
}

// newResumeCmd builds `pve lxc resume <vmid|name>`. The LXC resume endpoint takes no
// parameters.
func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <vmid|name>",
		Short: "Resume a suspended container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.CreateLxcStatusResume(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("resume container %s: %w", vmid, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s resumed.", vmid))
		},
	}
}

// newDeleteCmd builds `pve lxc delete <vmid|name>`.
func newDeleteCmd() *cobra.Command {
	var yes, purge, force, destroyUnreferenced bool
	cmd := &cobra.Command{
		Use:   "delete <vmid|name>",
		Short: "Destroy a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if !yes {
				return fmt.Errorf("refusing to delete container %s without confirmation: pass --yes to proceed", vmid)
			}

			params := &nodes.DeleteLxcParams{}
			fl := cmd.Flags()
			if fl.Changed("purge") {
				params.Purge = &purge
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("destroy-unreferenced-disks") {
				params.DestroyUnreferencedDisks = &destroyUnreferenced
			}

			resp, err := deps.API.Nodes.DeleteLxc(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("delete container %s: %w", vmid, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s deleted.", vmid))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm destruction without prompting")
	cmd.Flags().BoolVar(&purge, "purge", false, "remove from backup/replication/HA jobs and ACLs")
	cmd.Flags().BoolVar(&force, "force", false, "force destroy even if running")
	cmd.Flags().BoolVar(&destroyUnreferenced, "destroy-unreferenced-disks", false,
		"also destroy unreferenced disks with this VMID")
	return cmd
}
