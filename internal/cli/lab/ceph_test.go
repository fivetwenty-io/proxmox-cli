package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	assert.Contains(t, installCmd, "pveceph install --repository no-subscription;",
		"got %q", installCmd)
	assert.NotContains(t, installCmd, "no-subscription -y",
		"pveceph install takes no -y: the option parser rejects it outright and nothing installs")
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

// TestLabCephInstall_MidLoopFailure_RendersCompletedRowsBeforeErroring covers
// the MINOR fix: mon/mgr/osd all call cephRenderPartial to render whatever
// nodes completed before a mid-loop failure (cephRenderPartial's own doc
// comment: "discarding them leaves the operator an opaque error and no idea
// how far the run got"), but install used to return a bare error instead,
// discarding node 0's already-completed row. Node 0 must succeed fully
// (probe + install) before node 1's probe fails, and the completed node 0
// row must still reach output even though the command errors.
func TestLabCephInstall_MidLoopFailure_RendersCompletedRowsBeforeErroring(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: "absent"}, // 0: probe node 0
		exec.FakeResponse{},                 // 1: install node 0
		exec.FakeResponse{Err: errors.New("ssh: connect to host 10.10.1.11 port 22: no route to host")}, // 2: probe node 1
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "install", "wayne")
	require.Error(t, err)
	require.Len(t, fake.Calls, 3)
	assert.Contains(t, out, "install node 0")
	assert.Contains(t, out, "installed")
	assert.NotContains(t, out, "install node 1", "node 1 never completed and must not be rendered as a row")
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
		map[string]any{"name": "lab-ceph-0", "state": "running"},
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
	cephHandleMons(f, "running", "running", "running")
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mgr", []any{
		map[string]any{"name": "lab-ceph-0", "state": "active"},
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

// cephHandleMons registers the mon listing node 0 answers with, one entry per
// state, for lab-ceph-0, lab-ceph-1, and so on. The mgr verb reads it to decide
// whether Ceph is reachable, so every mgr test has to answer it.
func cephHandleMons(f *testhelper.FakePVE, states ...string) {
	list := make([]any, 0, len(states))
	for i, state := range states {
		list = append(list, map[string]any{"name": fmt.Sprintf("lab-ceph-%d", i), "state": state})
	}
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mon", list)
}

// cephDaemonStateCase is one row of the mon/mgr state tests: the listing
// PVE returns for node 0 and what the verb must do about each daemon.
type cephDaemonStateCase struct {
	name string
	kind string
	// listing is the ceph/<kind> listing node 0 answers with.
	listing []any
	// created lists the nodes whose daemon the verb must create.
	created []string
	// brokenState, when set, is the state of lab-ceph-1's daemon, which the
	// verb must report and leave alone (no create, no delete) and fail on.
	brokenState string
}

// cephDaemonStateCases covers a running, an unknown, a stopped, and an absent
// daemon for both kinds. Node 0 always runs a healthy daemon; the case varies
// lab-ceph-1 and leaves lab-ceph-2 absent.
func cephDaemonStateCases() []cephDaemonStateCase {
	var cases []cephDaemonStateCase
	for _, k := range []struct{ kind, healthy string }{{"mgr", "active"}, {"mon", "running"}} {
		node0 := map[string]any{"name": "lab-ceph-0", "state": k.healthy}
		cases = append(cases,
			cephDaemonStateCase{
				name: k.kind + " running", kind: k.kind,
				listing: []any{node0, map[string]any{"name": "lab-ceph-1", "state": k.healthy}},
				created: []string{"lab-ceph-2"},
			},
			cephDaemonStateCase{
				name: k.kind + " absent", kind: k.kind,
				listing: []any{node0},
				created: []string{"lab-ceph-1", "lab-ceph-2"},
			},
			cephDaemonStateCase{
				name: k.kind + " unknown", kind: k.kind,
				listing:     []any{node0, map[string]any{"name": "lab-ceph-1", "state": "unknown"}},
				created:     []string{"lab-ceph-2"},
				brokenState: "unknown",
			},
			cephDaemonStateCase{
				name: k.kind + " stopped", kind: k.kind,
				listing:     []any{node0, map[string]any{"name": "lab-ceph-1", "state": "stopped"}},
				created:     []string{"lab-ceph-2"},
				brokenState: "stopped",
			},
			cephDaemonStateCase{
				name: k.kind + " no state reported", kind: k.kind,
				listing:     []any{node0, map[string]any{"name": "lab-ceph-1"}},
				created:     []string{"lab-ceph-2"},
				brokenState: "none reported",
			},
		)
	}
	// A mgr that is not "active" or "standby" is broken, and a mon that is not
	// "running" is broken, even when the state is healthy for the other kind.
	cases = append(cases,
		cephDaemonStateCase{
			name: "mgr standby counts as present", kind: "mgr",
			listing: []any{
				map[string]any{"name": "lab-ceph-0", "state": "active"},
				map[string]any{"name": "lab-ceph-1", "state": "standby"},
				map[string]any{"name": "lab-ceph-2", "state": "standby"},
			},
		},
		cephDaemonStateCase{
			name: "mon state running is not a mgr state", kind: "mgr",
			listing: []any{
				map[string]any{"name": "lab-ceph-0", "state": "active"},
				map[string]any{"name": "lab-ceph-1", "state": "running"},
			},
			created:     []string{"lab-ceph-2"},
			brokenState: "running",
		},
	)
	return cases
}

