//go:build unix

package apiclient

import (
	"context"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func init() {
	assertChildExitedNormally = func(t *testing.T, ps *os.ProcessState) {
		t.Helper()
		require.NotNil(t, ps)

		ws, ok := ps.Sys().(syscall.WaitStatus)
		require.True(t, ok)
		assert.True(t, ws.Exited(), "the child must exit on its own")
		assert.False(t, ws.Signaled(), "the child must not be ended by a signal (got %v)", ws.Signal())
	}
}

// requireSignaled asserts that the reaped child was ended by sig.
func requireSignaled(t *testing.T, c *jumpConn, sig syscall.Signal) {
	t.Helper()

	ws, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.True(t, ws.Signaled(), "the child must have been ended by a signal")
	assert.Equal(t, sig, ws.Signal())
}

// requireGone asserts that pid is gone within bound, counting a zombie as
// gone.
func requireGone(t *testing.T, pid int, bound time.Duration, what string) {
	t.Helper()

	require.True(t, testhelper.WaitProcessGone(pid, bound), "%s (pid %d) still runs after %s", what, pid, bound)
}

// registered reports whether c still has a registry entry.
func registered(c *jumpConn) bool {
	jumpRegistry.mu.Lock()
	defer jumpRegistry.mu.Unlock()

	_, ok := jumpRegistry.live[c]

	return ok
}

// registryLen returns the number of live registry entries.
func registryLen() int {
	jumpRegistry.mu.Lock()
	defer jumpRegistry.mu.Unlock()

	return len(jumpRegistry.live)
}

// TestJumpConn_CloseSignalsStubbornChild pins that a child which ignores
// end-of-file is still ended after the grace period, and that the terminate
// signal reaches its whole process group.
func TestJumpConn_CloseSignalsStubbornChild(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHHang,
		IgnoreEOF:  true,
		Descendant: true,
	})

	c := dialStandIn(t, standInSpec(s), testJumpAddr)
	descendant := testhelper.WaitPID(t, s.DescendantPIDFile, 5*time.Second)

	start := time.Now()
	require.NoError(t, c.Close())
	assert.Less(t, time.Since(start), 100*time.Millisecond, "Close must not block")

	requireExited(t, c, 5*time.Second)
	assert.GreaterOrEqual(t, time.Since(start), jumpCloseGrace-100*time.Millisecond,
		"the child gets its grace period before it is signalled")
	requireSignaled(t, c, syscall.SIGTERM)
	requireGone(t, descendant, 5*time.Second, "the descendant")
}

// TestShutdownJumps_TerminatesUnreadChildAtOnce pins that a child which never
// read a byte, and so may be stuck in authentication ignoring its closed
// pipes, is terminated at once rather than after the bound.
//
// The bound is a minute and the wait for ShutdownJumps to return is bounded
// well below it, so the check separates "terminated at once" from "waited
// out the bound" by a wide margin that scheduler load cannot close. The
// SIGTERM is the proof of the behaviour itself: a child left to the bound
// would have been killed with SIGKILL instead.
func TestShutdownJumps_TerminatesUnreadChildAtOnce(t *testing.T) {
	t.Cleanup(ReopenJumps)

	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang, IgnoreEOF: true})

	c := dialStandIn(t, standInSpec(s), testJumpAddr)
	testhelper.WaitPID(t, s.PIDFile, 5*time.Second)

	const (
		bound    = time.Minute
		returnBy = 15 * time.Second
	)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		ShutdownJumps(bound)
	}()

	select {
	case <-returned:
	case <-time.After(returnBy):
		KillJumps()
		t.Fatalf("ShutdownJumps(%s) did not return within %s, so it waited on an unread child", bound, returnBy)
	}

	requireExited(t, c, 5*time.Second)
	requireSignaled(t, c, syscall.SIGTERM)
}

// TestShutdownJumps_KillsProcessGroupAtDeadline pins that an established
// child which ignores end-of-file is killed at the deadline together with its
// descendant, and that ShutdownJumps leaves the registry empty.
func TestShutdownJumps_KillsProcessGroupAtDeadline(t *testing.T) {
	t.Cleanup(ReopenJumps)

	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHForward,
		IgnoreEOF:  true,
		Descendant: true,
	})
	ln := testServer(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("x"))
		holdUntilClosed(conn)
	})

	c := dialStandIn(t, standInSpec(s), ln.Addr().String())

	_, err := io.ReadFull(c, make([]byte, 1))
	require.NoError(t, err, "the child must be established before the shutdown")

	self := testhelper.WaitPID(t, s.PIDFile, 5*time.Second)
	descendant := testhelper.WaitPID(t, s.DescendantPIDFile, 5*time.Second)

	const bound = 500 * time.Millisecond

	start := time.Now()
	ShutdownJumps(bound)
	returned := time.Now()

	assert.GreaterOrEqual(t, returned.Sub(start), bound-50*time.Millisecond,
		"an established child gets until the bound to exit on end-of-file")
	assert.Less(t, returned.Sub(start), bound+jumpShutdownSettle+500*time.Millisecond)

	requireGone(t, self, time.Second, "the stand-in")
	requireGone(t, descendant, time.Second, "the descendant")
	requireSignaled(t, c, syscall.SIGKILL)
	require.Eventually(t, func() bool { return registryLen() == 0 }, time.Second, 10*time.Millisecond,
		"the registry must be empty after the shutdown")
}

