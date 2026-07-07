package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// api get
// ---------------------------------------------------------------------------

func TestAPIRawGet_ObjectResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/admin/datastore/store1/gc", &rec, map[string]any{
		"store": "store1", "disk-bytes": 12345,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/admin/datastore/store1/gc")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/admin/datastore/store1/gc", rec.path)
	out := buf.String()
	require.Contains(t, out, "store1")
	require.Contains(t, out, "12345")
}

func TestAPIRawGet_NormalizesPathWithoutLeadingSlash(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/admin/datastore", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "admin/datastore")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/admin/datastore", rec.path)
}

func TestAPIRawGet_ArrayOfObjectsRendersDynamicTable(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/admin/datastore", &recordedRequest{}, []map[string]any{
		{"store": "s1", "content": "backup"},
		{"store": "s2", "content": "backup", "extra": "field"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/admin/datastore")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "STORE")
	require.Contains(t, out, "EXTRA")
	require.Contains(t, out, "s1")
	require.Contains(t, out, "s2")
	require.Contains(t, out, "field")
}

func TestAPIRawGet_ArrayOfScalarsRendersValueColumn(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/some/list", &recordedRequest{}, []string{"a", "b", "c"})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/some/list")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "VALUE")
	require.Contains(t, out, "a")
	require.Contains(t, out, "b")
	require.Contains(t, out, "c")
}

func TestAPIRawGet_EmptyArray(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/empty", &recordedRequest{}, []string{})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/empty")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "empty array")
}

func TestAPIRawGet_ScalarResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/scalar", &recordedRequest{}, "hello world")

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/scalar")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "hello world")
}

func TestAPIRawGet_NullResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/nulldata", &recordedRequest{}, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/nulldata")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "no data returned")
}

func TestAPIRawGet_DataFlagsSentAsQueryParams(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/status/datastore-usage", &rec, map[string]any{"store": "s1"})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/status/datastore-usage",
		"--data", "cf=AVERAGE", "-d", "timeframe=hour")
	require.NoError(t, err)
	require.Equal(t, "AVERAGE", rec.query.Get("cf"))
	require.Equal(t, "hour", rec.query.Get("timeframe"))
}

func TestAPIRawGet_MalformedDataRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/x", "--data", "no-equals-sign")
	require.Error(t, err)
	require.ErrorContains(t, err, "want KEY=VALUE")
}

func TestAPIRawGet_DuplicateDataKeyRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/x", "--data", "a=1", "--data", "a=2")
	require.Error(t, err)
	require.ErrorContains(t, err, "more than once")
}

func TestAPIRawGet_EmptyPathRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "   ")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAPIRawGet_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/admin/datastore", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawGetCmd(), "get", "/admin/datastore")
	require.Error(t, err)
	require.ErrorContains(t, err, "permission denied")
	require.ErrorContains(t, err, "GET /admin/datastore")
}

// ---------------------------------------------------------------------------
// api post
// ---------------------------------------------------------------------------

func TestAPIRawPost_SendsFormBodyAndRendersResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/admin/datastore/store1/gc", &rec, "UPID:pbs1:...:garbage_collection:store1:root@pam:")

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawPostCmd(), "post", "/admin/datastore/store1/gc",
		"--data", "worker-id=manual")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "manual", rec.form.Get("worker-id"))
	require.Contains(t, buf.String(), "UPID")
}

func TestAPIRawPost_NoDataSendsNoBody(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/some/action", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawPostCmd(), "post", "/some/action")
	require.NoError(t, err)
	require.Empty(t, rec.form)
}

func TestAPIRawPost_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("POST /api2/json/some/action", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid parameter")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawPostCmd(), "post", "/some/action")
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid parameter")
	require.ErrorContains(t, err, "POST /some/action")
}

// ---------------------------------------------------------------------------
// api put
// ---------------------------------------------------------------------------

func TestAPIRawPut_SendsFormBodyAndRendersResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/traffic-control/rule1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawPutCmd(), "put", "/config/traffic-control/rule1",
		"--data", "comment=updated via raw api")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "updated via raw api", rec.form.Get("comment"))
	require.Contains(t, buf.String(), "no data returned")
}

func TestAPIRawPut_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT /api2/json/some/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusPreconditionFailed, "digest mismatch")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawPutCmd(), "put", "/some/config")
	require.Error(t, err)
	require.ErrorContains(t, err, "digest mismatch")
	require.ErrorContains(t, err, "PUT /some/config")
}

// ---------------------------------------------------------------------------
// api delete
// ---------------------------------------------------------------------------

func TestAPIRawDelete_SendsQueryParamsAndRendersResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/traffic-control/rule1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawDeleteCmd(), "delete", "/config/traffic-control/rule1",
		"--data", "digest=abc123")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "no data returned")
}

func TestAPIRawDelete_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE /api2/json/some/resource", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAPIRawDeleteCmd(), "delete", "/some/resource")
	require.Error(t, err)
	require.ErrorContains(t, err, "not found")
	require.ErrorContains(t, err, "DELETE /some/resource")
}

// ---------------------------------------------------------------------------
// path normalization + params helper unit tests
// ---------------------------------------------------------------------------

func TestApiRawNormalizePath(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "/admin/datastore", want: "/admin/datastore"},
		{in: "admin/datastore", want: "/admin/datastore"},
		{in: "  /admin/datastore  ", want: "/admin/datastore"},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
	}
	for _, c := range cases {
		got, err := apiRawNormalizePath(c.in)
		if c.wantErr {
			require.Error(t, err, "input %q", c.in)
			continue
		}
		require.NoError(t, err, "input %q", c.in)
		require.Equal(t, c.want, got, "input %q", c.in)
	}
}

func TestApiRawParamsFromData_EmptyReturnsNilMap(t *testing.T) {
	params, err := apiRawParamsFromData(nil)
	require.NoError(t, err)
	require.Nil(t, params)
}

func TestApiRawParamsFromData_BuildsMap(t *testing.T) {
	params, err := apiRawParamsFromData([]string{"a=1", "b=two"})
	require.NoError(t, err)
	require.Equal(t, map[string]interface{}{"a": "1", "b": "two"}, params)
}

// ---------------------------------------------------------------------------
// group registration
// ---------------------------------------------------------------------------

func TestNewAPIRawCmd_RegistersAllVerbs(t *testing.T) {
	cmd := newAPIRawCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"get", "post", "put", "delete"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}
}
