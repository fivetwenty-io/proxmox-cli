package lxc

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// featureState is the parsed form of the container features= property string.
type featureState struct {
	Nesting    bool
	Keyctl     bool
	Fuse       bool
	Mknod      bool
	ForceRWSys bool
	Mount      string
}

// fields returns the feature values keyed by their wire name, in a map suitable
// for structured output and dotted-key text rendering.
func (fs featureState) fields() map[string]any {
	return map[string]any{
		"nesting":      fs.Nesting,
		"keyctl":       fs.Keyctl,
		"fuse":         fs.Fuse,
		"mknod":        fs.Mknod,
		"force_rw_sys": fs.ForceRWSys,
		"mount":        fs.Mount,
	}
}

// parseFeatures parses a features= property string (e.g.
// "nesting=1,keyctl=1,mount=nfs;cifs") into a featureState. Unknown sub-options
// are ignored; the mount value is carried verbatim.
func parseFeatures(s string) featureState {
	var fs featureState
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, _ := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "nesting":
			fs.Nesting = val == "1"
		case "keyctl":
			fs.Keyctl = val == "1"
		case "fuse":
			fs.Fuse = val == "1"
		case "mknod":
			fs.Mknod = val == "1"
		case "force_rw_sys":
			fs.ForceRWSys = val == "1"
		case "mount":
			fs.Mount = val
		}
	}
	return fs
}

// composeFeatures renders a featureState back into a features= property string,
// omitting every sub-option left at its default (false / empty) so the wire
// string carries only what is actually enabled.
func composeFeatures(fs featureState) string {
	var parts []string
	if fs.Nesting {
		parts = append(parts, "nesting=1")
	}
	if fs.Keyctl {
		parts = append(parts, "keyctl=1")
	}
	if fs.Fuse {
		parts = append(parts, "fuse=1")
	}
	if fs.Mknod {
		parts = append(parts, "mknod=1")
	}
	if fs.ForceRWSys {
		parts = append(parts, "force_rw_sys=1")
	}
	if fs.Mount != "" {
		parts = append(parts, "mount="+fs.Mount)
	}
	return strings.Join(parts, ",")
}

// compactFeatures renders the enabled feature keys as a compact comma list for
// the `security list` audit table (e.g. "nesting,keyctl"). Empty when nothing
// is enabled.
func compactFeatures(fs featureState) string {
	var on []string
	if fs.Nesting {
		on = append(on, "nesting")
	}
	if fs.Keyctl {
		on = append(on, "keyctl")
	}
	if fs.Fuse {
		on = append(on, "fuse")
	}
	if fs.Mknod {
		on = append(on, "mknod")
	}
	if fs.ForceRWSys {
		on = append(on, "force_rw_sys")
	}
	if fs.Mount != "" {
		on = append(on, "mount")
	}
	return strings.Join(on, ",")
}

// newSecurityFeaturesCmd builds `pmx lxc security features` and its sub-commands.
func newSecurityFeaturesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "features",
		Short: "Inspect and set container feature flags (nesting, keyctl, fuse, mknod, force_rw_sys, mount)",
		Long: "Inspect and set the features= config option of an LXC container with structured " +
			"per-feature flags, using the PVE config API (no ssh). This is NOT the 'pmx lxc feature' " +
			"command, which is an unrelated snapshot/clone/copy support probe.\n\n" +
			"The raw --features string on 'pmx lxc create' and 'pmx lxc config set' stays available " +
			"as an escape hatch.",
	}
	cmd.AddCommand(newSecurityFeaturesShowCmd(), newSecurityFeaturesSetCmd())
	return cmd
}

