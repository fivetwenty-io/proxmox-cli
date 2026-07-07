package pbs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// metrics influxdb-http ls
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbHTTPLs_ListsServersSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http", &rec, []map[string]any{
		{"name": "zzz", "url": "https://z.example.com:8086"},
		{"name": "aaa", "url": "https://a.example.com:8086", "bucket": "proxmox", "comment": "primary"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPLsCmd(), "ls")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/metrics/influxdb-http", rec.path)

	out := buf.String()
	aIdx := strings.Index(out, "aaa")
	zIdx := strings.Index(out, "zzz")
	require.True(t, aIdx >= 0 && zIdx >= 0)
	require.Less(t, aIdx, zIdx, "entries must be sorted by name")
	require.Contains(t, out, "primary")
}

func TestMetricsInfluxdbHTTPLs_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPLsCmd(), "ls")
	require.NoError(t, err)
}

// TestMetricsInfluxdbHTTPLs_TokenStrippedFromRaw verifies the API token is
// stripped from the Raw payload backing -o json/yaml, not just omitted from
// the table columns.
func TestMetricsInfluxdbHTTPLs_TokenStrippedFromRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http", &recordedRequest{}, []map[string]any{
		{"name": "srv1", "url": "https://influx.example.com:8086", "token": "secret-token-value"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPLsCmd(), "ls")
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "secret-token-value")
}

func TestMetricsInfluxdbHTTPLs_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/metrics/influxdb-http", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPLsCmd(), "ls")
	require.Error(t, err)
	require.ErrorContains(t, err, "permission denied")
}

// ---------------------------------------------------------------------------
// metrics influxdb-http show
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbHTTPShow_RendersServer(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http/srv1", &rec, map[string]any{
		"name": "srv1", "url": "https://influx.example.com:8086", "bucket": "proxmox", "verify-tls": true,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPShowCmd(), "show", "srv1")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/config/metrics/influxdb-http/srv1", rec.path)
	require.Contains(t, buf.String(), "influx.example.com")
}

// TestMetricsInfluxdbHTTPShow_TokenNotEchoed verifies the write-only API
// token the GET response returns is stripped from show output in every
// format, not just omitted from a subset of fields.
func TestMetricsInfluxdbHTTPShow_TokenNotEchoed(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http/srv1", &recordedRequest{}, map[string]any{
		"name": "srv1", "url": "https://influx.example.com:8086", "token": "secret-token-value",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPShowCmd(), "show", "srv1")
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "secret-token-value")
}

func TestMetricsInfluxdbHTTPShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http/srv1", &recordedRequest{}, map[string]any{
		"name": "srv1", "url": "https://influx.example.com:8086",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPShowCmd(), "show", "srv1", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "influx.example.com")
	require.Contains(t, out, "proxmox (default)", "bucket defaults to proxmox")
}

// TestMetricsInfluxdbHTTPShow_DefaultsJSON verifies the JSON set/defaults
// shape and that the write-only token is never resurrected as an "unset"
// default: it is excluded from the schema table entirely, so --defaults
// cannot reintroduce it.
func TestMetricsInfluxdbHTTPShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http/srv1", &recordedRequest{}, map[string]any{
		"name": "srv1", "url": "https://influx.example.com:8086", "token": "secret-token-value",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPShowCmd(), "show", "srv1", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)
	require.NotContains(t, buf.String(), "secret-token-value")

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "proxmox", got.Defaults["bucket"])
	require.NotContains(t, got.Defaults, "token", "token must not appear even as an unset default")
}

func TestMetricsInfluxdbHTTPShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPShowCmd(), "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbHTTPShow_NotFound(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/metrics/influxdb-http/ghost", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such server 'ghost'")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPShowCmd(), "show", "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such server")
}

// ---------------------------------------------------------------------------
// metrics influxdb-http add
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbHTTPAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/metrics/influxdb-http", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPAddCmd(), "add", "srv1",
		"--url", "https://influx.example.com:8086",
		"--bucket", "mybucket",
		"--organization", "myorg",
		"--token", "s3cr3t",
		"--max-body-size", "1000000",
		"--verify-tls",
		"--enable",
		"--comment", "primary influx",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/config/metrics/influxdb-http", rec.path)

	want := map[string]string{
		"name":          "srv1",
		"url":           "https://influx.example.com:8086",
		"bucket":        "mybucket",
		"organization":  "myorg",
		"token":         "s3cr3t",
		"max-body-size": "1000000",
		"verify-tls":    "1",
		"enable":        "1",
		"comment":       "primary influx",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Contains(t, buf.String(), "srv1")
}