// TestLabCephDaemons_CountsOnlyRunningDaemonsAsPresent pins that mon and mgr
// treat a daemon as present only when its reported state shows it running. A
// daemon in a bad state is named with its state and the delete command, is
// neither recreated nor deleted, and fails the verb after the healthy and
// absent nodes are handled.
func TestLabCephDaemons_CountsOnlyRunningDaemonsAsPresent(t *testing.T) {
	for _, tc := range cephDaemonStateCases() {
		t.Run(tc.name, func(t *testing.T) {
			lab := cephTestLab("ceph", 3)
			path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
			f := testhelper.NewFakePVE(t)
			// Node 0's mon is running in every case, so Ceph is reachable and a
			// bad daemon of either kind gets the delete advice.
			cephHandleMons(f, "running")
			f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/"+tc.kind, tc.listing)

			// Nothing is ever deleted, and a broken daemon is never recreated
			// over its own name.
			createForbid(f, t, "DELETE /api2/json/nodes")
			if tc.brokenState != "" {
				createForbid(f, t, "POST /api2/json/nodes/lab-ceph-1/ceph/"+tc.kind+"/lab-ceph-1")
			}

			var createRec []hostnetRecordedRequest
			for _, node := range tc.created {
				upid := cephTaskUPID(node)
				hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/"+node+"/ceph/"+tc.kind+"/"+node, upid, 200)
				cephHandleTaskStatus(f, node, upid)
			}

			cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
			stubInnerAPIClient(t, f)

			out, err := runGuestCmd(t, cmd, tc.kind, "ceph")

			require.Len(t, createRec, len(tc.created), "created exactly the absent daemons")
			for i, node := range tc.created {
				assert.Equal(t, "/api2/json/nodes/"+node+"/ceph/"+tc.kind+"/"+node, createRec[i].path)
			}

			if tc.brokenState == "" {
				require.NoError(t, err)
				assert.NotContains(t, out, "not running")
				return
			}

			require.Error(t, err)
			assert.ErrorContains(t, err, tc.kind+` "lab-ceph-1"`)
			assert.ErrorContains(t, err, `state "`+tc.brokenState+`"`)
			assert.ErrorContains(t, err, "pmx pve -c lab-ceph --node lab-ceph-1 node ceph "+tc.kind+" delete lab-ceph-1 --yes")
			assert.NotContains(t, err.Error(), "lab-ceph-0")
			assert.NotContains(t, err.Error(), "lab-ceph-2")
			assert.Contains(t, out, "present but not running (state: "+tc.brokenState+")")
			assert.NotContains(t, out, tc.kind+" lab-ceph-1  already present")
		})
	}
}

// TestLabCephDaemons_NamesEveryBrokenDaemon covers two broken daemons in one
// run: each is named with its own state and delete command, so the operator
// sees the whole repair list at once.
func TestLabCephDaemons_NamesEveryBrokenDaemon(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	cephHandleMons(f, "running", "running", "running")
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mgr", []any{
		map[string]any{"name": "lab-ceph-0", "state": "unknown"},
		map[string]any{"name": "lab-ceph-1", "state": "active"},
		map[string]any{"name": "lab-ceph-2", "state": "stopped"},
	})
	createForbid(f, t, "POST /api2/json/nodes")
	createForbid(f, t, "DELETE /api2/json/nodes")

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	_, err := runGuestCmd(t, cmd, "mgr", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, `state "unknown"`)
	assert.ErrorContains(t, err, "pmx pve -c lab-ceph --node lab-ceph-0 node ceph mgr delete lab-ceph-0 --yes")
	assert.ErrorContains(t, err, `state "stopped"`)
	assert.ErrorContains(t, err, "pmx pve -c lab-ceph --node lab-ceph-2 node ceph mgr delete lab-ceph-2 --yes")
	assert.NotContains(t, err.Error(), "lab-ceph-1")
}

// assertCephUnreachable checks the error of a run that stopped because PVE
// cannot reach Ceph: it says so, points at quorum, and carries no delete advice.
func assertCephUnreachable(t *testing.T, err error) {
	t.Helper()

	assert.ErrorContains(t, err, "Ceph is unreachable from PVE")
	assert.ErrorContains(t, err, "ceph -s")
	assert.NotContains(t, err.Error(), "pmx pve")
	assert.NotContains(t, err.Error(), "--yes")
}

