package nodefile

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/sshcmd"
)

const (
	testHost = "10.0.0.5"
	testPath = "/etc/pve/lxc/101.conf"
	testLock = "/run/lock/lxc/pve-config-101.lock"

	// testContent and testSHA are a fixed vector: testSHA is the hex sha256 of
	// the exact bytes of testContent (confirmed with `shasum -a 256`).
	testContent = "arch: amd64\nlxc.cap.drop: sys_admin\n"
	testSHA     = "642fd0b05d4c09aa052272f1529adb1d815938ea07c0b7fefb2108e3bffc4fea"
)

// testFlags returns the default (root, port 22) connection flags used across
// the argv assertions.
func testFlags() *sshcmd.Flags {
	return &sshcmd.Flags{User: "root", Port: 22}
}

// batchOpts is the leading ssh option argv every non-interactive call carries.
func batchOpts() []string {
	return []string{"-p", "22", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}
}

func TestRead_ArgvAndSHA(t *testing.T) {
	runner := exec.Fake(exec.FakeResponse{Stdout: testContent})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	content, sha, err := conn.Read(testPath)
	require.NoError(t, err)
	require.Equal(t, testContent, content)

	// sha matches both a freshly computed digest and the fixed vector.
	sum := sha256.Sum256([]byte(testContent))
	require.Equal(t, hex.EncodeToString(sum[:]), sha)
	require.Equal(t, testSHA, sha)

	require.Len(t, runner.Calls, 1)
	call := runner.Calls[0]
	require.Equal(t, "ssh", call.Name)

	wantArgs := append(batchOpts(), "root@10.0.0.5", "cat", sshcmd.ShellQuote(testPath))
	require.Equal(t, wantArgs, call.Args)
	require.Empty(t, call.StdinContents)
}

func TestRead_SizeCap(t *testing.T) {
	oversize := strings.Repeat("x", MaxFileSize+1)
	runner := exec.Fake(exec.FakeResponse{Stdout: oversize})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	_, _, err := conn.Read(testPath)
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestRead_Error(t *testing.T) {
	const stderr = "cat: /etc/pve/lxc/101.conf: No such file or directory"
	runner := exec.Fake(exec.FakeResponse{Stderr: stderr, ExitCode: 1})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	_, _, err := conn.Read(testPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), stderr)
}

func TestWrite_ArgvAndStdin(t *testing.T) {
	runner := exec.Fake(exec.FakeResponse{})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	err := conn.Write(testPath, testContent, testSHA, testLock)
	require.NoError(t, err)

	require.Len(t, runner.Calls, 1)
	call := runner.Calls[0]
	require.Equal(t, "ssh", call.Name)

	script := buildWriteScript(testPath, testSHA, testLock)
	wantArgs := append(batchOpts(), "root@10.0.0.5", "sh", "-ec", sshcmd.ShellQuote(script))
	require.Equal(t, wantArgs, call.Args)

	// The new file body travels on stdin, byte-for-byte, never in the argv.
	require.Equal(t, testContent, string(call.StdinContents))

	// The baked-in guard values and reserved exit codes are present in the script.
	require.Contains(t, script, sshcmd.ShellQuote(testPath))
	require.Contains(t, script, sshcmd.ShellQuote(testLock))
	require.Contains(t, script, sshcmd.ShellQuote(testSHA))
	require.Contains(t, script, "flock -w 10 9 || exit 90")
	require.Contains(t, script, "exit 91")
	require.Contains(t, script, "exit 92")
}

func TestWrite_ExitCodeDemux(t *testing.T) {
	cases := []struct {
		name string
		code int
		want error
	}{
		{"lock timeout", 90, ErrLockTimeout},
		{"conflict", 91, ErrConflict},
		{"not found", 92, ErrNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := exec.Fake(exec.FakeResponse{ExitCode: tc.code})
			conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

			err := conn.Write(testPath, testContent, testSHA, testLock)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestWrite_TransportError(t *testing.T) {
	const stderr = "root@10.0.0.5: Permission denied (publickey)."
	runner := exec.Fake(exec.FakeResponse{Stderr: stderr, ExitCode: 255})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	err := conn.Write(testPath, testContent, testSHA, testLock)
	require.Error(t, err)
	require.Contains(t, err.Error(), stderr)
	// 255 is not one of the mapped sentinels.
	require.NotErrorIs(t, err, ErrLockTimeout)
	require.NotErrorIs(t, err, ErrConflict)
	require.NotErrorIs(t, err, ErrNotFound)
}

func TestWrite_OversizeRefused(t *testing.T) {
	runner := exec.Fake(exec.FakeResponse{})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	oversize := strings.Repeat("x", MaxFileSize+1)
	err := conn.Write(testPath, oversize, testSHA, testLock)
	require.ErrorIs(t, err, ErrTooLarge)
	require.Empty(t, runner.Calls, "oversize content must be refused before any ssh call")
}

func TestExec_ArgvAndStreams(t *testing.T) {
	runner := exec.Fake(exec.FakeResponse{Stdout: "ok-out", Stderr: "some-warn"})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	const script = "pct config 101 >/dev/null"
	stdout, stderr, err := conn.Exec(script)
	require.NoError(t, err)
	require.Equal(t, "ok-out", stdout)
	require.Equal(t, "some-warn", stderr)

	require.Len(t, runner.Calls, 1)
	call := runner.Calls[0]
	require.Equal(t, "ssh", call.Name)

	wantArgs := append(batchOpts(), "root@10.0.0.5", "sh", "-ec", sshcmd.ShellQuote(script))
	require.Equal(t, wantArgs, call.Args)
}

func TestExec_Error(t *testing.T) {
	const stderr = "unable to parse config"
	runner := exec.Fake(exec.FakeResponse{Stdout: "partial", Stderr: stderr, ExitCode: 2})
	conn := Conn{Runner: runner, Flags: testFlags(), Host: testHost}

	stdout, gotStderr, err := conn.Exec("pct config 101 >/dev/null")
	require.Error(t, err)
	require.Contains(t, err.Error(), stderr)
	require.Equal(t, "partial", stdout)
	require.Equal(t, stderr, gotStderr)
}

// TestExec_ArgvMatchesWrite locks in that Read/Write/Exec all build their argv
// from the same BatchOptionArgs so the non-interactive hardening options can
// never drift between the three primitives.
func TestExec_ArgvMatchesWrite(t *testing.T) {
	f := &sshcmd.Flags{User: "admin", Port: 2222, Identity: "/k", Agent: true, NoStrict: true}
	want := sshcmd.BatchOptionArgs(f)

	readRunner := exec.Fake(exec.FakeResponse{Stdout: "x"})
	_, _, _ = Conn{Runner: readRunner, Flags: f, Host: testHost}.Read(testPath)
	require.Equal(t, want, readRunner.Calls[0].Args[:len(want)])

	execRunner := exec.Fake(exec.FakeResponse{})
	_, _, _ = Conn{Runner: execRunner, Flags: f, Host: testHost}.Exec("true")
	require.Equal(t, want, execRunner.Calls[0].Args[:len(want)])
}
