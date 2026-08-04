package lxc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/remote"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/lxcconf"
	"github.com/fivetwenty-io/proxmox-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/proxmox-cli/internal/nodefile"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// newSecurityCmd builds `pmx pve lxc security` and its show/list/caps/features
// sub-commands: the umbrella for reading and hardening a container's security
// posture (privilege level, feature flags, and the raw capability whitelist).
func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Inspect and harden container security posture (privilege, features, capabilities)",
		Long: "Read and tune the layered security posture of an LXC container: its privilege " +
			"level, the features= flags (nesting, keyctl, ...), and the low-level Linux " +
			"capability whitelist (lxc.cap.keep / lxc.cap.drop).\n\n" +
			"Reads (show, list, caps show, caps describe, features show) use only the PVE API. " +
			"Capability mutations have no API and edit /etc/pve/lxc/<vmid>.conf on the node over " +
			"root ssh; feature mutations use the config API.",
	}
	cmd.AddCommand(
		newSecurityShowCmd(),
		newSecurityListCmd(),
		newSecurityCapsCmd(),
		newSecurityFeaturesCmd(),
	)
	return cmd
}

// capsView is the JSON shape of the capability block shared by `security show`
// and `caps show`. Keep/Drop are always non-nil so structured output renders
// empty lists as [] rather than null.
type capsView struct {
	Mode string   `json:"mode"`
	Keep []string `json:"keep"`
	Drop []string `json:"drop"`
}

// newCapsView adapts an lxcconf.CapsState into the renderable/serialisable view,
// normalising nil slices to empty ones.
func newCapsView(s lxcconf.CapsState) capsView {
	v := capsView{Mode: s.Mode, Keep: s.Keep, Drop: s.Drop}
	if v.Keep == nil {
		v.Keep = []string{}
	}
	if v.Drop == nil {
		v.Drop = []string{}
	}
	return v
}

// securityPosture is the JSON shape emitted by `security show`.
type securityPosture struct {
	VMID         string         `json:"vmid"`
	Node         string         `json:"node"`
	Unprivileged bool           `json:"unprivileged"`
	Protection   bool           `json:"protection"`
	Features     map[string]any `json:"features"`
	Caps         capsView       `json:"caps"`
	Raw          [][]string     `json:"raw"`
}

// newSecurityShowCmd builds `pmx pve lxc security show <vmid|name>`.
func newSecurityShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show container security posture (privilege level, features, capabilities, raw lxc.* keys)",
		Long: "Show the full security posture of a container, gathered from a single config " +
			"read.\n\n" +
			"  * whether it runs unprivileged\n" +
			"  * the protection flag\n" +
			"  * the parsed features= flags\n" +
			"  * the capability whitelist (lxc.cap.keep / lxc.cap.drop)\n" +
			"  * any remaining raw lxc.* directives\n\n" +
			"Privilege level cannot be flipped in place. The API accepts unprivileged on " +
			"update but does not remap the rootfs UIDs, and PVE documents the key as one that " +
			"should not be modified manually. The supported path is a backup and restore with " +
			"the privilege choice made explicitly; see " +
			"`pmx pve lxc create --restore --force --unprivileged`.",
		Example: `  pmx pve lxc security show 200`,
		Args:    cobra.ExactArgs(1),
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
			if cfg == nil {
				return fmt.Errorf("get config for container %s: empty response", vmid)
			}

			unpriv := cfg.Unprivileged == nil || cfg.Unprivileged.Bool()
			prot := cfg.Protection != nil && cfg.Protection.Bool()
			fs := parseFeatures(derefStr(cfg.Features))
			state, err := capsFromLxcArray(cfg.Lxc)
			if err != nil {
				return fmt.Errorf("parse capabilities for container %s: %w", vmid, err)
			}
			rawEntries := rawLxcEntries(cfg.Lxc)

			// A privileged container is a loud, prose warning in text output only;
			// structured output carries "unprivileged": false and no prose.
			if !unpriv && (deps.Format == output.FormatTable || deps.Format == output.FormatPlain) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"WARNING: privileged container — the root user in the CT maps to root on the host")
			}

			single := map[string]string{
				"vmid":         vmid,
				"node":         node,
				"unprivileged": strconv.FormatBool(unpriv),
				"protection":   strconv.FormatBool(prot),
				"caps.mode":    state.Mode,
			}
			if len(state.Keep) > 0 {
				single["caps.keep"] = strings.Join(state.Keep, " ")
			}
			if len(state.Drop) > 0 {
				single["caps.drop"] = strings.Join(state.Drop, " ")
			}
			for k, v := range fs.fields() {
				single["features."+k] = fmt.Sprintf("%v", v)
			}
			for _, pair := range rawEntries {
				single["raw."+pair[0]] = pair[1]
			}

			res := output.Result{
				Single: single,
				Raw: securityPosture{
					VMID:         vmid,
					Node:         node,
					Unprivileged: unpriv,
					Protection:   prot,
					Features:     fs.fields(),
					Caps:         newCapsView(state),
					Raw:          rawEntries,
				},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// lxcResource is the minimal decoded shape of one cluster/resources entry used
// by `security list` to enumerate containers and the nodes they run on.
type lxcResource struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Node string `json:"node"`
	VMID *int64 `json:"vmid"`
	ID   string `json:"id"`
}

