package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// notifTargetsPath is the base /config/notifications/targets endpoint.
const notifTargetsPath = "/api2/json/config/notifications/targets"

// --- command tree -------------------------------------------------------------

func TestNewNotificationCmd_RegistersSubcommands(t *testing.T) {
	cmd := newNotificationCmd()
	require.Equal(t, "notification", cmd.Use)

	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["endpoint"])
	require.True(t, names["matcher"])
	require.True(t, names["target"])
}

func TestNewNotifEndpointCmd_RegistersEndpointTypes(t *testing.T) {
	cmd := newNotifEndpointCmd()
	require.Equal(t, "endpoint", cmd.Use)

	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["gotify"])
	require.True(t, names["sendmail"])
	require.True(t, names["smtp"])
	require.True(t, names["webhook"])
}

func TestNewNotifTargetCmd_RegistersVerbs(t *testing.T) {
	cmd := newNotifTargetCmd()
	require.Equal(t, "target", cmd.Use)

	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["ls"])
	require.True(t, names["test"])
	// GetNotificationsTargets returns null on the wire (directory index): no
	// "show" verb is registered since there is nothing to render.
	require.False(t, names["show"])
}

// --- target ls -----------------------------------------------------------------

func TestNotifTargetLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+notifTargetsPath, &rec, []map[string]any{
		{"name": "target-b", "type": "gotify", "comment": "second"},
		{"name": "target-a", "type": "sendmail", "disable": true},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifTargetCmd(), "target", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, notifTargetsPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "target-a")
	require.Contains(t, out, "target-b")
	require.Contains(t, out, "sendmail")
	require.Contains(t, out, "gotify")
}

func TestNotifTargetLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifTargetsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifTargetCmd(), "target", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list notification targets")
}

func TestNotifTargetLs_DecodeErrorSurfaces(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifTargetsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []string{"not-an-object"})
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifTargetCmd(), "target", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode notification target entry")
}

// --- target test -----------------------------------------------------------------

func TestNotifTargetTest_SendsTestNotification(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifTargetsPath+"/target-a/test", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifTargetCmd(), "target", "test", "target-a")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, notifTargetsPath+"/target-a/test", rec.path)
	require.Contains(t, buf.String(), `Test notification sent to target "target-a".`)
}

func TestNotifTargetTest_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+notifTargetsPath+"/target-a/test", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such target")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifTargetCmd(), "target", "test", "target-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "test notification target")
}
