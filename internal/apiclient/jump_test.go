package apiclient

import (
	"context"
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// TestApplyJumpOptions_EmptyJumpLeavesDirectDial pins the opt-in: a context
// with no jump host configured must dial exactly as it did before this
// existed, which for the SDK means leaving DialContext nil so its own
// DialTimeoutSec still governs.
func TestApplyJumpOptions_EmptyJumpLeavesDirectDial(t *testing.T) {
	for _, jump := range []string{"", "   "} {
		opts := ApplyJumpOptions(pve.Options{Host: "pve", DialTimeoutSec: 5}, jump)

		assert.Nil(t, opts.DialContext, "jump %q must not install a dialer", jump)
		assert.Equal(t, 5, opts.DialTimeoutSec, "the direct dial timeout must survive untouched")
	}
}

// TestApplyJumpOptions_InstallsDialer covers the other half: a configured jump
// host is what makes the API reachable at all, so the dialer has to be there.
func TestApplyJumpOptions_InstallsDialer(t *testing.T) {
	opts := ApplyJumpOptions(pve.Options{Host: "pve"}, "admin@bastion")

	require.NotNil(t, opts.DialContext)
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
			want: []string{"-W", "10.0.0.5:8006", "bastion"},
		},
		{
			name: "user and port",
			jump: "admin@bastion:2222",
			want: []string{"-p", "2222", "-W", "10.0.0.5:8006", "admin@bastion"},
		},
		{
			name: "chain puts every hop but the last behind -J",
			jump: "edge,admin@inner:2222",
			want: []string{"-J", "edge", "-p", "2222", "-W", "10.0.0.5:8006", "admin@inner"},
		},
		{
			name: "bracketed IPv6 literal with port",
			jump: "root@[2001:db8::1]:2222",
			want: []string{"-p", "2222", "-W", "10.0.0.5:8006", "root@2001:db8::1"},
		},
		{
			name: "bare IPv6 literal keeps every colon as host",
			jump: "2001:db8::1",
			want: []string{"-W", "10.0.0.5:8006", "2001:db8::1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jumpSSHArgs(tc.jump, "10.0.0.5:8006")
			require.NoError(t, err)

			assert.Equal(t, []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}, got[:4],
				"ssh must never prompt and must fail fast on an unreachable bastion")
			assert.Equal(t, tc.want, got[4:])
		})
	}
}

// TestJumpSSHArgs_EmptyFinalHopIsRejected guards a trailing comma, which would
// otherwise produce `ssh ... -W addr ""` and fail with an opaque ssh usage
// error rather than naming the misconfigured setting.
func TestJumpSSHArgs_EmptyFinalHopIsRejected(t *testing.T) {
	_, err := jumpSSHArgs("bastion,", "10.0.0.5:8006")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty final hop")
}

// TestJumpDialContext_RejectsNonTCP pins the one network a forwarded ssh
// stream cannot carry. Returning a clear error beats handing back a stream
// that behaves like a broken TCP connection.
func TestJumpDialContext_RejectsNonTCP(t *testing.T) {
	_, err := jumpDialContext("bastion")(context.Background(), "udp", "10.0.0.5:8006")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tcp only")
}

// TestJumpConn_CarriesBytesBothWays exercises the net.Conn adapter against a
// real process and a real listener, standing in for ssh with a command that
// forwards stdin/stdout the same way `ssh -W` does. It is the check that the
// pipe plumbing, the deadline support net.Conn requires, and the teardown all
// work, without needing an actual bastion.
func TestJumpConn_CarriesBytesBothWays(t *testing.T) {
	// /dev/tcp is a bash/ksh feature, not a POSIX one: on Debian-family hosts
	// /bin/sh is dash, which has no such redirection, so the script would die
	// on the first line and leave this test reading EOF. Ask for bash by name.
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	ln, lerr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, lerr)

	defer func() { _ = ln.Close() }()

	done := make(chan struct{})

	go func() {
		defer close(done)

		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}

		defer func() { _ = conn.Close() }()

		buf := make([]byte, 5)
		if _, rerr := conn.Read(buf); rerr != nil {
			return
		}

		_, _ = conn.Write([]byte("PONG!"))
	}()

	// A netcat-equivalent that is present wherever bash is: a tiny Go program
	// would need building, so use the shell's own /dev/tcp.
	script := "exec 3<>/dev/tcp/127.0.0.1/" + portOf(t, ln.Addr()) + "; cat <&0 >&3 & cat <&3"

	conn, err := dialViaCommand(context.Background(), shell, []string{"-c", script}, "test", ln.Addr().String())
	if err != nil {
		t.Skipf("shell /dev/tcp unavailable: %v", err)
	}

	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.SetDeadline(deadlineIn(t)))

	_, err = conn.Write([]byte("PING!"))
	require.NoError(t, err)

	buf := make([]byte, 5)
	_, err = conn.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "PONG!", string(buf))

	<-done
}

// portOf returns the port half of a listener address.
func portOf(t *testing.T, addr net.Addr) string {
	t.Helper()

	_, port, err := net.SplitHostPort(addr.String())
	require.NoError(t, err)

	return port
}

// deadlineIn returns a deadline far enough out that a healthy round trip
// never hits it, but close enough that a broken one fails the test rather
// than hanging it.
func deadlineIn(t *testing.T) time.Time {
	t.Helper()

	return time.Now().Add(10 * time.Second)
}
