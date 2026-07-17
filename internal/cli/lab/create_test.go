package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
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

// createTestUPID is a well-formed UPID whose embedded node is "node1", the
// node every test in this file targets; apiclient.WaitTask/WaitForUPID parses
// the node out of the UPID string itself to find the task-status endpoint.
const createTestUPID = "UPID:node1:00001234:00005678:65000000:qmcreate:9500:root@pam:"

// createTestLab returns a Lab definition fully populated for `pmx lab create`
// tests: a clean (non-peppi) vnet/CIDR/pool/DNS zone from cleanLab, plus every
// compute/storage/network field create.go reads that cleanLab leaves zeroed.
func createTestLab(name string) *config.Lab {
	lab := cleanLab(name)
	lab.Network.VnetAlias = "lab-" + name
	lab.Network.VxlanTag = 5001
	lab.Network.Mgmt = config.LabMgmt{Gateway: "10.10.1.1"}
	lab.Network.MTU = 1450
	lab.Compute = config.LabCompute{
		VCPU: 16, CPUType: "host", NUMA: true, Machine: "q35", Firmware: "ovmf",
		Memory: config.LabMemory{MinGB: 32, MaxGB: 96},
	}
	lab.Storage.OSDiskGB = 64
	lab.Storage.DataDiskGB = 400
	lab.Storage.Controller = "virtio-scsi-single"
	lab.Storage.IOThread = true
	lab.Storage.Discard = true
	lab.Storage.SSD = true
	return lab
}

// buildCreateCmd constructs `pmx lab create` wired to a *cli.Deps pointed at
// f and scoped to node, bypassing PersistentPreRunE via cli.WithDeps (the
// supported mechanism for group-package tests; see root.go and net_test.go's
// buildNetCmd).
func buildCreateCmd(t *testing.T, configPath string, f *testhelper.FakePVE, node string) *cobra.Command {
	t.Helper()

	cfg, err := config.Load(configPath)
	require.NoError(t, err)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)

	deps := &cli.Deps{
		Cfg:        cfg,
		ConfigPath: configPath,
		API:        api,
		Out:        output.New(),
		Format:     output.FormatPlain,
		Node:       node,
	}

	cmd := newCreateCmd()
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	return cmd
}

// runCreateCmd executes cmd with args, capturing combined stdout/stderr.
func runCreateCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

// createRecordedRequest captures one HTTP request create.go issued to the
// fake PVE server, decoding a form-urlencoded (or, failing that, JSON) body
// into a plain string map so assertions can read field values directly. This
// mirrors net_test.go's netRecordedRequest/netRecord pair.
type createRecordedRequest struct {
	method string
	path   string
	body   map[string]any
}

// createRecord installs a handler on f for pattern that appends every
// matching request to *rec, also appending label to *order when order is
// non-nil, and replies with payload (or a PVE-shaped error when status is
// >= 400).
func createRecord(
	f *testhelper.FakePVE, rec *[]createRecordedRequest, order *[]string, label, pattern string, payload any, status int,
) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if err := r.ParseForm(); err == nil {
			for k, v := range r.PostForm {
				if len(v) > 0 {
					body[k] = v[0]
				}
			}
		}
		if len(body) == 0 {
			if b, _ := io.ReadAll(r.Body); len(b) > 0 {
				_ = json.Unmarshal(b, &body)
			}
		}
		*rec = append(*rec, createRecordedRequest{method: r.Method, path: r.URL.Path, body: body})
		if order != nil {
			*order = append(*order, label)
		}
		if status >= 400 {
			testhelper.WriteError(w, status, "boom")
			return
		}
		testhelper.WriteData(w, payload)
	})
}

// createForbid installs a handler on f for pattern that fails t immediately
// if the endpoint is ever hit, for asserting a mutating (or VMID-allocating)
// call must never happen: idempotent skip, --dry-run, and peppi-refusal
// tests all rely on this to prove no forbidden side effect occurred.
func createForbid(f *testhelper.FakePVE, t *testing.T, pattern string) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("forbidden endpoint was called: %s %s", r.Method, r.URL.Path)
		testhelper.WriteError(w, http.StatusInternalServerError, "forbidden in this test")
	})
}

// createPoolNotFoundRoute registers a 404 "does not exist" response for GET
// /pools/{poolID}, the GetPools?type=qemu call buildCreatePlan's VM-lookup
// step issues to check pool membership before falling back to a name+node
// match. A bare "GET /api2/json/pools" route (registered by every test that
// also exercises the resource-pool list/create step) would otherwise
// prefix-match this exact per-pool path and answer with the wrong response
// shape, since FakePVE's router falls back to longest-prefix matching when
// no exact route is registered; registering the exact path here (which
// FakePVE always prefers over a prefix match) avoids that collision.
func createPoolNotFoundRoute(f *testhelper.FakePVE, poolID string) {
	f.HandleFunc("GET /api2/json/pools/"+poolID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, fmt.Sprintf("pool %q does not exist", poolID))
	})
}

// createHandleTaskStatus registers a terminal "stopped/OK" task-status
// response for createTestUPID so a blocking create/clone/start step
// completes immediately, matching qemu_test.go's handleTaskStatus.
func createHandleTaskStatus(f *testhelper.FakePVE) {
	f.HandleJSON("GET /api2/json/nodes/node1/tasks/"+createTestUPID+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "OK",
		"upid":       createTestUPID,
	})
}

