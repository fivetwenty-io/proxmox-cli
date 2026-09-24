package apiclient

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// assertChildExitedNormally checks that a reaped child exited on its own
// rather than by a signal. The Unix test file installs the real check; where
// the platform keeps no signal state there is nothing to assert.
var assertChildExitedNormally = func(t *testing.T, ps *os.ProcessState) {
	t.Helper()
	require.NotNil(t, ps)
}

// testJumpAddr is the forwarded target for tests whose stand-in never
// forwards, so their messages are fixed strings.
const testJumpAddr = "10.0.0.5:8006"

// TestApplyJumpSpec_EmptyChainLeavesDirectDial pins the opt-in: a context
// with no jump host configured must dial exactly as it did before this
// existed, which for the SDK means leaving DialContext nil so its own
// DialTimeoutSec still governs.
func TestApplyJumpSpec_EmptyChainLeavesDirectDial(t *testing.T) {
	for _, jump := range []string{"", "   "} {
		opts := ApplyJumpSpec(pve.Options{Host: "pve", DialTimeoutSec: 5},
			JumpSpec{Chain: jump, ConnectTimeout: 5 * time.Second})

		assert.Nil(t, opts.DialContext, "chain %q must not install a dialer", jump)
		assert.Equal(t, 5, opts.DialTimeoutSec, "the direct dial timeout must survive untouched")
	}
}

// TestApplyJumpSpec_InstallsDialer covers the other half: a configured jump
// host is what makes the API reachable at all, so the dialer has to be there.
func TestApplyJumpSpec_InstallsDialer(t *testing.T) {
	opts := ApplyJumpSpec(pve.Options{Host: "pve"}, JumpSpec{Chain: "admin@bastion", ConnectTimeout: time.Second})
	require.NotNil(t, opts.DialContext)
}

// jumpArgsHead is the fixed option prefix every argument list starts with,
// for a given ConnectTimeout value.
func jumpArgsHead(connectTimeout string) []string {
	head := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + connectTimeout}
	head = append(head, sshcmd.KeepaliveOptionArgs()...)

	return append(head, "-o", "LogLevel=INFO")
}

// TestJumpSSHArgs covers the argument shapes that decide whether ssh forwards
// to the right place. The single-hop and multi-hop forms differ structurally
// (-W takes exactly one destination), and the final hop's port needs -p
// because ssh's destination argument does not accept host:port the way -J
// does — getting that wrong would silently ssh to port 22 of the bastion
// instead of the port the operator configured.
func TestJumpSSHArgs(t *testing.T) {
	tests := []struct {
		name string
		jump string
		want []string
	}{
		{
			name: "single hop",
			jump: "bastion",
			want: []string{"-W", testJumpAddr, "--", "bastion"},
		},
		{
			name: "user and port",
			jump: "admin@bastion:2222",
			want: []string{"-p", "2222", "-W", testJumpAddr, "--", "admin@bastion"},
		},
		{
			name: "chain puts every hop but the last behind -J",
			jump: "edge,admin@inner:2222",
			want: []string{"-J", "edge", "-p", "2222", "-W", testJumpAddr, "--", "admin@inner"},
		},
		{
			name: "spaced chain is trimmed",
			jump: "edge1, edge2 , bastion",
			want: []string{"-J", "edge1,edge2", "-W", testJumpAddr, "--", "bastion"},
		},
		{
			name: "URI hop is normalised",
			jump: "ssh://admin@bastion:2222",
			want: []string{"-p", "2222", "-W", testJumpAddr, "--", "admin@bastion"},
		},
		{
			name: "URI intermediate hop is normalised for -J",
			jump: "ssh://ops@edge:2200,bastion",
			want: []string{"-J", "ops@edge:2200", "-W", testJumpAddr, "--", "bastion"},
		},
		{
			name: "bracketed IPv6 literal with port",
			jump: "root@[2001:db8::1]:2222",
			want: []string{"-p", "2222", "-W", testJumpAddr, "--", "root@2001:db8::1"},
		},
		{
			name: "bracketed IPv6 intermediate hop keeps its brackets",
			jump: "[2001:db8::1]:2222,bastion",
			want: []string{"-J", "[2001:db8::1]:2222", "-W", testJumpAddr, "--", "bastion"},
		},
		{
			name: "bare IPv6 literal keeps every colon as host",
			jump: "2001:db8::1",
			want: []string{"-W", testJumpAddr, "--", "2001:db8::1"},
		},
		{
			name: "directory login splits at the last @",
			jump: "alice@corp.example@bastion",
			want: []string{"-W", testJumpAddr, "--", "alice@corp.example@bastion"},
		},
		{
			name: "URI directory login is percent-decoded",
			jump: "ssh://alice%40corp.example@bastion:2222",
			want: []string{"-p", "2222", "-W", testJumpAddr, "--", "alice@corp.example@bastion"},
		},
		{
			name: "config alias",
			jump: "my_bastion",
			want: []string{"-W", testJumpAddr, "--", "my_bastion"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jumpSSHArgs(JumpSpec{Chain: tc.jump, ConnectTimeout: 5 * time.Second}, testJumpAddr)
			require.NoError(t, err)

			head := jumpArgsHead("5")
			require.GreaterOrEqual(t, len(got), len(head))
			assert.Equal(t, head, got[:len(head)],
				"ssh must never prompt, must fail fast, must be bounded, and must speak at INFO")
			assert.Equal(t, tc.want, got[len(head):])
		})
	}
}

// TestJumpSSHArgs_EmptyFinalHopIsRejected guards a trailing comma, which would
// otherwise produce `ssh ... -W addr ""` and fail with an opaque ssh usage
// error rather than naming the misconfigured hop.
func TestJumpSSHArgs_EmptyFinalHopIsRejected(t *testing.T) {
	_, err := jumpSSHArgs(JumpSpec{Chain: "bastion,", ConnectTimeout: time.Second}, testJumpAddr)

	require.EqualError(t, err, "hop 2 is empty")
}

// TestJumpSSHArgs_RejectsInvalidChain pins that the argument builder fails
// closed on its own, so a caller that skipped the resolver cannot hand a
// shell metacharacter to an OpenSSH that expands -J under /bin/sh -c.
func TestJumpSSHArgs_RejectsInvalidChain(t *testing.T) {
	_, err := jumpSSHArgs(JumpSpec{Chain: "x;id,h", ConnectTimeout: time.Second}, testJumpAddr)

	require.EqualError(t, err, `hop 1: host "x;id" is not a hostname, IPv4 address, or bracketed IPv6 literal`)
}

// TestJumpSSHArgs_NeverPassesNodeLogin pins that the bastion takes its user
// and port from the hop string alone: the node's ssh user, identity, and port
// must never leak onto the bastion hop.
func TestJumpSSHArgs_NeverPassesNodeLogin(t *testing.T) {
	bare, err := jumpSSHArgs(JumpSpec{Chain: "bastion", ConnectTimeout: time.Second}, testJumpAddr)
	require.NoError(t, err)

	for _, flag := range []string{"-l", "-i", "-p"} {
		assert.NotContains(t, bare, flag, "a bare hop must not carry %s", flag)
	}

	full, err := jumpSSHArgs(JumpSpec{Chain: "admin@bastion:2222", ConnectTimeout: time.Second}, testJumpAddr)
	require.NoError(t, err)

	assert.NotContains(t, full, "-l")
	assert.NotContains(t, full, "-i")

	p := slices.Index(full, "-p")
	require.GreaterOrEqual(t, p, 0)
	assert.Equal(t, "2222", full[p+1])
	assert.Equal(t, "admin@bastion", full[len(full)-1])
}

