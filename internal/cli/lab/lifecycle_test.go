package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newLifecycleFakeClient returns a FakePVE and a constructed APIClient
// pointing at it. Distinct from resolve_test.go's config-only harness
// because list/status/start/stop all call deps.API.
func newLifecycleFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()
	f := testhelper.NewFakePVE(t)
	ac, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	return f, ac
}

// newLifecycleDeps builds a *cli.Deps for lifecycle tests: cfg written and
// reloaded through resolve_test.go's writeConfig helper, wired to ac, JSON
// output so assertions can decode the rendered body.
func newLifecycleDeps(t *testing.T, cfg *config.Config, ac *apiclient.APIClient) *cli.Deps {
	t.Helper()
	path := writeConfig(t, cfg)
	loaded, err := config.Load(path)
	require.NoError(t, err)
	return &cli.Deps{Cfg: loaded, ConfigPath: path, API: ac, Out: output.New(), Format: output.FormatJSON}
}

// execLifecycle wires deps onto cmd's context, executes it with args, and
// returns the captured stdout body alongside any error.
func execLifecycle(cmd *cobra.Command, deps *cli.Deps, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// lifecycleUPID is a well-formed UPID for node "pve1", used by start/stop
// tests so apiclient.WaitTask's polling resolves against a real-shaped node.
const lifecycleUPID = "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:"

// handleLifecycleTaskStatus registers a terminal "stopped/OK" task-status
// response on node so a blocking start/stop call completes immediately.
func handleLifecycleTaskStatus(f *testhelper.FakePVE, node, upid string) {
	f.HandleJSON("GET /api2/json/nodes/"+node+"/tasks/"+upid+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "OK",
		"upid":       upid,
	})
}

// handleClusterResources registers GET /cluster/resources to return entries,
// each a map with at least "vmid", "node", "pool", "status", and "type" set.
func handleClusterResources(f *testhelper.FakePVE, entries ...map[string]any) {
	f.HandleJSON("GET /api2/json/cluster/resources", entries)
}

// jsonTable is the decode shape of a list command's rendered JSON body.
type jsonTable struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// jsonMessage is the decode shape of a start/stop command's rendered JSON body.
type jsonMessage struct {
	Message string `json:"message"`
}

// --- classifyLabVMName / findLabVMs ----------------------------------------

func TestClassifyLabVMName_LegacyBareNameIsNodeZero(t *testing.T) {
	isQ, idx, ok := classifyLabVMName("lab-wayne", "wayne")
	require.True(t, ok)
	assert.False(t, isQ)
	assert.Equal(t, 0, idx)
}

func TestClassifyLabVMName_ExplicitNodeIndexes(t *testing.T) {
	for i := 0; i <= maxLabNodeIndex; i++ {
		isQ, idx, ok := classifyLabVMName(labNodeVMName("wayne", i), "wayne")
		require.True(t, ok, "index %d", i)
		assert.False(t, isQ)
		assert.Equal(t, i, idx)
	}
}

func TestClassifyLabVMName_Qdevice(t *testing.T) {
	isQ, _, ok := classifyLabVMName("lab-wayne-q", "wayne")
	require.True(t, ok)
	assert.True(t, isQ)
}

func TestClassifyLabVMName_UnrelatedName_NotOK(t *testing.T) {
	_, _, ok := classifyLabVMName("some-other-vm", "wayne")
	assert.False(t, ok)
}

func TestClassifyLabVMName_AnotherLabsNodeName_NotOK(t *testing.T) {
	// "lab-dennis-0" must not classify against lab "wayne" — cross-lab name
	// confusion would silently misattribute a pool member.
	_, _, ok := classifyLabVMName("lab-dennis-0", "wayne")
	assert.False(t, ok)
}

