package pdm

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestPveQemuCmd_WiresAllVerbs asserts that `pve qemu` exposes the full
// shared verb set plus its qemu-only verbs.
func TestPveQemuCmd_WiresAllVerbs(t *testing.T) {
	cmd := newPveQemuCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{
		"ls", "config", "status", "pending", "rrddata", "start", "shutdown", "stop",
		"resume", "snapshot", "migrate", "migrate-preconditions", "remote-migrate", "firewall",
	} {
		require.True(t, names[want], "pve qemu must expose %q", want)
	}
}

// TestPveQemuSnapshotAdd_SendsVmstate asserts that `qemu snapshot add`
// registers and forwards --vmstate (CreateRemotesQemuSnapshotParams has a
// Vmstate field; the lxc equivalent does not).
func TestPveQemuSnapshotAdd_SendsVmstate(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/remotes/cluster1/qemu/104/snapshot", &rec, validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveGuestSnapshotAddCmd(pveGuestQemu, pveQemuOps().snapshotCreate),
		"add", "cluster1", "104", "snap1", "--vmstate")
	require.NoError(t, err)
	require.Equal(t, "1", rec.form.Get("vmstate"))
}

// TestPveQemuResume_AsyncPrintsUPID asserts that `qemu resume` (qemu-only,
// not gated) prints the UPID immediately when --async is set.
func TestPveQemuResume_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	f.HandleJSON("POST /api2/json/pve/remotes/cluster1/qemu/104/resume", validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuResumeCmd(), "resume", "cluster1", "104")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

// TestPveQemuMigratePreconditions_RendersFields asserts that `qemu
// migrate-preconditions` (qemu-only, synchronous GET) renders the
// pre-flight migration data.
func TestPveQemuMigratePreconditions_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/qemu/104/migrate", &rec, map[string]any{
		"running": true, "allowed_nodes": []string{"pve2"}, "local_disks": []map[string]any{},
		"local_resources": []string{}, "mapped-resources": []string{}, "mapped-resource-info": map[string]any{},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuMigratePreconditionsCmd(), "migrate-preconditions", "cluster1", "104", "--target", "pve2")
	require.NoError(t, err)
	require.Equal(t, "pve2", rec.query.Get("target"))
	require.Contains(t, buf.String(), "pve2")
}

// TestPveQemuMigrate_RequiresTargetNode asserts that `qemu migrate` refuses
// to run without --target-node.
func TestPveQemuMigrate_RequiresTargetNode(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuMigrateCmd(), "migrate", "cluster1", "104", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "--target-node is required")
}

// TestPveQemuMigrate_RequiresConfirmation asserts that `qemu migrate`
// refuses to run without --yes/-y even with --target-node set.
func TestPveQemuMigrate_RequiresConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuMigrateCmd(), "migrate", "cluster1", "104", "--target-node", "pve2")
	require.Error(t, err)
	require.ErrorContains(t, err, "--yes/-y")
}

// TestPveQemuMigrate_WaitsForRemoteTaskAndSendsQemuFlags asserts that `qemu
// migrate` with --yes blocks on the PVE remote's task-status endpoint and
// forwards qemu-specific flags (--force, --with-local-disks) that lxc's
// migrate does not accept.
func TestPveQemuMigrate_WaitsForRemoteTaskAndSendsQemuFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/remotes/cluster1/qemu/104/migrate", &rec, validUPID)
	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+remoteUpid("cluster1", validUPID)+"/status", map[string]any{
		"id": "qmigrate", "node": "pdm-host", "pid": 1, "pstart": 1, "starttime": 1, "type": "qmigrate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuMigrateCmd(), "migrate", "cluster1", "104",
		"--target-node", "pve2", "--yes", "--force", "--with-local-disks", "--online")
	require.NoError(t, err)
	require.Equal(t, "pve2", rec.form.Get("target"))
	require.Equal(t, "1", rec.form.Get("force"))
	require.Equal(t, "1", rec.form.Get("with-local-disks"))
	require.Equal(t, "1", rec.form.Get("online"))
	require.Contains(t, buf.String(), "migrated")
}

// TestPveQemuRemoteMigrate_RequiresTargetFlags asserts that `qemu
// remote-migrate` refuses to run when --target-remote, --target-bridge, or
// --target-storage are missing.
func TestPveQemuRemoteMigrate_RequiresTargetFlags(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuRemoteMigrateCmd(), "remote-migrate", "cluster1", "104", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "--target-remote is required")
}

