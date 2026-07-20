package cli_test

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"testing"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"
	"github.com/stretchr/testify/require"

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

	hint := cli.PortConventionHint(wrapped, connCtx("pve", 8007), "foo", "pmx")

	require.Contains(t, hint, "port 8007")
	require.Contains(t, hint, "Proxmox Backup Server default")
	require.Contains(t, hint, `context "foo" is set to product pve`)
	require.Contains(t, hint, "'pmx context show foo'")
}

func TestPortConventionHint_PersonaPrefix(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	hint := cli.PortConventionHint(dialErr, connCtx("pdm", 8006), "dc1", "pdm")

	require.Contains(t, hint, "'pdm context show dc1'")
}

func TestPortConventionHint_OwnDefaultPort_Silent(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	require.Empty(t, cli.PortConventionHint(dialErr, connCtx("pve", 8006), "foo", "pmx"))
	require.Empty(t, cli.PortConventionHint(dialErr, connCtx("pbs", 8007), "foo", "pmx"))
}

func TestPortConventionHint_NonStandardPort_Silent(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	require.Empty(t, cli.PortConventionHint(dialErr, connCtx("pve", 9999), "foo", "pmx"))
}

func TestPortConventionHint_NonConnectionError_Silent(t *testing.T) {
	require.Empty(t, cli.PortConventionHint(errors.New("HTTP 500"), connCtx("pve", 8007), "foo", "pmx"))
	require.Empty(t, cli.PortConventionHint(nil, connCtx("pve", 8007), "foo", "pmx"))
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	require.Empty(t, cli.PortConventionHint(dialErr, nil, "foo", "pmx"))
}

func TestPortConventionHint_TLSRecordError_Fires(t *testing.T) {
	var recErr tls.RecordHeaderError
	wrapped := fmt.Errorf("request: %w", recErr)

	hint := cli.PortConventionHint(wrapped, connCtx("pbs", 8443), "b1", "pmx")

	require.Contains(t, hint, "Proxmox Datacenter Manager default")
}

func TestUnreachableHint_Fires(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}
	wrapped := fmt.Errorf("GET /version: %w", dialErr)

	hint := cli.UnreachableHint(wrapped, connCtx("pve", 8006), "lab", "pmx")

	require.Contains(t, hint, "could not reach h:8006")
	require.Contains(t, hint, "dig +short h")
	require.Contains(t, hint, "nc -vz h 8006")
	require.Contains(t, hint, "pmx context show lab")
	require.Contains(t, hint, "pmx context update lab --host <address>")
}

func TestUnreachableHint_ZeroPortUsesProductDefault(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}

	hint := cli.UnreachableHint(dialErr, connCtx("pbs", 0), "backup", "pbs")

	require.Contains(t, hint, "could not reach h:8007")
	require.Contains(t, hint, "pbs context show backup")
}

func TestUnreachableHint_NonConnectionError_Silent(t *testing.T) {
	require.Empty(t, cli.UnreachableHint(errors.New("HTTP 500"), connCtx("pve", 8006), "lab", "pmx"))
	require.Empty(t, cli.UnreachableHint(nil, connCtx("pve", 8006), "lab", "pmx"))

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	require.Empty(t, cli.UnreachableHint(dialErr, nil, "lab", "pmx"))
}
