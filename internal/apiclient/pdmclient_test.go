package apiclient_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// NewPDMClient — constructor-only tests (no real HTTP connection required)
// ---------------------------------------------------------------------------

func TestNewPDMClient_WiresAllServices(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pdm!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}

	pc, err := apiclient.NewPDMClient(opts)
	require.NoError(t, err)
	require.NotNil(t, pc)
	require.NotNil(t, pc.Raw)
	require.NotNil(t, pc.Access)
	require.NotNil(t, pc.AutoInstall)
	require.NotNil(t, pc.Ceph)
	require.NotNil(t, pc.Config)
	require.NotNil(t, pc.Nodes)
	require.NotNil(t, pc.Pbs)
	require.NotNil(t, pc.Ping)
	require.NotNil(t, pc.Pve)
	require.NotNil(t, pc.Remotes)
	require.NotNil(t, pc.Resources)
	require.NotNil(t, pc.Sdn)
	require.NotNil(t, pc.Subscriptions)
	require.NotNil(t, pc.Version)
}

// TestNewPDMClient_AppliesPDMDefaults verifies that a raw client built via
// NewPDMClient is constructed against the PDM wire defaults (port 8443,
// PDMAPIToken header name) by pointing it at a FakePDM server and issuing a
// real Ping request, then asserting the fake actually received it.
func TestNewPDMClient_AppliesPDMDefaults(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePDM(t)

	var received bool
	f.HandleFunc("GET /api2/json/ping", func(w http.ResponseWriter, _ *http.Request) {
		received = true
		testhelper.WriteData(w, map[string]bool{"pong": true})
	})

	pc, err := apiclient.NewPDMClient(f.Options)
	require.NoError(t, err)
	require.NotNil(t, pc)

	_, err = pc.Ping.Ping(context.Background())
	require.NoError(t, err)
	require.True(t, received)
}

func TestNewPDMClient_InvalidOptions_EmptyHost(t *testing.T) {
	t.Parallel()
	opts := pve.Options{}
	pc, err := apiclient.NewPDMClient(opts)
	require.Error(t, err)
	require.Nil(t, pc)
}

func TestNewPDMClient_MissingCredentials_Error(t *testing.T) {
	t.Parallel()
	opts := pve.Options{
		Host: "pdm.example.com",
	}
	_, err := apiclient.NewPDMClient(opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// PDMClient.SetSlogLogger
// ---------------------------------------------------------------------------

func TestPDMClient_SetSlogLogger_NonNilInstalls(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pdm!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}
	pc, err := apiclient.NewPDMClient(opts)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	require.True(t, pc.SetSlogLogger(logger), "a non-nil logger should be installed")

	cfg := pc.Raw.GetLogConfig()
	require.True(t, cfg.Enabled)
	require.False(t, cfg.LogBody)
	require.False(t, cfg.LogResponseBody)
	require.Contains(t, cfg.RedactHeaders, "authorization")
}

func TestPDMClient_SetSlogLogger_NilSkips(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pdm!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}
	pc, err := apiclient.NewPDMClient(opts)
	require.NoError(t, err)

	require.False(t, pc.SetSlogLogger(nil), "a nil logger should be skipped")
}
