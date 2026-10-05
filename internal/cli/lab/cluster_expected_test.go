package lab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// samplePvecmStatusVotes returns pvecm status output for a clustered node
// reporting the given expected and total vote counts and quorate state.
func samplePvecmStatusVotes(expected, total int, quorate bool) string {
	q := "No"
	flags := "Activity blocked"
	if quorate {
		q = "Yes"
		flags = "Quorate"
	}
	return fmt.Sprintf(`Cluster information
-------------------
Name:             wayne
Config Version:   3
Transport:        knet
Secure auth:      on

Quorum information
------------------
Date:             Thu Jul 16 12:00:00 2026
Quorum provider:  corosync_votequorum
Nodes:            %d
Node ID:          0x00000002
Ring ID:          2.1b
Quorate:          %s

Votequorum information
----------------------
Expected votes:   %d
Highest expected: %d
Total votes:      %d
Quorum:           2
Flags:            %s

Membership information
----------------------
    Nodeid      Votes Name
0x00000002          1 10.10.1.11 (local)
`, total, q, expected, expected, total, flags)
}

// expectedTestCmd builds `pmx lab cluster` for a three-node lab named wayne,
// wired to a fake runner preloaded with responses.
func expectedTestCmd(t *testing.T, responses ...exec.FakeResponse) (*cobra.Command, *exec.FakeRunner) {
	t.Helper()

	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(responses...)
	cli.GetDeps(cmd).Runner = fake
	return cmd, fake
}

// remoteCommands returns the remote command of each recorded ssh call, which
// is always the final argv element.
func remoteCommands(fake *exec.FakeRunner) []string {
	cmds := make([]string, 0, len(fake.Calls))
	for _, c := range fake.Calls {
		cmds = append(cmds, c.Args[len(c.Args)-1])
	}
	return cmds
}

// callTargets returns the space-joined argv of each recorded ssh call, so a
// test can check which node's address a call went to.
func callTargets(fake *exec.FakeRunner) []string {
	out := make([]string, 0, len(fake.Calls))
	for _, c := range fake.Calls {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

func countRemote(fake *exec.FakeRunner, want string) int {
	n := 0
	for _, c := range remoteCommands(fake) {
		if c == want {
			n++
		}
	}
	return n
}

func TestClusterExpected_ExplicitNode(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)}, // status before
		exec.FakeResponse{}, // pvecm expected
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(1, 1, true)}, // status after
	)

	out, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "1")
	require.NoError(t, err)

	assert.Equal(t, []string{"pvecm status", "pvecm expected 1", "pvecm status"}, remoteCommands(fake),
		"an explicit --node must skip the reachability probe")
	for i, target := range callTargets(fake) {
		assert.Contains(t, target, "10.10.1.11", "call %d must target node 1", i)
	}
	assert.Contains(t, out, "1 (10.10.1.11)")
	assert.Contains(t, out, "quorate")
	assert.Contains(t, out, "true")
}

func TestClusterExpected_AutoPickSkipsUnreachableNode0(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{Stderr: "ssh: connect to host 10.10.1.10: No route to host", ExitCode: 255}, // probe node 0
		exec.FakeResponse{}, // probe node 1
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)}, // status before
		exec.FakeResponse{}, // pvecm expected
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(1, 1, true)}, // status after
	)

	out, err := runGuestCmd(t, cmd, "expected", "wayne", "1")
	require.NoError(t, err)

	assert.Equal(t,
		[]string{clusterExpectedProbeCommand, clusterExpectedProbeCommand, "pvecm status", "pvecm expected 1", "pvecm status"},
		remoteCommands(fake))
	targets := callTargets(fake)
	assert.Contains(t, targets[0], "10.10.1.10")
	for i := 1; i < len(targets); i++ {
		assert.Contains(t, targets[i], "10.10.1.11", "call %d must target node 1", i)
		assert.NotContains(t, targets[i], "10.10.1.10")
	}
	assert.Contains(t, out, "1 (10.10.1.11)")
}

func TestClusterExpected_AutoPickUsesNode0WhenItAnswers(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{}, // probe node 0
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(2, 1, false)},
	)

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "2")
	require.NoError(t, err)
	assert.Equal(t, 1, countRemote(fake, clusterExpectedProbeCommand), "the probe stops at the first node that answers")
	assert.Contains(t, callTargets(fake)[1], "10.10.1.10")
}

