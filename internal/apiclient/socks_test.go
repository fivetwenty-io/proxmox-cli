package apiclient_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// socksURL parses rawURL for a test, failing it on a parse error.
func socksURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()

	u, err := url.Parse(rawURL)
	require.NoError(t, err)

	return u
}

// requireDialError asserts that err is the dial *net.OpError every failed
// SOCKS dial returns, which the client library treats as terminal, and
// reports whether it is a timeout.
func requireDialError(t *testing.T, err error) bool {
	t.Helper()

	require.Error(t, err)

	var op *net.OpError
	require.ErrorAs(t, err, &op)
	require.Equal(t, "dial", op.Op, "a SOCKS failure must read as a dial failure so it is not retried")

	return op.Timeout()
}

// TestSOCKSDial_StalledConnectIsBounded is the blackholed-target case: the
// proxy takes the CONNECT and never answers, because its own connect to the
// target is still pending. The dial must give up at its bound, say which
// stage it was waiting on, and report a timeout.
func TestSOCKSDial_StalledConnectIsBounded(t *testing.T) {
	t.Parallel()

	proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{StallConnect: true})
	dial := apiclient.SOCKSDialContext(socksURL(t, "socks5h://"+proxy.Addr), 300*time.Millisecond, nil)

	start := time.Now()
	conn, err := dial(context.Background(), "tcp", "pve.invalid:8006")
	elapsed := time.Since(start)

	require.Nil(t, conn)

	require.True(t, requireDialError(t, err), "a stalled CONNECT is a timeout")
	require.Equal(t, "dial tcp pve.invalid:8006: socks5h proxy "+proxy.Addr+
		" did not connect to the target within 300ms", err.Error())
	require.Less(t, elapsed, 2*time.Second)
	require.GreaterOrEqual(t, elapsed, 250*time.Millisecond)

	conns := proxy.Connections()
	require.Len(t, conns, 1)
	require.Equal(t, "pve.invalid:8006", conns[0].Target)
}

// TestSOCKSDial_SilentProxyIsBounded covers a proxy that accepts the TCP
// connection and never answers the greeting.
func TestSOCKSDial_SilentProxyIsBounded(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- c
		}
	}()
	t.Cleanup(func() {
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
	})

	dial := apiclient.SOCKSDialContext(socksURL(t, "socks5://"+listener.Addr().String()), 200*time.Millisecond, nil)

	_, err = dial(context.Background(), "tcp", "10.0.0.1:8006")
	require.True(t, requireDialError(t, err))
	require.Equal(t, "dial tcp 10.0.0.1:8006: socks5 proxy "+listener.Addr().String()+
		" did not answer within 200ms", err.Error())
}

// TestSOCKSDial_ReplyFailureNamesTheReason covers a proxy that could not
// reach the target and said so with an RFC 1928 reply code.
func TestSOCKSDial_ReplyFailureNamesTheReason(t *testing.T) {
	t.Parallel()

	for code, reason := range map[byte]string{
		0x04: "host unreachable",
		0x05: "connection refused",
		0x2a: "reply code 42",
	} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()

			proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{ConnectReply: code})
			dial := apiclient.SOCKSDialContext(socksURL(t, "socks5h://"+proxy.Addr), 5*time.Second, nil)

			_, err := dial(context.Background(), "tcp", "pve.invalid:8006")
			require.False(t, requireDialError(t, err))
			require.Equal(t, "dial tcp pve.invalid:8006: socks5h proxy "+proxy.Addr+
				" could not connect to the target: "+reason, err.Error())
		})
	}
}

// TestSOCKSDial_ClosedWithoutReply covers a proxy that hangs up on the
// CONNECT instead of answering it, which is how OpenSSH's -D proxy reports a
// target it could not reach.
func TestSOCKSDial_ClosedWithoutReply(t *testing.T) {
	t.Parallel()

	proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{CloseOnConnect: true})
	dial := apiclient.SOCKSDialContext(socksURL(t, "socks5h://"+proxy.Addr), 5*time.Second, nil)

	_, err := dial(context.Background(), "tcp", "pve.invalid:8006")
	require.False(t, requireDialError(t, err))
	require.Equal(t, "dial tcp pve.invalid:8006: socks5h proxy "+proxy.Addr+
		" closed the connection without a reply, so it could not reach the target", err.Error())
}

