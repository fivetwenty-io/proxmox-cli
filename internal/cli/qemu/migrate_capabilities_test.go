package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestQemuMigrateCapabilities_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/migration", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{"has-dbus-vmstate": 1})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "migrate", "capabilities"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/migration", gotPath)
	out := buf.String()
	require.Contains(t, out, "has-dbus-vmstate")
	require.Contains(t, out, "yes")
}

func TestQemuMigrateCapabilities_False(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/migration", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"has-dbus-vmstate": 0})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "migrate", "capabilities"))
	require.Contains(t, buf.String(), "no")
}

func TestQemuMigrateCapabilities_RequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "migrate", "capabilities")
	require.Error(t, err)
	require.ErrorContains(t, err, "no node specified")
}

func TestQemuMigrateCapabilities_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/migration", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "migrate", "capabilities")
	require.Error(t, err)
	require.ErrorContains(t, err, "get QEMU migration capabilities")
}
