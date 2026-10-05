package lab

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// sshUnreachableExitCode is the exit status ssh itself returns when it cannot
// establish the connection (refused, timed out, no route, authentication
// failure). A remote command that ran and failed returns its own status
// instead, so this value is how a probe tells a dead node from a live one.
const sshUnreachableExitCode = 255

// clusterExpectedProbeCommand is the remote command the node auto-pick runs
// to find out whether a node answers over ssh. It does no work on the node.
const clusterExpectedProbeCommand = "true"

// newClusterExpectedCmd builds `pmx lab cluster expected <name> <votes>`.
func newClusterExpectedCmd() *cobra.Command {
	var (
		nodeFlag string
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "expected <name> <votes>",
		Short: "Lower a lab cluster's expected vote count on a surviving node",
		Long: "Run `pvecm expected <votes>` over ssh on one node of the lab's nested cluster, so " +
			"a lone surviving node regains quorum and `/etc/pve` accepts writes again. This is " +
			"the emergency step of the lab cluster runbook, used when the other nodes are gone " +
			"and the surviving node has lost quorum. The Proxmox VE API has no endpoint for it, " +
			"so the command goes over ssh into the node's own mgmt IP.\n\n" +
			"<votes> must be an integer of 1 or more. The command can only lower the count: " +
			"`pvecm expected` cannot raise it, so a value above the node's current expected " +
			"votes is refused before anything runs. Corosync resets the expected votes by " +
			"itself once the other nodes rejoin and quorum returns, so the change does not " +
			"need to be undone by hand.\n\n" +
			"Pass --node to pick the node. Without it, the command probes the lab's nodes in " +
			"index order with a cheap ssh command and uses the first one that answers, since " +
			"node 0 may be the node that died. A node that fails to connect (ssh exit status " +
			"255) counts as unreachable. If none answers, the command fails and names every " +
			"node it tried.\n\n" +
			"On the chosen node the command reads `pvecm status` first and refuses a node " +
			"that is not clustered, runs `pvecm expected`, reads `pvecm status` again, and " +
			"reports the node, its quorate state, and its expected and total votes.\n\n" +
			"With --dry-run the command still probes for reachability and reads `pvecm " +
			"status` to run the same checks, but it skips the mutating `pvecm expected` and " +
			"prints the node and command it would have used.",
		Example: `  pmx lab cluster expected wayne 1
  pmx lab cluster expected wayne 2 --node 1
  pmx lab cluster expected wayne 1 --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterExpected(cmd, args[0], args[1], nodeFlag, dryRun)
		},
	}
	cmd.Flags().StringVar(&nodeFlag, "node", "",
		"node index to run on (0-4); default is the first node that answers over ssh")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"probe and check, then print the node and command that would run, without running `pvecm expected`")
	return cmd
}

// parseExpectedVotes parses the <votes> argument: a base-10 integer of 1 or
// more. Zero, negative numbers, and anything non-numeric are rejected.
func parseExpectedVotes(arg string) (int, error) {
	votes, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("<votes> %q is not an integer; give a whole number of 1 or more", arg)
	}
	if votes < 1 {
		return 0, fmt.Errorf("<votes> must be 1 or more, got %d", votes)
	}
	return votes, nil
}

func runClusterExpected(cmd *cobra.Command, name, votesArg, nodeFlag string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	votes, err := parseExpectedVotes(votesArg)
	if err != nil {
		return err
	}

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	numNodes := config.EffectiveTopologyNodes(lab.Topology)

	idx, nodeIP, err := clusterExpectedPickNode(deps, lab, name, nodeFlag, numNodes, dryRun)
	if err != nil {
		return err
	}

	before, err := clusterExpectedReadStatus(deps, idx, nodeIP)
	if err != nil {
		return err
	}
	if !before.Clustered {
		return fmt.Errorf(
			"node %d (%s) of lab %q is not part of a cluster, so there is no expected vote count to lower",
			idx, nodeIP, name)
	}
	if before.ExpectedVotes < 1 {
		return fmt.Errorf("could not read the expected votes from `pvecm status` on node %d (%s) of lab %q",
			idx, nodeIP, name)
	}
	if votes > before.ExpectedVotes {
		return fmt.Errorf(
			"refusing to set expected votes to %d on node %d (%s): it currently expects %d, and "+
				"`pvecm expected` can only lower the count, never raise it",
			votes, idx, nodeIP, before.ExpectedVotes)
	}

	expectedCmd := fmt.Sprintf("pvecm expected %d", votes)

	if dryRun {
		msg := fmt.Sprintf("[dry-run] would run on node %d (%s): %s (currently expects %d votes)",
			idx, nodeIP, expectedCmd, before.ExpectedVotes)
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
	}

	if _, err := runGuestSSH(deps, nodeIP, expectedCmd); err != nil {
		return fmt.Errorf("run %q on node %d (%s): %w", expectedCmd, idx, nodeIP, err)
	}

	after, err := clusterExpectedReadStatus(deps, idx, nodeIP)
	if err != nil {
		return fmt.Errorf("%q ran, but reading the result back failed: %w", expectedCmd, err)
	}
	if !after.Clustered {
		return fmt.Errorf("%q ran on node %d (%s), but the node no longer reports a cluster", expectedCmd, idx, nodeIP)
	}
	if after.ExpectedVotes != votes {
		// Corosync recomputes the expected votes when nodes rejoin, so a
		// differing read-back is not a failure of the command itself.
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: node %d (%s) now reports %d expected votes instead of the requested %d; "+
				"other nodes may have rejoined\n",
			idx, nodeIP, after.ExpectedVotes, votes)
	}

	headers := []string{"FIELD", "VALUE"}
	rows := [][]string{
		{"lab", name},
		{"node", fmt.Sprintf("%d (%s)", idx, nodeIP)},
		{"previous expected votes", strconv.Itoa(before.ExpectedVotes)},
		{"quorate", strconv.FormatBool(after.Quorate)},
		{"expected votes", strconv.Itoa(after.ExpectedVotes)},
		{"total votes", strconv.Itoa(after.TotalVotes)},
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}

// clusterExpectedPickNode returns the index and mgmt IP of the node the verb
// runs on. An explicit --node is range-checked and used as given; in dry-run
// it is also probed so the preview reports reachability. Without --node, the
// lab's nodes are probed in index order and the first that answers wins.
func clusterExpectedPickNode(
	deps *cli.Deps, lab *config.Lab, name, nodeFlag string, numNodes int, dryRun bool,
) (int, string, error) {
	if nodeFlag != "" {
		idx, err := strconv.Atoi(nodeFlag)
		if err != nil {
			return 0, "", fmt.Errorf("--node %q is not a valid node index: %w", nodeFlag, err)
		}
		if idx < 0 || idx >= numNodes {
			return 0, "", fmt.Errorf(
				"--node %d is out of range for lab %q's %d-node topology (0-%d)", idx, name, numNodes, numNodes-1)
		}
		nodeIP, err := labNodeMgmtIP(lab.Network, idx)
		if err != nil {
			return 0, "", fmt.Errorf("resolve node %d mgmt IP: %w", idx, err)
		}
		if dryRun {
			reachable, perr := clusterExpectedProbe(deps, idx, nodeIP)
			if perr != nil {
				return 0, "", perr
			}
			if !reachable {
				return 0, "", fmt.Errorf("node %d (%s) is unreachable over ssh (exit status %d)",
					idx, nodeIP, sshUnreachableExitCode)
			}
		}
		return idx, nodeIP, nil
	}

	tried := make([]string, 0, numNodes)
	for idx := range numNodes {
		nodeIP, err := labNodeMgmtIP(lab.Network, idx)
		if err != nil {
			return 0, "", fmt.Errorf("resolve node %d mgmt IP: %w", idx, err)
		}
		reachable, perr := clusterExpectedProbe(deps, idx, nodeIP)
		if perr != nil {
			return 0, "", perr
		}
		if reachable {
			return idx, nodeIP, nil
		}
		tried = append(tried, fmt.Sprintf("node %d (%s)", idx, nodeIP))
	}
	return 0, "", fmt.Errorf(
		"no node of lab %q answered over ssh (exit status %d); tried %s; pass --node to target one explicitly",
		name, sshUnreachableExitCode, strings.Join(tried, ", "))
}

// clusterExpectedProbe reports whether the node at nodeIP answers over ssh. A
// connection failure (ssh exit status 255) is "not reachable" and not an
// error, so the caller can move on to the next node. Any other failure to
// even run ssh (a missing binary, bad connection settings) is returned as an
// error, since trying another node would fail the same way.
func clusterExpectedProbe(deps *cli.Deps, idx int, nodeIP string) (bool, error) {
	res, err := runGuestSSH(deps, nodeIP, clusterExpectedProbeCommand)
	if err == nil {
		return true, nil
	}
	if guestCommandTransportFailed(err) {
		return false, fmt.Errorf("probe node %d (%s): %w", idx, nodeIP, err)
	}
	if res.ExitCode == sshUnreachableExitCode {
		return false, nil
	}
	// The remote shell ran and returned its own non-zero status, so ssh did
	// connect and the node answered.
	return true, nil
}

// sshTransportMarkers are the lowercase fragments of the messages ssh prints
// to stderr when it cannot reach or authenticate to a host.
var sshTransportMarkers = []string{
	"connection refused",
	"connection timed out",
	"connection reset",
	"connection closed",
	"no route to host",
	"network is unreachable",
	"permission denied",
	"could not resolve hostname",
	"host key verification failed",
}

// sshTransportFailure reports whether a command that exited 255 failed in ssh
// itself and never reached the remote command. PVE's Perl CLI also exits 255
// when it dies (an unclustered node's `pvecm status` does), so the exit code
// alone cannot tell the two apart. An ssh failure leaves stdout empty and
// writes an ssh diagnostic to stderr: a line starting with "ssh:" or one of
// the well-known connection and authentication errors.
func sshTransportFailure(stdout, stderr string) bool {
	if strings.TrimSpace(stdout) != "" {
		return false
	}
	for line := range strings.SplitSeq(stderr, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(line, "ssh:") {
			return true
		}
		for _, marker := range sshTransportMarkers {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

// clusterExpectedReadStatus runs `pvecm status` on the node and parses it. A
// non-zero exit from pvecm itself, including exit 255 when its Perl CLI dies,
// is how an unclustered node reports its state, so it is not an error here
// (the parsed result carries Clustered). Exit 255 counts as unreachable only
// when it is an ssh transport failure. A failure to run ssh is an error.
func clusterExpectedReadStatus(deps *cli.Deps, idx int, nodeIP string) (pvecmStatus, error) {
	res, err := runGuestSSH(deps, nodeIP, "pvecm status")
	if err != nil {
		if guestCommandTransportFailed(err) {
			return pvecmStatus{}, fmt.Errorf("query node %d (%s) pvecm status: %w", idx, nodeIP, err)
		}
		if exec.ExitCodeOf(err) == sshUnreachableExitCode && sshTransportFailure(res.Stdout, res.Stderr) {
			return pvecmStatus{}, fmt.Errorf("node %d (%s) is unreachable over ssh: %w", idx, nodeIP, err)
		}
	}
	return parsePvecmStatus(res.Stdout), nil
}
