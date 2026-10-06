package cpi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

const apiPrefix = "/api2/json"

// fakeCluster answers every API request from its own exact-path tables, so a
// path the test did not set up gets a 404 instead of falling through to one
// of FakePVE's prefix routes. It records every request and fails the test on
// any method other than GET: the audit must never change the cluster.
type fakeCluster struct {
	t *testing.T
	f *testhelper.FakePVE

	mu       sync.Mutex
	payloads map[string]any
	statuses map[string]int
	requests []string

	// onRequest, when set, sees each request path before it is answered.
	onRequest func(path string)
}

func newFakeCluster(t *testing.T) *fakeCluster {
	t.Helper()
	c := &fakeCluster{
		t: t, f: testhelper.NewFakePVE(t),
		payloads: map[string]any{
			"/version": map[string]any{"release": "9.0", "version": "9.0.10", "repoid": "x"},
			"/cluster/status": []any{
				map[string]any{"type": "node", "name": "pve1", "id": "node/pve1", "online": 1, "nodeid": 1},
			},
			"/nodes":              []any{map[string]any{"node": "pve1", "status": "online"}},
			"/cluster/resources":  []any{},
			"/access/permissions": map[string]any{"/vms": map[string]any{"VM.Audit": 1}},
			"/pools":              []any{},
		},
		statuses: map[string]int{},
	}
	handler := http.HandlerFunc(c.serve)
	// Each FakePVE default is registered for GET on an exact path, which
	// outranks a catch-all, so this handler takes over those paths for every
	// method and then catches everything else under the API root.
	for _, p := range []string{"/version", "/cluster/status", "/cluster/resources", "/nodes"} {
		c.f.Handle("GET "+apiPrefix+p, handler)
		c.f.Handle(apiPrefix+p, handler)
	}
	c.f.Handle(apiPrefix+"/", handler)
	return c
}

func (c *fakeCluster) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, apiPrefix)
	c.mu.Lock()
	c.requests = append(c.requests, r.Method+" "+path)
	payload, ok := c.payloads[path]
	status := c.statuses[path]
	hook := c.onRequest
	c.mu.Unlock()
	if hook != nil {
		hook(path)
	}
	if r.Method != http.MethodGet {
		c.t.Errorf("disk-audit issued %s %s; the audit must only read", r.Method, r.URL.String())
		testhelper.WriteError(w, http.StatusMethodNotAllowed, "read-only test fixture")
		return
	}
	switch {
	case status != 0:
		testhelper.WriteError(w, status, http.StatusText(status))
	case ok:
		testhelper.WriteData(w, payload)
	default:
		testhelper.WriteError(w, http.StatusNotFound, "no such path "+path)
	}
}

func (c *fakeCluster) set(path string, payload any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payloads[path] = payload
}

func (c *fakeCluster) fail(path string, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statuses[path] = status
}

func (c *fakeCluster) requested(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.requests, "GET "+path)
}

func (c *fakeCluster) endpoint(t *testing.T) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(c.f.Server.Listener.Addr().String())
	require.NoError(t, err)
	return host, port
}

// guests sets the cluster index and each guest's pending rows. A guest whose
// config is nil gets no pending route, so its read answers 404.
func (c *fakeCluster) guests(rows []map[string]any, configs map[int]map[string]string) {
	list := make([]any, 0, len(rows))
	for _, r := range rows {
		list = append(list, r)
	}
	c.set("/cluster/resources", list)
	for id, cfg := range configs {
		pending := []any{}
		for _, k := range sortedKeys(cfg) {
			pending = append(pending, map[string]any{"key": k, "value": cfg[k]})
		}
		c.set(fmt.Sprintf("/nodes/pve1/qemu/%d/pending", id), pending)
	}
}

// storage sets one images storage on node and the volumes on it.
func (c *fakeCluster) storage(node, name string, volumes ...string) {
	c.set("/nodes/"+node+"/storage", []any{map[string]any{"storage": name, "content": "images,rootdir"}})
	content := []any{}
	for _, v := range volumes {
		content = append(content, map[string]any{"volid": v, "size": 1 << 30, "format": "raw"})
	}
	c.set("/nodes/"+node+"/storage/"+name+"/content", content)
}

