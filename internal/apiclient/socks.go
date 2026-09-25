package apiclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"
)

// SOCKS5 wire values from RFC 1928 and RFC 1929.
const (
	socksVersion5         = 0x05
	socksAuthNone         = 0x00
	socksAuthPassword     = 0x02
	socksAuthNoAcceptable = 0xff
	socksAuthVersion1     = 0x01
	socksCmdConnect       = 0x01
	socksAddrIPv4         = 0x01
	socksAddrDomain       = 0x03
	socksAddrIPv6         = 0x04
	socksDefaultPort      = "1080"
	socksMaxField         = 255
)

// socksReplies names the RFC 1928 reply codes a proxy sends when it could
// not complete a CONNECT.
var socksReplies = map[byte]string{
	0x01: "general SOCKS server failure",
	0x02: "connection not allowed by ruleset",
	0x03: "network unreachable",
	0x04: "host unreachable",
	0x05: "connection refused",
	0x06: "TTL expired",
	0x07: "command not supported",
	0x08: "address type not supported",
}

// DialFunc is the shape of http.Transport.DialContext and
// pve.Options.DialContext.
type DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// IsSOCKSProxy reports whether u names a SOCKS5 proxy, which pmx negotiates
// itself through SOCKSDialContext rather than leaving to net/http.
func IsSOCKSProxy(u *url.URL) bool {
	return u != nil && (u.Scheme == "socks5" || u.Scheme == "socks5h")
}

// SOCKSDialContext returns a dial function that reaches addr through the
// SOCKS5 proxy u. forward opens the connection to the proxy itself, and nil
// means a plain TCP dial; a bastion route passes its jump dialer here, so
// the bastion reaches the proxy and the proxy reaches addr.
//
// net/http runs its own SOCKS negotiation under the request's deadline, and
// it reports a failure as a "proxyconnect" error, which the client library
// retries. A proxy that accepts the connection but never finishes its own
// connect to a blackholed target therefore held each attempt for the whole
// request bound. This dial runs the negotiation itself, bounds the connect
// to the proxy, the greeting, the authentication, and the proxy's connect
// to addr together by bound, and reports every failure as a dial
// *net.OpError, which the client library treats as terminal, exactly as it
// treats a direct connect that timed out or was refused.
//
// With a forward dial the clock starts once forward returns, not before it.
// A bastion's dial returns as soon as ssh starts, and it may first wait on
// another connection's bastion setup; that wait is bounded by the jump's own
// first-byte timer, and charging it to bound as well would fail concurrent
// dials and blame the proxy for a slow bastion.
//
// Like net/http, it sends addr's hostname to the proxy for both schemes, so
// the proxy resolves it. Credentials come from u's userinfo, and the
// username/password method is offered alongside "no authentication" only
// when u carries a username. No error text ever includes the userinfo.
func SOCKSDialContext(u *url.URL, bound time.Duration, forward DialFunc) DialFunc {
	d := &socksDialer{
		label:   u.Scheme + " proxy " + socksProxyAddr(u),
		addr:    socksProxyAddr(u),
		bound:   bound,
		forward: forward,
	}

	if u.User != nil {
		d.username = u.User.Username()
		d.password, _ = u.User.Password()
		d.useAuth = true
	}

	return d.dial
}

// socksProxyAddr is u's host and port, with the SOCKS default port 1080 when
// u names none, as net/http fills it.
func socksProxyAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = socksDefaultPort
	}

	return net.JoinHostPort(u.Hostname(), port)
}

// socksDialer is the state one SOCKSDialContext closure carries.
type socksDialer struct {
	label    string
	addr     string
	bound    time.Duration
	forward  DialFunc // nil means a plain TCP dial inside the bound
	useAuth  bool
	username string
	password string
}

// socksStage names the part of a dial that failed, which picks the wording
// of a timeout.
type socksStage int

const (
	stageProxyConnect socksStage = iota
	stageGreeting
	stageTargetConnect
)

// socksError is the cause inside the dial *net.OpError a failed SOCKS dial
// returns. Its text starts with the proxy's scheme, host, and port, and it
// unwraps to the underlying error, so a bastion's *JumpError stays
// reachable through errors.As.
type socksError struct {
	msg     string
	err     error
	timeout bool
}

func (e *socksError) Error() string {
	if e.err == nil {
		return e.msg
	}

	return e.msg + ": " + e.err.Error()
}

func (e *socksError) Unwrap() error { return e.err }

// Timeout reports whether the dial ran out of time, which *net.OpError
// passes on through its own Timeout.
func (e *socksError) Timeout() bool { return e.timeout }

