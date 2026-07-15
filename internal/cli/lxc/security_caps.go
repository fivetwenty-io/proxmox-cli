package lxc

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/lxcconf"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/sshcmd"
)

// newSecurityCapsCmd builds `pmx lxc security caps` and its sub-commands: the
// manager for the low-level Linux capability whitelist.
func newSecurityCapsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "caps",
		Short: "Manage the container capability whitelist (lxc.cap.keep / lxc.cap.drop)",
		Long: "Manage the Linux capabilities an LXC container keeps or drops. PVE exposes no API " +
			"for lxc.cap.keep / lxc.cap.drop, so the mutating verbs (set, add, remove, reset) edit " +
			"/etc/pve/lxc/<vmid>.conf on the node over root ssh; changes apply on the next " +
			"container start. Reads (show, describe) use only the API and need no ssh.\n\n" +
			"Capability names are accepted in any spelling (CAP_NET_ADMIN, NET_ADMIN, net_admin) " +
			"and stored canonically. Granting a dangerous capability (sys_admin, sys_module, " +
			"sys_rawio, sys_boot, sys_time) requires --force.",
	}
	cmd.AddCommand(
		newSecurityCapsShowCmd(),
		newSecurityCapsDescribeCmd(),
		newSecurityCapsSetCmd(),
		newSecurityCapsAddCmd(),
		newSecurityCapsRemoveCmd(),
		newSecurityCapsResetCmd(),
	)
	return cmd
}