// TestJumpSSHArgs_ConnectTimeoutFromContext pins the rounding: up to whole
// seconds with a floor of one, because ssh reads ConnectTimeout=0 as "no
// bound" and rounding down would shorten what the operator asked for.
func TestJumpSSHArgs_ConnectTimeoutFromContext(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{20 * time.Second, "ConnectTimeout=20"},
		{500 * time.Millisecond, "ConnectTimeout=1"},
		{1400 * time.Millisecond, "ConnectTimeout=2"},
		{time.Second, "ConnectTimeout=1"},
		{0, "ConnectTimeout=1"},
	}

	for _, tc := range tests {
		t.Run(tc.in.String(), func(t *testing.T) {
			got, err := jumpSSHArgs(JumpSpec{Chain: "bastion", ConnectTimeout: tc.in}, testJumpAddr)
			require.NoError(t, err)

			i := slices.Index(got, tc.want)
			require.GreaterOrEqual(t, i, 1, "want %s in %v", tc.want, got)
			assert.Equal(t, "-o", got[i-1])
		})
	}
}

// TestJumpSSHArgs_KeepaliveAndSeparator pins the three properties that keep
// the hop bounded, readable, and safe: the keepalive pair, a single pinned
// LogLevel=INFO, and a "--" right before the destination.
func TestJumpSSHArgs_KeepaliveAndSeparator(t *testing.T) {
	got, err := jumpSSHArgs(JumpSpec{Chain: "edge,bastion", ConnectTimeout: time.Second}, testJumpAddr)
	require.NoError(t, err)

	joined := strings.Join(got, "\x00")
	assert.Contains(t, joined, strings.Join(sshcmd.KeepaliveOptionArgs(), "\x00"))

	levels := 0
	for i, a := range got {
		if strings.HasPrefix(strings.ToLower(a), "loglevel") {
			levels++
			assert.Equal(t, "LogLevel=INFO", a)
			assert.Equal(t, "-o", got[i-1])
		}
	}

	assert.Equal(t, 1, levels, "exactly one LogLevel option")
	assert.Equal(t, "--", got[len(got)-2])
	assert.Equal(t, "bastion", got[len(got)-1])
}

// TestValidateJumpChain pins the allow-list and its fixed messages. Every
// rejected input is one that an OpenSSH before 10.3 would otherwise have
// expanded into a ProxyCommand under /bin/sh -c, or one OpenSSH itself
// rejects.
func TestValidateJumpChain(t *testing.T) {
	rejected := []struct {
		chain string
		want  string
	}{
		{"x;id,h", `hop 1: host "x;id" is not a hostname, IPv4 address, or bracketed IPv6 literal`},
		{
			"-oProxyCommand=id,h",
			`hop 1: host "-oProxyCommand=id" is not a hostname, IPv4 address, or bracketed IPv6 literal`,
		},
		{"a$(id)@h", `hop 1: user "a$(id)" has a disallowed character`},
		{"bas\ntion", `hop 1: host "bas\ntion" is not a hostname, IPv4 address, or bracketed IPv6 literal`},
		{"bastion\n", `hop 1: host "bastion\n" is not a hostname, IPv4 address, or bracketed IPv6 literal`},
		{"", "hop 1 is empty"},
		{",bastion", "hop 1 is empty"},
		{"edge,,bastion", "hop 2 is empty"},
		{"bastion, ", "hop 2 is empty"},
		{"2001:db8::1,bastion", `hop 1: host "2001:db8::1" is not a hostname, IPv4 address, or bracketed IPv6 literal`},
		{
			"ssh://alice@corp.example@bastion:2222",
			`hop 1: host "corp.example@bastion" is not a hostname, IPv4 address, or bracketed IPv6 literal`,
		},
		{"bastion:0", `hop 1: port "0" is out of range [1, 65535]`},
		{"bastion:65536", `hop 1: port "65536" is out of range [1, 65535]`},
		{"bastion:22x", `hop 1: port "22x" is out of range [1, 65535]`},
		{"bastion:", `hop 1: port "" is out of range [1, 65535]`},
		{"ssh://al%zzice@bastion", `hop 1: user "al%zzice" has a disallowed character`},
		{"al%40ice@bastion", `hop 1: user "al%40ice" has a disallowed character`},
		{"@bastion", `hop 1: user "" has a disallowed character`},
		{"[bastion]:22", `hop 1: host "[bastion]:22" is not a hostname, IPv4 address, or bracketed IPv6 literal`},
		{"[2001:db8::1]x", `hop 1: host "[2001:db8::1]x" is not a hostname, IPv4 address, or bracketed IPv6 literal`},
		{"ssh://2001:db8::1", `hop 1: host "2001:db8::1" is not a hostname, IPv4 address, or bracketed IPv6 literal`},
		{"edge,bas`id`tion", "hop 2: host \"bas`id`tion\" is not a hostname, IPv4 address, or bracketed IPv6 literal"},
	}

	for _, tc := range rejected {
		t.Run("rejects "+tc.chain, func(t *testing.T) {
			require.EqualError(t, ValidateJumpChain(tc.chain), tc.want)
		})
	}

	for _, chain := range []string{
		"2001:db8::1",
		"bastion,2001:db8::1",
		"my_bastion",
		"bastion1, bastion2",
		"ssh://admin@bastion:2222",
		"ssh://bastion",
		"alice@corp.example@bastion",
		"ssh://alice%40corp.example@bastion:2222",
		"admin@bastion:2222",
		"edge,admin@inner:2222",
		"root@[2001:db8::1]:2222",
		"192.0.2.10:22",
		"ops@edge.example.com,ssh://root@[2001:db8::2]:2200,bastion",
	} {
		t.Run("accepts "+chain, func(t *testing.T) {
			require.NoError(t, ValidateJumpChain(chain))
		})
	}
}

// TestJumpError_Detail pins the fallback order, so no caller can ever print
// an empty detail.
func TestJumpError_Detail(t *testing.T) {
	cause := errors.New("exec: boom")

	tests := []struct {
		name string
		err  JumpError
		want string
	}{
		{
			"timer wins",
			JumpError{TimedOut: true, Timeout: 300 * time.Millisecond, Stderr: "x"},
			"no response within 300ms",
		},
		{"stderr", JumpError{Stderr: "Permission denied (publickey).", Err: cause}, "Permission denied (publickey)."},
		{"cause", JumpError{Err: cause, ExitStatus: -1}, "exec: boom"},
		{"signal", JumpError{Signaled: true, ExitStatus: -1}, "ssh was ended by a signal and printed nothing"},
		{"status", JumpError{ExitStatus: 255}, "ssh exited with status 255 and printed nothing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.err
			e.Chain, e.Addr = "bastion", testJumpAddr

			assert.Equal(t, tc.want, e.Detail())
			assert.Equal(t, "ssh jump bastion -> "+testJumpAddr+": "+tc.want, e.Error())
			assert.ErrorIs(t, &e, ErrJump)
		})
	}

	withCause := &JumpError{Err: cause}
	assert.ErrorIs(t, withCause, cause)
	assert.ErrorIs(t, withCause, ErrJump)
}