func TestMetricsInfluxdbHTTPAdd_OmitsUnsetOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/metrics/influxdb-http", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPAddCmd(), "add", "srv1", "--url", "https://influx.example.com:8086")
	require.NoError(t, err)
	require.Equal(t, "srv1", rec.form.Get("name"))
	require.Equal(t, "https://influx.example.com:8086", rec.form.Get("url"))

	for _, key := range []string{"bucket", "organization", "token", "max-body-size", "verify-tls", "enable", "comment"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestMetricsInfluxdbHTTPAdd_MissingUrlRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPAddCmd(), "add", "srv1")
	require.Error(t, err)
}

func TestMetricsInfluxdbHTTPAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPAddCmd(), "add", "", "--url", "https://influx.example.com:8086")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbHTTPAdd_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("POST /api2/json/config/metrics/influxdb-http", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "server 'srv1' already exists")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPAddCmd(), "add", "srv1", "--url", "https://influx.example.com:8086")
	require.Error(t, err)
	require.ErrorContains(t, err, "already exists")
}

// ---------------------------------------------------------------------------
// metrics influxdb-http update
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbHTTPUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/metrics/influxdb-http/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPUpdateCmd(), "update", "srv1",
		"--url", "https://new.example.com:8086",
		"--bucket", "newbucket",
		"--organization", "neworg",
		"--token", "newtoken",
		"--max-body-size", "2000000",
		"--verify-tls",
		"--enable",
		"--comment", "updated",
		"--digest", "abc123",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)

	want := map[string]string{
		"url":           "https://new.example.com:8086",
		"bucket":        "newbucket",
		"organization":  "neworg",
		"token":         "newtoken",
		"max-body-size": "2000000",
		"verify-tls":    "1",
		"enable":        "1",
		"comment":       "updated",
		"digest":        "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestMetricsInfluxdbHTTPUpdate_SendsOnlyChangedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/metrics/influxdb-http/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPUpdateCmd(), "update", "srv1", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{"url", "bucket", "organization", "token", "max-body-size", "verify-tls", "enable", "digest", "delete"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestMetricsInfluxdbHTTPUpdate_DeleteProperties(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/metrics/influxdb-http/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPUpdateCmd(), "update", "srv1", "--delete", "comment", "--delete", "token")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"comment", "token"}, rec.form["delete"])
}

func TestMetricsInfluxdbHTTPUpdate_EmptyDeleteEntryRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPUpdateCmd(), "update", "srv1", "--delete", "")
	require.Error(t, err)
}

func TestMetricsInfluxdbHTTPUpdate_NoChangesRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPUpdateCmd(), "update", "srv1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes given")
}

func TestMetricsInfluxdbHTTPUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPUpdateCmd(), "update", "", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbHTTPUpdate_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT /api2/json/config/metrics/influxdb-http/srv1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusPreconditionFailed, "digest mismatch")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPUpdateCmd(), "update", "srv1", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "digest mismatch")
}

// ---------------------------------------------------------------------------
// metrics influxdb-http delete
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbHTTPDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/metrics/influxdb-http/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPDeleteCmd(), "delete", "srv1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestMetricsInfluxdbHTTPDelete_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/metrics/influxdb-http/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPDeleteCmd(), "delete", "srv1", "--digest", "deadbeef", "--yes")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "deadbeef", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "srv1")
}

func TestMetricsInfluxdbHTTPDelete_NoDigestOmitsParam(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/metrics/influxdb-http/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPDeleteCmd(), "delete", "srv1", "--yes")
	require.NoError(t, err)
	_, hasDigest := rec.query["digest"]
	require.False(t, hasDigest)
}

func TestMetricsInfluxdbHTTPDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPDeleteCmd(), "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbHTTPDelete_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE /api2/json/config/metrics/influxdb-http/srv1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such server")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbHTTPDeleteCmd(), "delete", "srv1", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such server")
}

// ---------------------------------------------------------------------------
// metrics influxdb-udp ls
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbUDPLs_ListsServersSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/metrics/influxdb-udp", &rec, []map[string]any{
		{"name": "zzz", "host": "z.example.com:8089"},
		{"name": "aaa", "host": "a.example.com:8089", "mtu": 1500, "comment": "primary"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPLsCmd(), "ls")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/metrics/influxdb-udp", rec.path)

	out := buf.String()
	aIdx := strings.Index(out, "aaa")
	zIdx := strings.Index(out, "zzz")
	require.True(t, aIdx >= 0 && zIdx >= 0)
	require.Less(t, aIdx, zIdx, "entries must be sorted by name")
	require.Contains(t, out, "1500")
}

