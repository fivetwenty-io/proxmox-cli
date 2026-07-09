package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestPbsNodeAptUpdates_ListsPackages asserts that `pbs node apt updates`
// decodes the PBS host's capitalized field names (Package/OldVersion/...).
func TestPbsNodeAptUpdates_ListsPackages(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/nodes/pbs-node1/apt/update", []map[string]any{
		{
			"Package": "proxmox-backup-server", "OldVersion": "3.2-1", "Version": "3.2-2",
			"Priority": "optional", "Section": "admin", "Origin": "proxmox",
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsNodeAptUpdatesCmd(), "updates", "backup1", "pbs-node1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "proxmox-backup-server")
	require.Contains(t, buf.String(), "3.2-2")
}

// TestPbsNodeAptUpdateDatabase_BlocksUntilRemoteTaskFinishes asserts that
// `pbs node apt update-database` blocks until the remote task completes,
// polling the pbs group's task-status endpoint (not PDM's local node tasks).
func TestPbsNodeAptUpdateDatabase_BlocksUntilRemoteTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pbs/remotes/backup1/nodes/pbs-node1/apt/update", &rec, validUPID)
	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/tasks/"+validUPID+"/status", map[string]any{
		"node": "pbs-node1", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsNodeAptUpdateDatabaseCmd(), "update-database", "backup1", "pbs-node1")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), `APT package index on node "pbs-node1" of PBS remote "backup1" refreshed.`)
}

// TestPbsNodeAptUpdateDatabase_Async asserts that `pbs node apt
// update-database` prints the UPID immediately when --async is set.
func TestPbsNodeAptUpdateDatabase_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST /api2/json/pbs/remotes/backup1/nodes/pbs-node1/apt/update", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newPbsNodeAptUpdateDatabaseCmd(), "update-database", "backup1", "pbs-node1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "refreshed")
}

// TestPbsNodeAptRepositories_UsesRawBypass asserts that `pbs node apt
// repositories` recovers data via the raw-transport bypass (the generated
// binding discards the response body since the PDM API schema declares
// returns.type "null" for this endpoint) and renders summary counts in
// Single while preserving the full structure in Raw.
func TestPbsNodeAptRepositories_UsesRawBypass(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/nodes/pbs-node1/apt/repositories", map[string]any{
		"digest": "def456",
		"files":  []map[string]any{{"path": "/etc/apt/sources.list"}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsNodeAptRepositoriesCmd(), "repositories", "backup1", "pbs-node1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "def456")
	require.Contains(t, buf.String(), "files")
}

// TestPbsNodeAptChangelog_RendersText asserts that `pbs node apt changelog`
// takes the package name as a positional argument and decodes the changelog
// text.
func TestPbsNodeAptChangelog_RendersText(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pbs/remotes/backup1/nodes/pbs-node1/apt/changelog", &rec, "changelog text")

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsNodeAptChangelogCmd(), "changelog", "backup1", "pbs-node1", "proxmox-backup-server")
	require.NoError(t, err)

	require.Equal(t, "proxmox-backup-server", rec.query.Get("name"))
	require.Contains(t, buf.String(), "changelog text")
}

// TestPbsNodeSubscription_RendersSingle asserts that `pbs node subscription`
// renders the node's subscription fields.
func TestPbsNodeSubscription_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/nodes/pbs-node1/subscription", map[string]any{
		"status": "notfound",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsNodeSubscriptionCmd(), "subscription", "backup1", "pbs-node1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "notfound")
}
