package main_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// The package under test is three lines — persona selection from argv[0], and
// an exit code from cli.Main — but those three lines are the only place the
// binary's process contract lives: what a shell sees when a command fails, and
// which command surface a symlinked name exposes. Both are only observable by
// running a real binary, so these tests build one.

// TestMain clears the eight PMX_API_* variables before any test runs. Every
// binary these tests start inherits this process's environment, so an
// override exported in the operator's shell would otherwise change which
// endpoint, proxy, bastion, or timeout the binary resolves.
func TestMain(m *testing.M) {
	restore := testhelper.UnsetAPIEnv()
	code := m.Run()
	restore()

	os.Exit(code)
}

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

// TestMain_DiskAuditExitCodes pins the audit's codes as a shell sees them:
// 0 for a clean cluster, 9 for free-floating disks, 2 for a bad flag value,
// and pmx's usual 3 when the API cannot be reached.
func TestMain_DiskAuditExitCodes(t *testing.T) {
	audit := func(t *testing.T, cfg string, extra ...string) (string, int) {
		t.Helper()
		args := append([]string{"--config", cfg, "--no-log", "cpi", "disk-audit"}, extra...)
		_, stderr, code := runPMX(t, "pmx", args...)
		return stderr, code
	}

	t.Run("clean cluster exits 0", func(t *testing.T) {
		f := diskAuditFake(t)
		stderr, code := audit(t, fakeConfig(t, f))
		require.Equal(t, 0, code, stderr)
	})
	t.Run("free-floating disk exits 9", func(t *testing.T) {
		f := diskAuditFake(t, "a:vm-9001-disk-0")
		stderr, code := audit(t, fakeConfig(t, f))
		require.Equal(t, 9, code, stderr)
		require.Contains(t, stderr, "EXIT 9: 1 free-floating disk(s) found.")
	})
	t.Run("bad band exits 2", func(t *testing.T) {
		f := diskAuditFake(t)
		stderr, code := audit(t, fakeConfig(t, f), "--disk-band", "29999-9000")
		require.Equal(t, 2, code, stderr)
		require.Contains(t, stderr, "invalid --disk-band")
	})
	t.Run("unreachable API exits 3", func(t *testing.T) {
		f := diskAuditFake(t)
		cfg := fakeConfig(t, f)
		f.Server.Close()
		stderr, code := audit(t, cfg)
		require.Equal(t, 3, code, stderr)
	})
}

// diskAuditFake serves one online node with one images storage holding
// volumes, and no guests.
func diskAuditFake(t *testing.T, volumes ...string) *testhelper.FakePVE {
	t.Helper()
	f := testhelper.NewFakePVE(t)
	content := []any{}
	for _, v := range volumes {
		content = append(content, map[string]any{"volid": v, "size": 1 << 30})
	}
	f.HandleJSON("GET /api2/json/nodes/pve1/storage", []any{map[string]any{"storage": "a", "content": "images"}})
	f.HandleJSON("GET /api2/json/nodes/pve1/storage/a/content", content)
	f.HandleJSON("GET /api2/json/access/permissions", map[string]any{"/vms": map[string]any{"VM.Audit": 1}})
	return f
}

// fakeConfig writes a token-auth config pointing at f and returns its path.
func fakeConfig(t *testing.T, f *testhelper.FakePVE) string {
	t.Helper()
	host, port, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	cfg := "current-context: fake\ncontexts:\n  fake:\n    host: " + host + "\n    port: " + port +
		"\n    protocol: http\n    realm: pam\n    auth:\n      type: token\n      username: root\n" +
		"      token-id: test\n      secret: 00000000-0000-0000-0000-000000000000\n"
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
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