// TestJumpStderr_KeepsOpenFailedAndLastLine pins which lines a JumpError
// reports: the reason a channel open failed, which ssh prints before its
// fatal line, together with that fatal line, each without its "\r".
func TestJumpStderr_KeepsOpenFailedAndLastLine(t *testing.T) {
	var s jumpStderr

	assert.Empty(t, s.text())

	s.add([]byte("banner line\r\n"))
	assert.Equal(t, "banner line", s.text())

	s.add([]byte("channel 0: open failed: connect failed: No route to host\r\n"))
	assert.Equal(t, "channel 0: open failed: connect failed: No route to host", s.text(),
		"an open-failed line that is itself the last line is reported once")

	s.add([]byte("\r\n"))
	s.add([]byte("stdio forwarding failed\r\n"))
	assert.Equal(t, "channel 0: open failed: connect failed: No route to host; stdio forwarding failed", s.text())
}

// TestJumpDialContext_RejectsNonTCP pins the one network a forwarded ssh
// stream cannot carry. Returning a clear error beats handing back a stream
// that behaves like a broken TCP connection.
func TestJumpDialContext_RejectsNonTCP(t *testing.T) {
	_, err := JumpDialContext(JumpSpec{Chain: "bastion", ConnectTimeout: time.Second})(
		context.Background(), "udp", testJumpAddr)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tcp only")
}

// TestJumpDialContext_InvalidChainIsTerminal pins that a chain the allow-list
// rejects fails as a terminal dial error rather than something the kit
// retries four times.
func TestJumpDialContext_InvalidChainIsTerminal(t *testing.T) {
	_, err := JumpDialContext(JumpSpec{Chain: "x;id", ConnectTimeout: time.Second})(
		context.Background(), "tcp", testJumpAddr)

	je := requireTerminal(t, err)
	assert.Contains(t, je.Detail(), `host "x;id"`)
}

// testServer is a loopback listener whose handler runs once per accepted
// connection.
func testServer(t *testing.T, handle func(net.Conn)) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var wg sync.WaitGroup

	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})

	wg.Go(func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}

			wg.Go(func() {
				defer func() { _ = conn.Close() }()

				handle(conn)
			})
		}
	})

	return ln
}

// holdUntilClosed keeps a server connection open until the peer closes it.
func holdUntilClosed(conn net.Conn) {
	_, _ = io.Copy(io.Discard, conn)
}

// standInSpec is a JumpSpec that runs the stand-in.
func standInSpec(s testhelper.SSHScript) JumpSpec {
	return JumpSpec{Chain: "bastion", ConnectTimeout: time.Second, Program: s.Program}
}

// dialStandIn dials addr through a fresh dialer for spec and closes the
// connection when the test ends.
func dialStandIn(t *testing.T, spec JumpSpec, addr string) *jumpConn {
	t.Helper()

	conn, err := JumpDialContext(spec)(context.Background(), "tcp", addr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	jc, ok := conn.(*jumpConn)
	require.True(t, ok)

	return jc
}

// requireTerminal asserts the terminal shape: a dial OpError holding a
// *JumpError that wraps ErrJump.
func requireTerminal(t *testing.T, err error) *JumpError {
	t.Helper()

	require.Error(t, err)

	var op *net.OpError
	require.ErrorAs(t, err, &op, "a terminal failure must be a *net.OpError")
	assert.Equal(t, "dial", op.Op)

	var je *JumpError
	require.ErrorAs(t, err, &je)
	require.ErrorIs(t, err, ErrJump)

	return je
}

// requireExited waits up to bound for the reaper to see the child's exit.
func requireExited(t *testing.T, c *jumpConn, bound time.Duration) {
	t.Helper()

	select {
	case <-c.exited:
	case <-time.After(bound):
		t.Fatalf("ssh child not reaped within %s", bound)
	}
}

// TestJumpConn_CarriesBytesBothWays exercises the net.Conn adapter against a
// real process and a real listener, standing in for ssh with a script that
// forwards stdin/stdout the same way `ssh -W` does. It is the check that the
// pipe plumbing, the deadline support net.Conn requires, and the teardown all
// work, without needing an actual bastion.
func TestJumpConn_CarriesBytesBothWays(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

	ln := testServer(t, func(conn net.Conn) {
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}

		_, _ = conn.Write([]byte("PONG!"))
		holdUntilClosed(conn)
	})

	conn := dialStandIn(t, standInSpec(s), ln.Addr().String())

	// Windows pipes are not registered with the runtime poller, so every
	// deadline method returns os.ErrNoDeadline there.
	if runtime.GOOS != "windows" {
		require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	}

	_, err := conn.Write([]byte("PING!"))
	require.NoError(t, err)

	buf := make([]byte, 5)
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, "PONG!", string(buf))

	argv := s.Invocations(t)
	require.Len(t, argv, 1)
	assert.Equal(t, []string{"-W", ln.Addr().String(), "--", "bastion"}, argv[0][len(argv[0])-4:])
}

// TestJumpConn_CloseTerminatesChild pins that Close returns without waiting
// on the child, and that ssh, seeing end-of-file, exits on its own rather
// than on the grace period's signal.
//
// Every step is ordered by an event, not by the clock, so scheduler load
// cannot fail it. The stand-in writes its marker on end-of-file and then
// holds its exit until the test releases it, so the child provably still runs
// when Close returns. The grace is raised past the sum of the waits that
// follow Close, so no signal can reach the child while the test waits, and
// any signal in its exit state is a regression.
//
// The bounds only stop a hang. The start bound is the widest because on a
// loaded macOS host the first exec of a freshly written script can stall for
// tens of seconds before bash runs its first line.
func TestJumpConn_CloseTerminatesChild(t *testing.T) {
	const (
		startGuard = 2 * time.Minute
		stepGuard  = time.Minute
		grace      = 10 * time.Minute
	)

	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward, HoldExit: true})
	ln := testServer(t, holdUntilClosed)

	d := &jumpDialer{spec: standInSpec(s), hooks: jumpHooks{closeGrace: grace}}

	conn, err := d.dial(context.Background(), "tcp", ln.Addr().String())
	require.NoError(t, err)

	c, ok := conn.(*jumpConn)
	require.True(t, ok)

	// Cleanups run last-in first-out, so on a failed run this one ends the
	// held child before the test server waits for its connection to close,
	// rather than leaving it to the raised grace.
	t.Cleanup(func() {
		_ = c.Close()
		_ = os.WriteFile(s.ExitGate, nil, 0o600)

		select {
		case <-c.exited:
		default:
			killJumpChild(c.cmd.Process)
		}
	})

	testhelper.WaitPID(t, s.PIDFile, startGuard)

	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()

	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(stepGuard):
		t.Fatalf("Close did not return within %s, so it waits on the child", stepGuard)
	}

	select {
	case <-c.exited:
		t.Fatal("Close must not block: the held child had already exited when it returned")
	default:
	}

	// The wait also ends when the child exits, so an early signal fails at
	// once instead of after the whole bound.
	require.Eventually(t, func() bool {
		if _, serr := os.Stat(s.MarkerFile); serr == nil {
			return true
		}

		select {
		case <-c.exited:
			return true
		default:
			return false
		}
	}, stepGuard, 10*time.Millisecond, "the stand-in neither saw end-of-file nor exited")

	_, err = os.Stat(s.MarkerFile)
	require.NoError(t, err, "the stand-in must see end-of-file once Close returns")

	select {
	case <-c.exited:
		t.Fatalf("the child exited before the test released it: %v", c.cmd.ProcessState)
	default:
	}

	s.ReleaseExit(t)

	requireExited(t, c, stepGuard)
	assertChildExitedNormally(t, c.cmd.ProcessState)
}