// TestPveQemuRemoteMigrate_WaitsForRemoteTask asserts that `qemu
// remote-migrate` with --yes and all required flags blocks on the PVE
// remote's task-status endpoint.
func TestPveQemuRemoteMigrate_WaitsForRemoteTask(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/remotes/cluster1/qemu/104/remote-migrate", &rec, validUPID)
	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+remoteUpid("cluster1", validUPID)+"/status", map[string]any{
		"id": "qmigrate", "node": "pdm-host", "pid": 1, "pstart": 1, "starttime": 1, "type": "qmigrate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuRemoteMigrateCmd(), "remote-migrate", "cluster1", "104",
		"--target-remote", "cluster2", "--target-bridge", "vmbr0", "--target-storage", "local", "--yes")
	require.NoError(t, err)
	require.Equal(t, "cluster2", rec.form.Get("target"))
	require.Equal(t, []string{"vmbr0"}, rec.form["target-bridge"])
	require.Equal(t, []string{"local"}, rec.form["target-storage"])
	require.Contains(t, buf.String(), "remote-migrated")
}

// prefixedUPID is the task handle PDM returns for a task it proxies on a
// managed remote: the remote's product and name ahead of the node's UPID.
const prefixedUPID = "pve:cluster1!UPID:pve-0:0000A1B2:00C3D4E5:66F0A1B2:qmigrate:104:root@pam!pdm:"

// TestPveQemuMigrate_AcceptsPDMPrefixedTaskID covers the handle shape PDM
// really emits for proxied tasks. The command must print the handle with
// --async, and must wait on it through the PDM remote-task endpoint without
// --async, both with exit code 0.
func TestPveQemuMigrate_AcceptsPDMPrefixedTaskID(t *testing.T) {
	for _, async := range []bool{true, false} {
		f, pc := newFakeClient(t)
		deps := depsFor(t, pc, output.FormatJSON, async)

		var rec recordedRequest
		recordJSON(f, "POST /api2/json/pve/remotes/cluster1/qemu/104/migrate", &rec, prefixedUPID)

		polled := false
		f.HandleFunc("GET /api2/json/pve/remotes/cluster1/tasks/"+prefixedUPID+"/status",
			func(w http.ResponseWriter, _ *http.Request) {
				polled = true
				testhelper.WriteData(w, map[string]any{
					"id": "qmigrate", "node": "pve-0", "pid": 1, "pstart": 1, "starttime": 1, "type": "qmigrate",
					"upid": prefixedUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
				})
			})

		var buf bytes.Buffer
		err := run(deps, &buf, newPveQemuMigrateCmd(), "migrate", "cluster1", "104",
			"--target-node", "pve2", "--online", "--yes")
		require.NoError(t, err, "async=%v", async)
		require.Equal(t, exitcode.OK, exitcode.FromError(err), "async=%v", async)

		if async {
			require.Contains(t, buf.String(), prefixedUPID)
			require.False(t, polled, "--async must not poll the task")
		} else {
			require.True(t, polled, "a blocking migrate must poll the remote-task status endpoint")
			require.Contains(t, buf.String(), "migrated")
		}
	}
}

// TestPveQemuMigrate_PrefixedTaskFailureExitsNonZero asserts that a proxied
// task that stops with an error status fails the command with a nonzero code.
func TestPveQemuMigrate_PrefixedTaskFailureExitsNonZero(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("POST /api2/json/pve/remotes/cluster1/qemu/104/migrate", prefixedUPID)
	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+prefixedUPID+"/status", map[string]any{
		"id": "qmigrate", "node": "pve-0", "pid": 1, "pstart": 1, "starttime": 1, "type": "qmigrate",
		"upid": prefixedUPID, "user": "root@pam", "status": "stopped", "exitstatus": "migration aborted",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveQemuMigrateCmd(), "migrate", "cluster1", "104",
		"--target-node", "pve2", "--yes")
	require.ErrorContains(t, err, "migration aborted")
	require.NotEqual(t, exitcode.OK, exitcode.FromError(err))
}

// TestFinishRemoteAsync_AcceptsPDMPrefixedTaskID covers the PBS side of the
// shared remote-task path: PDM prefixes a proxied PBS task with `pbs:<remote>!`.
func TestFinishRemoteAsync_AcceptsPDMPrefixedTaskID(t *testing.T) {
	const pbsUPID = "pbs:store1!UPID:pbs-0:0000A1B2:00C3D4E5:66F0A1B2:aptupdate::root@pam:"

	for _, async := range []bool{true, false} {
		f, pc := newFakeClient(t)
		deps := depsFor(t, pc, output.FormatJSON, async)

		f.HandleJSON("GET /api2/json/pbs/remotes/store1/tasks/"+pbsUPID+"/status", map[string]any{
			"node": "pbs-0", "pid": 1, "pstart": 1, "starttime": 1, "worker_type": "aptupdate",
			"upid": pbsUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
		})

		cmd := &cobra.Command{Use: "test"}
		cmd.SetContext(context.Background())

		var buf bytes.Buffer
		cmd.SetOut(&buf)

		err := finishRemoteAsync(cmd, deps, "store1", mustJSONString(t, pbsUPID), "Remote task completed")
		require.NoError(t, err, "async=%v", async)
		require.Equal(t, exitcode.OK, exitcode.FromError(err))

		if async {
			require.Contains(t, buf.String(), pbsUPID)
		} else {
			require.Contains(t, buf.String(), "Remote task completed")
		}
	}
}