// splitCaps splits a comma-separated capability list, trimming spaces and
// dropping empty items.
func splitCaps(s string) []string {
	out := make([]string, 0)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// normalizeCaps canonicalises and de-duplicates a caller-supplied capability
// list, preserving first-seen order and failing on the first unknown name (with
// the did-you-mean hint from lxcconf).
func normalizeCaps(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, c := range in {
		n, err := lxcconf.Normalize(c)
		if err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

// canonCap lowercases a capability token and strips a leading cap_ prefix for
// set-membership comparison against on-disk tokens, without validating it
// against the catalog (lxcconf.SetCaps is the validator on write).
func canonCap(s string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "cap_")
}

// appendCaps returns existing with each of add that is not already present
// appended, comparing canonically and preserving order.
func appendCaps(existing, add []string) []string {
	out := append([]string{}, existing...)
	have := make(map[string]bool, len(existing))
	for _, c := range existing {
		have[canonCap(c)] = true
	}
	for _, c := range add {
		if !have[canonCap(c)] {
			have[canonCap(c)] = true
			out = append(out, c)
		}
	}
	return out
}

// removeCaps returns existing without any entry that canonically matches one of
// rm, preserving order.
func removeCaps(existing, rm []string) []string {
	drop := make(map[string]bool, len(rm))
	for _, c := range rm {
		drop[canonCap(c)] = true
	}
	out := make([]string, 0, len(existing))
	for _, c := range existing {
		if !drop[canonCap(c)] {
			out = append(out, c)
		}
	}
	return out
}

// dangerousIn returns the canonical names of any capabilities in caps that
// materially weaken container isolation.
func dangerousIn(caps []string) []string {
	var bad []string
	for _, c := range caps {
		if lxcconf.IsDangerous(c) {
			if n, err := lxcconf.Normalize(c); err == nil {
				bad = append(bad, n)
			}
		}
	}
	return bad
}

// gateDangerous enforces the dangerous-capability policy for a grant: it errors
// unless force is set when any of caps is dangerous, and prints a warning when
// force lets a dangerous grant through anyway.
func gateDangerous(cmd *cobra.Command, caps []string, force bool) error {
	bad := dangerousIn(caps)
	if len(bad) == 0 {
		return nil
	}
	if !force {
		return fmt.Errorf(
			"refusing to grant dangerous capability(ies) %s without --force: these let a container "+
				"reach across its isolation boundary and can compromise the host; re-run with --force "+
				"if you understand the risk", strings.Join(bad, ", "))
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"WARNING: granting dangerous capability(ies): %s\n", strings.Join(bad, ", "))
	return nil
}

// newSecurityCapsShowCmd builds `pmx lxc security caps show <vmid|name>`.
func newSecurityCapsShowCmd() *cobra.Command {
	var effective bool
	var f sshcmd.Flags

	cmd := &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show configured lxc.cap.keep / lxc.cap.drop entries",
		Long: "Show a container's configured capability mode and lists. Without --effective this is " +
			"an API-only read of the raw lxc.* entries. With --effective it additionally probes the " +
			"running container's /proc/1/status over root ssh and decodes its bounding (CapBnd) and " +
			"effective (CapEff) capability masks, so you can confirm what the container actually has.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			cfg, err := deps.API.Nodes.ListLxcConfig(cmd.Context(), node, vmid, &nodes.ListLxcConfigParams{})
			if err != nil {
				return fmt.Errorf("get config for container %s: %w", vmid, err)
			}
			state, err := capsFromLxcArray(cfg.Lxc)
			if err != nil {
				return fmt.Errorf("parse capabilities for container %s: %w", vmid, err)
			}

			if !effective {
				single := map[string]string{"vmid": vmid, "node": node, "mode": state.Mode}
				if len(state.Keep) > 0 {
					single["keep"] = strings.Join(state.Keep, " ")
				}
				if len(state.Drop) > 0 {
					single["drop"] = strings.Join(state.Drop, " ")
				}
				res := output.Result{Single: single, Raw: newCapsView(state)}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			// --effective needs a running container and root ssh.
			st, err := deps.API.Nodes.ListLxcStatusCurrent(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get status for container %s: %w", vmid, err)
			}
			if st == nil || st.Status != "running" {
				return fmt.Errorf(
					"container %s is not running; --effective needs a running container to read /proc/1/status", vmid)
			}

			conn, err := nodeConn(cmd, deps, &f, node)
			if err != nil {
				return err
			}
			out, _, err := conn.Exec(fmt.Sprintf("pct exec %s -- cat /proc/1/status", vmid))
			if err != nil {
				return fmt.Errorf("probe effective capabilities of container %s: %w", vmid, err)
			}

			bndHex, effHex := parseProcStatusCaps(out)
			bndNames, err := lxcconf.DecodeMask(bndHex)
			if err != nil {
				return fmt.Errorf("decode CapBnd of container %s: %w", vmid, err)
			}
			effNames, err := lxcconf.DecodeMask(effHex)
			if err != nil {
				return fmt.Errorf("decode CapEff of container %s: %w", vmid, err)
			}

			single := map[string]string{
				"vmid":                vmid,
				"node":                node,
				"configured.mode":     state.Mode,
				"effective.bounding":  strings.Join(bndNames, " "),
				"effective.effective": strings.Join(effNames, " "),
			}
			if len(state.Keep) > 0 {
				single["configured.keep"] = strings.Join(state.Keep, " ")
			}
			if len(state.Drop) > 0 {
				single["configured.drop"] = strings.Join(state.Drop, " ")
			}
			res := output.Result{
				Single: single,
				Raw: map[string]any{
					"vmid":       vmid,
					"node":       node,
					"configured": newCapsView(state),
					"effective":  map[string][]string{"bounding": bndNames, "effective": effNames},
				},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&effective, "effective", false,
		"probe the running container's bounding/effective capabilities via /proc/1/status (needs root ssh)")
	sshcmd.RegisterFlags(cmd, &f)
	return cmd
}

// parseProcStatusCaps extracts the CapBnd and CapEff hex masks from the text of
// /proc/<pid>/status. Missing fields yield "0".
func parseProcStatusCaps(status string) (capBnd, capEff string) {
	capBnd, capEff = "0", "0"
	for _, ln := range strings.Split(status, "\n") {
		key, val, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "CapBnd":
			capBnd = val
		case "CapEff":
			capEff = val
		}
	}
	return capBnd, capEff
}

// newSecurityCapsDescribeCmd builds `pmx lxc security caps describe`, an offline
// catalog of the known Linux capabilities and the named presets.
func newSecurityCapsDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "List known Linux capabilities and capability presets",
		Long: "List every Linux capability the security commands recognise, whether it is dangerous, " +
			"and a one-line note on what it grants, followed by the named presets and their " +
			"contents. Runs entirely offline.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			catalog := lxcconf.Catalog()
			presets := lxcconf.Presets()

			headers := []string{"CAPABILITY", "DANGEROUS", "NOTES"}
			rows := make([][]string, 0, len(catalog))
			for _, c := range catalog {
				dangerous := ""
				if c.Dangerous {
					dangerous = "yes"
				}
				rows = append(rows, []string{c.Name, dangerous, c.Note})
			}

			presetView := make(map[string][]string, len(presets))
			for name, caps := range presets {
				presetView[name] = caps
			}

			res := output.Result{
				Headers: headers,
				Rows:    rows,
				Raw: map[string]any{
					"capabilities": catalog,
					"presets":      presetView,
				},
			}
			if err := deps.Out.Render(cmd.OutOrStdout(), res, deps.Format); err != nil {
				return err
			}

			// Presets do not fit the single catalog table; append them as prose
			// for the human-readable formats (structured output carries them in Raw).
			if deps.Format == output.FormatTable || deps.Format == output.FormatPlain {
				w := cmd.OutOrStdout()
				_, _ = fmt.Fprintln(w)
				_, _ = fmt.Fprintln(w, "Presets (keep-mode whitelists):")
				for _, name := range lxcconf.PresetNames() {
					caps, _ := lxcconf.Preset(name)
					_, _ = fmt.Fprintf(w, "  %s: %s\n", name, strings.Join(caps, " "))
				}
			}
			return nil
		},
	}
}