// newSecurityFeaturesShowCmd builds `pmx lxc security features show <vmid|name>`.
func newSecurityFeaturesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show container feature flags",
		Long: "Show every known feature sub-option with its effective value (unset keys shown at " +
			"their default). API-only read of the features= config option.",
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
			fs := parseFeatures(derefStr(cfg.Features))

			// Fixed key order so the table reads the same every time.
			order := []string{"nesting", "keyctl", "fuse", "mknod", "force_rw_sys", "mount"}
			values := fs.fields()
			rows := make([][]string, 0, len(order))
			for _, k := range order {
				rows = append(rows, []string{k, fmt.Sprintf("%v", values[k])})
			}

			res := output.Result{
				Headers: []string{"FEATURE", "VALUE"},
				Rows:    rows,
				Raw:     values,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newSecurityFeaturesSetCmd builds `pmx lxc security features set <vmid|name>`.
func newSecurityFeaturesSetCmd() *cobra.Command {
	var (
		nesting bool
		keyctl  bool
		fuse    bool
		mknod   bool
		forceRW bool
		mount   string
		reset   bool
		digest  string
	)

	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Set container feature flags",
		Long: "Set one or more feature sub-options, merging with the current features= value: only " +
			"the flags you pass are changed, the rest are left untouched. Pass --reset to clear " +
			"features= entirely.\n\n" +
			"Notes from the PVE API: keyctl (unprivileged only) is needed by some Docker workloads " +
			"but breaks systemd-networkd; mknod is experimental and needs a recent kernel; mount " +
			"loop and NFS filesystems widen the attack surface. Feature changes apply on the next " +
			"container start.\n\n" +
			"Example: pmx lxc security features set web1 --nesting --keyctl",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			cfg, err := deps.API.Nodes.ListLxcConfig(cmd.Context(), node, vmid, &nodes.ListLxcConfigParams{})
			if err != nil {
				return fmt.Errorf("get config for container %s: %w", vmid, err)
			}

			// Enabling features on a privileged container is a loud warning.
			if cfg.Unprivileged != nil && !cfg.Unprivileged.Bool() {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"WARNING: enabling features on a privileged container substantially increases host attack surface")
			}

			params := &nodes.UpdateLxcConfigParams{}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			var msg string
			if reset {
				del := "features"
				params.Delete = &del
				msg = fmt.Sprintf("Container %s features reset to defaults.", vmid)
			} else {
				fs := parseFeatures(derefStr(cfg.Features))
				changed := false
				if fl.Changed("nesting") {
					fs.Nesting = nesting
					changed = true
				}
				if fl.Changed("keyctl") {
					fs.Keyctl = keyctl
					changed = true
				}
				if fl.Changed("fuse") {
					fs.Fuse = fuse
					changed = true
				}
				if fl.Changed("mknod") {
					fs.Mknod = mknod
					changed = true
				}
				if fl.Changed("force-rw-sys") {
					fs.ForceRWSys = forceRW
					changed = true
				}
				if fl.Changed("mount") {
					fs.Mount = mount
					changed = true
				}
				if !changed {
					return fmt.Errorf("no feature flags given: specify at least one of " +
						"--nesting/--keyctl/--fuse/--mknod/--force-rw-sys/--mount, or --reset")
				}
				s := composeFeatures(fs)
				if s == "" {
					// Every feature is back at its default: delete the key rather
					// than sending features="", which PVE may reject or ignore.
					del := "features"
					params.Delete = &del
					msg = fmt.Sprintf("Container %s features reset to defaults.", vmid)
				} else {
					params.Features = &s
					msg = fmt.Sprintf("Container %s features updated.", vmid)
				}
			}

			if err := deps.API.Nodes.UpdateLxcConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update features for container %s: %w", vmid, err)
			}

			res := output.Result{
				Message: msg + fmt.Sprintf(" Changes apply on next start (restart with 'pmx lxc reboot %s').", vmid),
				Raw:     map[string]any{"vmid": vmid, "node": node},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&nesting, "nesting", false, "allow nested containers/virtualization (nesting=1)")
	fl.BoolVar(&keyctl, "keyctl", false, "allow the keyring syscalls (keyctl=1; unprivileged only)")
	fl.BoolVar(&fuse, "fuse", false, "allow FUSE mounts (fuse=1)")
	fl.BoolVar(&mknod, "mknod", false, "allow mknod for a small set of devices (mknod=1; experimental)")
	fl.BoolVar(&forceRW, "force-rw-sys", false, "mount /sys read-write (force_rw_sys=1)")
	fl.StringVar(&mount, "mount", "", "allowed mount filesystem types, e.g. 'nfs;cifs' (empty clears)")
	fl.BoolVar(&reset, "reset", false, "clear features= entirely (delete=features)")
	fl.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")

	// --reset is exclusive with every feature-setting flag, but the feature
	// flags themselves may be combined freely, so mark each pair individually.
	for _, name := range []string{"nesting", "keyctl", "fuse", "mknod", "force-rw-sys", "mount"} {
		cmd.MarkFlagsMutuallyExclusive("reset", name)
	}
	return cmd
}