func writeTestConfig(t *testing.T, host, port string) string {
	t.Helper()
	cfg := fmt.Sprintf(`current-context: fake
contexts:
  fake:
    host: %s
    port: %s
    protocol: http
    realm: pam
    auth:
      type: token
      username: root
      token-id: test
      secret: 00000000-0000-0000-0000-000000000000
`, host, port)
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

type cmdResult struct {
	stdout, stderr string
	err            error
}

func (r cmdResult) code() int { return exitcode.FromError(r.err) }

// runAudit drives the real root command with the cpi group attached.
func runAudit(t *testing.T, host, port string, args ...string) cmdResult {
	t.Helper()
	return runAuditCtx(t, context.Background(), host, port, args...)
}

func runAuditCtx(t *testing.T, ctx context.Context, host, port string, args ...string) cmdResult {
	t.Helper()
	t.Setenv("PMX_OUTPUT", "")
	t.Setenv("PMX_CONTEXT", "")
	if _, set := os.LookupEnv("PMX_NODE"); !set {
		t.Setenv("PMX_NODE", "")
	}

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(ctx)
	root.AddCommand(Group(&cli.Deps{}))
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--config", writeTestConfig(t, host, port), "--no-log", "cpi", "disk-audit"},
		args...))
	err := root.Execute()
	return cmdResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (c *fakeCluster) run(t *testing.T, args ...string) cmdResult {
	t.Helper()
	host, port := c.endpoint(t)
	return runAudit(t, host, port, args...)
}

// populate builds a cluster with one disk of each classification, a parker
// with a sentinel, and a volume two guests name.
func populate(t *testing.T, c *fakeCluster) {
	t.Helper()
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{testBPD: map[string]any{
		"volid": "a:vm-90656-disk-0", "disk_cid": "cid-1", "node": "pve1",
		"source_vm_cid": "vm-1", "parked_at": "2026-01-01T00:00:00Z", "director_id": "dir-1",
	}}})
	c.guests(fixRows(), map[int]map[string]string{
		777:   {"scsi1": "a:vm-9001-disk-0,size=1G"},
		778:   {"unused0": "a:vm-9001-disk-0"},
		500:   {},
		90656: {"scsi0": "a:vm-90656-disk-0", "description": desc},
	})
	c.storage("pve1", "a", "a:vm-9001-disk-0", "a:vm-9002-disk-0", "a:vm-90656-disk-0")
}

func TestDiskAuditCmd_OnlyIssuesGETsToTheDocumentedEndpoints(t *testing.T) {
	c := newFakeCluster(t)
	populate(t, c)
	res := c.run(t, "--node", "pve1")
	require.Equal(t, exitcode.AuditFindings, res.code(), res.stderr)

	allowed := []string{"/nodes", "/cluster/resources", "/nodes/pve1/storage", "/nodes/pve1/storage/a/content",
		"/access/permissions", "/pools", "/version", "/cluster/status"}
	for _, r := range c.requests {
		method, path, _ := strings.Cut(r, " ")
		assert.Equal(t, http.MethodGet, method, r)
		if strings.HasPrefix(path, "/nodes/pve1/qemu/") {
			assert.True(t, strings.HasSuffix(path, "/pending"), r)
			continue
		}
		assert.Contains(t, allowed, path, r)
	}
	for _, p := range []string{"/cluster/resources", "/nodes/pve1/storage", "/nodes/pve1/storage/a/content",
		"/access/permissions", "/pools", "/nodes/pve1/qemu/777/pending"} {
		assert.True(t, c.requested(p), p)
	}
}

