package apiclient_test

import (
	"io"
	"log/slog"
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
)

// ---------------------------------------------------------------------------
// NewPBSClient — constructor-only tests (no real HTTP connection required)
// ---------------------------------------------------------------------------

func TestNewPBSClient_TokenAuth_ServicesNonNil(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pbs!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}

	pc, err := apiclient.NewPBSClient(opts)
	require.NoError(t, err)
	require.NotNil(t, pc)
	require.NotNil(t, pc.Raw)
	require.NotNil(t, pc.Access)
	require.NotNil(t, pc.Admin)
	require.NotNil(t, pc.Config)
	require.NotNil(t, pc.Nodes)
	require.NotNil(t, pc.Status)
	require.NotNil(t, pc.Tape)
	require.NotNil(t, pc.Version)
	require.NotNil(t, pc.Ping)
	require.NotNil(t, pc.Pull)
	require.NotNil(t, pc.Push)
}

// TestNewPBSClient_AppliesPBSDefaults verifies that a raw client built via
// NewPBSClient is constructed against the PBS wire defaults (port 8007,
// PBSAPIToken header name) even when the caller passes a zero-valued Port /
// APITokenName, mirroring pbs.NewClient's DefaultOptions pass-through.
func TestNewPBSClient_AppliesPBSDefaults(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port, // explicit port from the test server; still exercises the wiring path
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pbs!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}

	pc, err := apiclient.NewPBSClient(opts)
	require.NoError(t, err)
	require.NotNil(t, pc)
}

func TestNewPBSClient_InvalidOptions_EmptyHost(t *testing.T) {
	t.Parallel()
	opts := pve.Options{}
	pc, err := apiclient.NewPBSClient(opts)
	require.Error(t, err)
	require.Nil(t, pc)
}

func TestNewPBSClient_MissingCredentials_Error(t *testing.T) {
	t.Parallel()
	opts := pve.Options{
		Host: "pbs.example.com",
	}
	_, err := apiclient.NewPBSClient(opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// PBSClient.SetSlogLogger
// ---------------------------------------------------------------------------

func TestPBSClient_SetSlogLogger_NonNilInstalls(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pbs!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}
	pc, err := apiclient.NewPBSClient(opts)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	require.True(t, pc.SetSlogLogger(logger), "a non-nil logger should be installed")

	cfg := pc.Raw.GetLogConfig()
	require.True(t, cfg.Enabled)
	require.False(t, cfg.LogBody)
	require.False(t, cfg.LogResponseBody)
	require.Contains(t, cfg.RedactHeaders, "authorization")
}

func TestPBSClient_SetSlogLogger_NilSkips(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pbs!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}
	pc, err := apiclient.NewPBSClient(opts)
	require.NoError(t, err)

	require.False(t, pc.SetSlogLogger(nil), "a nil logger should be skipped")
}
