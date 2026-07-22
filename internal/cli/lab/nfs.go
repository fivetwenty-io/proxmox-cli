package lab

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nfsExportRoot is the ZFS/NFS export path root the shared NFS service
// (lab repo scripts/60-nfs-service, ADR-0012, multi-node lab plan §8.4)
// serves from on sm-0: "tank/nfs" today, migrating in place to a dedicated
// "nfs_pool" once new drives arrive (decision D1, amended) — that migration
// re-runs `pmx lab nfs attach` against the new server/export path rather
// than editing existing storage entries in place, per §8.4's documented
// procedure, so this constant's value tracks the CURRENT server layout, not
// a config-driven setting.
const nfsExportRoot = "/tank/nfs"

// The three fixed PVE storage IDs `pmx lab nfs attach` registers on a lab's
// node 0 (multi-node lab plan §8.5): nfsImagesStorageID/nfsBackupStorageID
// are lab-scoped (rw, hard-mounted); nfsSharedIsoStorageID is the one
// cluster-wide, read-only ISO/template export shared by every lab.
const (
	nfsImagesStorageID    = "nfs-images"
	nfsBackupStorageID    = "nfs-backup"
	nfsSharedIsoStorageID = "shared-iso"
)

// nfsAttachTarget describes one `pvesm add nfs` storage entry `pmx lab nfs
// attach` ensures.
type nfsAttachTarget struct {
	id      string
	export  string
	content string
	options string
}

// nfsAttachTargets returns the three fixed storage entries a lab needs
// (multi-node lab plan §8.5): the lab's own images/backup exports (rw,
// vers=4.1, hard-mounted — the PVE default, deliberately not "soft": a
// transient NFS hiccup should stall the guest, not silently error its I/O),
// and the shared, read-only ISO/template export (soft-mounted, the one
// deliberate exception).
//
// nfs-images carries the full "images,import,snippets,iso" content list, not
// just "images": the extra types are inert metadata for a plain guest-disk
// lab, but a lab serving as a BOSH CPI target rejects stemcell/qcow2 uploads
// ("storage 'nfs-images' does not support 'import' content") without them —
// a failure that used to surface weeks after attach, at first stemcell
// upload, gated on a manual runbook step.
func nfsAttachTargets(labName string) []nfsAttachTarget {
	return []nfsAttachTarget{
		{
			id:      nfsImagesStorageID,
			export:  fmt.Sprintf("%s/labs/%s/images", nfsExportRoot, labName),
			content: "images,import,snippets,iso",
			options: "vers=4.1",
		},
		{
			id:      nfsBackupStorageID,
			export:  fmt.Sprintf("%s/labs/%s/backup", nfsExportRoot, labName),
			content: "backup",
			options: "vers=4.1",
		},
		{
			id:      nfsSharedIsoStorageID,
			export:  fmt.Sprintf("%s/shared/iso", nfsExportRoot),
			content: "iso,vztmpl",
			options: "vers=4.1,ro,soft",
		},
	}
}

// nfsServerIP returns the NFS server address a lab's nodes mount against:
// sm-0's gateway IP on the lab's own mgmt /24 (multi-node lab plan §8.3 —
// per-lab gateway exposure, so every lab's ACL is scoped to its own
// subnet). Prefers n.Mgmt.Gateway when set and a valid IP address (the
// documented convention: gateway is always ".1") — validated via
// net.ParseIP rather than trusted verbatim, since this value is
// interpolated directly into a `pvesm add nfs --server <value>` remote
// shell command line (nfsAddCommand), and network.mgmt.gateway carries no
// charset/format validation anywhere in the config-load path; falls back to
// deriving ".1" from the mgmt base (labMgmtOffsetIP, which itself always
// returns a validated net.IP-derived string) when Gateway is empty.
func nfsServerIP(n config.LabNetwork) (string, error) {
	if n.Mgmt.Gateway != "" {
		if net.ParseIP(n.Mgmt.Gateway) == nil {
			return "", fmt.Errorf(
				"network.mgmt.gateway %q is not a valid IP address; refusing before it can reach any remote command line",
				n.Mgmt.Gateway)
		}
		return n.Mgmt.Gateway, nil
	}
	return labMgmtOffsetIP(n, 1)
}

