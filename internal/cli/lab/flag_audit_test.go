package lab

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// This file covers the flag-audit gaps left by the per-verb test files in
// this package. Most `pmx lab` flags already have an end-to-end assertion
// next to their verb (create_test.go's vcpu/memory-*-gb override, every
// configcmd_test.go add flag, quota_test.go's refquota-gb, access_test.go's
// role, and every destroy/net flag), so only what those files do not already
// cover is duplicated here: create's remaining config-override flags
// (data-disk-gb, os-disk-gb, vxlan-tag, cidr, pool), --start, and
// --clone-from's happy path (only its peppi-refusal path is covered
// elsewhere). It also covers Group's command tree, since lab.go has no
// dedicated test file of its own.

// TestLabGroup_CommandTreeAndAnnotation verifies Group(nil) assembles the
// full `pmx lab` command tree — every one of the ten sub-command groups this
// package exports — under the pve product annotation, without requiring a
// live *cli.Deps.
func TestLabGroup_CommandTreeAndAnnotation(t *testing.T) {
	root := Group(nil)

	assert.Equal(t, "lab", root.Name())
	assert.Equal(t, config.ProductPVE, root.Annotations[cli.ProductAnnotation])

	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}

	for _, want := range []string{
		"create", "destroy", "list", "status", "start", "stop",
		"net", "access", "quota", "config",
		"cluster", "qdevice", "sdn", "nfs",
	} {
		assert.True(t, names[want], "expected %q sub-command to be registered", want)
	}
	assert.Len(t, root.Commands(), 14, "expected exactly fourteen lab sub-commands")
}

// TestCreateAuditFields_NetworkStorageAndPoolOverrides covers the five
// `pmx lab create` config-override flags create_test.go's
// TestCreateFlagOverride_VCPUAndMemory does not already exercise:
// --vxlan-tag, --cidr, --os-disk-gb, --data-disk-gb, and --pool. Each must
// reach the request its resource's create call carries, in place of the
// lab's config value.
func TestCreateAuditFields_NetworkStorageAndPoolOverrides(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne") // Network.VxlanTag 5001, CIDR 10.10.1.0/24,
	// Storage.OSDiskGB 64, DataDiskGB 400, Access.Pool "lab-wayne".
	// The --cidr override below moves the lab to 10.10.9.0/24; drop the
	// fixture's mgmt gateway (10.10.1.1) so the overridden plan stays
	// coherent under create's network-plan validation.
	lab.Network.Mgmt = config.LabMgmt{}

	// Zone and storage already exist; overrides do not touch either, so they
	// are left alone here to keep the fixture focused on the fields under
	// test.
	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	createForbid(f, t, "POST /api2/json/storage")

	var vnetRec []createRecordedRequest
	createRecord(f, &vnetRec, nil, "vnet-list", "GET /api2/json/cluster/sdn/vnets", []any{}, 200)
	createRecord(f, &vnetRec, nil, "vnet-create", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)

	var subnetRec []createRecordedRequest
	createRecord(f, &subnetRec, nil, "subnet-list",
		"GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{}, 200)
	createRecord(f, &subnetRec, nil, "subnet-create",
		"POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)

	var poolRec []createRecordedRequest
	createRecord(f, &poolRec, nil, "pool-list", "GET /api2/json/pools", []any{}, 200)
	createRecord(f, &poolRec, nil, "pool-create", "POST /api2/json/pools", map[string]any{}, 200)
	createPoolNotFoundRoute(f, "custom-pool-wayne")

	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9600")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1",
		"--vxlan-tag", "6002",
		"--cidr", "10.10.9.0/24",
		"--os-disk-gb", "77",
		"--data-disk-gb", "501",
		"--pool", "custom-pool-wayne",
	)
	require.NoError(t, err)

	require.Len(t, vnetRec, 2, "expected one vnet list + one vnet create")
	assert.Equal(t, "6002", vnetRec[1].body["tag"], "--vxlan-tag must override network.vxlan_tag (5001)")

	require.Len(t, subnetRec, 2, "expected one subnet list + one subnet create")
	assert.Equal(t, "10.10.9.0/24", subnetRec[1].body["subnet"], "--cidr must override network.cidr (10.10.1.0/24)")

	require.Len(t, poolRec, 2, "expected one pool list + one pool create")
	assert.Equal(t, "custom-pool-wayne", poolRec[1].body["poolid"], "--pool must override access.pool (lab-wayne)")

	require.Len(t, qemuCreateRec, 1)
	body := qemuCreateRec[0].body
	assert.Equal(t, "custom-pool-wayne", body["pool"], "the VM must be created in the --pool-overridden pool")
	assert.Contains(t, body["scsi0"], ":77,", "--os-disk-gb must override storage.os_disk_gb (64) on scsi0 (the OS disk)")
	assert.Contains(t, body["scsi1"], ":501,", "--data-disk-gb must override storage.data_disk_gb (400) on scsi1 (the data disk)")
}

// TestCreateAuditFields_StartInvokesLifecycleStart covers --start against a
// lab whose zone/vnet/subnet/storage/pool/VM all already exist: every create
// step must be skipped, and the start step must still fire, calling
// CreateQemuStatusStart with the already-existing VM's VMID.
func TestCreateAuditFields_StartInvokesLifecycleStart(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne") // Access.Pool "lab-wayne", CIDR 10.10.1.0/24.

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets/labwayne/subnets")
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	createForbid(f, t, "POST /api2/json/storage")
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})
	createForbid(f, t, "POST /api2/json/pools")
	createPoolNotFoundRoute(f, lab.Access.Pool)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{map[string]any{"vmid": 9700, "name": "lab-wayne"}})
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	startCalled := false
	f.HandleFunc("POST /api2/json/nodes/node1/qemu/9700/status/start",
		func(w http.ResponseWriter, _ *http.Request) {
			startCalled = true
			testhelper.WriteData(w, createTestUPID)
		})
	createHandleTaskStatus(f)
	// Registered explicitly so it is not swept up by the prefix-matched
	// "POST /api2/json/nodes/node1/qemu" forbid registered above: create.go
	// pings the guest agent after starting, tolerating a failure (it only
	// becomes an informational note), but an unregistered route would still
	// hit that forbid handler via prefix match and fail the test.
	f.HandleJSON("POST /api2/json/nodes/node1/qemu/9700/agent/ping", nil)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--start")
	require.NoError(t, err)
	assert.True(t, startCalled, "--start must call CreateQemuStatusStart for the resolved VMID")
	assert.Contains(t, out, "created")
}

