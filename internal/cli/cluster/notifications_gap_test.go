package cluster

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestNotificationsTargetsTest_Success verifies `pve cluster notifications targets-test <name>`
// posts to POST /cluster/notifications/targets/{name}/test and renders a success message.
func TestNotificationsTargetsTest_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/cluster/notifications/targets/mail-to-root/test", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "targets-test", "mail-to-root"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/cluster/notifications/targets/mail-to-root/test", gotPath)
	require.Contains(t, buf.String(), "mail-to-root")
}

// TestNotificationsTargetsTest_ServerError verifies a server error surfaces correctly.
func TestNotificationsTargetsTest_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/cluster/notifications/targets/missing/test", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "target not found")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "notifications", "targets-test", "missing"))
}

// TestNotificationsMatcherFields_Table verifies `pve cluster notifications matcher-fields`
// reads GET /cluster/notifications/matcher-fields and renders the fields.
func TestNotificationsMatcherFields_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/notifications/matcher-fields", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"name": "severity"},
			map[string]any{"name": "type"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "matcher-fields"))

	require.Equal(t, "/api2/json/cluster/notifications/matcher-fields", gotPath)
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "severity")
}

// TestNotificationsMatcherFields_ServerError verifies server error surfaces correctly.
func TestNotificationsMatcherFields_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/notifications/matcher-fields", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "notifications", "matcher-fields"))
}

// TestNotificationsMatcherFieldValues_Table verifies `pve cluster notifications matcher-field-values`
// reads GET /cluster/notifications/matcher-field-values and renders a table.
func TestNotificationsMatcherFieldValues_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/notifications/matcher-field-values", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"field": "severity", "value": "error"},
			map[string]any{"field": "severity", "value": "warning"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "matcher-field-values"))

	require.Equal(t, "/api2/json/cluster/notifications/matcher-field-values", gotPath)
	out := buf.String()
	require.Contains(t, out, "FIELD")
	require.Contains(t, out, "severity")
}

// TestNotificationsMatcherFieldValues_ServerError verifies server error surfaces correctly.
func TestNotificationsMatcherFieldValues_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/notifications/matcher-field-values", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "notifications", "matcher-field-values"))
}

// TestNotificationsCommandTree_GapCommands verifies the new gap commands are registered.
func TestNotificationsCommandTree_GapCommands(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var notif *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "notifications" {
			notif = c
		}
	}
	require.NotNil(t, notif, "cluster must expose a notifications sub-command")

	names := make(map[string]bool)
	for _, c := range notif.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["targets-test"], "notifications must expose targets-test")
	require.True(t, names["matcher-fields"], "notifications must expose matcher-fields")
	require.True(t, names["matcher-field-values"], "notifications must expose matcher-field-values")
}