// TestCreateHappyPath_OrderedCalls covers a lab whose zone, vnet, subnet,
// storage, pool, and VM do not exist yet: every resource must be listed then
// created, in the fixed order create.go composes them in, and the VM must be
// created with a next-id-allocated VMID.
func TestCreateHappyPath_OrderedCalls(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	var order []string
	var recs []createRecordedRequest

	createRecord(f, &recs, &order, "zone-list", "GET /api2/json/cluster/sdn/zones", []any{}, 200)
	createRecord(f, &recs, &order, "zone-create", "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)
	createRecord(f, &recs, &order, "vnet-list", "GET /api2/json/cluster/sdn/vnets", []any{}, 200)
	createRecord(f, &recs, &order, "vnet-create", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	createRecord(f, &recs, &order, "subnet-list", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{}, 200)
	createRecord(f, &recs, &order, "subnet-create", "POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)
	createRecord(f, &recs, &order, "storage-list", "GET /api2/json/storage", []any{}, 200)
	createRecord(f, &recs, &order, "storage-create", "POST /api2/json/storage", map[string]any{}, 200)
	createRecord(f, &recs, &order, "pool-list", "GET /api2/json/pools", []any{}, 200)
	createRecord(f, &recs, &order, "pool-create", "POST /api2/json/pools", map[string]any{}, 200)
	createPoolNotFoundRoute(f, "lab-wayne")
	createRecord(f, &recs, &order, "qemu-list", "GET /api2/json/nodes/node1/qemu", []any{}, 200)
	createRecord(f, &recs, &order, "nextid", "GET /api2/json/cluster/nextid", "9500", 200)
	createRecord(f, &recs, &order, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "created")

	// buildCreatePlan performs every read (list) query, including the VMID
	// allocation, during its planning phase, before create.go's execution
	// loop issues any mutating call; this is intentional, so a protected
	// peppi VMID discovered late in planning aborts before any earlier step
	// (zone/vnet/subnet/storage/pool) has been mutated. Reads therefore all
	// precede writes here, rather than interleaving list/create per resource.
	assert.Equal(t, []string{
		"zone-list", "vnet-list", "subnet-list", "storage-list", "pool-list", "qemu-list", "nextid",
		"zone-create", "vnet-create", "subnet-create", "storage-create", "pool-create", "qemu-create",
	}, order)
}

// TestCreateIdempotent_SkipsExistingResources covers a lab whose zone, vnet,
// storage, and VM already exist in fake state: those four creates must be
// skipped with no duplicate call, while the still-missing subnet and pool are
// still created, keeping create idempotent.
func TestCreateIdempotent_SkipsExistingResources(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	var order []string
	var recs []createRecordedRequest

	createRecord(f, &recs, &order, "zone-list", "GET /api2/json/cluster/sdn/zones",
		[]any{map[string]any{"zone": "labs"}}, 200)
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")

	createRecord(f, &recs, &order, "vnet-list", "GET /api2/json/cluster/sdn/vnets",
		[]any{map[string]any{"vnet": "labwayne"}}, 200)
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")

	createRecord(f, &recs, &order, "subnet-list", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{}, 200)
	createRecord(f, &recs, &order, "subnet-create", "POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)

	createRecord(f, &recs, &order, "storage-list", "GET /api2/json/storage",
		[]any{map[string]any{"storage": "tank-lab-wayne"}}, 200)
	createForbid(f, t, "POST /api2/json/storage")

	createRecord(f, &recs, &order, "pool-list", "GET /api2/json/pools", []any{}, 200)
	createRecord(f, &recs, &order, "pool-create", "POST /api2/json/pools", map[string]any{}, 200)
	createPoolNotFoundRoute(f, "lab-wayne")

	createRecord(f, &recs, &order, "qemu-list", "GET /api2/json/nodes/node1/qemu",
		[]any{map[string]any{"vmid": 8123, "name": "lab-wayne"}}, 200)
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "skip")

	// As in the happy-path test, every read happens during planning before
	// any write; no next-id allocation call is made since the VM already
	// exists.
	assert.Equal(t, []string{
		"zone-list", "vnet-list", "subnet-list", "storage-list", "pool-list", "qemu-list",
		"subnet-create", "pool-create",
	}, order)
}

// TestCreateIdempotent_SkipsSubnetOnRealPVESubnetShape covers the subnet
// existence check against the real PVE response shape: the "subnet" field
// carries the PVE-assigned subnet identifier ("labwayne-10.10.1.0-24"), and
// the CIDR the lab config states lives in a separate "cidr" field. create
// must still recognize the subnet as already existing and skip
// CreateSdnVnetsSubnets. Against the field this test replaced (matching
// "subnet" itself to the lab's CIDR), a real-shaped list response never
// matches, so create would re-issue CreateSdnVnetsSubnets here and this test
// would fail on the forbidden-endpoint call below.
func TestCreateIdempotent_SkipsSubnetOnRealPVESubnetShape(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{
			"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR, "gateway": "10.10.1.1", "zone": lab.Network.EffectiveZoneName(),
		}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets/labwayne/subnets")
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	createForbid(f, t, "POST /api2/json/storage")
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})
	createForbid(f, t, "POST /api2/json/pools")
	createPoolNotFoundRoute(f, lab.Access.Pool)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{map[string]any{"vmid": 8123, "name": "lab-wayne"}})
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "skip")
}

