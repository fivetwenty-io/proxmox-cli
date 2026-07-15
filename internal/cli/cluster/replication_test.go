package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestClusterReplication_List verifies `pmx pve cluster replication list` reads
// GET /cluster/replication and renders the job columns.
func TestClusterReplication_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/replication", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"id": "101-0", "guest": 101, "type": "local", "target": "pve2", "schedule": "*/15"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "list"))
	out := buf.String()
	require.Contains(t, out, "101-0")
	require.Contains(t, out, "pve2")
}

// TestClusterReplication_CreateForwardsFields verifies create posts the required
// id/target/type plus changed optional flags, and omits unset ones.
func TestClusterReplication_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/replication", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "create",
		"--id", "101-0", "--target-node", "pve2", "--schedule", "*/30"))
	require.Equal(t, "101-0", gotForm.Get("id"))
	require.Equal(t, "pve2", gotForm.Get("target"))
	require.Equal(t, "local", gotForm.Get("type"))
	require.Equal(t, "*/30", gotForm.Get("schedule"))
	// --disable was not passed; it must be absent from the body.
	_, hasDisable := gotForm["disable"]
	require.False(t, hasDisable, "unset --disable must be omitted from the request body")
}

// TestClusterReplication_Get verifies get reads GET /cluster/replication/{id}.
func TestClusterReplication_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/replication/101-0", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"id": "101-0", "guest": 101, "type": "local", "target": "pve2", "schedule": "*/15",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "get", "101-0"))
	require.Contains(t, buf.String(), "pve2")
}

// TestClusterReplication_SetRequiresFlag verifies set rejects an empty update.
func TestClusterReplication_SetRequiresFlag(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/replication/101-0", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "replication", "set", "101-0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
	require.False(t, called, "set must not issue a PUT when no flags are passed")
}

// TestClusterReplication_SetForwardsFields verifies set forwards changed flags
// (including --disable as a bool) and omits unset ones.
func TestClusterReplication_SetForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/replication/101-0", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "set", "101-0", "--schedule", "*/45", "--disable"))
	require.Equal(t, "*/45", gotForm.Get("schedule"))
	require.Equal(t, "1", gotForm.Get("disable"))
	_, hasRate := gotForm["rate"]
	require.False(t, hasRate, "unset --rate must be omitted from the request body")
}

// TestClusterReplication_DeleteRequiresYes verifies delete refuses without --yes.
func TestClusterReplication_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/replication/101-0", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "replication", "delete", "101-0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

// TestClusterReplication_DeleteWithYes verifies delete issues the DELETE and
// forwards --force as a query parameter (DELETE params are query-encoded).
func TestClusterReplication_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotForce string
	f.HandleFunc("DELETE /api2/json/cluster/replication/101-0", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotForce = r.URL.Query().Get("force")
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "delete", "101-0", "--yes", "--force"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "1", gotForce)
	require.Contains(t, buf.String(), "deleted")
}
