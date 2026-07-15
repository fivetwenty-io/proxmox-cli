package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- snapshot show ----------------------------------------------------------

func TestQemuSnapshotShow_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/snapshot/pre-upgrade/config", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"description": "before kernel upgrade",
			"snaptime":    1700000000,
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "show", "100", "pre-upgrade"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/snapshot/pre-upgrade/config", gotPath)
	out := buf.String()
	require.Contains(t, out, "before kernel upgrade")
}

func TestQemuSnapshotShow_EmptyResponse(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/snapshot/snap1/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "show", "100", "snap1"))
	require.Contains(t, buf.String(), "no additional configuration")
}

func TestQemuSnapshotShow_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/snapshot/snap1/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "snap not found")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "snapshot", "show", "100", "snap1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "show snapshot")
}

func TestQemuSnapshotShow_UnknownGuestErrors(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "snapshot", "show", "100", "snap1")
	require.Error(t, err)
	require.ErrorContains(t, err, "not found")
}

// --- snapshot update --------------------------------------------------------

func TestQemuSnapshotUpdate_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/snapshot/pre-upgrade/config", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "update", "100", "pre-upgrade",
		"--description", "updated desc"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/snapshot/pre-upgrade/config", gotPath)
	form := parseForm(t, body)
	require.Equal(t, "updated desc", form.Get("description"))
	require.Contains(t, buf.String(), "updated")
}

func TestQemuSnapshotUpdate_NoChanges(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "snapshot", "update", "100", "snap1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no configuration changes")
}

func TestQemuSnapshotUpdate_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/snapshot/snap1/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "snapshot", "update", "100", "snap1", "--description", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update snapshot")
}

func TestQemuSnapshotCommandTree_ShowUpdate(t *testing.T) {
	root := Group(nil)
	var snap *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "snapshot" {
			snap = c
			break
		}
	}
	require.NotNil(t, snap)

	subNames := make(map[string]bool)
	for _, c := range snap.Commands() {
		subNames[c.Name()] = true
	}
	require.True(t, subNames["show"], "expected snapshot sub-command 'show'")
	require.True(t, subNames["update"], "expected snapshot sub-command 'update'")
}
