package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newHostnetCmd builds `pmx lab hostnet` and its subcommands.
func newHostnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hostnet",
		Short: "Manage a lab's nested-node host network bonds and bridges",
		Long: "Reconcile the guest-OS bonds and bridges (network.nested_network.bonds) inside " +
			"each of a lab's nested PVE nodes against its config. Distinct from `pmx lab net` " +
			"(the outer host's own SDN vnet) and `pmx lab sdn`/`pmx lab sdn vlan` (the nested " +
			"cluster's own SDN zones): this manages the nested node's plain Linux bond/bridge " +
			"interfaces those SDN zones are layered on top of, via the nested cluster's own " +
			"`/nodes/{node}/network` API (internal/cli/node/network.go's bindings), never over ssh.",
	}
	cmd.AddCommand(newHostnetApplyCmd())
	return cmd
}

// newHostnetApplyCmd builds `pmx lab hostnet apply <name>`.
func newHostnetApplyCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "apply <name>",
		Short: "Reconcile a lab's nested-node bonds and bridges against its config",
		Long: "For each of the lab's nested node indices (0..topology.nodes-1), list the node's " +
			"live/staged host network interfaces, diff every configured " +
			"network.nested_network.bonds[] entry's bond and bridge against them, and issue " +
			"CreateNetwork/UpdateNetwork2 calls for anything missing or drifted, then " +
			"UpdateNetwork (the staged-changes reload) once per node when anything changed for " +
			"that node. Idempotent and safe to rerun — this is the only path for a node whose " +
			"bonds were never rendered at OS-install time (e.g. an already-installed node picking " +
			"up a newly-added network.nested_network config without a reinstall). Requires the " +
			"lab's own lab-<name> context (registered by `pmx lab context sync`) to be the " +
			"currently active context (--context/-c): this command talks to the nested cluster's " +
			"own API, never the outer host's, and refuses to run against any other context so it " +
			"can never mistakenly stage a bond/bridge on the wrong cluster. A lab with no " +
			"network.nested_network.bonds configured is a no-op with a notice.",
		Example: `  pmx -c lab-pve-cpi lab hostnet apply pve-cpi
  pmx -c lab-pve-cpi lab hostnet apply pve-cpi --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHostnetApply(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"preview the bond/bridge reconciliation without issuing any API call")
	return cmd
}

// hostnetRequireLabContext refuses the run unless deps.CtxName is exactly
// the lab's own derived context name (labContextName(name)). hostnet apply's
// bond/bridge phase talks to the nested cluster's own `/nodes/{node}/network`
// API through deps.API — which is bound to whichever context was resolved at
// PersistentPreRunE time, not to the outer host `pmx lab create` provisions
// against — so running it under any other active context would silently
// target the wrong cluster (or the outer host itself) instead of failing
// loud. Its NIC-naming ensure phase (hostnetEnsureNICNaming) is
// ssh-transported over each node's own mgmt IP, exactly like this package's
// other ssh-transported verbs (cluster.go, sdninner.go) — but it still
// derives its ssh connection defaults (user/port/identity) from deps.Ctx,
// which PersistentPreRunE resolves from this same active context, so the
// same guard keeps both phases pointed at the correct lab.
func hostnetRequireLabContext(deps *cli.Deps, name string) error {
	want := labContextName(name)
	if deps.CtxName == want {
		return nil
	}
	return fmt.Errorf(
		"lab %q: hostnet apply must run against its own nested-cluster context %q, "+
			"not the currently active context %q (this command talks to the nested cluster's "+
			"own API, never the outer host's); run 'pmx -c %s lab hostnet apply %s', "+
			"registering the context first with 'pmx lab context sync %s' if it does not exist yet",
		name, want, deps.CtxName, want, name, name)
}

func runHostnetApply(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	if err := hostnetRequireLabContext(deps, name); err != nil {
		return err
	}

	nn := lab.Network.NestedNetwork
	if len(nn.Bonds) == 0 {
		res := output.Result{Message: fmt.Sprintf(
			"lab %q has no network.nested_network.bonds configured; nothing to do.", name)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	numNodes := config.EffectiveTopologyNodes(lab.Topology)
	headers := []string{"NODE", "STEP", "STATUS"}

	// dry-run never touches deps.API or deps.Runner (mirrors runSdnApply/
	// runClusterInit's identical convention for their own ssh-transported
	// previews): it lists every step this run would take, without probing
	// live remote state (nor enumerating live NICs over ssh) to decide
	// create-vs-update.
	if dryRun {
		var rows [][]string
		for idx := 0; idx < numNodes; idx++ {
			nodeName := hostnetNodeName(lab.Name, idx)
			rows = append(rows, []string{nodeName,
				fmt.Sprintf("ensure NIC naming (nic0-nic%d)", hostnetRequiredNICCount-1), "would run"})
			for _, b := range nn.Bonds {
				rows = append(rows, []string{nodeName,
					fmt.Sprintf("ensure bond %q (mode %s, nics %s)", b.Name, b.Mode, strings.Join(b.NICs, ",")),
					"would run"})
				rows = append(rows, []string{nodeName,
					fmt.Sprintf("ensure bridge %q (port %s, vlan_aware=%v)", b.Bridge, b.Name, b.VlanAware),
					"would run"})
			}
			rows = append(rows, []string{nodeName, "apply staged network changes", "would run (if anything changed)"})
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
	}

	ctx := cmd.Context()
	var allRows [][]string
	anyRebootPending := false
	for idx := 0; idx < numNodes; idx++ {
		nodeName := hostnetNodeName(lab.Name, idx)

		nodeIP, err := labNodeMgmtIP(lab.Network, idx)
		if err != nil {
			return fmt.Errorf("lab %q: resolve node %d mgmt IP: %w", name, idx, err)
		}

		// NIC-naming ensure phase (hostnetEnsureNICNaming): must run BEFORE
		// any bond/bridge work — a bond referencing nic0-nic5 cannot be
		// created against a node whose physical NICs are still named
		// ens18-ens23. A node this phase leaves reboot-pending is skipped
		// for the rest of THIS run (its bond/bridge phase never runs), but
		// every other node in the lab is still processed — see this
		// function's final anyRebootPending check for why the overall run
		// still exits nonzero.
		namingRow, outcome, err := hostnetEnsureNICNaming(deps, name, nodeName, idx, nodeIP)
		if err != nil {
			return err
		}
		allRows = append(allRows, namingRow)

		if outcome == hostnetNICNamingRebootPending {
			anyRebootPending = true
			continue
		}

		rows, err := hostnetEnsureNode(ctx, deps.API, name, nodeName, nn)
		if err != nil {
			return err
		}
		allRows = append(allRows, rows...)
	}

	if err := deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: allRows}, deps.Format); err != nil {
		return err
	}

	if anyRebootPending {
		return fmt.Errorf(
			"lab %q: hostnet apply left one or more nodes with pending NIC renames (see the table above) — "+
				"reboot each reboot-pending node, one at a time (never in parallel — cluster runbook §10.2 "+
				"quorum-safe serial reboot procedure), then re-run 'pmx lab hostnet apply %s' to finish the "+
				"bond/bridge reconciliation on those nodes",
			name, name)
	}
	return nil
}

// hostnetNodeName returns the nested cluster's own PVE node name for node
// index i (0-based) of a lab named name: "lab-<name>-<i>", the same
// convention labNodeVMName derives for the outer VM name (resolve.go). This
// is a documented assumption, not a live-fetched fact for every index: only
// node 0's hostname is ever actually read back from the nested node itself
// (labFetchHostname, labcontext.go, during `pmx lab context sync`) — the FQDN
// pattern that hostname is derived from (`lab-{member}-{node_index}.{member}
// .lab.fivetwenty.io`, per the multi-AZ topology plan §8.1) applies the exact
// same "lab-<name>-<i>" shape to every node index, so this is the correct
// convention to derive node 1..N-1's own PVE node name from, absent a
// per-node hostname-fetch API this package does not otherwise have reason to
// add. An operator whose answer.toml template diverges from this convention
// on a hand-edited lab must rename the node in PVE to match, or this command
// will report a "node not found" API error rather than silently querying the
// wrong node.
func hostnetNodeName(name string, i int) string {
	return labNodeVMName(name, i)
}

