package node_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newSwapTestCert returns a self-signed certificate for localhost and
// 127.0.0.1 that can also act as its own CA, so a context can trust it
// either by fingerprint or through a CA bundle.
func newSwapTestCert(t *testing.T, name string) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// colonFingerprint renders the SHA-256 of der in the colon-separated form a
// context pin stores.
func colonFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// certSwapServer serves f's routes over TLS. It presents oldCert until a
// request reaches an ACME certificate endpoint and newCert from then on,
// which is what pveproxy does once the task installs the new certificate.
// Every response closes its connection, so the next request handshakes
// again and sees the swap.
func certSwapServer(t *testing.T, f *testhelper.FakePVE, oldCert, newCert tls.Certificate) *httptest.Server {
	t.Helper()

	var swapped atomic.Bool
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		f.Server.Config.Handler.ServeHTTP(w, r)
		if strings.HasSuffix(r.URL.Path, "/certificates/acme/certificate") {
			swapped.Store(true)
		}
	}))
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if swapped.Load() {
				return &newCert, nil
			}
			return &oldCert, nil
		},
	}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func TestACMETrustHint_WaitFailsTLSAfterSwap(t *testing.T) {
	oldCert := newSwapTestCert(t, "pve1 self-signed")
	newCert := newSwapTestCert(t, "pve1 acme")

	caPath := filepath.Join(t.TempDir(), "pve-root-ca.pem")
	require.NoError(t, os.WriteFile(caPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: oldCert.Certificate[0]}), 0o600))

	for name, tc := range map[string]struct {
		tls  config.TLSBlock
		args []string
		want string
	}{
		"pinned order": {
			tls:  config.TLSBlock{Fingerprint: colonFingerprint(oldCert.Certificate[0])},
			args: orderPVE1,
			want: `pmx context update lab --fingerprint ""`,
		},
		"CA bundle renew": {
			tls:  config.TLSBlock{CACert: caPath},
			args: []string{"--node", "pve1", "node", "cert", "acme", "renew", "--yes"},
			want: `pmx context update lab --ca-cert ""`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := trustFake(t, "pve1")
			ts := certSwapServer(t, f, oldCert, newCert)
			_, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
			require.NoError(t, err)
			port, err := strconv.Atoi(portStr)
			require.NoError(t, err)

			_, stderr, err := runTrustCmdAt(t, "https", "localhost", port, tc.tls, nil, tc.args...)
			require.Error(t, err, "the wait cannot confirm the task, so the verb still fails")
			require.Contains(t, err.Error(), `node "pve1" most likely installed the new certificate`)

			require.Contains(t, stderr, `node "pve1" most likely serves a new ACME certificate already`)
			require.Contains(t, stderr, "Waiting for the task failed TLS verification")
			require.Contains(t, stderr, tc.want)
			require.Contains(t, stderr, "pmx node cert acme status --node pve1",
				"the hint names the node command path the verb ran under")
		})
	}
}

func TestACMETrustHint_OtherWaitFailureHasNoHint(t *testing.T) {
	f := trustFake(t, "pve1")
	upid := trustTestUPID
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "task status unavailable")
	})

	_, stderr, err := runTrustCmd(t, f, config.TLSBlock{Fingerprint: trustTestPin}, nil, orderPVE1...)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "most likely installed")
	require.NotContains(t, stderr, "hint:", "a wait failure unrelated to TLS says nothing about the certificate")
}
