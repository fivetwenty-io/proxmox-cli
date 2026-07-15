package qemu

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/propstr"
)

// newSecurityNicCmd builds `pmx pve qemu security nic` and its show/firewall
// sub-commands.
func newSecurityNicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nic",
		Short: "Inspect and toggle per-NIC firewall coverage",
		Long: "Each VM network device opts into the PVE firewall individually via the firewall= " +
			"sub-option of its net[n] entry. A NIC with firewall=0 bypasses every VM firewall " +
			"rule even when the VM firewall is enabled. Rule and option management lives under " +
			"'pmx pve qemu firewall'.",
	}
	cmd.AddCommand(newSecurityNicShowCmd(), newSecurityNicFirewallCmd())
	return cmd
}

// newSecurityNicShowCmd builds `pmx pve qemu security nic show <vmid|name>`.
func newSecurityNicShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show per-NIC firewall coverage and link state",
		Long: "List each configured network device with its model, bridge, MAC, VLAN tag, " +
			"whether the per-VM firewall covers it (firewall=1), and whether its link is " +
			"forced down. In table/plain output, warns on stderr about any NIC that bypasses " +
			"an enabled VM firewall.",
		Example: `  pmx pve qemu security nic show 100
  pmx pve qemu security nic show web1`,
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
			nics := parseNICs(m)

			if deps.Format == output.FormatTable || deps.Format == output.FormatPlain {
				fwResp, err := deps.API.Nodes.ListQemuFirewallOptions(cmd.Context(), node, vmid)
				if err != nil {
					return fmt.Errorf("get firewall options for VM %s on node %q: %w", vmid, node, err)
				}
				fo := firewallOptionsFromResp(fwResp)
				if fo.Enable {
					for _, n := range nics {
						if !n.Firewall {
							_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
								"note: VM firewall is enabled but net%d has firewall=0 — its traffic bypasses all rules\n", n.Slot)
						}
					}
				}
			}

			rows := make([][]string, 0, len(nics))
			for _, n := range nics {
				rows = append(rows, []string{
					strconv.Itoa(n.Slot), n.Model, n.Bridge, n.MAC, n.Tag,
					strconv.FormatBool(n.Firewall), strconv.FormatBool(n.LinkDown),
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Headers: []string{"SLOT", "MODEL", "BRIDGE", "MAC", "VLAN", "FIREWALL", "LINK-DOWN"},
				Rows:    rows,
				Raw:     nics,
			}, deps.Format)
		},
	}
}

// dedupInts returns slots with duplicates removed, keeping each value's first
// occurrence and its original order. A repeated "--slot N N" would otherwise
// double-list the same NIC in the changed/unchanged report even though the
// PUT itself already dedupes via netParams (a map).
func dedupInts(slots []int) []int {
	if len(slots) == 0 {
		return slots
	}
	seen := make(map[int]bool, len(slots))
	out := make([]int, 0, len(slots))
	for _, s := range slots {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// newSecurityNicFirewallCmd builds `pmx pve qemu security nic firewall <vmid|name>`.
func newSecurityNicFirewallCmd() *cobra.Command {
	var (
		on      bool
		off     bool
		slots   []int
		all     bool
		digest  string
		restart bool
	)

	cmd := &cobra.Command{
		Use:   "firewall <vmid|name>",
		Short: "Enable or disable the firewall on specific NICs",
		Long: "Flip the firewall= sub-option on one or more net[n] devices, preserving every " +
			"other sub-option (model, bridge, MAC, VLAN, ...). With hotplug=network the change " +
			"applies live; otherwise it is pending until restart.\n\n" +
			"Example: pmx pve qemu security nic firewall web1 --on --all",
		Example: `  pmx pve qemu security nic firewall web1 --on --all`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if !on && !off {
				return fmt.Errorf("exactly one of --on or --off is required")
			}
			if !fl.Changed("slot") && !all {
				return fmt.Errorf("exactly one of --slot or --all is required")
			}

			m, autoDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			nics := parseNICs(m)
			nicBySlot := make(map[int]nicPosture, len(nics))
			configured := make([]int, 0, len(nics))
			for _, n := range nics {
				nicBySlot[n.Slot] = n
				configured = append(configured, n.Slot)
			}
			sort.Ints(configured)

			targets := dedupInts(slots)
			if all {
				targets = configured
			}
			if len(targets) == 0 {
				return fmt.Errorf("VM %s has no configured network devices", vmid)
			}

			var changed, unchanged []string
			netParams := map[int]string{}
			for _, slot := range targets {
				n, ok := nicBySlot[slot]
				if !ok {
					names := make([]string, len(configured))
					for i, s := range configured {
						names[i] = "net" + strconv.Itoa(s)
					}
					return fmt.Errorf("VM %s has no net%d; configured NICs are: %s", vmid, slot, strings.Join(names, ", "))
				}
				if n.Firewall == on {
					unchanged = append(unchanged, fmt.Sprintf("net%d", slot))
					continue
				}
				list := propstr.Parse(n.Raw, "")
				if on {
					list.Set("firewall", "1")
				} else {
					list.Delete("firewall")
				}
				netParams[slot] = list.String()
				changed = append(changed, fmt.Sprintf("net%d", slot))
			}

			state := "enabled"
			if off {
				state = "disabled"
			}

			if off && len(changed) > 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"WARNING: disabling the firewall on %s of VM %s — its traffic bypasses all VM firewall rules\n",
					strings.Join(changed, ", "), vmid)
			}

			if len(changed) == 0 {
				parts := make([]string, len(unchanged))
				for i, s := range unchanged {
					parts[i] = s + " already " + state
				}
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{
						Message: fmt.Sprintf("VM %s: %s; no change.", vmid, strings.Join(parts, ", ")),
						Raw:     map[string]any{"vmid": vmid, "node": node, "changed": []string{}, "unchanged": unchanged},
					}, deps.Format)
			}

			params := &nodes.UpdateQemuConfigParams{Net: netParams}
			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update NIC firewall settings for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}

			parts := make([]string, 0, len(changed)+len(unchanged))
			for _, s := range changed {
				parts = append(parts, fmt.Sprintf("%s firewall %s", s, state))
			}
			for _, s := range unchanged {
				parts = append(parts, fmt.Sprintf("%s already %s", s, state))
			}
			msg := fmt.Sprintf("VM %s: %s.", vmid, strings.Join(parts, ", "))
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Message: msg + suffix,
					Raw:     map[string]any{"vmid": vmid, "node": node, "changed": changed, "unchanged": unchanged},
				}, deps.Format)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&on, "on", false, "enable firewall coverage on the selected NICs")
	f.BoolVar(&off, "off", false, "disable firewall coverage on the selected NICs")
	f.IntSliceVar(&slots, "slot", nil, "NIC slot to change, e.g. 0 for net0 (repeatable)")
	f.BoolVar(&all, "all", false, "change every configured NIC")
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	f.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")
	cmd.MarkFlagsMutuallyExclusive("on", "off")
	cmd.MarkFlagsMutuallyExclusive("slot", "all")
	return cmd
}
