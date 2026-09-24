package testhelper

import (
	"encoding/binary"
	"io"
	"net"
	"slices"
	"strconv"
	"sync"
	"testing"
)

// SOCKS5Connection records what SOCKS5StandIn observed for one client
// connection.
type SOCKS5Connection struct {
	// Target is the destination the client asked the proxy to reach, in
	// host:port form. A hostname target arrives here exactly as the client
	// sent it, because SOCKS5StandIn never resolves it itself: it dials the
	// real backend by port alone (every origin behind it is a local
	// httptest server), which is what lets a caller confirm the hostname
	// travelled unresolved rather than being turned into an IP address
	// before it ever reached the proxy.
	Target string

	// Authenticated reports whether the client performed the RFC 1929
	// username/password subnegotiation. Username and Password are set only
	// when it did; SOCKS5StandIn accepts any credentials offered, because
	// what a test needs is to see what the client sent, not to enforce a
	// particular pair.
	Authenticated bool
	Username      string
	Password      string
}

// SOCKS5Proxy is the in-process SOCKS5 stand-in SOCKS5StandIn returns.
type SOCKS5Proxy struct {
	// Addr is "host:port" the stand-in listens on, ready to drop into a
	// "socks5://" or "socks5h://" proxy URL.
	Addr string

	t testing.TB

	mu          sync.Mutex
	connections []SOCKS5Connection
}

// SOCKS5StandIn starts an in-process SOCKS5 proxy listening on loopback and
// returns it, modelled on the standard library's own testSOCKS5Proxy in
// net/http/transport_test.go. It speaks the SOCKS5 CONNECT command over a
// real TCP listener, negotiates the RFC 1929 username/password
// subnegotiation whenever the client offers it, and relays the resulting
// byte stream to a real backend, so a client dialing through it reaches an
// actual origin rather than a canned response. t.Cleanup stops it once the
// test ends.
func SOCKS5StandIn(t testing.TB) *SOCKS5Proxy {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testhelper.SOCKS5StandIn: listen: %v", err)
	}

	p := &SOCKS5Proxy{Addr: listener.Addr().String(), t: t}

	var wg sync.WaitGroup

	// The accept loop itself is tracked in wg, not just the connections it
	// spawns: that keeps wg's counter above zero for as long as the loop
	// might still call wg.Go, so Cleanup's wg.Wait can never return while a
	// connection accepted just before listener.Close is still being added.
	wg.Go(func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			wg.Go(func() {
				p.serve(conn)
			})
		}
	})

	t.Cleanup(func() {
		_ = listener.Close()
		wg.Wait()
	})

	return p
}

// Connections returns every connection SOCKS5StandIn has finished handling
// so far, in the order it accepted them.
func (p *SOCKS5Proxy) Connections() []SOCKS5Connection {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]SOCKS5Connection, len(p.connections))
	copy(out, p.connections)

	return out
}

func (p *SOCKS5Proxy) record(c SOCKS5Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connections = append(p.connections, c)
}

// serve handles one client connection end to end: the method negotiation, an
// optional username/password subnegotiation, the CONNECT request, and the
// relay to the real backend. A read or write that fails because the client
// closed the connection — which a test that deliberately cancels a dial
// through this stand-in does on purpose — is logged rather than failing the
// test; only a genuine protocol violation from a client still talking (a
// wrong SOCKS version, an unsupported command, or an unsupported address
// type) calls t.Errorf.
func (p *SOCKS5Proxy) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	rec, ok := p.negotiate(conn)
	if !ok {
		return
	}

	target, ok := p.readConnectRequest(conn)
	if !ok {
		return
	}

	rec.Target = target

	// A minimal success reply: VER=5, REP=0 (succeeded), RSV=0, ATYP=1
	// (IPv4), followed by an all-zero bound address and port. Real clients,
	// including net/http's own SOCKS5 dialer, do not validate the bound
	// address of a CONNECT reply, so a fixed placeholder is enough.
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: connect reply: %v", err)

		return
	}

	p.record(rec)

	p.relay(conn, target)
}

// negotiate reads the client's method-selection greeting and, when the
// client offers RFC 1929 username/password authentication, performs that
// subnegotiation. It prefers username/password whenever the client offers
// it, so a test that wants to see the credential can always force it by
// putting one on the proxy URL.
func (p *SOCKS5Proxy) negotiate(conn net.Conn) (SOCKS5Connection, bool) {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read greeting: %v", err)

		return SOCKS5Connection{}, false
	}

	const socksVersion5 = 5
	if greeting[0] != socksVersion5 {
		p.t.Errorf("testhelper.SOCKS5StandIn: unexpected SOCKS version %d", greeting[0])

		return SOCKS5Connection{}, false
	}

	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read methods: %v", err)

		return SOCKS5Connection{}, false
	}

	const (
		authNotRequired      = 0x00
		authUsernamePassword = 0x02
	)

	selected := byte(authNotRequired)
	if slices.Contains(methods, byte(authUsernamePassword)) {
		selected = authUsernamePassword
	}

	if _, err := conn.Write([]byte{socksVersion5, selected}); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: write method selection: %v", err)

		return SOCKS5Connection{}, false
	}

	if selected != authUsernamePassword {
		return SOCKS5Connection{}, true
	}

	return p.subnegotiateAuth(conn)
}