// TestSOCKSDial_RefusedProxy covers a proxy address nothing listens on.
func TestSOCKSDial_RefusedProxy(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	dial := apiclient.SOCKSDialContext(socksURL(t, "socks5://"+addr), 5*time.Second, nil)

	_, err = dial(context.Background(), "tcp", "10.0.0.1:8006")
	requireDialError(t, err)
	require.Contains(t, err.Error(), "dial tcp 10.0.0.1:8006: socks5 proxy "+addr+" is unreachable: ")
	require.Contains(t, err.Error(), "refused")
}

// TestSOCKSDial_Authentication covers both ways a proxy turns a client away
// at the greeting, and pins that no error ever carries the password.
func TestSOCKSDial_Authentication(t *testing.T) {
	t.Parallel()

	t.Run("required but no username set", func(t *testing.T) {
		t.Parallel()

		proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{RequireAuth: true})
		dial := apiclient.SOCKSDialContext(socksURL(t, "socks5://"+proxy.Addr), 5*time.Second, nil)

		_, err := dial(context.Background(), "tcp", "10.0.0.1:8006")
		requireDialError(t, err)
		require.Equal(t, "dial tcp 10.0.0.1:8006: socks5 proxy "+proxy.Addr+
			" requires authentication, and no proxy username is set", err.Error())
	})

	t.Run("credentials rejected", func(t *testing.T) {
		t.Parallel()

		proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{RejectAuth: true})
		u := socksURL(t, "socks5://"+proxy.Addr)
		u.User = url.UserPassword("pmxuser", "s3cret-pass")
		dial := apiclient.SOCKSDialContext(u, 5*time.Second, nil)

		_, err := dial(context.Background(), "tcp", "10.0.0.1:8006")
		requireDialError(t, err)
		require.Equal(t, "dial tcp 10.0.0.1:8006: socks5 proxy "+proxy.Addr+
			" rejected the proxy username and password", err.Error())
		require.NotContains(t, err.Error(), "s3cret-pass")
		require.NotContains(t, err.Error(), "pmxuser")
	})
}

// TestSOCKSDial_CancelledContext pins that a cancelled dial returns at once
// rather than waiting out its bound.
func TestSOCKSDial_CancelledContext(t *testing.T) {
	t.Parallel()

	proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{StallConnect: true})
	dial := apiclient.SOCKSDialContext(socksURL(t, "socks5h://"+proxy.Addr), time.Minute, nil)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	_, err := dial(ctx, "tcp", "pve.invalid:8006")

	requireDialError(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), 5*time.Second)
}

// TestSOCKSDial_TargetAddressForms pins how each form of target travels in
// the CONNECT request: an IPv4 or IPv6 literal as an address, and a name
// unresolved, for the proxy to resolve. Each dial still reaches the origin.
func TestSOCKSDial_TargetAddressForms(t *testing.T) {
	t.Parallel()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(origin.Close)

	_, port, err := net.SplitHostPort(socksURL(t, origin.URL).Host)
	require.NoError(t, err)

	for _, host := range []string{"127.0.0.1", "::1", "pve-target.invalid"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()

			proxy := testhelper.SOCKS5StandIn(t)
			dial := apiclient.SOCKSDialContext(socksURL(t, "socks5://"+proxy.Addr), 5*time.Second, nil)

			target := net.JoinHostPort(host, port)
			tr := &http.Transport{DialContext: dial, DisableKeepAlives: true}
			t.Cleanup(tr.CloseIdleConnections)

			resp, err := (&http.Client{Transport: tr}).Get("http://" + target + "/")
			require.NoError(t, err)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, "ok", string(body))

			conns := proxy.Connections()
			require.Len(t, conns, 1)
			require.Equal(t, target, conns[0].Target)
		})
	}
}

// TestSOCKSDial_ForwardDialerReachesProxy pins that the forward dial opens
// the connection to the proxy, which is how a bastion route reaches it.
func TestSOCKSDial_ForwardDialerReachesProxy(t *testing.T) {
	t.Parallel()

	proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{ConnectReply: 0x04})

	var dialed string

	forward := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr

		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	dial := apiclient.SOCKSDialContext(socksURL(t, "socks5h://"+proxy.Addr), 5*time.Second, forward)

	_, err := dial(context.Background(), "tcp", "pve.invalid:8006")
	require.Error(t, err)
	require.Equal(t, proxy.Addr, dialed)

	t.Run("a forward failure keeps its cause", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("bastion said no")
		failing := func(context.Context, string, string) (net.Conn, error) { return nil, sentinel }

		dial := apiclient.SOCKSDialContext(socksURL(t, "socks5h://proxy.example.com"), 5*time.Second, failing)

		_, err := dial(context.Background(), "tcp", "pve.invalid:8006")
		requireDialError(t, err)
		require.ErrorIs(t, err, sentinel)
		require.Equal(t, "dial tcp pve.invalid:8006: socks5h proxy proxy.example.com:1080 is unreachable: "+
			"bastion said no", err.Error())
	})
}