// TestCreateIdempotent_FindsExistingVMViaPoolMembership covers create
// locating its lab's already-existing VM by resource-pool membership,
// matching how destroy/start/stop/list/status locate it, even when the live
// VM's own name has diverged from the "lab-<name>" convention create's own
// name-match fallback expects. A name-only lookup would treat this VM as
// absent and attempt to create a duplicate, which the forbidden
// nextid/qemu-create routes below would catch.
func TestCreateIdempotent_FindsExistingVMViaPoolMembership(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne") // Access.Pool "lab-wayne"

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

	f.HandleFunc("GET /api2/json/pools/lab-wayne", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200, "name": "lab-wayne"},
			},
		})
	})

	// The name-match fallback would miss this VM: its live name has
	// diverged from the "lab-wayne" convention (e.g. renamed by hand), so
	// only pool membership can find it.
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{map[string]any{"vmid": 9200, "name": "renamed-vm"}})
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "9200")
	assert.Contains(t, out, "skip")
}

// TestCreateStart_TargetsExistingVMsOwnNode covers create --start against a
// lab whose VM already exists on a different node than --node: the start (and
// the guest-agent ping after it) must target the node the VM actually lives
// on, learned from pool membership, not the --node flag value — a node-scoped
// qemu call against the wrong node 404s.
func TestCreateStart_TargetsExistingVMsOwnNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})

	f.HandleFunc("GET /api2/json/pools/lab-wayne", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				{"id": "qemu/9200", "node": "node2", "type": "qemu", "vmid": 9200, "name": "lab-wayne"},
			},
		})
	})

	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	// Prefix-matches every node1 qemu POST, including a misdirected
	// /nodes/node1/qemu/9200/status/start.
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	startCalled := false
	f.HandleFunc("POST /api2/json/nodes/node2/qemu/9200/status/start", func(w http.ResponseWriter, _ *http.Request) {
		startCalled = true
		testhelper.WriteData(w, nil)
	})
	f.HandleJSON("POST /api2/json/nodes/node2/qemu/9200/agent/ping", map[string]any{})

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--start")
	require.NoError(t, err)
	assert.True(t, startCalled, "start must be issued against the VM's own node")
	assert.Contains(t, out, "node2")
}

// TestCreateIdempotent_FallsBackToNameMatchWhenPoolAbsent covers create's
// name+node fallback: when the lab's resource pool does not exist yet (this
// lab's first-ever create, before its pool has any member, or a pool
// destroyed since), the existing-VM lookup falls back to a name match on
// node instead of treating an already-created VM as absent and duplicating
// it.
func TestCreateIdempotent_FallsBackToNameMatchWhenPoolAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets/labwayne/subnets")
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	createForbid(f, t, "POST /api2/json/storage")
	f.HandleJSON("GET /api2/json/pools", []any{})
	f.HandleJSON("POST /api2/json/pools", map[string]any{})
	createPoolNotFoundRoute(f, "lab-wayne") // pool has no member yet: falls back to name match

	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{map[string]any{"vmid": 8123, "name": "lab-wayne"}})
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "8123")
	assert.Contains(t, out, "skip")
}

// TestCreateDryRun_NoMutationsShowsPlaceholderVMID covers --dry-run against a
// lab with no existing resources: every mutating endpoint (including the
// next-id allocator) must be forbidden, and the rendered plan must use the
// literal "<vmid>" placeholder for the not-yet-created VM.
func TestCreateDryRun_NoMutationsShowsPlaceholderVMID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets/labwayne/subnets")
	f.HandleJSON("GET /api2/json/storage", []any{})
	createForbid(f, t, "POST /api2/json/storage")
	f.HandleJSON("GET /api2/json/pools", []any{})
	createForbid(f, t, "POST /api2/json/pools")
	createPoolNotFoundRoute(f, "lab-wayne")
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")
	createForbid(f, t, "GET /api2/json/cluster/nextid")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "<vmid>")
	assert.Contains(t, out, "would create")
}

// TestCreateFlagOverride_VCPUAndMemory covers flag-over-config precedence: a flag that
// was passed (--vcpu, --memory-max-gb) must override the lab's config value
// in the request sent to CreateQemu, while a flag left unset
// (--memory-min-gb) must carry the config value through unchanged.
func TestCreateFlagOverride_VCPUAndMemory(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne") // Compute.VCPU=16, Memory.MaxGB=96, Memory.MinGB=32

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})
	createPoolNotFoundRoute(f, lab.Access.Pool)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9500")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--vcpu", "24", "--memory-max-gb", "128")
	require.NoError(t, err)

	require.Len(t, qemuCreateRec, 1)
	body := qemuCreateRec[0].body
	assert.Equal(t, "24", body["cores"], "--vcpu must override compute.vcpu (16)")
	assert.Equal(t, "131072", body["memory"], "--memory-max-gb 128 must override compute.memory.max_gb (96) as MiB")
	assert.Equal(t, "32768", body["balloon"], "unset --memory-min-gb must carry compute.memory.min_gb (32) through as MiB")
}