// socksAddr is the target address a failed dial names.
type socksAddr string

func (a socksAddr) Network() string { return "tcp" }
func (a socksAddr) String() string  { return string(a) }

func (d *socksDialer) dial(parent context.Context, network, addr string) (net.Conn, error) {
	var (
		conn net.Conn
		err  error
	)

	if d.forward != nil {
		conn, err = d.forward(parent, network, d.addr)
	}

	budget := d.bound
	if deadline, ok := parent.Deadline(); ok {
		budget = max(0, min(budget, time.Until(deadline)))
	}

	ctx, cancel := context.WithTimeout(parent, d.bound)
	defer cancel()

	if d.forward == nil {
		conn, err = (&net.Dialer{}).DialContext(ctx, network, d.addr)
	}

	if err != nil {
		return nil, d.fail(ctx, addr, stageProxyConnect, budget, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	// The context fires at the deadline and on a cancel. Moving the deadline
	// into the past unblocks any read or write at once, and where a conn
	// cannot take a deadline, such as a pipe on some platforms, closing it
	// does the same.
	stop := context.AfterFunc(ctx, func() {
		if conn.SetDeadline(time.Unix(1, 0)) != nil {
			_ = conn.Close()
		}
	})

	stage, err := d.negotiate(conn, addr)

	if !stop() && err == nil {
		err = ctx.Err()
	}

	if err != nil {
		_ = conn.Close()

		return nil, d.fail(ctx, addr, stage, budget, err)
	}

	_ = conn.SetDeadline(time.Time{})

	return conn, nil
}

// proxyDialCause strips the dial *net.OpError a forward dial returns down to
// its cause, since the SOCKS error names the proxy address already. Any other
// error, such as a bastion's bare *JumpError, is kept whole.
func proxyDialCause(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && error(op) == err && op.Err != nil {
		return op.Err
	}

	return err
}

// fail wraps err in the dial *net.OpError the transport returns. A bastion's
// failure keeps the bastion's own error as the cause, even when it is the
// jump's first-byte timer, because that error already says what went wrong
// and the SOCKS stage would only hide it. Otherwise a dial that ran out of
// its budget says which stage it was waiting on, a cancelled one says so,
// and every other failure keeps err as its cause. Cancellation is checked
// before the deadline, because a cancel moves the connection's deadline into
// the past and the read it interrupts then reports a timeout.
func (d *socksDialer) fail(ctx context.Context, addr string, stage socksStage, budget time.Duration, err error) error {
	var (
		se *socksError
		je *JumpError
	)

	cause := proxyDialCause(err)

	switch {
	case errors.As(err, &je) && stage != stageTargetConnect:
		se = &socksError{msg: d.label + " is unreachable through the bastion", err: cause}
	case errors.As(err, &je):
		se = &socksError{msg: d.label + " failed during the SOCKS handshake", err: cause}
	case errors.Is(ctx.Err(), context.Canceled):
		se = &socksError{msg: d.label + ": dial cancelled", err: context.Canceled}
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || isTimeout(err):
		se = &socksError{msg: d.timeoutText(stage, budget), timeout: true}
	case errors.As(err, &se):
		// negotiate already phrased it.
	case stage == stageTargetConnect && errors.Is(err, io.EOF):
		// OpenSSH's -D proxy, among others, closes the connection without a
		// reply when its own connect to the target fails.
		se = &socksError{msg: d.label + " closed the connection without a reply, so it could not reach the target"}
	case stage == stageProxyConnect:
		se = &socksError{msg: d.label + " is unreachable", err: cause}
	default:
		se = &socksError{msg: d.label + " failed during the SOCKS handshake", err: cause}
	}

	return &net.OpError{Op: "dial", Net: "tcp", Addr: socksAddr(addr), Err: se}
}

// timeoutText phrases a timeout by the stage the dial was waiting on.
func (d *socksDialer) timeoutText(stage socksStage, budget time.Duration) string {
	within := " within " + budget.Round(time.Millisecond).String()

	switch stage {
	case stageProxyConnect:
		return d.label + " did not accept a connection" + within
	case stageGreeting:
		return d.label + " did not answer" + within
	default:
		return d.label + " did not connect to the target" + within
	}
}

// negotiate runs the greeting, the optional RFC 1929 authentication, and the
// CONNECT request on conn, and returns the stage it reached.
func (d *socksDialer) negotiate(conn net.Conn, addr string) (socksStage, error) {
	request, err := socksConnectRequest(addr)
	if err != nil {
		return stageGreeting, &socksError{msg: d.label + ": " + err.Error()}
	}

	if d.useAuth && (d.username == "" || len(d.username) > socksMaxField || len(d.password) > socksMaxField) {
		return stageGreeting, &socksError{
			msg: d.label + ": the proxy username must be 1 to 255 bytes and the password at most 255 bytes",
		}
	}

	methods := []byte{socksAuthNone}
	if d.useAuth {
		methods = append(methods, socksAuthPassword)
	}

	if _, err := conn.Write(append([]byte{socksVersion5, byte(len(methods))}, methods...)); err != nil {
		return stageGreeting, err
	}

	var choice [2]byte
	if _, err := io.ReadFull(conn, choice[:]); err != nil {
		return stageGreeting, err
	}

	if choice[0] != socksVersion5 {
		return stageGreeting, d.protocolError("its greeting reply carried SOCKS version %d", choice[0])
	}

	switch {
	case choice[1] == socksAuthNone:
	case choice[1] == socksAuthPassword && d.useAuth:
		if err := d.authenticate(conn); err != nil {
			return stageGreeting, err
		}
	case choice[1] == socksAuthNoAcceptable && !d.useAuth:
		return stageGreeting, &socksError{msg: d.label + " requires authentication, and no proxy username is set"}
	case choice[1] == socksAuthNoAcceptable:
		return stageGreeting, &socksError{msg: d.label + " accepts none of the offered authentication methods"}
	default:
		return stageGreeting, d.protocolError("it chose authentication method %d, which was not offered", choice[1])
	}

	if _, err := conn.Write(request); err != nil {
		return stageTargetConnect, err
	}

	return stageTargetConnect, d.readConnectReply(conn)
}

// authenticate runs the RFC 1929 username/password subnegotiation.
func (d *socksDialer) authenticate(conn net.Conn) error {
	msg := make([]byte, 0, 3+len(d.username)+len(d.password))
	msg = append(msg, socksAuthVersion1, byte(len(d.username)))
	msg = append(msg, d.username...)
	msg = append(msg, byte(len(d.password)))
	msg = append(msg, d.password...)

	if _, err := conn.Write(msg); err != nil {
		return err
	}

	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}

	if reply[0] != socksAuthVersion1 {
		return d.protocolError("its authentication reply carried version %d", reply[0])
	}

	if reply[1] != 0 {
		return &socksError{msg: d.label + " rejected the proxy username and password"}
	}

	return nil
}

// readConnectReply reads the CONNECT reply, including its bound address,
// which is discarded, so the stream is positioned at the target's first
// byte.
func (d *socksDialer) readConnectReply(conn net.Conn) error {
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return err
	}

	if head[0] != socksVersion5 {
		return d.protocolError("its connect reply carried SOCKS version %d", head[0])
	}

	if head[1] != 0 {
		reason, ok := socksReplies[head[1]]
		if !ok {
			reason = "reply code " + strconv.Itoa(int(head[1]))
		}

		return &socksError{msg: d.label + " could not connect to the target: " + reason}
	}

	var skip int

	switch head[3] {
	case socksAddrIPv4:
		skip = net.IPv4len
	case socksAddrIPv6:
		skip = net.IPv6len
	case socksAddrDomain:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return err
		}

		skip = int(n[0])
	default:
		return d.protocolError("its connect reply carried address type %d", head[3])
	}

	_, err := io.CopyN(io.Discard, conn, int64(skip+2))

	return err
}

func (d *socksDialer) protocolError(format string, args ...any) error {
	return &socksError{msg: d.label + " broke the SOCKS protocol: " + fmt.Sprintf(format, args...)}
}

// socksConnectRequest encodes the CONNECT request for addr. An IP literal
// travels as an address and anything else as a domain name, left for the
// proxy to resolve.
func socksConnectRequest(addr string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("target %q is not host:port", addr)
	}

	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("target %q has an invalid port", addr)
	}

	req := []byte{socksVersion5, socksCmdConnect, 0}

	switch ip := net.ParseIP(host); {
	case ip != nil && ip.To4() != nil:
		req = append(req, socksAddrIPv4)
		req = append(req, ip.To4()...)
	case ip != nil:
		req = append(req, socksAddrIPv6)
		req = append(req, ip.To16()...)
	case len(host) == 0 || len(host) > socksMaxField:
		return nil, fmt.Errorf("target host name %q must be 1 to 255 bytes", host)
	default:
		req = append(req, socksAddrDomain, byte(len(host)))
		req = append(req, host...)
	}

	return binary.BigEndian.AppendUint16(req, uint16(port)), nil
}