func TestDiskAuditCmd_JSONGolden(t *testing.T) {
	c := newFakeCluster(t)
	populate(t, c)
	res := c.run(t, "-o", "json")
	require.Equal(t, exitcode.AuditFindings, res.code(), res.stderr)

	host, port := c.endpoint(t)
	want := `{"host":"` + host + `","port":` + port + `,"disk_band":[9000,29999],"parker_band":[90000,90999],` +
		`"summary":{"total":3,"attached":1,"parked":1,"free_floating":1,"unknown":0,"multiply_referenced":1},` +
		`"disks":[` +
		`{"volid":"a:vm-9001-disk-0","storage":"a","node":"pve1","size_bytes":1073741824,` +
		`"classification":"attached","holder_vmid":777,"holder_node":"pve1","holder_name":"web-0",` +
		`"discovered_by":["band"]},` +
		`{"volid":"a:vm-9002-disk-0","storage":"a","node":"pve1","size_bytes":1073741824,` +
		`"classification":"free-floating","discovered_by":["band"]},` +
		`{"volid":"a:vm-90656-disk-0","storage":"a","node":"pve1","size_bytes":1073741824,` +
		`"classification":"parked","holder_vmid":90656,"holder_node":"pve1","holder_name":"bosh-parker-90656",` +
		`"disk_cid":"cid-1","source_vm_cid":"vm-1","parked_at":"2026-01-01T00:00:00Z","parked_node":"pve1",` +
		`"director_id":"dir-1","discovered_by":["parker","sentinel"]}],` +
		`"parkers":[{"vmid":90656,"node":"pve1","name":"bosh-parker-90656","pool":"","disk_count":1,` +
		`"unused_count":0,"config_read":true,"slot_capacity":31,"empty":false}],` +
		`"skipped_storages":[],` +
		`"multiply_referenced":[{"volid":"a:vm-9001-disk-0","owner_vmid":9001,"owner_present":false,` +
		`"owner_holds_reference":false,"references":[` +
		`{"vmid":777,"node":"pve1","name":"web-0","slot":"scsi1","kind":"active","parker":false,"owns":false},` +
		`{"vmid":778,"node":"pve1","name":"web-1","slot":"unused0","kind":"unused","parker":false,"owns":false}]}],` +
		`"multiply_referenced_unreadable_vmids":[],"multiply_referenced_visibility":"full",` +
		`"multiply_referenced_complete":true}`
	var got bytes.Buffer
	require.NoError(t, json.Compact(&got, []byte(res.stdout)), res.stdout)
	assert.Equal(t, want, got.String())
	assert.NotContains(t, res.stdout, "WARN")
	assert.Contains(t, res.stderr, "WARN: volume a:vm-9001-disk-0 is named by 2 guests")
}

func TestDiskAuditCmd_YAMLIsTheJSONDocument(t *testing.T) {
	c := newFakeCluster(t)
	populate(t, c)
	jsonRes := c.run(t, "-o", "json")
	yamlRes := c.run(t, "-o", "yaml")
	require.Equal(t, exitcode.AuditFindings, yamlRes.code())

	var fromJSON, fromYAML any
	require.NoError(t, json.Unmarshal([]byte(jsonRes.stdout), &fromJSON))
	require.NoError(t, yaml.Unmarshal([]byte(yamlRes.stdout), &fromYAML))
	normalized, err := json.Marshal(fromYAML)
	require.NoError(t, err)
	var roundTrip any
	require.NoError(t, json.Unmarshal(normalized, &roundTrip))
	assert.Equal(t, fromJSON, roundTrip)

	var ordered yaml.MapSlice
	require.NoError(t, yaml.Unmarshal([]byte(yamlRes.stdout), &ordered))
	var keys []string
	for _, item := range ordered {
		keys = append(keys, fmt.Sprint(item.Key))
	}
	assert.Equal(t, []string{"host", "port", "disk_band", "parker_band", "summary", "disks", "parkers",
		"skipped_storages", "multiply_referenced", "multiply_referenced_unreadable_vmids",
		"multiply_referenced_visibility", "multiply_referenced_complete"}, keys)
}

func TestDiskAuditCmd_HumanReportGoesToStdoutAndWarningsToStderr(t *testing.T) {
	c := newFakeCluster(t)
	populate(t, c)
	res := c.run(t)
	require.Equal(t, exitcode.AuditFindings, res.code())
	host, port := c.endpoint(t)
	assert.True(t, strings.HasPrefix(res.stdout, "\nBOSH Persistent Disk Audit\n  Host:  "+host+":"+port+"\n"),
		res.stdout)
	assert.Contains(t, res.stdout, "FREE-FLOATING DISKS  [WARNING: potential orphans]")
	assert.Contains(t, res.stdout, "PARKER VMs\n")
	assert.NotContains(t, res.stdout, "WARN:")
	assert.Contains(t, res.stderr, "WARN: volume a:vm-9001-disk-0 is named by 2 guests")
	assert.Equal(t, "EXIT 9: 1 free-floating disk(s) found.", res.err.Error())
}