// TestJumpConn_SurvivesDialContextCancel pins that the child belongs to the
// connection, not to the dial context.
func TestJumpConn_SurvivesDialContextCancel(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

	ln := testServer(t, func(conn net.Conn) {
		buf := make([]byte, 4)
		for {
			if _, err := io.ReadFull(conn, buf); err != nil {
				return
			}

			if _, err := conn.Write(buf); err != nil {
				return
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())

	conn, err := JumpDialContext(standInSpec(s))(ctx, "tcp", ln.Addr().String())
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	cancel()

	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)

	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(buf))

	select {
	case <-conn.(*jumpConn).exited:
		t.Fatal("cancelling the dial context must not end the child")
	default:
	}
}

// TestJumpDial_CancelledContextStartsNoChild pins the existing ctx.Err()
// check: a dial whose context is already done spawns nothing.
func TestJumpDial_CancelledContextStartsNoChild(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := JumpDialContext(standInSpec(s))(ctx, "tcp", testJumpAddr)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, conn)

	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, s.Invocations(t))
	assert.Empty(t, s.LivePIDs(t))
}

// TestJumpConn_SurfacesSSHStderr pins that ssh's own message reaches the
// operator from both Read and Write, which run on separate goroutines in the
// transport, without a race on the standard-error buffer.
func TestJumpConn_SurfacesSSHStderr(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHFail,
		Stderr:     []string{"debug noise", "admin@bastion: Permission denied (publickey)."},
		ExitStatus: 255,
	})

	c := dialStandIn(t, standInSpec(s), testJumpAddr)
	want := "ssh jump bastion -> " + testJumpAddr + ": admin@bastion: Permission denied (publickey)."

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, err := c.Read(buf)
		readErr <- err
	}()

	writeErr := make(chan error, 1)
	go func() {
		chunk := make([]byte, 1024)
		deadline := time.Now().Add(10 * time.Second)

		for time.Now().Before(deadline) {
			if _, err := c.Write(chunk); err != nil {
				writeErr <- err

				return
			}
		}

		writeErr <- errors.New("write never failed")
	}()

	var rerr error
	select {
	case rerr = <-readErr:
	case <-time.After(10 * time.Second):
		t.Fatal("Read never returned")
	}

	var je *JumpError
	require.ErrorAs(t, rerr, &je)
	assert.Equal(t, want, je.Error())

	var werr error
	select {
	case werr = <-writeErr:
	case <-time.After(10 * time.Second):
		t.Fatal("Write never returned")
	}

	var wje *JumpError
	require.ErrorAs(t, werr, &wje)
	assert.Equal(t, want, wje.Error())
}

// TestJumpDial_FailureIsTerminalOpError pins that a bastion that cannot reach
// the node costs one attempt and says why: ssh's INFO-level open-failed line
// and its fatal line both reach the message, without their "\r", and the
// kit sees a dial OpError it will not retry.
func TestJumpDial_FailureIsTerminalOpError(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHFail,
		Stderr:     []string{"channel 0: open failed: connect failed: Connection refused", "stdio forwarding failed"},
		ExitStatus: 255,
	})

	c := dialStandIn(t, standInSpec(s), testJumpAddr)

	// Time only what pmx controls: once the stand-in has started, it writes
	// its two lines and exits at once, so the Read returns as soon as the
	// standard-error copy reaches end-of-file.
	testhelper.WaitPID(t, s.PIDFile, 5*time.Second)

	start := time.Now()
	_, err := c.Read(make([]byte, 16))
	elapsed := time.Since(start)

	je := requireTerminal(t, err)
	assert.Equal(t, "ssh jump bastion -> "+testJumpAddr+
		": channel 0: open failed: connect failed: Connection refused; stdio forwarding failed", je.Error())
	assert.NotContains(t, je.Error(), "\r")
	assert.Equal(t, 255, je.ExitStatus)
	assert.Less(t, elapsed, 200*time.Millisecond,
		"the parent's copy of the stderr write end must be closed, or every error waits out the budget")
}

// TestJumpDial_StartFailureCarriesCause pins that a missing ssh names itself
// rather than printing an empty detail. Only a bare name that exec.LookPath
// fails to find yields exec.ErrNotFound; a missing absolute path would fail
// at fork/exec with a path error instead.
func TestJumpDial_StartFailureCarriesCause(t *testing.T) {
	spec := JumpSpec{Chain: "bastion", ConnectTimeout: time.Second, Program: "pmx-test-no-such-ssh"}

	_, err := JumpDialContext(spec)(context.Background(), "tcp", testJumpAddr)

	je := requireTerminal(t, err)
	require.ErrorIs(t, je.Err, exec.ErrNotFound)
	assert.True(t, strings.HasSuffix(je.Error(), je.Err.Error()), "message %q must end with the exec error", je.Error())
	assert.Equal(t, -1, je.ExitStatus)
}

// TestJumpDial_SilentExitNamesStatus pins that an ssh that prints nothing
// still yields a detail.
func TestJumpDial_SilentExitNamesStatus(t *testing.T) {
	t.Run("exit status", func(t *testing.T) {
		s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHFail, ExitStatus: 255})

		c := dialStandIn(t, standInSpec(s), testJumpAddr)
		_, err := c.Read(make([]byte, 16))

		je := requireTerminal(t, err)
		assert.Equal(t, 255, je.ExitStatus)
		assert.False(t, je.Signaled)
		assert.Equal(t, "ssh exited with status 255 and printed nothing", je.Detail())
	})

	t.Run("signal", func(t *testing.T) {
		s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHFail, KillSelf: true})

		c := dialStandIn(t, standInSpec(s), testJumpAddr)
		_, err := c.Read(make([]byte, 16))

		var je *JumpError
		require.ErrorAs(t, err, &je)
		require.ErrorIs(t, err, ErrJump)
		assert.True(t, je.Signaled)
		assert.Equal(t, -1, je.ExitStatus)
		assert.Equal(t, "ssh was ended by a signal and printed nothing", je.Detail())

		// Before the first byte only a 255 exit or the timer is terminal, and
		// a signal that pmx did not send is not ssh's own verdict.
		var op *net.OpError
		assert.False(t, errors.As(err, &op), "a signal death must not be a terminal dial error")
	})
}