// TestLabCephDaemons_NoRunningMonMeansCephIsUnreachable covers the mon
// listings that show Ceph as unreachable from PVE, which is only a listing
// with no running mon. Each fails the verb with no create, no delete, and no
// delete command, because deleting a daemon while quorum is lost could wipe the
// monmap. The mgr verb reaches the same verdict from the mon listing, whatever
// the mgrs report.
func TestLabCephDaemons_NoRunningMonMeansCephIsUnreachable(t *testing.T) {
	entry := func(i int, state string) map[string]any {
		return map[string]any{"name": fmt.Sprintf("lab-ceph-%d", i), "state": state}
	}

	for _, tc := range []struct {
		name string
		kind string
		mons []any
		// mgrs is the mgr listing; the mgr verb must not need it to decide.
		mgrs []any
	}{
		{"the only mon in unknown", "mon", []any{entry(0, "unknown")}, nil},
		{"every mon in unknown", "mon", []any{entry(0, "unknown"), entry(1, "unknown"), entry(2, "unknown")}, nil},
		{"every mon stopped", "mon", []any{entry(0, "stopped"), entry(1, "stopped"), entry(2, "stopped")}, nil},
		{"an unknown mon beside a stopped one", "mon", []any{entry(0, "stopped"), entry(1, "unknown")}, nil},
		{"a mon with no state reported", "mon", []any{map[string]any{"name": "lab-ceph-0"}}, nil},
		{"no mon running while mgrs show stopped", "mgr",
			[]any{entry(0, "stopped"), entry(1, "unknown"), entry(2, "stopped")},
			[]any{entry(0, "stopped"), entry(1, "stopped"), entry(2, "stopped")}},
		{"no mon running while mgrs show active", "mgr",
			[]any{entry(0, "unknown"), entry(1, "unknown")},
			[]any{entry(0, "active"), entry(1, "standby")}},
		{"an empty mon listing for the mgr verb", "mgr", []any{}, []any{entry(0, "stopped")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lab := cephTestLab("ceph", 3)
			path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
			f := testhelper.NewFakePVE(t)
			f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mon", tc.mons)
			if tc.mgrs != nil {
				f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mgr", tc.mgrs)
			}
			createForbid(f, t, "POST /api2/json/nodes")
			createForbid(f, t, "DELETE /api2/json/nodes")

			cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
			stubInnerAPIClient(t, f)

			_, err := runGuestCmd(t, cmd, tc.kind, "ceph")
			require.Error(t, err, "the verb still exits non-zero")
			assertCephUnreachable(t, err)
			assert.ErrorContains(t, err, "cannot ensure ceph "+tc.kind)
			assert.ErrorContains(t, err, "no mon is running")
		})
	}

	t.Run("an empty mon listing lets the mon verb create the first mon", func(t *testing.T) {
		lab := cephTestLab("ceph", 3)
		path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
		f := testhelper.NewFakePVE(t)
		f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mon", []any{})
		createForbid(f, t, "DELETE /api2/json/nodes")

		var createRec []hostnetRecordedRequest
		for _, node := range []string{"lab-ceph-0", "lab-ceph-1", "lab-ceph-2"} {
			upid := cephTaskUPID(node)
			hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/"+node+"/ceph/mon/"+node, upid, 200)
			cephHandleTaskStatus(f, node, upid)
		}

		cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
		stubInnerAPIClient(t, f)

		_, err := runGuestCmd(t, cmd, "mon", "ceph")
		require.NoError(t, err)
		assert.Len(t, createRec, 3)
	})
}

// TestLabCephDaemons_RunningMonKeepsDeleteAdvice covers the listings that look
// alarming but leave Ceph reachable, because at least one mon is running. The
// bad daemon is named with its delete command and the error never calls Ceph
// unreachable: an unknown mon among running mons, a stopped mon, the only mgr
// in unknown while the mons run, every mgr in unknown, a stopped mgr, and an
// unknown mgr among healthy ones.
func TestLabCephDaemons_RunningMonKeepsDeleteAdvice(t *testing.T) {
	entry := func(i int, state string) map[string]any {
		return map[string]any{"name": fmt.Sprintf("lab-ceph-%d", i), "state": state}
	}

	for _, tc := range []struct {
		name string
		kind string
		mons []any
		mgrs []any
		// bad is the node whose daemon must get the delete command.
		bad string
	}{
		{"an unknown mon among running mons", "mon",
			[]any{entry(0, "running"), entry(1, "unknown"), entry(2, "running")}, nil, "lab-ceph-1"},
		{"a stopped mon among running mons", "mon",
			[]any{entry(0, "running"), entry(1, "stopped"), entry(2, "running")}, nil, "lab-ceph-1"},
		{"the only mgr in unknown while mons run", "mgr",
			[]any{entry(0, "running"), entry(1, "running"), entry(2, "running")},
			[]any{entry(0, "unknown"), entry(1, "active"), entry(2, "standby")}, "lab-ceph-0"},
		{"every mgr in unknown while mons run", "mgr",
			[]any{entry(0, "running"), entry(1, "running"), entry(2, "running")},
			[]any{entry(0, "unknown"), entry(1, "unknown"), entry(2, "unknown")}, "lab-ceph-1"},
		{"a stopped mgr while mons run", "mgr",
			[]any{entry(0, "running"), entry(1, "running"), entry(2, "running")},
			[]any{entry(0, "active"), entry(1, "stopped"), entry(2, "standby")}, "lab-ceph-1"},
		{"an unknown mgr among healthy ones", "mgr",
			[]any{entry(0, "running"), entry(1, "running"), entry(2, "running")},
			[]any{entry(0, "active"), entry(1, "unknown"), entry(2, "standby")}, "lab-ceph-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lab := cephTestLab("ceph", 3)
			path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
			f := testhelper.NewFakePVE(t)
			f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mon", tc.mons)
			if tc.mgrs != nil {
				f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mgr", tc.mgrs)
			}
			createForbid(f, t, "DELETE /api2/json/nodes")
			// A bad daemon is never recreated over its own name, and every
			// other node in these listings already has a daemon.
			createForbid(f, t, "POST /api2/json/nodes")

			cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
			stubInnerAPIClient(t, f)

			_, err := runGuestCmd(t, cmd, tc.kind, "ceph")
			require.Error(t, err)
			assert.ErrorContains(t, err,
				"pmx pve -c lab-ceph --node "+tc.bad+" node ceph "+tc.kind+" delete "+tc.bad+" --yes")
			assert.NotContains(t, err.Error(), "unreachable")
		})
	}
}

// TestLabCephMgr_LoneUnknownMgrWhileMonsRun is the case a live lab hit: the
// mgr listing holds a single mgr in unknown and the mons run. Ceph is
// reachable, so the other nodes get their mgr and the unknown one is named with
// its delete command instead of being reported as an unreachable cluster.
func TestLabCephMgr_LoneUnknownMgrWhileMonsRun(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	cephHandleMons(f, "running", "running", "running")
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/mgr", []any{
		map[string]any{"name": "lab-ceph-0", "state": "unknown"},
	})
	createForbid(f, t, "DELETE /api2/json/nodes")
	createForbid(f, t, "POST /api2/json/nodes/lab-ceph-0/ceph/mgr/lab-ceph-0")

	var createRec []hostnetRecordedRequest
	for _, node := range []string{"lab-ceph-1", "lab-ceph-2"} {
		upid := cephTaskUPID(node)
		hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/"+node+"/ceph/mgr/"+node, upid, 200)
		cephHandleTaskStatus(f, node, upid)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	_, err := runGuestCmd(t, cmd, "mgr", "ceph")
	require.Error(t, err)
	assert.Len(t, createRec, 2, "the nodes without a mgr still get one")
	assert.ErrorContains(t, err, "pmx pve -c lab-ceph --node lab-ceph-0 node ceph mgr delete lab-ceph-0 --yes")
	assert.NotContains(t, err.Error(), "unreachable")
}

