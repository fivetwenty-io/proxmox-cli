package api

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// shape + annotation
// ---------------------------------------------------------------------------

// TestAPIGroup_RawShapeAndAnnotation verifies that the `api` group is
// annotated product:context (so the root resolves the active client from the
// active context rather than defaulting to PVE) and carries the four raw
// request verbs as sub-commands.
func TestAPIGroup_RawShapeAndAnnotation(t *testing.T) {
	cmd := Group(&cli.Deps{})
	require.Equal(t, "api", cmd.Name())
	require.Equal(t, cli.ProductFromContext, cmd.Annotations[cli.ProductAnnotation])
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"get", "post", "put", "delete"} {
		require.True(t, names[want], "missing %s", want)
	}
}

// ---------------------------------------------------------------------------
// test harness
// ---------------------------------------------------------------------------

// run mounts sub on a bare "api" parent command, injects deps via context,
// captures output in buf, and executes it with the supplied args.
func rawRun(deps *cli.Deps, buf *bytes.Buffer, sub *cobra.Command, args ...string) error {
	cmd := &cobra.Command{Use: "api"}
	cmd.AddCommand(sub)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	return cmd.Execute()
}

// newFakePBSClient returns a FakePBS test server and a PBSClient pointed at
// it, along with a Deps carrying only PBS (deps.API left nil), the shape the
// active-context resolution produces for a PBS context.
func newFakePBSClient(t *testing.T) (*testhelper.FakePBS, *apiclient.PBSClient) {
	t.Helper()

	f := testhelper.NewFakePBS(t)
	pc, err := apiclient.NewPBSClient(f.Options)
	require.NoError(t, err)

	return f, pc
}

// pbsDepsFor builds a Deps with the given PBS client, format, and async flag.
func pbsDepsFor(t *testing.T, pc *apiclient.PBSClient, format output.Format) *cli.Deps {
	t.Helper()

	return &cli.Deps{PBS: pc, Out: output.New(), Format: format}
}

// newFakePVEClient returns a FakePVE test server and an APIClient pointed at
// it, the shape the active-context resolution produces for a PVE context.
func newFakePVEClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()

	f := testhelper.NewFakePVE(t)
	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTP,
		APIToken: "root@pam!test=00000000-0000-0000-0000-000000000000",
		SSLOptions: &pve.SSLOptions{
			VerifyMode: pve.SSLVerifyNone,
		},
	}
	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)

	return f, ac
}

// pveDepsFor builds a Deps with the given PVE client and format (deps.PBS
// left nil, so rawClient falls through to deps.API).
func pveDepsFor(pc *apiclient.APIClient, format output.Format) *cli.Deps {
	return &cli.Deps{API: pc, Out: output.New(), Format: format}
}

// ---------------------------------------------------------------------------
// api get (PBS client selected)
// ---------------------------------------------------------------------------

func TestAPIRawGet_PBS_ObjectResponse(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/admin/datastore/store1/gc", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{"store": "store1", "disk-bytes": 12345})
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/admin/datastore/store1/gc")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/admin/datastore/store1/gc", gotPath)
	out := buf.String()
	require.Contains(t, out, "store1")
	require.Contains(t, out, "12345")
}

func TestAPIRawGet_PBS_NormalizesPathWithoutLeadingSlash(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var gotPath string
	f.HandleFunc("GET /api2/json/admin/datastore", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []map[string]any{})
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "admin/datastore")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/admin/datastore", gotPath)
}

func TestAPIRawGet_PBS_ArrayOfObjectsRendersDynamicTable(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("GET /api2/json/admin/datastore", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []map[string]any{
			{"store": "s1", "content": "backup"},
			{"store": "s2", "content": "backup", "extra": "field"},
		})
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/admin/datastore")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "STORE")
	require.Contains(t, out, "EXTRA")
	require.Contains(t, out, "s1")
	require.Contains(t, out, "s2")
	require.Contains(t, out, "field")
}

func TestAPIRawGet_PBS_ArrayOfScalarsRendersValueColumn(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("GET /api2/json/some/list", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []string{"a", "b", "c"})
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/some/list")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "VALUE")
	require.Contains(t, out, "a")
	require.Contains(t, out, "b")
	require.Contains(t, out, "c")
}