// --- NIC naming ensure phase -----------------------------------------------
//
// A fresh PVE install brings its virtio NICs up under systemd predictable
// names (ens18-ens23 for the q35 net0-net5 slots `pmx lab create` gives every
// lab node), not the lab convention nic0-nic5 that
// network.nested_network.bonds[].nics references. scripts/first-boot-
// network.sh.tmpl pins that convention at install time for a freshly
// provisioned node; this section is the day-2 counterpart for a node whose
// NICs were never renamed at install time (e.g. an already-installed,
// already-clustered node that just had new NICs added live and now needs
// nested_network config applied for the first time). hostnetEnsureNICNaming
// runs this phase, per node, before hostnetEnsureNode's API-driven bond/
// bridge phase.

// hostnetRequiredNICCount is the fixed physical-NIC count every lab node
// must report (net0-net5, `pmx lab create`'s q35 6-NIC layout) before the
// bond/bridge phase can run against it.
const hostnetRequiredNICCount = 6

// hostnetLinkFileDir and hostnetLinkFilePrefix locate the systemd .link
// files this phase writes to pin the nic0-nic5 naming convention, matching
// scripts/first-boot-network.sh.tmpl's own
// "/etc/systemd/network/10-lab-nicN.link" path exactly — a node renamed by
// the first-boot hook and a node renamed by this day-2 phase must produce
// byte-identical .link files.
const (
	hostnetLinkFileDir    = "/etc/systemd/network"
	hostnetLinkFilePrefix = "10-lab-nic"
)

// hostnetNICEnumerateCmd is the composite POSIX sh command run over ssh
// against a node's mgmt IP to enumerate its physical NICs: every
// /sys/class/net/* entry carrying a device symlink (excludes lo, bonds,
// bridges, vlans — mirrors scripts/first-boot-network.sh.tmpl's own `[ -e
// "$dev/device" ] || continue` filter), emitted as one tab-separated
// NAME\tMAC\tDEVPATH line. NAME is whatever the kernel currently calls the
// interface (not trusted for ordering — the kernel's ensNN/enoN naming is
// not guaranteed to match net0..net5 VM-slot order); DEVPATH is the fully
// resolved sysfs device path (`readlink -f .../device`), which
// hostnetSortNICs derives its net0..net5 ordering key from, exactly as the
// template does. A NIC whose MAC or resolved device path cannot be read
// (`cat`/`readlink -f` failing, e.g. a race with a device disappearing
// mid-enumeration) is skipped via `|| continue` rather than aborting the
// whole enumeration.
const hostnetNICEnumerateCmd = `for dev in /sys/class/net/*; do ` +
	`[ -e "$dev/device" ] || continue; ` +
	`name=$(basename "$dev"); ` +
	`mac=$(cat "$dev/address" 2>/dev/null) || continue; ` +
	`devpath=$(readlink -f "$dev/device") || continue; ` +
	`printf '%s\t%s\t%s\n' "$name" "$mac" "$devpath"; ` +
	`done`

// hostnetPhysicalNIC is one parsed line of hostnetNICEnumerateCmd's output.
type hostnetPhysicalNIC struct {
	// Name is the kernel-reported interface name at enumeration time (e.g.
	// "ens18", or already "nic0").
	Name string
	// MAC is the interface's hardware address, lowercase colon-separated
	// (Linux's own /sys/class/net/<if>/address format).
	MAC string
	// DevPath is the fully resolved sysfs device path
	// (`readlink -f /sys/class/net/<if>/device`), the source of the
	// net0..net5 ordering key (hostnetPCISortKey).
	DevPath string
}

// hostnetPCIFunctionRE matches a PCI bus:device.function address segment
// (e.g. "0000:06:12.0") anywhere in a resolved sysfs device path. Mirrors
// scripts/first-boot-network.sh.tmpl's own
// `grep -o '[0-9a-f]\{4\}:[0-9a-f]\{2\}:[0-9a-f]\{2\}\.[0-9a-f]' | tail -n1`
// pattern exactly (lowercase hex only — sysfs paths are always lowercase).
var hostnetPCIFunctionRE = regexp.MustCompile(`[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-9a-f]`)

// hostnetMACRE validates a MAC address read back from a node before it is
// ever embedded into a systemd .link file this package writes back to that
// node over ssh. The value originates from remote command output (external,
// untrusted input): a MAC address is the only field this phase interpolates
// into file content it writes, so it is validated strictly (6 lowercase hex
// octet pairs, colon-separated) before use.
var hostnetMACRE = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// hostnetIfaceNameRE validates a kernel-reported interface name (a physical
// NIC's OLD, pre-rename name) before it is embedded into the
// /etc/network/interfaces(.d/*) stale-reference rewrite script
// (hostnetBuildInterfacesRewriteScript) as a grep -E/sed -E pattern
// operand. Like hostnetMACRE, the value originates from remote command
// output (external, untrusted input). Real Linux interface names are
// letters, digits, dots, and hyphens only (systemd predictable names:
// ensNN, enoN, enpXsY[.Z]; legacy: ethN) — this charset also excludes every
// shell metacharacter (quotes, `;`, `$`, whitespace, `/`), so a name
// failing this check can never break out of the double-quoted shell string
// it is interpolated into.
var hostnetIfaceNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// hostnetParseNICEnumeration parses hostnetNICEnumerateCmd's tab-separated
// output into one hostnetPhysicalNIC per non-blank line. A malformed
// non-blank line (not exactly 3 tab-separated fields) is a hard error: the
// enumeration command's shape is fixed by this package, so a malformed line
// means the remote shell did not run it as expected (unexpected login
// shell/output injected from a profile script, a truncated transport read,
// etc.), not a legitimate "no NICs" signal that should be papered over.
func hostnetParseNICEnumeration(output string) ([]hostnetPhysicalNIC, error) {
	var nics []hostnetPhysicalNIC
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf(
				"malformed NIC enumeration line %q: want 3 tab-separated fields (name, mac, devpath), got %d",
				line, len(fields))
		}
		nics = append(nics, hostnetPhysicalNIC{Name: fields[0], MAC: fields[1], DevPath: fields[2]})
	}
	return nics, nil
}

// hostnetPCISortKey extracts the LAST PCI bus:device.function address found
// in devPath — scripts/first-boot-network.sh.tmpl's own sort key. Virtio
// NICs resolve to ".../<pci-fn>/virtioN", where the virtioN ordinal itself
// is not lexically sortable past 9 (lexically "virtio10" < "virtio2"); the
// PCI function segment that precedes virtioN in the path IS reliably
// sortable (fixed-width hex digits), which is why the template — and this
// mirror of it — key off that instead. Returns false when devPath contains
// no PCI-function-shaped segment at all.
func hostnetPCISortKey(devPath string) (string, bool) {
	matches := hostnetPCIFunctionRE.FindAllString(devPath, -1)
	if len(matches) == 0 {
		return "", false
	}
	return matches[len(matches)-1], true
}

