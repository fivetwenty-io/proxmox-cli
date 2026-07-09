package pdm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// guestKindFixture pairs a pveGuestKind with its concrete adapters, so the
// shared-behavior tests below can run identically against qemu and lxc.
type guestKindFixture struct {
	kind pveGuestKind
	ops  pveGuestOps
}

var guestFixtures = []guestKindFixture{
	{kind: pveGuestQemu, ops: pveQemuOps()},
	{kind: pveGuestLxc, ops: pveLxcOps()},
}

// TestNewPveCmd_WiresQemuAndLxc asserts that `pve` exposes both guest groups.
func TestNewPveCmd_WiresQemuAndLxc(t *testing.T) {
	cmd := newPveCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["qemu"], "pve must expose a qemu sub-command")
	require.True(t, names["lxc"], "pve must expose an lxc sub-command")
}

// TestPveGuestLs_SortsByVmid asserts that `<kind> ls` sorts entries by VMID
// and keeps each row's raw JSON paired through the sort, for both guest kinds.
func TestPveGuestLs_SortsByVmid(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun, []map[string]any{
				{"vmid": 200, "status": "running"},
				{"vmid": 100, "status": "stopped"},
			})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestLsCmd(gf.kind, gf.ops.list), "ls", "cluster1")
			require.NoError(t, err)

			var got []map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
			require.Len(t, got, 2)
			require.EqualValues(t, 100, got[0]["vmid"], "entries must sort by vmid")
			require.EqualValues(t, 200, got[1]["vmid"])
		})
	}
}

// TestPveGuestLs_SendsNode asserts that `<kind> ls` forwards --node on the wire.
func TestPveGuestLs_SendsNode(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatTable, false)

			var rec recordedRequest
			recordJSON(f, "GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun, &rec,
				[]map[string]any{{"vmid": 100, "status": "running"}})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestLsCmd(gf.kind, gf.ops.list), "ls", "cluster1", "--node", "pve1")
			require.NoError(t, err)
			require.Equal(t, "pve1", rec.query.Get("node"))
		})
	}
}

// TestPveGuestConfig_RawBypassDoesNotFabricateFields asserts that `<kind>
// config` recovers data via the raw-transport bypass and does not fabricate
// the numbered per-slot fields (dev0, mp0, ...) the typed struct declares as
// always-present.
func TestPveGuestConfig_RawBypassDoesNotFabricateFields(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var rec recordedRequest
			recordJSON(f, "GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/config", &rec,
				map[string]any{"hostname": "ct1", "memory": 512})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestConfigCmd(gf.kind), "config", "cluster1", "104")
			require.NoError(t, err)
			require.Equal(t, "pending", rec.query.Get("state"), "state defaults to pending")

			var got map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
			require.Equal(t, "ct1", got["hostname"])
			_, hasDev0 := got["dev0"]
			require.False(t, hasDev0, "config output must not fabricate unset numbered fields like dev0")
		})
	}
}

// TestPveGuestConfig_SendsStateNodeSnapshot asserts that `<kind> config`
// forwards --state/--node/--snapshot on the wire.
func TestPveGuestConfig_SendsStateNodeSnapshot(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var rec recordedRequest
			recordJSON(f, "GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/config", &rec, map[string]any{})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestConfigCmd(gf.kind), "config", "cluster1", "104",
				"--state", "active", "--node", "pve1", "--snapshot", "snap1")
			require.NoError(t, err)
			require.Equal(t, "active", rec.query.Get("state"))
			require.Equal(t, "pve1", rec.query.Get("node"))
			require.Equal(t, "snap1", rec.query.Get("snapshot"))
		})
	}
}

// TestPveGuestStatus_RendersSingle asserts that `<kind> status` renders the
// guest's status fields.
func TestPveGuestStatus_RendersSingle(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/status",
				map[string]any{"status": "running", "vmid": 104})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestStatusCmd(gf.kind, gf.ops.status), "status", "cluster1", "104")
			require.NoError(t, err)
			require.Contains(t, buf.String(), "running")
		})
	}
}