// TestJumpDial_TimerFiresBeforeFirstByte pins the jump's own first-byte
// timer: a bastion that never answers becomes a terminal failure the kit
// does not retry, and the child is ended at once rather than after Close's
// grace.
func TestJumpDial_TimerFiresBeforeFirstByte(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang, IgnoreEOF: true})

	spec := standInSpec(s)
	spec.FirstByteTimeout = 300 * time.Millisecond

	c := dialStandIn(t, spec, testJumpAddr)

	start := time.Now()
	_, err := c.Read(make([]byte, 16))
	fired := time.Now()

	je := requireTerminal(t, err)
	assert.True(t, je.TimedOut)
	assert.Equal(t, -1, je.ExitStatus, "the timer builds its error before the exit is known")
	assert.Equal(t, "no response within 300ms", je.Detail())
	assert.GreaterOrEqual(t, fired.Sub(start), 250*time.Millisecond)

	requireExited(t, c, 10*time.Second)
	assert.Less(t, time.Since(fired), time.Second, "the timer must end the child at once")
}

// TestJumpConn_NonTerminalExitIsNotDialError pins that a failure after the
// first byte stays retryable under the kit's ordinary rules.
func TestJumpConn_NonTerminalExitIsNotDialError(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHForward,
		Stderr:     []string{"Connection to bastion closed by remote host."},
		ExitStatus: 1,
	})
	ln := testServer(t, func(conn net.Conn) { _, _ = conn.Write([]byte("x")) })

	c := dialStandIn(t, standInSpec(s), ln.Addr().String())

	data, err := io.ReadAll(c)
	assert.Equal(t, "x", string(data))

	var je *JumpError
	require.ErrorAs(t, err, &je)
	assert.Equal(t, 1, je.ExitStatus)

	var op *net.OpError
	assert.False(t, errors.As(err, &op), "a failure after the first byte must not be a dial error")
}

// TestJumpConn_EOFAfterCleanExit pins that a clean exit after the first byte
// is a plain io.EOF, so a server closing an idle keep-alive connection
// reaches the transport's own handling, even when ssh printed something.
func TestJumpConn_EOFAfterCleanExit(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:   testhelper.SSHForward,
		Stderr: []string{"Banner: authorised use only"},
	})
	ln := testServer(t, func(conn net.Conn) { _, _ = conn.Write([]byte("x")) })

	c := dialStandIn(t, standInSpec(s), ln.Addr().String())

	buf := make([]byte, 1)
	_, err := io.ReadFull(c, buf)
	require.NoError(t, err)

	_, err = c.Read(buf)
	// Identity is the point: the transport compares with ==.
	assert.True(t, err == io.EOF, "want io.EOF unchanged, got %v", err) //nolint:errorlint // identity is the contract
}

// TestJumpDialContext_FailureMemo pins that a terminal failure costs one
// authentication per dialer for ten seconds, and that the memo is per
// dialer.
func TestJumpDialContext_FailureMemo(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHFail,
		Stderr:     []string{"admin@bastion: Permission denied (publickey)."},
		ExitStatus: 255,
	})

	dial := JumpDialContext(standInSpec(s))

	conn, err := dial(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	_, first := conn.Read(make([]byte, 16))
	_ = requireTerminal(t, first)

	start := time.Now()
	_, second := dial(context.Background(), "tcp", testJumpAddr)
	assert.Less(t, time.Since(start), 100*time.Millisecond, "the memo must answer at once")

	_ = requireTerminal(t, second)
	assert.Equal(t, first.Error(), second.Error())
	assert.Len(t, s.Invocations(t), 1, "the memo must not start ssh again")

	fresh, err := JumpDialContext(standInSpec(s))(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = fresh.Close() })

	require.Eventually(t, func() bool { return len(s.Invocations(t)) == 2 }, 5*time.Second, 10*time.Millisecond,
		"a fresh dialer starts ssh again")
}

// dialResult is one background dial's outcome.
type dialResult struct {
	conn net.Conn
	err  error
}

// dialAsync starts a dial in the background and closes any connection it
// returns when the test ends.
func dialAsync(
	t *testing.T, dial func(context.Context, string, string) (net.Conn, error), addr string,
) <-chan dialResult {
	t.Helper()

	ch := make(chan dialResult, 1)

	go func() {
		conn, err := dial(context.Background(), "tcp", addr)
		ch <- dialResult{conn, err}
	}()

	t.Cleanup(func() {
		select {
		case r := <-ch:
			if r.conn != nil {
				_ = r.conn.Close()
			}
		default:
		}
	})

	return ch
}

// verdictNames renders a verdict in a trace event.
var verdictNames = map[jumpVerdict]string{
	verdictNone:        "none",
	verdictEstablished: "established",
	verdictTerminal:    "terminal",
	verdictNonTerminal: "non-terminal",
}

// verdictTrace records what a dialer's hooks report, as "<source> <verdict>"
// events in publication order and a count of finished reapers.
type verdictTrace struct {
	mu        sync.Mutex
	published []string
	reaped    int
}

func (tr *verdictTrace) events() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return slices.Clone(tr.published)
}

// awaitReaped waits up to bound for n reapers to have finished.
func (tr *verdictTrace) awaitReaped(t *testing.T, n int, bound time.Duration) {
	t.Helper()

	require.Eventually(t, func() bool {
		tr.mu.Lock()
		defer tr.mu.Unlock()

		return tr.reaped >= n
	}, bound, 10*time.Millisecond, "want %d reaper(s) finished within %s", n, bound)
}

// tracedDialer returns a dialer for spec whose hooks feed a verdictTrace.
// When hold is not nil, the reaper waits for it to close before it
// classifies an exit, so a test can let another path publish first.
func tracedDialer(spec JumpSpec, hold <-chan struct{}) (*jumpDialer, *verdictTrace) {
	tr := &verdictTrace{}

	d := &jumpDialer{spec: spec, hooks: jumpHooks{
		published: func(source string, v jumpVerdict) {
			tr.mu.Lock()
			tr.published = append(tr.published, source+" "+verdictNames[v])
			tr.mu.Unlock()
		},
		reaped: func() {
			tr.mu.Lock()
			tr.reaped++
			tr.mu.Unlock()
		},
	}}

	if hold != nil {
		d.hooks.classify = func() { <-hold }
	}

	return d, tr
}

// parkedTimer is a first-byte timeout no test waits out. A test arms it and
// fires it with fireFirstByteTimer once its stand-in has started, because a
// timer that runs from the dial races bash's start-up under parallel load.
const parkedTimer = time.Minute

// fireFirstByteTimer makes c's armed first-byte timer fire now, which drives
// the real timer path at a moment the test chooses.
func fireFirstByteTimer(t *testing.T, c *jumpConn) {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()

	require.NotNil(t, c.timer, "no first-byte timer is armed")
	require.True(t, c.timer.Reset(0), "the first-byte timer already fired or was stopped")
}