// TestJumpConn_ExitSeenWhileDescendantHoldsStderr pins that a descendant
// holding standard error open can neither hide ssh's 255 exit nor end the
// registry entry early, and that the shutdown still reaches it.
func TestJumpConn_ExitSeenWhileDescendantHoldsStderr(t *testing.T) {
	t.Cleanup(ReopenJumps)

	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHFail,
		Descendant: true,
		Stderr:     []string{"kex_exchange_identification: Connection closed by remote host"},
		ExitStatus: 255,
	})

	c := dialStandIn(t, standInSpec(s), testJumpAddr)

	_, err := c.Read(make([]byte, 16))
	je := requireTerminal(t, err)
	assert.Equal(t, 255, je.ExitStatus)
	assert.Equal(t, "kex_exchange_identification: Connection closed by remote host", je.Stderr)

	requireExited(t, c, time.Second)

	descendant := testhelper.WaitPID(t, s.DescendantPIDFile, 5*time.Second)
	require.False(t, testhelper.ProcessGone(descendant), "the descendant must still hold standard error")

	select {
	case <-c.stderrDone:
		t.Fatal("the standard-error copy must not reach end-of-file while the descendant holds the pipe")
	default:
	}

	assert.True(t, registered(c), "the entry must outlive Wait while standard error is still held")

	ShutdownJumps(2 * time.Second)

	requireGone(t, descendant, time.Second, "the descendant")
	assert.False(t, registered(c))
}

// TestShutdownJumps_ClosesRegistry pins that a dial whose child registers
// after the shutdown gets that child killed at once and fails terminally.
func TestShutdownJumps_ClosesRegistry(t *testing.T) {
	t.Cleanup(ReopenJumps)

	ShutdownJumps(time.Second)

	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang, IgnoreEOF: true})

	_, err := JumpDialContext(standInSpec(s))(context.Background(), "tcp", testJumpAddr)

	je := requireTerminal(t, err)
	require.ErrorIs(t, je.Err, errJumpsShutdown)
	assert.Contains(t, je.Detail(), "shut down")

	require.Eventually(t, func() bool { return len(s.LivePIDs(t)) == 0 }, time.Second, 10*time.Millisecond,
		"the refused child must be killed at once")
}

// TestKillJumps_KillsGroupsWithoutWaiting pins the second-signal path: the
// kill goes out at once, nothing waits for a reap, and the registry refuses
// new children afterwards.
func TestKillJumps_KillsGroupsWithoutWaiting(t *testing.T) {
	t.Cleanup(ReopenJumps)

	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHHang,
		IgnoreEOF:  true,
		Descendant: true,
	})

	c := dialStandIn(t, standInSpec(s), testJumpAddr)
	self := testhelper.WaitPID(t, s.PIDFile, 5*time.Second)
	descendant := testhelper.WaitPID(t, s.DescendantPIDFile, 5*time.Second)

	start := time.Now()
	KillJumps()
	assert.Less(t, time.Since(start), 50*time.Millisecond, "KillJumps must never wait")

	requireGone(t, self, time.Second, "the stand-in")
	requireGone(t, descendant, time.Second, "the descendant")
	requireExited(t, c, time.Second)
	requireSignaled(t, c, syscall.SIGKILL)

	_, err := JumpDialContext(standInSpec(s))(context.Background(), "tcp", testJumpAddr)

	je := requireTerminal(t, err)
	require.ErrorIs(t, je.Err, errJumpsShutdown)
}

// TestJumpCommand_IsolatedFromTerminal pins that the child can prompt
// nowhere: no controlling terminal, no askpass, and no display for an older
// ssh to fall through to.
func TestJumpCommand_IsolatedFromTerminal(t *testing.T) {
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("SSH_ASKPASS_REQUIRE", "force")

	cmd := newJumpCommand("", []string{"-W", testJumpAddr, "--", "bastion"})

	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setsid, "the child must run in a session of its own")
	assert.Equal(t, jumpWaitDelay, cmd.WaitDelay)

	assert.Contains(t, cmd.Env, "SSH_ASKPASS_REQUIRE=never")
	assert.NotContains(t, cmd.Env, "SSH_ASKPASS_REQUIRE=force")
	assert.False(t, slices.ContainsFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "DISPLAY=") || strings.HasPrefix(kv, "WAYLAND_DISPLAY=")
	}), "no display variable may reach the child")
}