// TestSOCKSDial_ClientTriesOnce proves the point of the change end to end:
// through the client library, a proxy stalled on a blackholed target costs
// one attempt of about the SOCKS bound, not four attempts of the request
// bound.
func TestSOCKSDial_ClientTriesOnce(t *testing.T) {
	t.Parallel()

	proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{StallConnect: true})

	opts := apiclient.BuildOptions("pve.invalid", 8006, "https", "root@pam", "pam", "pmx=secret",
		"", "", "", true, "")
	opts = apiclient.ApplyTimeoutOptions(opts, apiclient.TimeoutSpec{
		Connect: time.Second, TLSHandshake: time.Second, Request: 20 * time.Second,
	})

	opts, err := apiclient.ApplyProxyOptions(opts, apiclient.ProxySpec{URL: socksURL(t, "socks5h://"+proxy.Addr)},
		300*time.Millisecond)
	require.NoError(t, err)
	require.Nil(t, opts.Proxy, "net/http must not run its own SOCKS negotiation")
	require.NotNil(t, opts.DialContext)

	client, err := pve.NewClient(opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	start := time.Now()
	_, err = client.GetCtx(pve.WithRetryDelay(context.Background(), 10*time.Millisecond), "/version", nil)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Contains(t, err.Error(), "did not connect to the target within 300ms")
	require.NotContains(t, err.Error(), "Client.Timeout exceeded")
	require.Less(t, elapsed, 5*time.Second)
	require.Len(t, proxy.Connections(), 1, "a stalled SOCKS connect is tried once")
}

// TestApplyProxyTransport_Routes pins which transport field each kind of
// route lands on.
func TestApplyProxyTransport_Routes(t *testing.T) {
	t.Parallel()

	forward := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unused") }

	t.Run("socks wraps the dial and clears the proxy", func(t *testing.T) {
		t.Parallel()

		tr := &http.Transport{DialContext: forward, Proxy: http.ProxyFromEnvironment}
		spec := apiclient.ProxySpec{URL: socksURL(t, "socks5://127.0.0.1:1080")}

		require.NoError(t, apiclient.ApplyProxyTransport(tr, spec, time.Second))
		require.Nil(t, tr.Proxy)
		require.NotNil(t, tr.DialContext)
	})

	t.Run("http sets the proxy and keeps the dial", func(t *testing.T) {
		t.Parallel()

		tr := &http.Transport{DialContext: forward}
		spec := apiclient.ProxySpec{URL: socksURL(t, "http://127.0.0.1:3128")}

		require.NoError(t, apiclient.ApplyProxyTransport(tr, spec, time.Second))
		require.NotNil(t, tr.Proxy)

		_, err := tr.DialContext(context.Background(), "tcp", "x:1")
		require.EqualError(t, err, "unused")
	})

	t.Run("direct clears an ambient proxy", func(t *testing.T) {
		t.Parallel()

		tr := &http.Transport{Proxy: http.ProxyFromEnvironment}

		require.NoError(t, apiclient.ApplyProxyTransport(tr, apiclient.ProxySpec{}, time.Second))
		require.Nil(t, tr.Proxy)
	})

	t.Run("an invalid proxy leaves the transport untouched", func(t *testing.T) {
		t.Parallel()

		tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
		spec := apiclient.ProxySpec{URL: socksURL(t, "https://proxy.example.com:"+strconv.Itoa(443))}

		require.Error(t, apiclient.ApplyProxyTransport(tr, spec, time.Second))
		require.NotNil(t, tr.Proxy)
	})
}