// requirePending asserts that a background dial has not returned within d.
func requirePending(t *testing.T, ch <-chan dialResult, d time.Duration, msg string) {
	t.Helper()

	select {
	case r := <-ch:
		if r.conn != nil {
			_ = r.conn.Close()
		}

		t.Fatalf("%s: dial returned (err %v)", msg, r.err)
	case <-time.After(d):
	}
}

// awaitDial waits up to d for a background dial to return.
func awaitDial(t *testing.T, ch <-chan dialResult, d time.Duration) dialResult {
	t.Helper()

	select {
	case r := <-ch:
		if r.conn != nil {
			t.Cleanup(func() { _ = r.conn.Close() })
		}

		return r
	case <-time.After(d):
		t.Fatalf("dial did not return within %s", d)

		return dialResult{}
	}
}

// TestJumpDialContext_SingleFlightBeforeFirstByte pins the gate: a dial that
// starts while an earlier dial has no verdict waits for it, takes a terminal
// failure from the memo, starts its own ssh once the earlier connection has
// read a byte, and, after an early non-terminal verdict, exactly one waiter
// becomes the new leader.
func TestJumpDialContext_SingleFlightBeforeFirstByte(t *testing.T) {
	// terminal runs one leader to a terminal verdict that source publishes,
	// and asserts that a waiter takes that verdict from the memo. For the
	// Read path the reaper is held back until the Read has returned, so the
	// Read path alone must have written the memo before it cleared the gate.
	terminal := func(t *testing.T, opts testhelper.SSHStandInOptions, timer time.Duration, source string) {
		t.Helper()

		s := testhelper.SSHStandIn(t, opts)
		spec := standInSpec(s)
		spec.FirstByteTimeout = timer

		var (
			hold    chan struct{}
			once    sync.Once
			release = func() {}
		)

		if source == "read" {
			hold = make(chan struct{})
			release = func() { once.Do(func() { close(hold) }) }

			// A failed assertion must not leave the reaper parked.
			t.Cleanup(release)
		}

		d, tr := tracedDialer(spec, hold)

		first, err := d.dial(context.Background(), "tcp", testJumpAddr)
		require.NoError(t, err)

		t.Cleanup(func() { _ = first.Close() })

		second := dialAsync(t, d.dial, testJumpAddr)
		requirePending(t, second, 100*time.Millisecond, "the second dial must wait for the first dial's verdict")

		// The argv record is written before the pid file, so the invocation
		// count below cannot miss the stand-in, and the timer fires only
		// once the stand-in is running.
		testhelper.WaitPID(t, s.PIDFile, 5*time.Second)

		if timer > 0 {
			fireFirstByteTimer(t, first.(*jumpConn))
		}

		var firstErr error
		if source == "read" {
			_, firstErr = first.Read(make([]byte, 16))
			_ = requireTerminal(t, firstErr)
		}

		r := awaitDial(t, second, 5*time.Second)
		je := requireTerminal(t, r.err)

		if firstErr != nil {
			assert.Equal(t, firstErr.Error(), r.err.Error())
		}

		assert.Len(t, s.Invocations(t), 1, "the waiter must take the memoised failure, not start ssh")

		if timer > 0 {
			assert.True(t, je.TimedOut)
		} else {
			assert.Equal(t, 255, je.ExitStatus)
		}

		release()
		tr.awaitReaped(t, 1, 10*time.Second)
		assert.Equal(t, []string{source + " terminal"}, tr.events(),
			"the %s path must publish the only verdict", source)
	}

	failing := testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHFail,
		StartDelay: 300 * time.Millisecond,
		Stderr:     []string{"admin@bastion: Permission denied (publickey)."},
		ExitStatus: 255,
	}

	t.Run("terminal through a Read", func(t *testing.T) { terminal(t, failing, 0, "read") })
	t.Run("terminal through the reaper", func(t *testing.T) { terminal(t, failing, 0, "reaper") })
	t.Run("terminal through the timer", func(t *testing.T) {
		terminal(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang, IgnoreEOF: true}, parkedTimer, "timer")
	})

	t.Run("first byte releases the waiter", func(t *testing.T) {
		s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})
		ln := testServer(t, func(conn net.Conn) {
			_, _ = conn.Write([]byte("hi"))
			holdUntilClosed(conn)
		})

		dial := JumpDialContext(standInSpec(s))

		first, err := dial(context.Background(), "tcp", ln.Addr().String())
		require.NoError(t, err)

		t.Cleanup(func() { _ = first.Close() })

		second := dialAsync(t, dial, ln.Addr().String())
		requirePending(t, second, 150*time.Millisecond, "the second dial must wait until the first reads a byte")

		_, err = first.Read(make([]byte, 1))
		require.NoError(t, err)

		r := awaitDial(t, second, 5*time.Second)
		require.NoError(t, r.err)
		require.Eventually(t, func() bool { return len(s.Invocations(t)) == 2 }, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("an early close promotes exactly one waiter", func(t *testing.T) {
		s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

		accepted := make(chan net.Conn, 4)
		ln := testServer(t, func(conn net.Conn) {
			accepted <- conn
			holdUntilClosed(conn)
		})
		addr := ln.Addr().String()

		dial := JumpDialContext(standInSpec(s))

		first, err := dial(context.Background(), "tcp", addr)
		require.NoError(t, err)

		a := dialAsync(t, dial, addr)
		b := dialAsync(t, dial, addr)

		require.Eventually(t, func() bool { return len(s.Invocations(t)) == 1 }, 5*time.Second, 10*time.Millisecond)
		requirePending(t, a, 100*time.Millisecond, "a dial must wait while the leader has no verdict")
		requirePending(t, b, 10*time.Millisecond, "a dial must wait while the leader has no verdict")
		require.Len(t, s.Invocations(t), 1)

		require.NoError(t, first.Close())

		var (
			leader  dialResult
			waiting <-chan dialResult
		)

		select {
		case leader = <-a:
			waiting = b
		case leader = <-b:
			waiting = a
		case <-time.After(200 * time.Millisecond):
			t.Fatal("no waiter became the new leader within 200ms")
		}

		require.NoError(t, leader.err)
		t.Cleanup(func() { _ = leader.conn.Close() })

		require.Eventually(t, func() bool { return len(s.Invocations(t)) == 2 }, 5*time.Second, 10*time.Millisecond)
		requirePending(t, waiting, 300*time.Millisecond, "the other waiter must stay gated behind the new leader")
		assert.Len(t, s.Invocations(t), 2)

		// Feed the new leader's connection a byte. The first accepted
		// connection belongs to the closed leader.
		<-accepted

		var second net.Conn
		select {
		case second = <-accepted:
		case <-time.After(5 * time.Second):
			t.Fatal("the new leader never connected")
		}

		_, err = second.Write([]byte("x"))
		require.NoError(t, err)

		_, err = leader.conn.Read(make([]byte, 1))
		require.NoError(t, err)

		r := awaitDial(t, waiting, 5*time.Second)
		require.NoError(t, r.err)
		require.Eventually(t, func() bool { return len(s.Invocations(t)) == 3 }, 5*time.Second, 10*time.Millisecond)
	})
}

