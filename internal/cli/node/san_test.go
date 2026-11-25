package node_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

var sanFormats = []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML}

// TestNodeSan_ListVerbs verifies each SAN list verb reads its endpoint and
// renders the rows in table, json, and yaml.
func TestNodeSan_ListVerbs(t *testing.T) {
	cases := []struct {
		args    []string
		path    string
		payload []any
		want    string
	}{
		{[]string{"san", "list"}, "/api2/json/nodes/pve1/san", []any{map[string]any{"name": "iscsi"}}, "iscsi"},
		{[]string{"san", "luns"}, "/api2/json/nodes/pve1/san/luns",
			[]any{map[string]any{"wwid": "3600a0980383030", "paths": 4}}, "3600a0980383030"},
		{[]string{"san", "iscsi", "list"}, "/api2/json/nodes/pve1/san/iscsi",
			[]any{map[string]any{"name": "status"}}, "status"},
		{[]string{"san", "iscsi", "targets"}, "/api2/json/nodes/pve1/san/iscsi/target",
			[]any{map[string]any{"target": "iqn.2001-04.com.example:t1", "portal": "10.0.0.5:3260"}}, "iqn.2001-04.com.example:t1"},
		{[]string{"san", "nvmeof", "list"}, "/api2/json/nodes/pve1/san/nvmeof",
			[]any{map[string]any{"name": "controller"}}, "controller"},
		{[]string{"san", "nvmeof", "controllers"}, "/api2/json/nodes/pve1/san/nvmeof/controller",
			[]any{map[string]any{"controller": "nvme1", "subsysnqn": "nqn.2014-08.org.example:sub1"}}, "nvme1"},
	}
	for _, tc := range cases {
		for _, format := range sanFormats {
			t.Run(tc.path+"/"+string(format), func(t *testing.T) {
				f := testhelper.NewFakePVE(t)
				var rec recordedRequest
				recordForm(f, "GET "+tc.path, &rec, tc.payload)

				root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
				root.SetArgs(append(prefix, append([]string{"--node", "pve1", "node"}, tc.args...)...))

				require.NoError(t, root.Execute())
				require.Equal(t, http.MethodGet, rec.method)
				require.Equal(t, tc.path, rec.path)
				require.Contains(t, buf.String(), tc.want)
			})
		}
	}
}

// TestNodeSan_StatusVerbs verifies the iSCSI and NVMe-oF status verbs render
// the host identity as a key/value block.
func TestNodeSan_StatusVerbs(t *testing.T) {
	cases := []struct {
		args    []string
		path    string
		payload map[string]any
		want    []string
	}{
		{[]string{"san", "iscsi", "status"}, "/api2/json/nodes/pve1/san/iscsi/status",
			map[string]any{"supported": 1, "initiator-name": "iqn.1993-08.org.debian:01:abc", "initiator-alias": "pve1"},
			[]string{"initiator-name", "iqn.1993-08.org.debian:01:abc"}},
		{[]string{"san", "nvmeof", "status"}, "/api2/json/nodes/pve1/san/nvmeof/status",
			map[string]any{"supported": 1, "host-nqn": "nqn.2014-08.org.nvmexpress:uuid:1", "host-id": "abc"},
			[]string{"host-nqn", "nqn.2014-08.org.nvmexpress:uuid:1"}},
	}
	for _, tc := range cases {
		for _, format := range sanFormats {
			t.Run(tc.path+"/"+string(format), func(t *testing.T) {
				f := testhelper.NewFakePVE(t)
				var rec recordedRequest
				recordForm(f, "GET "+tc.path, &rec, tc.payload)

				root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
				root.SetArgs(append(prefix, append([]string{"--node", "pve1", "node"}, tc.args...)...))

				require.NoError(t, root.Execute())
				require.Equal(t, tc.path, rec.path)
				for _, want := range tc.want {
					require.Contains(t, buf.String(), want)
				}
			})
		}
	}
}

