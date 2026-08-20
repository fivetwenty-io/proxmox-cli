package lab

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestLabCephInstall_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "install", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephInstall_TwoNodeLab_Refuses(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "install", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "at least a 3-node lab")
	assert.Empty(t, fake.Calls)
}

func TestLabCephInstall_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	out, err := runGuestCmd(t, cmd, "install", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "[dry-run]")
	require.Empty(t, fake.Calls, "dry-run must never invoke the runner")
}

func TestLabCephInstall_HappyPath_InstallsOnAllThree(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: "absent"}, // 0: probe node 0
		exec.FakeResponse{},                 // 1: install node 0
		exec.FakeResponse{Stdout: "absent"}, // 2: probe node 1
		exec.FakeResponse{},                 // 3: install node 1
		exec.FakeResponse{Stdout: "absent"}, // 4: probe node 2
		exec.FakeResponse{},                 // 5: install node 2
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "install", "wayne")
	require.NoError(t, err)

	assert.Contains(t, out, "install node 0")
	assert.Contains(t, out, "install node 1")
	assert.Contains(t, out, "install node 2")
	assert.Contains(t, out, "installed")

	require.Len(t, fake.Calls, 6)
	installCmd := fake.Calls[1].Args[len(fake.Calls[1].Args)-1]
	assert.True(t, strings.HasSuffix(installCmd, "pveceph install --repository no-subscription -y"),
		"the install command must end with the mandatory -y flag, got %q", installCmd)
}

func TestLabCephInstall_AlreadyInstalled_Skips(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: "installed"}, // 0: probe node 0 -> already installed, no install call
		exec.FakeResponse{Stdout: "installed"}, // 1: probe node 1
		exec.FakeResponse{Stdout: "installed"}, // 2: probe node 2
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "install", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "already installed")

	require.Len(t, fake.Calls, 3, "no second (install) call must run per node once already installed")
}

func TestLabCephInstall_TransportFailure_Aborts(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	fake := exec.Fake(
		exec.FakeResponse{Err: errors.New("ssh: connect to host 10.10.1.10 port 22: no route to host")},
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "install", "wayne")
	require.Error(t, err)
	require.Len(t, fake.Calls, 1)
}

// --- init / mon / mgr -------------------------------------------------------

// cephTestLab returns a 3+-node lab whose network.cidr (the lab /16) and
// mgmt.subnet (the node-addressing /24) are deliberately distinct: `ceph
// init` must POST the /16 (network.cidr), never the /24 labMgmtCIDR derives
// node IPs from, and a fixture where both happened to share one value would
// not catch the two being confused.
func cephTestLab(name string, nodes int) *config.Lab {
	lab := multiNodeTestLab(name, nodes, "")
	lab.Network.CIDR = "10.252.0.0/16"
	return lab
}

// cephTaskUPID returns a well-formed UPID string for a task running on node,
// matching node/ceph_test.go's cephUPID convention.
func cephTaskUPID(node string) string {
	return fmt.Sprintf("UPID:%s:00000001:00000002:AABBCCDD:cephdaemoncreate:%s:root@pam:", node, node)
}

// cephHandleTaskStatus registers a terminal "stopped/OK" task-status
// response for upid on node, so a blocking apiclient.WaitTask call
// completes immediately instead of polling (destroyHandleTaskStatus's
// convention, local to this file since node/kind vary per case here).
func cephHandleTaskStatus(f *testhelper.FakePVE, node, upid string) {
	f.HandleJSON("GET /api2/json/nodes/"+node+"/tasks/"+upid+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "OK",
		"upid":       upid,
	})
}

func TestLabCephInit_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "init", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephMon_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "mon", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephMgr_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "mgr", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephInit_AlreadyInitialized_Skips(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/cfg/raw",
		"[global]\n\tfsid = abc-123-def\n\tmon_host = 10.252.0.10\n")

	var initRec []hostnetRecordedRequest
	hostnetRecord(f, &initRec, nil, "", "POST /api2/json/nodes/lab-ceph-0/ceph/init", nil, 200)

	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "init", "ceph")
	require.NoError(t, err)
	assert.Contains(t, out, "already initialized")
	assert.Empty(t, initRec, "no POST /ceph/init must be sent once already initialized")
	assert.Empty(t, fake.Calls)
}

