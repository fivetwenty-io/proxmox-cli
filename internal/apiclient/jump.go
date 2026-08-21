package apiclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// jumpConnectTimeoutSec bounds how long the jump host itself may take to
// answer, mirroring defaultDialTimeoutSec's reasoning for the direct dial: a
// bastion that silently drops SYNs must fail fast rather than consume the
// whole request timeout. It is passed to ssh as ConnectTimeout, so ssh
// enforces it against the jump host while the API request timeout still bounds
// everything after.
const jumpConnectTimeoutSec = 5

// ApplyJumpOptions routes the API client's connections through an ssh jump
// host when one is configured, and returns opts unchanged when jump is empty.
//
// The Proxmox API is plain HTTPS, so reaching a host that is not directly
// routable is purely a matter of where the TCP connection comes from. That
// makes Options.DialContext the whole mechanism: TLS is still negotiated
// against opts.Host at the far end, so certificate verification, fingerprint
// pinning, and TOFU all behave exactly as they do on a direct connection. This
// deliberately runs after ApplyTOFUOptions for that reason: it changes how the
// bytes travel, never what is checked at the other end.
//
// jump takes ssh's own ProxyJump syntax, [user@]host[:port], and a
// comma-separated chain of them.
func ApplyJumpOptions(opts pve.Options, jump string) pve.Options {
	if strings.TrimSpace(jump) == "" {
		return opts
	}

	opts.DialContext = jumpDialContext(jump)

	return opts
}

// jumpDialContext returns a dial function that reaches addr through the jump
// chain by running `ssh -W <addr> <jump>`, the same mechanism OpenSSH's own
// ProxyJump uses. Shelling out to ssh rather than speaking the protocol
// ourselves is the point: the operator's existing config, keys, agent,
// known_hosts, and certificates all apply unchanged, so a jump host that
// already works for `pmx ssh` works here without being configured twice.
func jumpDialContext(jump string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("ssh jump host cannot carry %q connections (tcp only)", network)
		}

		args, err := jumpSSHArgs(jump, addr)
		if err != nil {
			return nil, err
		}

		return dialViaSSH(ctx, args, jump, addr)
	}
}

// jumpSSHArgs builds the ssh argument list that forwards a connection to addr
// through the jump chain.
//
// ssh -W takes exactly one destination, so a multi-hop chain puts every hop
// but the last behind -J and connects to the last: "a,b" becomes
// `ssh -J a -W addr b`. The final hop's optional :port needs -p, because ssh's
// destination argument does not accept the [user@]host:port form that -J does.
func jumpSSHArgs(jump, addr string) ([]string, error) {
	hops := strings.Split(jump, ",")

	last := strings.TrimSpace(hops[len(hops)-1])
	if last == "" {
		return nil, fmt.Errorf("ssh jump %q: empty final hop", jump)
	}

	dest, port, err := splitJumpHop(last)
	if err != nil {
		return nil, fmt.Errorf("ssh jump %q: %w", jump, err)
	}

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(jumpConnectTimeoutSec),
	}

	if len(hops) > 1 {
		args = append(args, "-J", strings.Join(hops[:len(hops)-1], ","))
	}

	if port != "" {
		args = append(args, "-p", port)
	}

	return append(args, "-W", addr, dest), nil
}

// splitJumpHop splits one [user@]host[:port] hop into an ssh destination and
// its port. An IPv6 literal must be bracketed to carry a port, exactly as ssh
// requires, so a bare "::1" stays a host rather than being read as host ":"
// port "1".
func splitJumpHop(hop string) (dest, port string, err error) {
	user := ""
	if at := strings.LastIndex(hop, "@"); at >= 0 {
		user, hop = hop[:at+1], hop[at+1:]
	}

	switch {
	case strings.HasPrefix(hop, "["):
		end := strings.Index(hop, "]")
		if end < 0 {
			return "", "", fmt.Errorf("unterminated IPv6 literal in %q", hop)
		}

		host := hop[1:end]

		switch rest := hop[end+1:]; {
		case rest == "":
			return user + host, "", nil
		case strings.HasPrefix(rest, ":"):
			return user + host, rest[1:], nil
		default:
			return "", "", fmt.Errorf("trailing %q after IPv6 literal", rest)
		}

	case strings.Count(hop, ":") == 1:
		host, p, _ := strings.Cut(hop, ":")

		return user + host, p, nil

	default:
		// No colon at all, or an unbracketed IPv6 literal: either way there
		// is no port to split off.
		return user + hop, "", nil
	}
}

// dialViaSSH starts ssh with args and returns its stdin/stdout as a net.Conn.
func dialViaSSH(ctx context.Context, args []string, jump, addr string) (net.Conn, error) {
	return dialViaCommand(ctx, "ssh", args, jump, addr)
}