// hostnetSortNICs filters nics to only those whose device path resolves a
// PCI-function sort key (hostnetPCISortKey) — mirroring
// scripts/first-boot-network.sh.tmpl's own `[ -n "$pci" ] || continue` skip,
// so an entry with no resolvable PCI function is excluded from both the
// ordering AND the count hostnetEnsureNICNaming's preflight checks — then
// returns the survivors sorted by that key, ascending. This is identical to
// the template's plain `sort` over "<pci> <mac> <name>" lines: fixed-width
// lowercase hex bus/slot/function digits compare correctly as plain ASCII.
func hostnetSortNICs(nics []hostnetPhysicalNIC) []hostnetPhysicalNIC {
	type keyed struct {
		nic hostnetPhysicalNIC
		key string
	}
	kept := make([]keyed, 0, len(nics))
	for _, n := range nics {
		key, ok := hostnetPCISortKey(n.DevPath)
		if !ok {
			continue
		}
		kept = append(kept, keyed{nic: n, key: key})
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].key < kept[j].key })
	out := make([]hostnetPhysicalNIC, len(kept))
	for i, k := range kept {
		out[i] = k.nic
	}
	return out
}

// hostnetNICName returns the lab convention name for physical NIC index i
// (0-based): "nic0".."nic5".
func hostnetNICName(i int) string { return fmt.Sprintf("nic%d", i) }

// hostnetLinkFilePath returns the systemd .link file path this phase writes
// for physical NIC index i.
func hostnetLinkFilePath(i int) string {
	return fmt.Sprintf("%s/%s%d.link", hostnetLinkFileDir, hostnetLinkFilePrefix, i)
}

// hostnetLinkFileContent renders the .link file content pinning physical NIC
// index i to mac, byte-identical in shape to
// scripts/first-boot-network.sh.tmpl's own heredoc (a comment naming the
// pinned convention, [Match] MACAddress, [Link] Name). Always ends with a
// trailing newline, since the heredoc terminator line
// (hostnetBuildNICRenameCmd) must start on its own line.
func hostnetLinkFileContent(i int, mac string) string {
	return fmt.Sprintf(
		"# Written by `pmx lab hostnet apply`: pin lab NIC naming convention.\n"+
			"[Match]\n"+
			"MACAddress=%s\n"+
			"\n"+
			"[Link]\n"+
			"Name=%s\n",
		mac, hostnetNICName(i))
}

// hostnetRenamePair is one OLD-kernel-name -> NEW-lab-convention-name
// mapping the composite rename command must apply BOTH to the systemd
// .link file it writes for New (hostnetLinkFileContent) AND to any stale
// reference to Old still present in /etc/network/interfaces or
// /etc/network/interfaces.d/* (hostnetBuildInterfacesRewriteScript). A
// day-2 node whose bonds/bridges were already configured against Old
// before its physical NICs are renamed — the exact live shape this closes
// (a lab node with a working bridge referencing its pre-rename kernel
// name, about to have 5 more NICs added and renamed) — would otherwise
// boot post-rename with every such reference pointing at a device that no
// longer exists, taking its mgmt IP down with it.
type hostnetRenamePair struct {
	Old string
	New string
}

// hostnetInterfacesFile and hostnetInterfacesDDir are the Debian ifupdown
// network config paths (PVE's own network stack) hostnetBuildInterfacesRewriteScript
// scans for stale interface-name references — the same paths
// scripts/first-boot-network.sh.tmpl rewrites wholesale at install time.
// This phase instead surgically rewrites only the specific stale name
// tokens a day-2 node's ALREADY-CONFIGURED interfaces file may reference,
// leaving everything else in the file untouched.
const (
	hostnetInterfacesFile = "/etc/network/interfaces"
	hostnetInterfacesDDir = "/etc/network/interfaces.d"
)

// hostnetInterfacesDBackupDir is where hostnetBuildInterfacesRewriteScript
// backs up a MODIFIED file found under hostnetInterfacesDDir — a sibling of
// hostnetInterfacesDDir, deliberately OUTSIDE it (unlike
// hostnetInterfacesFile's own backup, which stays a plain sibling of
// itself — "/etc/network/interfaces.<infix>.<ts>" — since that path was
// never at risk of the hazard described below).
//
// hostnetInterfacesFile routinely `source`s every file under
// hostnetInterfacesDDir (`source /etc/network/interfaces.d/*` — present in
// the live az1 fixture this phase was built against): a backup left INSIDE
// hostnetInterfacesDDir would therefore itself get glob-matched and
// sourced by ifupdown/ifreload as if it were a live config fragment.
// Worse, confirmed against a real Debian container during this fix's
// development: on a SECOND run before the node has rebooted (the
// "intermediate" reboot-still-pending state this phase is explicitly
// designed to reconverge safely — see hostnetBuildInterfacesRewriteScript's
// own idempotency doc comment), that backup — still containing the
// pre-rewrite stale name — would itself get picked up by this package's
// own `for f in $IFACES_D_DIR/*` loop and get "rewritten" (and re-backed-up
// again) on every subsequent run, defeating the very idempotency this
// phase depends on for safe re-entry. Routing every interfaces.d backup
// through this separate, non-glob-matched directory instead closes both
// problems at once.
const hostnetInterfacesDBackupDir = hostnetInterfacesDDir + ".pmx-nic-rename-backups"

// hostnetInterfacesBackupInfix names the timestamped backup
// hostnetBuildInterfacesRewriteScript's remote script takes of any file it
// is about to modify, mirroring scripts/first-boot-network.sh.tmpl's own
// "<file>.pre-multinic-bond.<UTC-timestamp>" backup-naming convention
// (a distinct infix, "pre-nic-rename", since this phase's rewrite is a
// narrower, single-purpose edit — a handful of stale tokens — not the
// template's full-file rewrite).
const hostnetInterfacesBackupInfix = "pre-nic-rename"

// hostnetRewrittenPrefix is the line prefix the remote rewrite script
// (hostnetBuildInterfacesRewriteScript) prints to stdout for every file it
// actually modified, letting hostnetEnsureNICNaming report exactly which
// files were rewritten in its returned STEP/STATUS row (hostnetParseRewrittenFiles)
// without a second round trip.
const hostnetRewrittenPrefix = "REWRITTEN:"

