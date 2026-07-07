package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// lifecycleCall describes a single lifecycle sub-command: how it invokes the API
// and what success message it prints when blocking.
type lifecycleCall func(cmd *cobra.Command, deps *cli.Deps, node, vmid string) (json.RawMessage, error)

// newLifecycleCmd builds a standard lifecycle command with a --async flag.
func newLifecycleCmd(use, short, doneMsg string, call lifecycleCall, addFlags func(*cobra.Command)) *cobra.Command {
	var async bool
	cmd := &cobra.Command{
		Use:   use + " <vmid|name>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			// The local --async flag overrides only when explicitly set.
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

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
		timeout            int64
		migratedfrom       string
		stateuri           string
		forceCPU           string
		machine            string
		skiplock           bool
		migrationNetwork   string
		migrationType      string
		targetstorage      string
		netsHostMtu        string
		withConntrackState bool
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
			if cmd.Flags().Changed("force-cpu") {
				params.ForceCpu = strPtr(forceCPU)
			}
			if cmd.Flags().Changed("machine") {
				params.Machine = strPtr(machine)
			}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
			}
			if cmd.Flags().Changed("migration-network") {
				params.MigrationNetwork = strPtr(migrationNetwork)
			}
			if cmd.Flags().Changed("migration-type") {
				params.MigrationType = strPtr(migrationType)
			}
			if cmd.Flags().Changed("targetstorage") {
				params.Targetstorage = strPtr(targetstorage)
			}
			if cmd.Flags().Changed("nets-host-mtu") {
				params.NetsHostMtu = strPtr(netsHostMtu)
			}
			if cmd.Flags().Changed("with-conntrack-state") {
				params.WithConntrackState = boolPtr(withConntrackState)
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
			c.Flags().StringVar(&forceCPU, "force-cpu", "", "override the QEMU '-cpu' argument (live migration only)")
			c.Flags().StringVar(&machine, "machine", "", "specify the QEMU machine type for this start")
			c.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
			c.Flags().StringVar(&migrationNetwork, "migration-network", "",
				"[advanced] CIDR of the (sub) network used for migration traffic")
			c.Flags().StringVar(&migrationType, "migration-type", "",
				"[advanced] migration traffic encryption type: secure or insecure")
			c.Flags().StringVar(&targetstorage, "targetstorage", "",
				"[advanced] mapping from source to target storages, e.g. local-lvm:local-lvm or 1 for all")
			c.Flags().StringVar(&netsHostMtu, "nets-host-mtu", "",
				"[advanced] list of VirtIO network devices and their effective host MTU for migration")
			c.Flags().BoolVar(&withConntrackState, "with-conntrack-state", false,
				"[advanced] migrate conntrack entries for running VMs")
		})
	return cmd
}

// newStopCmd builds `pve qemu stop <vmid>`.
func newStopCmd() *cobra.Command {
	var (
		timeout          int64
		skiplock         bool
		keepActive       bool
		overruleShutdown bool
		migratedfrom     string
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
			if cmd.Flags().Changed("overrule-shutdown") {
				params.OverruleShutdown = boolPtr(overruleShutdown)
			}
			if cmd.Flags().Changed("migratedfrom") {
				params.Migratedfrom = strPtr(migratedfrom)
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
			c.Flags().BoolVar(&overruleShutdown, "overrule-shutdown", false,
				"abort a pending graceful shutdown task before stopping")
			c.Flags().StringVar(&migratedfrom, "migratedfrom", "",
				"[advanced] source cluster node name (set automatically during migration)")
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
		skiplock   bool
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
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
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
			c.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
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
		skiplock     bool
		todisk       bool
		statestorage string
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
			if cmd.Flags().Changed("statestorage") {
				params.Statestorage = strPtr(statestorage)
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
			c.Flags().StringVar(&statestorage, "statestorage", "",
				"target storage for the VM state (requires --todisk)")
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