func TestMetricsInfluxdbUDPLs_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-udp", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPLsCmd(), "ls")
	require.NoError(t, err)
}

func TestMetricsInfluxdbUDPLs_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/metrics/influxdb-udp", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPLsCmd(), "ls")
	require.Error(t, err)
	require.ErrorContains(t, err, "permission denied")
}

// ---------------------------------------------------------------------------
// metrics influxdb-udp show
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbUDPShow_RendersServer(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/metrics/influxdb-udp/srv1", &rec, map[string]any{
		"name": "srv1", "host": "udp.example.com:8089",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPShowCmd(), "show", "srv1")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/config/metrics/influxdb-udp/srv1", rec.path)
	require.Contains(t, buf.String(), "udp.example.com")
}

func TestMetricsInfluxdbUDPShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-udp/srv1", &recordedRequest{}, map[string]any{
		"name": "srv1", "host": "udp.example.com:8089",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPShowCmd(), "show", "srv1", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "udp.example.com")
	require.Contains(t, out, "1500 (default)", "mtu defaults to 1500")
}

func TestMetricsInfluxdbUDPShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	recordJSON(f, "GET /api2/json/config/metrics/influxdb-udp/srv1", &recordedRequest{}, map[string]any{
		"name": "srv1", "host": "udp.example.com:8089",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPShowCmd(), "show", "srv1", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "udp.example.com:8089", got.Set["host"])
	require.Equal(t, "1500", got.Defaults["mtu"])
}

func TestMetricsInfluxdbUDPShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPShowCmd(), "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbUDPShow_NotFound(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/metrics/influxdb-udp/ghost", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such server 'ghost'")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPShowCmd(), "show", "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such server")
}

// ---------------------------------------------------------------------------
// metrics influxdb-udp add
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbUDPAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/metrics/influxdb-udp", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPAddCmd(), "add", "srv1",
		"--host", "udp.example.com:8089",
		"--mtu", "1500",
		"--enable",
		"--comment", "primary influx udp",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"name":    "srv1",
		"host":    "udp.example.com:8089",
		"mtu":     "1500",
		"enable":  "1",
		"comment": "primary influx udp",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestMetricsInfluxdbUDPAdd_OmitsUnsetOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/metrics/influxdb-udp", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPAddCmd(), "add", "srv1", "--host", "udp.example.com:8089")
	require.NoError(t, err)
	require.Equal(t, "srv1", rec.form.Get("name"))
	require.Equal(t, "udp.example.com:8089", rec.form.Get("host"))

	for _, key := range []string{"mtu", "enable", "comment"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestMetricsInfluxdbUDPAdd_MissingHostRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPAddCmd(), "add", "srv1")
	require.Error(t, err)
}

func TestMetricsInfluxdbUDPAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPAddCmd(), "add", "", "--host", "udp.example.com:8089")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbUDPAdd_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("POST /api2/json/config/metrics/influxdb-udp", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "server 'srv1' already exists")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPAddCmd(), "add", "srv1", "--host", "udp.example.com:8089")
	require.Error(t, err)
	require.ErrorContains(t, err, "already exists")
}

// ---------------------------------------------------------------------------
// metrics influxdb-udp update
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbUDPUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/metrics/influxdb-udp/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPUpdateCmd(), "update", "srv1",
		"--host", "new.example.com:8089",
		"--mtu", "9000",
		"--enable",
		"--comment", "updated",
		"--digest", "abc123",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)

	want := map[string]string{
		"host":    "new.example.com:8089",
		"mtu":     "9000",
		"enable":  "1",
		"comment": "updated",
		"digest":  "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestMetricsInfluxdbUDPUpdate_SendsOnlyChangedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/metrics/influxdb-udp/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPUpdateCmd(), "update", "srv1", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{"host", "mtu", "enable", "digest", "delete"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestMetricsInfluxdbUDPUpdate_DeleteProperties(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/metrics/influxdb-udp/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPUpdateCmd(), "update", "srv1", "--delete", "comment", "--delete", "mtu")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"comment", "mtu"}, rec.form["delete"])
}

func TestMetricsInfluxdbUDPUpdate_EmptyDeleteEntryRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPUpdateCmd(), "update", "srv1", "--delete", "")
	require.Error(t, err)
}

func TestMetricsInfluxdbUDPUpdate_NoChangesRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPUpdateCmd(), "update", "srv1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes given")
}

func TestMetricsInfluxdbUDPUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPUpdateCmd(), "update", "", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbUDPUpdate_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT /api2/json/config/metrics/influxdb-udp/srv1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusPreconditionFailed, "digest mismatch")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPUpdateCmd(), "update", "srv1", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "digest mismatch")
}

