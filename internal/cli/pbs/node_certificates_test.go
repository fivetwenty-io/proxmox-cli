package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestNodeCertificatesLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/certificates", &rec, []map[string]any{
		{"subdir": "acme"}, {"subdir": "custom"}, {"subdir": "info"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "acme")
	require.Contains(t, buf.String(), "custom")
}

func TestNodeCertificatesLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/certificates", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list certificates on node")
}

func TestNodeCertificatesInfo_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/certificates/info", &rec, []map[string]any{
		{"filename": "pveproxy-ssl.pem", "subject": "CN=pbs1", "issuer": "CN=Let's Encrypt",
			"notbefore": 1000, "notafter": 2000, "fingerprint": "AA:BB"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "info")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "pveproxy-ssl.pem")
	require.Contains(t, buf.String(), "AA:BB")
}

func TestNodeCertificatesAcmeOrder_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "acme", "order")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

func TestNodeCertificatesAcmeOrder_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/certificates/acme/certificate", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "acme", "order", "--yes", "--force")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "1", rec.form.Get("force"))
	require.Contains(t, buf.String(), "ordered")
}

func TestNodeCertificatesAcmeRenew_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/certificates/acme/certificate", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "acme", "renew", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Contains(t, buf.String(), validUPID)
}

func TestNodeCertificatesAcmeRenew_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "acme", "renew")
	require.Error(t, err)
}

func TestNodeCertificatesCustomUpload_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "custom", "upload", "--certificates", "PEMDATA")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

func TestNodeCertificatesCustomUpload_RequiresCertificates(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "custom", "upload", "--yes")
	require.Error(t, err)
}

func TestNodeCertificatesCustomUpload_SendsFieldsAndRendersResult(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/certificates/custom", &rec, []map[string]any{
		{"filename": "pveproxy-ssl.pem", "subject": "CN=pbs1"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "custom", "upload", "--yes",
		"--certificates", "PEMDATA", "--key", "KEYDATA", "--force", "--restart")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "PEMDATA", rec.form.Get("certificates"))
	require.Equal(t, "KEYDATA", rec.form.Get("key"))
	require.Equal(t, "1", rec.form.Get("force"))
	require.Equal(t, "1", rec.form.Get("restart"))
	require.Contains(t, buf.String(), "pveproxy-ssl.pem")
}

func TestNodeCertificatesCustomDelete_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "custom", "delete")
	require.Error(t, err)
}

func TestNodeCertificatesCustomDelete_Deletes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+nodeAPIBase+"/certificates/custom", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "certificates", "custom", "delete", "--yes", "--restart")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "1", rec.query.Get("restart"))
	require.Contains(t, buf.String(), "removed")
}