// TestPveGuestPending_UsesRawBypass asserts that `<kind> pending` recovers
// data via the raw-transport bypass (the generated binding discards the
// response body despite the endpoint being data-bearing).
func TestPveGuestPending_UsesRawBypass(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/pending",
				[]map[string]any{{"key": "memory", "value": "512", "pending": "1024"}})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestPendingCmd(gf.kind), "pending", "cluster1", "104")
			require.NoError(t, err)
			require.Contains(t, buf.String(), "memory")
			require.Contains(t, buf.String(), "1024")
		})
	}
}

// TestPveGuestRrddata_ValidatesTimeframe asserts that `<kind> rrddata`
// validates --timeframe against the enum before issuing any request.
func TestPveGuestRrddata_ValidatesTimeframe(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			_, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatTable, false)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestRrddataCmd(gf.kind, gf.ops.rrddata),
				"rrddata", "cluster1", "104", "--timeframe", "bogus")
			require.Error(t, err)
			require.ErrorContains(t, err, "--timeframe must be one of")
		})
	}
}

// TestPveGuestRrddata_PreservesServerOrder asserts that `<kind> rrddata`
// renders RRD data points in server order (not sorted) and never sends
// --node (neither Rrddata params struct accepts one).
func TestPveGuestRrddata_PreservesServerOrder(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatTable, false)

			var rec recordedRequest
			recordJSON(f, "GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/rrddata", &rec,
				[]map[string]any{{"time": 2000, "cpu-current": 0.4}, {"time": 1000, "cpu-current": 0.1}})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestRrddataCmd(gf.kind, gf.ops.rrddata),
				"rrddata", "cluster1", "104", "--timeframe", "hour")
			require.NoError(t, err)

			rows := buf.String()
			require.Less(t, strings.Index(rows, "2000"), strings.Index(rows, "1000"),
				"rrddata rows must preserve server order, not be sorted")
			require.Empty(t, rec.query.Get("node"), "rrddata params accept no node field")
		})
	}
}

// TestPveGuestLifecycle_StartAsyncPrintsUPID asserts that `<kind> start`
// (not gated) prints the UPID immediately when --async is set.
func TestPveGuestLifecycle_StartAsyncPrintsUPID(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, true)

			f.HandleJSON("POST /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/start", validUPID)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestLifecycleCmd(gf.kind, "start", "started", false, gf.ops.start),
				"start", "cluster1", "104")
			require.NoError(t, err)
			require.Contains(t, buf.String(), validUPID)
		})
	}
}

// TestPveGuestLifecycle_ShutdownSendsNode asserts that `<kind> shutdown`
// (not gated) forwards --node on the wire.
func TestPveGuestLifecycle_ShutdownSendsNode(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, true)

			var rec recordedRequest
			recordJSON(f, "POST /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/shutdown", &rec, validUPID)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestLifecycleCmd(gf.kind, "shutdown", "shut down", false, gf.ops.shutdown),
				"shutdown", "cluster1", "104", "--node", "pve1")
			require.NoError(t, err)
			require.Equal(t, "pve1", rec.form.Get("node"))
		})
	}
}

// TestPveGuestLifecycle_StopRequiresConfirmation asserts that `<kind> stop`
// (gated) refuses to run without --yes/-y.
func TestPveGuestLifecycle_StopRequiresConfirmation(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			_, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestLifecycleCmd(gf.kind, "stop", "stopped", true, gf.ops.stop),
				"stop", "cluster1", "104")
			require.Error(t, err)
			require.ErrorContains(t, err, "--yes/-y")
		})
	}
}

// TestPveGuestLifecycle_StopWaitsForRemoteTask asserts that `<kind> stop`
// with --yes blocks on and polls the PVE remote's task-status endpoint (not
// PDM's own local node-task endpoint) until the task completes.
func TestPveGuestLifecycle_StopWaitsForRemoteTask(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			f.HandleJSON("POST /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/stop", validUPID)
			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
				"id": "vmstop", "node": "pdm-host", "pid": 1, "pstart": 1, "starttime": 1, "type": "vmstop",
				"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
			})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestLifecycleCmd(gf.kind, "stop", "stopped", true, gf.ops.stop),
				"stop", "cluster1", "104", "--yes")
			require.NoError(t, err)
			require.Contains(t, buf.String(), "stopped")
		})
	}
}