func TestDiskAuditCmd_CleanRunExitsZero(t *testing.T) {
	c := newFakeCluster(t)
	c.guests(fixRows(), map[int]map[string]string{
		777: {"scsi1": "a:vm-9001-disk-0"}, 778: {}, 500: {}, 90656: {"scsi0": "a:vm-90656-disk-0"},
	})
	c.storage("pve1", "a", "a:vm-9001-disk-0", "a:vm-90656-disk-0")
	res := c.run(t)
	require.NoError(t, res.err, res.stderr)
	assert.Equal(t, exitcode.OK, res.code())
	assert.Empty(t, res.stderr)
	assert.Contains(t, res.stdout, "    free-floating: 0\n")
}

func TestDiskAuditCmd_CancelledAuditPrintsNoReportAndExitsNonZero(t *testing.T) {
	c := newFakeCluster(t)
	populate(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The operator presses ^C once the audit has read every guest and asks
	// for the principal's permissions.
	c.onRequest = func(path string) {
		if path == "/access/permissions" {
			cancel()
		}
	}
	host, port := c.endpoint(t)
	res := runAuditCtx(t, ctx, host, port, "--node", "pve1")
	require.Error(t, res.err)
	require.ErrorIs(t, res.err, context.Canceled)
	assert.NotEqual(t, exitcode.OK, res.code())
	assert.NotEqual(t, exitcode.AuditFindings, res.code())
	assert.Empty(t, res.stdout)
}

func TestDiskAuditCmd_EmptyClusterExitsZero(t *testing.T) {
	c := newFakeCluster(t)
	res := c.run(t, "-o", "json")
	require.NoError(t, res.err, res.stderr)
	assert.Contains(t, res.stdout, `"total": 0`)
}

func TestDiskAuditCmd_BadFlagValuesExitTwoBeforeAnyRead(t *testing.T) {
	cases := [][]string{
		{"--disk-band", "29999-9000"},
		{"--disk-band", "9000-9000"},
		{"--disk-band", "0-0"},
		{"--disk-band", "9000"},
		{"--disk-band", "a-b"},
		{"--disk-band", "-1-5"},
		{"--parker-band", "90999-90000"},
		{"--parker-band", "90000-90000"},
		{"--parker-band", "90000-"},
		{"--detached-disk-strategy", "delete"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c := newFakeCluster(t)
			res := c.run(t, args...)
			require.Error(t, res.err)
			assert.Equal(t, exitcode.BadArgs, res.code(), res.err.Error())
			assert.Contains(t, res.err.Error(), "invalid --")
			assert.False(t, c.requested("/cluster/resources"))
		})
	}
}

func TestDiskAuditCmd_CustomBandsAndStrategy(t *testing.T) {
	c := newFakeCluster(t)
	rows := []map[string]any{row(95000, "bosh-parker-95000", "bosh-parker", "qemu")}
	c.guests(rows, map[int]map[string]string{95000: {"scsi0": "a:vm-95000-disk-0"}})
	c.storage("pve1", "a", "a:vm-95000-disk-0", "a:vm-10000-disk-0", "a:vm-9001-disk-0")
	res := c.run(t, "--disk-band", "10000-19999", "--parker-band", "95000-95999",
		"--detached-disk-strategy", "free", "-o", "json")
	require.Equal(t, exitcode.AuditFindings, res.code(), res.stderr)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &doc))
	assert.Equal(t, []any{float64(10000), float64(19999)}, doc["disk_band"])
	assert.Equal(t, []any{float64(95000), float64(95999)}, doc["parker_band"])
	var volids []string
	for _, d := range doc["disks"].([]any) {
		volids = append(volids, d.(map[string]any)["volid"].(string))
	}
	assert.Equal(t, []string{"a:vm-10000-disk-0", "a:vm-95000-disk-0"}, volids)
	assert.Contains(t, res.stderr, "WARN: 1 parked disk(s) found but detached_disk_strategy='free'.")
}

func TestDiskAuditCmd_EmptyStrategyMeansParked(t *testing.T) {
	c := newFakeCluster(t)
	c.guests(fixRows(), map[int]map[string]string{90656: {"scsi0": "a:vm-90656-disk-0"}, 777: {}, 778: {}, 500: {}})
	c.storage("pve1", "a", "a:vm-90656-disk-0")
	res := c.run(t, "--detached-disk-strategy", "")
	require.NoError(t, res.err, res.stderr)
	assert.NotContains(t, res.stderr, "detached_disk_strategy")
}

