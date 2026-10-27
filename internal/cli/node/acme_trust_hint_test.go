package node_test

import (
	"bytes"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// trustTestPin is a well-formed SHA-256 pin. The fake server speaks plain
// HTTP, so the pin is stored and resolved but never checked against a
// certificate, which is all these tests need.
var trustTestPin = strings.TrimSuffix(strings.Repeat("AB:", 32), ":")

const trustTestUPID = "UPID:pve1:00000001:00000002:AABBCCDD:acmeordercert::root@pam:"

// trustFake serves an ACME order and renewal on pve1 and pve2, a cluster
// status in which servingNode answers the API, and a pve1 config whose ACME
// domain is localhost.
func trustFake(t *testing.T, servingNode string) *testhelper.FakePVE {
	t.Helper()
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/config", map[string]any{"acmedomain0": "localhost,plugin=cf-dns"})
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "cluster", "name": "lab", "quorate": 1},
		map[string]any{"type": "node", "name": "pve1", "ip": "192.168.1.10", "local": boolInt(servingNode == "pve1")},
		map[string]any{"type": "node", "name": "pve2", "ip": "192.168.1.11", "local": boolInt(servingNode == "pve2")},
	})
	for _, node := range []string{"pve1", "pve2"} {
		upid := strings.Replace(trustTestUPID, "pve1", node, 1)
		reply := func(w http.ResponseWriter, _ *http.Request) { testhelper.WriteData(w, upid) }
		f.HandleFunc("POST /api2/json/nodes/"+node+"/certificates/acme/certificate", reply)
		f.HandleFunc("PUT /api2/json/nodes/"+node+"/certificates/acme/certificate", reply)
		f.HandleJSON("GET /api2/json/nodes/"+node+"/tasks/"+upid+"/status", map[string]any{
			"status": "stopped", "exitstatus": "OK", "upid": upid,
		})
	}
	return f
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// runTrustCmd runs args through a root whose single context "lab" points at
// f by the name localhost, which trustFake configures as pve1's ACME domain,
// with the given TLS block, and returns stdout and stderr apart.
func runTrustCmd(t *testing.T, f *testhelper.FakePVE, tls config.TLSBlock, env map[string]string, args ...string) (
	string, string, error,
) {
	t.Helper()
	return runTrustCmdHost(t, f, "localhost", tls, env, args...)
}

// runTrustCmdHost is runTrustCmd with the context's host spelled as host.
func runTrustCmdHost(
	t *testing.T, f *testhelper.FakePVE, host string, tls config.TLSBlock, env map[string]string, args ...string,
) (string, string, error) {
	t.Helper()
	_, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return runTrustCmdAt(t, "http", host, port, tls, env, args...)
}

// runTrustCmdAt runs args through a root whose single context "lab" reaches
// protocol://host:port with the given TLS block, and returns stdout and
// stderr apart.
func runTrustCmdAt(
	t *testing.T, protocol, host string, port int, tls config.TLSBlock, env map[string]string, args ...string,
) (string, string, error) {
	t.Helper()
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")
	for _, name := range testhelper.APIEnvNames() {
		t.Setenv(name, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: host, Port: port, Protocol: protocol,
				Auth: config.AuthBlock{
					Type: "token", Username: "root@pam", TokenID: "test",
					Secret: "${PMX_TEST_TRUST_SECRET}",
				},
				TLS: tls,
			},
		},
	}
	t.Setenv("PMX_TEST_TRUST_SECRET", "00000000-0000-0000-0000-000000000000")
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(path, cfg))

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	addNodeGroup(root)

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--config", path}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

var orderPVE1 = []string{"--node", "pve1", "node", "cert", "acme", "order", "--yes"}

func TestACMETrustHint_ContextPinAfterOrder(t *testing.T) {
	f := trustFake(t, "pve1")
	stdout, stderr, err := runTrustCmd(t, f, config.TLSBlock{Fingerprint: trustTestPin}, nil, orderPVE1...)
	require.NoError(t, err)

	require.Contains(t, stdout, "ACME certificate ordered")
	require.NotContains(t, stdout, "hint:", "the hint goes to stderr only")
	for _, want := range []string{
		`hint: context "lab" pins the API certificate by fingerprint, and node "pve1" now serves a new ACME certificate`,
		`pmx context update lab --fingerprint ""`,
		"pmx context validate lab --connect",
		"openssl x509 -in /etc/pve/local/pveproxy-ssl.pem -noout -fingerprint -sha256",
		"pmx context update lab --fingerprint <fingerprint>",
	} {
		require.Contains(t, stderr, want)
	}
}

func TestACMETrustHint_RenewAndAsync(t *testing.T) {
	f := trustFake(t, "pve1")
	_, stderr, err := runTrustCmd(t, f, config.TLSBlock{Fingerprint: trustTestPin}, nil,
		"--node", "pve1", "node", "cert", "acme", "renew", "--yes")
	require.NoError(t, err)
	require.Contains(t, stderr, `node "pve1" now serves a new ACME certificate`)

	_, stderr, err = runTrustCmd(t, f, config.TLSBlock{Fingerprint: trustTestPin}, nil,
		append([]string{"--async"}, orderPVE1...)...)
	require.NoError(t, err)
	require.Contains(t, stderr, `node "pve1" will serve a new ACME certificate once the task finishes`)
}

