package cli_test

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

func TestAuthHint_Unauthorized(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", pveerrors.ErrUnauthorized},
		{"wrapped sentinel", fmt.Errorf("cluster status: %w", pveerrors.ErrUnauthorized)},
		{"typed auth error", &pveerrors.AuthenticationError{Realm: "pam"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := cli.AuthHint(tc.err)
			require.Contains(t, hint, "HTTP 401")
			require.Contains(t, hint, "USER@REALM!TOKENID=SECRET")
		})
	}
}

func TestAuthHint_Forbidden(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", pveerrors.ErrForbidden},
		{"wrapped sentinel", fmt.Errorf("node status: %w", pveerrors.ErrForbidden)},
		{"typed permission error", &pveerrors.PermissionError{What: "/nodes/pve1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := cli.AuthHint(tc.err)
			require.Contains(t, hint, "HTTP 403")
			require.Contains(t, hint, "Privilege Separation")
		})
	}
}

func TestAuthHint_NonAuthErrorsReturnEmpty(t *testing.T) {
	cases := []error{
		nil,
		errors.New("boom"),
		pveerrors.ErrNotFound,
		&pveerrors.ParameterError{},
	}
	for _, err := range cases {
		require.Empty(t, cli.AuthHint(err))
	}
}

func connCtx(product string, port int) *config.Context {
	return &config.Context{Host: "h", Port: port, Product: product}
}

func TestPortConventionHint_Fires(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	wrapped := fmt.Errorf("GET /version: %w", dialErr)

	hint := cli.PortConventionHint(wrapped, connCtx("pve", 8007), cli.Connection{}, "foo", "pmx")

	require.Contains(t, hint, "port 8007")
	require.Contains(t, hint, "Proxmox Backup Server default")
	require.Contains(t, hint, `context "foo" is set to product pve`)
	require.Contains(t, hint, "'pmx context show foo'")
}

func TestPortConventionHint_PersonaPrefix(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	hint := cli.PortConventionHint(dialErr, connCtx("pdm", 8006), cli.Connection{}, "dc1", "pdm")

	require.Contains(t, hint, "'pdm context show dc1'")
}

func TestPortConventionHint_OwnDefaultPort_Silent(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	require.Empty(t, cli.PortConventionHint(dialErr, connCtx("pve", 8006), cli.Connection{}, "foo", "pmx"))
	require.Empty(t, cli.PortConventionHint(dialErr, connCtx("pbs", 8007), cli.Connection{}, "foo", "pmx"))
}

func TestPortConventionHint_NonStandardPort_Silent(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	require.Empty(t, cli.PortConventionHint(dialErr, connCtx("pve", 9999), cli.Connection{}, "foo", "pmx"))
}

func TestPortConventionHint_NonConnectionError_Silent(t *testing.T) {
	require.Empty(t, cli.PortConventionHint(errors.New("HTTP 500"), connCtx("pve", 8007), cli.Connection{}, "foo", "pmx"))
	require.Empty(t, cli.PortConventionHint(nil, connCtx("pve", 8007), cli.Connection{}, "foo", "pmx"))
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	require.Empty(t, cli.PortConventionHint(dialErr, nil, cli.Connection{}, "foo", "pmx"))
}

func TestPortConventionHint_TLSRecordError_Fires(t *testing.T) {
	var recErr tls.RecordHeaderError
	wrapped := fmt.Errorf("request: %w", recErr)

	hint := cli.PortConventionHint(wrapped, connCtx("pbs", 8443), cli.Connection{}, "b1", "pmx")

	require.Contains(t, hint, "Proxmox Datacenter Manager default")
}

func TestUnreachableHint_Fires(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}
	wrapped := fmt.Errorf("GET /version: %w", dialErr)

	hint := cli.UnreachableHint(wrapped, connCtx("pve", 8006), cli.Connection{}, "lab", "pmx")

	require.Contains(t, hint, "could not reach h:8006")
	require.Contains(t, hint, "dig +short h")
	require.Contains(t, hint, "nc -vz h 8006")
	require.Contains(t, hint, "pmx context show lab")
	require.Contains(t, hint, "pmx context update lab --host <address>")
}

func TestUnreachableHint_ZeroPortUsesProductDefault(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}

	hint := cli.UnreachableHint(dialErr, connCtx("pbs", 0), cli.Connection{}, "backup", "pbs")

	require.Contains(t, hint, "could not reach h:8007")
	require.Contains(t, hint, "pbs context show backup")
}

func TestUnreachableHint_NonConnectionError_Silent(t *testing.T) {
	require.Empty(t, cli.UnreachableHint(errors.New("HTTP 500"), connCtx("pve", 8006), cli.Connection{}, "lab", "pmx"))
	require.Empty(t, cli.UnreachableHint(nil, connCtx("pve", 8006), cli.Connection{}, "lab", "pmx"))

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	require.Empty(t, cli.UnreachableHint(dialErr, nil, cli.Connection{}, "lab", "pmx"))
}

