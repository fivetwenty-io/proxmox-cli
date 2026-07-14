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

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
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

func TestDestroy_HappyPathWithYes_StopsAndDeletesVMInOrder(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var calls []string
	f.HandleFunc("GET /api2/json/pools/lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "GetPools")
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				{"id": "qemu/100", "node": "pve1", "type": "qemu", "vmid": 100},
			},
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

	assert.Equal(t, []string{"GetPools", "Status", "Stop", "Delete"}, calls)
	assert.Contains(t, stdout.String(), "destroyed")
}

func TestDestroy_HappyPathWithPurge_AlsoDeletesPoolAndStorage(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var calls []string
	f.HandleFunc("GET /api2/json/pools/lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "GetPools")
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				{"id": "qemu/100", "node": "pve1", "type": "qemu", "vmid": 100},
			},
		})
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
	assert.Equal(t, []string{"GetPools", "Status", "Delete", "DeletePool", "DeleteStorage"}, calls)
	assert.Contains(t, stdout.String(), "destroyed")
}

func TestDestroy_RefusesWithoutYesNonInteractively(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	var mutatingCalls int
	f.HandleFunc("GET /api2/json/pools/lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				{"id": "qemu/100", "node": "pve1", "type": "qemu", "vmid": 100},
			},
		})
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
	f.HandleFunc("GET /api2/json/pools/lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				{"id": "qemu/100", "node": "pve1", "type": "qemu", "vmid": 100},
			},
		})
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
	f.HandleFunc("GET /api2/json/pools/lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				// 50010 is a peppi-protected production VMID.
				{"id": "qemu/50010", "node": "pve1", "type": "qemu", "vmid": 50010},
			},
		})
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

	f.HandleFunc("GET /api2/json/pools/lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "pool 'lab-alpha' does not exist")
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "alpha", "--yes")
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "nothing to destroy")
}

func TestDestroy_PurgeIdempotentWhenPoolAndStorageAlreadyGone(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")},
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)

	f.HandleFunc("GET /api2/json/pools/lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "pool 'lab-alpha' does not exist")
	})
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "pool 'lab-alpha' does not exist")
	})
	f.HandleFunc("DELETE /api2/json/storage/tank-lab-alpha", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "storage 'tank-lab-alpha' does not exist")
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	stdout, _, err := destroyRun(t, cmd, "alpha", "--yes", "--purge")
	require.NoError(t, err, "absent pool/storage must be reported as already gone, not errors")

	assert.Contains(t, stdout.String(), "destroyed")
}
