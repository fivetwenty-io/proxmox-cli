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
		"serial":     cephOSDSerial(idx),
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
	// real run will match it: by serial.
	assert.Contains(t, out, cephOSDSerial(0))
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
	assert.ErrorContains(t, err, cephOSDSerial(0))
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

// TestCephMatchDisk_MatchesSerialNotDerivedByIDPath pins the PVE 9.2 shape
// that broke OSD creation: the by-id link is named after the DRIVE, so the
// path this package used to derive from the serial matches nothing. Matching
// on the reported serial finds the disk, and the by-id path is then read from
// the listing rather than guessed.
func TestCephMatchDisk_MatchesSerialNotDerivedByIDPath(t *testing.T) {
	entries := []cephDiskEntry{
		{Devpath: "/dev/sda", ByIDLink: "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi0", Serial: "os0"},
		{Devpath: "/dev/sdc", ByIDLink: cephDiskByIDLink(0), Serial: cephOSDSerial(0), Osdid: -1},
	}

	require.NotContains(t, cephDiskByIDLink(0), cephOSDSerial(0),
		"fixture guard: on PVE 9.2 the by-id link must not contain the serial")
	assert.Nil(t, cephMatchDisk(entries, "osd9"), "an absent serial must not match anything")

	got := cephMatchDisk(entries, cephOSDSerial(0))
	require.NotNil(t, got, "the OSD disk must be found by its serial")
	assert.Equal(t, cephDiskByIDLink(0), got.ByIDLink)
	assert.Equal(t, "/dev/sdc", got.Devpath)
}

// TestCephMatchDisk_FallsBackToDerivedPathWhenSerialAbsent covers a PVE build
// whose disk listing omits serial entirely, where the derived by-id path is
// the only identifier available.
func TestCephMatchDisk_FallsBackToDerivedPathWhenSerialAbsent(t *testing.T) {
	entries := []cephDiskEntry{
		{Devpath: "/dev/sdc", ByIDLink: cephOSDLegacyByIDPath(cephOSDSerial(0)), Osdid: -1},
	}

	got := cephMatchDisk(entries, cephOSDSerial(0))
	require.NotNil(t, got, "a listing without serials must still match on the legacy derived path")
	assert.Equal(t, "/dev/sdc", got.Devpath)
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
