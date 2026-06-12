package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestHaStatusList_Table verifies `pve cluster ha status list` queries
// GET /cluster/ha/status and renders the entries.
func TestHaStatusList_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/status", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"id": "quorum", "node": "pve", "status": "OK", "type": "quorum"},
			map[string]any{"id": "master", "node": "pve", "status": "active", "type": "master"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "status", "list"))

	require.Equal(t, "/api2/json/cluster/ha/status", gotPath)
	out := buf.String()
	require.Contains(t, out, "quorum")
	require.Contains(t, out, "master")
	require.Contains(t, out, "active")
}

// TestHaStatusCurrent_Table verifies `pve cluster ha status current` queries
// GET /cluster/ha/status/current.
func TestHaStatusCurrent_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/status/current", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"id": "vm:100", "node": "pve", "state": "started", "crm_state": "started"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "status", "current"))

	require.Equal(t, "/api2/json/cluster/ha/status/current", gotPath)
	require.Contains(t, buf.String(), "vm:100")
}

// TestHaStatusManager_Single verifies `pve cluster ha status manager` reads the
// raw manager status object.
func TestHaStatusManager_Single(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/status/manager_status", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"node_status": "online", "master_node": "pve"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "status", "manager"))

	require.Equal(t, "/api2/json/cluster/ha/status/manager_status", gotPath)
	require.Contains(t, buf.String(), "pve")
}

// TestHaStatusArm_RequiresYes verifies arm refuses without --yes and POSTs once
// confirmed.
func TestHaStatusArm_RequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("POST /api2/json/cluster/ha/status/arm-ha", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "status", "arm")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "arm must not be issued without --yes")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "ha", "status", "arm", "--yes"))
	require.True(t, called)
	require.Contains(t, buf.String(), "armed")
}

// TestHaStatusDisarm_RequiresYesAndMode verifies disarm refuses without --yes,
// then refuses without --resource-mode, then POSTs the mode once both are given.
func TestHaStatusDisarm_RequiresYesAndMode(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/ha/status/disarm-ha", func(w http.ResponseWriter, r *http.Request) {
		called = true
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "status", "disarm")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")

	buf.Reset()
	err = run(deps, &buf, "ha", "status", "disarm", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--resource-mode")
	require.False(t, called, "disarm must not be issued without --resource-mode")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "ha", "status", "disarm", "--yes", "--resource-mode", "freeze"))
	require.True(t, called)
	require.Equal(t, "freeze", gotForm.Get("resource-mode"))
	require.Contains(t, buf.String(), "disarmed")
}

// TestHaStatusList_ServerError verifies a server failure on status list surfaces
// an error.
func TestHaStatusList_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ha/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "ha", "status", "list"))
}