func TestClusterExpected_AllNodesUnreachable(t *testing.T) {
	unreachable := exec.FakeResponse{ExitCode: 255}
	cmd, fake := expectedTestCmd(t, unreachable, unreachable, unreachable)

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "1")
	require.Error(t, err)
	for _, ip := range []string{"10.10.1.10", "10.10.1.11", "10.10.1.12"} {
		assert.ErrorContains(t, err, ip)
	}
	assert.ErrorContains(t, err, "node 0")
	assert.ErrorContains(t, err, "node 2")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"))
	assert.Len(t, fake.Calls, 3, "one probe per node and nothing else")
}

func TestClusterExpected_ExplicitNodeUnreachable(t *testing.T) {
	cmd, fake := expectedTestCmd(t, exec.FakeResponse{
		Stderr:   "ssh: connect to host 10.10.1.11 port 22: Connection timed out",
		ExitCode: 255,
	})

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "unreachable")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"))
}

func TestClusterExpected_RefusesRaise(t *testing.T) {
	cmd, fake := expectedTestCmd(t, exec.FakeResponse{Stdout: samplePvecmStatusVotes(2, 2, true)})

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "3", "--node", "0")
	require.Error(t, err)
	assert.ErrorContains(t, err, "only lower")
	assert.ErrorContains(t, err, "currently expects 2")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 3"))
}

func TestClusterExpected_EqualVotesIsAllowed(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(2, 2, true)},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(2, 2, true)},
	)

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "2", "--node", "0")
	require.NoError(t, err)
	assert.Equal(t, 1, countRemote(fake, "pvecm expected 2"))
}

func TestClusterExpected_RefusesUnclusteredNode(t *testing.T) {
	cmd, fake := expectedTestCmd(t, exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1})

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "0")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not part of a cluster")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"))
}

func TestClusterExpected_UnclusteredNodeExiting255IsNotUnreachable(t *testing.T) {
	cmd, fake := expectedTestCmd(t, exec.FakeResponse{
		Stderr:   "Corosync config '/etc/pve/corosync.conf' does not exist - is this node part of a cluster?\n",
		ExitCode: 255,
	})

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "0")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not part of a cluster")
	assert.NotContains(t, err.Error(), "unreachable")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"))
}

func TestClusterExpected_SSHFailureExiting255IsUnreachable(t *testing.T) {
	cmd, fake := expectedTestCmd(t, exec.FakeResponse{
		Stderr:   "ssh: connect to host 10.10.1.10 port 22: Connection timed out\n",
		ExitCode: 255,
	})

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "0")
	require.Error(t, err)
	assert.ErrorContains(t, err, "unreachable over ssh")
	assert.NotContains(t, err.Error(), "not part of a cluster")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"))
}

func TestSSHTransportFailure(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr string
		want                 bool
	}{
		{"ssh prefix", "", "ssh: connect to host x port 22: Connection timed out", true},
		{"connection refused", "", "connect to host x port 22: Connection refused", true},
		{"permission denied", "", "root@x: Permission denied (publickey).", true},
		{"resolve failure", "", "ssh: Could not resolve hostname x: nodename nor servname provided", true},
		{"second stderr line", "", "Warning: something\nssh: connect to host x port 22: No route to host", true},
		{"stdout present", "Cluster information", "Connection refused", false},
		{"pve die message", "", "Corosync config does not exist", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sshTransportFailure(tc.stdout, tc.stderr))
		})
	}
}

func TestClusterExpected_FailsWhenExpectedVotesUnreadable(t *testing.T) {
	cmd, fake := expectedTestCmd(t, exec.FakeResponse{Stdout: samplePvecmStatusVotes(0, 1, false)})

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "could not read the expected votes from `pvecm status` on node 1")
	assert.NotContains(t, err.Error(), "currently expects")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"))
}

func TestClusterExpected_RejectsBadVotes(t *testing.T) {
	for _, tc := range []struct {
		name, votes, want string
	}{
		{"zero", "0", "must be 1 or more"},
		{"negative", "-2", "must be 1 or more"},
		{"non-integer", "two", "not an integer"},
		{"fractional", "1.5", "not an integer"},
		{"empty", "", "not an integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, fake := expectedTestCmd(t)

			// "--" keeps cobra from reading a negative number as a flag.
			_, err := runGuestCmd(t, cmd, "expected", "wayne", "--", tc.votes)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
			assert.Empty(t, fake.Calls, "bad votes must fail before any ssh call")
		})
	}
}