// newNfsCmd builds `pmx lab nfs` and its subcommands.
func newNfsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nfs",
		Short: "Attach, inspect, or detach a lab's shared NFS storage",
		Long: "Register (or remove) the shared NFS service's per-lab images/backup exports and " +
			"the cluster-wide read-only ISO/template export as PVE storage on a lab's nested " +
			"cluster (or single-node lab — NFS attach applies regardless of node count), over " +
			"ssh/pvesm against node 0.\n\n" +
			"`attach` also ensures the SERVER-side ZFS dataset/export objects these storage " +
			"entries point at (tank/nfs/labs/<lab>/{images,backup}, plus this lab's mgmt /24 " +
			"membership in the shared tank/nfs/shared/iso export's ro= list) exist on the outer " +
			"PVE node hosting tank/nfs, over ssh via --node/$PMX_NODE/the context default node. " +
			"`detach` never touches any of that server-side state — see its own Long text.",
	}
	cmd.AddCommand(newNfsAttachCmd(), newNfsStatusCmd(), newNfsDetachCmd())
	return cmd
}

// newNfsAttachCmd builds `pmx lab nfs attach <name>`.
func newNfsAttachCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "attach <name>",
		Short: "Register the shared NFS exports as PVE storage on a lab",
		Long: "First ensures this lab's SERVER-side NFS export objects exist on the outer PVE " +
			"node hosting tank/nfs (resolved via --node/$PMX_NODE/the context default node, the " +
			"same resolution `pmx pve node ssh` uses): the tank/nfs/labs/<name>/{images,backup} " +
			"datasets (created if missing, with their recordsize), a ZFS `quota` on their " +
			"tank/nfs/labs/<name> parent (storage.nfs_quota_gb, default 200G), a `rw` sharenfs " +
			"ACL on images/backup scoped to this lab's own mgmt /24, and this lab's mgmt /24 " +
			"membership in the shared tank/nfs/shared/iso export's `ro=` subnet list (inserted, " +
			"never replacing any other lab's entry). This step refuses loudly, naming lab repo " +
			"scripts/60-nfs-service as the remediation, if the shared/iso export itself has no " +
			"usable sharenfs value yet — it can only ensure lab-subnet MEMBERSHIP in an " +
			"already-built list, never construct one from scratch.\n\n" +
			"Also ensures the outer node's host firewall carries an enabled ACCEPT rule for " +
			"2049/tcp (NFS) and 111/tcp (rpcbind — PVE's storage online-probe) scoped to this " +
			"lab's mgmt /24, matched by the same comment key scripts/60-nfs-service's firewall " +
			"group uses, so neither path duplicates the other's rules. A rule that exists but is " +
			"disabled is a loud failure, never silently skipped or force-enabled.\n\n" +
			"Then runs `pvesm add nfs ...` over ssh on node 0 for each of the lab's three fixed " +
			"exports: nfs-images (content images,import,snippets,iso — full BOSH-CPI-target " +
			"parity from day one) and nfs-backup (both rw, hard-mounted, scoped to this lab " +
			"under tank/nfs/labs/<name>), and shared-iso (ro, soft-mounted, the one ISO/template " +
			"export every lab shares). An already-attached entry whose content-type list lacks " +
			"any of these types is widened in place (`pvesm set`), only ever adding types. Every " +
			"step is idempotent: an already-satisfied dataset/property/rule/storage entry is " +
			"skipped, not re-created or re-added, so re-running attach against a partially-built " +
			"lab is safe. Requires the shared NFS service's own host software " +
			"(nfs-kernel-server) to already be built and healthy (lab repo " +
			"scripts/60-nfs-service's nfsd/health groups) — attach provisions this lab's own " +
			"export objects and firewall rules, never the shared NFS host service itself.",
		Example: `  pmx lab nfs attach wayne
  pmx lab nfs attach wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNfsAttach(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the pvesm commands that would run, without executing them")
	return cmd
}

func runNfsAttach(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}
	server, err := nfsServerIP(lab.Network)
	if err != nil {
		return fmt.Errorf("resolve NFS server IP: %w", err)
	}

	serverPlan, err := buildNfsServerEnsurePlan(lab)
	if err != nil {
		return err
	}

	targets := nfsAttachTargets(lab.Name)

	// dry-run never touches deps.Runner (see cluster.go's runClusterInit for
	// the same convention): it previews the server-side ensure phase's steps,
	// the host-firewall rules, and the pvesm add commands this run would
	// issue, without probing which datasets/properties/rules/storage entries
	// already exist, and without resolving the server-side ssh host at all
	// (that resolution is itself a live API call this preview has no need to
	// depend on).
	if dryRun {
		headers := []string{"STEP", "STATUS"}
		rows := nfsServerDryRunSteps(serverPlan)
		rows = append(rows, nfsFirewallDryRunSteps(serverPlan)...)
		for _, t := range targets {
			rows = append(rows, []string{nfsAddCommand(t, server), "would run"})
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
	}

	// The server-side ensure phase runs BEFORE the client-side pvesm-add
	// loop: pvesm add would otherwise reach a live PVE host, fail deep in
	// its own online-probe ("storage 'nfs-images' is not online"), and leave
	// the operator with no indication the SERVER export never existed at
	// all — the exact live failure this phase exists to fix.
	if deps.Node == "" {
		return fmt.Errorf(
			"lab %q: the server-side NFS export-ensure phase needs the outer PVE node hosting "+
				"tank/nfs; pass --node, set $PMX_NODE, or configure a context default node", name)
	}
	if deps.Ctx == nil || deps.Runner == nil {
		return fmt.Errorf(
			"lab %q: the server-side NFS export-ensure phase requires an active pmx context/ssh runner", name)
	}

	serverHost, herr := createDatasetSSHHost(cmd.Context(), deps.API, deps.Node)
	if herr != nil {
		return fmt.Errorf("lab %q: resolve ssh host for NFS server-side ensure phase (node %q): %w", name, deps.Node, herr)
	}

	serverRows, serr := nfsServerEnsure(deps, serverHost, serverPlan)
	if serr != nil {
		return fmt.Errorf("lab %q: server-side NFS export ensure phase: %w", name, serr)
	}

	// The host-firewall rules are ensured BEFORE the pvesm-add loop for the
	// same reason the ZFS phase is: without them, pvesm add reaches a live
	// PVE host whose online-probe then fails (or, with 111/tcp dropped,
	// hangs) against a server that is exporting correctly but unreachable.
	fwRows, ferr := nfsEnsureFirewallRules(cmd.Context(), deps, serverPlan)
	if ferr != nil {
		return fmt.Errorf("lab %q: host firewall ensure phase: %w", name, ferr)
	}
	serverRows = append(serverRows, fwRows...)

	rows, err := nfsEnsureAttached(deps, name, node0IP, server, targets)
	if err != nil {
		return err
	}
	rows = append(serverRows, rows...)
	rows = append(rows, []string{"summary", fmt.Sprintf("lab %q: NFS storage reconciled against server %s.", name, server)})

	headers := []string{"STORAGE", "STATUS"}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}

// nfsEnsureAttached performs `nfs attach`'s actual work — probe each
// target's existing storage config, add it if missing, and widen an existing
// entry's content-type list when it lacks any of the target's types — without
// any cobra/rendering coupling, so `pmx lab scale`'s reconcile step can reuse
// the identical idempotent logic runNfsAttach's RunE wraps. Returns one
// STORAGE/STATUS row per target, in order (no trailing "summary" row —
// callers append their own, since scale.go's summary text differs from
// runNfsAttach's).
func nfsEnsureAttached(deps *cli.Deps, name, node0IP, server string, targets []nfsAttachTarget) ([][]string, error) {
	rows := make([][]string, 0, len(targets))

	for _, t := range targets {
		res, perr := runGuestSSH(deps, node0IP, fmt.Sprintf("pvesh get /storage/%s --output-format json", t.id))
		switch {
		case perr == nil:
			row, uerr := nfsEnsureContent(deps, name, node0IP, t, res.Stdout)
			if uerr != nil {
				return nil, uerr
			}
			rows = append(rows, row)
			continue
		case guestCommandTransportFailed(perr):
			return nil, fmt.Errorf("lab %q: probe storage %q on node 0 (%s): %w", name, t.id, node0IP, perr)
		}

		if _, aerr := runGuestSSH(deps, node0IP, nfsAddCommand(t, server)); aerr != nil {
			return nil, fmt.Errorf("lab %q: attach storage %q on node 0 (%s): %w", name, t.id, node0IP, aerr)
		}
		rows = append(rows, []string{t.id, "attached"})
	}

	return rows, nil
}

// nfsEnsureContent widens an already-attached storage entry's content-type
// list to include every type in t.content, via `pvesm set` on node 0. The
// probe's own pvesh JSON supplies the current list; any type already present
// beyond the target's (an operator addition) is preserved, never removed —
// this step only ever ADDS types. A probe payload without a readable
// `content` field is a skip, not a mutation: with no positive reading of the
// current list, guessing a `pvesm set` could silently clobber operator state.
func nfsEnsureContent(deps *cli.Deps, name, node0IP string, t nfsAttachTarget, probeJSON string) ([]string, error) {
	var cfg struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal([]byte(probeJSON), &cfg); err != nil || cfg.Content == nil {
		return []string{t.id, "skip (already attached)"}, nil
	}

	merged, missing := nfsMergeContent(t.content, *cfg.Content)
	if !missing {
		return []string{t.id, "skip (already attached)"}, nil
	}

	if _, serr := runGuestSSH(deps, node0IP, fmt.Sprintf("pvesm set %s --content %s", t.id, merged)); serr != nil {
		return nil, fmt.Errorf("lab %q: widen content types on storage %q on node 0 (%s): %w", name, t.id, node0IP, serr)
	}
	return []string{t.id, fmt.Sprintf("content widened (%s)", merged)}, nil
}

// nfsMergeContent merges a target content-type list into an existing one.
// missing reports whether existing lacks any of want's types; when it does,
// merged is want's types in want's canonical order followed by existing's
// extra types in their original order (preserved, never dropped). When
// existing already covers want, missing is false and merged is empty — an
// order-only difference is deliberately NOT a mutation (nothing functional
// to fix, and churning a shared entry's config invites needless drift).
func nfsMergeContent(want, existing string) (merged string, missing bool) {
	wantList := strings.Split(want, ",")
	existingList := strings.Split(existing, ",")

	have := make(map[string]bool, len(existingList))
	for _, e := range existingList {
		if e = strings.TrimSpace(e); e != "" {
			have[e] = true
		}
	}

	inWant := make(map[string]bool, len(wantList))
	out := make([]string, 0, len(wantList)+len(existingList))
	for _, w := range wantList {
		inWant[w] = true
		out = append(out, w)
		if !have[w] {
			missing = true
		}
	}
	if !missing {
		return "", false
	}
	for _, e := range existingList {
		if e = strings.TrimSpace(e); e != "" && !inWant[e] {
			out = append(out, e)
			inWant[e] = true // dedupe a repeated extra
		}
	}
	return strings.Join(out, ","), true
}

// nfsAddCommand renders the `pvesm add nfs` command for t against server.
func nfsAddCommand(t nfsAttachTarget, server string) string {
	return fmt.Sprintf("pvesm add nfs %s --server %s --export %s --content %s --options %s",
		t.id, server, t.export, t.content, t.options)
}

// nfsStatusLineRE parses one data row of `pvesm status` plain-text output:
// "<name> <type> <status> <total> <used> <available> <percent>%".
var nfsStatusLineRE = regexp.MustCompile(`(?m)^(\S+)\s+(\S+)\s+(\S+)\s+\d+\s+\d+\s+\d+\s+[\d.]+%\s*$`)

// parsePvesmStatus parses `pvesm status`'s plain-text table into a
// storage-ID -> live-status ("active"/"inactive"/...) map.
func parsePvesmStatus(output string) map[string]string {
	out := make(map[string]string)
	for _, m := range nfsStatusLineRE.FindAllStringSubmatch(output, -1) {
		out[m[1]] = m[3]
	}
	return out
}

// newNfsStatusCmd builds `pmx lab nfs status <name>`.
func newNfsStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show a lab's NFS storage attachment state",
		Long: "Run `pvesm status` over ssh on node 0 and report whether each of the lab's three " +
			"fixed NFS storage entries (nfs-images, nfs-backup, shared-iso) is configured and, " +
			"if so, its live status (active/inactive/...). Read-only: never mutates anything.",
		Example: `  pmx lab nfs status wayne`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNfsStatus(cmd, args[0])
		},
	}
	return cmd
}

func runNfsStatus(cmd *cobra.Command, name string) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLab(cmd, name)
	if err != nil {
		return err
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	res, serr := runGuestSSH(deps, node0IP, "pvesm status")
	if serr != nil && guestCommandTransportFailed(serr) {
		return fmt.Errorf("lab %q: query pvesm status on node 0 (%s): %w", name, node0IP, serr)
	}
	statuses := parsePvesmStatus(res.Stdout)

	headers := []string{"STORAGE", "CONFIGURED", "STATUS"}
	rows := make([][]string, 0, len(nfsAttachTargets(lab.Name)))
	for _, t := range nfsAttachTargets(lab.Name) {
		if status, ok := statuses[t.id]; ok {
			rows = append(rows, []string{t.id, "yes", status})
		} else {
			rows = append(rows, []string{t.id, "no", "n/a"})
		}
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}

// newNfsDetachCmd builds `pmx lab nfs detach <name>`.
func newNfsDetachCmd() *cobra.Command {
	var (
		dryRun bool
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "detach <name>",
		Short: "Remove a lab's NFS storage entries",
		Long: "Run `pvesm remove <id>` over ssh on node 0 for each of the lab's three NFS " +
			"storage entries. Idempotent: an entry that is not configured is skipped, not " +
			"treated as an error. Removal is safe only once no VM disk still references the " +
			"storage — PVE itself refuses to remove a storage entry with in-use content. " +
			"Refuses to run without --yes/-y or an interactive 'y' confirmation.\n\n" +
			"IMPORTANT ASYMMETRY: detach ONLY removes the CLIENT-side pvesm storage entries. " +
			"It never touches the SERVER-side ZFS datasets/exports `attach` ensures " +
			"(tank/nfs/labs/<name>/{images,backup}, their quota/sharenfs properties, or this " +
			"lab's entry in the shared/iso export's ro= list) — those carry this lab's actual " +
			"data and shared-export state, and this command never deletes data implicitly. " +
			"Remove them by hand over ssh (e.g. `zfs destroy -r tank/nfs/labs/<name>`) only once " +
			"you have confirmed the lab's data is no longer needed.",
		Example: `  pmx lab nfs detach wayne --yes
  pmx lab nfs detach wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNfsDetach(cmd, args[0], dryRun, yes)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the pvesm commands that would run, without executing them")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runNfsDetach(cmd *cobra.Command, name string, dryRun, yes bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	targets := nfsAttachTargets(lab.Name)
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.id
	}

	// dry-run never touches deps.Runner (see cluster.go's runClusterInit for
	// the same convention): it previews the pvesm remove commands this run
	// would issue, without probing or prompting.
	if dryRun {
		headers := []string{"STEP", "STATUS"}
		rows := make([][]string, 0, len(targets))
		for _, id := range ids {
			rows = append(rows, []string{fmt.Sprintf("pvesm remove %s", id), "would run"})
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
	}

	if !yes {
		ok, cerr := confirmYesNo(cmd, fmt.Sprintf(
			"Detach NFS storage (%s) from lab %q?", strings.Join(ids, ", "), name))
		if cerr != nil {
			return cerr
		}
		if !ok {
			res := output.Result{Message: "Aborted."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		}
	}

	headers := []string{"STORAGE", "STATUS"}
	rows := make([][]string, 0, len(targets))

	for _, t := range targets {
		_, perr := runGuestSSH(deps, node0IP, fmt.Sprintf("pvesh get /storage/%s --output-format json", t.id))
		switch {
		case perr != nil && guestCommandTransportFailed(perr):
			return fmt.Errorf("lab %q: probe storage %q on node 0 (%s): %w", name, t.id, node0IP, perr)
		case perr != nil:
			rows = append(rows, []string{t.id, "skip (not configured)"})
			continue
		}

		if _, rerr := runGuestSSH(deps, node0IP, fmt.Sprintf("pvesm remove %s", t.id)); rerr != nil {
			return fmt.Errorf("lab %q: remove storage %q on node 0 (%s): %w", name, t.id, node0IP, rerr)
		}
		rows = append(rows, []string{t.id, "removed"})
	}

	rows = append(rows, []string{"summary", fmt.Sprintf("lab %q: NFS storage detached.", name)})
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}
