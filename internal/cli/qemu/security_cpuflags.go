package qemu

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/propstr"
)

// cpuFlagInfo is one entry of the offline security-relevant CPU flag catalog.
type cpuFlagInfo struct {
	Name       string `json:"name"`
	Mitigation bool   `json:"mitigation"`
	Notes      string `json:"notes"`
}

// cpuFlagCatalog is the fixed set of 13 flags PVE allows per VM (apidoc.json
// cpu.flags enum), each with a one-line description and whether disabling it
// weakens a speculative-execution mitigation.
var cpuFlagCatalog = []cpuFlagInfo{
	{"aes", false, "AES-NI passthrough (perf, not a mitigation)"},
	{"amd-no-ssb", true, "Speculative Store Bypass"},
	{"amd-ssbd", true, "Speculative Store Bypass"},
	{"hv-evmcs", false, "Hyper-V enlightenments (perf)"},
	{"hv-tlbflush", false, "Hyper-V enlightenments (perf)"},
	{"ibpb", true, "indirect branch prediction barrier"},
	{"md-clear", true, "MDS"},
	{"nested-virt", false, "expose VMX/SVM (weakens isolation, blocks migration)"},
	{"pcid", false, "Meltdown/KPTI performance"},
	{"pdpe1gb", false, "1 GiB pages"},
	{"spec-ctrl", true, "Spectre v2 (IBRS)"},
	{"ssbd", true, "Speculative Store Bypass"},
	{"virt-ssbd", true, "Speculative Store Bypass"},
}

func findCPUFlag(name string) *cpuFlagInfo {
	for i := range cpuFlagCatalog {
		if cpuFlagCatalog[i].Name == name {
			return &cpuFlagCatalog[i]
		}
	}
	return nil
}

func cpuFlagNames() []string {
	names := make([]string, len(cpuFlagCatalog))
	for i, f := range cpuFlagCatalog {
		names[i] = f.Name
	}
	return names
}

// didYouMean returns the catalog name closest to name (Levenshtein distance
// under 3), or "" when nothing is close enough to suggest.
func didYouMean(name string, candidates []string) string {
	best := ""
	bestDist := 3
	for _, c := range candidates {
		d := levenshtein(name, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// levenshtein computes the edit distance between a and b.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// splitFlagTokens splits a cpu= flags= sub-value ("+aes;-pcid") into its
// individual ;-separated tokens, dropping empty segments.
func splitFlagTokens(v string) []string {
	if v == "" {
		return nil
	}
	out := make([]string, 0)
	for tok := range strings.SplitSeq(v, ";") {
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// dedupNames returns names with duplicates removed, keeping each name's first
// occurrence and its original order. Used on --enable/--disable so a repeated
// name (e.g. from "--enable spec-ctrl,spec-ctrl") produces one token and one
// mention in messages, not two.
func dedupNames(names []string) []string {
	if len(names) == 0 {
		return names
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// applyFlagChanges removes any existing +/-FLAG token for a flag named in
// enable or disable, then appends the requested new tokens, leaving every
// untouched flag's token (and relative order) exactly as it was.
func applyFlagChanges(tokens, enable, disable []string) []string {
	touch := make(map[string]bool, len(enable)+len(disable))
	for _, f := range enable {
		touch[f] = true
	}
	for _, f := range disable {
		touch[f] = true
	}
	out := make([]string, 0, len(tokens)+len(enable)+len(disable))
	for _, tok := range tokens {
		if len(tok) < 2 {
			continue
		}
		if touch[tok[1:]] {
			continue
		}
		out = append(out, tok)
	}
	for _, f := range enable {
		out = append(out, "+"+f)
	}
	for _, f := range disable {
		out = append(out, "-"+f)
	}
	return out
}

// newSecurityCpuFlagsCmd builds `pve qemu security cpu-flags` and its
// show/set/describe sub-commands.
func newSecurityCpuFlagsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cpu-flags",
		Short: "Manage the VM's security-relevant CPU flags (Spectre/Meltdown mitigations and friends)",
		Long: "Manage the flags= list inside the cpu= config option with validated, structured " +
			"flags. PVE restricts per-VM flags to a fixed security-relevant set (aes, " +
			"amd-no-ssb, amd-ssbd, hv-evmcs, hv-tlbflush, ibpb, md-clear, nested-virt, pcid, " +
			"pdpe1gb, spec-ctrl, ssbd, virt-ssbd). The cputype and other cpu= sub-options are " +
			"preserved. This is NOT 'pve qemu cpu-flags', which lists the flags a NODE's " +
			"hardware supports.",
	}
	cmd.AddCommand(newSecurityCpuFlagsShowCmd(), newSecurityCpuFlagsSetCmd(), newSecurityCpuFlagsDescribeCmd())
	return cmd
}

// newSecurityCpuFlagsShowCmd builds `pve qemu security cpu-flags show <vmid|name>`.
func newSecurityCpuFlagsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show the VM's security-relevant CPU flags",
		Args:  cobra.ExactArgs(1),
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
			cp := parseCPUFlagsPosture(m)

			if (deps.Format == output.FormatTable || deps.Format == output.FormatPlain) && cp.CPUType == "host" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"note: cputype=host — most mitigation flags are irrelevant because the host CPU is passed through")
			}

			enabledSet := make(map[string]bool, len(cp.Enabled))
			for _, f := range cp.Enabled {
				enabledSet[f] = true
			}
			disabledSet := make(map[string]bool, len(cp.Disabled))
			for _, f := range cp.Disabled {
				disabledSet[f] = true
			}

			rows := make([][]string, 0, len(cpuFlagCatalog))
			for _, f := range cpuFlagCatalog {
				state := ""
				switch {
				case enabledSet[f.Name]:
					state = "+"
				case disabledSet[f.Name]:
					state = "-"
				}
				mit := ""
				if f.Mitigation {
					mit = "yes"
				}
				rows = append(rows, []string{f.Name, state, mit, f.Notes})
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Headers: []string{"FLAG", "STATE", "MITIGATION", "NOTES"},
				Rows:    rows,
				Raw:     cp,
			}, deps.Format)
		},
	}
}