// TestCreateZoneSpecMatchesNetApply covers a divergence risk between two
// verbs that both provision the lab's config-resolved SDN zone: `pmx lab create`'s
// zone-create call must carry the same Peers, Nodes, and MTU as `pmx lab net
// apply`'s ensureLabSdnZone (net.go), since both build the request through
// the shared labZoneCreateParams helper.
func TestCreateZoneSpecMatchesNetApply(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne") // Network.MTU: 1450

	var zoneRec []createRecordedRequest
	createRecord(f, &zoneRec, nil, "zone-list", "GET /api2/json/cluster/sdn/zones", []any{}, 200)
	createRecord(f, &zoneRec, nil, "zone-create", "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})
	createPoolNotFoundRoute(f, lab.Access.Pool)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9500")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)

	require.Len(t, zoneRec, 2, "expected one zone list + one zone create")
	create := zoneRec[1]
	assert.Equal(t, lab.Network.EffectiveZoneName(), create.body["zone"])
	assert.Equal(t, lab.Network.EffectiveZoneType(), create.body["type"])
	assert.Empty(t, create.body["peers"], "must match net.go's ensureLabSdnZone peers (simple zone: no peers field)")
	assert.Equal(t, "node1", create.body["nodes"], "must be scoped to the node create targets, same as net apply")
	assert.Equal(t, "1450", create.body["mtu"])
}

// TestCreateDerivesStorageAndPoolFromNonDefaultConfig covers a lab whose
// Storage.Pool is explicitly a non-"tank" base pool and whose Access.Pool is
// left empty: create must still target the correctly derived storage
// ID/dataset (othertank-lab-wayne / othertank/labs/wayne), not "othertank"
// itself, and the correctly derived resource pool (lab-wayne), not an empty
// pool.
func TestCreateDerivesStorageAndPoolFromNonDefaultConfig(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.Pool = "othertank"
	lab.Access.Pool = ""

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})

	var storageRec []createRecordedRequest
	createRecord(f, &storageRec, nil, "storage-list", "GET /api2/json/storage", []any{}, 200)
	createRecord(f, &storageRec, nil, "storage-create", "POST /api2/json/storage", map[string]any{}, 200)

	var poolRec []createRecordedRequest
	createRecord(f, &poolRec, nil, "pool-list", "GET /api2/json/pools", []any{}, 200)
	createRecord(f, &poolRec, nil, "pool-create", "POST /api2/json/pools", map[string]any{}, 200)
	createPoolNotFoundRoute(f, "lab-wayne")

	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9500")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)

	require.Len(t, storageRec, 2, "expected one storage list + one storage create")
	assert.Equal(t, "othertank-lab-wayne", storageRec[1].body["storage"])
	assert.Equal(t, "othertank/labs/wayne", storageRec[1].body["pool"])

	require.Len(t, poolRec, 2, "expected one pool list + one pool create")
	assert.Equal(t, "lab-wayne", poolRec[1].body["poolid"],
		"empty access.pool must fall back to lab-<name>, matching destroy/lifecycle/access")

	require.Len(t, qemuCreateRec, 1)
	assert.Equal(t, "lab-wayne", qemuCreateRec[0].body["pool"])
	assert.Contains(t, qemuCreateRec[0].body["efidisk0"], "othertank-lab-wayne:",
		"the VM's disks must be created on the same storage ID create just registered")
}

// TestCreateCloneFrom_PeppiGuardRefusesProtectedSourceVMID covers
// --clone-from: it must never be allowed to seed a lab clone from a
// peppi-protected production VMID, even though cloning only reads the
// source.
func TestCreateCloneFrom_PeppiGuardRefusesProtectedSourceVMID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})
	createPoolNotFoundRoute(f, lab.Access.Pool)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9500")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu/50000/clone")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--clone-from", "50000")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "50000")
}

// TestCreateCloneFrom_PeppiGuardRefusesProtectedSourceName covers a
// --clone-from source whose VMID (777) is not itself one of the protected
// peppi VMIDs, but whose live name ("peppiprd") matches a protected name
// pattern: the guard must still refuse, since it is looked up in the same
// node's qemu list and guarded by name as well as VMID.
func TestCreateCloneFrom_PeppiGuardRefusesProtectedSourceName(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})
	createPoolNotFoundRoute(f, lab.Access.Pool)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{map[string]any{"vmid": 777, "name": "peppiprd"}})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9500")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu/777/clone")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--clone-from", "777")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppiprd")
}

// TestCreatePeppiGuard_RefusesExistingProtectedVMID covers a lab whose VM
// already exists at a protected peppi VMID (50000): the command must refuse
// before any mutating call, including the SDN zone/vnet steps that are
// ordered before the VM step, since buildCreatePlan resolves and guards the
// VMID during its read-only planning phase.
func TestCreatePeppiGuard_RefusesExistingProtectedVMID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets/labwayne/subnets")
	f.HandleJSON("GET /api2/json/storage", []any{})
	createForbid(f, t, "POST /api2/json/storage")
	f.HandleJSON("GET /api2/json/pools", []any{})
	createForbid(f, t, "POST /api2/json/pools")
	createPoolNotFoundRoute(f, "lab-wayne")
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{map[string]any{"vmid": 50000, "name": "lab-wayne"}})
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "50000")
}