// TestCreateAuditFields_CloneFromForwardsToCloneAndConfigUpdate covers
// --clone-from's happy path: create_test.go's
// TestCreateCloneFrom_PeppiGuardRefusesProtectedSourceVMID only exercises the
// peppi-refusal branch. This asserts the flag's value reaches
// CreateQemuClone as the source path segment (and the clone request body's
// pool/storage), and that the follow-up UpdateQemuConfig call targets the
// newly allocated VMID.
func TestCreateAuditFields_CloneFromForwardsToCloneAndConfigUpdate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})
	createPoolNotFoundRoute(f, lab.Access.Pool)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9500")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	var cloneRec []createRecordedRequest
	createRecord(f, &cloneRec, nil, "qemu-clone", "POST /api2/json/nodes/node1/qemu/9400/clone", createTestUPID, 200)
	createHandleTaskStatus(f)

	var configRec []createRecordedRequest
	createRecord(f, &configRec, nil, "qemu-config-update",
		"PUT /api2/json/nodes/node1/qemu/9500/config", map[string]any{}, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--clone-from", "9400")
	require.NoError(t, err)

	require.Len(t, cloneRec, 1, "--clone-from must call CreateQemuClone against its source VMID's path")
	cloneBody := cloneRec[0].body
	assert.Equal(t, "9500", cloneBody["newid"], "the clone must target the allocated VMID")
	assert.Equal(t, "lab-wayne", cloneBody["pool"])
	assert.Equal(t, "tank-lab-wayne", cloneBody["storage"])
	assert.Equal(t, "1", cloneBody["full"])

	require.Len(t, configRec, 1, "the cloned VM's compute/network spec must be applied with a follow-up config update")
	assert.Equal(t, "16", configRec[0].body["cores"])
}
