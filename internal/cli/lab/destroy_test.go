package lab

import (
	"bytes"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// destroyFakeClient returns a FakePVE and a constructed APIClient pointing at
// it, mirroring the qemu package's own newFakeClient helper. Named distinctly
// (destroy-prefixed) since several other lab command test files build the
// same kind of fake concurrently and package-level names must not collide.
func destroyFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()

	f := testhelper.NewFakePVE(t)

	u, err := url.Parse(f.BaseURL())
	require.NoError(t, err)
	host, portStr, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	opts := f.Options
	opts.Host = host
	opts.Port = port

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return f, ac
}

// destroyTestCmd builds `pmx lab destroy` as a standalone cobra.Command
// carrying a *cli.Deps loaded from configPath (via the shared newCmdWithDeps
// helper) with api and node additionally wired in, ready for
// cmd.SetArgs/cmd.Execute.
func destroyTestCmd(t *testing.T, configPath string, api *apiclient.APIClient, node string) *cobra.Command {
	t.Helper()

	base := newCmdWithDeps(t, configPath)
	deps := cli.GetDeps(base)
	deps.API = api
	deps.Node = node
	deps.Out = output.New()
	deps.Format = output.FormatPlain

	cmd := newDestroyCmd()
	cmd.SetContext(base.Context())
	return cmd
}

// destroyHandleTaskStatus registers a terminal "stopped/OK" task-status
// response for upid on node, so a blocking apiclient.WaitTask call
// completes immediately instead of polling.
func destroyHandleTaskStatus(f *testhelper.FakePVE, node, upid string) {
	f.HandleJSON("GET /api2/json/nodes/"+node+"/tasks/"+upid+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "OK",
		"upid":       upid,
	})
}

// destroyRun executes cmd with args, capturing stdout/stderr into the
// returned buffers and feeding stdin an empty reader (non-interactive) unless
// overridden by the caller beforehand.
func destroyRun(t *testing.T, cmd *cobra.Command, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if cmd.InOrStdin() == nil {
		cmd.SetIn(strings.NewReader(""))
	}
	cmd.SetArgs(args)
	err = cmd.Execute()
	return stdout, stderr, err
}

// destroyHandleClusterResources registers GET /cluster/resources to return
// entries, mirroring lifecycle_test.go's handleClusterResources (a distinct
// name to avoid a package-level collision with that file's own helper).
func destroyHandleClusterResources(f *testhelper.FakePVE, entries ...map[string]any) {
	f.HandleJSON("GET /api2/json/cluster/resources", entries)
}

// buildDestroyCmdWithContext builds a destroy command whose deps.Cfg already
// carries a lab-<name> pmx context (a minimal token context that satisfies
// config.Load's lenient validation) plus a clean lab config, and registers an
// empty cluster/resources pool so the run resolves to "nothing to destroy" —
// the no-op path that still must trigger context/keychain cleanup on success.
func buildDestroyCmdWithContext(t *testing.T, name string) (*cobra.Command, *cli.Deps) {
	t.Helper()

	cfg := &config.Config{
		Labs: map[string]*config.Lab{name: cleanLab(name)},
		Contexts: map[string]*config.Context{
			"lab-" + name: {
				Host:     "10.10.1.10",
				Port:     8006,
				Protocol: "https",
				Product:  config.ProductPVE,
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "pmx@pve",
					TokenID:  "pmx",
					Secret:   "keychain:pmx-lab-" + name + "/pmx@pve!pmx",
				},
			},
		},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/resources", []any{})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	deps := cli.GetDeps(cmd)
	return cmd, deps
}

func TestDestroy_RemovesContextAndSecret(t *testing.T) {
	var deletedService, deletedAccount string
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(service, account string) error {
		deletedService, deletedAccount = service, account
		return nil
	}
	t.Cleanup(func() { labDeleteSecretFn = orig })

	cmd, deps := buildDestroyCmdWithContext(t, "demo")
	_, _, err := destroyRun(t, cmd, "demo", "--yes")
	require.NoError(t, err)

	assert.Nil(t, deps.Cfg.Contexts["lab-demo"], "context must be removed")
	assert.Equal(t, "pmx-lab-demo", deletedService)
	assert.Equal(t, "pmx@pve!pmx", deletedAccount)
}

func TestDestroy_KeepContextFlag_PreservesContext(t *testing.T) {
	orig := labDeleteSecretFn
	called := false
	labDeleteSecretFn = func(string, string) error { called = true; return nil }
	t.Cleanup(func() { labDeleteSecretFn = orig })

	cmd, deps := buildDestroyCmdWithContext(t, "demo")
	_, _, err := destroyRun(t, cmd, "demo", "--yes", "--keep-context")
	require.NoError(t, err)

	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"], "--keep-context must preserve the context")
	assert.False(t, called, "--keep-context must not touch the keychain")
}