// TestFindLabVMs_ClassifiesThreeNodeCluster covers the 3-node shape: three
// pool members named by the explicit "-0".."-2" convention classify to
// their respective node indexes, and an absent node (3, 4) simply has no
// entry.
func TestFindLabVMs_ClassifiesThreeNodeCluster(t *testing.T) {
	vms := []labVM{
		{VMID: 100, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne-0"},
		{VMID: 101, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne-1"},
		{VMID: 102, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne-2"},
		{VMID: 999, Node: "pve1", Pool: "lab-other", Name: "lab-other-0"}, // different pool: excluded
	}

	classified, err := findLabVMs(vms, "lab-wayne", "wayne")
	require.NoError(t, err)
	require.Len(t, classified, 3)

	for i := 0; i < 3; i++ {
		vm, found := nodeLabVM(classified, i)
		require.True(t, found, "node %d", i)
		assert.Equal(t, int64(100+i), vm.VMID)
	}
	_, found := nodeLabVM(classified, 3)
	assert.False(t, found)
	_, found = qdeviceLabVM(classified)
	assert.False(t, found)
}

// TestFindLabVMs_ClassifiesTwoNodePlusQdevice covers the 2+QDevice shape:
// two node VMs plus a QDevice VM, all correctly classified.
func TestFindLabVMs_ClassifiesTwoNodePlusQdevice(t *testing.T) {
	vms := []labVM{
		{VMID: 200, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne-0"},
		{VMID: 201, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne-1"},
		{VMID: 202, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne-q"},
	}

	classified, err := findLabVMs(vms, "lab-wayne", "wayne")
	require.NoError(t, err)
	require.Len(t, classified, 3)

	vm0, ok := nodeLabVM(classified, 0)
	require.True(t, ok)
	assert.Equal(t, int64(200), vm0.VMID)

	vm1, ok := nodeLabVM(classified, 1)
	require.True(t, ok)
	assert.Equal(t, int64(201), vm1.VMID)

	q, ok := qdeviceLabVM(classified)
	require.True(t, ok)
	assert.Equal(t, int64(202), q.VMID)
}

// TestFindLabVMs_LegacyBareNameClassifiesAsNodeZero covers the back-compat
// path: a single legacy-named VM (no index suffix) in the pool classifies
// as node 0, exactly like an explicit "-0"-named VM would.
func TestFindLabVMs_LegacyBareNameClassifiesAsNodeZero(t *testing.T) {
	vms := []labVM{
		{VMID: 300, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne"},
	}

	classified, err := findLabVMs(vms, "lab-wayne", "wayne")
	require.NoError(t, err)
	require.Len(t, classified, 1)

	vm, ok := nodeLabVM(classified, 0)
	require.True(t, ok)
	assert.Equal(t, int64(300), vm.VMID)
}

// TestFindLabVMs_UnclassifiableName_Errors covers a pool member whose name
// matches none of the lab's node/QDevice naming conventions: this is a data
// problem the operator must resolve, not a case findLabVMs guesses past.
func TestFindLabVMs_UnclassifiableName_Errors(t *testing.T) {
	vms := []labVM{
		{VMID: 400, Node: "pve1", Pool: "lab-wayne", Name: "totally-unrelated"},
	}

	_, err := findLabVMs(vms, "lab-wayne", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "totally-unrelated")
	assert.ErrorContains(t, err, "400")
}

// TestFindLabVMs_DuplicateRoleClaim_Errors covers two pool members that both
// classify to node 0 (a legacy-named VM alongside an explicit "-0"-named
// VM): ambiguous, and must error rather than silently picking one.
func TestFindLabVMs_DuplicateRoleClaim_Errors(t *testing.T) {
	vms := []labVM{
		{VMID: 500, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne"},
		{VMID: 501, Node: "pve1", Pool: "lab-wayne", Name: "lab-wayne-0"},
	}

	_, err := findLabVMs(vms, "lab-wayne", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "500")
	assert.ErrorContains(t, err, "501")
}

// --- list -------------------------------------------------------------

func TestList_VMPresentAndAbsent(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "running",
		"type":   "qemu",
	})

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
			"beta":  cleanLab("beta"),
		},
	}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newListCmd(), deps)
	require.NoError(t, err)

	var table jsonTable
	require.NoError(t, json.Unmarshal([]byte(body), &table))
	assert.Equal(t, []string{"NAME", "POOL", "NODES", "VMID", "NODE", "STATUS"}, table.Headers)
	require.Len(t, table.Rows, 2)

	byName := map[string][]string{}
	for _, row := range table.Rows {
		byName[row[0]] = row
	}

	assert.Equal(t, []string{"alpha", "lab-alpha", "1", "100", "pve1", "running"}, byName["alpha"])
	assert.Equal(t, []string{"beta", "lab-beta", "1", "", "", "absent"}, byName["beta"])
}

func TestList_NoLabsConfigured(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f)

	deps := newLifecycleDeps(t, &config.Config{}, ac)

	body, err := execLifecycle(newListCmd(), deps)
	require.NoError(t, err)

	var table jsonTable
	require.NoError(t, json.Unmarshal([]byte(body), &table))
	assert.Empty(t, table.Rows)
}

// --- status -------------------------------------------------------------

func TestStatus_RunningVMShowsPowerIPAndConfig(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "running",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "running",
		"vmid":   100,
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces", map[string]any{
		"result": []any{
			map[string]any{
				"name": "lo",
				"ip-addresses": []any{
					map[string]any{"ip-address": "127.0.0.1", "ip-address-type": "ipv4"},
				},
			},
			map[string]any{
				"name": "eth0",
				"ip-addresses": []any{
					map[string]any{"ip-address": "10.10.1.50", "ip-address-type": "ipv4"},
				},
			},
		},
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/config", map[string]any{
		"cores":   4,
		"sockets": 1,
		"memory":  "8192",
		"scsi0":   "tank:vm-100-disk-0,size=64G",
	})

	lab := cleanLab("alpha")
	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": lab}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStatusCmd(), deps, "alpha")
	require.NoError(t, err)

	var table jsonTable
	require.NoError(t, json.Unmarshal([]byte(body), &table))
	assert.Equal(t, []string{"NODE", "VMID", "PVE_NODE", "STATUS", "IP", "AGENT", "VCPU", "MEMORY_MAX_GB", "WARNING"}, table.Headers)
	require.Len(t, table.Rows, 2, "one node row plus a trailing summary row (single-node/default topology)")

	row := table.Rows[0]
	assert.Equal(t, "0", row[0], "node label")
	assert.Equal(t, "100", row[1], "vmid")
	assert.Equal(t, "pve1", row[2], "pve node")
	assert.Equal(t, "running", row[3], "status")
	assert.Equal(t, "10.10.1.50", row[4], "guest-agent-reported IP")
	assert.Equal(t, "ok", row[5], "agent")
	assert.Equal(t, "16", row[6], "vcpu falls back to the single-node profile default")
	assert.Equal(t, "128", row[7], "memory_max_gb falls back to the single-node profile default")

	summary := table.Rows[1]
	assert.Equal(t, "summary", summary[0])
	assert.Contains(t, summary[len(summary)-1], `"alpha"`,
		"the summary line must be rendered as a row, since output.Result.Message is dropped whenever Rows is also set")
}

