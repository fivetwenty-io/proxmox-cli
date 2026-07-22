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
//
// Multi-cluster-as-multi-lab: a deployment that wants two (or more)
// independent nested PVE clusters — e.g. two HA clusters realized as two
// BOSH availability zones — gets there with zero code change to this file.
// Each nested cluster is simply a separate `pmx lab` config entry (its own
// name, vnet, mgmt CIDR, and topology); `cluster init`/`cluster join`/
// `cluster status` always resolve every value — the `pvecm create <name>
// --link0 <ip>` cluster name, the mgmt IPs passed to `pvecm add`, the
// corosync poll target — from the single lab argument the command was
// invoked against (see runClusterInit/runClusterJoin/runClusterStatus:
// every value is derived from resolveLab/resolveLabForMutate's return,
// nothing package-level or shared is threaded between two labs). Running these
// commands against two differently-named labs therefore always targets two
// distinct clusters with distinct cluster names and mgmt IPs, never leaking
// state from one lab's cluster into the other's — see
// TestCluster_TwoIndependentLabs.
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
	if note := refreshLabContextAfterClusterInit(cmd, deps, lab); note != "" {
		message += "; " + note
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: message}, deps.Format)
}

// refreshLabContextAfterClusterInit refreshes the lab's pmx context after
// `pvecm create`, which reissues the node's API certificate under a new
// cluster CA and so invalidates any previously pinned TLS fingerprint. It runs
// only when a lab-<name> context already exists (a --no-context lab is left
// untouched), is best-effort, and returns a short note for the command
// message; it never fails cluster init, which has already succeeded.
func refreshLabContextAfterClusterInit(cmd *cobra.Command, deps *cli.Deps, lab *config.Lab) string {
	ctxName := labContextName(lab.Name)
	if deps.Cfg == nil || deps.Cfg.Contexts[ctxName] == nil {
		return ""
	}
	if _, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false}); err != nil {
		return fmt.Sprintf("⚠ context %s refresh failed: %v; run 'pmx lab context sync %s'", ctxName, err, lab.Name)
	}
	return fmt.Sprintf("context %s refreshed", ctxName)
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
			"clustering; a guest-hosting node may create a cluster but must never join one. Before " +
			"`pvecm add` runs, non-interactive root ssh trust from the joining node to node 0 is " +
			"seeded and verified end to end (root keypair, pushed public key, accepted host key) — " +
			"`pvecm add`'s own ssh-fallback join path relies on that trust already existing and " +
			"fails silently (reporting success) on a fresh node pair without it. After `pvecm add` " +
			"returns, the joining node is re-probed to confirm it actually joined before waiting for " +
			"quorum, since `pvecm add`'s exit code alone cannot be trusted. Nodes must be joined one " +
			"at a time, in index order (1, then 2, then 3, ...) — never run two joins for the same " +
			"lab concurrently, and never join node i before node i-1 has finished joining and " +
			"reached quorum.",
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
		res := output.Result{Message: fmt.Sprintf(
			"[dry-run] would seed root ssh trust from node %d (%s) to node 0 (%s) (ensure root keypair, "+
				"push its public key into node 0's authorized_keys, accept node 0's host key) then run on "+
				"node %d (%s): %s",
			idx, nodeIP, node0IP, idx, nodeIP, joinCmd)}
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

	// Seed and verify non-interactive root ssh trust from the joining node to
	// node 0 BEFORE pvecm add ever runs — see clusterSeedJoinTrust's doc
	// comment for the live failure mode this works around.
	if err := clusterSeedJoinTrust(deps, idx, node0IP, nodeIP); err != nil {
		return "", fmt.Errorf("lab %q: %w", name, err)
	}

	// pvecm add's own exit code is not trustworthy evidence that the join
	// actually happened (see clusterSeedJoinTrust's doc comment): a transport
	// failure (ssh to the joining node itself broken) still aborts here, but
	// any other error — including "succeeded" with a warning on stderr — is
	// deliberately let through to the join-verification probe below, which is
	// the actual source of truth.
	joinRes, joinErr := runGuestSSH(deps, nodeIP, joinCmd)
	if joinErr != nil && guestCommandTransportFailed(joinErr) {
		return "", fmt.Errorf("join node %d (%s) to cluster %q: %w", idx, nodeIP, lab.Name, joinErr)
	}

	// Re-probe the joining node itself and require it to actually report
	// membership in THIS lab's cluster before ever waiting on node 0's
	// quorum — pvecm add exiting 0 is not sufficient evidence (M-JOINTRUST:
	// the live pve-cpi-az2 failure this guards against left no
	// corosync.conf at all on the joining node despite `pvecm add` exiting
	// 0 and printing "node 1 joined").
	verifyRes, verifyErr := runGuestSSH(deps, nodeIP, "pvecm status")
	if verifyErr != nil && guestCommandTransportFailed(verifyErr) {
		return "", fmt.Errorf("lab %q: verify node %d (%s) joined cluster: %w", name, idx, nodeIP, verifyErr)
	}
	vst := parsePvecmStatus(verifyRes.Stdout)
	if !vst.Clustered || vst.ClusterName != lab.Name {
		return "", fmt.Errorf(
			"lab %q: node %d (%s) did not actually join cluster %q — `pvecm add` exited %d but a "+
				"follow-up `pvecm status` on the joining node reports clustered=%v cluster=%q; "+
				"pvecm add output — stdout: %q stderr: %q",
			name, idx, nodeIP, lab.Name, joinRes.ExitCode, vst.Clustered, vst.ClusterName,
			strings.TrimSpace(joinRes.Stdout), strings.TrimSpace(joinRes.Stderr))
	}

	if err := clusterWaitForJoin(deps, node0IP, idx+1); err != nil {
		return "", fmt.Errorf("lab %q: node %d joined, but %w", name, idx, err)
	}

	return fmt.Sprintf(
		"lab %q: node %d (%s) joined cluster %q; %d/%d votes quorate, all corosync links up.",
		name, idx, nodeIP, lab.Name, idx+1, idx+1), nil
}

