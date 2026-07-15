package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestPveLxcCmd_WiresAllVerbs asserts that `pve lxc` exposes the full
// shared verb set plus its lxc-only verbs, and excludes qemu-only verbs
// (resume, migrate-preconditions).
func TestPveLxcCmd_WiresAllVerbs(t *testing.T) {
	cmd := newPveLxcCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{
		"ls", "config", "status", "pending", "rrddata", "start", "shutdown", "stop",
		"snapshot", "migrate", "remote-migrate", "firewall",
	} {
		require.True(t, names[want], "pve lxc must expose %q", want)
	}
	require.False(t, names["resume"], "pve lxc must not expose qemu-only resume")
	require.False(t, names["migrate-preconditions"], "pve lxc must not expose qemu-only migrate-preconditions")
}

// TestPveLxcSnapshotAdd_RejectsVmstateFlag asserts that `lxc snapshot add`
// does not register --vmstate (CreateRemotesLxcSnapshotParams has no such
// field — a container has no RAM state to snapshot).
func TestPveLxcSnapshotAdd_RejectsVmstateFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveGuestSnapshotAddCmd(pveGuestLxc, pveLxcOps().snapshotCreate),
		"add", "cluster1", "104", "snap1", "--vmstate")
	require.Error(t, err)
	require.ErrorContains(t, err, "unknown flag")
}

// TestPveLxcMigrate_RequiresTargetNode asserts that `lxc migrate` refuses
// to run without --target-node.
func TestPveLxcMigrate_RequiresTargetNode(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveLxcMigrateCmd(), "migrate", "cluster1", "104", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "--target-node is required")
}

// TestPveLxcMigrate_RequiresConfirmation asserts that `lxc migrate` refuses
// to run without --yes/-y even with --target-node set.
func TestPveLxcMigrate_RequiresConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveLxcMigrateCmd(), "migrate", "cluster1", "104", "--target-node", "pve2")
	require.Error(t, err)
	require.ErrorContains(t, err, "--yes/-y")
}

// TestPveLxcMigrate_WaitsForRemoteTaskAndSendsLxcFlags asserts that `lxc
// migrate` with --yes blocks on the PVE remote's task-status endpoint and
// forwards lxc-specific flags (--restart, --timeout) that qemu's migrate
// does not accept.
func TestPveLxcMigrate_WaitsForRemoteTaskAndSendsLxcFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/remotes/cluster1/lxc/104/migrate", &rec, validUPID)
	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
		"id": "vzmigrate", "node": "pdm-host", "pid": 1, "pstart": 1, "starttime": 1, "type": "vzmigrate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveLxcMigrateCmd(), "migrate", "cluster1", "104",
		"--target-node", "pve2", "--yes", "--restart", "--timeout", "30")
	require.NoError(t, err)
	require.Equal(t, "pve2", rec.form.Get("target"))
	require.Equal(t, "1", rec.form.Get("restart"))
	require.Equal(t, "30", rec.form.Get("timeout"))
	require.Contains(t, buf.String(), "migrated")
}

// TestPveLxcRemoteMigrate_RequiresTargetFlags asserts that `lxc
// remote-migrate` refuses to run when --target-remote, --target-bridge, or
// --target-storage are missing.
func TestPveLxcRemoteMigrate_RequiresTargetFlags(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveLxcRemoteMigrateCmd(), "remote-migrate", "cluster1", "104", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "--target-remote is required")
}

// TestPveLxcRemoteMigrate_WaitsForRemoteTask asserts that `lxc
// remote-migrate` with --yes and all required flags blocks on the PVE
// remote's task-status endpoint.
func TestPveLxcRemoteMigrate_WaitsForRemoteTask(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/remotes/cluster1/lxc/104/remote-migrate", &rec, validUPID)
	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
		"id": "vzmigrate", "node": "pdm-host", "pid": 1, "pstart": 1, "starttime": 1, "type": "vzmigrate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveLxcRemoteMigrateCmd(), "remote-migrate", "cluster1", "104",
		"--target-remote", "cluster2", "--target-bridge", "vmbr0", "--target-storage", "local", "--yes")
	require.NoError(t, err)
	require.Equal(t, "cluster2", rec.form.Get("target"))
	require.ElementsMatch(t, []string{"vmbr0"}, rec.form["target-bridge"])
	require.ElementsMatch(t, []string{"local"}, rec.form["target-storage"])
	require.Contains(t, buf.String(), "remote-migrated")
}