func TestDestroy_HappyPathWithYes_StopsAndDeletesVMInOrder(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var calls []string
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "ClusterResources")
		testhelper.WriteData(w, []map[string]any{
			{"vmid": 100, "node": "pve1", "pool": "lab-alpha", "name": "lab-alpha", "status": "running", "type": "qemu"},
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "Status")
		testhelper.WriteData(w, map[string]any{"status": "running", "vmid": 100})
	})
	stopUPID := "UPID:pve1:00000001:00000001:65000000:qmstop:100:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "Stop")
		testhelper.WriteData(w, stopUPID)
	})
	destroyHandleTaskStatus(f, "pve1", stopUPID)
	deleteUPID := "UPID:pve1:00000002:00000002:65000000:qmdestroy:100:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "Delete")
		testhelper.WriteData(w, deleteUPID)
	})
	destroyHandleTaskStatus(f, "pve1", deleteUPID)

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "alpha", "--yes")
	require.NoError(t, err)

	assert.Equal(t, []string{"ClusterResources", "Status", "Stop", "Delete"}, calls)
	assert.Contains(t, stdout.String(), "destroyed")
}

func TestDestroy_HappyPathWithPurge_AlsoDeletesPoolAndStorage(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var calls []string
	destroyHandleClusterResources(f, map[string]any{
		"vmid": 100, "node": "pve1", "pool": "lab-alpha", "name": "lab-alpha", "status": "stopped", "type": "qemu",
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "Status")
		testhelper.WriteData(w, map[string]any{"status": "stopped", "vmid": 100})
	})
	deleteUPID := "UPID:pve1:00000002:00000002:65000000:qmdestroy:100:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "Delete")
		testhelper.WriteData(w, deleteUPID)
	})
	destroyHandleTaskStatus(f, "pve1", deleteUPID)
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "DeletePool")
		testhelper.WriteData(w, nil)
	})
	f.HandleFunc("DELETE /api2/json/storage/tank-lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "DeleteStorage")
		testhelper.WriteData(w, nil)
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "alpha", "--yes", "--purge")
	require.NoError(t, err)

	// The VM was already stopped, so no Stop call is expected between Status
	// and Delete.
	assert.Equal(t, []string{"Status", "Delete", "DeletePool", "DeleteStorage"}, calls)
	assert.Contains(t, stdout.String(), "destroyed")
}

func TestDestroy_RefusesWithoutYesNonInteractively(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var mutatingCalls int
	destroyHandleClusterResources(f, map[string]any{
		"vmid": 100, "node": "pve1", "pool": "lab-alpha", "name": "lab-alpha", "status": "running", "type": "qemu",
	})
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	// Empty stdin simulates a non-interactive invocation: the confirmation
	// read hits EOF immediately and must be treated as "no", not a hang.
	cmd.SetIn(strings.NewReader(""))
	stdout, _, err := destroyRun(t, cmd, "alpha")
	require.NoError(t, err)

	assert.Zero(t, mutatingCalls, "must refuse to mutate without confirmation")
	assert.Contains(t, stdout.String(), "Aborted")
}

func TestDestroy_DryRun_NoMutatingCallsAndPreviewsDoomedResources(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var mutatingCalls int
	destroyHandleClusterResources(f, map[string]any{
		"vmid": 100, "node": "pve1", "pool": "lab-alpha", "name": "lab-alpha", "status": "running", "type": "qemu",
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++ // never reached in a dry-run; any call here is a bug
	})
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})
	f.HandleFunc("DELETE /api2/json/storage/tank-lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "alpha", "--dry-run", "--purge")
	require.NoError(t, err)

	assert.Zero(t, mutatingCalls, "dry-run must never mutate")
	out := stdout.String()
	assert.Contains(t, out, "dry-run")
	assert.Contains(t, out, "VM 100")
	assert.Contains(t, out, `pool "lab-alpha"`)
	assert.Contains(t, out, `storage "tank-lab-alpha"`)
}

func TestDestroy_PeppiRefusesProtectedVMIDBeforeAnyMutation(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var mutatingCalls int
	destroyHandleClusterResources(f, map[string]any{
		// 50010 is a peppi-protected production VMID.
		"vmid": 50010, "node": "pve1", "pool": "lab-alpha", "name": "lab-alpha", "status": "running", "type": "qemu",
	})
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/50010/status/stop", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/50010", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	_, _, err := destroyRun(t, cmd, "alpha", "--yes")

	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "50010")
	assert.Zero(t, mutatingCalls, "must refuse before any mutating call")
}

