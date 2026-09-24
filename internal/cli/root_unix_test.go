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

// Every wait in these tests is for an event, and these bounds only stop a
// hang, so scheduler load cannot fail a test that the product passes.
const (
	// helperStartGuard bounds a helper's start, including its dial and the
	// stand-in's start inside it.
	helperStartGuard = 3 * time.Minute

	// standInStartGuard bounds the stand-in's start inside a helper. It is
	// below helperStartGuard, so a stand-in that never starts fails the
	// helper with its own message first.
	standInStartGuard = 2 * time.Minute

	// stepGuard bounds every later step: a line, an exit, or a process
	// going away after the product ended it.
	stepGuard = time.Minute

	// helperOrphanGuard is how long a helper waits for the signal the test
	// sends it before it gives up, so a helper whose test died never
	// lingers. It is above the sum of every bound the test waits through.
	helperOrphanGuard = 10 * time.Minute
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
//
// It waits for cmd in the background and returns a channel that closes once
// Wait has returned, after which cmd.ProcessState is safe to read. The
// cleanup kills a helper that still runs and waits for that channel, so a
// failed test never leaves a helper behind and never reads the process
// state while Wait is writing it.
func startHelper(t *testing.T, cmd *exec.Cmd) <-chan struct{} {
	t.Helper()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	err := cmd.Start()
	signal.Stop(hup)

	require.NoError(t, err)

	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = cmd.Wait()
	}()

	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})

	return done
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

	testhelper.WaitPID(t, os.Getenv(jumpHelperDescendantEnv), standInStartGuard)

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
// done is the channel startHelper returned.
func waitHelper(t *testing.T, cmd *exec.Cmd, done <-chan struct{}, bound time.Duration) syscall.WaitStatus {
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

	done := startHelper(t, cmd)

	ws := waitHelper(t, cmd, done, helperStartGuard)
	require.True(t, ws.Exited(), "the helper must exit, not die")
	require.Equal(t, 0, ws.ExitStatus(), "Main must succeed for --help")

	// The helper waited for both pid files before it ran Main, so both are
	// already written.
	self := testhelper.WaitPID(t, s.PIDFile, stepGuard)
	descendant := testhelper.WaitPID(t, s.DescendantPIDFile, stepGuard)

	// Neither process ever exits on its own and both run in a session of
	// their own, so only Execute's reaping can end them, and a wait of any
	// length still fails when it did not.
	requireProcessGone(t, self, stepGuard, "the stand-in")
	requireProcessGone(t, descendant, stepGuard, "the descendant")
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

		// The second signal must end this process. Reaching the exit below
		// means it never came or never killed.
		time.Sleep(helperOrphanGuard)

		_ = conn
		os.Exit(92)

	case "hup-ignored":
		signal.Ignore(syscall.SIGHUP)

		ctx, stop := signalContext()
		defer stop()

		fmt.Println("ready")

		// The test kills this process once it has seen that the signal
		// changed nothing.
		select {
		case <-ctx.Done():
			fmt.Println("cancelled")
		case <-time.After(helperOrphanGuard):
		}

		time.Sleep(helperOrphanGuard)
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

		done := startHelper(t, cmd)

		awaitLine(t, out, "ready", helperStartGuard)

		// The helper printed ready only after both pid files were written.
		self := testhelper.WaitPID(t, s.PIDFile, stepGuard)
		descendant := testhelper.WaitPID(t, s.DescendantPIDFile, stepGuard)

		require.NoError(t, cmd.Process.Signal(syscall.SIGHUP))
		awaitLine(t, out, "cancelled", stepGuard)
		require.False(t, testhelper.ProcessGone(self), "the first signal only cancels; the child is left to Execute")

		require.NoError(t, cmd.Process.Signal(syscall.SIGHUP))

		ws := waitHelper(t, cmd, done, stepGuard)
		require.True(t, ws.Signaled(), "the second signal must end the helper by that signal (status %v)", ws)
		assert.Equal(t, syscall.SIGHUP, ws.Signal())

		// Neither process ever exits on its own, so only the kill that the
		// second signal sent can end them.
		requireProcessGone(t, self, stepGuard, "the stand-in")
		requireProcessGone(t, descendant, stepGuard, "the descendant")
	})

	t.Run("a SIGHUP ignored at start stays ignored", func(t *testing.T) {
		s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang})

		cmd := helperCommand(t, "TestSignalContext_SecondSignalKillsJumpGroups", "hup-ignored", s)
		out := lines(t, cmd)

		done := startHelper(t, cmd)

		awaitLine(t, out, "ready", helperStartGuard)
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
		waitHelper(t, cmd, done, stepGuard)
	})
}