// createHandleNextIDSequence registers GET /cluster/nextid to return each of
// ids in turn on successive calls, wrapping to the last value once
// exhausted. Since buildCreatePlan's createNextVMIDAllocator (M2-01 fix)
// calls the live GET /cluster/nextid endpoint at most once per
// buildCreatePlan run — every VMID after the first is derived by
// incrementing locally, not by re-querying the server — a multi-node test
// using this handler only ever actually receives ids[0] over the wire; the
// remaining elements exist for readability at call sites (each element
// still documents the exact VMID that target is expected to end up with)
// rather than because the live route is hit that many times.
// TestCreateThreeNodeCluster_NextIDStateful_EachNodeGetsDistinctVMID below
// additionally proves the allocator's real behavior against a server-state-
// driven fake, where nextid genuinely only advances once a VM has actually
// been created — the behavior a pre-scripted sequence like this one cannot
// exercise.
func createHandleNextIDSequence(f *testhelper.FakePVE, ids ...string) {
	var mu sync.Mutex
	call := 0
	f.HandleFunc("GET /api2/json/cluster/nextid", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		idx := call
		if idx >= len(ids) {
			idx = len(ids) - 1
		}
		call++
		mu.Unlock()
		testhelper.WriteData(w, ids[idx])
	})
}

// createSharedResourcesExist registers the zone/vnet/subnet/storage/pool
// list routes as already existing (and forbids their create endpoints),
// isolating a multi-node test to only the per-node VM creation steps under
// test. name is the lab's resolved name (lab.Name is left empty on the
// unresolved struct these test fixtures build directly, rather than through
// config.ResolveLabs, so it cannot be read off lab itself).
func createSharedResourcesExist(f *testhelper.FakePVE, t *testing.T, lab *config.Lab, name, poolID string) {
	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": lab.Network.VnetID}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/"+lab.Network.VnetID+"/subnets",
		[]any{map[string]any{"subnet": lab.Network.VnetID + "-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets/"+lab.Network.VnetID+"/subnets")
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-" + name}})
	createForbid(f, t, "POST /api2/json/storage")
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": poolID}})
	createForbid(f, t, "POST /api2/json/pools")
	createPoolNotFoundRoute(f, poolID) // no members yet: every node target falls back to name-match (empty)
}

// TestCreateThreeNodeCluster_CreatesOneVMPerNode covers the 3-node create
// shape (multi-node lab plan §11 M2 acceptance): topology.nodes: 3, no
// QDevice (odd count). Every node gets its own qemu-create call, named by
// resolve.go's labNodeVMName convention, with a distinct nextid-allocated
// VMID, in index order.
func TestCreateThreeNodeCluster_CreatesOneVMPerNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("pve-cpi")
	lab.Topology = config.LabTopology{Nodes: 3}
	poolID := lab.Access.Pool // "lab-pve-cpi"

	createSharedResourcesExist(f, t, lab, "pve-cpi", poolID)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	createHandleNextIDSequence(f, "9500", "9501", "9502")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pve-cpi": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "pve-cpi", "--node", "node1")
	require.NoError(t, err)

	require.Len(t, qemuCreateRec, 3, "one qemu-create call per node")
	for i, rec := range qemuCreateRec {
		assert.Equal(t, "lab-pve-cpi-"+strconv.Itoa(i), rec.body["name"], "node %d name", i)
		assert.Equal(t, poolID, rec.body["pool"])
	}
	assert.Equal(t, "9500", qemuCreateRec[0].body["vmid"])
	assert.Equal(t, "9501", qemuCreateRec[1].body["vmid"])
	assert.Equal(t, "9502", qemuCreateRec[2].body["vmid"])

	assert.Contains(t, out, "9500")
	assert.Contains(t, out, "9501")
	assert.Contains(t, out, "9502")
	assert.NotContains(t, out, "lab-pve-cpi-q", "an odd node count must never provision a QDevice")
}

// createStatefulPVEState tracks which VMIDs have actually been created via
// a POST .../qemu call, so createHandleStatefulNextID can mirror PVE's real
// GET /cluster/nextid contract — the lowest free VMID given CURRENT live
// server state — rather than a pre-scripted response sequence. This is the
// realistic fake M2-01's regression test needs: the real bug (every target
// allocated the same VMID) is invisible against a fake that advances nextid
// on every call regardless of whether a VM was actually created in between.
type createStatefulPVEState struct {
	mu    sync.Mutex
	taken map[int64]bool
}

func newCreateStatefulPVEState() *createStatefulPVEState {
	return &createStatefulPVEState{taken: map[int64]bool{}}
}

// markTaken records vmid as belonging to a VM that now actually exists.
func (s *createStatefulPVEState) markTaken(vmid int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taken[vmid] = true
}

// nextFree returns the lowest VMID at or above start not yet marked taken.
func (s *createStatefulPVEState) nextFree(start int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := start
	for s.taken[id] {
		id++
	}
	return id
}

// createHandleStatefulNextID registers GET /cluster/nextid to return
// state.nextFree(start) on every call — PVE's real contract — instead of a
// pre-scripted sequence, so a test using it cannot pass unless the code
// under test genuinely avoids re-querying nextid before the previous
// target's VM exists.
func createHandleStatefulNextID(f *testhelper.FakePVE, state *createStatefulPVEState, start int64) {
	f.HandleFunc("GET /api2/json/cluster/nextid", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, strconv.FormatInt(state.nextFree(start), 10))
	})
}

