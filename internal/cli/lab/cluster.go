package lab

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// clusterJoinPollAttempts and clusterJoinPollInterval bound how long `pmx
// lab cluster join` waits, after issuing `pvecm add`, for node 0 to report
// the newly-joined node's vote counted and every corosync link up (multi-
// node lab plan §6.2: "wait until pvecm status on node 0 shows
// expected=total=i+1 ... before starting the next join"). 30 attempts at 2
// seconds apart bounds the wait at one minute — generous for corosync
// convergence on a single physical host, where link latency is microseconds,
// while still failing a genuinely stuck join within a reasonable command
// timeout rather than hanging forever.
const (
	clusterJoinPollAttempts = 30
	clusterJoinPollInterval = 2 * time.Second
)

// clusterPollSleep is the sleep function clusterWaitForJoin calls between
// poll attempts. Tests override this package variable with a no-op so the
// polling loop's retry logic can be exercised without a real-time test
// taking up to a minute to run.
var clusterPollSleep = time.Sleep

// newClusterCmd builds `pmx lab cluster` and its subcommands.
func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Create and join a lab's nested PVE cluster",
		Long: "Form the nested Proxmox VE cluster inside a multi-node lab: `init` creates the " +
			"cluster on node 0, `join` adds one more node at a time (serialized — never run two " +
			"joins concurrently against the same lab), and `status` reports quorum and corosync " +
			"link health. Every mutating verb runs entirely over ssh into the lab guest's own " +
			"mgmt IP, never against the outer Proxmox VE API.",
	}
	cmd.AddCommand(newClusterInitCmd(), newClusterJoinCmd(), newClusterStatusCmd())
	return cmd
}

// newClusterInitCmd builds `pmx lab cluster init <name>`.
func newClusterInitCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Create a lab's nested PVE cluster on node 0",
		Long: "Run `pvecm create <lab-name> --link0 <node-0-mgmt-ip>` over ssh on the lab's node " +
			"0, then verify it reports a quorate 1-of-1-vote cluster. Idempotent: if node 0 " +
			"already reports a cluster with this lab's name, the command reports it as already " +
			"initialized and does nothing further. Requires topology.nodes >= 2 — a single-node " +
			"lab has no cluster to create.",
		Example: `  pmx lab cluster init wayne
  pmx lab cluster init wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterInit(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the ssh commands that would run, without executing them")
	return cmd
}

func runClusterInit(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	numNodes := config.EffectiveTopologyNodes(lab.Topology)
	if numNodes < 2 {
		return fmt.Errorf(
			"lab %q has topology.nodes=%d; a single-node lab has no cluster to create", name, numNodes)
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	createCmd := fmt.Sprintf("pvecm create %s --link0 %s", lab.Name, node0IP)

	// dry-run never touches deps.Runner at all (matching quota.go's
	// established ssh-based-verb precedent, quota_test.go's
	// TestQuotaSet_DryRun_RecordsNoCallAndPrintsCommand): it always previews
	// the literal command this run would issue, without probing live remote
	// state first, so a preview never itself makes a network call.
	if dryRun {
		res := output.Result{Message: fmt.Sprintf("[dry-run] would run on node 0 (%s): %s", node0IP, createCmd)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	message, err := ensureClusterInit(deps, lab, name, node0IP)
	if err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: message}, deps.Format)
}

// ensureClusterInit performs `cluster init`'s actual work — probe, create,
// verify — without any cobra/rendering coupling, so `pmx lab scale`'s grow
// path can reuse the identical idempotent logic runClusterInit's RunE
// wraps. Every returned message/error string is already fully formed
// (including the "lab %q:" prefix where runClusterInit's original inline
// version had one, and without it where it did not), so callers only need
// to render message on success or propagate err on failure — see
// runClusterInit for the byte-for-byte equivalence this preserves.
func ensureClusterInit(deps *cli.Deps, lab *config.Lab, name, node0IP string) (message string, err error) {
	createCmd := fmt.Sprintf("pvecm create %s --link0 %s", lab.Name, node0IP)

	probe, perr := runGuestSSH(deps, node0IP, "pvecm status")
	if perr != nil && guestCommandTransportFailed(perr) {
		return "", fmt.Errorf("probe node 0 (%s) cluster state: %w", node0IP, perr)
	}
	st := parsePvecmStatus(probe.Stdout)

	if st.Clustered && st.ClusterName == lab.Name {
		return fmt.Sprintf(
			"lab %q: node 0 (%s) is already clustered as %q; nothing to do.", name, node0IP, st.ClusterName), nil
	}
	if st.Clustered && st.ClusterName != lab.Name {
		return "", fmt.Errorf(
			"lab %q: node 0 (%s) is already part of a DIFFERENT cluster (%q); refusing to overwrite it",
			name, node0IP, st.ClusterName)
	}

	if _, err := runGuestSSH(deps, node0IP, createCmd); err != nil {
		return "", fmt.Errorf("create cluster %q on node 0 (%s): %w", lab.Name, node0IP, err)
	}

	verify, verr := runGuestSSH(deps, node0IP, "pvecm status")
	if verr != nil {
		return "", fmt.Errorf("verify cluster %q after create: %w", lab.Name, verr)
	}
	vst := parsePvecmStatus(verify.Stdout)
	if !vst.Clustered || !vst.Quorate || vst.ExpectedVotes != 1 || vst.TotalVotes != 1 {
		return "", fmt.Errorf(
			"lab %q: cluster create ran but node 0 does not report a quorate 1-of-1-vote cluster "+
				"(clustered=%v quorate=%v expected=%d total=%d)",
			name, vst.Clustered, vst.Quorate, vst.ExpectedVotes, vst.TotalVotes)
	}

	return fmt.Sprintf(
		"lab %q: cluster %q created on node 0 (%s), quorate 1/1 votes.", name, lab.Name, node0IP), nil
}

// newClusterJoinCmd builds `pmx lab cluster join <name> --node <i>`.
func newClusterJoinCmd() *cobra.Command {
	var (
		nodeFlag string
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "join <name>",
		Short: "Join one more node to a lab's nested PVE cluster",
		Long: "Run `pvecm add <node-0-mgmt-ip> --link0 <node-i-mgmt-ip> --use_ssh` over ssh on " +
			"the joining node (--node, required, 1-4 — node 0 is created, never joined), then " +
			"block until node 0 reports the expected vote count reached and every corosync link " +
			"connected (multi-node lab plan §6.2). Idempotent: if the target node already reports " +
			"membership in this lab's cluster, the command reports it as already joined and does " +
			"nothing further. Refuses to join a node that hosts any VM or container (`qm list`/" +
			"`pct list` on the joining node) — plan §6.2: only node 0 may ever hold guests before " +
			"clustering; a guest-hosting node may create a cluster but must never join one. Nodes " +
			"must be joined one at a time, in index order (1, then 2, then 3, ...) — never run two " +
			"joins for the same lab concurrently, and never join node i before node i-1 has " +
			"finished joining and reached quorum.",
		Example: `  pmx lab cluster join wayne --node 1
  pmx lab cluster join wayne --node 1 --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterJoin(cmd, args[0], nodeFlag, dryRun)
		},
	}
	cmd.Flags().StringVar(&nodeFlag, "node", "", "node index to join (1-4); required")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the ssh commands that would run, without executing them")
	return cmd
}