// TestSOCKSDial_ReplyAddressTypes pins that the dial reads past every form
// of bound address in a success reply, so the stream starts exactly at the
// target's first byte and an HTTP request through it succeeds.
func TestSOCKSDial_ReplyAddressTypes(t *testing.T) {
	t.Parallel()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(origin.Close)

	for name, atyp := range map[string]byte{"ipv4": 1, "domain": 3, "ipv6": 4} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			proxy := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{ReplyAddrType: atyp})
			dial := apiclient.SOCKSDialContext(socksURL(t, "socks5://"+proxy.Addr), 5*time.Second, nil)

			tr := &http.Transport{DialContext: dial, DisableKeepAlives: true}
			t.Cleanup(tr.CloseIdleConnections)

			resp, err := (&http.Client{Transport: tr}).Get(origin.URL)
			require.NoError(t, err)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, "ok", string(body))
		})
	}
}

// scriptedProxy listens on loopback and answers each connection with the
// bytes of replies, one reply after each read from the client, so a test
// can make a proxy break the protocol at any step.
func scriptedProxy(t *testing.T, replies ...[]byte) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			go func() {
				defer func() { _ = conn.Close() }()

				buf := make([]byte, 512)
				for _, reply := range replies {
					if _, readErr := conn.Read(buf); readErr != nil {
						return
					}

					if _, writeErr := conn.Write(reply); writeErr != nil {
						return
					}
				}
			}()
		}
	}()

	return listener.Addr().String()
}

// TestSOCKSDial_ProtocolErrors covers a proxy that breaks RFC 1928 or RFC
// 1929, and credentials the dial refuses to send.
func TestSOCKSDial_ProtocolErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		user     *url.Userinfo
		replies  [][]byte
		wantTail string
	}{
		{
			name:     "wrong greeting version",
			replies:  [][]byte{{4, 0}},
			wantTail: " broke the SOCKS protocol: its greeting reply carried SOCKS version 4",
		},
		{
			name:     "a method that was not offered",
			replies:  [][]byte{{5, 2}},
			wantTail: " broke the SOCKS protocol: it chose authentication method 2, which was not offered",
		},
		{
			name:     "wrong authentication reply version",
			user:     url.UserPassword("pmx", "pw"),
			replies:  [][]byte{{5, 2}, {5, 0}},
			wantTail: " broke the SOCKS protocol: its authentication reply carried version 5",
		},
		{
			name:     "wrong connect reply version",
			replies:  [][]byte{{5, 0}, {4, 0, 0, 1, 0, 0, 0, 0, 0, 0}},
			wantTail: " broke the SOCKS protocol: its connect reply carried SOCKS version 4",
		},
		{
			name:     "an unknown bound address type",
			replies:  [][]byte{{5, 0}, {5, 0, 0, 9}},
			wantTail: " broke the SOCKS protocol: its connect reply carried address type 9",
		},
		{
			name:     "an empty username",
			user:     url.UserPassword("", "pw"),
			replies:  [][]byte{{5, 2}},
			wantTail: ": the proxy username must be 1 to 255 bytes and the password at most 255 bytes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := scriptedProxy(t, tc.replies...)
			u := socksURL(t, "socks5://"+addr)
			u.User = tc.user

			dial := apiclient.SOCKSDialContext(u, 5*time.Second, nil)

			_, err := dial(context.Background(), "tcp", "10.0.0.1:8006")
			requireDialError(t, err)
			require.Equal(t, "dial tcp 10.0.0.1:8006: socks5 proxy "+addr+tc.wantTail, err.Error())
		})
	}
}

// TestSOCKSDial_BastionFailureStaysReachable pins that a bastion's
// *JumpError, even wrapped in the jump's own dial error, stays reachable
// through errors.As and names the bastion as the route that failed.
func TestSOCKSDial_BastionFailureStaysReachable(t *testing.T) {
	t.Parallel()

	je := &apiclient.JumpError{Chain: "admin@bastion", Addr: "proxy.example.com:1080", ExitStatus: 255}
	forward := func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: je}
	}

	dial := apiclient.SOCKSDialContext(socksURL(t, "socks5h://proxy.example.com:1080"), 5*time.Second, forward)

	_, err := dial(context.Background(), "tcp", "pve.invalid:8006")
	requireDialError(t, err)

	got, ok := errors.AsType[*apiclient.JumpError](err)
	require.True(t, ok)
	require.Same(t, je, got)
	require.Contains(t, err.Error(), "socks5h proxy proxy.example.com:1080 is unreachable through the bastion: ")
	require.ErrorIs(t, err, apiclient.ErrJump)
}
