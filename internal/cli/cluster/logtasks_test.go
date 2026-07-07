package cluster

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestClusterLog_Table verifies that `pmx cluster log` queries GET /cluster/log,
// sends the default --max, and renders the expected columns.
func TestClusterLog_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/cluster/log", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{
				"time": 1717000000,
				"node": "pve1",
				"pid":  1234,
				"uid":  0,
				"tag":  "pvedaemon",
				"msg":  "successful auth",
			},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "log"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/log", gotPath)
	require.Contains(t, gotQuery, "max=50")

	out := buf.String()
	require.Contains(t, out, "TIME")
	require.Contains(t, out, "NODE")
	require.Contains(t, out, "PID")
	require.Contains(t, out, "UID")
	require.Contains(t, out, "TAG")
	require.Contains(t, out, "MSG")
	require.Contains(t, out, "pve1")
	require.Contains(t, out, "pvedaemon")
	require.Contains(t, out, "successful auth")
}

// TestClusterLog_StringUID verifies that a log entry whose uid is a JSON string
// (which real PVE emits, e.g. a username) decodes and renders instead of failing
// with a number-into-int64 error. Regression for the live-tested decode bug.
func TestClusterLog_StringUID(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/log", []any{
		map[string]any{
			"time": 1717000000,
			"node": "pve1",
			"pid":  1234,
			"uid":  "root@pam",
			"tag":  "pvedaemon",
			"msg":  "string uid entry",
		},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "log"))

	out := buf.String()
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "string uid entry")
}

// TestClusterLog_MaxFlag verifies the --max flag overrides the default value in
// the query string.
func TestClusterLog_MaxFlag(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/log", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "log", "--max", "10"))
	require.Contains(t, gotQuery, "max=10")
}

// TestClusterLog_JSONRaw verifies the log entries are emitted verbatim as
// structured JSON (Result.Raw fidelity), preserving native field names.
func TestClusterLog_JSONRaw(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/log", []any{
		map[string]any{
			"node": "pve1",
			"tag":  "pvedaemon",
			"msg":  "boot complete",
		},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "log"))

	out := buf.String()
	require.Contains(t, out, "pvedaemon")
	require.Contains(t, out, "boot complete")
	// Raw JSON preserves the native lowercase API field names rather than the
	// synthetic headers/rows table envelope.
	require.Contains(t, out, "\"msg\"")
	require.NotContains(t, out, "\"headers\"")
}

// TestClusterLog_ServerError verifies a server failure surfaces as an error.
func TestClusterLog_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/log", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "log"))
}

// TestClusterTasks_Table verifies `pmx cluster tasks` queries GET /cluster/tasks
// and renders the expected columns.
func TestClusterTasks_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/tasks", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"upid":      "UPID:pve1:0000ABCD:00:qmstart:100:root@pam:",
				"node":      "pve1",
				"type":      "qmstart",
				"id":        "100",
				"starttime": 1717000000,
				"status":    "OK",
				"user":      "root@pam",
			},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tasks"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/tasks", gotPath)

	out := buf.String()
	require.Contains(t, out, "UPID")
	require.Contains(t, out, "NODE")
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "ID")
	require.Contains(t, out, "STARTTIME")
	require.Contains(t, out, "STATUS")
	require.Contains(t, out, "USER")
	require.Contains(t, out, "qmstart")
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "1717000000")
}

// TestClusterTasks_JSONRaw verifies tasks are emitted verbatim as JSON.
func TestClusterTasks_JSONRaw(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/tasks", []any{
		map[string]any{
			"upid":   "UPID:pve1:x:qmstart:100:root@pam:",
			"node":   "pve1",
			"status": "OK",
		},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tasks"))

	out := buf.String()
	require.Contains(t, out, "\"upid\"")
	require.NotContains(t, out, "\"headers\"")
}

// TestClusterTasks_ServerError verifies task listing surfaces server failures.
func TestClusterTasks_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/tasks", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "tasks"))
}