// newSecurityCapsSetCmd builds `pmx lxc security caps set <vmid|name>`.
func newSecurityCapsSetCmd() *cobra.Command {
	var keepList, dropList, preset string
	var force, restart bool
	var f sshcmd.Flags

	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Replace the container's capability entries (whitelist or drop list)",
		Long: "Replace the container's capability configuration. --keep writes an lxc.cap.keep " +
			"allowlist (and removes any drop line); --drop writes an lxc.cap.drop blocklist (and " +
			"removes any keep line); --preset writes a named keep-mode whitelist. Exactly one is " +
			"required. Granting a dangerous capability requires --force.\n\n" +
			"Example: pmx lxc security caps set web1 --keep chown,net_bind_service,setuid,setgid,kill",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			mode, caps, err := capsSetSelection(cmd, keepList, dropList, preset)
			if err != nil {
				return err
			}
			norm, err := normalizeCaps(caps)
			if err != nil {
				return err
			}
			// The dangerous gate guards grants only. --keep and --preset grant the
			// listed capabilities; --drop revokes them, which is always safe.
			if mode == lxcconf.ModeKeep {
				if err := gateDangerous(cmd, norm, force); err != nil {
					return err
				}
			}

			edit := func(content string) (string, string, bool, error) {
				out, err := lxcconf.SetCaps(content, mode, norm)
				if err != nil {
					return "", "", false, err
				}
				return out, fmt.Sprintf("capabilities set (%s: %d)", mode, len(norm)), out != content, nil
			}
			return runCapsMutation(cmd, deps, &f, args[0], restart, edit)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&keepList, "keep", "", "comma-separated allowlist: write lxc.cap.keep with exactly these caps")
	fl.StringVar(&dropList, "drop", "", "comma-separated blocklist: write lxc.cap.drop with these caps")
	fl.StringVar(&preset, "preset", "",
		fmt.Sprintf("write a named keep-mode preset (%s)", strings.Join(lxcconf.PresetNames(), ", ")))
	fl.BoolVar(&force, "force", false, "allow dangerous capability names")
	fl.BoolVar(&restart, "restart", false, "reboot the container after a successful write (if running)")
	cmd.MarkFlagsMutuallyExclusive("keep", "drop", "preset")
	cmd.MarkFlagsOneRequired("keep", "drop", "preset")
	sshcmd.RegisterFlags(cmd, &f)
	return cmd
}

// capsSetSelection resolves the mutually exclusive --keep/--drop/--preset flags
// into a mode and the requested capability list.
func capsSetSelection(cmd *cobra.Command, keepList, dropList, preset string) (mode string, caps []string, err error) {
	fl := cmd.Flags()
	switch {
	case fl.Changed("keep"):
		return lxcconf.ModeKeep, splitCaps(keepList), nil
	case fl.Changed("drop"):
		return lxcconf.ModeDrop, splitCaps(dropList), nil
	case fl.Changed("preset"):
		caps, ok := lxcconf.Preset(preset)
		if !ok {
			return "", nil, fmt.Errorf(
				"unknown preset %q (known presets: %s)", preset, strings.Join(lxcconf.PresetNames(), ", "))
		}
		return lxcconf.ModeKeep, caps, nil
	default:
		return "", nil, fmt.Errorf("one of --keep, --drop, or --preset is required")
	}
}