// TestPveGuestSnapshotLs_PreservesServerOrder asserts that `<kind> snapshot
// ls` renders the parent-chain listing (ending in the synthetic "current"
// entry) in server order, not sorted.
func TestPveGuestSnapshotLs_PreservesServerOrder(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatTable, false)

			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/snapshot", []map[string]any{
				{"name": "zeta-snap", "description": "z"},
				{"name": "current", "description": "You are here!"},
			})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotLsCmd(gf.kind, gf.ops.snapshotList), "ls", "cluster1", "104")
			require.NoError(t, err)

			rows := buf.String()
			require.Less(t, strings.Index(rows, "zeta-snap"), strings.Index(rows, "current"),
				"snapshot rows must preserve server order (parent-chain), not be sorted")
		})
	}
}

// TestPveGuestSnapshotAdd_AsyncPrintsUPID asserts that `<kind> snapshot add`
// (not gated) creates a snapshot and prints the UPID when --async is set.
func TestPveGuestSnapshotAdd_AsyncPrintsUPID(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, true)

			var rec recordedRequest
			recordJSON(f, "POST /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/snapshot", &rec, validUPID)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotAddCmd(gf.kind, gf.ops.snapshotCreate),
				"add", "cluster1", "104", "snap1", "--description", "before upgrade")
			require.NoError(t, err)
			require.Contains(t, buf.String(), validUPID)
			require.Equal(t, "snap1", rec.form.Get("snapname"))
			require.Equal(t, "before upgrade", rec.form.Get("description"))
		})
	}
}

// TestPveGuestSnapshotDelete_RequiresConfirmation asserts that `<kind>
// snapshot delete` refuses to run without --yes/-y.
func TestPveGuestSnapshotDelete_RequiresConfirmation(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			_, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotDeleteCmd(gf.kind, gf.ops.snapshotDelete),
				"delete", "cluster1", "104", "snap1")
			require.Error(t, err)
			require.ErrorContains(t, err, "--yes/-y")
		})
	}
}

// TestPveGuestSnapshotDelete_WaitsForRemoteTask asserts that `<kind>
// snapshot delete` with --yes blocks on the PVE remote's task-status
// endpoint.
func TestPveGuestSnapshotDelete_WaitsForRemoteTask(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			f.HandleJSON("DELETE /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/snapshot/snap1", validUPID)
			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
				"id": "delsnapshot", "node": "pdm-host", "pid": 1, "pstart": 1, "starttime": 1, "type": "delsnapshot",
				"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
			})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotDeleteCmd(gf.kind, gf.ops.snapshotDelete),
				"delete", "cluster1", "104", "snap1", "--yes")
			require.NoError(t, err)
			require.Contains(t, buf.String(), "deleted")
		})
	}
}

// TestPveGuestSnapshotUpdate_RequiresDescription asserts that `<kind>
// snapshot update` refuses to run without --description.
func TestPveGuestSnapshotUpdate_RequiresDescription(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			_, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotUpdateCmd(gf.kind, gf.ops.snapshotUpdate),
				"update", "cluster1", "104", "snap1")
			require.Error(t, err)
			require.ErrorContains(t, err, "--description")
		})
	}
}

// TestPveGuestSnapshotUpdate_IsSynchronous asserts that `<kind> snapshot
// update` completes without polling any task-status endpoint (no worker
// task is created — UpdateRemotes<Kind>SnapshotConfig returns only an
// error).
func TestPveGuestSnapshotUpdate_IsSynchronous(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var rec recordedRequest
			recordJSON(f, "PUT /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/snapshot/snap1/config", &rec, nil)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotUpdateCmd(gf.kind, gf.ops.snapshotUpdate),
				"update", "cluster1", "104", "snap1", "--description", "renamed")
			require.NoError(t, err)
			require.Equal(t, "renamed", rec.form.Get("description"))
			require.Contains(t, buf.String(), "updated")
		})
	}
}