func TestAPIRawGet_PBS_EmptyArray(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("GET /api2/json/empty", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []string{})
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/empty")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "empty array")
}

func TestAPIRawGet_PBS_ScalarResponse(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("GET /api2/json/scalar", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, "hello world")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/scalar")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "hello world")
}

func TestAPIRawGet_PBS_NullResponse(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("GET /api2/json/nulldata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/nulldata")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "no data returned")
}

func TestAPIRawGet_PBS_DataFlagsSentAsQueryParams(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var gotQuery string
	f.HandleFunc("GET /api2/json/status/datastore-usage", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"store": "s1"})
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/status/datastore-usage",
		"--data", "cf=AVERAGE", "-d", "timeframe=hour")
	require.NoError(t, err)
	require.Contains(t, gotQuery, "cf=AVERAGE")
	require.Contains(t, gotQuery, "timeframe=hour")
}

func TestAPIRawGet_PBS_MalformedDataRejected(t *testing.T) {
	_, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/x", "--data", "no-equals-sign")
	require.Error(t, err)
	require.ErrorContains(t, err, "want KEY=VALUE")
}

func TestAPIRawGet_PBS_DuplicateDataKeyRejected(t *testing.T) {
	_, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/x", "--data", "a=1", "--data", "a=2")
	require.Error(t, err)
	require.ErrorContains(t, err, "more than once")
}

func TestAPIRawGet_PBS_EmptyPathRejected(t *testing.T) {
	_, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "   ")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAPIRawGet_PBS_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("GET /api2/json/admin/datastore", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/admin/datastore")
	require.Error(t, err)
	require.ErrorContains(t, err, "permission denied")
	require.ErrorContains(t, err, "GET /admin/datastore")
}

// ---------------------------------------------------------------------------
// api post / put / delete (PBS client selected)
// ---------------------------------------------------------------------------

func TestAPIRawPost_PBS_SendsFormBodyAndRendersResponse(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var gotMethod string
	var gotForm string
	f.HandleFunc("POST /api2/json/admin/datastore/store1/gc", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.PostFormValue("worker-id")
		testhelper.WriteData(w, "UPID:pbs1:...:garbage_collection:store1:root@pam:")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawPostCmd(), "post", "/admin/datastore/store1/gc",
		"--data", "worker-id=manual")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "manual", gotForm)
	require.Contains(t, buf.String(), "UPID")
}

func TestAPIRawPost_PBS_NoDataSendsNoBody(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var gotForm string
	f.HandleFunc("POST /api2/json/some/action", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm.Encode()
		testhelper.WriteData(w, nil)
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawPostCmd(), "post", "/some/action")
	require.NoError(t, err)
	require.Empty(t, gotForm)
}

func TestAPIRawPost_PBS_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("POST /api2/json/some/action", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid parameter")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawPostCmd(), "post", "/some/action")
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid parameter")
	require.ErrorContains(t, err, "POST /some/action")
}

func TestAPIRawPut_PBS_SendsFormBodyAndRendersResponse(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var gotMethod string
	var gotForm string
	f.HandleFunc("PUT /api2/json/config/traffic-control/rule1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.PostFormValue("comment")
		testhelper.WriteData(w, nil)
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawPutCmd(), "put", "/config/traffic-control/rule1",
		"--data", "comment=updated via raw api")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "updated via raw api", gotForm)
	require.Contains(t, buf.String(), "no data returned")
}

func TestAPIRawPut_PBS_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("PUT /api2/json/some/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusPreconditionFailed, "digest mismatch")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawPutCmd(), "put", "/some/config")
	require.Error(t, err)
	require.ErrorContains(t, err, "digest mismatch")
	require.ErrorContains(t, err, "PUT /some/config")
}

func TestAPIRawDelete_PBS_SendsQueryParamsAndRendersResponse(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	var gotMethod string
	var gotQuery string
	f.HandleFunc("DELETE /api2/json/config/traffic-control/rule1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, nil)
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawDeleteCmd(), "delete", "/config/traffic-control/rule1",
		"--data", "digest=abc123")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, gotQuery, "digest=abc123")
	require.Contains(t, buf.String(), "no data returned")
}