func runClusterJoin(cmd *cobra.Command, name, nodeFlag string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	if nodeFlag == "" {
		return fmt.Errorf("--node is required (1-%d)", maxLabNodeIndex)
	}
	idx, err := strconv.Atoi(nodeFlag)
	if err != nil {
		return fmt.Errorf("--node %q is not a valid node index: %w", nodeFlag, err)
	}

	numNodes := config.EffectiveTopologyNodes(lab.Topology)
	if idx < 1 || idx >= numNodes {
		return fmt.Errorf(
			"--node %d is out of range for lab %q's %d-node topology; joinable indexes are 1-%d",
			idx, name, numNodes, numNodes-1)
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}
	nodeIP, err := labNodeMgmtIP(lab.Network, idx)
	if err != nil {
		return fmt.Errorf("resolve node %d mgmt IP: %w", idx, err)
	}

	joinCmd := fmt.Sprintf("pvecm add %s --link0 %s --use_ssh", node0IP, nodeIP)

	// dry-run never touches deps.Runner: see runClusterInit's matching
	// comment for why this mirrors quota.go's established precedent instead
	// of buildCreatePlan's API-GET-based preview.
	if dryRun {
		res := output.Result{Message: fmt.Sprintf("[dry-run] would run on node %d (%s): %s", idx, nodeIP, joinCmd)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	message, err := ensureClusterJoin(deps, lab, name, idx, node0IP, nodeIP)
	if err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: message}, deps.Format)
}