// TestJumpDialContext_TimerVerdictSurvivesTheExit pins that the timer's memo
// is not replaced when the child it terminated then exits 255, as ssh's
// client loop does on SIGTERM, so the operator reads "no response" once and
// the memo's ten seconds do not restart.
func TestJumpDialContext_TimerVerdictSurvivesTheExit(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:           testhelper.SSHHang,
		IgnoreEOF:      true,
		TermExitStatus: 255,
	})

	spec := standInSpec(s)
	spec.FirstByteTimeout = parkedTimer

	d, tr := tracedDialer(spec, nil)

	conn, err := d.dial(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	// The pid file follows the trap, so the timer's SIGTERM finds it set.
	testhelper.WaitPID(t, s.PIDFile, 5*time.Second)
	fireFirstByteTimer(t, conn.(*jumpConn))

	_, first := conn.Read(make([]byte, 16))
	require.True(t, requireTerminal(t, first).TimedOut)

	tr.awaitReaped(t, 1, 10*time.Second)
	require.Equal(t, 255, conn.(*jumpConn).cmd.ProcessState.ExitCode(),
		"the stand-in must report pmx's SIGTERM as a 255 exit")

	_, second := d.dial(context.Background(), "tcp", testJumpAddr)

	je := requireTerminal(t, second)
	assert.True(t, je.TimedOut, "the memo must keep the timer's verdict")
	assert.Equal(t, first.Error(), second.Error())
	assert.Len(t, s.Invocations(t), 1)
	assert.Equal(t, []string{"timer terminal"}, tr.events())
}

// TestJumpDialContext_CloseBeforeFirstByteLeavesNoMemo pins that a close
// before the first byte stays non-terminal even when the child, terminated
// after the grace period, exits 255 as ssh does on SIGTERM. The next dial
// through the same dialer starts its own ssh.
func TestJumpDialContext_CloseBeforeFirstByteLeavesNoMemo(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:           testhelper.SSHHang,
		IgnoreEOF:      true,
		TermExitStatus: 255,
	})

	d, tr := tracedDialer(standInSpec(s), nil)

	conn, err := d.dial(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err)

	testhelper.WaitPID(t, s.PIDFile, 5*time.Second)
	require.NoError(t, conn.Close())

	tr.awaitReaped(t, 1, jumpCloseGrace+10*time.Second)
	require.Equal(t, 255, conn.(*jumpConn).cmd.ProcessState.ExitCode(),
		"the stand-in must report pmx's SIGTERM as a 255 exit")
	assert.Equal(t, []string{"close non-terminal"}, tr.events())

	next, err := d.dial(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err, "a close before the first byte must leave no memo")

	t.Cleanup(func() { _ = next.Close() })

	require.Eventually(t, func() bool { return len(s.Invocations(t)) == 2 }, 5*time.Second, 10*time.Millisecond,
		"the next dial must start its own ssh")
}

// TestJumpConn_BytesForwardedBeforeExitLeaveNoMemo pins that an ssh that
// forwards a byte and then exits 255 is not memoised as a dead bastion when
// a reader drains the byte within the failure budget. The reaper waits for
// the reader to reach end-of-file, so the byte is marked before the exit is
// classified.
func TestJumpConn_BytesForwardedBeforeExitLeaveNoMemo(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode:       testhelper.SSHFail,
		Stdout:     "x",
		Stderr:     []string{"Connection to bastion closed by remote host."},
		ExitStatus: 255,
	})

	d, tr := tracedDialer(standInSpec(s), nil)

	conn, err := d.dial(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	// Read only once the exit is known, which is when the reaper would
	// otherwise classify it.
	requireExited(t, conn.(*jumpConn), 10*time.Second)

	data, err := io.ReadAll(conn)
	assert.Equal(t, "x", string(data))

	var je *JumpError
	require.ErrorAs(t, err, &je)
	assert.Equal(t, 255, je.ExitStatus)

	var op *net.OpError
	assert.False(t, errors.As(err, &op), "an exit after the first byte must not be a dial error")

	tr.awaitReaped(t, 1, 5*time.Second)
	assert.Equal(t, []string{"read established"}, tr.events())

	next, err := d.dial(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err, "a connection that carried a byte must leave no memo")

	t.Cleanup(func() { _ = next.Close() })

	require.Eventually(t, func() bool { return len(s.Invocations(t)) == 2 }, 5*time.Second, 10*time.Millisecond)
}

// TestJumpConn_DeadlineIsATimeout pins that a read deadline expiring is
// reported as the timeout it is, at once, without waiting out the failure
// budget and without settling a verdict, so the transport's own timeout
// handling sees it and no waiter is promoted.
func TestJumpConn_DeadlineIsATimeout(t *testing.T) {
	s := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang})

	d, tr := tracedDialer(standInSpec(s), nil)

	conn, err := d.dial(context.Background(), "tcp", testJumpAddr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(50*time.Millisecond)))

	start := time.Now()
	_, err = conn.Read(make([]byte, 1))
	elapsed := time.Since(start)

	require.ErrorIs(t, err, os.ErrDeadlineExceeded)

	var je *JumpError
	assert.False(t, errors.As(err, &je), "a deadline must not be dressed up as a jump failure")

	var ne net.Error
	require.ErrorAs(t, err, &ne)
	assert.True(t, ne.Timeout())
	assert.Less(t, elapsed, jumpFailureBudget, "a deadline must not wait out the failure budget")
	assert.Empty(t, tr.events(), "a deadline must not settle a verdict")

	select {
	case <-conn.(*jumpConn).outDone:
		t.Fatal("a deadline must not end the reader's stream")
	default:
	}
}

// TestJumpError_QuotesARejectedChain pins that a chain holding a control
// character is quoted in the message, so a stray newline or escape sequence
// in the config never reaches the terminal raw, while an accepted chain,
// even one spaced with a tab, prints as written.
func TestJumpError_QuotesARejectedChain(t *testing.T) {
	_, err := JumpDialContext(JumpSpec{Chain: "bas\ntion", ConnectTimeout: time.Second})(
		context.Background(), "tcp", testJumpAddr)

	je := requireTerminal(t, err)
	assert.NotContains(t, je.Error(), "\n")
	assert.Equal(t, `ssh jump "bas\ntion" -> `+testJumpAddr+
		`: hop 1: host "bas\ntion" is not a hostname, IPv4 address, or bracketed IPv6 literal`, je.Error())

	assert.Equal(t, "edge,\tbastion", printableChain("edge,\tbastion"))
	assert.Equal(t, `"bas\x1b[2Jtion"`, printableChain("bas\x1b[2Jtion"))
}