func TestDestroy_PeppiRefusesAtResolveForProtectedPool(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.Access.Pool = "peppiprd-pool"
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"dirty": dirty},
	}
	path := writeConfig(t, cfg)

	// No API client wired at all: resolveLabForMutate must refuse before
	// deps.API is ever touched, so a nil API is safe here.
	cmd := destroyTestCmd(t, path, nil, "pve1")
	_, _, err := destroyRun(t, cmd, "dirty", "--yes")

	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppiprd")
}

func TestDestroy_IdempotentWhenVMAlreadyAbsent(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	destroyHandleClusterResources(f) // no live VMs at all

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "alpha", "--yes")
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "nothing to destroy")
}

// TestDestroy_PurgeIdempotentWhenPoolAndStorageAlreadyGone covers F8's fix
// against a real PVE 9 response: DELETE /pools and DELETE
// /storage/{storage} both signal "already gone" with a bare HTTP 500 whose
// body is the literal Perl die string ("pool '<id>' does not exist" /
// "storage '<id>' does not exist"), never an HTTP 404 — confirmed live
// against PVE 9 (F8). destroyDeletePool/destroyDeleteStorage must still
// treat this as already-gone, not a fatal error.
func TestDestroy_PurgeIdempotentWhenPoolAndStorageAlreadyGone(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	destroyHandleClusterResources(f) // no live VMs at all
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteErrorText(w, http.StatusInternalServerError, "pool 'lab-alpha' does not exist")
	})
	f.HandleFunc("DELETE /api2/json/storage/tank-lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteErrorText(w, http.StatusInternalServerError, "storage 'tank-lab-alpha' does not exist")
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "alpha", "--yes", "--purge")
	require.NoError(t, err, "absent pool/storage must be reported as already gone, not errors")

	assert.Contains(t, stdout.String(), "destroyed")
}

// TestDestroy_PurgeFailsOnGenuinePoolDeleteError covers the flip side of
// isPoolNotFound: a 500 that is NOT the "pool '<id>' does not exist"
// message (e.g. a lock timeout) must still abort destroy, not be swallowed
// as though the pool were already gone.
func TestDestroy_PurgeFailsOnGenuinePoolDeleteError(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	destroyHandleClusterResources(f) // no live VMs at all
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteErrorText(w, http.StatusInternalServerError, "unable to acquire lock 'file' - got timeout")
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	_, _, err := destroyRun(t, cmd, "alpha", "--yes", "--purge")
	require.Error(t, err, "a genuine pool-delete failure must abort destroy, not be treated as already-gone")
	assert.Contains(t, err.Error(), "delete pool")
}

// TestDestroy_PurgeFailsOnGenuineStorageDeleteError mirrors
// TestDestroy_PurgeFailsOnGenuinePoolDeleteError for storage deletion: a 500
// naming a DIFFERENT storage ID as missing must not be misread as this
// lab's own storage already being gone.
func TestDestroy_PurgeFailsOnGenuineStorageDeleteError(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	destroyHandleClusterResources(f) // no live VMs at all
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteErrorText(w, http.StatusInternalServerError, "pool 'lab-alpha' does not exist")
	})
	f.HandleFunc("DELETE /api2/json/storage/tank-lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteErrorText(w, http.StatusInternalServerError, "storage 'tank-lab-other' does not exist")
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	_, _, err := destroyRun(t, cmd, "alpha", "--yes", "--purge")
	require.Error(t, err, "a 500 naming a different storage ID must not be classified as this lab's storage already being gone")
	assert.Contains(t, err.Error(), "delete storage")
}