func TestDiskAuditCmd_ParkerPoolFlag(t *testing.T) {
	c := newFakeCluster(t)
	rows := []map[string]any{row(140, "bosh-web-0", "", "qemu"), row(141, "bosh-web-1", "", "qemu"),
		row(142, "bosh-web-2", "", "qemu")}
	rows[0]["pool"] = "blue-parker"
	rows[1]["pool"] = "green-parker"
	rows[2]["pool"] = "workload"
	c.guests(rows, map[int]map[string]string{140: {}, 141: {}, 142: {}})

	res := c.run(t)
	require.NoError(t, res.err)
	assert.NotContains(t, res.stderr, "sits in a parker pool")

	res = c.run(t, "--parker-pool", "blue-parker", "--parker-pool", " green-parker, ,blue-parker")
	require.NoError(t, res.err)
	assert.Contains(t, res.stderr, "WARN: VM 140 (bosh-web-0, pool blue-parker)")
	assert.Contains(t, res.stderr, "WARN: VM 141 (bosh-web-1, pool green-parker)")
	assert.NotContains(t, res.stderr, "VM 142")
}

func twoNodeCluster(t *testing.T) *fakeCluster {
	t.Helper()
	c := newFakeCluster(t)
	c.set("/nodes", []any{
		map[string]any{"node": "pve1", "status": "online"},
		map[string]any{"node": "pve2", "status": "online"},
		map[string]any{"node": "pve3", "status": "offline"},
	})
	c.storage("pve1", "a", "a:vm-9001-disk-0")
	c.storage("pve2", "b", "b:vm-9002-disk-0")
	return c
}

func TestDiskAuditCmd_ExplicitNodeScopesTheStorageScan(t *testing.T) {
	c := twoNodeCluster(t)
	res := c.run(t, "--node", "pve2", "-o", "json")
	require.Equal(t, exitcode.AuditFindings, res.code())
	assert.True(t, c.requested("/nodes/pve2/storage"))
	assert.False(t, c.requested("/nodes/pve1/storage"))
	assert.Contains(t, res.stdout, `"volid": "b:vm-9002-disk-0"`)
	assert.NotContains(t, res.stdout, "a:vm-9001-disk-0")

	human := c.run(t, "--node", "pve2")
	assert.Contains(t, human.stdout, "  Scope: node=pve2\n")
}

func TestDiskAuditCmd_AmbientNodeDoesNotScopeTheScan(t *testing.T) {
	c := twoNodeCluster(t)
	t.Setenv("PMX_NODE", "pve2")
	res := c.run(t)
	require.Equal(t, exitcode.AuditFindings, res.code())
	assert.True(t, c.requested("/nodes/pve1/storage"))
	assert.True(t, c.requested("/nodes/pve2/storage"))
	assert.False(t, c.requested("/nodes/pve3/storage"), "an offline node is not scanned")
	assert.NotContains(t, res.stdout, "Scope:")
	assert.Equal(t, "EXIT 9: 2 free-floating disk(s) found.", res.err.Error())
}

func TestDiskAuditCmd_StepEightSoftFailuresAreListed(t *testing.T) {
	c := newFakeCluster(t)
	c.guests([]map[string]any{row(777, "a", "", "qemu"), row(778, "b", "", "qemu"), row(779, "c", "", "qemu"),
		row(780, "d", "", "qemu")}, map[int]map[string]string{
		779: {"scsi1": "a:779/vm-779-disk-1.raw"},
		780: {"unused0": "a:779/vm-779-disk-1.raw"},
	})
	c.fail("/nodes/pve1/qemu/777/pending", http.StatusInternalServerError)
	c.fail("/nodes/pve1/qemu/778/pending", http.StatusForbidden)
	res := c.run(t, "-o", "json")
	require.NoError(t, res.err, res.stderr)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &doc))
	assert.Equal(t, []any{float64(777), float64(778)}, doc["multiply_referenced_unreadable_vmids"])
	assert.Equal(t, false, doc["multiply_referenced_complete"])
	assert.Len(t, doc["multiply_referenced"], 1)
	assert.Contains(t, res.stderr, "WARN: the configs of 2 QEMU guest(s) did not come back (777, 778)")
}

func TestDiskAuditCmd_PermissionsReadFailureMakesVisibilityUnknown(t *testing.T) {
	c := newFakeCluster(t)
	c.fail("/access/permissions", http.StatusForbidden)
	res := c.run(t, "-o", "json")
	require.NoError(t, res.err, res.stderr)
	assert.Contains(t, res.stdout, `"multiply_referenced_visibility": "unknown"`)
	assert.Contains(t, res.stderr, "could not read this token's permissions on /vms (HTTP 403 ")
}

