package pbs

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// pruneJobRunPath is the POST endpoint hit by `prune job run`.
const pruneJobRunPath = pruneAdminListPath + "/" + pruneJobID + "/run"

// pruneRunOutcome is what one confirmation-test invocation produced.
type pruneRunOutcome struct {
	err    error
	stdout string
	stderr string
	// calls counts the requests that reached the server.
	calls int
}

// runPruneWithInput runs `pmx pbs prune <args...>` with stdin set to input and
// cli.StdinInteractive reporting tty, so a test can play the part of a person
// at a terminal or of a script with no terminal. handlerPath is the endpoint
// the verb would call; calls counts how many requests reached it.
func runPruneWithInput(t *testing.T, handlerPath, input string, tty bool, args ...string) pruneRunOutcome {
	t.Helper()

	orig := cli.StdinInteractive
	cli.StdinInteractive = func(io.Reader) bool { return tty }
	t.Cleanup(func() { cli.StdinInteractive = orig })

	f, pc := newFakeClient(t)

	var out pruneRunOutcome

	f.HandleFunc("POST "+handlerPath, func(w http.ResponseWriter, _ *http.Request) {
		out.calls++
		if strings.HasSuffix(handlerPath, "/run") {
			testhelper.WriteData(w, nil)

			return
		}

		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)

	var stdout, stderr bytes.Buffer

	parent := &cobra.Command{Use: "pbs"}
	parent.AddCommand(newPruneCmd())
	parent.SetContext(cli.WithDeps(context.Background(), deps))
	parent.SetIn(strings.NewReader(input))
	parent.SetOut(&stdout)
	parent.SetErr(&stderr)
	parent.SetArgs(append([]string{"prune"}, args...))

	out.err = parent.Execute()
	out.stdout, out.stderr = stdout.String(), stderr.String()

	return out
}

func TestPruneRun_Confirmation(t *testing.T) {
	base := []string{"run", "--store", pruneStore, "--keep-daily", "7"}

	t.Run("terminal accepts", func(t *testing.T) {
		out := runPruneWithInput(t, prunePruneDatastorePath, "y\n", true, base...)
		require.NoError(t, out.err)
		require.Equal(t, 1, out.calls, "an accepted prompt prunes")
		require.Contains(t, out.stderr, `Prune datastore "store1"? Snapshots outside the retention window are deleted permanently. [y/N]: `)
		require.Contains(t, out.stdout, `Prune of datastore "store1" finished.`)
	})

	t.Run("terminal declines", func(t *testing.T) {
		out := runPruneWithInput(t, prunePruneDatastorePath, "n\n", true, base...)
		require.NoError(t, out.err)
		require.Zero(t, out.calls, "a declined prompt must not prune")
		require.Contains(t, out.stdout, "Aborted.")
		require.NotContains(t, out.stdout, "finished")
	})

	t.Run("terminal with no answer declines", func(t *testing.T) {
		out := runPruneWithInput(t, prunePruneDatastorePath, "", true, base...)
		require.NoError(t, out.err)
		require.Zero(t, out.calls)
		require.Contains(t, out.stdout, "Aborted.")
	})

	t.Run("non-terminal refuses", func(t *testing.T) {
		// Even an answer on stdin cannot stand in for --yes when there is no
		// terminal to ask.
		out := runPruneWithInput(t, prunePruneDatastorePath, "y\n", false, base...)
		require.EqualError(t, out.err,
			`refusing to prune datastore "store1" without confirmation: pass --yes/-y`)
		require.Zero(t, out.calls, "a refused run must not reach the server")
		require.NotContains(t, out.stderr, "[y/N]")
	})

	t.Run("yes prunes without a prompt", func(t *testing.T) {
		for _, flag := range []string{"--yes", "-y"} {
			out := runPruneWithInput(t, prunePruneDatastorePath, "", false, append(append([]string{}, base...), flag)...)
			require.NoError(t, out.err, flag)
			require.Equal(t, 1, out.calls, flag)
			require.NotContains(t, out.stderr, "[y/N]", flag)
		}
	})

	t.Run("dry run needs neither", func(t *testing.T) {
		for _, tty := range []bool{false, true} {
			out := runPruneWithInput(t, prunePruneDatastorePath, "", tty, append(append([]string{}, base...), "--dry-run")...)
			require.NoError(t, out.err)
			require.Equal(t, 1, out.calls, "the preview request is made")
			require.NotContains(t, out.stderr, "[y/N]", "a preview never prompts")
		}
	})

	t.Run("dry-run=false is a real prune and is gated", func(t *testing.T) {
		out := runPruneWithInput(t, prunePruneDatastorePath, "", false, append(append([]string{}, base...), "--dry-run=false")...)
		require.ErrorContains(t, out.err, "refusing to prune datastore")
		require.Zero(t, out.calls)
	})

	t.Run("namespace is named in the prompt", func(t *testing.T) {
		args := append(append([]string{}, base...), "--ns", "team-a")
		out := runPruneWithInput(t, prunePruneDatastorePath, "n\n", true, args...)
		require.NoError(t, out.err)
		require.Contains(t, out.stderr, `Prune datastore "store1" (namespace "team-a")?`)
		require.Zero(t, out.calls)
	})
}

func TestPruneJobRun_Confirmation(t *testing.T) {
	base := []string{"job", "run", pruneJobID}

	t.Run("terminal accepts", func(t *testing.T) {
		out := runPruneWithInput(t, pruneJobRunPath, "yes\n", true, base...)
		require.NoError(t, out.err)
		require.Equal(t, 1, out.calls)
		require.Contains(t, out.stderr, `Run prune job "job1"?`)
		require.Contains(t, out.stdout, `Prune job "job1" started.`)
	})

	t.Run("terminal declines", func(t *testing.T) {
		out := runPruneWithInput(t, pruneJobRunPath, "n\n", true, base...)
		require.NoError(t, out.err)
		require.Zero(t, out.calls)
		require.Contains(t, out.stdout, "Aborted.")
	})

	t.Run("non-terminal refuses", func(t *testing.T) {
		out := runPruneWithInput(t, pruneJobRunPath, "y\n", false, base...)
		require.EqualError(t, out.err,
			`refusing to run prune job "job1" without confirmation: pass --yes/-y`)
		require.Zero(t, out.calls)
	})

	t.Run("yes runs the job", func(t *testing.T) {
		out := runPruneWithInput(t, pruneJobRunPath, "", false, append(append([]string{}, base...), "--yes")...)
		require.NoError(t, out.err)
		require.Equal(t, 1, out.calls)
		require.Contains(t, out.stdout, `Prune job "job1" started.`)
	})
}

// TestPruneRunVerbs_HelpNamesYes guards the help of every verb that deletes by
// retention: each lists --yes and says what happens without it, and its
// example shows the flag where the verb needs it.
func TestPruneRunVerbs_HelpNamesYes(t *testing.T) {
	root := newPruneCmd()

	for _, path := range [][]string{{"run"}, {"job", "run"}} {
		cmd, _, err := root.Find(path)
		require.NoError(t, err)
		require.NotNil(t, cmd.Flags().Lookup("yes"), "%v has no --yes", path)
		require.Equal(t, "y", cmd.Flags().Lookup("yes").Shorthand)
		require.Contains(t, cmd.Long, "--yes/-y", "%v help must explain the confirmation", path)
		require.Contains(t, cmd.Example, "--yes", "%v examples must show --yes", path)
	}

	// The preview verb never deletes and takes no confirmation.
	sim, _, err := root.Find([]string{"simulate"})
	require.NoError(t, err)
	require.Nil(t, sim.Flags().Lookup("yes"))
}
