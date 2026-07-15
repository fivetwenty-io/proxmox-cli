package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestNodeCertificateInfo_ListsCertificates asserts that `node certificate
// info` renders the node's certificate chain.
func TestNodeCertificateInfo_ListsCertificates(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/certificates/info", []map[string]any{
		{"filename": "pveproxy-ssl.pem", "subject": "CN=pdm-host", "issuer": "CN=pdm-host", "fingerprint": "AA:BB"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateInfoCmd(), "info", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "pveproxy-ssl.pem")
	require.Contains(t, buf.String(), "AA:BB")
}

// TestNodeCertificateUpload_RefusesWithoutConfirmation asserts the --yes/-y
// gate on `node certificate upload` blocks the request entirely when unset.
func TestNodeCertificateUpload_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateUploadCmd(), "upload", "pdm-host", "--certificates", "PEM-DATA")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to upload a custom certificate on node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeCertificateUpload_SendsWireBodyWithoutEchoingKey asserts that
// `node certificate upload` sends the certificate and key on the wire, and
// that the private key never appears in the command's own output.
func TestNodeCertificateUpload_SendsWireBodyWithoutEchoingKey(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/certificates/custom", &rec, []map[string]any{
		{"filename": "pveproxy-ssl.pem", "subject": "CN=pdm-host"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateUploadCmd(), "upload", "pdm-host", "--yes",
		"--certificates", "CERT-PEM-DATA", "--key", "PRIVATE-KEY-DATA")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "CERT-PEM-DATA", rec.form.Get("certificates"))
	require.Equal(t, "PRIVATE-KEY-DATA", rec.form.Get("key"))
	require.NotContains(t, buf.String(), "PRIVATE-KEY-DATA", "private key must never be echoed in command output")
	require.Contains(t, buf.String(), "pveproxy-ssl.pem")
}

// TestNodeCertificateDeleteCustom_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `node certificate delete-custom` blocks the request
// entirely when unset.
func TestNodeCertificateDeleteCustom_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateDeleteCustomCmd(), "delete-custom", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to remove the custom certificate on node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeCertificateDeleteCustom_SendsRequestWithConfirmation asserts that
// passing --yes issues the delete request.
func TestNodeCertificateDeleteCustom_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/nodes/pdm-host/certificates/custom", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateDeleteCustomCmd(), "delete-custom", "pdm-host", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Custom certificate on node "pdm-host" removed.`)
}

// TestNodeCertificateAcmeOrder_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `node certificate acme order` blocks the request
// entirely when unset.
func TestNodeCertificateAcmeOrder_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateAcmeOrderCmd(), "order", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to order an ACME certificate on node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeCertificateAcmeOrder_BlocksUntilTaskFinishes asserts that `node
// certificate acme order --yes` blocks until the order task completes.
func TestNodeCertificateAcmeOrder_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/certificates/acme/certificate", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateAcmeOrderCmd(), "order", "pdm-host", "--yes")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), `ACME certificate ordered on node "pdm-host".`)
}

// TestNodeCertificateAcmeOrder_Async asserts that `node certificate acme
// order` prints the UPID immediately when --async is set.
func TestNodeCertificateAcmeOrder_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST /api2/json/nodes/pdm-host/certificates/acme/certificate", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateAcmeOrderCmd(), "order", "pdm-host", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "ordered")
}

// TestNodeCertificateAcmeRenew_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `node certificate acme renew` blocks the request
// entirely when unset.
func TestNodeCertificateAcmeRenew_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateAcmeRenewCmd(), "renew", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to renew the ACME certificate on node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeCertificateAcmeRenew_BlocksUntilTaskFinishes asserts that `node
// certificate acme renew --yes --force` blocks until the renew task
// completes.
func TestNodeCertificateAcmeRenew_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/certificates/acme/certificate", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateAcmeRenewCmd(), "renew", "pdm-host", "--yes", "--force")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "1", rec.form.Get("force"))
	require.Contains(t, buf.String(), `ACME certificate renewed on node "pdm-host".`)
}

// TestNodeCertificateAcmeRenew_Async asserts that `node certificate acme
// renew` prints the UPID immediately when --async is set.
func TestNodeCertificateAcmeRenew_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("PUT /api2/json/nodes/pdm-host/certificates/acme/certificate", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCertificateAcmeRenewCmd(), "renew", "pdm-host", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "renewed")
}
