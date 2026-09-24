//go:build unix

package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// The re-executed helpers read their role and the stand-in's files from
// these variables.
const (
	jumpHelperEnv           = "PMX_TEST_JUMP_HELPER"
	jumpHelperProgramEnv    = "PMX_TEST_JUMP_PROGRAM"
	jumpHelperDescendantEnv = "PMX_TEST_JUMP_DESCENDANT_PID_FILE"
)

// helperCommand re-executes this test binary as a helper that runs only
// test, in role, with a home of its own so nothing it writes lands in the
// operator's real config or log directories.
func helperCommand(t *testing.T, test, role string, s testhelper.SSHScript) *exec.Cmd {
	t.Helper()

	home := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.count=1") //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(),
		jumpHelperEnv+"="+role,
		jumpHelperProgramEnv+"="+s.Program,
		jumpHelperDescendantEnv+"="+s.DescendantPIDFile,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
	)

	return cmd
}

// startHelper starts cmd with SIGHUP at its default disposition. A child
// inherits an ignored signal across exec, so a runner started under nohup
// would otherwise leak its disposition into the helper; registering a
// handler for the moment of the fork makes the runtime reset it to the
// default in the child.
func startHelper(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	err := cmd.Start()
	signal.Stop(hup)

	require.NoError(t, err)

	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
}

// dialJumpHelper dials through the stand-in named in the environment and
// leaves the connection open on purpose, then waits until the stand-in has
// spawned its descendant. It returns the connection so the caller can keep
// it reachable.
func dialJumpHelper(t *testing.T) any {
	t.Helper()

	// Main closes the jump registry on its way out, and this process may
	// have run it before, so reopen it before dialing.
	apiclient.ReopenJumps()

	spec := apiclient.JumpSpec{
		Chain:          "bastion",
		ConnectTimeout: time.Second,
		Program:        os.Getenv(jumpHelperProgramEnv),
	}

	conn, err := apiclient.JumpDialContext(spec)(context.Background(), "tcp", "127.0.0.1:9")
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper dial:", err)
		os.Exit(90)
	}

	testhelper.WaitPID(t, os.Getenv(jumpHelperDescendantEnv), 10*time.Second)

	return conn
}

// lines streams the helper's standard output line by line.
func lines(t *testing.T, cmd *exec.Cmd) <-chan string {
	t.Helper()

	out, err := cmd.StdoutPipe()
	require.NoError(t, err)

	ch := make(chan string, 16)

	go func() {
		defer close(ch)

		sc := bufio.NewScanner(out)
		for sc.Scan() {
			ch <- sc.Text()
		}
	}()

	return ch
}

// awaitLine waits up to bound for the helper to print want.
func awaitLine(t *testing.T, ch <-chan string, want string, bound time.Duration) {
	t.Helper()

	deadline := time.After(bound)

	for {
		select {
		case line, ok := <-ch:
			require.True(t, ok, "helper exited before printing %q", want)

			if line == want {
				return
			}
		case <-deadline:
			t.Fatalf("helper did not print %q within %s", want, bound)
		}
	}
}

// waitHelper waits up to bound for cmd to exit, and returns its wait status.
func waitHelper(t *testing.T, cmd *exec.Cmd, done <-chan error, bound time.Duration) syscall.WaitStatus {
	t.Helper()

	select {
	case <-done:
	case <-time.After(bound):
		t.Fatalf("helper did not exit within %s", bound)
	}

	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	require.True(t, ok)

	return ws
}

// requireProcessGone asserts that pid is gone within bound, counting a
// zombie as gone.
func requireProcessGone(t *testing.T, pid int, bound time.Duration, what string) {
	t.Helper()

	require.True(t, testhelper.WaitProcessGone(pid, bound), "%s (pid %d) still runs after %s", what, pid, bound)
}

