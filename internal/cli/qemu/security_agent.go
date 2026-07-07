package qemu

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/propstr"
)

// newSecurityAgentCmd builds `pve qemu security agent` and its show/set
// sub-commands.
func newSecurityAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Inspect and set the QEMU guest-agent configuration (the agent= config option)",
		Long: "Inspect and set the agent= config option with structured per-field flags, merging " +
			"with the current value. This configures the virtio/isa agent DEVICE and PVE's use " +
			"of it; it is NOT 'pve qemu agent', which runs operational guest-agent commands " +
			"(exec, file-read, ...). The raw --agent string on 'pve qemu create' and " +
			"'pve qemu config set' stays available as an escape hatch.",
	}
	cmd.AddCommand(newSecurityAgentShowCmd(), newSecurityAgentSetCmd())
	return cmd
}

// newSecurityAgentShowCmd builds `pve qemu security agent show <vmid|name>`.
func newSecurityAgentShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show guest-agent configuration",
		Long: "Show every agent= sub-option with its effective value (unset keys shown at their " +
			"API default: enabled=false, freeze-fs=true, fstrim_cloned_disks=false, type=virtio).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			m, _, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			raw, _ := rawStr(m, "agent")
			ap := parseAgentPosture(raw)

			order := []string{"enabled", "freeze-fs", "fstrim_cloned_disks", "type"}
			values := map[string]any{
				"enabled":             ap.Enabled,
				"freeze-fs":           ap.FreezeFS,
				"fstrim_cloned_disks": ap.FstrimClonedDisks,
				"type":                ap.Type,
			}
			rows := make([][]string, 0, len(order))
			for _, k := range order {
				rows = append(rows, []string{k, fmt.Sprintf("%v", values[k])})
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: []string{"OPTION", "VALUE"}, Rows: rows, Raw: ap}, deps.Format)
		},
	}
}

// newSecurityAgentSetCmd builds `pve qemu security agent set <vmid|name>`.
func newSecurityAgentSetCmd() *cobra.Command {
	var (
		enabled   bool
		freezeFS  bool
		fstrim    bool
		agentType string
		reset     bool
		digest    string
		restart   bool
	)

	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Set guest-agent configuration options",
		Long: "Set one or more agent= sub-options, merging with the current value: only the flags " +
			"you pass are changed. Pass --reset to delete agent= entirely (agent disabled).\n\n" +
			"Security note: an enabled agent lets anyone with VM.GuestAgent.* privileges run " +
			"commands and read/write files inside the guest; freeze-fs affects snapshot/backup " +
			"consistency, not isolation.\n\n" +
			"Example: pve qemu security agent set web1 --enabled --type virtio",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if !reset && !fl.Changed("enabled") && !fl.Changed("freeze-fs") &&
				!fl.Changed("fstrim-cloned-disks") && !fl.Changed("type") {
				return fmt.Errorf("no agent flags given: specify at least one of " +
					"--enabled/--freeze-fs/--fstrim-cloned-disks/--type, or --reset")
			}
			if fl.Changed("type") && agentType != "virtio" && agentType != "isa" {
				return fmt.Errorf("--type must be one of virtio, isa (got %q)", agentType)
			}

			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			m, autoDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}

			params := &nodes.UpdateQemuConfigParams{}
			var msg string

			if reset {
				params.Delete = strPtr("agent")
				msg = fmt.Sprintf("VM %s agent configuration removed (agent disabled).", vmid)
			} else {
				raw, _ := rawStr(m, "agent")
				list := propstr.Parse(raw, "enabled")
				if fl.Changed("enabled") {
					list.Set("enabled", boolToStr(enabled))
				}
				if fl.Changed("freeze-fs") {
					list.Set("freeze-fs", boolToStr(freezeFS))
				}
				if fl.Changed("fstrim-cloned-disks") {
					list.Set("fstrim_cloned_disks", boolToStr(fstrim))
				}
				if fl.Changed("type") {
					list.Set("type", agentType)
				}

				if isAgentAtAPIDefault(list) {
					params.Delete = strPtr("agent")
					msg = fmt.Sprintf(
						"VM %s agent configuration updated (all sub-options at their API default; agent= removed).", vmid)
				} else {
					s := list.String()
					params.Agent = strPtr(s)
					msg = fmt.Sprintf("VM %s agent configuration updated.", vmid)
				}
			}

			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update agent config for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg + suffix, Raw: map[string]any{"vmid": vmid, "node": node}}, deps.Format)
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&enabled, "enabled", false, "enable communication with the QEMU guest agent (enabled=1)")
	fl.BoolVar(&freezeFS, "freeze-fs", false, "freeze guest filesystems via QGA on snapshot/backup/clone (freeze-fs=1)")
	fl.BoolVar(&fstrim, "fstrim-cloned-disks", false, "run fstrim after disk move / migration (fstrim_cloned_disks=1)")
	fl.StringVar(&agentType, "type", "", "agent device type: virtio or isa")
	fl.BoolVar(&reset, "reset", false, "delete agent= entirely (PVE default: disabled)")
	fl.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	fl.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")

	for _, name := range []string{"enabled", "freeze-fs", "fstrim-cloned-disks", "type"} {
		cmd.MarkFlagsMutuallyExclusive("reset", name)
	}
	return cmd
}