// hostnetEscapeEREChars escapes every POSIX extended regular expression
// (ERE) metacharacter in s, so it can be embedded as a purely LITERAL match
// inside a grep -E/sed -E pattern. Given hostnetIfaceNameRE's restricted
// charset ([A-Za-z0-9._-]), the only character this can ever actually need
// to escape is '.' (matches any character, unescaped) — a VLAN-style
// sub-interface name like "enp3s0.100" is the one realistic shape where
// this matters. The full ERE metacharacter set is escaped regardless
// (defensive: stays correct even if hostnetIfaceNameRE's charset is ever
// loosened).
func hostnetEscapeEREChars(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '^', '$', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hostnetStaleRefPattern returns the POSIX ERE used, identically, both to
// detect (grep -Eq) and rewrite (sed -E) a whole-word reference to
// interface name old within an /etc/network/interfaces(.d/*) file. old must
// be preceded and followed by either start/end-of-line or a
// non-alphanumeric/non-underscore character, so "ens18" matches as the sole
// value of `bridge-ports ens18` or as one token inside `bond-slaves ens18
// ens19`, but never as a substring of a longer token like "ens180" or
// "myens18if". Capture group 1 is the (possibly empty) leading boundary
// character, group 2 the trailing one; the sed replacement re-emits both
// around the new name so the boundary character itself (a space, a tab, or
// nothing at start/end of line) is preserved untouched.
//
// TestHostnetStaleRefPattern_* proves this exact pattern's word-boundary
// behavior directly against Go's regexp engine, applied line-by-line
// (matching how `sed` processes one line of pattern space at a time, so
// `^`/`$` mean start/end of THAT line, consistent with Go's own default
// non-multiline `^`/`$` semantics applied to a single-line input) — the
// bracket-expression-and-alternation shape used here has identical meaning
// under POSIX ERE and Go's RE2, so this is a faithful proof of the literal
// pattern text embedded into the remote script below, not merely an
// analogous one.
func hostnetStaleRefPattern(old string) string {
	return fmt.Sprintf(`(^|[^[:alnum:]_])%s([^[:alnum:]_]|$)`, hostnetEscapeEREChars(old))
}

// hostnetBuildInterfacesRewriteScript builds the POSIX sh function
// definition and invocation — embedded into hostnetBuildNICRenameCmd's
// larger composite command, under the same `set -eu` — that rewrites every
// stale whole-word reference to a renamed NIC's Old kernel name, in
// hostnetInterfacesFile and every regular file directly under
// hostnetInterfacesDDir, to its New lab-convention name
// (hostnetStaleRefPattern's word-boundary match). Returns "" when pairs is
// empty (nothing to rewrite — hostnetBuildNICRenameCmd omits this block
// entirely in that case).
//
// A file is left completely untouched (no backup, no stdout line) unless it
// actually contains at least one stale reference. A file that DOES get
// modified is first backed up (cp -p, preserving mode/ownership/timestamps)
// exactly once, even when multiple pairs each match it, then has every
// matching pair's stale references rewritten in place (GNU sed -i, PVE's
// own Debian userland — no backup-suffix argument, unlike BSD sed's
// mandatory one). Every invocation passes its own backup target as a
// second argument rather than the function deriving one from the file's
// own path: hostnetInterfacesFile backs up to a plain sibling of itself
// ("<file>.<hostnetInterfacesBackupInfix>.<UTC timestamp>"), but a file
// found under hostnetInterfacesDDir backs up to
// hostnetInterfacesDBackupDir instead — see that constant's doc comment for
// why a same-directory backup is unsafe specifically for interfaces.d
// files (it is not for hostnetInterfacesFile itself).
//
// Idempotent: a file with no remaining stale reference to any pair (already
// rewritten by a prior run, or never referenced any old name at all) is a
// pure no-op on every subsequent call — no backup, no rewrite, no stdout
// line. This is what makes a re-run against the "links already written,
// interfaces already rewritten, reboot still pending" intermediate state
// converge safely, without re-backing-up or re-touching an already-correct
// file, rather than what would otherwise require tracking "did I already do
// this" state separately from the file content itself. Routing
// interfaces.d backups outside hostnetInterfacesDDir (rather than merely
// naming them distinctively inside it) is what makes this true for THOSE
// files too: a same-directory backup would itself be picked up by the very
// `for f in .../*` loop below on the next call, since it would still
// contain the pre-rewrite stale reference — confirmed as a real,
// non-idempotent failure mode against a real Debian container during this
// fix's development, before hostnetInterfacesDBackupDir was introduced.
func hostnetBuildInterfacesRewriteScript(pairs []hostnetRenamePair) string {
	if len(pairs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("PMXNICTS=$(date -u +%Y%m%dT%H%M%SZ)\n")
	b.WriteString("pmx_rewrite_stale_refs() {\n")
	b.WriteString("  f=\"$1\"\n")
	b.WriteString("  bak=\"$2\"\n")
	b.WriteString("  [ -f \"$f\" ] || return 0\n")
	b.WriteString("  changed=0\n")
	for _, p := range pairs {
		pattern := hostnetStaleRefPattern(p.Old)
		b.WriteString("  if grep -Eq \"" + pattern + "\" \"$f\"; then\n")
		b.WriteString("    if [ \"$changed\" = \"0\" ]; then\n")
		b.WriteString("      mkdir -p \"$(dirname \"$bak\")\"\n")
		b.WriteString("      cp -p \"$f\" \"$bak\"\n")
		b.WriteString("      changed=1\n")
		b.WriteString("    fi\n")
		b.WriteString("    sed -E -i \"s/" + pattern + "/\\1" + p.New + "\\2/g\" \"$f\"\n")
		b.WriteString("  fi\n")
	}
	b.WriteString("  [ \"$changed\" = \"1\" ] && echo \"" + hostnetRewrittenPrefix + "$f\"\n")
	b.WriteString("  return 0\n")
	b.WriteString("}\n")
	b.WriteString("pmx_rewrite_stale_refs " + hostnetInterfacesFile +
		" \"" + hostnetInterfacesFile + "." + hostnetInterfacesBackupInfix + ".$PMXNICTS\"\n")
	b.WriteString("if [ -d " + hostnetInterfacesDDir + " ]; then\n")
	b.WriteString("  for f in " + hostnetInterfacesDDir + "/*; do\n")
	b.WriteString("    pmx_rewrite_stale_refs \"$f\" \"" + hostnetInterfacesDBackupDir +
		"/$(basename \"$f\")." + hostnetInterfacesBackupInfix + ".$PMXNICTS\"\n")
	b.WriteString("  done\n")
	b.WriteString("fi\n")
	return b.String()
}

// hostnetParseRewrittenFiles scans output (a rename command's captured
// stdout) for hostnetRewrittenPrefix-tagged lines and returns the file
// paths named on them, in the order the remote script printed them.
func hostnetParseRewrittenFiles(output string) []string {
	var files []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, ok := strings.CutPrefix(line, hostnetRewrittenPrefix); ok && rest != "" {
			files = append(files, rest)
		}
	}
	return files
}

// hostnetBuildNICRenameCmd builds the single composite POSIX sh command
// that: writes every sorted[i]'s systemd .link file (pinning it to nicI);
// rewrites any stale reference to a renamed NIC's old kernel name still
// present in /etc/network/interfaces or /etc/network/interfaces.d/*
// (hostnetBuildInterfacesRewriteScript — a day-2 node's bonds/bridges may
// already be configured against the old name); then refreshes the
// initramfs (initramfs-tools' udev hook copies /etc/systemd/network/*.link
// so the rename holds from the earliest boot stage —
// scripts/first-boot-network.sh.tmpl's own rationale), run as one ssh call
// (runGuestSSH's established one-composite-command-per-node convention,
// guestssh.go). Uses `set -eu` plus sequential statements (rather than
// `&&`-joining across heredoc boundaries, which is not valid POSIX sh — a
// command following a heredoc's closing delimiter cannot begin on the
// delimiter's own following line with a leading operator) so any failed
// write, failed rewrite, or failed update-initramfs aborts the whole call.
// Every MAC and every OLD name (for entries actually needing a rename) must
// already have been validated (hostnetMACRE, hostnetIfaceNameRE) by the
// caller (hostnetEnsureNICNaming) — this function does not re-validate, so
// it must never be called with unvalidated NIC data.
func hostnetBuildNICRenameCmd(sorted []hostnetPhysicalNIC) string {
	var pairs []hostnetRenamePair
	var b strings.Builder
	b.WriteString("set -eu\n")
	fmt.Fprintf(&b, "mkdir -p %s\n", hostnetLinkFileDir)
	for i, n := range sorted {
		fmt.Fprintf(&b, "cat > %s <<'PMXNICEOF'\n%sPMXNICEOF\n", hostnetLinkFilePath(i), hostnetLinkFileContent(i, n.MAC))
		if n.Name != hostnetNICName(i) {
			pairs = append(pairs, hostnetRenamePair{Old: n.Name, New: hostnetNICName(i)})
		}
	}
	b.WriteString(hostnetBuildInterfacesRewriteScript(pairs))
	b.WriteString("update-initramfs -u\n")
	return b.String()
}

// hostnetNICNamingOutcome distinguishes hostnetEnsureNICNaming's two
// possible non-error outcomes for one node.
type hostnetNICNamingOutcome int

const (
	// hostnetNICNamingAlreadyDone means every physical NIC already carries
	// its nic0-nic5 lab-convention name; the bond/bridge phase may proceed
	// immediately for this node.
	hostnetNICNamingAlreadyDone hostnetNICNamingOutcome = iota
	// hostnetNICNamingRebootPending means this call wrote fresh .link files
	// and refreshed the initramfs; the rename only takes effect at reboot (a
	// live rename would tear the active mgmt NIC out of vmbr0 while it is
	// carrying the connection this command is itself running over), so the
	// bond/bridge phase must NOT run for this node until an operator has
	// rebooted it (serialized, one node at a time — cluster runbook §10.2)
	// and hostnet apply has been re-run.
	hostnetNICNamingRebootPending
)

// hostnetEnsureNICNaming ensures node nodeName's (at nodeIP) physical NICs
// are named nic0-nic5 before any bond/bridge work runs against it. See the
// "--- NIC naming ensure phase ---" section comment above for the day-1/
// day-2 split this is the day-2 half of.
//
// Enumerates via hostnetNICEnumerateCmd, sorts by PCI function
// (hostnetSortNICs), and requires exactly hostnetRequiredNICCount(6)
// resolvable physical NICs — anything else is a hard error naming what was
// found, since a lab node's net0-net5 VM slots (`pmx lab create`) are the
// only source of truth for how many physical NICs to expect; a node
// reporting some other count needs its NIC reconcile + reboot step run
// first (or its q35 layout does not match the lab convention at all), and
// this phase cannot safely guess which. When every sorted position i
// already carries the name nicI, this is a no-op
// (hostnetNICNamingAlreadyDone) and no ssh mutation is issued. Otherwise it
// validates every MAC (hostnetMACRE) before writing anything, writes one
// MAC-matched systemd .link file per NIC and runs update-initramfs -u in a
// single composite ssh call (hostnetBuildNICRenameCmd), and returns
// hostnetNICNamingRebootPending — this call NEVER reboots the node itself.
//
// Returns one NODE/STEP/STATUS row describing the outcome, the outcome
// itself, and an error only for a hard failure (ssh transport failure,
// wrong NIC count, malformed enumeration output, an invalid MAC read back
// from the node) — never for the expected "reboot pending" outcome, which
// is reported via the returned outcome value instead so the caller
// (runHostnetApply) can continue processing the lab's other nodes rather
// than aborting the whole run.
func hostnetEnsureNICNaming(deps *cli.Deps, name, nodeName string, idx int, nodeIP string) ([]string, hostnetNICNamingOutcome, error) {
	stepLabel := fmt.Sprintf("ensure NIC naming (nic0-nic%d)", hostnetRequiredNICCount-1)

	enumRes, err := runGuestSSH(deps, nodeIP, hostnetNICEnumerateCmd)
	if err != nil {
		return nil, hostnetNICNamingAlreadyDone, fmt.Errorf(
			"lab %q: node %d (%s): enumerate physical NICs: %w", name, idx, nodeIP, err)
	}

	all, perr := hostnetParseNICEnumeration(enumRes.Stdout)
	if perr != nil {
		return nil, hostnetNICNamingAlreadyDone, fmt.Errorf("lab %q: node %d (%s): %w", name, idx, nodeIP, perr)
	}

	sorted := hostnetSortNICs(all)
	if len(sorted) != hostnetRequiredNICCount {
		found := make([]string, len(sorted))
		for i, n := range sorted {
			found[i] = fmt.Sprintf("%s(%s)", n.Name, n.MAC)
		}
		return nil, hostnetNICNamingAlreadyDone, fmt.Errorf(
			"lab %q: node %d (%s): expected exactly %d physical NICs (net0-net%d from `pmx lab create`), "+
				"found %d with a resolvable PCI address: [%s] — the NIC reconcile + reboot step "+
				"(scripts/first-boot-network.sh.tmpl's day-1 equivalent) must run first, or this node's "+
				"PVE q35 NIC layout does not match the lab convention",
			name, idx, nodeIP, hostnetRequiredNICCount, hostnetRequiredNICCount-1, len(sorted), strings.Join(found, ", "))
	}

	allNamed := true
	for i, n := range sorted {
		if n.Name != hostnetNICName(i) {
			allNamed = false
			break
		}
	}
	if allNamed {
		return []string{nodeName, stepLabel, "already named nic0-nic5 (no-op)"}, hostnetNICNamingAlreadyDone, nil
	}

	for i, n := range sorted {
		if !hostnetMACRE.MatchString(n.MAC) {
			return nil, hostnetNICNamingAlreadyDone, fmt.Errorf(
				"lab %q: node %d (%s): physical NIC %q (sorted position %d, would become nic%d) reports "+
					"an invalid MAC address %q; refusing to write a .link file for it",
				name, idx, nodeIP, n.Name, i, i, n.MAC)
		}
		// Names already at their target (n.Name == hostnetNICName(i)) never
		// become a hostnetRenamePair.Old (hostnetBuildNICRenameCmd's own
		// check), so only a NIC that actually needs renaming has its old name
		// interpolated into the interfaces-rewrite script — validate exactly
		// (and only) those, mirroring the MAC check just above.
		if n.Name != hostnetNICName(i) && !hostnetIfaceNameRE.MatchString(n.Name) {
			return nil, hostnetNICNamingAlreadyDone, fmt.Errorf(
				"lab %q: node %d (%s): physical NIC %q (sorted position %d, would become nic%d) has a "+
					"kernel-reported name containing characters outside [A-Za-z0-9._-]; refusing to use it "+
					"in the /etc/network/interfaces stale-reference rewrite",
				name, idx, nodeIP, n.Name, i, i)
		}
	}

	renameCmd := hostnetBuildNICRenameCmd(sorted)
	renameRes, err := runGuestSSH(deps, nodeIP, renameCmd)
	if err != nil {
		return nil, hostnetNICNamingAlreadyDone, fmt.Errorf(
			"lab %q: node %d (%s): write nic0-nic%d .link files, rewrite stale interfaces references, and "+
				"refresh initramfs: %w",
			name, idx, nodeIP, hostnetRequiredNICCount-1, err)
	}

	status := fmt.Sprintf(
		"NIC renames written (nic0-nic%d), initramfs refreshed",
		hostnetRequiredNICCount-1)
	if rewritten := hostnetParseRewrittenFiles(renameRes.Stdout); len(rewritten) > 0 {
		status += fmt.Sprintf(", rewrote stale interface-name references in: %s", strings.Join(rewritten, ", "))
	} else {
		status += "; no stale interface-name references found in /etc/network/interfaces(.d/*)"
	}
	status += " — REBOOT REQUIRED before bond/bridge work can proceed on this node (serialize reboots " +
		"one node at a time per cluster runbook §10.2, then re-run hostnet apply)"
	return []string{nodeName, stepLabel, status}, hostnetNICNamingRebootPending, nil
}

// hostnetIfaceState is the subset of a `/nodes/{node}/network` list entry
// hostnet apply reads to decide whether a bond or bridge interface exists
// and matches its configured shape. BridgeVlanAware and Autostart decode as
// int64, not bool: PVE's list endpoint reports its 1/0-valued fields as
// JSON numbers, not JSON booleans (the same convention internal/cli/node/
// network.go's netIfaceEntry already documents for Active/Autostart, whose
// exact field names/JSON keys/types Cidr/Address/Netmask/Gateway/Autostart
// below mirror byte-for-byte), so unmarshaling straight into a Go bool
// would fail on every real response.
//
// Cidr/Address/Netmask/Gateway exist here purely so this package's own
// UpdateNetwork2 calls can carry an interface's EXISTING addressing
// forward unchanged (hostnetPreserveUntouchedBridgeFields) — hostnet apply
// itself never reads config for, or makes decisions based on, any
// interface's addressing; it only prevents accidentally erasing it as a
// side effect of a PUT issued for an unrelated reason (bridge_ports,
// bridge_vlan_aware, or autostart drift/staging). Confirmed live: az1's
// vmbr0 flipped from `inet static` (with address+gateway) to `inet
// manual` after an UpdateNetwork2 call that specified neither field.
type hostnetIfaceState struct {
	Iface           string `json:"iface"`
	Type            string `json:"type"`
	Slaves          string `json:"slaves"`
	BondMode        string `json:"bond_mode"`
	BondPrimary     string `json:"bond-primary"`
	BridgePorts     string `json:"bridge_ports"`
	BridgeVlanAware int64  `json:"bridge_vlan_aware"`
	Autostart       int64  `json:"autostart"`
	Cidr            string `json:"cidr"`
	Address         string `json:"address"`
	Netmask         string `json:"netmask"`
	Gateway         string `json:"gateway"`
}

// hostnetDecodeInterfaces decodes every entry of list into a hostnetIfaceState,
// indexed by interface name. A raw entry naming no iface (should never
// happen against a real PVE response) is skipped rather than overwriting a
// prior indexed entry with an empty-keyed one.
func hostnetDecodeInterfaces(list nodes.ListNetworkResponse) (map[string]hostnetIfaceState, error) {
	out := make(map[string]hostnetIfaceState, len(list))
	for _, raw := range list {
		var e hostnetIfaceState
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("decode network interface entry: %w", err)
		}
		if e.Iface == "" {
			continue
		}
		out[e.Iface] = e
	}
	return out, nil
}