// createHandleStatefulQemuCreate registers POST /nodes/{node}/qemu to
// record each request like createRecord, and additionally mark the
// created VMID taken in state so a subsequent nextFree call reflects it.
func createHandleStatefulQemuCreate(f *testhelper.FakePVE, state *createStatefulPVEState, rec *[]createRecordedRequest, node string) {
	f.HandleFunc("POST /api2/json/nodes/"+node+"/qemu", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if err := r.ParseForm(); err == nil {
			for k, v := range r.PostForm {
				if len(v) > 0 {
					body[k] = v[0]
				}
			}
		}
		*rec = append(*rec, createRecordedRequest{method: r.Method, path: r.URL.Path, body: body})
		if vmidStr, ok := body["vmid"].(string); ok {
			if vmid, err := strconv.ParseInt(vmidStr, 10, 64); err == nil {
				state.markTaken(vmid)
			}
		}
		testhelper.WriteData(w, createTestUPID)
	})
}

// TestCreateThreeNodeCluster_NextIDStateful_EachNodeGetsDistinctVMID is
// M2-01's regression test: against a nextid fake that only advances once a
// VM has actually been created (createStatefulPVEState), rather than a
// pre-scripted sequence, every one of the 3 nodes must still receive a
// distinct VMID. Before the createNextVMIDAllocator fix, buildCreatePlan
// queried ListNextid once per target during planning — before any target's
// VM existed — so every target received the identical "lowest free VMID"
// answer and node 1's create would fail "VM already exists" against node
// 0's already-created VM; this test fails against that behavior (all three
// recorded vmid values would be equal) and passes against the fix.
func TestCreateThreeNodeCluster_NextIDStateful_EachNodeGetsDistinctVMID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("pve-cpi")
	lab.Topology = config.LabTopology{Nodes: 3}
	poolID := lab.Access.Pool

	createSharedResourcesExist(f, t, lab, "pve-cpi", poolID)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})

	state := newCreateStatefulPVEState()
	createHandleStatefulNextID(f, state, 9500)
	var qemuCreateRec []createRecordedRequest
	createHandleStatefulQemuCreate(f, state, &qemuCreateRec, "node1")
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pve-cpi": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "pve-cpi", "--node", "node1")
	require.NoError(t, err)

	require.Len(t, qemuCreateRec, 3, "one qemu-create call per node")
	seen := map[string]bool{}
	for i, rec := range qemuCreateRec {
		vmid, _ := rec.body["vmid"].(string)
		require.NotEmpty(t, vmid, "node %d must carry a resolved vmid", i)
		assert.False(t, seen[vmid], "VMID %s was allocated to more than one node", vmid)
		seen[vmid] = true
	}
	assert.Len(t, seen, 3, "every node must receive a distinct VMID")
}

// TestCreateTwoNodePlusQdeviceCluster_NextIDStateful_EachTargetGetsDistinctVMID
// covers the 2-node + QDevice shape against the same stateful nextid fake:
// the QDevice target (allocated after both node targets) must also receive
// a VMID distinct from either node's.
func TestCreateTwoNodePlusQdeviceCluster_NextIDStateful_EachTargetGetsDistinctVMID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Topology = config.LabTopology{Nodes: 2}
	poolID := lab.Access.Pool

	createSharedResourcesExist(f, t, lab, "wayne", poolID)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})

	state := newCreateStatefulPVEState()
	createHandleStatefulNextID(f, state, 9600)
	var qemuCreateRec []createRecordedRequest
	createHandleStatefulQemuCreate(f, state, &qemuCreateRec, "node1")
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)

	require.Len(t, qemuCreateRec, 3, "two node VMs plus the mandatory QDevice VM")
	seen := map[string]bool{}
	for _, rec := range qemuCreateRec {
		vmid, _ := rec.body["vmid"].(string)
		require.NotEmpty(t, vmid)
		assert.False(t, seen[vmid], "VMID %s reused across targets", vmid)
		seen[vmid] = true
	}
	assert.Len(t, seen, 3)
}

// TestCreateTwoNodeCluster_AddsMandatoryQdevice covers the 2-node create
// shape: topology.nodes: 2 with the default "auto" QDevice policy adds a
// third, tiny QDevice VM (1 vCPU, 1G RAM, 8G disk — never the lab's node
// sizing), named by resolve.go's labQdeviceVMName convention, created after
// both node VMs.
func TestCreateTwoNodeCluster_AddsMandatoryQdevice(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Topology = config.LabTopology{Nodes: 2}
	poolID := lab.Access.Pool

	createSharedResourcesExist(f, t, lab, "wayne", poolID)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	createHandleNextIDSequence(f, "9600", "9601", "9602")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)

	require.Len(t, qemuCreateRec, 3, "two node VMs plus the mandatory QDevice VM")
	assert.Equal(t, "lab-wayne-0", qemuCreateRec[0].body["name"])
	assert.Equal(t, "lab-wayne-1", qemuCreateRec[1].body["name"])

	qdevice := qemuCreateRec[2]
	assert.Equal(t, "lab-wayne-q", qdevice.body["name"])
	assert.Equal(t, "9602", qdevice.body["vmid"])
	assert.Equal(t, "1", qdevice.body["cores"], "QDevice must use the fixed 1-vCPU spec, never the lab's node sizing (16)")
	assert.Equal(t, "1024", qdevice.body["memory"], "QDevice must use the fixed 1G RAM spec")
	assert.Contains(t, qdevice.body["scsi0"], ":8", "QDevice disk must be the fixed 8G spec")
	_, hasScsi1 := qdevice.body["scsi1"]
	assert.False(t, hasScsi1, "QDevice VM has a single disk, unlike node VMs' OS+data disk pair")

	assert.Contains(t, out, "lab-wayne-q")
}