// newSecurityCpuFlagsSetCmd builds `pve qemu security cpu-flags set <vmid|name>`.
func newSecurityCpuFlagsSetCmd() *cobra.Command {
	var (
		enable  []string
		disable []string
		clear   bool
		force   bool
		digest  string
		restart bool
	)

	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Enable or disable security-relevant CPU flags",
		Long: "Merge flag changes into the cpu= option: --enable adds +FLAG entries, --disable " +
			"adds -FLAG entries, and flags named in neither are left untouched. --clear removes " +
			"the whole flags= list. Names are validated against PVE's security-relevant set. " +
			"Explicitly disabling a mitigation flag (spec-ctrl, ssbd, virt-ssbd, amd-ssbd, " +
			"amd-no-ssb, ibpb, md-clear) exposes the guest to speculative-execution attacks and " +
			"requires --force.\n\n" +
			"Example: pve qemu security cpu-flags set web1 --enable spec-ctrl,ssbd,md-clear",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if !clear && len(enable) == 0 && len(disable) == 0 {
				return fmt.Errorf("no CPU flags given: specify at least one of --enable/--disable, or --clear")
			}
			if clear && (len(enable) > 0 || len(disable) > 0) {
				return fmt.Errorf("--clear is mutually exclusive with --enable/--disable")
			}
			enable = dedupNames(enable)
			disable = dedupNames(disable)

			all := append(append([]string{}, enable...), disable...)
			for _, name := range all {
				if findCPUFlag(name) == nil {
					msg := fmt.Sprintf("unknown CPU flag %q: allowed flags are %s", name, strings.Join(cpuFlagNames(), ", "))
					if s := didYouMean(name, cpuFlagNames()); s != "" {
						msg += fmt.Sprintf(" (did you mean %q?)", s)
					}
					return fmt.Errorf("%s", msg)
				}
			}
			enableSet := make(map[string]bool, len(enable))
			for _, f := range enable {
				enableSet[f] = true
			}
			for _, f := range disable {
				if enableSet[f] {
					return fmt.Errorf("flag %q named in both --enable and --disable", f)
				}
			}

			var mitigationsDisabled []string
			for _, f := range disable {
				if info := findCPUFlag(f); info != nil && info.Mitigation {
					mitigationsDisabled = append(mitigationsDisabled, f)
				}
			}
			if len(mitigationsDisabled) > 0 && !force {
				return fmt.Errorf(
					"refusing to disable mitigation flag(s) %s without --force: this exposes the "+
						"guest to speculative-execution attacks", strings.Join(mitigationsDisabled, ", "))
			}

			m, autoDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			raw, hasCPU := rawStr(m, "cpu")
			list := propstr.Parse(raw, "cputype")

			var msg string
			if clear {
				list.Delete("flags")
				msg = fmt.Sprintf("VM %s CPU flags cleared.", vmid)
			} else {
				flagsVal, _ := list.Get("flags")
				tokens := applyFlagChanges(splitFlagTokens(flagsVal), enable, disable)
				if len(tokens) == 0 {
					list.Delete("flags")
				} else {
					list.Set("flags", strings.Join(tokens, ";"))
				}
				parts := make([]string, 0, len(enable)+len(disable))
				for _, f := range enable {
					parts = append(parts, "+"+f)
				}
				for _, f := range disable {
					parts = append(parts, "-"+f)
				}
				msg = fmt.Sprintf("VM %s CPU flags updated (%s).", vmid, strings.Join(parts, " "))
				if !hasCPU {
					msg += " No cpu= was previously set; cputype is left to the PVE default (kvm64)."
				}
			}

			if len(mitigationsDisabled) > 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"WARNING: disabling mitigation flag(s): %s\n", strings.Join(mitigationsDisabled, ", "))
			}
			for _, f := range enable {
				if f == "nested-virt" {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"note: nested-virt exposes VMX/SVM to the guest and blocks live migration")
				}
			}

			params := &nodes.UpdateQemuConfigParams{}
			if s := list.String(); s == "" {
				params.Delete = strPtr("cpu")
			} else {
				params.Cpu = strPtr(s)
			}
			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update CPU flags for VM %s on node %q: %w", vmid, node, err)
			}

			suffix, err := mutationSuffix(cmd, deps, vmid, node, restart)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg + suffix, Raw: map[string]any{"vmid": vmid, "node": node}}, deps.Format)
		},
	}

	f := cmd.Flags()
	f.StringSliceVar(&enable, "enable", nil, "comma-separated flags to enable (+FLAG), repeatable")
	f.StringSliceVar(&disable, "disable", nil, "comma-separated flags to disable (-FLAG), repeatable")
	f.BoolVar(&clear, "clear", false, "remove the flags= list from cpu= entirely")
	f.BoolVar(&force, "force", false, "allow disabling a mitigation flag")
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	f.BoolVar(&restart, "restart", false, "reboot the VM after a successful change (applies pending config)")
	cmd.MarkFlagsMutuallyExclusive("clear", "enable")
	cmd.MarkFlagsMutuallyExclusive("clear", "disable")
	return cmd
}

// newSecurityCpuFlagsDescribeCmd builds `pve qemu security cpu-flags describe`,
// an offline catalog listing (Annotations noClient, mirrors lxc's caps describe).
func newSecurityCpuFlagsDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "List the security-relevant CPU flags PVE allows per VM",
		Long: "List every CPU flag settable per VM, what it mitigates or provides, and guidance " +
			"on when to enable it. Runs entirely offline. To see which flags a node's hardware " +
			"actually supports, use 'pve qemu cpu-flags'.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			rows := make([][]string, 0, len(cpuFlagCatalog))
			for _, f := range cpuFlagCatalog {
				mit := ""
				if f.Mitigation {
					mit = "yes"
				}
				rows = append(rows, []string{f.Name, mit, f.Notes})
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: []string{"FLAG", "MITIGATION", "NOTES"}, Rows: rows, Raw: cpuFlagCatalog},
				deps.Format)
		},
	}
}