// overriddenRoute resolves the connection context "lab" takes under
// --api-endpoint raw, the way the root resolves it, so the hint tests read
// the same Connection a failing command leaves on Deps.Route.
func overriddenRoute(t *testing.T, ctx *config.Context, raw string) cli.Connection {
	t.Helper()

	ep, err := cli.ParseEndpoint("--api-endpoint", raw)
	require.NoError(t, err)

	conn, err := cli.ResolveConnection("lab", ctx, cli.ConnectionOverrides{
		Host: ep.Host, Port: ep.Port, Protocol: ep.Protocol, EndpointSource: "--api-endpoint",
	})
	require.NoError(t, err)

	return conn
}

func TestUnreachableHint_NamesRoute(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	t.Run("an endpoint override names the overridden host and port", func(t *testing.T) {
		ctx := connCtx("pve", 8006)
		route := overriddenRoute(t, ctx, "pve9:9999")

		hint := cli.UnreachableHint(fmt.Errorf("GET /version: %w", dialErr), ctx, route, "lab", "pmx")

		require.True(t, strings.HasPrefix(hint, "hint: could not reach pve9:9999: "), hint)
		require.Contains(t, hint, "dig +short pve9\n")
		require.Contains(t, hint, "nc -vz pve9 9999\n")
		require.Contains(t, hint, `The endpoint came from --api-endpoint rather than from context "lab"`)
		require.Contains(t, hint, "pmx context show lab")
		require.NotContains(t, hint, "h:8006", "the stored endpoint was not dialled and must not be named")
		require.NotContains(t, hint, "context update", "repointing the context would not fix an override")
	})

	t.Run("a silent bastion failure names the route and its exit status", func(t *testing.T) {
		route := cli.Connection{
			ContextName: "lab", Host: "pve1", Port: 8006, Protocol: "https",
			Jump: apiclient.JumpSpec{Chain: "admin@bastion.example.com"},
		}
		jumpErr := &apiclient.JumpError{
			Chain: "admin@bastion.example.com", Addr: "pve1:8006", ExitStatus: 255,
		}
		err := fmt.Errorf(`Get "https://pve1:8006/api2/json/version": %w`,
			&net.OpError{Op: "dial", Net: "tcp", Err: jumpErr})

		hint := cli.UnreachableHint(err, connCtx("pve", 8006), route, "lab", "pmx")

		require.True(t, strings.HasPrefix(hint,
			"hint: could not reach pve1:8006 via jump admin@bastion.example.com: "), hint)
		require.Contains(t, hint, "The bastion reported: ssh exited with status 255 and printed nothing\n")
		require.NotContains(t, hint, "dig ")
		require.NotContains(t, hint, "nc -vz")
	})

	t.Run("a bastion error that reaches the hint unwrapped still counts", func(t *testing.T) {
		route := cli.Connection{Host: "pve1", Port: 8006, Jump: apiclient.JumpSpec{Chain: "b"}}
		jumpErr := &apiclient.JumpError{Chain: "b", Addr: "pve1:8006", Stderr: "channel 0: open failed: connect failed"}

		hint := cli.UnreachableHint(jumpErr, connCtx("pve", 8006), route, "lab", "pmx")

		require.Contains(t, hint, "via jump b")
		require.Contains(t, hint, "The bastion reported: channel 0: open failed: connect failed\n")
	})

	t.Run("a proxy names the redacted route and gives no dig or nc advice", func(t *testing.T) {
		proxyURL, err := url.Parse("socks5h://pmx:s3cret@proxy.example.com:1080")
		require.NoError(t, err)

		route := cli.Connection{
			Host: "pve1", Port: 8006, Protocol: "https",
			Proxy: apiclient.ProxySpec{URL: proxyURL},
		}

		hint := cli.UnreachableHint(dialErr, connCtx("pve", 8006), route, "lab", "pmx")

		require.Contains(t, hint, "could not reach pve1:8006 via proxy socks5h://")
		require.Contains(t, hint, "proxy.example.com:1080")
		require.NotContains(t, hint, "s3cret", "the proxy password must never reach the terminal")
		require.NotContains(t, hint, "bastion reported")
		require.NotContains(t, hint, "dig ")
		require.NotContains(t, hint, "nc -vz")
	})

	t.Run("the route advice names only the hops the route has", func(t *testing.T) {
		proxyURL, err := url.Parse("socks5h://proxy.example.com:1080")
		require.NoError(t, err)

		jump := apiclient.JumpSpec{Chain: "admin@bastion.example.com"}
		proxy := apiclient.ProxySpec{URL: proxyURL}

		for _, tc := range []struct {
			name  string
			route cli.Connection
			want  string
		}{
			{"a bastion", cli.Connection{Host: "pve1", Port: 8006, Jump: jump},
				"confirm that the bastion accepts connections and can itself reach pve1:8006."},
			{"a proxy", cli.Connection{Host: "pve1", Port: 8006, Proxy: proxy},
				"confirm that the proxy accepts connections and can itself reach pve1:8006."},
			{"a bastion and a proxy", cli.Connection{Host: "pve1", Port: 8006, Jump: jump, Proxy: proxy},
				"confirm that the bastion and the proxy accept connections and can reach pve1:8006."},
		} {
			t.Run(tc.name, func(t *testing.T) {
				hint := cli.UnreachableHint(dialErr, connCtx("pve", 8006), tc.route, "lab", "pmx")

				require.Contains(t, strings.Join(strings.Fields(hint), " "), tc.want, hint)
			})
		}
	})

	t.Run("an unset route reads the context as before", func(t *testing.T) {
		hint := cli.UnreachableHint(dialErr, connCtx("pve", 8006), cli.Connection{}, "lab", "pmx")

		require.Equal(t, `hint: could not reach h:8006: the connection was never established.
  Nothing is listening there, or a firewall or proxy is dropping the connection.
  Confirm the name resolves to the node itself, not a CDN or reverse proxy:
    dig +short h
  Check that the API port accepts connections:
    nc -vz h 8006
  Inspect or repoint the context:
    pmx context show lab
    pmx context update lab --host <address>`, hint)
	})
}

