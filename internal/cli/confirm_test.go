package cli_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// withTerminal makes cli.StdinInteractive report tty for the test.
func withTerminal(t *testing.T, tty bool) {
	t.Helper()
	orig := cli.StdinInteractive
	cli.StdinInteractive = func(io.Reader) bool { return tty }
	t.Cleanup(func() { cli.StdinInteractive = orig })
}

func confirmCmd(stdin string) (*cobra.Command, *bytes.Buffer) {
	var stderr bytes.Buffer
	cmd := &cobra.Command{Use: "x"}
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(&stderr)
	return cmd, &stderr
}

func TestConfirmDestructive_YesSkipsThePromptEvenOffATerminal(t *testing.T) {
	withTerminal(t, false)
	cmd, stderr := confirmCmd("")

	ok, err := cli.ConfirmDestructive(cmd, true, "prune datastore", "Prune?")
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, stderr.String())
}

func TestConfirmDestructive_NonInteractiveRefusesWithTheUsualMessage(t *testing.T) {
	withTerminal(t, false)
	// An answer on a pipe must not count: the run cannot be asked.
	cmd, stderr := confirmCmd("y\n")

	ok, err := cli.ConfirmDestructive(cmd, false, "prune datastore \"tank\"", "Prune?")
	require.False(t, ok)
	require.EqualError(t, err, `refusing to prune datastore "tank" without confirmation: pass --yes/-y`)
	require.Empty(t, stderr.String(), "no prompt is printed when nobody can answer")
}

func TestConfirmDestructive_TerminalAnswers(t *testing.T) {
	cases := map[string]struct {
		input string
		want  bool
	}{
		"y":        {"y\n", true},
		"yes":      {"yes\n", true},
		"upper Y":  {"Y\n", true},
		"padded":   {"  yes  \n", true},
		"y no eol": {"y", true},
		"n":        {"n\n", false},
		"empty":    {"\n", false},
		"other":    {"maybe\n", false},
		"no":       {"no\n", false},
		"eof":      {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withTerminal(t, true)
			cmd, stderr := confirmCmd(tc.input)

			ok, err := cli.ConfirmDestructive(cmd, false, "prune", "Prune datastore?")
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
			require.Equal(t, "Prune datastore? [y/N]: ", stderr.String(), "the prompt goes to stderr")
		})
	}
}