func TestClusterExpected_RejectsBadNode(t *testing.T) {
	for _, node := range []string{"3", "-1", "x"} {
		t.Run(node, func(t *testing.T) {
			cmd, fake := expectedTestCmd(t)

			_, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", node)
			require.Error(t, err)
			assert.ErrorContains(t, err, "--node")
			assert.Empty(t, fake.Calls)
		})
	}
}

func TestClusterExpected_RequiresTwoArgs(t *testing.T) {
	cmd, fake := expectedTestCmd(t)

	_, err := runGuestCmd(t, cmd, "expected", "wayne")
	require.Error(t, err)
	assert.Empty(t, fake.Calls)
}

func TestClusterExpected_UnknownLab(t *testing.T) {
	cmd, fake := expectedTestCmd(t)

	_, err := runGuestCmd(t, cmd, "expected", "nope", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestClusterExpected_DryRunNeverRunsExpected(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{ExitCode: 255}, // probe node 0
		exec.FakeResponse{},              // probe node 1
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)},
	)

	out, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--dry-run")
	require.NoError(t, err)

	assert.Contains(t, out, "[dry-run]")
	assert.Contains(t, out, "node 1 (10.10.1.11)")
	assert.Contains(t, out, "pvecm expected 1")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"), "dry-run must never run pvecm expected")
	assert.Equal(t, []string{clusterExpectedProbeCommand, clusterExpectedProbeCommand, "pvecm status"}, remoteCommands(fake),
		"dry-run still probes reachability and reads status")
}

func TestClusterExpected_DryRunExplicitNodeProbes(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{}, // probe
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)},
	)

	out, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "2", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "node 2 (10.10.1.12)")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 1"))
	assert.Len(t, fake.Calls, 2)
}

func TestClusterExpected_DryRunStillRefusesRaise(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(1, 1, true)},
	)

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "2", "--dry-run")
	require.Error(t, err)
	assert.ErrorContains(t, err, "only lower")
	assert.Equal(t, 0, countRemote(fake, "pvecm expected 2"))
}

func TestClusterExpected_JSONOutput(t *testing.T) {
	cmd, _ := expectedTestCmd(t,
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(1, 1, true)},
	)
	cli.GetDeps(cmd).Format = output.FormatJSON

	out, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "1")
	require.NoError(t, err)

	var decoded struct {
		Headers []string   `json:"headers"`
		Rows    [][]string `json:"rows"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &decoded), "json output must be a single valid document")
	got := map[string]string{}
	for _, row := range decoded.Rows {
		require.Len(t, row, 2)
		got[row[0]] = row[1]
	}
	assert.Equal(t, "wayne", got["lab"])
	assert.Equal(t, "1 (10.10.1.11)", got["node"])
	assert.Equal(t, "true", got["quorate"])
	assert.Equal(t, "1", got["expected votes"])
	assert.Equal(t, "1", got["total votes"])
	assert.Equal(t, "3", got["previous expected votes"])
}

func TestClusterExpected_WarnsWhenReadBackDiffers(t *testing.T) {
	cmd, _ := expectedTestCmd(t,
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 2, true)}, // nodes rejoined
	)

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"expected", "wayne", "1", "--node", "1"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, stderr.String(), "warning:")
	assert.Contains(t, stderr.String(), "now reports 3 expected votes")
	assert.Contains(t, stderr.String(), "other nodes may have rejoined")
	assert.Contains(t, stdout.String(), "expected votes", "the result is still rendered")
	assert.NotContains(t, stdout.String(), "warning:")
}

func TestClusterExpected_ExpectedCommandFails(t *testing.T) {
	cmd, fake := expectedTestCmd(t,
		exec.FakeResponse{Stdout: samplePvecmStatusVotes(3, 1, false)},
		exec.FakeResponse{Stderr: "Unable to set expected votes", ExitCode: 1},
	)

	_, err := runGuestCmd(t, cmd, "expected", "wayne", "1", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "pvecm expected 1")
	assert.Len(t, fake.Calls, 2, "no status read-back after a failed command")
}

func TestClusterExpected_RegisteredInGroup(t *testing.T) {
	var found bool
	for _, c := range newClusterCmd().Commands() {
		if c.Name() == "expected" {
			found = true
			assert.NotNil(t, c.Flags().Lookup("node"))
			assert.NotNil(t, c.Flags().Lookup("dry-run"))
			assert.NotEmpty(t, c.Short)
			assert.NotEmpty(t, c.Long)
			assert.NotEmpty(t, c.Example)
		}
	}
	assert.True(t, found, "cluster must register the expected verb")
}