// TestDestroy_ThreeNodeCluster_DestroysInReverseOrder covers M2-02's
// multi-node acceptance shape: a 3-node lab's VMs are stopped and deleted
// in reverse start order (node 2, then node 1, then node 0), not pool
// iteration order.
func TestDestroy_ThreeNodeCluster_DestroysInReverseOrder(t *testing.T) {
	lab := cleanLab("pve-cpi")
	lab.Topology = config.LabTopology{Nodes: 3}
	cfg := &config.Config{Labs: map[string]*config.Lab{"pve-cpi": lab}}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var order []string
	destroyHandleClusterResources(f,
		map[string]any{"vmid": 100, "node": "pve1", "pool": "lab-pve-cpi", "name": "lab-pve-cpi-0", "status": "stopped", "type": "qemu"},
		map[string]any{"vmid": 101, "node": "pve1", "pool": "lab-pve-cpi", "name": "lab-pve-cpi-1", "status": "stopped", "type": "qemu"},
		map[string]any{"vmid": 102, "node": "pve1", "pool": "lab-pve-cpi", "name": "lab-pve-cpi-2", "status": "stopped", "type": "qemu"},
	)
	// Every route (including each VMID's task-status route) is registered
	// up front, before destroyRun executes: registering a route from inside
	// another route's handler would deadlock, since FakePVE's router holds
	// its RWMutex's read lock for the whole duration of the handler it
	// dispatches to, and registration takes that same mutex's write lock.
	for _, vmid := range []string{"100", "101", "102"} {
		vmid := vmid
		deleteUPID := "UPID:pve1:00000000:00000000:65000000:qmdestroy:" + vmid + ":root@pam:"
		f.HandleFunc("GET /api2/json/nodes/pve1/qemu/"+vmid+"/status/current", func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{"status": "stopped", "vmid": vmid})
		})
		f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/"+vmid, func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "delete-"+vmid)
			testhelper.WriteData(w, deleteUPID)
		})
		destroyHandleTaskStatus(f, "pve1", deleteUPID)
	}

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "pve-cpi", "--yes")
	require.NoError(t, err)

	assert.Equal(t, []string{"delete-102", "delete-101", "delete-100"}, order,
		"destroy must proceed from the highest node index down to node 0")
	assert.Contains(t, stdout.String(), "destroyed")
}

// TestDestroy_TwoNodePlusQdevice_DestroysQdeviceFirst covers the QDevice
// ordering half of M2-02: the QDevice VM is destroyed before either node
// VM, mirroring lifecycle.go's stop sequencing.
func TestDestroy_TwoNodePlusQdevice_DestroysQdeviceFirst(t *testing.T) {
	lab := cleanLab("wayne")
	lab.Topology = config.LabTopology{Nodes: 2}
	cfg := &config.Config{Labs: map[string]*config.Lab{"wayne": lab}}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var order []string
	destroyHandleClusterResources(f,
		map[string]any{"vmid": 200, "node": "pve1", "pool": "lab-wayne", "name": "lab-wayne-0", "status": "stopped", "type": "qemu"},
		map[string]any{"vmid": 201, "node": "pve1", "pool": "lab-wayne", "name": "lab-wayne-1", "status": "stopped", "type": "qemu"},
		map[string]any{"vmid": 202, "node": "pve1", "pool": "lab-wayne", "name": "lab-wayne-q", "status": "stopped", "type": "qemu"},
	)
	// Every route (including each VMID's task-status route) is registered
	// up front, before destroyRun executes: see the identical note in
	// TestDestroy_ThreeNodeCluster_DestroysInReverseOrder.
	for _, vmid := range []string{"200", "201", "202"} {
		vmid := vmid
		deleteUPID := "UPID:pve1:00000000:00000000:65000000:qmdestroy:" + vmid + ":root@pam:"
		f.HandleFunc("GET /api2/json/nodes/pve1/qemu/"+vmid+"/status/current", func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{"status": "stopped", "vmid": vmid})
		})
		f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/"+vmid, func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "delete-"+vmid)
			testhelper.WriteData(w, deleteUPID)
		})
		destroyHandleTaskStatus(f, "pve1", deleteUPID)
	}

	cmd := destroyTestCmd(t, path, ac, "pve1")
	_, _, err := destroyRun(t, cmd, "wayne", "--yes")
	require.NoError(t, err)

	assert.Equal(t, []string{"delete-202", "delete-201", "delete-200"}, order,
		"the QDevice (202) must be destroyed first, then node 1, then node 0")
}

// TestDestroy_UnclassifiablePoolMember_RefusesLoudly covers the other half
// of M2-02: a pool member whose live name matches none of the node/QDevice
// naming convention refuses the whole command before any mutating call,
// rather than silently ignoring it or guessing which VM it corresponds to.
func TestDestroy_UnclassifiablePoolMember_RefusesLoudly(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var mutatingCalls int
	destroyHandleClusterResources(f, map[string]any{
		"vmid": 100, "node": "pve1", "pool": "lab-alpha", "name": "totally-unrelated", "status": "running", "type": "qemu",
	})
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, _ *http.Request) {
		mutatingCalls++
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	_, _, err := destroyRun(t, cmd, "alpha", "--yes")

	require.Error(t, err)
	assert.ErrorContains(t, err, "totally-unrelated")
	assert.Zero(t, mutatingCalls, "must refuse before any mutating call")
}