func TestStatus_VMAbsentIsConfigOnlyAndSucceeds(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f) // no live VMs at all

	lab := cleanLab("beta")
	lab.Compute.VCPU = 8
	lab.Compute.Memory.MinGB = 16
	lab.Compute.Memory.MaxGB = 64
	lab.Storage.OSDiskGB = 32
	lab.Storage.DataDiskGB = 200

	cfg := &config.Config{Labs: map[string]*config.Lab{"beta": lab}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStatusCmd(), deps, "beta")
	require.NoError(t, err, "an absent VM must not be treated as a command error")

	var table jsonTable
	require.NoError(t, json.Unmarshal([]byte(body), &table))
	require.Len(t, table.Rows, 2, "one node row plus a trailing summary row")

	row := table.Rows[0]
	assert.Equal(t, "0", row[0], "node label")
	assert.Equal(t, "", row[1], "no VMID when absent")
	assert.Equal(t, "", row[2], "no pve node when absent")
	assert.Equal(t, "absent", row[3], "status")
	assert.Equal(t, "n/a", row[5], "agent")
	assert.Equal(t, "8", row[6], "vcpu reflects the lab's configured override")
	assert.Equal(t, "64", row[7], "memory_max_gb reflects the lab's configured override")
}

func TestStatus_UnknownLab(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f)
	deps := newLifecycleDeps(t, &config.Config{}, ac)

	_, err := execLifecycle(newStatusCmd(), deps, "ghost")
	require.Error(t, err)
	assert.ErrorContains(t, err, `lab "ghost" not found`)
}

// --- start --------------------------------------------------------------

func TestStart_StoppedVMStartsAndWaits(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "stopped",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "stopped",
		"vmid":   100,
	})

	startCalled := false
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(w http.ResponseWriter, _ *http.Request) {
		startCalled = true
		testhelper.WriteData(w, lifecycleUPID)
	})
	handleLifecycleTaskStatus(f, "pve1", lifecycleUPID)

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStartCmd(), deps, "alpha")
	require.NoError(t, err)
	assert.True(t, startCalled, "start endpoint must be called for a stopped VM")

	var msg jsonMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg))
	assert.Contains(t, msg.Message, "started")
}