// clusterEnsureRootKeypairCmd is the remote command clusterSeedJoinTrust runs
// on the joining node to ensure a root ssh keypair exists before its public
// key can be pushed to node 0. This mirrors what PVE::Cluster::Setup::
// setup_ssh_keys (invoked internally by `pvecm add`/`pvecm create`) would do
// anyway on first use — running it here, before pvecm add, only moves the
// timing earlier so the public key is available for the trust-seeding steps
// that follow. Idempotent: a no-op if the keypair already exists (e.g. a
// re-run after a partially-failed prior join attempt).
const clusterEnsureRootKeypairCmd = "test -f /root/.ssh/id_rsa || ssh-keygen -q -t rsa -b 4096 -N '' -f /root/.ssh/id_rsa"

// clusterSeedJoinTrust seeds and verifies non-interactive root ssh trust from
// the joining node (idx, at nodeIP) to node 0 (node0IP) BEFORE `pvecm add`
// ever runs.
//
// Root cause this works around (live pve-cpi-az2 join failure, reproduced
// against PVE 9.2, /usr/share/perl5/PVE/CLI/pvecm.pm): `pvecm add`'s
// ssh-fallback join path runs `ssh-copy-id -i /root/.ssh/id_rsa
// root@<node0>` and then `ssh <node0> -o BatchMode=yes pvecm apiver` /
// `pvecm addnode`, all non-interactively. On a freshly-provisioned node pair
// neither node i's root key nor node 0's host key is yet trusted by the
// other, so `ssh-copy-id` fails ("unable to copy ssh ID: exit code 1") and
// every ssh call downstream of it fails too — while `pvecm add` itself still
// exits 0 and reports success, leaving no corosync.conf on the joining node
// at all. Seeding that exact trust relationship here, and PROVING it works
// end to end with a real non-interactive ssh call before pvecm add ever
// runs, closes that gap; ensureClusterJoin's post-add re-probe (see its own
// comment) is the second half of this workaround, for the case where trust
// was fine but the join still silently failed for some other reason.
//
// Every step runs over the existing runGuestSSH seam (operator ssh into the
// lab guest, using the key pmx's own answer-file provisioning already baked
// into every lab guest's root account — never a new key of pmx's own).
// Any failure aborts immediately, before pvecm add ever runs, with an error
// naming both nodes and the specific step that failed.
func clusterSeedJoinTrust(deps *cli.Deps, idx int, node0IP, nodeIP string) error {
	if _, err := runGuestSSH(deps, nodeIP, clusterEnsureRootKeypairCmd); err != nil {
		return fmt.Errorf("node %d (%s): ensure root ssh keypair before joining node 0 (%s): %w", idx, nodeIP, node0IP, err)
	}

	pubRes, err := runGuestSSH(deps, nodeIP, "cat /root/.ssh/id_rsa.pub")
	if err != nil {
		return fmt.Errorf("node %d (%s): read root ssh public key before joining node 0 (%s): %w", idx, nodeIP, node0IP, err)
	}
	pubKey := strings.TrimSpace(pubRes.Stdout)
	if pubKey == "" {
		return fmt.Errorf(
			"node %d (%s): root ssh public key (/root/.ssh/id_rsa.pub) read back empty; cannot seed trust on node 0 (%s)",
			idx, nodeIP, node0IP)
	}
	// The key travels to node 0 as a single-quoted shell argument (appendCmd
	// below); a literal single quote inside it would break out of that
	// quoting. A valid OpenSSH public key line is a key-type token, base64
	// key material, and an optional comment — none of that alphabet ever
	// legitimately contains a single quote, so seeing one here means
	// something upstream (e.g. what `cat` printed) is not actually a public
	// key, and it is safer to abort than to attempt (and likely mis-quote)
	// the append.
	if strings.Contains(pubKey, "'") {
		return fmt.Errorf(
			"node %d (%s): root ssh public key contains a single quote character, refusing to seed trust on node 0 (%s)",
			idx, nodeIP, node0IP)
	}

	// grep -qF against a fixed string makes the append idempotent (a re-run
	// after a partially-failed prior join attempt must not double-append the
	// same key); mkdir/touch/chmod ensure the directory and file exist with
	// the permissions ssh itself requires (0700/0600) before grep or the
	// append ever run, on a node 0 whose .ssh directory may not have existed
	// yet at all.
	appendCmd := fmt.Sprintf(
		"mkdir -p /root/.ssh && chmod 700 /root/.ssh && touch /root/.ssh/authorized_keys && "+
			"chmod 600 /root/.ssh/authorized_keys && grep -qF '%s' /root/.ssh/authorized_keys || "+
			"echo '%s' >> /root/.ssh/authorized_keys",
		pubKey, pubKey)
	if _, err := runGuestSSH(deps, node0IP, appendCmd); err != nil {
		return fmt.Errorf(
			"node 0 (%s): append node %d's (%s) root ssh public key to authorized_keys: %w", node0IP, idx, nodeIP, err)
	}

	// The definitive proof the seeding worked: a real non-interactive ssh
	// call from node i to node 0, run exactly the way pvecm add's own
	// ssh-fallback path will run it. This both records node 0's host key in
	// node i's known_hosts (StrictHostKeyChecking=accept-new) and proves key
	// auth actually works — if this fails, pvecm add would fail identically
	// (and silently, per the doc comment above), so abort here instead.
	preflightCmd := fmt.Sprintf(
		"ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@%s pvecm apiver", node0IP)
	if _, err := runGuestSSH(deps, nodeIP, preflightCmd); err != nil {
		return fmt.Errorf(
			"node %d (%s): non-interactive ssh trust to node 0 (%s) still failing after seeding "+
				"(key pushed, but the ssh call itself did not succeed): %w",
			idx, nodeIP, node0IP, err)
	}

	return nil
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
