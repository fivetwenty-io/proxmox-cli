package pbs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// validUPID is a well-formed PBS UPID string (9 fields: the extra hex task-id
// sits between pstart and starttime) whose node is "pbs1". The task-wait
// helper parses the node from this string when blocking on completion.
const validUPID = "UPID:pbs1:00000C86:0000000B:00000003:685F9A3C:garbage_collection:store1:root@pam:"

// run mounts sub on a bare "pbs" parent command, injects deps via context,
// captures output in buf, and executes it with the supplied args. Tests drive
// their own sub-command directly so each command file is testable on its own.
func run(deps *cli.Deps, buf *bytes.Buffer, sub *cobra.Command, args ...string) error {
	cmd := &cobra.Command{Use: "pbs"}
	cmd.AddCommand(sub)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	return cmd.Execute()
}

// newFakeClient returns a FakePBS and a constructed PBSClient pointing at it.
func newFakeClient(t *testing.T) (*testhelper.FakePBS, *apiclient.PBSClient) {
	t.Helper()

	f := testhelper.NewFakePBS(t)
	pc, err := apiclient.NewPBSClient(f.Options)
	require.NoError(t, err)

	return f, pc
}

// depsFor builds a Deps with the given PBS client, format, and async flag.
func depsFor(t *testing.T, pc *apiclient.PBSClient, format output.Format, async bool) *cli.Deps {
	t.Helper()

	return &cli.Deps{PBS: pc, Out: output.New(), Format: format, Async: async}
}

// recordedRequest captures the method, path, query, and form-encoded body of a
// request the fake server received, for assertion in tests. The Proxmox client
// encodes POST and PUT parameters as application/x-www-form-urlencoded values.
type recordedRequest struct {
	method string
	path   string
	query  url.Values
	form   url.Values
}

// recordJSON registers a handler that records the request and replies with the
// PBS-shaped {"data": payload} envelope.
func recordJSON(f *testhelper.FakePBS, pattern string, rec *recordedRequest, payload any) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.Query()
		_ = r.ParseForm()
		rec.form = r.PostForm
		testhelper.WriteData(w, payload)
	})
}

// handleTaskStatus registers a terminal "stopped/OK" task-status response so a
// blocking command completes immediately.
func handleTaskStatus(f *testhelper.FakePBS, upid string) {
	f.HandleJSON("GET /api2/json/nodes/pbs1/tasks/"+upid+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "OK",
		"upid":       upid,
	})
}

// mustJSONString returns v marshalled as a JSON string literal, for building
// raw UPID responses in tests.
func mustJSONString(t *testing.T, v string) json.RawMessage {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	return b
}

func TestGroup_CarriesPBSProductAnnotation(t *testing.T) {
	cmd := Group(&cli.Deps{})
	require.Equal(t, "pbs", cmd.Use)
	require.Equal(t, "pbs", cmd.Annotations[cli.ProductAnnotation])
}

func TestFinishAsync_AsyncPrintsUPID(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, true)

	sub := &cobra.Command{
		Use: "asynctest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return finishAsync(cmd, cli.GetDeps(cmd), mustJSONString(t, validUPID), "done.")
		},
	}

	var buf bytes.Buffer
	err := run(deps, &buf, sub, "asynctest")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "done.")
}

func TestFinishAsync_WaitsForTaskAndPrintsMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)
	deps := depsFor(t, pc, output.FormatTable, false)

	sub := &cobra.Command{
		Use: "waittest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return finishAsync(cmd, cli.GetDeps(cmd), mustJSONString(t, validUPID), "task finished.")
		},
	}

	var buf bytes.Buffer
	err := run(deps, &buf, sub, "waittest")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "task finished.")
}

func TestFinishAsync_RejectsNonUPIDResponse(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	sub := &cobra.Command{
		Use: "badupid",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return finishAsync(cmd, cli.GetDeps(cmd), mustJSONString(t, "not-a-upid"), "done.")
		},
	}

	var buf bytes.Buffer
	err := run(deps, &buf, sub, "badupid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a UPID")
}

// TestRunHarness_ExecutesSubcommandAgainstFake exercises the full harness:
// a sub-command resolves Deps.PBS via cli.GetDeps and hits the fake server,
// with recordJSON capturing the request.
func TestRunHarness_ExecutesSubcommandAgainstFake(t *testing.T) {
	f, pc := newFakeClient(t)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/ping", &rec, map[string]any{"pong": true})

	sub := &cobra.Command{
		Use: "pingtest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Ping.Ping(cmd.Context())
			if err != nil {
				return fmt.Errorf("ping: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("pong=%v", resp.Pong.Bool())}, deps.Format)
		},
	}

	var buf bytes.Buffer
	err := run(depsFor(t, pc, output.FormatTable, false), &buf, sub, "pingtest")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/ping", rec.path)
	require.Contains(t, buf.String(), "pong=true")
}

func TestPtrHelpers(t *testing.T) {
	require.Equal(t, "x", *strPtr("x"))
	require.True(t, *boolPtr(true))
	require.Equal(t, int64(42), *int64Ptr(42))
}