// TestCreateFourNodeCluster_QdeviceNeverOptsOut covers --qdevice never (and
// the equivalent topology.qdevice: never) suppressing the otherwise
// default-on QDevice at 4 nodes — a legitimate opt-out per §3.1, unlike at 2
// nodes (see TestCreateTwoNodesQdeviceNever_Refused), where a QDevice is
// mandatory and ValidateTopology rejects "never" outright.
func TestCreateFourNodeCluster_QdeviceNeverOptsOut(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Topology = config.LabTopology{Nodes: 4}
	poolID := lab.Access.Pool

	createSharedResourcesExist(f, t, lab, "wayne", poolID)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	createHandleNextIDSequence(f, "9700", "9701", "9702", "9703")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--qdevice", "never")
	require.NoError(t, err)

	require.Len(t, qemuCreateRec, 4, "only the four node VMs; --qdevice never suppresses the QDevice")
}

// TestCreateTwoNodesQdeviceNever_Refused covers M2-06: create must refuse
// --qdevice never against a 2-node lab before any API call at all — a
// QDevice is mandatory at exactly 2 nodes (§3.1), so this combination is a
// topology validation error, not a valid opt-out.
func TestCreateTwoNodesQdeviceNever_Refused(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Topology = config.LabTopology{Nodes: 2}

	f.HandleFunc("GET /api2/json/cluster/sdn/zones", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("an invalid topology must be rejected before any plan-building API call")
	})

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--qdevice", "never")
	require.Error(t, err)
	assert.ErrorContains(t, err, "topology is invalid")
	assert.ErrorContains(t, err, "mandatory")
}

// --- capacity gate ----------------------------------------------------

// createHandleStorageStatus registers GET /nodes/{node}/storage/{storage}/status
// to report totalBytes and usedBytes.
func createHandleStorageStatus(f *testhelper.FakePVE, node, storage string, totalBytes, usedBytes int64) {
	f.HandleJSON("GET /api2/json/nodes/"+node+"/storage/"+storage+"/status", map[string]any{
		"total": totalBytes,
		"used":  usedBytes,
		"avail": totalBytes - usedBytes,
	})
}

// createNoNFSReserve returns a *config.Config with storage.nfs_reserved_gb
// explicitly set to 0, isolating a capacity-gate test to the refquota and/or
// peppi-actuals ("used") terms without the default 1024G NFS reserve
// (config.DefaultNFSReservedGB) also contributing to the ratio.
func createNoNFSReserve(labs map[string]*config.Lab) *config.Config {
	return &config.Config{Labs: labs, Storage: config.ConfigStorage{NFSReservedGB: createPtr(0)}}
}

// TestCreateCapacityGate_BelowWarnThreshold_NoNote covers the quiet path:
// aggregate refquota reservation, live pool usage, and the default NFS
// reserve together well under 75% of the pool's reported size produce no
// capacity-gate note in the output.
func TestCreateCapacityGate_BelowWarnThreshold_NoNote(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.RefquotaGB = 100 // 100G refquota + 0G used + 1024G default NFS reserve, against a 10T pool

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 10*1024*1024*1024*1024, 0) // 10 TiB total, 0 used
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9800")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.NotContains(t, out, "capacity gate")
}

// TestCreateCapacityGate_AboveWarnThreshold_AddsWarningNote covers the
// warning path: aggregate reservation (refquota + live used + the default
// NFS reserve) over 75% of the pool's reported size (but under 85%) still
// creates the lab, with a WARNING note in the output naming all three terms.
func TestCreateCapacityGate_AboveWarnThreshold_AddsWarningNote(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.RefquotaGB = 7000 // 7000G refquota + 0G used + 1024G default NFS reserve = 8024G

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 10000*1024*1024*1024, 0) // 10000G total: 8024/10000 = 80.24%
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9800")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "capacity gate WARNING")
	assert.Contains(t, out, "reserve 7000G", "must name the refquota-sum term")
	assert.Contains(t, out, "usage 0G", "must name the peppi-actuals (\"used\") term")
	assert.Contains(t, out, "NFS reserve 1024G", "must name the default NFS-reserve term")
	require.Len(t, qemuCreateRec, 1, "a warning does not block creation")
}

// TestCreateCapacityGate_AboveRefuseThreshold_RefusesWithoutForce covers the
// hard-refusal path: aggregate reservation (refquota + live used + the
// default NFS reserve) over 85% of the pool's reported size refuses create
// entirely, before any VMID is allocated or any VM step added, unless
// --force is passed.
func TestCreateCapacityGate_AboveRefuseThreshold_RefusesWithoutForce(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.RefquotaGB = 900 // 900G + 0G used + 1024G default NFS reserve = 1924G against a 1000G pool

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 1000*1024*1024*1024, 0)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "capacity gate")
	assert.ErrorContains(t, err, "--force")
}