// TestNodeSan_RequiresNode verifies a SAN read with no node fails before any
// request.
func TestNodeSan_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "san", "luns"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestNodeSan_ListWrapsServerError verifies a refusal names the node.
func TestNodeSan_ListWrapsServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/san/luns", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "Permission check failed")
	})
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "san", "luns"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `list SAN LUNs on node "pve1"`)
}

const sanDeleteUPID = "UPID:pve1:00000001:00000002:AABBCCDD:nvmedisconnect:nvme1:root@pam:"

// TestNodeSanNvmeofControllerDelete_WithYes verifies the delete sends the
// portal and subsystem guards, waits for the task, and renders in every format.
func TestNodeSanNvmeofControllerDelete_WithYes(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "DELETE /api2/json/nodes/pve1/san/nvmeof/controller/nvme1", &rec, sanDeleteUPID)
			waited := false
			f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+sanDeleteUPID+"/status",
				func(w http.ResponseWriter, _ *http.Request) {
					waited = true
					testhelper.WriteData(w, map[string]any{
						"status": "stopped", "exitstatus": "OK", "upid": sanDeleteUPID,
					})
				})

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "san", "nvmeof", "controller", "delete", "nvme1",
				"--portal", "10.0.0.5:4420", "--subsysnqn", "nqn.2014-08.org.example:sub1", "--yes"))

			require.NoError(t, root.Execute())
			require.Equal(t, http.MethodDelete, rec.method)
			require.Equal(t, "/api2/json/nodes/pve1/san/nvmeof/controller/nvme1", rec.path)
			require.Contains(t, rec.query, "portal=10.0.0.5%3A4420")
			require.Contains(t, rec.query, "subsysnqn=nqn.2014-08.org.example%3Asub1")
			require.Contains(t, buf.String(), "nvme1")
			require.True(t, waited, "the command must wait on the task")
		})
	}
}

// TestNodeSanNvmeofControllerDelete_RejectsNonUPID verifies a response that is
// not a task ID is an error rather than a success report.
func TestNodeSanNvmeofControllerDelete_RejectsNonUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("DELETE /api2/json/nodes/pve1/san/nvmeof/controller/nvme1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "san", "nvmeof", "controller", "delete", "nvme1", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "task ID")
	require.NotContains(t, buf.String(), "disconnected")
}

// TestNodeSanNvmeofControllerDelete_Async verifies --async prints the UPID
// without waiting, and that the guards are left out when not given.
func TestNodeSanNvmeofControllerDelete_Async(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "DELETE /api2/json/nodes/pve1/san/nvmeof/controller/nvme1", &rec, sanDeleteUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "san", "nvmeof", "controller", "delete",
		"nvme1", "--yes"))

	require.NoError(t, root.Execute())
	require.Empty(t, rec.query)
	require.Contains(t, buf.String(), sanDeleteUPID)
}

// TestNodeSanNvmeofControllerDelete_RefusesWithoutYes verifies no request goes
// out without confirmation.
func TestNodeSanNvmeofControllerDelete_RefusesWithoutYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/san/nvmeof/controller/nvme1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, sanDeleteUPID)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "san", "nvmeof", "controller", "delete", "nvme1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// TestNodeSanNvmeofControllerDelete_WrapsServerError verifies the controller
// and node are named when the server refuses.
func TestNodeSanNvmeofControllerDelete_WrapsServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("DELETE /api2/json/nodes/pve1/san/nvmeof/controller/nvme1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "portal mismatch")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "san", "nvmeof", "controller", "delete", "nvme1", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `controller "nvme1" on node "pve1"`)
}

// TestNodeScanNvmeof verifies the scan sends the portal and renders the
// subsystems in every format, and that --portal is required.
func TestNodeScanNvmeof(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/scan/nvmeof", &rec, []any{
				map[string]any{"subnqn": "nqn.2014-08.org.example:sub1", "traddr": "10.0.0.5"},
			})

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "scan", "nvmeof", "--portal", "10.0.0.5"))

			require.NoError(t, root.Execute())
			require.Equal(t, "portal=10.0.0.5", rec.query)
			require.Contains(t, buf.String(), "nqn.2014-08.org.example:sub1")
		})
	}

	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "scan", "nvmeof"))
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "portal")
}