// ---------------------------------------------------------------------------
// metrics influxdb-udp delete
// ---------------------------------------------------------------------------

func TestMetricsInfluxdbUDPDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/metrics/influxdb-udp/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPDeleteCmd(), "delete", "srv1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestMetricsInfluxdbUDPDelete_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/metrics/influxdb-udp/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPDeleteCmd(), "delete", "srv1", "--digest", "deadbeef", "--yes")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "deadbeef", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "srv1")
}

func TestMetricsInfluxdbUDPDelete_NoDigestOmitsParam(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/metrics/influxdb-udp/srv1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPDeleteCmd(), "delete", "srv1", "--yes")
	require.NoError(t, err)
	_, hasDigest := rec.query["digest"]
	require.False(t, hasDigest)
}

func TestMetricsInfluxdbUDPDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPDeleteCmd(), "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestMetricsInfluxdbUDPDelete_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE /api2/json/config/metrics/influxdb-udp/srv1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such server")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsInfluxdbUDPDeleteCmd(), "delete", "srv1", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such server")
}

// ---------------------------------------------------------------------------
// metrics data
// ---------------------------------------------------------------------------

func TestMetricsData_RendersBareArrayResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/status/metrics", &rec, []map[string]any{
		{"id": "node/localhost", "metric": "cpu", "timestamp": 1700000000, "type": "gauge", "value": 0.5},
		{"id": "datastore/store1", "metric": "used", "timestamp": 1700000001, "type": "gauge", "value": 12345},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/status/metrics", rec.path)

	out := buf.String()
	require.Contains(t, out, "node/localhost")
	require.Contains(t, out, "datastore/store1")
	require.Contains(t, out, "12345")
}

func TestMetricsData_RendersWrappedDataObjectResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/status/metrics", &rec, map[string]any{
		"data": []map[string]any{
			{"id": "node/localhost", "metric": "cpu", "timestamp": 1700000000, "type": "gauge", "value": 0.25},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "node/localhost")
	require.Contains(t, buf.String(), "0.25")
}

func TestMetricsData_SortsByIdThenMetricThenTimestamp(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/status/metrics", &recordedRequest{}, []map[string]any{
		{"id": "node/z", "metric": "cpu", "timestamp": 1, "type": "gauge", "value": 1},
		{"id": "node/a", "metric": "cpu", "timestamp": 1, "type": "gauge", "value": 2},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data")
	require.NoError(t, err)

	out := buf.String()
	aIdx := strings.Index(out, "node/a")
	zIdx := strings.Index(out, "node/z")
	require.True(t, aIdx >= 0 && zIdx >= 0)
	require.Less(t, aIdx, zIdx)
}

func TestMetricsData_SendsHistoryAndStartTimeFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/status/metrics", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data", "--history", "--start-time", "1700000000")
	require.NoError(t, err)
	require.Equal(t, "1", rec.query.Get("history"))
	require.Equal(t, "1700000000", rec.query.Get("start-time"))
}

func TestMetricsData_NoFlagsOmitsQueryParams(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/status/metrics", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data")
	require.NoError(t, err)
	_, hasHistory := rec.query["history"]
	require.False(t, hasHistory)
	_, hasStartTime := rec.query["start-time"]
	require.False(t, hasStartTime)
}

func TestMetricsData_EmptyResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/status/metrics", &recordedRequest{}, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data")
	require.NoError(t, err)
}

func TestMetricsData_MalformedElementSurfacesDecodeError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/status/metrics", &recordedRequest{}, []map[string]any{
		{"id": "node/localhost", "metric": "cpu", "timestamp": "not-a-number", "type": "gauge", "value": 1},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data")
	require.Error(t, err)
	require.ErrorContains(t, err, "decode metric data point")
}

func TestMetricsData_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/status/metrics", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newMetricsDataCmd(), "data")
	require.Error(t, err)
	require.ErrorContains(t, err, "boom")
}

// ---------------------------------------------------------------------------
// group registration
// ---------------------------------------------------------------------------

func TestNewMetricsCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newMetricsCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"influxdb-http", "influxdb-udp", "data"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}

	httpCmd := findSubcommand(t, cmd, "influxdb-http")
	httpNames := map[string]bool{}
	for _, c := range httpCmd.Commands() {
		httpNames[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, httpNames[want], "missing influxdb-http verb %q", want)
	}

	udpCmd := findSubcommand(t, cmd, "influxdb-udp")
	udpNames := map[string]bool{}
	for _, c := range udpCmd.Commands() {
		udpNames[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, udpNames[want], "missing influxdb-udp verb %q", want)
	}
}