func TestDiskAuditCmd_LimitedVisibility(t *testing.T) {
	c := newFakeCluster(t)
	c.set("/access/permissions", map[string]any{"/vms": map[string]any{"VM.Allocate": 1}})
	res := c.run(t, "-o", "json")
	require.NoError(t, res.err)
	assert.Contains(t, res.stdout, `"multiply_referenced_visibility": "limited"`)
	assert.Contains(t, res.stderr, "lacks VM.Audit on /vms")
}

func TestDiskAuditCmd_FailedHardPendingReadFailsClosed(t *testing.T) {
	c := newFakeCluster(t)
	c.guests([]map[string]any{row(777, "web-0", "", "qemu")}, nil)
	c.storage("pve1", "a", "a:vm-777-disk-1")
	c.fail("/nodes/pve1/qemu/777/pending", http.StatusInternalServerError)
	res := c.run(t)
	require.Error(t, res.err)
	assert.NotEqual(t, exitcode.AuditFindings, res.code())
	assert.NotEqual(t, exitcode.OK, res.code())
	assert.Contains(t, res.err.Error(), "GET /nodes/pve1/qemu/777/pending failed")
	assert.Empty(t, res.stdout)
}

func TestDiskAuditCmd_ForbiddenHardReadMapsToAuth(t *testing.T) {
	c := newFakeCluster(t)
	c.guests([]map[string]any{row(777, "web-0", "", "qemu")}, nil)
	c.storage("pve1", "a", "a:vm-777-disk-1")
	c.fail("/nodes/pve1/qemu/777/pending", http.StatusForbidden)
	res := c.run(t)
	assert.Equal(t, exitcode.Auth, res.code(), res.err)
}

func TestDiskAuditCmd_MissingGuestReadsAsNoConfig(t *testing.T) {
	c := newFakeCluster(t)
	c.guests([]map[string]any{row(777, "web-0", "", "qemu")}, nil)
	c.storage("pve1", "a", "a:vm-9001-disk-0")
	res := c.run(t)
	require.Equal(t, exitcode.AuditFindings, res.code(), res.err)
	assert.Contains(t, res.stderr, "WARN: 1 guest config(s) could not be read")
}

// A reply that is not a list fails the soft read too, so the hard read runs
// and ends the audit.
func TestDiskAuditCmd_NonListPendingReplyEndsTheRun(t *testing.T) {
	c := newFakeCluster(t)
	c.guests([]map[string]any{row(777, "web-0", "", "qemu")}, nil)
	c.storage("pve1", "a", "a:vm-777-disk-1")
	c.set("/nodes/pve1/qemu/777/pending", map[string]any{"key": "scsi0"})
	res := c.run(t)
	require.Error(t, res.err)
	assert.Equal(t, exitcode.Generic, res.code())
	assert.Equal(t, "GET /nodes/pve1/qemu/777/pending: expected a list of config rows, got object", res.err.Error())
}