// vmidString returns the entry's numeric VMID as a string, deriving it from the
// id suffix (e.g. "lxc/101") when the vmid field is absent.
func (r lxcResource) vmidString() string {
	if r.VMID != nil {
		return strconv.FormatInt(*r.VMID, 10)
	}
	if i := strings.LastIndex(r.ID, "/"); i >= 0 {
		return r.ID[i+1:]
	}
	return ""
}

// securityRow is one row of the `security list` audit table, retained in a
// struct so privileged containers can be sorted ahead of unprivileged ones.
//
// Err is set instead of the posture fields when the per-container config read
// failed; the row still renders rather than aborting the whole audit, matching
// the VM twin. An audit that reports nothing because one container out of two
// hundred is unreadable is an audit nobody can act on.
//
// The fields are exported with json tags because the row is what -o json and
// -o yaml render: unexported fields marshal to an empty object, so the machine
// -readable views of this audit used to be a list of {}.
type securityRow struct {
	VMID         string `json:"vmid"`
	Name         string `json:"name"`
	Node         string `json:"node"`
	Unprivileged bool   `json:"unprivileged"`
	Features     string `json:"features"`
	Caps         string `json:"caps"`
	Protection   bool   `json:"protection"`
	Err          string `json:"error,omitempty"`
}