// subnegotiateAuth reads and accepts an RFC 1929 username/password exchange:
// VER, ULEN, UNAME, PLEN, PASSWD, unconditionally replying with success so
// the calling test controls acceptance by inspecting what was recorded
// rather than by the stand-in rejecting anything.
func (p *SOCKS5Proxy) subnegotiateAuth(conn net.Conn) (SOCKS5Connection, bool) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read auth header: %v", err)

		return SOCKS5Connection{}, false
	}

	username := make([]byte, header[1])
	if _, err := io.ReadFull(conn, username); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read username: %v", err)

		return SOCKS5Connection{}, false
	}

	passwordLen := make([]byte, 1)
	if _, err := io.ReadFull(conn, passwordLen); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read password length: %v", err)

		return SOCKS5Connection{}, false
	}

	password := make([]byte, passwordLen[0])
	if _, err := io.ReadFull(conn, password); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read password: %v", err)

		return SOCKS5Connection{}, false
	}

	const authVersion1 = 1
	if _, err := conn.Write([]byte{authVersion1, 0}); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: write auth reply: %v", err)

		return SOCKS5Connection{}, false
	}

	return SOCKS5Connection{
		Authenticated: true,
		Username:      string(username),
		Password:      string(password),
	}, true
}

// readConnectRequest reads a SOCKS5 CONNECT request (VER, CMD, RSV, ATYP,
// DST.ADDR, DST.PORT) and returns the requested destination in host:port
// form, preserving a domain-name destination exactly as sent.
func (p *SOCKS5Proxy) readConnectRequest(conn net.Conn) (string, bool) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read connect request: %v", err)

		return "", false
	}

	const cmdConnect = 1
	if header[1] != cmdConnect {
		p.t.Errorf("testhelper.SOCKS5StandIn: unsupported command %d", header[1])

		return "", false
	}

	const (
		addrTypeIPv4   = 1
		addrTypeDomain = 3
		addrTypeIPv6   = 4
	)

	var host string

	switch header[3] {
	case addrTypeIPv4:
		addr := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, addr); err != nil {
			p.t.Logf("testhelper.SOCKS5StandIn: read IPv4 address: %v", err)

			return "", false
		}

		host = net.IP(addr).String()

	case addrTypeDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			p.t.Logf("testhelper.SOCKS5StandIn: read domain length: %v", err)

			return "", false
		}

		name := make([]byte, length[0])
		if _, err := io.ReadFull(conn, name); err != nil {
			p.t.Logf("testhelper.SOCKS5StandIn: read domain name: %v", err)

			return "", false
		}

		host = string(name)

	case addrTypeIPv6:
		addr := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, addr); err != nil {
			p.t.Logf("testhelper.SOCKS5StandIn: read IPv6 address: %v", err)

			return "", false
		}

		host = net.IP(addr).String()

	default:
		p.t.Errorf("testhelper.SOCKS5StandIn: unsupported address type %d", header[3])

		return "", false
	}

	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		p.t.Logf("testhelper.SOCKS5StandIn: read port: %v", err)

		return "", false
	}

	port := binary.BigEndian.Uint16(portBytes)

	return net.JoinHostPort(host, strconv.Itoa(int(port))), true
}

// relay dials the real backend and copies bytes both ways until either side
// closes. It ignores the host component of target and always dials
// 127.0.0.1, because every backend a test puts behind this stand-in is a
// local httptest server; only the port from the client's request matters for
// actually reaching it.
func (p *SOCKS5Proxy) relay(conn net.Conn, target string) {
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		p.t.Errorf("testhelper.SOCKS5StandIn: split target %q: %v", target, err)

		return
	}

	backend, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		p.t.Errorf("testhelper.SOCKS5StandIn: dial backend on port %s: %v", port, err)

		return
	}
	defer func() { _ = backend.Close() }()

	var wg sync.WaitGroup

	wg.Go(func() {
		_, _ = io.Copy(backend, conn)
	})

	wg.Go(func() {
		_, _ = io.Copy(conn, backend)
	})

	wg.Wait()
}