// TestCephDaemonDeleteCommand pins the printed command's form: it names the
// lab's own context so it runs from any shell.
func TestCephDaemonDeleteCommand(t *testing.T) {
	assert.Equal(t, "pmx pve -c lab-wayne --node lab-wayne-1 node ceph mon delete lab-wayne-1 --yes",
		cephDaemonDeleteCommand("wayne", "mon", "lab-wayne-1"))
	assert.Equal(t, "pmx pve -c lab-wayne --node lab-wayne-0 node ceph mgr delete lab-wayne-0 --yes",
		cephDaemonDeleteCommand("wayne", "mgr", "lab-wayne-0"))
}

// TestCephDaemonHelp_NamesTheLabContextAndTheUnreachableCase keeps the long help
// in step with what the verb prints.
func TestCephDaemonHelp_NamesTheLabContextAndTheUnreachableCase(t *testing.T) {
	for _, kind := range []string{"mon", "mgr"} {
		long := newCephDaemonCmd(kind, "x").Long
		assert.Contains(t, long, "pmx pve -c lab-<name> --node <node> node ceph "+kind+" delete <name> --yes")
		assert.Contains(t, long, "unreachable from PVE")
		assert.Contains(t, long, "ceph -s")
	}
}

// --- osd / pool / status ----------------------------------------------------

// cephOSDTestLab is cephTestLab plus per-node OSD disks: `lab ceph osd`
// derives the device paths it expects from storage.osd_disks, so a fixture
// without them exercises only the refusal path. The controller is pinned
// because ValidateStorage (run by config.Load) refuses osd_disks on anything
// but virtio-scsi-single.
func cephOSDTestLab(name string, nodes, disks int) *config.Lab {
	lab := cephTestLab(name, nodes)
	lab.Storage.Controller = "virtio-scsi-single"
	lab.Storage.OSDDisks = &config.LabOSDDisks{Count: disks, SizeGB: 100}
	return lab
}

// cephDiskByIDLink is the by-id link PVE 9.2 publishes for the idx-th OSD
// disk. It is named after the DRIVE (scsi2 is the first OSD disk, after the
// OS and data disks on scsi0/scsi1), NOT after the serial: reproducing that
// exact naming is the point of this fixture, since deriving the link from the
// serial instead is what broke `pmx lab ceph osd` on PVE 9.2.
func cephDiskByIDLink(idx int) string {
	return fmt.Sprintf("/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi%d", idx+2)
}

// cephDiskEntryJSON builds one GET /nodes/{n}/disks/list element for the
// idx-th OSD disk, carrying the fields the verb reads. devpath is the kernel
// name a wipe targets; serial is what the disk is matched on; by_id_link is
// the path the OSD is then created on, read from this listing rather than
// derived from the serial.
//
// osdid is rendered the way PVE renders it and not the way its schema
// documents it: a JSON NUMBER for a disk Ceph does not own (-1) and a JSON
// STRING for one it does ("0"). Both forms appear in the same array, because
// PVE is Perl and a scalar carries whichever type it was last used as. A
// fixture that emitted only numbers is why a decoder that could not read the
// real listing still passed its tests. See testdata/ for the captured
// payload this mirrors.
func cephDiskEntryJSON(idx int, used string, osdid int) map[string]any {
	e := map[string]any{
		"devpath":    "/dev/sd" + string(rune('c'+idx)),
		"by_id_link": cephDiskByIDLink(idx),
		"serial":     fmt.Sprintf("drive-scsi%d", cephOSDSlot(idx)),
		"used":       used,
		"osdid":      osdid,
		"osdid-list": nil,
	}
	if osdid >= 0 {
		e["osdid"] = strconv.Itoa(osdid)
		e["osdid-list"] = []any{strconv.Itoa(osdid)}
	}
	return e
}

// cephHandleDisksList registers node's disk listing on f.
func cephHandleDisksList(f *testhelper.FakePVE, node string, entries ...map[string]any) {
	list := make([]any, 0, len(entries))
	for _, e := range entries {
		list = append(list, e)
	}
	f.HandleJSON("GET /api2/json/nodes/"+node+"/disks/list", list)
}

// cephRecordOSDCreate records POST /nodes/{node}/ceph/osd on f, answering
// with a terminal task so cephWaitTask returns immediately.
func cephRecordOSDCreate(f *testhelper.FakePVE, rec *[]hostnetRecordedRequest, node string) {
	upid := cephTaskUPID(node)
	hostnetRecord(f, rec, nil, "", "POST /api2/json/nodes/"+node+"/ceph/osd", upid, 200)
	cephHandleTaskStatus(f, node, upid)
}

func TestLabCephOsd_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "osd", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephPool_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "pool", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephStatus_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "status", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephOsd_NoOSDDisksConfigured_Errors(t *testing.T) {
	lab := cephTestLab("ceph", 3) // no storage.osd_disks at all
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		t.Fatal("a lab with no OSD disks must be refused before the inner client is built")
		return nil, nil
	}

	_, err := runGuestCmd(t, cmd, "osd", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "has no storage.osd_disks configured")
}