// TestPveGuestSnapshotRollback_RequiresConfirmation asserts that `<kind>
// snapshot rollback` refuses to run without --yes/-y.
func TestPveGuestSnapshotRollback_RequiresConfirmation(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			_, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotRollbackCmd(gf.kind, gf.ops.snapshotRollback),
				"rollback", "cluster1", "104", "snap1")
			require.Error(t, err)
			require.ErrorContains(t, err, "--yes/-y")
		})
	}
}

// TestPveGuestSnapshotRollback_WaitsForRemoteTask asserts that `<kind>
// snapshot rollback` with --yes blocks on the PVE remote's task-status
// endpoint and forwards --start.
func TestPveGuestSnapshotRollback_WaitsForRemoteTask(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var rec recordedRequest
			recordJSON(f, "POST /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/snapshot/snap1/rollback", &rec, validUPID)
			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
				"id": "rollback", "node": "pdm-host", "pid": 1, "pstart": 1, "starttime": 1, "type": "rollback",
				"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
			})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestSnapshotRollbackCmd(gf.kind, gf.ops.snapshotRollback),
				"rollback", "cluster1", "104", "snap1", "--yes", "--start")
			require.NoError(t, err)
			require.Equal(t, "1", rec.form.Get("start"))
			require.Contains(t, buf.String(), "rolled back")
		})
	}
}

// TestPveGuestFirewallOptionsShow_RendersSingle asserts that `<kind>
// firewall options show` renders the guest's firewall options.
func TestPveGuestFirewallOptionsShow_RendersSingle(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/firewall/options",
				map[string]any{"enable": true, "policy_in": "ACCEPT"})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestFirewallOptionsShowCmd(gf.kind, gf.ops.firewallShow),
				"show", "cluster1", "104")
			require.NoError(t, err)
			require.Contains(t, buf.String(), "ACCEPT")
		})
	}
}

// TestPveGuestFirewallOptionsUpdate_RequiresAChange asserts that `<kind>
// firewall options update` refuses to run with no flags set.
func TestPveGuestFirewallOptionsUpdate_RequiresAChange(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			_, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestFirewallOptionsUpdateCmd(gf.kind, gf.ops.firewallUpdate),
				"update", "cluster1", "104")
			require.Error(t, err)
			require.ErrorContains(t, err, "no changes given")
		})
	}
}

// TestPveGuestFirewallOptionsUpdate_SendsOnlyChangedFlags asserts that
// `<kind> firewall options update` forwards only explicitly-set flags.
func TestPveGuestFirewallOptionsUpdate_SendsOnlyChangedFlags(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatJSON, false)

			var rec recordedRequest
			recordJSON(f, "PUT /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/firewall/options", &rec, nil)

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestFirewallOptionsUpdateCmd(gf.kind, gf.ops.firewallUpdate),
				"update", "cluster1", "104", "--enable", "--policy-in", "ACCEPT")
			require.NoError(t, err)
			require.Equal(t, "1", rec.form.Get("enable"))
			require.Equal(t, "ACCEPT", rec.form.Get("policy_in"))
			require.Empty(t, rec.form.Get("dhcp"), "unset flags must not be sent")
		})
	}
}

// TestPveGuestFirewallRules_PreservesServerOrder asserts that `<kind>
// firewall rules` renders the position-ordered rule list in server order.
func TestPveGuestFirewallRules_PreservesServerOrder(t *testing.T) {
	for _, gf := range guestFixtures {
		t.Run(gf.kind.noun, func(t *testing.T) {
			f, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatTable, false)

			f.HandleJSON("GET /api2/json/pve/remotes/cluster1/"+gf.kind.noun+"/104/firewall/rules", []map[string]any{
				{"pos": 1, "type": "in", "action": "ACCEPT"},
				{"pos": 0, "type": "in", "action": "DROP"},
			})

			var buf bytes.Buffer
			err := run(deps, &buf, newPveGuestFirewallRulesCmd(gf.kind, gf.ops.firewallRules), "rules", "cluster1", "104")
			require.NoError(t, err)

			rows := buf.String()
			require.Less(t, strings.Index(rows, "ACCEPT"), strings.Index(rows, "DROP"),
				"firewall rules must preserve server (position) order, not be sorted")
		})
	}
}