// newSecurityListCmd builds `pmx pve lxc security list`.
func newSecurityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Security posture summary for all containers",
		Long: "Audit the security posture of every container in the cluster (or on --node): its " +
			"privilege level, enabled features, capability mode, and protection flag. Privileged " +
			"containers are flagged with '!' and sorted first so the riskiest rows read top-down. " +
			"This is a cluster resources scan plus one config read per container.",
		Example: `  pmx pve lxc security list
  pmx pve lxc security list --node pve1`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			ctx := cmd.Context()

			typeVM := "vm"
			resp, err := deps.API.Cluster.ListResources(ctx, &pvecluster.ListResourcesParams{Type: &typeVM})
			if err != nil {
				return fmt.Errorf("list cluster resources: %w", err)
			}

			// Select the containers to audit first, then read their configs
			// concurrently: one config read per guest run back to back is what
			// makes this command crawl on a real fleet.
			var targets []lxcResource
			if resp != nil {
				for _, raw := range *resp {
					var r lxcResource
					if err := json.Unmarshal(raw, &r); err != nil {
						return fmt.Errorf("decode cluster resource entry: %w", err)
					}
					if r.Type != cli.GuestLXC {
						continue
					}
					// Only an explicit --node narrows the audit; an ambient
					// default node must not silently shrink a cluster-wide scan.
					if deps.NodeExplicit && r.Node != deps.Node {
						continue
					}
					targets = append(targets, r)
				}
			}

			rows := make([]securityRow, len(targets))
			warnings := make([]string, len(targets))

			cli.ForEachIndex(ctx, len(targets), cli.DefaultFanout, func(ctx context.Context, i int) {
				r := targets[i]
				vmid := r.vmidString()

				fail := func(err error) {
					warnings[i] = fmt.Sprintf(
						"warning: skipping container %s security posture (%s): %v\n", vmid, r.Node, err)
					rows[i] = securityRow{
						VMID: vmid, Name: r.Name, Node: r.Node,
						Features: "?", Caps: "?", Err: err.Error(),
					}
				}

				cfg, err := deps.API.Nodes.ListLxcConfig(ctx, r.Node, vmid, &nodes.ListLxcConfigParams{})
				if err != nil {
					fail(fmt.Errorf("get config: %w", err))
					return
				}
				state, err := capsFromLxcArray(cfg.Lxc)
				if err != nil {
					fail(fmt.Errorf("parse capabilities: %w", err))
					return
				}

				rows[i] = securityRow{
					VMID:         vmid,
					Name:         r.Name,
					Node:         r.Node,
					Unprivileged: cfg.Unprivileged == nil || cfg.Unprivileged.Bool(),
					Features:     compactFeatures(parseFeatures(derefStr(cfg.Features))),
					Caps:         capsSummary(state),
					Protection:   cfg.Protection != nil && cfg.Protection.Bool(),
				}
			})

			// Emitted after the fan-out, in target order, so concurrent reads
			// cannot interleave a warning mid-line or reorder them run to run.
			for _, w := range warnings {
				if w != "" {
					_, _ = fmt.Fprint(cmd.ErrOrStderr(), w)
				}
			}

			// Privileged first (an unreadable row counts as risky too, since it
			// hides whatever posture it would have reported), then numeric VMID
			// order within each group.
			sort.SliceStable(rows, func(i, j int) bool {
				iRisky := !rows[i].Unprivileged || rows[i].Err != ""
				jRisky := !rows[j].Unprivileged || rows[j].Err != ""
				if iRisky != jRisky {
					return iRisky
				}
				return rows[i].VMID < rows[j].VMID
			})

			res := output.Result{
				Headers: []string{"VMID", "NAME", "NODE", "UNPRIVILEGED", "FEATURES", "CAPS", "PROTECTION"},
				Raw:     rows,
			}
			for _, r := range rows {
				vmidCell := r.VMID
				if !r.Unprivileged || r.Err != "" {
					vmidCell = "! " + r.VMID
				}
				capsCell := r.Caps
				if r.Err != "" {
					capsCell = "error: " + r.Err
				}
				res.Rows = append(res.Rows, []string{
					vmidCell, r.Name, r.Node,
					strconv.FormatBool(r.Unprivileged),
					r.Features, capsCell,
					strconv.FormatBool(r.Protection),
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// capsSummary renders a capability state as "default", "keep(n)", or "drop(n)"
// for the audit table.
func capsSummary(s lxcconf.CapsState) string {
	switch s.Mode {
	case lxcconf.ModeKeep:
		return fmt.Sprintf("keep(%d)", len(s.Keep))
	case lxcconf.ModeDrop:
		return fmt.Sprintf("drop(%d)", len(s.Drop))
	default:
		return lxcconf.ModeDefault
	}
}

// capsFromLxcArray builds a capability state from the read-only Lxc [][]string
// array of a ListLxcConfig response by re-emitting the pairs as config text and
// running them through the same parser the on-disk editor uses, so accumulation
// and keep/drop exclusivity are handled identically.
func capsFromLxcArray(lxc [][]string) (lxcconf.CapsState, error) {
	var b strings.Builder
	for _, pair := range lxc {
		if len(pair) != 2 {
			continue
		}
		b.WriteString(pair[0])
		b.WriteString(": ")
		b.WriteString(pair[1])
		b.WriteByte('\n')
	}
	return lxcconf.GetCaps(b.String())
}

// rawLxcEntries returns every lxc.* pair that is not a capability line, for the
// "raw" block of `security show`.
func rawLxcEntries(lxc [][]string) [][]string {
	out := make([][]string, 0)
	for _, pair := range lxc {
		if len(pair) != 2 {
			continue
		}
		if pair[0] == lxcconf.KeyCapKeep || pair[0] == lxcconf.KeyCapDrop {
			continue
		}
		out = append(out, pair)
	}
	return out
}

// lxcConfPath is the guest config path edited by the caps mutation verbs.
func lxcConfPath(vmid string) string { return "/etc/pve/lxc/" + vmid + ".conf" }

// lxcLockPath is pct's own per-container config lock, reused so an edit
// serialises against concurrent pct/PVE writers.
func lxcLockPath(vmid string) string { return "/run/lock/lxc/pve-config-" + vmid + ".lock" }

// nodeConn builds a nodefile.Conn for editing /etc/pve on node over ssh. It
// applies the active context's ssh defaults, then refuses early unless the
// effective login user is root, since /etc/pve is only writable by root. The
// node's management address is resolved from cluster status.
func nodeConn(cmd *cobra.Command, deps *cli.Deps, f *sshcmd.Flags, node string) (*nodefile.Conn, error) {
	remote.ApplyContextSSHDefaults(cmd, deps, f, "user", "port", "identity")
	if f.User != "root" {
		return nil, fmt.Errorf(
			"editing /etc/pve requires a root ssh login, but the ssh user is %q; "+
				"/etc/pve is only writable by root (re-run with -l root)", f.User)
	}
	host, err := nodeaddr.Resolve(cmd.Context(), deps.API.Cluster, node, deps.Log)
	if err != nil {
		return nil, fmt.Errorf("resolve address for node %q: %w", node, err)
	}
	return &nodefile.Conn{Runner: deps.Runner, Flags: f, Host: host}, nil
}

// capsEdit transforms the current config text into new text. summary is a short
// human fragment (e.g. "capabilities set (keep: 5)"); changed is false when the
// edit is a no-op, which runCapsMutation reports without writing.
type capsEdit func(content string) (newContent, summary string, changed bool, err error)

// runCapsMutation performs the shared write flow for every caps mutation verb:
// resolve the guest, refuse if PVE holds a config lock, read the guest config
// over ssh, apply edit, write it back under the pct lock with an optimistic
// sha guard, then validate the result with `pct config`. A validation failure
// triggers an automatic sha-guarded rollback to the original bytes. On success
// it appends the restart guidance (or performs the reboot when --restart is
// given).
func runCapsMutation(
	cmd *cobra.Command, deps *cli.Deps, f *sshcmd.Flags, target string, restart bool, edit capsEdit,
) error {
	ctx := cmd.Context()
	vmid, node, err := resolveGuest(ctx, deps, target)
	if err != nil {
		return err
	}

	cfg, err := deps.API.Nodes.ListLxcConfig(ctx, node, vmid, &nodes.ListLxcConfigParams{})
	if err != nil {
		return fmt.Errorf("get config for container %s: %w", vmid, err)
	}
	if cfg != nil && cfg.Lock != nil && *cfg.Lock != "" {
		return fmt.Errorf(
			"container %s is locked (lock: %s); clear the PVE lock before editing capabilities", vmid, *cfg.Lock)
	}

	conn, err := nodeConn(cmd, deps, f, node)
	if err != nil {
		return err
	}

	path := lxcConfPath(vmid)
	lock := lxcLockPath(vmid)

	content, sha, err := conn.Read(path)
	if err != nil {
		return fmt.Errorf("read %s on node %s: %w", path, node, err)
	}

	newContent, summary, changed, err := edit(content)
	if err != nil {
		return err
	}
	if !changed {
		res := output.Result{Message: fmt.Sprintf("Container %s: %s; no change.", vmid, summary)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	if err := conn.Write(path, newContent, sha, lock); err != nil {
		return fmt.Errorf("write %s on node %s: %w", path, node, err)
	}

	// Validate the rewritten config; roll back to the original bytes on failure.
	if _, stderr, verr := conn.Exec(fmt.Sprintf("pct config %s >/dev/null", vmid)); verr != nil {
		// Exit 255 is ssh's own transport failure, not a pct parse verdict: the
		// write already succeeded, so leave it in place rather than reverting.
		if exec.ExitCodeOf(verr) == 255 {
			return fmt.Errorf(
				"container %s: the write succeeded but the validation step could not reach node %s (%s); "+
					"verify with 'pct config %s' on the node",
				vmid, node, strings.TrimSpace(stderr), vmid)
		}
		newSHA := sha256Hex(newContent)
		if rbErr := conn.Write(path, content, newSHA, lock); rbErr != nil {
			return fmt.Errorf(
				"container %s config failed validation after the write (%s) and the automatic rollback also "+
					"failed (%v); %s on node %s may be left in a bad state and needs manual repair",
				vmid, strings.TrimSpace(stderr), rbErr, path, node)
		}
		return fmt.Errorf(
			"container %s config failed validation (%s); rolled back to the previous contents",
			vmid, strings.TrimSpace(stderr))
	}

	suffix, err := restartSuffix(cmd, deps, vmid, node, restart)
	if err != nil {
		return err
	}

	res := output.Result{
		Message: fmt.Sprintf("Container %s: %s.%s", vmid, summary, suffix),
		Raw:     map[string]any{"vmid": vmid, "node": node, "summary": summary},
	}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// restartSuffix produces the trailing clause of a mutation's success message.
// Without --restart it points the operator at the manual restart; with
// --restart it reboots the container when it is running (honouring --async) and
// says so, or reports that no restart was needed when it is stopped.
func restartSuffix(cmd *cobra.Command, deps *cli.Deps, vmid, node string, restart bool) (string, error) {
	if !restart {
		return fmt.Sprintf(
			" Changes apply on next start (restart with 'pmx pve lxc reboot %s' or pass --restart)", vmid), nil
	}

	st, err := deps.API.Nodes.ListLxcStatusCurrent(cmd.Context(), node, vmid)
	if err != nil {
		return "", fmt.Errorf("get status for container %s: %w", vmid, err)
	}
	if st == nil || st.Status != "running" {
		return " Container is not running; no restart needed.", nil
	}

	resp, err := deps.API.Nodes.CreateLxcStatusReboot(cmd.Context(), node, vmid, &nodes.CreateLxcStatusRebootParams{})
	if err != nil {
		return "", fmt.Errorf("reboot container %s: %w", vmid, err)
	}
	upid, err := apiclient.UPIDFromRaw(*resp)
	if err != nil {
		return "", err
	}
	if deps.Async {
		return fmt.Sprintf(" Reboot task started (%s).", upid), nil
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return "", err
	}
	return " Container rebooted.", nil
}

// sha256Hex returns the hex sha256 of s, matching the digest nodefile.Read
// computes, so a rollback write can present the guard token for the bytes it
// just wrote.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
