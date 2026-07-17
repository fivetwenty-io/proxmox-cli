package lab

import (
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
func nfsAttachTargets(labName string) []nfsAttachTarget {
	return []nfsAttachTarget{
		{
			id:      nfsImagesStorageID,
			export:  fmt.Sprintf("%s/labs/%s/images", nfsExportRoot, labName),
			content: "images",
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
			"ssh/pvesm against node 0.",
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
		Long: "Run `pvesm add nfs ...` over ssh on node 0 for each of the lab's three fixed " +
			"exports: nfs-images and nfs-backup (rw, hard-mounted, scoped to this lab under " +
			"tank/nfs/labs/<name>), and shared-iso (ro, soft-mounted, the one ISO/template " +
			"export every lab shares). Idempotent: an already-registered storage entry is " +
			"skipped, not re-added. Requires the shared NFS service to already be built and " +
			"healthy on the host (lab repo scripts/60-nfs-service) — this command only registers " +
			"the client side.",
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

	targets := nfsAttachTargets(lab.Name)

	// dry-run never touches deps.Runner (see cluster.go's runClusterInit for
	// the same convention): it previews the pvesm add commands this run
	// would issue, without probing which storage entries already exist.
	if dryRun {
		headers := []string{"STEP", "STATUS"}
		rows := make([][]string, 0, len(targets))
		for _, t := range targets {
			rows = append(rows, []string{nfsAddCommand(t, server), "would run"})
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
	}

	headers := []string{"STORAGE", "STATUS"}
	rows := make([][]string, 0, len(targets))

	for _, t := range targets {
		_, perr := runGuestSSH(deps, node0IP, fmt.Sprintf("pvesh get /storage/%s --output-format json", t.id))
		switch {
		case perr == nil:
			rows = append(rows, []string{t.id, "skip (already attached)"})
			continue
		case guestCommandTransportFailed(perr):
			return fmt.Errorf("lab %q: probe storage %q on node 0 (%s): %w", name, t.id, node0IP, perr)
		}

		if _, aerr := runGuestSSH(deps, node0IP, nfsAddCommand(t, server)); aerr != nil {
			return fmt.Errorf("lab %q: attach storage %q on node 0 (%s): %w", name, t.id, node0IP, aerr)
		}
		rows = append(rows, []string{t.id, "attached"})
	}

	rows = append(rows, []string{"summary", fmt.Sprintf("lab %q: NFS storage reconciled against server %s.", name, server)})
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
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
			"Refuses to run without --yes/-y or an interactive 'y' confirmation.",
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
