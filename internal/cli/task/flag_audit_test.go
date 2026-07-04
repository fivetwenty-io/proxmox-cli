package task_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestTaskList_AuditAllQueryFlags verifies every `task list` filter flag is
// forwarded together as a GET /nodes/{node}/tasks query parameter in one
// request, proving they compose without clobbering each other. Individual
// filters are also exercised in task_test.go.
func TestTaskList_AuditAllQueryFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := runTask(t, f, "pve1", "table", "list",
		"--vmid", "100",
		"--typefilter", "vzdump",
		"--statusfilter", "error",
		"--since", "1700000000",
		"--until", "1700100000",
		"--limit", "25",
		"--start", "10",
		"--errors",
		"--source", "archive",
		"--userfilter", "root@pam",
	)
	require.NoError(t, err)

	form, err := url.ParseQuery(gotQuery)
	require.NoError(t, err)
	require.Equal(t, "100", form.Get("vmid"))
	require.Equal(t, "vzdump", form.Get("typefilter"))
	require.Equal(t, "error", form.Get("statusfilter"))
	require.Equal(t, "1700000000", form.Get("since"))
	require.Equal(t, "1700100000", form.Get("until"))
	require.Equal(t, "25", form.Get("limit"))
	require.Equal(t, "10", form.Get("start"))
	require.Equal(t, "1", form.Get("errors"))
	require.Equal(t, "archive", form.Get("source"))
	require.Equal(t, "root@pam", form.Get("userfilter"))
}

// TestTaskList_OmitsUnsetFilters verifies that a bare `task list` (no filter
// flags) sends no query parameters at all, including the flags that carry
// non-zero defaults in their registration (limit=50) but are gated behind
// cmd.Flags().Changed.
func TestTaskList_OmitsUnsetFilters(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := runTask(t, f, "pve1", "table", "list")
	require.NoError(t, err)
	require.Empty(t, gotQuery, "no query parameters must be sent when no filter flags are set")
}

// TestTaskLog_AuditFlags verifies `task log`'s --limit and --start flags
// forward together as query parameters.
func TestTaskLog_AuditFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/log", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := runTask(t, f, "pve1", "table", "log", testUPID, "--limit", "100", "--start", "5")
	require.NoError(t, err)

	form, err := url.ParseQuery(gotQuery)
	require.NoError(t, err)
	require.Equal(t, "100", form.Get("limit"))
	require.Equal(t, "5", form.Get("start"))
}

// TestTaskLog_DownloadConflictsWithLimit verifies --download combined with
// --limit is rejected before any request is made.
func TestTaskLog_DownloadConflictsWithLimit(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	called := false
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/log", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := runTask(t, f, "pve1", "table", "log", testUPID, "--download", "--limit", "10")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be combined")
	require.False(t, called, "no request must be issued when --download conflicts with --limit")
}

// TestTaskLog_DownloadConflictsWithStart verifies --download combined with
// --start is rejected before any request is made.
func TestTaskLog_DownloadConflictsWithStart(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	called := false
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/log", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := runTask(t, f, "pve1", "table", "log", testUPID, "--download", "--start", "5")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be combined")
	require.False(t, called, "no request must be issued when --download conflicts with --start")
}

// TestTaskWait_AuditClientFlags verifies `task wait`'s four flags (--timeout,
// --interval, --backoff, --max-interval) parse and compose together. These
// are client-side polling controls, not API request parameters: WaitForUPID
// consumes them to shape its own polling loop, so there is no request body or
// query string to assert against. The task terminates on the first poll here
// (status "stopped"), so the flags exercise parsing/wiring only, matching the
// scope of the existing TestWait_Backoff case in task_test.go.
func TestTaskWait_AuditClientFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"upid":       testUPID,
			"status":     "stopped",
			"exitstatus": "OK",
		})
	})

	out, err := runTask(t, f, "", "table", "wait", testUPID,
		"--timeout", "5", "--interval", "50", "--backoff", "--max-interval", "500")
	require.NoError(t, err)
	require.Contains(t, out, "stopped")
}

// TestTaskStop_NoWriteFlags documents that `task stop` registers no
// command-specific flag: it issues a bare DELETE to
// /nodes/{node}/tasks/{upid} with the UPID carried entirely in the URL path,
// no query string and no body.
func TestTaskStop_NoWriteFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotMethod, gotQuery string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/tasks/"+testUPID, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, nil)
	})

	out, err := runTask(t, f, "pve1", "table", "stop", testUPID)
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Empty(t, gotQuery, "stop must send no query parameters; it has no command-specific flags")
	require.Contains(t, out, "stopped")
}

// TestTaskStatus_NoWriteFlags documents that `task status` registers no
// command-specific flag at all: it is a single non-polling GET with the node
// parsed from the UPID, and sends no query string.
func TestTaskStatus_NoWriteFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/status", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"upid": testUPID, "status": "stopped", "exitstatus": "OK",
		})
	})

	_, err := runTask(t, f, "", "table", "status", testUPID)
	require.NoError(t, err)
	require.Empty(t, gotQuery, "status must send no query parameters; it has no command-specific flags")
}

// TestTaskClusterList_NoWriteFlags documents that `task cluster-list`
// registers no command-specific flag: it is a bare GET /cluster/tasks with no
// query string.
func TestTaskClusterList_NoWriteFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/tasks", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := runTask(t, f, "", "table", "cluster-list")
	require.NoError(t, err)
	require.Empty(t, gotQuery, "cluster-list must send no query parameters; it has no command-specific flags")
}

// TestTask_AuditCommandTree verifies every documented task sub-command is
// registered: list, cluster-list, log, status, wait, stop. This consolidates
// the separate tree checks in task_test.go (TestGroupCmd_Subcommands, missing
// "status") and task_gap_test.go (TestTask_GapCommandTree, "status" only)
// into a single, complete audit of the package's final command surface.
func TestTask_AuditCommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())
	addTaskGroup(root)

	var group *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "task" {
			group = c
			break
		}
	}
	require.NotNil(t, group, "task group must be registered")

	names := make(map[string]bool)
	for _, c := range group.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"list", "cluster-list", "log", "status", "wait", "stop"} {
		require.True(t, names[want], "expected task sub-command %q to be registered", want)
	}
}