// The hard read refuses a reply it cannot read as config rows. The soft
// read keeps the rows it can read, as the script's does.
func TestPveReader_MalformedPendingReply(t *testing.T) {
	cases := map[string]struct {
		payload any
		want    string
	}{
		"an object":            {map[string]any{"key": "scsi0"}, "expected a list of config rows, got object"},
		"a string":             {"scsi0", "expected a list of config rows, got string"},
		"a non-object row":     {[]any{map[string]any{"key": "scsi0", "value": "a:vm-1-disk-0"}, "oops"}, `"oops"`},
		"a row without a key":  {[]any{map[string]any{"value": "x"}}, `malformed config row {"value":"x"}`},
		"a row with a non-str": {[]any{map[string]any{"key": 5}}, `malformed config row {"key":5}`},
		"a row with empty key": {[]any{map[string]any{"key": ""}}, `malformed config row {"key":""}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := newFakeCluster(t)
			c.set("/nodes/pve1/qemu/777/pending", tc.payload)
			r := &pveReader{raw: c.f.MustNewClient(t)}
			_, err := r.vmViews(context.Background(), "pve1", 777)
			require.Error(t, err)
			assert.Equal(t, exitcode.Generic, exitcode.FromError(err))
			assert.Contains(t, err.Error(), "GET /nodes/pve1/qemu/777/pending: ")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDiskAuditCmd_StorageContentFailureFailsClosed(t *testing.T) {
	c := newFakeCluster(t)
	c.storage("pve1", "a", "a:vm-9001-disk-0")
	c.fail("/nodes/pve1/storage/a/content", http.StatusInternalServerError)
	res := c.run(t)
	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "GET /nodes/pve1/storage/a/content?content=images failed")
}

func TestDiskAuditCmd_DisabledStorageIsSkipped(t *testing.T) {
	c := newFakeCluster(t)
	c.set("/nodes/pve1/storage", []any{
		map[string]any{"storage": "off", "content": "images", "enabled": 0},
		map[string]any{"storage": "dark", "content": "images", "active": 0},
	})
	res := c.run(t, "-o", "json")
	require.NoError(t, res.err)
	assert.False(t, c.requested("/nodes/pve1/storage/off/content"))
	assert.False(t, c.requested("/nodes/pve1/storage/dark/content"))
	assert.Contains(t, res.stderr, "SKIPPED: node pve1 storage off: ")
	assert.Contains(t, res.stderr, "SKIPPED: node pve1 storage dark: ")
	var doc struct {
		Skipped []map[string]string `json:"skipped_storages"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &doc))
	require.Len(t, doc.Skipped, 2)
	names := []string{doc.Skipped[0]["storage"], doc.Skipped[1]["storage"]}
	sort.Strings(names)
	assert.Equal(t, []string{"dark", "off"}, names)
}

func TestDiskAuditCmd_TransportFailureMapsToInfra(t *testing.T) {
	c := newFakeCluster(t)
	host, port := c.endpoint(t)
	c.f.Server.Close()
	res := runAudit(t, host, port)
	require.Error(t, res.err)
	assert.Equal(t, exitcode.Infra, res.code(), res.err.Error())
}

func TestDiskAuditCmd_RejectsArguments(t *testing.T) {
	c := newFakeCluster(t)
	res := c.run(t, "extra")
	require.Error(t, res.err)
	assert.False(t, c.requested("/cluster/resources"))
}

func TestPveReader_PendingViewsHardAndSoftReadTheSameEndpoint(t *testing.T) {
	c := newFakeCluster(t)
	c.set("/nodes/pve1/qemu/777/pending", []any{
		map[string]any{"key": "scsi0", "value": "a:vm-1-disk-0", "pending": "a:vm-2-disk-0"},
		map[string]any{"key": "scsi1", "value": "a:vm-3-disk-0", "delete": 1},
	})
	r := &pveReader{raw: c.f.MustNewClient(t)}
	hard, err := r.vmViews(context.Background(), "pve1", 777)
	require.NoError(t, err)
	soft := r.vmViewsSoft(context.Background(), "pve1", 777)
	assert.Equal(t, hard, soft)
	assert.Equal(t, map[string]string{"scsi0": "a:vm-1-disk-0", "scsi1": "a:vm-3-disk-0"}, hard.current)
	assert.Equal(t, map[string]string{"scsi0": "a:vm-2-disk-0"}, hard.applied)
	assert.Equal(t, []string{"GET /nodes/pve1/qemu/777/pending", "GET /nodes/pve1/qemu/777/pending"}, c.requests)
}

func TestPveReader_MissingAndFailedReads(t *testing.T) {
	c := newFakeCluster(t)
	c.fail("/nodes/pve1/qemu/778/pending", http.StatusNotImplemented)
	c.fail("/nodes/pve1/qemu/779/pending", http.StatusInternalServerError)
	r := &pveReader{raw: c.f.MustNewClient(t)}
	ctx := context.Background()

	views, err := r.vmViews(ctx, "pve1", 777)
	require.NoError(t, err, "404 reads as a missing guest")
	assert.Nil(t, views)
	views, err = r.vmViews(ctx, "pve1", 778)
	require.NoError(t, err, "501 reads as a missing guest")
	assert.Nil(t, views)
	_, err = r.vmViews(ctx, "pve1", 779)
	require.Error(t, err)
	assert.Nil(t, r.vmViewsSoft(ctx, "pve1", 779))

	_, err = r.storageContent(ctx, "pve1", "gone")
	require.NoError(t, err, "a storage that vanished reads as empty")
}
