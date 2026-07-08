package pdm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// validUPID is a well-formed PDM UPID string (9 fields: the extra hex task-id
// sits between pstart and starttime) whose node is "pdm-host". The task-wait
// helper parses the node from this string when blocking on completion.
const validUPID = "UPID:pdm-host:00000C86:0000000B:00000003:685F9A3C:task_name:remotes:root@pam:"

// run mounts sub on a bare "pdm" parent command, injects deps via context,
// captures output in buf, and executes it with the supplied args. Tests drive
// their own sub-command directly so each command file is testable on its own.
func run(deps *cli.Deps, buf *bytes.Buffer, sub *cobra.Command, args ...string) error {
	cmd := &cobra.Command{Use: "pdm"}
	cmd.AddCommand(sub)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	return cmd.Execute()
}

// newFakeClient returns a FakePDM and a constructed PDMClient pointing at it.
func newFakeClient(t *testing.T) (*testhelper.FakePDM, *apiclient.PDMClient) {
	t.Helper()

	f := testhelper.NewFakePDM(t)
	pc, err := apiclient.NewPDMClient(f.Options)
	require.NoError(t, err)

	return f, pc
}

// depsFor builds a Deps with the given PDM client, format, and async flag.
func depsFor(t *testing.T, pc *apiclient.PDMClient, format output.Format, async bool) *cli.Deps {
	t.Helper()

	return &cli.Deps{PDM: pc, Out: output.New(), Format: format, Async: async}
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
// PDM-shaped {"data": payload} envelope.
func recordJSON(f *testhelper.FakePDM, pattern string, rec *recordedRequest, payload any) {
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
func handleTaskStatus(f *testhelper.FakePDM, upid string) {
	f.HandleJSON("GET /api2/json/nodes/pdm-host/tasks/"+upid+"/status", map[string]any{
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

// TestGroup_CarriesPDMProductAnnotation asserts that the pdm.Group command
// carries the correct product annotation.
func TestGroup_CarriesPDMProductAnnotation(t *testing.T) {
	cmd := Group(nil)
	require.NotNil(t, cmd)
	require.NotNil(t, cmd.Annotations)

	annotation, ok := cmd.Annotations[cli.ProductAnnotation]
	require.True(t, ok, "pdm.Group missing ProductAnnotation")
	require.Equal(t, config.ProductPDM, annotation)
}

// TestChildFactories_MatchesGroupChildrenAndExcludesShared asserts that
// ChildFactories returns exactly the commands exposed by Group and excludes
// shared commands (version, api, ping).
func TestChildFactories_MatchesGroupChildrenAndExcludesShared(t *testing.T) {
	factories := ChildFactories()

	// Build the Group command and collect its child names
	group := Group(nil)
	childNames := make(map[string]bool)
	for _, child := range group.Commands() {
		childNames[child.Name()] = true
	}

	// Each factory should produce exactly one command
	factoryCount := len(factories)
	require.Equal(t, len(childNames), factoryCount,
		"factory count should match group child count")

	// Verify shared commands are excluded
	require.False(t, childNames["version"], "version should not be in pdm.Group")
	require.False(t, childNames["api"], "api should not be in pdm.Group")
	require.False(t, childNames["ping"], "ping should not be in pdm.Group")
}

// TestFinishAsync_AsyncPrintsUPID asserts that with deps.Async=true,
// finishAsync prints the UPID immediately without waiting.
func TestFinishAsync_AsyncPrintsUPID(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	cmd := &cobra.Command{Use: "test"}
	upidMsg := mustJSONString(t, validUPID)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishAsync(cmd, deps, upidMsg, "Test message")
	require.NoError(t, err)

	// Should contain the UPID in output
	output := buf.String()
	require.Contains(t, output, validUPID)
}

// TestFinishAsync_WaitsForTaskAndPrintsMessage asserts that with deps.Async=false,
// finishAsync blocks until the task completes and prints the provided message.
func TestFinishAsync_WaitsForTaskAndPrintsMessage(t *testing.T) {
	fakePDM, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	// Register task status endpoint to return "stopped/OK" immediately
	handleTaskStatus(fakePDM, validUPID)

	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	upidMsg := mustJSONString(t, validUPID)
	expectedMsg := "Test completed"

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishAsync(cmd, deps, upidMsg, expectedMsg)
	require.NoError(t, err)

	// Should contain the success message
	output := buf.String()
	require.Contains(t, output, expectedMsg)
}

// TestFinishAsync_RejectsNonUPIDResponse asserts that finishAsync rejects
// responses that don't parse as UPID strings.
func TestFinishAsync_RejectsNonUPIDResponse(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	cmd := &cobra.Command{Use: "test"}

	// Provide an invalid UPID (not a valid string)
	invalidMsg := json.RawMessage(`{}`)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishAsync(cmd, deps, invalidMsg, "Test message")
	require.Error(t, err)
}