func TestLabCephOsd_DryRun_NoAPICalls(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		t.Fatal("dry-run constructed the inner client")
		return nil, nil
	}

	out, err := runGuestCmd(t, cmd, "osd", "ceph", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "[dry-run]")
	// Dry-run never reads a disk listing, so it names the disk the way the
	// real run will match it: by the by-id link of its SCSI slot.
	assert.Contains(t, out, cephDiskByIDLink(0))
	assert.Empty(t, fake.Calls)
}

func TestLabCephOsd_CreatesOSDOnCleanDevices(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)

	var createRec, wipeRec []hostnetRecordedRequest
	for i := range 3 {
		node := fmt.Sprintf("lab-ceph-%d", i)
		cephHandleDisksList(f, node, cephDiskEntryJSON(0, "", -1))
		cephRecordOSDCreate(f, &createRec, node)
		hostnetRecord(f, &wipeRec, nil, "", "PUT /api2/json/nodes/"+node+"/disks/wipedisk", nil, 200)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "osd", "ceph")
	require.NoError(t, err)

	require.Len(t, createRec, 3, "one OSD must be created per node")
	assert.Empty(t, wipeRec, "a clean device must never be wiped")
	for _, rec := range createRec {
		assert.Equal(t, cephDiskByIDLink(0), rec.body["dev"],
			"the OSD must be created on the by-id path the node itself reported, "+
				"not on one derived from the serial and not on the kernel name")
	}
	assert.Contains(t, out, "created")
}

func TestLabCephOsd_ExistingOSDNeverWiped(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)

	var createRec, wipeRec []hostnetRecordedRequest
	for i := range 3 {
		node := fmt.Sprintf("lab-ceph-%d", i)
		// used is "LVM" exactly as a live OSD reports it: only osdid tells a
		// healthy OSD apart from a foreign LVM volume, so it alone may gate.
		cephHandleDisksList(f, node, cephDiskEntryJSON(0, "LVM", 0))
		cephRecordOSDCreate(f, &createRec, node)
		hostnetRecord(f, &wipeRec, nil, "", "PUT /api2/json/nodes/"+node+"/disks/wipedisk", nil, 200)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "osd", "ceph", "--wipe", "--yes")
	require.NoError(t, err)

	assert.Empty(t, wipeRec, "--wipe must never touch a device Ceph already owns")
	assert.Empty(t, createRec, "an existing OSD must not be recreated")
	assert.Contains(t, out, "already OSD 0")
}

func TestLabCephOsd_InUseDeviceSkippedWithoutWipe(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)

	var createRec, wipeRec []hostnetRecordedRequest
	for i := range 3 {
		node := fmt.Sprintf("lab-ceph-%d", i)
		cephHandleDisksList(f, node, cephDiskEntryJSON(0, "partitions", -1))
		cephRecordOSDCreate(f, &createRec, node)
		hostnetRecord(f, &wipeRec, nil, "", "PUT /api2/json/nodes/"+node+"/disks/wipedisk", nil, 200)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "osd", "ceph")
	require.NoError(t, err)

	assert.Empty(t, wipeRec, "without --wipe nothing may be wiped")
	assert.Empty(t, createRec, "an in-use device must not be handed to Ceph")
	assert.Contains(t, out, "in use (skipped)")
	assert.Contains(t, out, "partitions")
}

func TestLabCephOsd_WipeRequiresConfirmation(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)

	var createRec, wipeRec []hostnetRecordedRequest
	for i := range 3 {
		node := fmt.Sprintf("lab-ceph-%d", i)
		cephHandleDisksList(f, node, cephDiskEntryJSON(0, "partitions", -1))
		cephRecordOSDCreate(f, &createRec, node)
		hostnetRecord(f, &wipeRec, nil, "", "PUT /api2/json/nodes/"+node+"/disks/wipedisk", nil, 200)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)
	// Empty stdin is a non-interactive invocation: the confirmation read
	// hits EOF and must decline rather than hang or error.
	cmd.SetIn(strings.NewReader(""))

	out, err := runGuestCmd(t, cmd, "osd", "ceph", "--wipe")
	require.NoError(t, err)

	assert.Contains(t, out, "Aborted.")
	assert.Empty(t, wipeRec, "a declined confirmation must wipe nothing")
	assert.Empty(t, createRec, "a declined confirmation must create nothing")
}

func TestLabCephOsd_WipeThenCreate(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)

	var createRec, wipeRec []hostnetRecordedRequest
	for i := range 3 {
		node := fmt.Sprintf("lab-ceph-%d", i)
		cephHandleDisksList(f, node, cephDiskEntryJSON(0, "partitions", -1))
		upid := cephTaskUPID(node)
		hostnetRecord(f, &wipeRec, nil, "", "PUT /api2/json/nodes/"+node+"/disks/wipedisk", upid, 200)
		cephHandleTaskStatus(f, node, upid)
		cephRecordOSDCreate(f, &createRec, node)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "osd", "ceph", "--wipe", "--yes")
	require.NoError(t, err)

	require.Len(t, wipeRec, 3, "each in-use device must be wiped once")
	for _, rec := range wipeRec {
		assert.Equal(t, "/dev/sdc", rec.body["disk"],
			"wipedisk takes the kernel device name, not the by-id path")
	}
	require.Len(t, createRec, 3, "each wiped device must then become an OSD")
	assert.Contains(t, out, "wiped")
}