func TestStart_NullTaskResponseIsImmediateSuccess(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "stopped",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "stopped",
		"vmid":   100,
	})

	// An older server answering `data: null` instead of a UPID: the start
	// already happened server-side, so the command must report success and
	// never poll a task. No task-status route is registered — a poll attempt
	// would 501 and fail the command.
	startCalled := false
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(w http.ResponseWriter, _ *http.Request) {
		startCalled = true
		testhelper.WriteData(w, nil)
	})

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStartCmd(), deps, "alpha")
	require.NoError(t, err)
	assert.True(t, startCalled, "start endpoint must be called for a stopped VM")

	var msg jsonMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg))
	assert.Contains(t, msg.Message, "started")
}

func TestStart_AlreadyRunningIsNoop(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "running",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "running",
		"vmid":   100,
	})
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("start must not be called when the VM is already running")
	})

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStartCmd(), deps, "alpha")
	require.NoError(t, err)

	var msg jsonMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg))
	assert.Contains(t, msg.Message, "already running")
}

func TestStart_DryRunMakesNoCalls(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "stopped",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "stopped",
		"vmid":   100,
	})
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("--dry-run must not call the start endpoint")
	})

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStartCmd(), deps, "alpha", "--dry-run")
	require.NoError(t, err)

	var msg jsonMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg))
	assert.Contains(t, msg.Message, "dry-run")
	assert.Contains(t, msg.Message, "start")
}

func TestStart_NoVMFoundErrors(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f) // pool has no qemu member yet

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	_, err := execLifecycle(newStartCmd(), deps, "alpha")
	require.Error(t, err)
	assert.ErrorContains(t, err, "no VM found")
}

func TestStart_PeppiGuardRefusesBeforeStatusCall(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   50020,
		"node":   "pve1",
		"pool":   "lab-dirty",
		"name":   "lab-dirty",
		"status": "stopped",
		"type":   "qemu",
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/50020/status/current", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("peppi guard must refuse before the status call is ever made")
	})

	dirty := cleanLab("dirty")
	cfg := &config.Config{Labs: map[string]*config.Lab{"dirty": dirty}}
	deps := newLifecycleDeps(t, cfg, ac)

	_, err := execLifecycle(newStartCmd(), deps, "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "50020")
}

// --- stop -----------------------------------------------------------------

func TestStop_RunningVMStopsAndWaits(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "running",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "running",
		"vmid":   100,
	})

	stopCalled := false
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(w http.ResponseWriter, _ *http.Request) {
		stopCalled = true
		testhelper.WriteData(w, lifecycleUPID)
	})
	handleLifecycleTaskStatus(f, "pve1", lifecycleUPID)

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStopCmd(), deps, "alpha")
	require.NoError(t, err)
	assert.True(t, stopCalled, "stop endpoint must be called for a running VM")

	var msg jsonMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg))
	assert.Contains(t, msg.Message, "stopped")
}

func TestStop_AlreadyStoppedIsNoop(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "stopped",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "stopped",
		"vmid":   100,
	})
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("stop must not be called when the VM is already stopped")
	})

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStopCmd(), deps, "alpha")
	require.NoError(t, err)

	var msg jsonMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg))
	assert.Contains(t, msg.Message, "already stopped")
}

func TestStop_DryRunMakesNoCalls(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "running",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "running",
		"vmid":   100,
	})
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("--dry-run must not call the stop endpoint")
	})

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStopCmd(), deps, "alpha", "--dry-run")
	require.NoError(t, err)

	var msg jsonMessage
	require.NoError(t, json.Unmarshal([]byte(body), &msg))
	assert.Contains(t, msg.Message, "dry-run")
	assert.Contains(t, msg.Message, "stop")
}

func TestStop_NoVMFoundErrors(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f)

	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	deps := newLifecycleDeps(t, cfg, ac)

	_, err := execLifecycle(newStopCmd(), deps, "alpha")
	require.Error(t, err)
	assert.ErrorContains(t, err, "no VM found")
}

func TestStop_PeppiGuardRefusesBeforeStatusCall(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   50020,
		"node":   "pve1",
		"pool":   "lab-dirty",
		"name":   "lab-dirty",
		"status": "running",
		"type":   "qemu",
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/50020/status/current", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("peppi guard must refuse before the status call is ever made")
	})

	dirty := cleanLab("dirty")
	cfg := &config.Config{Labs: map[string]*config.Lab{"dirty": dirty}}
	deps := newLifecycleDeps(t, cfg, ac)

	_, err := execLifecycle(newStopCmd(), deps, "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "50020")
}
