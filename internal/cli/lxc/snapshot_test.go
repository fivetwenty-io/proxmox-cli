package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestLxcSnapshotShow_Success verifies the basic get/render path for a named
// snapshot's stored configuration.
func TestLxcSnapshotShow_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/snapshot/pre-upgrade/config", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"description": "before kernel upgrade",
			"snaptime":    1700000000,
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "show", "200", "pre-upgrade")
	require.NoError(t, run())

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/200/snapshot/pre-upgrade/config", gotPath)
	require.Contains(t, buf.String(), "before kernel upgrade")
}

// TestLxcSnapshotShow_LargeNumberAndNestedValue pins the fix for the snapshot
// show renderer: a decoded JSON number above six digits used to print in
// exponent notation via fmt's %v, and a nested object used to print in Go map
// syntax with unstable key order. Both fields now render through
// cli.StringifyValue: the number as a plain integer, the nested object as
// compact, deterministically-ordered JSON.
func TestLxcSnapshotShow_LargeNumberAndNestedValue(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/snapshot/pre-upgrade/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"memory": 536870912,
			"lxc": []any{
				[]any{"lxc.cap.drop", "sys_admin"},
			},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "show", "200", "pre-upgrade")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "536870912")
	require.NotContains(t, out, "e+08")
	require.NotContains(t, out, "map[")
	require.Contains(t, out, "lxc.cap.drop")
}

func TestLxcSnapshotShow_EmptyResponse(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/snapshot/snap1/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "show", "200", "snap1")
	require.NoError(t, run())
}

func TestLxcSnapshotShow_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/snapshot/snap1/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "snap not found")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "show", "200", "snap1")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get config for snapshot")
}
