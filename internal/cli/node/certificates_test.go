package node_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---- list ------------------------------------------------------------------

func TestNodeCert_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/certificates/info", &rec, []any{
		map[string]any{"subject": "CN=pve1.lab", "issuer": "CN=PVE", "fingerprint": "AA:BB"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "list"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/certificates/info", rec.path)
	out := buf.String()
	require.Contains(t, out, "CN=pve1.lab")
	require.Contains(t, out, "FINGERPRINT")
}

// ---- acme list -------------------------------------------------------------

// TestNodeCertAcme_List verifies `cert acme list` reads the certificates info
// endpoint (GET /nodes/{node}/certificates/acme is only a directory index)
// and keeps only the ACME-managed pveproxy-ssl.pem entry.
func TestNodeCertAcme_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/certificates/info", &rec, []any{
		map[string]any{"filename": "pve-ssl.pem", "subject": "CN=self-signed"},
		map[string]any{"filename": "pveproxy-ssl.pem", "subject": "CN=pve1.lab", "issuer": "CN=R3"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "list"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/certificates/info", rec.path)
	out := buf.String()
	require.Contains(t, out, "CN=pve1.lab")
	require.NotContains(t, out, "CN=self-signed", "only the ACME-managed certificate must be shown")
}

// TestNodeCertAcme_ListNoneInstalled verifies the friendly message when no
// pveproxy-ssl.pem certificate exists.
func TestNodeCertAcme_ListNoneInstalled(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/certificates/info", &rec, []any{
		map[string]any{"filename": "pve-ssl.pem", "subject": "CN=self-signed"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "list"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "No ACME or custom certificate")
}

// TestNodeCertAcme_ListNoneInstalledJSON verifies -o json emits an empty list
// (not a message object) when no pveproxy-ssl.pem certificate exists, so the
// output shape matches the found case.
func TestNodeCertAcme_ListNoneInstalledJSON(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/certificates/info", &rec, []any{
		map[string]any{"filename": "pve-ssl.pem", "subject": "CN=self-signed"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "list"))

	require.NoError(t, root.Execute())
	// The buffer starts with the --insecure TLS warning; the JSON follows.
	out := buf.String()
	i := strings.Index(out, "[")
	require.NotEqual(t, -1, i, "expected a JSON array in output, got: %s", out)
	require.JSONEq(t, "[]", out[i:])
}

// ---- acme order ------------------------------------------------------------

func TestNodeCertAcme_OrderRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/certificates/acme/certificate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "order"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no API call must be made without confirmation")
}

func TestNodeCertAcme_OrderBlocksUntilDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:acmeordercert::root@pam:"
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/certificates/acme/certificate", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "order", "--force", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "force=1")
	require.Contains(t, buf.String(), "ordered")
}

func TestNodeCertAcme_OrderAsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:acmeordercert::root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/certificates/acme/certificate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "cert", "acme", "order", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

// ---- acme renew ------------------------------------------------------------

func TestNodeCertAcme_RenewWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	// nil data exercises the non-UPID fallback to a plain success message.
	recordOn(f, "PUT /api2/json/nodes/pve1/certificates/acme/certificate", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "renew", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	// --force was not passed, so it must be omitted from the request body.
	require.NotContains(t, rec.body, "force")
	require.Contains(t, buf.String(), "renewed")
}

func TestNodeCertAcme_RenewRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/certificates/acme/certificate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "renew"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// ---- acme delete -----------------------------------------------------------

func TestNodeCertAcme_DeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/certificates/acme/certificate", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "delete", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "removed")
}

func TestNodeCertAcme_DeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/certificates/acme/certificate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "delete"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// ---- custom upload ---------------------------------------------------------

func TestNodeCertCustom_UploadRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/certificates/custom", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "custom", "upload",
		"--certificates", "PEMCERTDATA"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCertCustom_UploadRequiresCertificates(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "custom", "upload", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificates")
}

func TestNodeCertCustom_UploadWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	const secretKey = "SUPERSECRETKEYDATA"
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/certificates/custom", &rec, map[string]any{
		"subject": "CN=pve1.lab", "fingerprint": "AA:BB:CC", "filename": "pveproxy-ssl.pem",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "custom", "upload",
		"--certificates", "PEMCERTDATA", "--key", secretKey, "--restart", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "certificates=PEMCERTDATA")
	// The private key is forwarded to the API...
	require.Contains(t, rec.body, "key="+secretKey)
	require.Contains(t, rec.body, "restart=1")
	// ...but must never be echoed back to the user.
	require.NotContains(t, buf.String(), secretKey)
	require.Contains(t, buf.String(), "AA:BB:CC")
}

func TestNodeCertCustom_UploadOmitsUnsetKey(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/certificates/custom", &rec, map[string]any{
		"subject": "CN=pve1.lab",
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "custom", "upload",
		"--certificates", "PEMCERTDATA", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "certificates=PEMCERTDATA")
	// Unset optional key/force/restart must be omitted from the request body.
	require.NotContains(t, rec.body, "key=")
	require.NotContains(t, rec.body, "force")
	require.NotContains(t, rec.body, "restart")
}

// ---- custom delete ---------------------------------------------------------

func TestNodeCertCustom_DeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/certificates/custom", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "custom", "delete", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	// --restart was not passed, so it must be omitted from the request.
	require.NotContains(t, rec.body, "restart")
	require.Contains(t, buf.String(), "removed")
}

func TestNodeCertCustom_DeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/certificates/custom", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "custom", "delete"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// ---- node scoping + command tree -------------------------------------------

func TestNodeCert_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "cert", "list"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCert_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	addNodeGroup(root)

	find := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	nodeCmd := find(root, "node")
	require.NotNil(t, nodeCmd)
	cert := find(nodeCmd, "cert")
	require.NotNil(t, cert, "node cert command must be registered")

	for _, verb := range []string{"list", "acme", "custom"} {
		require.NotNil(t, find(cert, verb), "cert must expose %q", verb)
	}
	acme := find(cert, "acme")
	for _, verb := range []string{"list", "order", "renew", "delete"} {
		require.NotNil(t, find(acme, verb), "cert acme must expose %q", verb)
	}
	custom := find(cert, "custom")
	for _, verb := range []string{"upload", "delete"} {
		require.NotNil(t, find(custom, verb), "cert custom must expose %q", verb)
	}
}