// hostnetSplitFields splits s on any run of whitespace (PVE's own
// "slaves"/"bridge_ports" list-of-interface-names separator) and returns the
// tokens sorted, so two interface lists naming the same set compare equal
// regardless of ordering — mirroring sdninner.go's normalizePeers/peersEqual
// convention for the same class of comparison.
func hostnetSplitFields(s string) []string {
	fields := strings.Fields(s)
	sort.Strings(fields)
	return fields
}

// hostnetFieldsEqual reports whether a and b name the same whitespace-
// separated interface set, regardless of ordering (hostnetSplitFields).
func hostnetFieldsEqual(a, b string) bool {
	as, bs := hostnetSplitFields(a), hostnetSplitFields(b)
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// hostnetEnsureNode reconciles every bond+bridge pair of nn.Bonds against
// node nodeName's live/staged interface list, then issues one UpdateNetwork
// (the staged-changes reload) call for that node iff anything changed.
// Returns one STEP/STATUS row (NODE-prefixed) per bond, per bridge, and a
// final "apply staged network changes" row.
func hostnetEnsureNode(ctx context.Context, api *apiclient.APIClient, name, nodeName string, nn config.LabNestedNetwork) ([][]string, error) {
	list, err := api.Nodes.ListNetwork(ctx, nodeName, nil)
	if err != nil {
		return nil, fmt.Errorf("lab %q: list network interfaces on node %q: %w", name, nodeName, err)
	}
	var existing map[string]hostnetIfaceState
	if list != nil {
		existing, err = hostnetDecodeInterfaces(*list)
		if err != nil {
			return nil, fmt.Errorf("lab %q: decode network interfaces on node %q: %w", name, nodeName, err)
		}
	} else {
		existing = map[string]hostnetIfaceState{}
	}

	var rows [][]string
	changed := false

	for i, b := range nn.Bonds {
		if len(b.NICs) < 2 {
			return nil, fmt.Errorf(
				"lab %q: network.nested_network.bonds[%d] (%q) has %d nics, need at least 2 to bond",
				name, i, b.Name, len(b.NICs))
		}

		restageChanged, restageRow, err := hostnetRestageBridgeIfSlaveConflict(ctx, api, nodeName, b, existing)
		if err != nil {
			return nil, fmt.Errorf("lab %q: %w", name, err)
		}
		if restageRow != nil {
			rows = append(rows, restageRow)
		}
		changed = changed || restageChanged

		bondChanged, bondRow, err := hostnetEnsureBond(ctx, api, nodeName, b, existing)
		if err != nil {
			return nil, fmt.Errorf("lab %q: %w", name, err)
		}
		rows = append(rows, bondRow)
		changed = changed || bondChanged

		bridgeChanged, bridgeRow, err := hostnetEnsureBridge(ctx, api, nodeName, b, existing)
		if err != nil {
			return nil, fmt.Errorf("lab %q: %w", name, err)
		}
		rows = append(rows, bridgeRow)
		changed = changed || bridgeChanged
	}

	applyStatus := "skip (no pending changes)"
	if changed {
		if _, err := api.Nodes.UpdateNetwork(ctx, nodeName, &nodes.UpdateNetworkParams{}); err != nil {
			return nil, fmt.Errorf("lab %q: apply staged network changes on node %q: %w", name, nodeName, err)
		}
		applyStatus = "applied"
	}
	rows = append(rows, []string{nodeName, "apply staged network changes", applyStatus})

	return rows, nil
}

// hostnetRestageBridgeIfSlaveConflict covers the day-2 retrofit shape a
// fresh-install node never hits: bond b.Name does not exist yet, but
// b.Bridge ALREADY exists (installer-created, e.g. PVE's own default
// single-NIC vmbr0) and its current bridge_ports directly names one or
// more of b.NICs — the exact NICs b.Name is about to claim as slaves.
// Creating the bond as-is fails PVE's own parameter validation ("nicN is
// already used on interface '<bridge>'") because that NIC is still live on
// the bridge; confirmed against az1's real retrofit state (vmbr0 holding
// nic0 directly, no bonds yet).
//
// When this shape is detected, it clears b.Bridge's bridge_ports to ""
// (UpdateNetwork2 — a ports-less bridge is valid PVE config) BEFORE
// hostnetEnsureBond ever runs, freeing the slave NICs in the staged view
// without referencing anything. It deliberately does NOT stage
// bridge_ports=b.Name directly here — confirmed live against az1: PVE's
// own parameter verification for a bridge's bridge_ports rejects a name
// with no representation AT ALL yet, staged or applied ("unable to find
// bridge port 'bond0'"), and at the point this function runs, b.Name has
// no representation whatsoever (hostnetEnsureBond has not created it,
// staged or otherwise). Restaging to "" avoids referencing bond b.Name
// before it exists in any form; the SECOND half of the restage — pointing
// b.Bridge's bridge_ports at b.Name — falls out naturally from
// hostnetEnsureBridge's own ordinary drift-reconciliation, which runs
// AFTER hostnetEnsureBond has already staged b.Name via CreateNetwork (see
// hostnetEnsureNode's call order: this function, then hostnetEnsureBond,
// then hostnetEnsureBridge) — by the time hostnetEnsureBridge compares
// existing[b.Bridge] (now "") against b.Name and issues its own
// UpdateNetwork2, b.Name DOES have a staged representation (the bond POST
// that just ran), matching PVE's own documented staged-changes workflow
// (the web UI supports creating a bond and pointing a bridge at it in the
// same pending batch, applied together) — this is the basis for expecting
// that second PUT to succeed where the ORIGINAL single-PUT-to-bond-name
// design failed. This has not been exercised against a live nested
// cluster from this package's own test suite; treat it as unconfirmed
// until proven against az1.
//
// Both this function's own PUT and hostnetEnsureBridge's later one are
// pure staging calls — PVE's `/nodes/{node}/network` POST/PUT write only
// to the pending interfaces.new file, never applying anything until
// hostnetEnsureNode's own single UpdateNetwork (reload) call at the very
// end of the WHOLE bond/bridge loop (every bond, not just this one) — so
// this restage, the bond create, and the bridge's own follow-up PUT all
// land in that SAME pending file, applied together in that one later
// reload. There is no window where a live vmbr0 has neither nic0 nor a
// working bond0, since nothing is actually applied until then. If either
// staged PUT is ever rejected by PVE (as the ORIGINAL bridge_ports=b.Name
// design was), the failure mode is non-degrading: the rejected call
// returns an error immediately, propagated up before hostnetEnsureNode
// ever reaches its apply step — never a half-applied state. A genuinely
// two-apply fallback (restage+create+apply, THEN a second
// reconcile-and-apply pass) would introduce a real interim window where
// b.Bridge is ports-less and (for vmbr0 specifically) mgmt/corosync-
// affecting; this function deliberately does not implement one — if the
// single-apply ordering above is ever proven insufficient live, that is a
// design decision for a human to make explicitly, not something to fall
// back to silently.
//
// On success, this function ALSO updates existing[b.Bridge]'s in-memory
// BridgePorts to "" (the value it actually staged, NOT b.Name), so
// hostnetEnsureBridge's own drift check correctly sees it still needs its
// own PUT.
//
// Returns (false, nil, nil) — a pure no-op, no API call — for every shape
// OTHER than the exact conflict above: b.Name already exists (this
// function is only ever relevant during a bond's first creation);
// b.Bridge does not exist yet (nothing holds the NICs, so
// hostnetEnsureBond's plain create path has no conflict to avoid — this is
// the fresh-install/az2 shape, and every other still-to-be-created
// bond/bridge pair in a partially-converged multi-bond lab); b.Bridge
// exists but is not of type "bridge" (hostnetEnsureBridge's own type-guard
// covers that case on its own turn); b.Bridge's current bridge_ports is
// already empty (nothing to free — this is also what makes a re-run
// against a "staged-but-unapplied leftover" from a prior partially-failed
// run converge correctly: the ListNetwork this package reads existing[]
// from reflects PVE's pending state, so an already-cleared bridge_ports
// from an earlier interrupted run reads back as already-empty here, and
// this function is a no-op on the retry, falling through to
// hostnetEnsureBond/hostnetEnsureBridge to finish the job); or
// bridge_ports already equals b.Name exactly (bridge already points at the
// bond — a hand-edited config, or state this package itself would never
// produce given it always restages to "" first — nothing further to stage
// here).
//
// Returns a hard error, naming the node, the bridge, and what its
// bridge_ports actually holds, when b.Bridge exists, is of type "bridge",
// and its bridge_ports is non-empty but references at least one interface
// that is NEITHER already b.Name NOR one of b.NICs — an unexpected shape
// this function must never silently rewrite past.
func hostnetRestageBridgeIfSlaveConflict(ctx context.Context, api *apiclient.APIClient, nodeName string, b config.LabNestedBond, existing map[string]hostnetIfaceState) (bool, []string, error) {
	if _, bondFound := existing[b.Name]; bondFound {
		return false, nil, nil
	}

	bridge, bridgeFound := existing[b.Bridge]
	if !bridgeFound || bridge.Type != "bridge" {
		return false, nil, nil
	}

	ports := hostnetSplitFields(bridge.BridgePorts)
	if len(ports) == 0 || hostnetFieldsEqual(bridge.BridgePorts, b.Name) {
		return false, nil, nil
	}

	for _, port := range ports {
		if !slices.Contains(b.NICs, port) {
			return false, nil, fmt.Errorf(
				"node %q: bridge %q already exists with bridge_ports %q, which references %q — not "+
					"one of bond %q's configured nics (%s) and not %q itself; refusing to restage it "+
					"automatically, since this is not the expected \"bridge still directly holds one of "+
					"its own future bond's slave nics\" retrofit shape",
				nodeName, b.Bridge, bridge.BridgePorts, port, b.Name, strings.Join(b.NICs, ","), b.Name)
		}
	}

	stepLabel := fmt.Sprintf(
		"restage bridge %q (bridge_ports %q -> \"\", freeing its slave nics before bond %q is created)",
		b.Bridge, bridge.BridgePorts, b.Name)
	params := &nodes.UpdateNetwork2Params{Type: "bridge", BridgePorts: netPtr("")}
	// This PUT's own opinion is BridgePorts only — every other field
	// (addressing, autostart, vlan_aware) must be carried forward from the
	// bridge's current state exactly as read, or PVE defaults it away (see
	// hostnetPreserveUntouchedBridgeFields's own doc comment for the live
	// az1 evidence this guards against).
	hostnetPreserveUntouchedBridgeFields(bridge, params)
	if err := api.Nodes.UpdateNetwork2(ctx, nodeName, b.Bridge, params); err != nil {
		return false, nil, fmt.Errorf("node %q: %s: %w", nodeName, stepLabel, err)
	}

	bridge.BridgePorts = ""
	existing[b.Bridge] = bridge

	return true, []string{nodeName, stepLabel, "restaged (bridge_ports cleared)"}, nil
}

// hostnetPreserveUntouchedBridgeFields fills every still-nil addressing/
// autostart/vlan-aware field of params from cur's current state, for every
// field this specific call has not already formed its own opinion on.
// PVE's `/nodes/{node}/network/{iface}` PUT is NOT a partial patch: it
// treats the parameter set of each call as authoritative for the WHOLE
// interface, so a field neither this call's own logic nor a caller has
// already set is silently defaulted away rather than left unchanged.
// Confirmed live: after an UpdateNetwork2 call for vmbr0 that specified
// neither address nor gateway (only bridge_ports/type), the node's
// rendered /etc/network/interfaces flipped that stanza from `inet static`
// (with its original address+gateway) to `inet manual` — this function
// exists specifically to close that gap for every UpdateNetwork2 call this
// package issues against an ALREADY-EXISTING bridge (hostnetEnsureBridge's
// own update path, and hostnetRestageBridgeIfSlaveConflict's
// bridge_ports-to-empty restage).
//
// Callers that themselves own a field's value for THIS call (e.g.
// hostnetEnsureBridge's own config-driven BridgeVlanAware/BridgePorts
// drift logic, or an autostart-drift fix) must set params' opinion on that
// field BEFORE calling this — a field already non-nil on params is left
// untouched, never overwritten, so this only ever fills in what nobody
// else already decided for this specific call. Every copy is itself
// conditional on cur actually having a value for that field: a `*string`
// with `omitempty` still serializes an explicit "" (only a nil pointer is
// omitted), so blindly copying an always-empty field would itself assert
// "no address"/"not vlan-aware" onto an interface that may simply have
// never had that property touched by this package before (e.g. a
// deliberately address-less VLAN-trunk bridge).
func hostnetPreserveUntouchedBridgeFields(cur hostnetIfaceState, params *nodes.UpdateNetwork2Params) {
	if params.Cidr == nil && cur.Cidr != "" {
		params.Cidr = netPtr(cur.Cidr)
	}
	if params.Address == nil && cur.Address != "" {
		params.Address = netPtr(cur.Address)
	}
	if params.Netmask == nil && cur.Netmask != "" {
		params.Netmask = netPtr(cur.Netmask)
	}
	if params.Gateway == nil && cur.Gateway != "" {
		params.Gateway = netPtr(cur.Gateway)
	}
	if params.Autostart == nil && cur.Autostart != 0 {
		params.Autostart = netPtr(true)
	}
	if params.BridgeVlanAware == nil && cur.BridgeVlanAware != 0 {
		params.BridgeVlanAware = netPtr(true)
	}
}

// hostnetEnsureBond ensures bond b.Name exists as a "bond" interface on
// nodeName with slaves b.NICs (space-joined), mode b.Mode, and (when set)
// primary b.Primary: it creates the bond when absent; when present, it
// updates only the fields that have drifted, issuing no request when nothing
// has changed. An interface already present under b.Name with a type other
// than "bond" is a hard error — hostnet apply must never silently repurpose
// an existing non-bond interface (e.g. the outer net0's own eth device).
// Returns whether anything changed and this step's NODE/STEP/STATUS row.
func hostnetEnsureBond(ctx context.Context, api *apiclient.APIClient, nodeName string, b config.LabNestedBond, existing map[string]hostnetIfaceState) (bool, []string, error) {
	stepLabel := fmt.Sprintf("bond %q (mode %s, nics %s)", b.Name, b.Mode, strings.Join(b.NICs, ","))
	wantSlaves := strings.Join(b.NICs, " ")

	cur, found := existing[b.Name]
	if !found {
		// Autostart: az2 (the reference, first-boot-scripted shape) has
		// `auto` on every interface it renders, including every bond;
		// az1's day-2-created bonds initially had none at all — confirmed
		// live, PVE's own default for a network-API-created interface is
		// autostart=0 (no `auto` line), which ifreload never brings up on
		// its own (a bridge layered on top only comes up as a DEPENDENCY
		// of ITS OWN `auto` line, not because its slave bond has one) —
		// bond1/bond2 and vmbr1/vmbr2 stayed down entirely in that state.
		params := &nodes.CreateNetworkParams{
			Iface: b.Name, Type: "bond", Slaves: netPtr(wantSlaves), BondMode: netPtr(b.Mode),
			Autostart: netPtr(true),
		}
		if b.Primary != "" {
			params.BondPrimary = netPtr(b.Primary)
		}
		if err := api.Nodes.CreateNetwork(ctx, nodeName, params); err != nil {
			return false, nil, fmt.Errorf("node %q: create bond %q: %w", nodeName, b.Name, err)
		}
		return true, []string{nodeName, stepLabel, "created"}, nil
	}

	if cur.Type != "bond" {
		return false, nil, fmt.Errorf(
			"node %q: interface %q already exists as type %q, not bond; refusing to overwrite it",
			nodeName, b.Name, cur.Type)
	}

	params := &nodes.UpdateNetwork2Params{Type: "bond"}
	drifted := false
	if !hostnetFieldsEqual(cur.Slaves, wantSlaves) {
		params.Slaves = netPtr(wantSlaves)
		drifted = true
	}
	if cur.BondMode != b.Mode {
		params.BondMode = netPtr(b.Mode)
		drifted = true
	}
	if b.Primary != "" && cur.BondPrimary != b.Primary {
		params.BondPrimary = netPtr(b.Primary)
		drifted = true
	}
	if cur.Autostart == 0 {
		params.Autostart = netPtr(true)
		drifted = true
	}
	if !drifted {
		return false, []string{nodeName, stepLabel, "already matches"}, nil
	}

	if err := api.Nodes.UpdateNetwork2(ctx, nodeName, b.Name, params); err != nil {
		return false, nil, fmt.Errorf("node %q: update bond %q: %w", nodeName, b.Name, err)
	}
	return true, []string{nodeName, stepLabel, "updated"}, nil
}

// hostnetEnsureBridge ensures b.Bridge exists as a "bridge" interface on
// nodeName with bridge_ports b.Name (the bond built above) and
// bridge_vlan_aware b.VlanAware: it creates the bridge when absent; when
// present, it updates only the fields that have drifted, issuing no request
// when nothing has changed. An interface already present under b.Bridge with
// a type other than "bridge" is a hard error, mirroring hostnetEnsureBond's
// own non-overwrite guarantee. Returns whether anything changed and this
// step's NODE/STEP/STATUS row.
func hostnetEnsureBridge(ctx context.Context, api *apiclient.APIClient, nodeName string, b config.LabNestedBond, existing map[string]hostnetIfaceState) (bool, []string, error) {
	stepLabel := fmt.Sprintf("bridge %q (port %s, vlan_aware=%v)", b.Bridge, b.Name, b.VlanAware)

	cur, found := existing[b.Bridge]
	if !found {
		// Autostart: see hostnetEnsureBond's matching comment — az2's
		// reference shape has `auto` on every interface; a freshly
		// API-created bridge otherwise comes up with none (confirmed live
		// on az1: vmbr1/vmbr2 existed as correct stanzas but had no `auto`
		// line and no live link at all).
		params := &nodes.CreateNetworkParams{
			Iface: b.Bridge, Type: "bridge", BridgePorts: netPtr(b.Name), Autostart: netPtr(true),
		}
		if b.VlanAware {
			params.BridgeVlanAware = netPtr(true)
		}
		if err := api.Nodes.CreateNetwork(ctx, nodeName, params); err != nil {
			return false, nil, fmt.Errorf("node %q: create bridge %q: %w", nodeName, b.Bridge, err)
		}
		return true, []string{nodeName, stepLabel, "created"}, nil
	}

	if cur.Type != "bridge" {
		return false, nil, fmt.Errorf(
			"node %q: interface %q already exists as type %q, not bridge; refusing to overwrite it",
			nodeName, b.Bridge, cur.Type)
	}

	params := &nodes.UpdateNetwork2Params{Type: "bridge"}
	drifted := false
	if !hostnetFieldsEqual(cur.BridgePorts, b.Name) {
		params.BridgePorts = netPtr(b.Name)
		drifted = true
	}
	if (cur.BridgeVlanAware != 0) != b.VlanAware {
		params.BridgeVlanAware = netPtr(b.VlanAware)
		drifted = true
	}
	if cur.Autostart == 0 {
		params.Autostart = netPtr(true)
		drifted = true
	}
	if !drifted {
		return false, []string{nodeName, stepLabel, "already matches"}, nil
	}

	// This PUT's own opinion is whichever of bridge_ports/vlan_aware/
	// autostart drifted above; every other field (addressing, and
	// vlan_aware/autostart when THEY didn't drift) must be carried forward
	// from cur unchanged, or PVE defaults it away — see
	// hostnetPreserveUntouchedBridgeFields's own doc comment for the live
	// az1 evidence this guards against (this is the SAME UpdateNetwork2
	// endpoint hostnetRestageBridgeIfSlaveConflict's restage-to-empty PUT
	// uses, and either call could have been the one responsible for the
	// live `inet static` -> `inet manual` flip observed there).
	hostnetPreserveUntouchedBridgeFields(cur, params)

	if err := api.Nodes.UpdateNetwork2(ctx, nodeName, b.Bridge, params); err != nil {
		return false, nil, fmt.Errorf("node %q: update bridge %q: %w", nodeName, b.Bridge, err)
	}
	return true, []string{nodeName, stepLabel, "updated"}, nil
}