func TestAPIRawDelete_PBS_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakePBSClient(t)
	deps := pbsDepsFor(t, pc, output.FormatTable)

	f.HandleFunc("DELETE /api2/json/some/resource", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawDeleteCmd(), "delete", "/some/resource")
	require.Error(t, err)
	require.ErrorContains(t, err, "not found")
	require.ErrorContains(t, err, "DELETE /some/resource")
}

// ---------------------------------------------------------------------------
// PVE client selected (deps.PBS nil, deps.API set) — proves rawClient falls
// through to deps.API for a PVE (non-PBS) active context.
// ---------------------------------------------------------------------------

func TestAPIRawGet_PVE_ObjectResponse(t *testing.T) {
	f, ac := newFakePVEClient(t)
	deps := pveDepsFor(ac, output.FormatTable)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/status", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{"uptime": 12345})
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/nodes/pve1/status")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/status", gotPath)
	require.Contains(t, buf.String(), "12345")
}

func TestAPIRawPost_PVE_SendsFormBodyAndRendersResponse(t *testing.T) {
	f, ac := newFakePVEClient(t)
	deps := pveDepsFor(ac, output.FormatTable)

	var gotMethod string
	var gotForm string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.PostFormValue("vmid")
		testhelper.WriteData(w, "UPID:pve1:...:qmcreate:100:root@pam:")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawPostCmd(), "post", "/nodes/pve1/qemu", "--data", "vmid=100")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "100", gotForm)
	require.Contains(t, buf.String(), "UPID")
}

func TestAPIRawPut_PVE_ServerErrorSurfaced(t *testing.T) {
	f, ac := newFakePVEClient(t)
	deps := pveDepsFor(ac, output.FormatTable)

	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusPreconditionFailed, "digest mismatch")
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawPutCmd(), "put", "/nodes/pve1/qemu/100/config")
	require.Error(t, err)
	require.ErrorContains(t, err, "digest mismatch")
}

func TestAPIRawDelete_PVE_SendsQueryParamsAndRendersResponse(t *testing.T) {
	f, ac := newFakePVEClient(t)
	deps := pveDepsFor(ac, output.FormatTable)

	var gotMethod string
	var gotQuery string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, nil)
	})

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawDeleteCmd(), "delete", "/nodes/pve1/qemu/100", "--data", "purge=1")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, gotQuery, "purge=1")
	require.Contains(t, buf.String(), "no data returned")
}

// TestAPIRawGet_PVE_PreferredOverPBSWhenBothSet verifies the documented
// precedence: deps.PBS wins when set, even if deps.API is also populated.
func TestAPIRawGet_PVE_PBSPreferredWhenBothSet(t *testing.T) {
	pbsFake, pc := newFakePBSClient(t)
	_, ac := newFakePVEClient(t)

	var pbsHit bool
	pbsFake.HandleFunc("GET /api2/json/ping-marker", func(w http.ResponseWriter, _ *http.Request) {
		pbsHit = true
		testhelper.WriteData(w, "pbs")
	})

	deps := &cli.Deps{PBS: pc, API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := rawRun(deps, &buf, newRawGetCmd(), "get", "/ping-marker")
	require.NoError(t, err)
	require.True(t, pbsHit, "deps.PBS must be preferred over deps.API when both are set")
}

// ---------------------------------------------------------------------------
// path normalization + params helper unit tests
// ---------------------------------------------------------------------------

func TestRawNormalizePath(t *testing.T) {
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
		got, err := rawNormalizePath(c.in)
		if c.wantErr {
			require.Error(t, err, "input %q", c.in)
			continue
		}
		require.NoError(t, err, "input %q", c.in)
		require.Equal(t, c.want, got, "input %q", c.in)
	}
}

func TestRawParamsFromData_EmptyReturnsNilMap(t *testing.T) {
	params, err := rawParamsFromData(nil)
	require.NoError(t, err)
	require.Nil(t, params)
}

func TestRawParamsFromData_BuildsMap(t *testing.T) {
	params, err := rawParamsFromData([]string{"a=1", "b=two"})
	require.NoError(t, err)
	require.Equal(t, map[string]interface{}{"a": "1", "b": "two"}, params)
}