// TestExecute_ReapsJumpChildrenBeforeExit pins that no jump child outlives
// pmx: the ssh child runs in a session of its own, so nothing but Execute's
// reaping ends it once main calls os.Exit.
func TestExecute_ReapsJumpChildrenBeforeExit(t *testing.T) {
	if os.Getenv(jumpHelperEnv) == "reap" {
		conn := dialJumpHelper(t)

		os.Args = []string{"pmx", "--help"}
		code := Main("pmx", nil)

		_ = conn
		os.Exit(code)
	}

	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHHang,
		IgnoreEOF:  true,
		Descendant: true,
	})

	cmd := helperCommand(t, "TestExecute_ReapsJumpChildrenBeforeExit", "reap", s)
	cmd.Stdout = nil
	cmd.Stderr = nil

	startHelper(t, cmd)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	ws := waitHelper(t, cmd, done, 30*time.Second)
	require.True(t, ws.Exited(), "the helper must exit, not die")
	require.Equal(t, 0, ws.ExitStatus(), "Main must succeed for --help")

	self := testhelper.WaitPID(t, s.PIDFile, time.Second)
	descendant := testhelper.WaitPID(t, s.DescendantPIDFile, time.Second)

	requireProcessGone(t, self, 3*time.Second, "the stand-in")
	requireProcessGone(t, descendant, 3*time.Second, "the descendant")
}

// TestSignalContext_SecondSignalKillsJumpGroups pins the hangup and
// second-signal paths: the first SIGHUP cancels the root context, the second
// kills every jump process group and then pmx itself by the same signal, and
// a SIGHUP that pmx started with ignored stays ignored.
func TestSignalContext_SecondSignalKillsJumpGroups(t *testing.T) {
	switch os.Getenv(jumpHelperEnv) {
	case "second-signal":
		// Start from the default disposition whatever the runner passed on.
		signal.Reset(syscall.SIGHUP)

		if signal.Ignored(syscall.SIGHUP) {
			fmt.Fprintln(os.Stderr, "helper started with SIGHUP ignored")
			os.Exit(91)
		}

		ctx, stop := signalContext()
		defer stop()

		conn := dialJumpHelper(t)

		fmt.Println("ready")
		<-ctx.Done()
		fmt.Println("cancelled")

		time.Sleep(30 * time.Second)

		_ = conn
		os.Exit(92)

	case "hup-ignored":
		signal.Ignore(syscall.SIGHUP)

		ctx, stop := signalContext()
		defer stop()

		fmt.Println("ready")

		select {
		case <-ctx.Done():
			fmt.Println("cancelled")
		case <-time.After(30 * time.Second):
		}

		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	t.Run("second SIGHUP kills the jump groups and pmx", func(t *testing.T) {
		s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
			Mode:       testhelper.SSHHang,
			IgnoreEOF:  true,
			Descendant: true,
		})

		cmd := helperCommand(t, "TestSignalContext_SecondSignalKillsJumpGroups", "second-signal", s)
		out := lines(t, cmd)

		startHelper(t, cmd)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		awaitLine(t, out, "ready", 30*time.Second)

		self := testhelper.WaitPID(t, s.PIDFile, 5*time.Second)
		descendant := testhelper.WaitPID(t, s.DescendantPIDFile, 5*time.Second)

		require.NoError(t, cmd.Process.Signal(syscall.SIGHUP))
		awaitLine(t, out, "cancelled", 5*time.Second)
		require.False(t, testhelper.ProcessGone(self), "the first signal only cancels; the child is left to Execute")

		require.NoError(t, cmd.Process.Signal(syscall.SIGHUP))

		ws := waitHelper(t, cmd, done, 5*time.Second)
		require.True(t, ws.Signaled(), "the second signal must end the helper by that signal (status %v)", ws)
		assert.Equal(t, syscall.SIGHUP, ws.Signal())

		requireProcessGone(t, self, time.Second, "the stand-in")
		requireProcessGone(t, descendant, time.Second, "the descendant")
	})

	t.Run("a SIGHUP ignored at start stays ignored", func(t *testing.T) {
		s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang})

		cmd := helperCommand(t, "TestSignalContext_SecondSignalKillsJumpGroups", "hup-ignored", s)
		out := lines(t, cmd)

		startHelper(t, cmd)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		awaitLine(t, out, "ready", 30*time.Second)
		require.NoError(t, cmd.Process.Signal(syscall.SIGHUP))

		select {
		case line, ok := <-out:
			if ok {
				t.Fatalf("an ignored SIGHUP must leave the context live, but the helper printed %q", line)
			}

			t.Fatal("an ignored SIGHUP must leave the helper running")
		case <-done:
			t.Fatal("an ignored SIGHUP must leave the helper running")
		case <-time.After(500 * time.Millisecond):
		}

		require.NoError(t, cmd.Process.Kill())
		waitHelper(t, cmd, done, 5*time.Second)
	})
}