func TestLabCephOsd_ConfiguredDiskMissingFromListing_Errors(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)

	var createRec []hostnetRecordedRequest
	for i := range 3 {
		node := fmt.Sprintf("lab-ceph-%d", i)
		// Only the OS disk is present: the configured OSD disk was never
		// attached to this VM.
		cephHandleDisksList(f, node, map[string]any{
			"devpath": "/dev/sda", "by_id_link": "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi0",
			"used": "LVM", "osdid": -1,
		})
		cephRecordOSDCreate(f, &createRec, node)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	_, err := runGuestCmd(t, cmd, "osd", "ceph")
	require.Error(t, err)
	assert.ErrorContains(t, err, "lab-ceph-0")
	assert.ErrorContains(t, err, cephDiskByIDLink(0))
	assert.ErrorContains(t, err, "pmx pve qemu disk add")
	assert.Empty(t, createRec, "an unresolvable device must abort before any OSD is created")
}

func TestLabCephPool_CreatesWithDefaults(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/pool", []any{})

	var createRec []hostnetRecordedRequest
	upid := cephTaskUPID("lab-ceph-0")
	hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/lab-ceph-0/ceph/pool", upid, 200)
	cephHandleTaskStatus(f, "lab-ceph-0", upid)

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "pool", "ceph")
	require.NoError(t, err)

	require.Len(t, createRec, 1)
	assert.Equal(t, "labrbd", createRec[0].body["name"])
	assert.Equal(t, "3", createRec[0].body["size"])
	assert.Equal(t, "2", createRec[0].body["min_size"])
	assert.Equal(t, "on", createRec[0].body["pg_autoscale_mode"])
	assert.Equal(t, "1", createRec[0].body["add_storages"])
	assert.Contains(t, out, "created")
}

func TestLabCephPool_FlagOverridesReachTheRequest(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/pool", []any{})

	var createRec []hostnetRecordedRequest
	upid := cephTaskUPID("lab-ceph-0")
	hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/lab-ceph-0/ceph/pool", upid, 200)
	cephHandleTaskStatus(f, "lab-ceph-0", upid)

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	_, err := runGuestCmd(t, cmd, "pool", "ceph", "--name", "labpool", "--size", "2",
		"--min-size", "1", "--pg-autoscale-mode", "warn", "--add-storages=false")
	require.NoError(t, err)

	require.Len(t, createRec, 1)
	assert.Equal(t, "labpool", createRec[0].body["name"])
	assert.Equal(t, "2", createRec[0].body["size"])
	assert.Equal(t, "1", createRec[0].body["min_size"])
	assert.Equal(t, "warn", createRec[0].body["pg_autoscale_mode"])
	assert.Equal(t, "0", createRec[0].body["add_storages"])
}

func TestLabCephPool_ExistingPoolSkips(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/pool", []any{
		map[string]any{"pool": 2, "pool_name": "labrbd"},
	})

	var createRec []hostnetRecordedRequest
	hostnetRecord(f, &createRec, nil, "", "POST /api2/json/nodes/lab-ceph-0/ceph/pool", nil, 200)

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "pool", "ceph")
	require.NoError(t, err)

	assert.Empty(t, createRec, "an existing pool must not be re-created")
	assert.Contains(t, out, "already present")
}

// TestLabCephPool_EmptyName_RefusedBeforeAnyAPICall covers the MINOR fix:
// cephPoolExists matches on p.PoolName == name || p.Name == name, so an
// empty --name could match a pool PVE reports with a blank name field, or
// otherwise reach CreateCephPool with an empty pool name. An empty --name
// must be refused up front, before the inner API client is even
// constructed.
func TestLabCephPool_EmptyName_RefusedBeforeAnyAPICall(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		t.Fatal("an empty --name must be rejected before the inner API client is constructed")
		return nil, nil
	}

	_, err := runGuestCmd(t, cmd, "pool", "ceph", "--name", "")
	require.Error(t, err)
	assert.ErrorContains(t, err, "--name")
	assert.Empty(t, fake.Calls)
}

func TestLabCephPool_DryRun_NoAPICalls(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		t.Fatal("dry-run constructed the inner client")
		return nil, nil
	}

	out, err := runGuestCmd(t, cmd, "pool", "ceph", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "[dry-run]")
	assert.Contains(t, out, "labrbd")
	assert.Empty(t, fake.Calls)
}

func TestLabCephStatus_RendersHealth(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/status", map[string]any{
		"health": map[string]any{"status": "HEALTH_WARN"},
		"monmap": map[string]any{"mons": []any{
			map[string]any{"name": "lab-ceph-0"},
			map[string]any{"name": "lab-ceph-1"},
			map[string]any{"name": "lab-ceph-2"},
		}},
		"mgrmap": map[string]any{
			"active_name": "lab-ceph-0",
			"standbys":    []any{map[string]any{"name": "lab-ceph-1"}},
		},
		// Deliberately three distinct counts, so a renderer that reads the
		// wrong field cannot pass by coincidence.
		"osdmap": map[string]any{"num_osds": 4, "num_up_osds": 3, "num_in_osds": 2},
		"pgmap":  map[string]any{"num_pools": 5},
	})

	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "status", "ceph")
	require.NoError(t, err)

	assert.Contains(t, out, "HEALTH_WARN")
	assert.Contains(t, out, "3 up / 2 in / 4 total")
	assert.Contains(t, out, "mons")
	assert.Contains(t, out, "pools")
	assert.Empty(t, fake.Calls, "ceph status is API-only: it must never ssh into a guest")
}

// TestLabCephStatus_EmptyPayload_ReportsNoStatus covers the MINOR fix: an
// empty JSON object body (a decodable but unusable payload — no health
// section at all) used to render the health cell as an empty string
// alongside every count at zero, indistinguishable from a genuinely healthy,
// empty cluster. It must instead render a clear "no status reported"
// placeholder while still exiting 0 and still carrying the raw payload.
func TestLabCephStatus_EmptyPayload_ReportsNoStatus(t *testing.T) {
	lab := cephTestLab("ceph", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/lab-ceph-0/ceph/status", map[string]any{})

	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "status", "ceph")
	require.NoError(t, err)

	assert.Contains(t, out, "no status reported")
	assert.Empty(t, fake.Calls)
}

