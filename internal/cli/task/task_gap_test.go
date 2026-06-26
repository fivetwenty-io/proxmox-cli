package task_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestTask_GapCommandTree verifies that `task status` is registered in the
// task group's command tree.
func TestTask_GapCommandTree(t *testing.T) {
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
	require.True(t, names["status"], "task group must expose the status sub-command")
}

// TestTaskStatus_Success verifies that `task status <upid>` issues GET
// /nodes/{node}/tasks/{upid}/status, parses the node from the UPID (no
// --node flag or default-node required), and renders the key fields.
func TestTaskStatus_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/status",
		func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.Path
			testhelper.WriteData(w, map[string]any{
				"upid":       testUPID,
				"status":     "stopped",
				"exitstatus": "OK",
				"pid":        1234,
				"pstart":     43981,
				"starttime":  1700000000,
				"type":       "vzdump",
				"id":         "100",
				"node":       "pve1",
				"user":       "root@pam",
			})
		},
	)

	// No node configured in deps — the command resolves node from the UPID.
	out, err := runTask(t, f, "", "table", "status", testUPID)
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/tasks/"+testUPID+"/status", gotPath)
	require.Contains(t, out, "stopped")
	require.Contains(t, out, "OK")
	require.Contains(t, out, "vzdump")
	require.Contains(t, out, "root@pam")
}

// TestTaskStatus_Running verifies that a running task (no exitstatus field)
// renders without EXITSTATUS in the output and does not error.
func TestTaskStatus_Running(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/status",
		func(w http.ResponseWriter, r *http.Request) {
			testhelper.WriteData(w, map[string]any{
				"upid":      testUPID,
				"status":    "running",
				"pid":       1234,
				"pstart":    43981,
				"starttime": 1700000000,
				"type":      "vzdump",
				"id":        "100",
				"node":      "pve1",
				"user":      "root@pam",
			})
		},
	)

	out, err := runTask(t, f, "", "table", "status", testUPID)
	require.NoError(t, err)
	require.Contains(t, out, "running")
}

// TestTaskStatus_InvalidUPID verifies that a malformed UPID errors before any
// HTTP call is made.
func TestTaskStatus_InvalidUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := runTask(t, f, "", "table", "status", "not-a-upid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse upid")
}

// TestTaskStatus_ServerError verifies that an upstream API error is surfaced.
func TestTaskStatus_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/status",
		func(w http.ResponseWriter, r *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "status read failed")
		},
	)

	_, err := runTask(t, f, "", "table", "status", testUPID)
	require.Error(t, err)
}