// TestCreateCapacityGate_ForceOverridesRefusal covers --force bypassing the
// 85% refusal threshold, still creating the VM.
func TestCreateCapacityGate_ForceOverridesRefusal(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.RefquotaGB = 900

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 1000*1024*1024*1024, 0)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9900")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--force")
	require.NoError(t, err)
	require.Len(t, qemuCreateRec, 1)
}

// TestCreateCapacityGate_NFSReserveIncludedByDefault covers decision D1
// (amended): the tank/nfs quota reserve counts in the capacity-gate
// numerator even when no lab config mentions it at all. A refquota/pool
// combination that would stay well under the warning threshold on its own
// (200G against a 1600G pool: 12.5%) crosses it once config.DefaultNFSReservedGB
// (1024G) is added in (200+1024=1224G, 76.5%), proving the default reserve
// is applied even without any explicit storage.nfs_reserved_gb config.
func TestCreateCapacityGate_NFSReserveIncludedByDefault(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.RefquotaGB = 200

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 1600*1024*1024*1024, 0)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9800")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	// No storage.nfs_reserved_gb set at all: the default must still apply.
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "capacity gate WARNING",
		"the default 1024G NFS reserve alone must be enough to cross the 75% warning threshold here")
	assert.Contains(t, out, "NFS reserve 1024G")
}

// TestCreateCapacityGate_NFSReserveOverriddenToZero covers the opt-out path
// (multi-node lab plan §10 decision D1, amended: "operators set it to 0"
// once NFS moves to its own dedicated nfs_pool): the same refquota/pool
// combination that trips the warning threshold under the default NFS
// reserve (TestCreateCapacityGate_NFSReserveIncludedByDefault) must stay
// clean once storage.nfs_reserved_gb is explicitly 0.
func TestCreateCapacityGate_NFSReserveOverriddenToZero(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.RefquotaGB = 200

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 1600*1024*1024*1024, 0)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9800")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, createNoNFSReserve(map[string]*config.Lab{"wayne": lab}))
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.NotContains(t, out, "capacity gate",
		"storage.nfs_reserved_gb: 0 must remove the NFS term entirely, leaving this well under the warning threshold")
}

// TestCreateCapacityGate_PeppiActualUsageCounted covers the "peppi actuals"
// term (multi-node lab plan §3.4: "sum of all lab refquotas + peppi actuals
// vs. pool size"): a small lab refquota alone would stay well under the
// warning threshold, but the pool's live "used" figure (standing in for
// peppi's actual usage, per createCapacityGate's documented conservative
// estimate) alone crosses it. NFS reserve is zeroed out here to isolate the
// "used" term from the default NFS reserve's own contribution.
func TestCreateCapacityGate_PeppiActualUsageCounted(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Storage.RefquotaGB = 100 // 100G refquota + 1500G used + 0G NFS reserve = 1600G

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 2000*1024*1024*1024, 1500*1024*1024*1024) // 1600/2000 = 80%
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
	f.HandleJSON("GET /api2/json/cluster/nextid", "9800")

	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	path := writeConfig(t, createNoNFSReserve(map[string]*config.Lab{"wayne": lab}))
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "capacity gate WARNING")
	assert.Contains(t, out, "usage 1500G", "the pool's live \"used\" figure must be counted as the peppi-actuals term")
	require.Len(t, qemuCreateRec, 1)
}

// TestCreateCapacityGate_UsesEffectiveLabNotOnDiskConfig covers M2-03: the
// gate must substitute eff (this invocation's flag-overridden topology) for
// the on-disk config entry of the same name, not the stale pre-override
// figure. The on-disk lab leaves topology.nodes unset (defaults to 1,
// EffectiveRefquotaGB 480G); --nodes 5 raises the reservation to the
// 5-node default (5*264=1320G). Against a 1600G pool, the on-disk figure
// alone would stay under the 75% warning threshold (480/1600=30%), but the
// --nodes 5 override crosses it (1320/1600=82.5%) — proving the gate
// reflects the override, not the stale on-disk topology.
func TestCreateCapacityGate_UsesEffectiveLabNotOnDiskConfig(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne") // on-disk: topology.nodes unset (defaults to 1)
	// cleanLab (createTestLab's base) sets an explicit storage.refquota_gb
	// (50G), which would otherwise override the profile-derived default
	// regardless of node count and defeat this test's premise; clear it so
	// EffectiveRefquotaGB falls through to the 480G(1-node)/264G-per-node
	// profile default this test is exercising.
	lab.Storage.RefquotaGB = 0

	createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
	createHandleStorageStatus(f, "node1", "tank", 1600*1024*1024*1024, 0)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})

	path := writeConfig(t, createNoNFSReserve(map[string]*config.Lab{"wayne": lab}))
	cmd := buildCreateCmd(t, path, f, "node1")

	out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--nodes", "5", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "capacity gate WARNING",
		"the --nodes 5 override must push the gate's reservation figure above the on-disk (1-node) value")
	assert.Contains(t, out, "reserve 1320G",
		"the numerator must use the 5-node refquota default (5*264G), not the on-disk 1-node default (480G)")
}