func TestPortConventionHint_UsesResolvedEndpoint(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	const storedPortHint = `hint: port 8007 is the Proxmox Backup Server default; ` +
		`context "lab" is set to product pve: check 'pmx context show lab'`

	t.Run("an overridden port that is another product's default names its source", func(t *testing.T) {
		ctx := connCtx("pve", 8006)
		route := overriddenRoute(t, ctx, "pve9:8007")

		hint := cli.PortConventionHint(dialErr, ctx, route, "lab", "pmx")

		require.Equal(t, `hint: port 8007 from --api-endpoint is the Proxmox Backup Server default; `+
			`context "lab" is set to product pve: check the endpoint you passed`, hint)
	})

	t.Run("an overridden port from the environment names the variable", func(t *testing.T) {
		ctx := connCtx("pve", 8006)

		ep, err := cli.ParseEndpoint("$PMX_API_ENDPOINT", "pve9:8007")
		require.NoError(t, err)

		route, err := cli.ResolveConnection("lab", ctx, cli.ConnectionOverrides{
			Host: ep.Host, Port: ep.Port, Protocol: ep.Protocol, EndpointSource: "$PMX_API_ENDPOINT",
		})
		require.NoError(t, err)

		require.Equal(t, `hint: port 8007 from $PMX_API_ENDPOINT is the Proxmox Backup Server default; `+
			`context "lab" is set to product pve: check the endpoint in $PMX_API_ENDPOINT`,
			cli.PortConventionHint(dialErr, ctx, route, "lab", "pmx"))
	})

	t.Run("an override on the product's own port silences the stored port", func(t *testing.T) {
		ctx := connCtx("pve", 8007)
		route := overriddenRoute(t, ctx, "pve9:8006")

		require.Empty(t, cli.PortConventionHint(dialErr, ctx, route, "lab", "pmx"),
			"the stored port was not dialled, so it must not be judged")
	})

	t.Run("a route without an override reads its own port", func(t *testing.T) {
		ctx := connCtx("pve", 8007)
		route, err := cli.ResolveConnection("lab", ctx, cli.ConnectionOverrides{})
		require.NoError(t, err)

		require.Equal(t, storedPortHint, cli.PortConventionHint(dialErr, ctx, route, "lab", "pmx"))
	})

	t.Run("an unset route reads the context as before", func(t *testing.T) {
		require.Equal(t, storedPortHint,
			cli.PortConventionHint(dialErr, connCtx("pve", 8007), cli.Connection{}, "lab", "pmx"))
		require.Empty(t, cli.PortConventionHint(dialErr, connCtx("pve", 0), cli.Connection{}, "lab", "pmx"))
	})
}

func TestUnreachableHint_StripsIPv6Brackets(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	ctx := connCtx("pve", 8006)
	route := overriddenRoute(t, ctx, "[::1]:8006")
	require.Equal(t, "[::1]", route.Host, "the resolver keeps an IPv6 host bracketed")

	hint := cli.UnreachableHint(dialErr, ctx, route, "lab", "pmx")

	firstLine, _, _ := strings.Cut(hint, "\n")
	require.Equal(t, "hint: could not reach [::1]:8006: the connection was never established.", firstLine)
	require.Contains(t, hint, "    dig +short ::1\n")
	require.Contains(t, hint, "    nc -vz ::1 8006\n")
	require.NotContains(t, hint, "dig +short [")
	require.NotContains(t, hint, "nc -vz [")
}
