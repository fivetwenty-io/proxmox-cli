package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestClusterBulk_StartForwardsFields verifies `pve cluster bulk start` posts the
// VMID list and changed optional flags and omits unset ones.
func TestClusterBulk_StartForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/start", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "bulk", "start", "--vmids", "100,101", "--timeout", "60", "--yes"))
	require.Equal(t, []string{"100", "101"}, gotForm["vms"])
	require.Equal(t, "60", gotForm.Get("timeout"))
	_, hasMW := gotForm["max-workers"]
	require.False(t, hasMW, "unset --max-workers must be omitted from the request body")
	require.Contains(t, buf.String(), "Bulk start started")
}

// TestClusterBulk_StartRequiresYes verifies start refuses to act without --yes.
func TestClusterBulk_StartRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/start", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "bulk", "start")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "start must not POST without --yes")
}

// TestClusterBulk_StartInvalidVMIDs verifies a non-numeric --vmids value is
// rejected before any request is made.
func TestClusterBulk_StartInvalidVMIDs(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/start", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "bulk", "start", "--vmids", "100,abc", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid VMID")
	require.False(t, called, "an invalid VMID list must not reach the API")
}

// TestClusterBulk_ShutdownForwardsForceStop verifies shutdown forwards --force-stop
// as a bool and omits unset numeric flags.
func TestClusterBulk_ShutdownForwardsForceStop(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/shutdown", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "bulk", "shutdown", "--force-stop", "--yes"))
	require.Equal(t, "1", gotForm.Get("force-stop"))
	_, hasTimeout := gotForm["timeout"]
	require.False(t, hasTimeout, "unset --timeout must be omitted from the request body")
	require.Contains(t, buf.String(), "Bulk shutdown started")
}

// TestClusterBulk_SuspendForwardsToDisk verifies suspend forwards --to-disk and
// --statestorage.
func TestClusterBulk_SuspendForwardsToDisk(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/suspend", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "bulk", "suspend", "--to-disk", "--statestorage", "local", "--yes"))
	require.Equal(t, "1", gotForm.Get("to-disk"))
	require.Equal(t, "local", gotForm.Get("statestorage"))
	require.Contains(t, buf.String(), "Bulk suspend started")
}

// TestClusterBulk_MigrateRequiresTargetNode verifies migrate fails without the
// required --target-node flag.
func TestClusterBulk_MigrateRequiresTargetNode(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/migrate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "bulk", "migrate", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "target-node")
	require.False(t, called, "migrate must not POST without --target-node")
}

// TestClusterBulk_MigrateForwardsFields verifies migrate posts the target node,
// VMID list, and --online flag.
func TestClusterBulk_MigrateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/migrate", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "bulk", "migrate", "--target-node", "pve2", "--vmids", "100", "--online", "--yes"))
	require.Equal(t, "pve2", gotForm.Get("target"))
	require.Equal(t, []string{"100"}, gotForm["vms"])
	require.Equal(t, "1", gotForm.Get("online"))
	require.Contains(t, buf.String(), "Bulk migration started")
}

// TestClusterBulk_MigrateAsyncReturnsUPID verifies --async prints the task UPID
// without waiting for completion.
func TestClusterBulk_MigrateAsyncReturnsUPID(t *testing.T) {
	f, ac := newFakeClient(t)
	upid := "UPID:pve:00000001:00000002:AABBCCDD:migrateall:root@pam:"
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain, Async: true}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "bulk", "migrate", "--target-node", "pve2", "--yes"))
	require.Contains(t, buf.String(), upid)
}

// TestClusterBulk_StartServerError verifies a server failure surfaces as an error.
func TestClusterBulk_StartServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/cluster/bulk-action/guest/start", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "bulk", "start", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "bulk start guests")
}

// TestClusterBulk_CommandTree verifies the bulk sub-tree exposes every verb.
func TestClusterBulk_CommandTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var bulk *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "bulk" {
			bulk = c
		}
	}
	require.NotNil(t, bulk, "cluster must expose a bulk sub-command")

	names := make(map[string]bool)
	for _, c := range bulk.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"start", "shutdown", "suspend", "migrate"} {
		require.True(t, names[want], "expected bulk sub-command %q", want)
	}
}
