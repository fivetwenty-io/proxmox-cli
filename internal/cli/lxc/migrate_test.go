package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestLxcMigrateCheck_Success verifies the basic feasibility-check path,
// including a comma-joined allowed-nodes list.
func TestLxcMigrateCheck_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"running":       false,
			"allowed-nodes": []string{"pve2", "pve3"},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "200")
	require.NoError(t, run())

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/200/migrate", gotPath)
	out := buf.String()
	require.Contains(t, out, "pve2")
	require.Contains(t, out, "pve3")
}

// TestLxcMigrateCheck_NotAllowedNodesAsArray pins the fix that keeps
// not-allowed-nodes consistent with the comma-joined rendering already used
// for allowed-nodes, for the shape where PVE sends it as a plain array of
// node names.
func TestLxcMigrateCheck_NotAllowedNodesAsArray(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"running":           false,
			"allowed-nodes":     []string{"pve2"},
			"not-allowed-nodes": []string{"pve4", "pve5"},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "200")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "pve4, pve5")
	require.NotContains(t, out, "[")
	require.NotContains(t, out, "]")
}

// TestLxcMigrateCheck_NotAllowedNodesAsObject pins the fix for the shape PVE
// sends when a node is blocked for a specific reason: not-allowed-nodes as an
// object keyed by node name. Before the fix this rendered as raw JSON text
// (via json.Marshal) while allowed-nodes rendered as a comma-joined list; the
// two are now rendered consistently as "node: reason" pairs.
func TestLxcMigrateCheck_NotAllowedNodesAsObject(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"running":       false,
			"allowed-nodes": []string{"pve2"},
			"not-allowed-nodes": map[string]any{
				"pve4": map[string]any{"unavailable_storages": []string{"local-lvm"}},
				"pve3": "incompatible cpu",
			},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "200")
	require.NoError(t, run())

	out := buf.String()
	// Deterministic key order: pve3 sorts before pve4.
	require.Contains(t, out, "pve3: incompatible cpu")
	require.Contains(t, out, "pve4:")
	require.Contains(t, out, "unavailable_storages")
	require.NotContains(t, out, "map[")
}

func TestLxcMigrateCheck_WithTargetNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"running": false})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "200", "--target-node", "pve2")
	require.NoError(t, run())
	require.Contains(t, gotQuery, "target=pve2")
}

func TestLxcMigrateCheck_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "200")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "check migration feasibility")
}
