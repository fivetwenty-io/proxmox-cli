package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestNodeAptUpdates_ListsPackages asserts that `node apt updates` renders
// the available package updates.
func TestNodeAptUpdates_ListsPackages(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/apt/update", []map[string]any{
		{"package": "proxmox-datacenter-manager", "oldversion": "0.9.0", "version": "0.9.1", "priority": "optional", "section": "admin", "origin": "proxmox"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptUpdatesCmd(), "updates", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "proxmox-datacenter-manager")
	require.Contains(t, buf.String(), "0.9.1")
}

// TestNodeAptUpdateDatabase_BlocksUntilTaskFinishes asserts that `node apt
// update-database` blocks until the refresh task completes.
func TestNodeAptUpdateDatabase_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/apt/update", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptUpdateDatabaseCmd(), "update-database", "pdm-host", "--quiet")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "1", rec.form.Get("quiet"))
	require.Contains(t, buf.String(), `APT package index on node "pdm-host" refreshed.`)
}

// TestNodeAptUpdateDatabase_Async asserts that `node apt update-database`
// prints the UPID immediately when --async is set.
func TestNodeAptUpdateDatabase_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST /api2/json/nodes/pdm-host/apt/update", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptUpdateDatabaseCmd(), "update-database", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "refreshed")
}

// TestNodeAptRepositories_RendersSummary asserts that `node apt
// repositories` renders summary counts in Single and the full structure in Raw.
func TestNodeAptRepositories_RendersSummary(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/apt/repositories", map[string]any{
		"digest": "abc123",
		"files":  []map[string]any{{"path": "/etc/apt/sources.list"}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptRepositoriesCmd(), "repositories", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "abc123")
	require.Contains(t, buf.String(), "files")
}

// TestNodeAptRepositoryAdd_SendsHandle asserts that `node apt repository
// add` issues a PUT with the handle field (the binding's Update* method is
// the HTTP PUT that adds a repository).
func TestNodeAptRepositoryAdd_SendsHandle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/apt/repositories", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptRepositoryAddCmd(), "add", "pdm-host", "--handle", "no-subscription")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "no-subscription", rec.form.Get("handle"))
	require.Contains(t, buf.String(), `APT repository "no-subscription" added on node "pdm-host".`)
}

// TestNodeAptRepositoryChange_SendsPathAndIndex asserts that `node apt
// repository change` issues a POST with path/index/enabled (the binding's
// Create* method is the HTTP POST that changes properties).
func TestNodeAptRepositoryChange_SendsPathAndIndex(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/apt/repositories", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptRepositoryChangeCmd(), "change", "pdm-host",
		"--path", "/etc/apt/sources.list", "--index", "0", "--enabled=false")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/etc/apt/sources.list", rec.form.Get("path"))
	require.Equal(t, "0", rec.form.Get("index"))
	require.Equal(t, "0", rec.form.Get("enabled"))
	require.Contains(t, buf.String(), `APT repository "/etc/apt/sources.list"[0] on node "pdm-host" changed.`)
}

// TestNodeAptVersions_ListsPackages asserts that `node apt versions`
// renders installed package versions.
func TestNodeAptVersions_ListsPackages(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/apt/versions", []map[string]any{
		{"package": "proxmox-datacenter-manager", "version": "0.9.1"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptVersionsCmd(), "versions", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "0.9.1")
}

// TestNodeAptChangelog_RendersText asserts that `node apt changelog`
// decodes and prints the changelog text.
func TestNodeAptChangelog_RendersText(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/apt/changelog", "changelog text")

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeAptChangelogCmd(), "changelog", "pdm-host", "--name", "proxmox-datacenter-manager")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "changelog text")
}
