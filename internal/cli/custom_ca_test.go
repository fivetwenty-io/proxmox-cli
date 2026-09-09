package cli_test

import (
	"context"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestContextCustomCARealTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PVEAPIToken=root@pam!test=secret", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"data":{"version":"test"}}`)
	}))
	defer srv.Close()
	host, portText, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600))
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	require.NoError(t, os.WriteFile(invalid, []byte("invalid"), 0600))
	for _, tc := range []struct {
		name, ca, host                             string
		insecure, wantBuildError, wantRequestError bool
	}{
		{name: "declared CA", ca: ca, host: host},
		{name: "system roots reject self signed", host: host, wantRequestError: true},
		{name: "hostname still verified", ca: ca, host: "localhost", wantRequestError: true},
		{name: "invalid PEM", ca: invalid, host: host, wantBuildError: true},
		{name: "missing PEM", ca: ca + "-missing", host: host, wantBuildError: true},
		{name: "explicit insecure overrides CA", ca: invalid, host: host, insecure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Contexts: map[string]*config.Context{"test": {
				Host: tc.host, Port: port, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "test", Secret: "secret"},
				TLS:  config.TLSBlock{CACert: tc.ca, Insecure: tc.insecure},
			}}}
			cmd := &cobra.Command{}
			cmd.SetErr(io.Discard)
			client, _, err := cli.BuildContextClient(cmd, cfg, "", "test", false, func() bool { return false })
			if tc.wantBuildError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			_, err = client.Raw.GetCtx(context.Background(), "/version", nil)
			if tc.wantRequestError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
