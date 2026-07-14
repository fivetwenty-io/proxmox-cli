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

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
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

// jsonSingle is the decode shape of a status command's rendered JSON body.
type jsonSingle struct {
	Data map[string]string `json:"data"`
}

// jsonMessage is the decode shape of a start/stop command's rendered JSON body.
type jsonMessage struct {
	Message string `json:"message"`
}

// --- list -------------------------------------------------------------

func TestList_VMPresentAndAbsent(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
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
	assert.Equal(t, []string{"NAME", "POOL", "VMID", "NODE", "STATUS"}, table.Headers)
	require.Len(t, table.Rows, 2)

	byName := map[string][]string{}
	for _, row := range table.Rows {
		byName[row[0]] = row
	}

	assert.Equal(t, []string{"alpha", "lab-alpha", "100", "pve1", "running"}, byName["alpha"])
	assert.Equal(t, []string{"beta", "lab-beta", "", "", "absent"}, byName["beta"])
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

	var single jsonSingle
	require.NoError(t, json.Unmarshal([]byte(body), &single))
	assert.Equal(t, "running", single.Data["STATUS"])
	assert.Equal(t, "100", single.Data["VMID"])
	assert.Equal(t, "pve1", single.Data["NODE"])
	assert.Equal(t, "10.10.1.50", single.Data["IP"])
	assert.Equal(t, "4", single.Data["CORES"])
	assert.Equal(t, "1", single.Data["SOCKETS"])
	assert.Equal(t, "8192", single.Data["MEMORY"])
	assert.Equal(t, "tank:vm-100-disk-0,size=64G", single.Data["SCSI0"])
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

	var single jsonSingle
	require.NoError(t, json.Unmarshal([]byte(body), &single))
	assert.Equal(t, "absent", single.Data["STATUS"])
	assert.Equal(t, "8", single.Data["VCPU"])
	assert.Equal(t, "16", single.Data["MEMORY_MIN_GB"])
	assert.Equal(t, "64", single.Data["MEMORY_MAX_GB"])
	assert.Equal(t, "32", single.Data["OS_DISK_GB"])
	assert.Equal(t, "200", single.Data["DATA_DISK_GB"])
	// No live fields should be present when the VM was never located.
	_, hasVMID := single.Data["VMID"]
	assert.False(t, hasVMID)
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