// dialViaCommand is dialViaSSH with the program named explicitly, so the
// net.Conn adapter can be exercised against a stand-in that forwards
// stdin/stdout the same way `ssh -W` does.
func dialViaCommand(ctx context.Context, name string, args []string, jump, addr string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Own both pipe ends rather than using cmd.StdinPipe/StdoutPipe: those
	// hand back wrappers, and *os.File is what carries the read/write
	// deadlines net.Conn is required to support.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("ssh jump %s: create stdin pipe: %w", jump, err)
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW)

		return nil, fmt.Errorf("ssh jump %s: create stdout pipe: %w", jump, err)
	}

	var stderr bytes.Buffer

	cmd := exec.Command(name, args...) //nolint:gosec // args are built from config, never from remote input
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		closeAll(inR, inW, outR, outW)

		return nil, fmt.Errorf("ssh jump %s: start ssh: %w", jump, err)
	}

	// The child holds its own descriptors now; keeping the parent's copies
	// open would stop reads from ever seeing EOF when ssh exits.
	closeAll(inR, outW)

	return &jumpConn{
		cmd:    cmd,
		in:     inW,
		out:    outR,
		stderr: &stderr,
		jump:   jump,
		addr:   addr,
	}, nil
}

// closeAll closes every file, discarding errors: each call site is either
// unwinding a failed setup (where the original error is the one worth
// reporting) or handing a descriptor to the child process it just started.
func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// jumpConn adapts an `ssh -W` process to net.Conn: writes go to ssh's stdin
// and reads come from its stdout, which is exactly the forwarded stream.
type jumpConn struct {
	cmd    *exec.Cmd
	in     *os.File
	out    *os.File
	stderr *bytes.Buffer
	jump   string
	addr   string

	closeOnce sync.Once
	closeErr  error
}

func (c *jumpConn) Read(b []byte) (int, error) {
	n, err := c.out.Read(b)
	if err != nil && !isTimeout(err) {
		// ssh reports a refused forward, a rejected key, or an unreachable
		// jump host on stderr and then simply closes the stream, which
		// would otherwise reach the caller as a bare EOF.
		if msg := strings.TrimSpace(c.stderr.String()); msg != "" {
			return n, fmt.Errorf("ssh jump %s -> %s: %s", c.jump, c.addr, msg)
		}
	}

	return n, err
}

func (c *jumpConn) Write(b []byte) (int, error) { return c.in.Write(b) }

func (c *jumpConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.in.Close()

		if err := c.out.Close(); c.closeErr == nil {
			c.closeErr = err
		}

		// Closing both pipes makes ssh see EOF and exit on its own; kill
		// only if it does not, so a clean teardown stays clean.
		done := make(chan struct{})
		go func() {
			_ = c.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	})

	return c.closeErr
}

func (c *jumpConn) LocalAddr() net.Addr  { return jumpAddr("ssh-jump:" + c.jump) }
func (c *jumpConn) RemoteAddr() net.Addr { return jumpAddr(c.addr) }

func (c *jumpConn) SetDeadline(t time.Time) error {
	err := c.in.SetWriteDeadline(t)
	if rerr := c.out.SetReadDeadline(t); err == nil {
		err = rerr
	}

	return err
}

func (c *jumpConn) SetReadDeadline(t time.Time) error  { return c.out.SetReadDeadline(t) }
func (c *jumpConn) SetWriteDeadline(t time.Time) error { return c.in.SetWriteDeadline(t) }

// jumpAddr is the net.Addr a jumpConn reports. There is no local socket to
// name (the connection is a pipe to an ssh process), so this exists to satisfy
// net.Conn and to make a logged address readable.
type jumpAddr string

func (a jumpAddr) Network() string { return "tcp" }
func (a jumpAddr) String() string  { return string(a) }

// isTimeout reports whether err is a deadline expiry rather than a real
// failure, so Read does not dress a timeout up in ssh's stderr.
func isTimeout(err error) bool {
	var netErr net.Error
	if ok := asNetError(err, &netErr); ok {
		return netErr.Timeout()
	}

	return false
}

// asNetError is errors.As specialised to net.Error, kept separate so Read
// stays readable. io.EOF, the ordinary end of a forwarded stream, is not a
// net.Error and so is never treated as a timeout.
func asNetError(err error, target *net.Error) bool {
	if err == nil || err == io.EOF {
		return false
	}

	ne, ok := err.(net.Error) //nolint:errorlint // os.File deadline errors implement net.Error directly
	if ok {
		*target = ne
	}

	return ok
}
