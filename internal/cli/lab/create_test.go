package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		[]any{map[string]any{"zone": "labsvxlan"}}, 200)
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{
			"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR, "gateway": "10.10.1.1", "zone": labZoneName,
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
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
				{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200},
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank-lab-wayne"}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": lab.Access.Pool}})

	f.HandleFunc("GET /api2/json/pools/lab-wayne", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"members": []map[string]any{
				{"id": "qemu/9200", "node": "node2", "type": "qemu", "vmid": 9200},
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
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
// verbs that both provision the shared labsvxlan zone: `pmx lab create`'s
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
	assert.Equal(t, labZoneName, create.body["zone"])
	assert.Equal(t, labZoneType, create.body["type"])
	assert.Equal(t, labZonePeers, create.body["peers"], "must match net.go's ensureLabSdnZone peers")
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
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

	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labsvxlan"}})
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