func TestLabCephInit_EmptyConf_PostsInit(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	// PVE answers 200 with an EMPTY body on a node that never ran pveceph
	// init: err == nil alone must not read as "already initialized".
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/cfg/raw", "")

	var initRec []hostnetRecordedRequest
	hostnetRecord(f, &initRec, nil, "", "POST /api2/json/nodes/lab-ceph-0/ceph/init", nil, 200)

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "init", "ceph")
	require.NoError(t, err)
	assert.Contains(t, out, "initialized")
	require.Len(t, initRec, 1, "an empty ceph.conf body must still POST /ceph/init")
}

func TestLabCephInit_HappyPath_PostsNetwork(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	// A probe failure (500) also falls through to the POST: CreateCephInit is
	// idempotent (preserves an existing [global] section), and the POST
	// surfaces any real problem loudly where a swallowed probe error would not.
	f.HandleFunc("GET /api2/json/nodes/lab-ceph-0/ceph/cfg/raw", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "not initialized")
	})

	var initRec []hostnetRecordedRequest
	hostnetRecord(f, &initRec, nil, "", "POST /api2/json/nodes/lab-ceph-0/ceph/init", nil, 200)

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "init", "ceph")
	require.NoError(t, err)
	assert.Contains(t, out, "initialized")

	require.Len(t, initRec, 1)
	// network.cidr (the lab /16) must be posted, never mgmt.subnet (the /24)
	// labMgmtCIDR derives node IPs from — spec §4: pveceph init gets the /16.
	assert.Equal(t, "10.252.0.0/16", initRec[0].body["network"])
}

func TestLabCephInit_DryRun_NoAPICalls(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		t.Fatal("dry-run constructed the inner client")
		return nil, nil
	}

	out, err := runGuestCmd(t, cmd, "init", "ceph", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "[dry-run]")
	assert.Empty(t, fake.Calls)
}

func TestLabCephInit_MissingLabContext_Errors(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		return nil, errors.New("context not found")
	}

	_, err := runGuestCmd(t, cmd, "init", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "context not found")
	assert.ErrorContains(t, err, "pmx lab context sync")
	assert.Empty(t, fake.Calls)
}

func TestLabCephMon_CreatesMissingMonsOnly(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mon", []any{
		map[string]any{"name": "lab-ceph-0"},
	})

	var createRec []hostnetRecordedRequest
	for _, node := range []string{"lab-ceph-1", "lab-ceph-2"} {
		upid := cephTaskUPID(node)
		hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/"+node+"/ceph/mon/"+node, upid, 200)
		cephHandleTaskStatus(f, node, upid)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "mon", "ceph")
	require.NoError(t, err)

	require.Len(t, createRec, 2, "only the two missing mons must be created")
	assert.Contains(t, out, "mon lab-ceph-0")
	assert.Contains(t, out, "already present")
	assert.Contains(t, out, "mon lab-ceph-1")
	assert.Contains(t, out, "mon lab-ceph-2")
	assert.Contains(t, out, "created")
}

func TestLabCephMgr_CreatesMissingMgrs(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mgr", []any{
		map[string]any{"name": "lab-ceph-0"},
	})

	var createRec []hostnetRecordedRequest
	for _, node := range []string{"lab-ceph-1", "lab-ceph-2"} {
		upid := cephTaskUPID(node)
		hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/"+node+"/ceph/mgr/"+node, upid, 200)
		cephHandleTaskStatus(f, node, upid)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "mgr", "ceph")
	require.NoError(t, err)

	require.Len(t, createRec, 2, "only the two missing mgrs must be created")
	assert.Contains(t, out, "mgr lab-ceph-0")
	assert.Contains(t, out, "already present")
	assert.Contains(t, out, "mgr lab-ceph-1")
	assert.Contains(t, out, "mgr lab-ceph-2")
	assert.Contains(t, out, "created")
}
