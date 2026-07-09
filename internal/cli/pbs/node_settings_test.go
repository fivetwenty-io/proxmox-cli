package pbs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// --- dns ----------------------------------------------------------------

func TestNodeDNSShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/dns", &rec, map[string]any{
		"digest": "abc123", "dns1": "1.1.1.1", "search": "example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "dns", "show")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "1.1.1.1")
}

func TestNodeDNSUpdate_RequiresAFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "dns", "update")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestNodeDNSUpdate_SendsAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/dns", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "dns", "update",
		"--dns1", "1.1.1.1", "--dns2", "8.8.8.8", "--dns3", "9.9.9.9",
		"--search", "example.com", "--delete", "dns3", "--digest", "digestval")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "1.1.1.1", rec.form.Get("dns1"))
	require.Equal(t, "8.8.8.8", rec.form.Get("dns2"))
	require.Equal(t, "9.9.9.9", rec.form.Get("dns3"))
	require.Equal(t, "example.com", rec.form.Get("search"))
	require.Equal(t, []string{"dns3"}, rec.form["delete"])
	require.Equal(t, "digestval", rec.form.Get("digest"))
}

func TestNodeDNSUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+nodeAPIBase+"/dns", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "dns", "update", "--dns1", "1.1.1.1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update dns")
}

// --- time -----------------------------------------------------------------

func TestNodeTimeShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/time", &rec, map[string]any{
		"localtime": 1000, "time": 1000, "timezone": "UTC",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "time", "show")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "UTC")
}

func TestNodeTimeUpdate_RequiresTimezone(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "time", "update")
	require.Error(t, err)
}

func TestNodeTimeUpdate_SendsTimezone(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/time", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "time", "update", "--timezone", "America/New_York")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "America/New_York", rec.form.Get("timezone"))
}

// --- config -----------------------------------------------------------------

func TestNodeConfigShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/config", &rec, map[string]any{
		"description": "my node", "email-from": "root@example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "config", "show")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "my node")
}

// TestNodeConfigShow_DefaultsTable verifies --defaults lists an unset option
// (the PBS node config schema declares no built-in defaults, so unset
// options render "(unset)" rather than "(default)").
func TestNodeConfigShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+nodeAPIBase+"/config", &recordedRequest{}, map[string]any{
		"description": "my node",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "config", "show", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "my node")
	require.Contains(t, out, "(unset)", "location has no schema default")
}

func TestNodeConfigShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+nodeAPIBase+"/config", &recordedRequest{}, map[string]any{
		"description": "my node",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "config", "show", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "my node", got.Set["description"])
	require.Empty(t, got.Defaults, "node config schema declares no built-in defaults")
}

func TestNodeConfigUpdate_RequiresAFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "config", "update")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestNodeConfigUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/config", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "config", "update",
		"--acme", "acct1",
		"--acmedomain0", "domain0=example.com",
		"--acmedomain1", "domain1=example.org",
		"--acmedomain2", "domain2=example.net",
		"--acmedomain3", "domain3=example.io",
		"--acmedomain4", "domain4=example.dev",
		"--ciphers-tls-1.2", "HIGH",
		"--ciphers-tls-1.3", "TLS_AES_256_GCM_SHA384",
		"--consent-text", "consent",
		"--default-lang", "en",
		"--description", "updated desc",
		"--email-from", "root@example.com",
		"--http-proxy", "http://proxy:8080",
		"--location", "dc1",
		"--task-log-max-days", "30",
		"--delete", "location",
		"--digest", "digestval",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)

	want := map[string]string{
		"acme":              "acct1",
		"acmedomain0":       "domain0=example.com",
		"acmedomain1":       "domain1=example.org",
		"acmedomain2":       "domain2=example.net",
		"acmedomain3":       "domain3=example.io",
		"acmedomain4":       "domain4=example.dev",
		"ciphers-tls-1.2":   "HIGH",
		"ciphers-tls-1.3":   "TLS_AES_256_GCM_SHA384",
		"consent-text":      "consent",
		"default-lang":      "en",
		"description":       "updated desc",
		"email-from":        "root@example.com",
		"http-proxy":        "http://proxy:8080",
		"location":          "dc1",
		"task-log-max-days": "30",
		"digest":            "digestval",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"location"}, rec.form["delete"])
}

func TestNodeConfigUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/config", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "config", "update", "--description", "only desc")
	require.NoError(t, err)
	require.Equal(t, "only desc", rec.form.Get("description"))

	for _, key := range []string{
		"acme", "acmedomain0", "acmedomain1", "acmedomain2", "acmedomain3", "acmedomain4",
		"ciphers-tls-1.2", "ciphers-tls-1.3", "consent-text", "default-lang", "email-from",
		"http-proxy", "location", "task-log-max-days", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

// --- subscription -------------------------------------------------------

func TestNodeSubscriptionShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/subscription", &rec, map[string]any{"status": "notfound"})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "subscription", "show")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "notfound")
}

func TestNodeSubscriptionSet_RequiresKey(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "subscription", "set")
	require.Error(t, err)
}

func TestNodeSubscriptionSet_SendsKey(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/subscription", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "subscription", "set", "--key", "some-key")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "some-key", rec.form.Get("key"))
}

func TestNodeSubscriptionUpdate_SendsForce(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/subscription", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "subscription", "update", "--force")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "1", rec.form.Get("force"))
}

func TestNodeSubscriptionDelete_Deletes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+nodeAPIBase+"/subscription", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "subscription", "delete", "--yes")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
}

func TestNodeSubscriptionDelete_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "subscription", "delete")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

// --- identity -------------------------------------------------------------

func TestNodeIdentity_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/identity", &rec, map[string]any{"pbs-instance-id": "abcd1234"})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "identity")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "abcd1234")
}

func TestNodeIdentity_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/identity", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "identity")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get identity for node")
}