// TestCephMatchDisk_MatchesSlotNotSerial pins the shape a live PVE 9.2.11
// node reports and that broke OSD creation: the serial in the listing is the
// DRIVE name, not the serial=osdN option `pmx lab create` pins on the disk,
// so neither matching on that value nor deriving a by-id path from it finds
// anything. The by-id link of the disk's SCSI slot does.
func TestCephMatchDisk_MatchesSlotNotSerial(t *testing.T) {
	entries := []cephDiskEntry{
		{Devpath: "/dev/sda", ByIDLink: "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi0", Serial: "drive-scsi0"},
		{Devpath: "/dev/sdc", ByIDLink: cephDiskByIDLink(0), Serial: "drive-scsi2", Osdid: -1},
	}

	require.NotContains(t, cephDiskByIDLink(0), cephOSDSerial(0),
		"fixture guard: the by-id link must not contain the serial= value")
	assert.Nil(t, cephMatchDisk(entries, cephOSDSlot(7), cephOSDSerial(7)),
		"a slot the node does not report must not match anything")

	got := cephMatchDisk(entries, cephOSDSlot(0), cephOSDSerial(0))
	require.NotNil(t, got, "the OSD disk must be found by its slot")
	assert.Equal(t, cephDiskByIDLink(0), got.ByIDLink)
	assert.Equal(t, "/dev/sdc", got.Devpath)
}

// TestCephMatchDisk_IgnoresDevpathOrdering covers lab-ceph-2, where the
// kernel names invert and /dev/sda is drive-scsi1. Slot order and /dev/sdX
// order track nothing in common, so matching must follow the by-id link and
// hand Ceph the right disk regardless of listing or kernel-name order.
func TestCephMatchDisk_IgnoresDevpathOrdering(t *testing.T) {
	entries := []cephDiskEntry{
		{Devpath: "/dev/sdc", ByIDLink: cephDiskByIDLink(1), Serial: "drive-scsi3", Osdid: -1},
		{Devpath: "/dev/sda", ByIDLink: cephDiskByIDLink(0), Serial: "drive-scsi2", Osdid: -1},
	}

	got := cephMatchDisk(entries, cephOSDSlot(0), cephOSDSerial(0))
	require.NotNil(t, got)
	assert.Equal(t, "/dev/sda", got.Devpath, "slot 2 must resolve by by-id link, not by /dev/sdX order")
}

// TestCephMatchDisk_FallsBackToSerialWhenByIDAbsent covers a listing that
// gives no by-id link: the drive-name serial still identifies the slot.
func TestCephMatchDisk_FallsBackToSerialWhenByIDAbsent(t *testing.T) {
	entries := []cephDiskEntry{
		{Devpath: "/dev/sdc", Serial: "drive-scsi2", Osdid: -1},
	}

	got := cephMatchDisk(entries, cephOSDSlot(0), cephOSDSerial(0))
	require.NotNil(t, got, "a listing without by-id links must still match on the drive-name serial")
	assert.Equal(t, "/dev/sdc", got.Devpath)
}

// TestCephMatchDisk_FallsBackToLegacyKeys covers a build that does surface
// ID_SCSI_SERIAL, and the derived by-id path this package used to match on.
// Both are kept so a PVE that names things differently still resolves.
func TestCephMatchDisk_FallsBackToLegacyKeys(t *testing.T) {
	bySerial := []cephDiskEntry{{Devpath: "/dev/sdc", Serial: cephOSDSerial(0), Osdid: -1}}
	got := cephMatchDisk(bySerial, cephOSDSlot(0), cephOSDSerial(0))
	require.NotNil(t, got, "a build reporting ID_SCSI_SERIAL must still match")
	assert.Equal(t, "/dev/sdc", got.Devpath)

	byLegacy := []cephDiskEntry{
		{Devpath: "/dev/sdd", ByIDLink: cephOSDLegacyByIDPath(cephOSDSerial(0)), Osdid: -1},
	}
	got = cephMatchDisk(byLegacy, cephOSDSlot(0), cephOSDSerial(0))
	require.NotNil(t, got, "the legacy derived path must remain a fallback")
	assert.Equal(t, "/dev/sdd", got.Devpath)
}

// TestCephMatchDisk_CapturedPayload_ResolvesBothOSDDisks drives the matcher
// from the disks/list response captured verbatim from a live PVE 9.2.11 node,
// so it exercises the real serial, the real by-id link, the string osdid, and
// osdid-list together.
func TestCephMatchDisk_CapturedPayload_ResolvesBothOSDDisks(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "ceph_disks_list_lab-ceph-0.json"))
	require.NoError(t, err)

	var elems []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &elems))

	entries := make([]cephDiskEntry, 0, len(elems))
	for _, el := range elems {
		e := cephDiskEntry{Osdid: -1}
		require.NoError(t, json.Unmarshal(el, &e))
		entries = append(entries, e)
	}

	for idx, wantDev := range map[int]string{0: "/dev/sdc", 1: "/dev/sdd"} {
		got := cephMatchDisk(entries, cephOSDSlot(idx), cephOSDSerial(idx))
		require.NotNil(t, got, "OSD disk %d must resolve against the captured listing", idx)
		assert.Equal(t, wantDev, got.Devpath)
		assert.Equal(t, int64(idx), got.Osdid.Int(), "the captured listing reports osdid as a string")
	}

	// The OS and data disks live in slots 0 and 1 and must never be picked
	// up as OSD candidates.
	assert.Equal(t, "/dev/sda", entries[0].Devpath)
	assert.Equal(t, "/dev/sdb", entries[1].Devpath)
}