// newSecurityCapsAddCmd builds `pmx lxc security caps add <vmid|name> <cap>...`.
func newSecurityCapsAddCmd() *cobra.Command {
	var force, restart bool
	var f sshcmd.Flags

	cmd := &cobra.Command{
		Use:   "add <vmid|name> <capability>...",
		Short: "Grant capability(ies) to the container",
		// add grants an existing capability rather than creating anything;
		// a "create" alias would misdescribe the command.
		Annotations: map[string]string{cli.AnnotationNoVerbAlias: "true"},
		Long: "Grant one or more capabilities to the container. In keep mode the caps are appended " +
			"to lxc.cap.keep; in drop mode they are removed from lxc.cap.drop. Both mean the " +
			"container gains the capability. With no capability configuration yet, add errors and " +
			"points you at 'caps set --keep', since PVE's defaults already grant most capabilities. " +
			"Granting a dangerous capability requires --force.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			norm, err := normalizeCaps(args[1:])
			if err != nil {
				return err
			}
			if err := gateDangerous(cmd, norm, force); err != nil {
				return err
			}

			edit := func(content string) (string, string, bool, error) {
				state, err := lxcconf.GetCaps(content)
				if err != nil {
					return "", "", false, err
				}
				summary := "granted " + strings.Join(norm, ", ")
				switch state.Mode {
				case lxcconf.ModeDefault:
					return "", "", false, fmt.Errorf(
						"container has no capability whitelist configured; start one with " +
							"'pmx lxc security caps set --keep ...' (PVE defaults already grant most capabilities)")
				case lxcconf.ModeKeep:
					out, err := lxcconf.SetCaps(content, lxcconf.ModeKeep, appendCaps(state.Keep, norm))
					if err != nil {
						return "", "", false, err
					}
					return out, summary, out != content, nil
				case lxcconf.ModeDrop:
					newDrop := removeCaps(state.Drop, norm)
					if len(newDrop) == 0 {
						out, changed := lxcconf.ClearCaps(content)
						return out, summary + " (drop list now empty; restored PVE defaults)", changed, nil
					}
					out, err := lxcconf.SetCaps(content, lxcconf.ModeDrop, newDrop)
					if err != nil {
						return "", "", false, err
					}
					return out, summary, out != content, nil
				default:
					return "", "", false, fmt.Errorf("unexpected capability mode %q", state.Mode)
				}
			}
			return runCapsMutation(cmd, deps, &f, args[0], restart, edit)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "allow dangerous capability names")
	cmd.Flags().BoolVar(&restart, "restart", false, "reboot the container after a successful write (if running)")
	sshcmd.RegisterFlags(cmd, &f)
	return cmd
}

// newSecurityCapsRemoveCmd builds `pmx lxc security caps remove <vmid|name> <cap>...`.
func newSecurityCapsRemoveCmd() *cobra.Command {
	var restart bool
	var f sshcmd.Flags

	cmd := &cobra.Command{
		Use:   "remove <vmid|name> <capability>...",
		Short: "Revoke capability(ies) from the container",
		Long: "Revoke one or more capabilities from the container. In keep mode the caps are removed " +
			"from lxc.cap.keep; in drop mode they are added to lxc.cap.drop. With no capability " +
			"configuration yet, remove bootstraps an lxc.cap.drop entry — the natural way to take " +
			"a single capability away from PVE's defaults.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			norm, err := normalizeCaps(args[1:])
			if err != nil {
				return err
			}

			edit := func(content string) (string, string, bool, error) {
				state, err := lxcconf.GetCaps(content)
				if err != nil {
					return "", "", false, err
				}
				summary := "revoked " + strings.Join(norm, ", ")
				switch state.Mode {
				case lxcconf.ModeDefault:
					out, err := lxcconf.SetCaps(content, lxcconf.ModeDrop, norm)
					if err != nil {
						return "", "", false, err
					}
					return out, summary + " (added to a new drop list)", out != content, nil
				case lxcconf.ModeKeep:
					newKeep := removeCaps(state.Keep, norm)
					if len(newKeep) == 0 {
						return "", "", false, fmt.Errorf(
							"removing %s would leave an empty keep list; use "+
								"'pmx lxc security caps reset' to restore PVE defaults", strings.Join(norm, ", "))
					}
					out, err := lxcconf.SetCaps(content, lxcconf.ModeKeep, newKeep)
					if err != nil {
						return "", "", false, err
					}
					return out, summary, out != content, nil
				case lxcconf.ModeDrop:
					out, err := lxcconf.SetCaps(content, lxcconf.ModeDrop, appendCaps(state.Drop, norm))
					if err != nil {
						return "", "", false, err
					}
					return out, summary, out != content, nil
				default:
					return "", "", false, fmt.Errorf("unexpected capability mode %q", state.Mode)
				}
			}
			return runCapsMutation(cmd, deps, &f, args[0], restart, edit)
		},
	}

	cmd.Flags().BoolVar(&restart, "restart", false, "reboot the container after a successful write (if running)")
	sshcmd.RegisterFlags(cmd, &f)
	return cmd
}

// newSecurityCapsResetCmd builds `pmx lxc security caps reset <vmid|name>`.
func newSecurityCapsResetCmd() *cobra.Command {
	var restart bool
	var f sshcmd.Flags

	cmd := &cobra.Command{
		Use:   "reset <vmid|name>",
		Short: "Remove lxc.cap.keep and lxc.cap.drop entries (restore PVE defaults)",
		Long: "Remove every lxc.cap.keep and lxc.cap.drop entry from the container config, restoring " +
			"PVE's default capability set. There is no confirmation prompt because this restores the " +
			"safer default.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			edit := func(content string) (string, string, bool, error) {
				out, changed := lxcconf.ClearCaps(content)
				return out, "removed capability entries (restored PVE defaults)", changed, nil
			}
			return runCapsMutation(cmd, deps, &f, args[0], restart, edit)
		},
	}

	cmd.Flags().BoolVar(&restart, "restart", false, "reboot the container after a successful write (if running)")
	sshcmd.RegisterFlags(cmd, &f)
	return cmd
}