// TestRedactJumpChain pins the masking of a misused user:password hop in
// both hop forms, and that a chain the validator accepts, or one with no
// password, comes back unchanged.
func TestRedactJumpChain(t *testing.T) {
	cases := map[string]string{
		"u:s3cret@bastion":              "u:<redacted>@bastion",
		"ssh://u:s3cret@bastion:2222":   "ssh://u:<redacted>@bastion:2222",
		"u:s3@cret@bastion":             "u:<redacted>@bastion",
		"ssh://u:s3@cret@bastion":       "ssh://u:<redacted>@bastion",
		"ok@edge, u:pw@inner":           "ok@edge, u:<redacted>@inner",
		" ssh://u:pw@inner":             " ssh://u:<redacted>@inner",
		"SSH://u:pw@inner":              "SSH:<redacted>@inner",
		"u:@bastion":                    "u:<redacted>@bastion",
		"admin:hunter,2@bastion":        "admin:<redacted>@bastion",
		"ssh://admin:hunter,2@bastion":  "ssh://admin:<redacted>@bastion",
		"bastion,ssh://u:se,cret@h":     "bastion,ssh://u:<redacted>@h",
		"ssh://u%3Asecret@h":            "ssh://u%3A<redacted>@h",
		"ssh://u%3asecret@h":            "ssh://u%3a<redacted>@h",
		"ssh://u%253Asecret@h":          "ssh://u%253A<redacted>@h",
		"ssh://u:pw@h/x@y":              "ssh://u:<redacted>@y",
		"edge:22,admin:pw@inner":        "edge:<redacted>@inner",
		"admin@corp:pw,x@bastion":       "admin@corp:<redacted>@bastion",
		"bastion":                       "bastion",
		"alice@corp.example@bastion":    "alice@corp.example@bastion",
		"root@[2001:db8::1]:2222":       "root@[2001:db8::1]:2222",
		"bastion:2222,2001:db8::1":      "bastion:2222,2001:db8::1",
		"bastion:2222,admin@inner:2222": "bastion:2222,admin@inner:2222",
		"ssh://alice%40corp@bastion:1":  "ssh://alice%40corp@bastion:1",
		"x;id,bastion:22":               "x;id,bastion:22",
		"":                              "",
	}

	for chain, want := range cases {
		assert.Equal(t, want, RedactJumpChain(chain), chain)
	}
}

// misusedJumpChains are chains that carry a password ssh has no syntax for,
// each mapped to the distinctive runs of its password. The shapes cover a
// comma that splits the password across hops, a percent-encoded colon, an
// "@" inside the password, a password that begins in a hop that also parses
// as host:port, and a directory login in front of the colon.
var misusedJumpChains = map[string][]string{
	"ssh://admin:Zq9alpha@bastion":                   {"Zq9alpha"},
	"admin:Zq9alpha,Xk7bravo@bastion":                {"Zq9alpha", "Xk7bravo"},
	"ssh://admin:Zq9alpha,Xk7bravo@bastion":          {"Zq9alpha", "Xk7bravo"},
	"bastion,ssh://u:Zq9alpha,Xk7bravo,Wm4charlie@h": {"Zq9alpha", "Xk7bravo", "Wm4charlie"},
	"ssh://u%3AZq9alpha@h":                           {"Zq9alpha"},
	"ssh://u%3aZq9alpha,Xk7bravo@h":                  {"Zq9alpha", "Xk7bravo"},
	"ssh://u%253AZq9alpha@h":                         {"Zq9alpha"},
	"u:Zq9alpha%3AXk7bravo@h":                        {"Zq9alpha", "Xk7bravo"},
	"u:Zq9alpha@Xk7bravo@bastion":                    {"Zq9alpha", "Xk7bravo"},
	"ssh://u:Zq9alpha@Xk7bravo,Wm4charlie@bastion":   {"Zq9alpha", "Xk7bravo", "Wm4charlie"},
	"u:4242,Xk7b!Wm4c@h":                             {"4242", "Xk7b", "Wm4c"},
	"admin@Zq9alpha:Xk7bravo@bastion":                {"Xk7bravo"},
	"edge:22,admin:Zq9alpha,Xk7bravo@inner":          {"Zq9alpha", "Xk7bravo"},
	"u:Zq9alpha,@h":                                  {"Zq9alpha"},
	"u:Zq9alpha,,Xk7bravo@h":                         {"Zq9alpha", "Xk7bravo"},
	"u:Zq9:alpha,Xk7bravo@h":                         {"Zq9", "alpha", "Xk7bravo"},
	"SSH://u:Zq9alpha,Xk7bravo@h":                    {"Zq9alpha", "Xk7bravo"},
	"ok@edge, u:Zq9alpha\nXk7bravo@inner":            {"Zq9alpha", "Xk7bravo"},
}

// TestJumpChainErrors_NeverEchoAPassword proves that no run of a misused
// hop's password reaches the validator's reason, RedactJumpChain, or a
// dial's JumpError, whatever the password holds.
func TestJumpChainErrors_NeverEchoAPassword(t *testing.T) {
	for chain, runs := range misusedJumpChains {
		verr := ValidateJumpChain(chain)
		require.Error(t, verr, chain)

		_, derr := JumpDialContext(JumpSpec{Chain: chain, ConnectTimeout: time.Second})(
			context.Background(), "tcp", testJumpAddr)
		je := requireTerminal(t, derr)

		for _, text := range []string{verr.Error(), RedactJumpChain(chain), je.Error()} {
			for _, run := range runs {
				assert.NotContains(t, text, run, "%q leaked through %q", chain, text)
			}
		}
	}

	for _, chain := range []string{"u:s3cret@bastion", "ssh://u:s3cret@bastion", "edge,u:s3@cret@bastion"} {
		err := ValidateJumpChain(chain)
		require.Error(t, err, chain)
		assert.Contains(t, err.Error(), `user "u:<redacted>"`, chain)

		_, err = JumpDialContext(JumpSpec{Chain: chain, ConnectTimeout: time.Second})(
			context.Background(), "tcp", testJumpAddr)

		je := requireTerminal(t, err)
		assert.Contains(t, je.Error(), "u:<redacted>@bastion -> "+testJumpAddr, chain)
	}
}

// TestValidateJumpChain_MasksQuotedValues pins the reasons for hop shapes
// whose quoted value would otherwise hold password bytes: the value is
// quoted with the password part masked, and a value wholly inside the
// password quotes as "<redacted>".
func TestValidateJumpChain_MasksQuotedValues(t *testing.T) {
	cases := map[string]string{
		"admin:hunter,2@bastion":    `hop 1: port "<redacted>" is out of range [1, 65535]`,
		"bastion,ssh://u:se,cret@h": `hop 2: port "<redacted>" is out of range [1, 65535]`,
		"ssh://u%3Asecret@h":        `hop 1: user "u%3A<redacted>" has a disallowed character`,
		"u:4242,Xk7b!Wm4c@h":        `hop 2: user "<redacted>" has a disallowed character`,
		"admin@corp:pw@bastion":     `hop 1: user "admin@corp:<redacted>" has a disallowed character`,
		"u:a:b,c@h": `hop 1: host "u:<redacted>" is not a hostname, IPv4 address, or bracketed IPv6 ` +
			`literal`,
		"edge:22,x!y@h":           `hop 2: user "<redacted>" has a disallowed character`,
		"bastion:0":               `hop 1: port "0" is out of range [1, 65535]`,
		"x;id,bastion:22,admin@b": `hop 1: host "x;id" is not a hostname, IPv4 address, or bracketed IPv6 literal`,
	}

	for chain, want := range cases {
		require.EqualError(t, ValidateJumpChain(chain), want, chain)
	}
}