// ensureClusterJoin performs `cluster join`'s actual work — probe, guest-
// free check, pvecm add, wait-for-quorum — without any cobra/rendering
// coupling, so `pmx lab scale`'s grow path can reuse the identical
// idempotent logic runClusterJoin's RunE wraps. Every returned message/
// error string is already fully formed exactly as runClusterJoin's original
// inline version produced it — see runClusterJoin for the byte-for-byte
// equivalence this preserves.
func ensureClusterJoin(deps *cli.Deps, lab *config.Lab, name string, idx int, node0IP, nodeIP string) (message string, err error) {
	joinCmd := fmt.Sprintf("pvecm add %s --link0 %s --use_ssh", node0IP, nodeIP)

	probe, perr := runGuestSSH(deps, nodeIP, "pvecm status")
	if perr != nil && guestCommandTransportFailed(perr) {
		return "", fmt.Errorf("probe node %d (%s) cluster state: %w", idx, nodeIP, perr)
	}
	st := parsePvecmStatus(probe.Stdout)
	if st.Clustered && st.ClusterName == lab.Name {
		return fmt.Sprintf(
			"lab %q: node %d (%s) is already joined to cluster %q; nothing to do.",
			name, idx, nodeIP, st.ClusterName), nil
	}
	if st.Clustered && st.ClusterName != lab.Name {
		return "", fmt.Errorf(
			"lab %q: node %d (%s) is already part of a DIFFERENT cluster (%q); refusing to join it into %q",
			name, idx, nodeIP, st.ClusterName, lab.Name)
	}

	// Multi-node lab plan §6.2 step 1: "Joining nodes must be guest-free;
	// only node 0 may ever hold guests pre-cluster (a node with guests can
	// create but not join)". Refuse before pvecm add ever runs, rather than
	// letting a guest-hosting node join and silently violate the invariant.
	if err := clusterEnsureGuestFree(deps, idx, nodeIP); err != nil {
		return "", fmt.Errorf("lab %q: %w", name, err)
	}

	if _, err := runGuestSSH(deps, nodeIP, joinCmd); err != nil {
		return "", fmt.Errorf("join node %d (%s) to cluster %q: %w", idx, nodeIP, lab.Name, err)
	}

	if err := clusterWaitForJoin(deps, node0IP, idx+1); err != nil {
		return "", fmt.Errorf("lab %q: node %d joined, but %w", name, idx, err)
	}

	return fmt.Sprintf(
		"lab %q: node %d (%s) joined cluster %q; %d/%d votes quorate, all corosync links up.",
		name, idx, nodeIP, lab.Name, idx+1, idx+1), nil
}

// clusterEnsureGuestFree refuses to proceed if node idx (at nodeIP) hosts
// any qemu VM or LXC container, per multi-node lab plan §6.2 step 1: a node
// with guests may create a cluster (node 0 only) but must never join one. It
// runs `qm list` and `pct list` over ssh and counts data rows via
// clusterCountGuestListRows. A non-zero exit from either command is treated
// as a real failure (unlike this package's other idempotency probes,
// `qm list`/`pct list` are expected to always succeed on a healthy node —
// there is no "not found" interpretation for a plain listing command), not
// as "no guests".
func clusterEnsureGuestFree(deps *cli.Deps, idx int, nodeIP string) error {
	qmRes, err := runGuestSSH(deps, nodeIP, "qm list")
	if err != nil {
		return fmt.Errorf("list VMs on node %d (%s) before join: %w", idx, nodeIP, err)
	}
	if n := clusterCountGuestListRows(qmRes.Stdout); n > 0 {
		return fmt.Errorf(
			"node %d (%s) hosts %d VM(s); joining nodes must be guest-free (multi-node lab plan §6.2) — "+
				"evacuate or destroy them before joining", idx, nodeIP, n)
	}

	pctRes, err := runGuestSSH(deps, nodeIP, "pct list")
	if err != nil {
		return fmt.Errorf("list containers on node %d (%s) before join: %w", idx, nodeIP, err)
	}
	if n := clusterCountGuestListRows(pctRes.Stdout); n > 0 {
		return fmt.Errorf(
			"node %d (%s) hosts %d container(s); joining nodes must be guest-free (multi-node lab plan §6.2) — "+
				"evacuate or destroy them before joining", idx, nodeIP, n)
	}

	return nil
}