func TestACMETrustHint_OtherNodeIsSilent(t *testing.T) {
	f := trustFake(t, "pve1")
	_, stderr, err := runTrustCmd(t, f, config.TLSBlock{Fingerprint: trustTestPin}, nil,
		"--node", "pve2", "node", "cert", "acme", "order", "--yes")
	require.NoError(t, err)
	require.NotContains(t, stderr, "hint:", "pve2's certificate does not serve this context's API")
}

func TestACMETrustHint_UnknownServingNodeStillHints(t *testing.T) {
	f := trustFake(t, "")
	_, stderr, err := runTrustCmd(t, f, config.TLSBlock{Fingerprint: trustTestPin}, nil, orderPVE1...)
	require.NoError(t, err)
	require.Contains(t, stderr, `pmx context update lab --fingerprint ""`)
}

func TestACMETrustHint_InsecureAndSystemTrustAreSilent(t *testing.T) {
	for name, tls := range map[string]config.TLSBlock{
		"insecure":     {Insecure: true},
		"system trust": {},
	} {
		t.Run(name, func(t *testing.T) {
			f := trustFake(t, "pve1")
			_, stderr, err := runTrustCmd(t, f, tls, nil, orderPVE1...)
			require.NoError(t, err)
			require.NotContains(t, stderr, "hint:")
		})
	}
}

func TestACMETrustHint_TOFUAndCACert(t *testing.T) {
	f := trustFake(t, "pve1")
	_, stderr, err := runTrustCmd(t, f, config.TLSBlock{Tofu: true}, nil, orderPVE1...)
	require.NoError(t, err)
	require.Contains(t, stderr, "trusts the API certificate it saw first (tls.tofu)")
	require.Contains(t, stderr, "pmx context update lab --tofu=false")
	require.NotContains(t, stderr, "openssl", "re-pinning applies to a fingerprint pin only")

	caPath := filepath.Join(t.TempDir(), "pve-root-ca.pem")
	_, stderr, err = runTrustCmd(t, f, config.TLSBlock{CACert: caPath}, nil, orderPVE1...)
	require.NoError(t, err)
	require.Contains(t, stderr, "verifies the API against its own CA bundle (tls.ca-cert)")
	require.Contains(t, stderr, `pmx context update lab --ca-cert ""`)
}

func TestACMETrustHint_OverridePin(t *testing.T) {
	f := trustFake(t, "pve1")
	_, stderr, err := runTrustCmd(t, f, config.TLSBlock{}, nil,
		append([]string{"--api-fingerprint", trustTestPin}, orderPVE1...)...)
	require.NoError(t, err)
	require.Contains(t, stderr, "The --api-fingerprint value this command used will not verify the new certificate")
	require.NotContains(t, stderr, "context update", "the context itself trusts the system CAs")

	_, stderr, err = runTrustCmd(t, f, config.TLSBlock{},
		map[string]string{"PMX_API_FINGERPRINT": trustTestPin}, orderPVE1...)
	require.NoError(t, err)
	require.Contains(t, stderr, "unset PMX_API_FINGERPRINT before the next command")
}

func TestACMETrustHint_IPHostSwitchesToACMEDomain(t *testing.T) {
	f := trustFake(t, "pve1")
	f.HandleJSON("GET /api2/json/nodes/pve1/config", map[string]any{
		"acmedomain0": "*.example.com,plugin=cf-dns",
		"acmedomain1": "pve1.example.com,plugin=cf-dns",
		"acmedomain2": "alt.example.com,plugin=cf-dns",
	})

	_, stderr, err := runTrustCmdHost(t, f, "127.0.0.1", config.TLSBlock{Fingerprint: trustTestPin}, nil, orderPVE1...)
	require.NoError(t, err)

	require.Contains(t, stderr, "The context's host 127.0.0.1 is an IP address")
	require.Contains(t, stderr, "a pin on it breaks at every renewal")
	require.Contains(t, stderr, `pmx context update lab --host pve1.example.com --fingerprint ""`,
		"the first domain that is not a wildcard becomes the host")
	require.Contains(t, stderr, "pmx context validate lab --connect")
	require.Contains(t, stderr, "Any other domain the certificate names works too: alt.example.com.")
	require.NotContains(t, stderr, "Trusting the system CAs keeps working", "the IP host needs the switch")
}

func TestACMETrustHint_IPHostWithoutKnownDomain(t *testing.T) {
	f := trustFake(t, "pve1")
	f.HandleFunc("GET /api2/json/nodes/pve1/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "Permission check failed (/, Sys.Audit)")
	})

	_, stderr, err := runTrustCmdHost(t, f, "127.0.0.1", config.TLSBlock{Tofu: true}, nil, orderPVE1...)
	require.NoError(t, err)
	require.Contains(t, stderr, "pmx context update lab --host <acme-domain> --tofu=false")
	require.Contains(t, stderr, "Use a domain the certificate was ordered for")
}

func TestACMETrustHint_NameHostNotCovered(t *testing.T) {
	f := trustFake(t, "pve1")
	f.HandleJSON("GET /api2/json/nodes/pve1/config", map[string]any{"acmedomain0": "pve1.example.com"})

	_, stderr, err := runTrustCmd(t, f, config.TLSBlock{Fingerprint: trustTestPin}, nil, orderPVE1...)
	require.NoError(t, err)
	require.Contains(t, stderr, "The context's host localhost is not one of the node's ACME domains (pve1.example.com)")
	require.Contains(t, stderr, `pmx context update lab --host pve1.example.com --fingerprint ""`)
}