// TestCephDiskEntry_DecodesEveryScalarFormPVEEmits pins the decode of osdid
// and osdid-list across every form PVE renders them in. PVE is Perl: the same
// disks/list array carries osdid as a number for a disk Ceph does not own and
// as a string for one it does, and osdid-list is null, absent, or an array of
// either form. A decoder that accepts only one of those fails on the real
// listing, which is exactly what `pmx lab ceph osd` did.
func TestCephDiskEntry_DecodesEveryScalarFormPVEEmits(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		wantID   int64
		wantList []int64
	}{
		{"number negative", `{"osdid":-1,"osdid-list":null}`, -1, nil},
		{"number zero", `{"osdid":0,"osdid-list":[0]}`, 0, []int64{0}},
		{"number positive", `{"osdid":3,"osdid-list":[3]}`, 3, []int64{3}},
		{"string zero", `{"osdid":"0","osdid-list":["0"]}`, 0, []int64{0}},
		{"string positive", `{"osdid":"12","osdid-list":["12"]}`, 12, []int64{12}},
		{"string negative", `{"osdid":"-1"}`, -1, nil},
		{"mixed list", `{"osdid":-1,"osdid-list":["4",5]}`, -1, []int64{4, 5}},
		{"empty string", `{"osdid":""}`, 0, nil},
		{"null", `{"osdid":null}`, -1, nil},
		{"absent", `{"devpath":"/dev/sdc"}`, -1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// -1 is the seed cephListDisks uses, so an absent or null field
			// must survive as "not an OSD" rather than reading as OSD 0.
			e := cephDiskEntry{Osdid: -1}
			require.NoError(t, json.Unmarshal([]byte(tc.json), &e))
			assert.Equal(t, tc.wantID, e.Osdid.Int())

			got := make([]int64, 0, len(e.OsdidList))
			for _, id := range e.OsdidList {
				got = append(got, id.Int())
			}
			if tc.wantList == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tc.wantList, got)
		})
	}
}

// TestCephListDisks_CapturedPayload_Decodes drives the decoder from a
// disks/list response captured verbatim from a live PVE 9.2.11 node with two
// Ceph OSDs, so the fixture cannot drift toward what the schema documents and
// away from what the API sends.
func TestCephListDisks_CapturedPayload_Decodes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "ceph_disks_list_lab-ceph-0.json"))
	require.NoError(t, err)

	var elems []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &elems))
	require.Len(t, elems, 4)

	entries := make([]cephDiskEntry, 0, len(elems))
	for _, el := range elems {
		e := cephDiskEntry{Osdid: -1}
		require.NoError(t, json.Unmarshal(el, &e))
		entries = append(entries, e)
	}

	// The OS and data disks report osdid as the NUMBER -1; the two OSD disks
	// report it as the STRING "0" and "1", in the same array.
	assert.Equal(t, int64(-1), entries[0].Osdid.Int())
	assert.Equal(t, int64(-1), entries[1].Osdid.Int())
	assert.Equal(t, int64(0), entries[2].Osdid.Int())
	assert.Equal(t, int64(1), entries[3].Osdid.Int())

	// The serial PVE reports is the DRIVE name, never the serial= option
	// `pmx lab create` pins on the disk.
	assert.Equal(t, "drive-scsi2", entries[2].Serial)
	assert.NotEqual(t, cephOSDSerial(0), entries[2].Serial)
	assert.Equal(t, cephDiskByIDLink(0), entries[2].ByIDLink)
}

// TestLabCephOsd_DBWALCarrierNeverWiped covers the device osdid alone cannot
// classify: a shared DB or WAL device is not itself an OSD (osdid -1), so the
// osdid check reads it as free, yet its osdid-list names the OSDs whose
// metadata it carries. Wiping it destroys those OSDs. --wipe --yes must
// decline it and say which OSDs it serves.
func TestLabCephOsd_DBWALCarrierNeverWiped(t *testing.T) {
	lab := cephOSDTestLab("ceph", 3, 1)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"ceph": lab}})
	f := testhelper.NewFakePVE(t)

	var createRec, wipeRec []hostnetRecordedRequest
	for i := range 3 {
		node := fmt.Sprintf("lab-ceph-%d", i)
		entry := cephDiskEntryJSON(0, "LVM", -1)
		entry["osdid-list"] = []any{"3", "4"}
		cephHandleDisksList(f, node, entry)
		cephRecordOSDCreate(f, &createRec, node)
		hostnetRecord(f, &wipeRec, nil, "", "PUT /api2/json/nodes/"+node+"/disks/wipedisk", nil, 200)
	}

	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())
	stubInnerAPIClient(t, f)

	out, err := runGuestCmd(t, cmd, "osd", "ceph", "--wipe", "--yes")
	require.NoError(t, err)

	assert.Empty(t, wipeRec, "--wipe must never destroy a device backing another OSD's DB or WAL")
	assert.Empty(t, createRec, "a DB or WAL carrier must not be handed to Ceph as a new OSD")
	assert.Contains(t, out, "backs OSD 3, 4")
}

// TestCephOSDWipeCount_ExcludesCarriers pins the count the confirmation
// prompt quotes: only devices a wipe would genuinely destroy.
func TestCephOSDWipeCount_ExcludesCarriers(t *testing.T) {
	devices := []cephOSDDevice{
		{node: "a", osdid: -1, used: "partitions"},                 // wipeable
		{node: "b", osdid: 0, used: "LVM"},                         // already an OSD
		{node: "c", osdid: -1, used: ""},                           // free
		{node: "d", osdid: -1, used: "LVM", osdidList: []int64{2}}, // DB or WAL carrier
	}
	assert.Equal(t, 1, cephOSDWipeCount(devices))
}
