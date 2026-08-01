package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The package under test is three lines — persona selection from argv[0], and
// an exit code from cli.Main — but those three lines are the only place the
// binary's process contract lives: what a shell sees when a command fails, and
// which command surface a symlinked name exposes. Both are only observable by
// running a real binary, so these tests build one.

var (
	buildOnce sync.Once
	builtPath string
	buildErr  error
)

// pmxBinary builds cmd/pmx once per test run and returns the path. Building
// costs a few seconds on a cold cache and nothing after, which is why it is
// shared rather than per-test.
func pmxBinary(t *testing.T) string {
	t.Helper()

	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pmx-bin")
		if err != nil {
			buildErr = err
			return
		}
		builtPath = filepath.Join(dir, "pmx")
		out, err := exec.Command("go", "build", "-o", builtPath, ".").CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("go build failed: %s", out)
		}
	})
	require.NoError(t, buildErr)

	return builtPath
}

// runPMX runs the built binary under argv0 (a copy named for the persona under
// test) with an empty HOME, so it can neither read the developer's real config
// nor write to their log tree.
func runPMX(t *testing.T, argv0 string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	bin := pmxBinary(t)
	path := bin
	if argv0 != "pmx" {
		path = filepath.Join(t.TempDir(), argv0)
		data, err := os.ReadFile(bin)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o700)) //nolint:gosec // test binary must be executable
	}

	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
		"PMX_CONTEXT=", "PMX_NODE=", "PMX_OUTPUT=",
	)

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errorAs(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		require.NoError(t, err, "running the binary failed for a reason other than its exit code")
	}

	return outBuf.String(), errBuf.String(), code
}

// errorAs is errors.As, kept local so the import list stays about the process
// contract this file tests.
func errorAs(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError) //nolint:errorlint // exec.Cmd.Run returns it unwrapped
	if ok {
		*target = e
	}
	return ok
}

// TestMain_ExitCodes pins what a shell sees. cli.Main maps an error to a
// semantic code and main passes it to os.Exit; a regression that swallowed the
// code would make every failure look like success to a script.
func TestMain_ExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{name: "help succeeds", args: []string{"--help"}, want: 0},
		{name: "client version needs no config", args: []string{"version", "client"}, want: 0},
		{name: "unknown flag fails", args: []string{"--nosuchflag"}, want: 1},
		{name: "unknown command fails", args: []string{"nosuchcommand"}, want: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, code := runPMX(t, "pmx", tc.args...)
			require.Equal(t, tc.want, code)
		})
	}
}

// TestMain_PersonaComesFromArgv0 covers the other half of main: the binary
// shipped under several names, and the name it was invoked as selects the
// command surface. Installed as `pve`, the PVE commands are hoisted to the top
// level; installed as `pmx`, they sit under a `pve` group.
func TestMain_PersonaComesFromArgv0(t *testing.T) {
	pmxOut, _, code := runPMX(t, "pmx", "--help")
	require.Equal(t, 0, code)
	require.Contains(t, pmxOut, "pve", "the full tree groups each product")
	require.Contains(t, pmxOut, "pbs")
	require.Contains(t, pmxOut, "pdm")

	pveOut, _, code := runPMX(t, "pve", "--help")
	require.Equal(t, 0, code)
	require.Contains(t, pveOut, "qemu", "the pve persona hoists that product to the top level")
	require.Contains(t, pveOut, "lxc")
	require.NotContains(t, pveOut, "Manage a Proxmox Backup Server",
		"and does not offer the other products' groups")
}

// TestMain_UnconfiguredContextFailsWithGuidance is the first thing a new
// install does: run a command with no config at all. It must fail cleanly with
// a message naming what is missing, not panic or hang trying to reach a
// server that was never configured.
func TestMain_UnconfiguredContextFailsWithGuidance(t *testing.T) {
	_, stderr, code := runPMX(t, "pmx", "pve", "node", "ls", "--no-log")

	require.NotEqual(t, 0, code)
	require.Contains(t, strings.ToLower(stderr), "context")
}