// clusterCountGuestListRows counts the data rows of a `qm list`/`pct list`
// plain-text table: every non-blank line after the first (header) line.
// Both commands always print a header line even with zero guests, so an
// empty node's output (header-only, or entirely empty in a degenerate case)
// counts as zero regardless of which of those two shapes it takes.
func clusterCountGuestListRows(output string) int {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	count := 0
	for i, line := range lines {
		if i == 0 {
			continue // header row
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		count++
	}
	return count
}

// clusterWaitForJoin polls node 0 (via pvecm status and corosync-cfgtool -s)
// until it reports exactly wantVotes expected and total votes, quorate, and
// every corosync link connected — or clusterJoinPollAttempts is exhausted,
// in which case it returns a descriptive error rather than hanging forever.
func clusterWaitForJoin(deps *cli.Deps, node0IP string, wantVotes int) error {
	var lastErr error

	for attempt := 1; attempt <= clusterJoinPollAttempts; attempt++ {
		statusRes, serr := runGuestSSH(deps, node0IP, "pvecm status")
		if serr != nil {
			lastErr = fmt.Errorf("query node 0 (%s) pvecm status: %w", node0IP, serr)
			clusterSleepIfMoreAttempts(attempt)
			continue
		}
		st := parsePvecmStatus(statusRes.Stdout)

		linksRes, lerr := runGuestSSH(deps, node0IP, "corosync-cfgtool -s")
		if lerr != nil {
			lastErr = fmt.Errorf("query node 0 (%s) corosync-cfgtool -s: %w", node0IP, lerr)
			clusterSleepIfMoreAttempts(attempt)
			continue
		}
		allUp, linkStatuses := parseCorosyncLinks(linksRes.Stdout)

		if st.Quorate && st.ExpectedVotes == wantVotes && st.TotalVotes == wantVotes && allUp {
			return nil
		}

		lastErr = fmt.Errorf(
			"node 0 not yet at %d/%d quorate votes with all corosync links up "+
				"(quorate=%v expected=%d total=%d links=%v)",
			wantVotes, wantVotes, st.Quorate, st.ExpectedVotes, st.TotalVotes, linkStatuses)
		clusterSleepIfMoreAttempts(attempt)
	}

	return fmt.Errorf("timed out after %d attempts waiting for quorum: %w", clusterJoinPollAttempts, lastErr)
}

// clusterSleepIfMoreAttempts calls clusterPollSleep only when another poll
// attempt will actually follow (attempt < clusterJoinPollAttempts), so the
// loop never sleeps one extra interval after its final, still-failing
// attempt before returning the timeout error.
func clusterSleepIfMoreAttempts(attempt int) {
	if attempt < clusterJoinPollAttempts {
		clusterPollSleep(clusterJoinPollInterval)
	}
}

// newClusterStatusCmd builds `pmx lab cluster status <name>`.
func newClusterStatusCmd() *cobra.Command {
	var nodeFlag string

	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show a lab's nested cluster quorum and corosync link state",
		Long: "Run `pvecm status` and `corosync-cfgtool -s` over ssh against one node (node 0 " +
			"by default; pass --node to target a different index) and report cluster " +
			"membership, quorum, vote counts, QDevice presence, and whether every corosync link " +
			"is connected. Read-only: never mutates anything.",
		Example: `  pmx lab cluster status wayne
  pmx lab cluster status wayne --node 1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterStatus(cmd, args[0], nodeFlag)
		},
	}
	cmd.Flags().StringVar(&nodeFlag, "node", "0", "node index to query (0-4)")
	return cmd
}

func runClusterStatus(cmd *cobra.Command, name, nodeFlag string) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLab(cmd, name)
	if err != nil {
		return err
	}

	idx, err := strconv.Atoi(nodeFlag)
	if err != nil {
		return fmt.Errorf("--node %q is not a valid node index: %w", nodeFlag, err)
	}
	numNodes := config.EffectiveTopologyNodes(lab.Topology)
	if idx < 0 || idx >= numNodes {
		return fmt.Errorf("--node %d is out of range for lab %q's %d-node topology (0-%d)", idx, name, numNodes, numNodes-1)
	}

	nodeIP, err := labNodeMgmtIP(lab.Network, idx)
	if err != nil {
		return fmt.Errorf("resolve node %d mgmt IP: %w", idx, err)
	}

	statusRes, serr := runGuestSSH(deps, nodeIP, "pvecm status")
	if serr != nil && guestCommandTransportFailed(serr) {
		return fmt.Errorf("query node %d (%s) pvecm status: %w", idx, nodeIP, serr)
	}
	st := parsePvecmStatus(statusRes.Stdout)

	var linkSummary string
	if st.Clustered {
		linksRes, lerr := runGuestSSH(deps, nodeIP, "corosync-cfgtool -s")
		if lerr != nil && guestCommandTransportFailed(lerr) {
			return fmt.Errorf("query node %d (%s) corosync-cfgtool -s: %w", idx, nodeIP, lerr)
		}
		allUp, statuses := parseCorosyncLinks(linksRes.Stdout)
		if allUp {
			linkSummary = "all links connected"
		} else if len(statuses) == 0 {
			linkSummary = "no link status parsed"
		} else {
			linkSummary = fmt.Sprintf("degraded: %s", strings.Join(statuses, ", "))
		}
	} else {
		linkSummary = "n/a (not clustered)"
	}

	headers := []string{"FIELD", "VALUE"}
	rows := [][]string{
		{"lab", name},
		{"queried node", fmt.Sprintf("%d (%s)", idx, nodeIP)},
		{"clustered", strconv.FormatBool(st.Clustered)},
		{"cluster name", st.ClusterName},
		{"quorate", strconv.FormatBool(st.Quorate)},
		{"expected votes", strconv.Itoa(st.ExpectedVotes)},
		{"total votes", strconv.Itoa(st.TotalVotes)},
		{"qdevice registered", strconv.FormatBool(st.HasQdevice)},
		{"corosync links", linkSummary},
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}
