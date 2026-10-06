package cpi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubClient is an inventoryClient that answers from fixed data. A guest
// whose views are absent reads as missing, the way a 404 does. It is safe
// for the parallel soft reads.
type stubClient struct {
	rows     []map[string]any
	nodes    []string
	storages []map[string]any
	content  map[string][]string
	views    map[int]*pendingViews

	failContent map[string]bool
	softFail    map[int]bool
	hardFail    map[int]bool
	privs       map[string]any
	permErr     string
	pools       []map[string]any
	poolErr     error

	// cancelOnSoft and cancelOnPerms cancel the run's context from inside the
	// matching read, the way ^C lands mid-audit.
	cancelOnSoft  context.CancelFunc
	cancelOnPerms context.CancelFunc
	cancelOnPools context.CancelFunc

	mu           sync.Mutex
	reads        []string
	hardReads    []int
	contentReads []string
}

var errStubRead = errors.New("stub: read failed")

func newStub(rows []map[string]any, configs map[int]map[string]string) *stubClient {
	views := map[int]*pendingViews{}
	for id, cfg := range configs {
		if cfg != nil {
			views[id] = &pendingViews{current: cfg, applied: cfg}
		}
	}
	return &stubClient{
		rows: rows, nodes: []string{"pve1"}, content: map[string][]string{}, views: views,
		failContent: map[string]bool{}, softFail: map[int]bool{}, hardFail: map[int]bool{},
		privs: map[string]any{"VM.Audit": float64(1)},
	}
}

func (s *stubClient) clusterResourcesVMs(context.Context) ([]map[string]any, error) {
	return s.rows, nil
}

func (s *stubClient) listNodes(context.Context) ([]string, error) { return s.nodes, nil }

func (s *stubClient) nodeStorages(context.Context, string) ([]map[string]any, error) {
	return s.storages, nil
}

func (s *stubClient) storageContent(_ context.Context, _, storage string) ([]map[string]any, error) {
	s.mu.Lock()
	s.contentReads = append(s.contentReads, storage)
	s.mu.Unlock()
	if s.failContent[storage] {
		return nil, errStubRead
	}
	var out []map[string]any
	for _, v := range s.content[storage] {
		out = append(out, map[string]any{"volid": v, "size": float64(1 << 30)})
	}
	return out, nil
}

func (s *stubClient) vmViews(_ context.Context, node string, vmid int) (*pendingViews, error) {
	s.mu.Lock()
	s.reads = append(s.reads, fmt.Sprintf("%s/%d", node, vmid))
	s.hardReads = append(s.hardReads, vmid)
	s.mu.Unlock()
	if s.hardFail[vmid] {
		return nil, errStubRead
	}
	return s.views[vmid], nil
}

func (s *stubClient) vmViewsSoft(_ context.Context, node string, vmid int) *pendingViews {
	s.mu.Lock()
	s.reads = append(s.reads, fmt.Sprintf("%s/%d", node, vmid))
	s.mu.Unlock()
	if s.cancelOnSoft != nil {
		s.cancelOnSoft()
		return nil
	}
	if s.softFail[vmid] {
		return nil
	}
	return s.views[vmid]
}

func (s *stubClient) vmsPermissions(context.Context) (map[string]any, string) {
	if s.cancelOnPerms != nil {
		s.cancelOnPerms()
		return nil, "context canceled"
	}
	if s.permErr != "" {
		return nil, s.permErr
	}
	return s.privs, ""
}

func (s *stubClient) listPools(context.Context) ([]map[string]any, error) {
	if s.cancelOnPools != nil {
		s.cancelOnPools()
		return nil, errStubRead
	}
	if s.poolErr != nil {
		return nil, s.poolErr
	}
	return s.pools, nil
}

func (s *stubClient) hardReadCount(vmid int) int {
	n := 0
	for _, v := range s.hardReads {
		if v == vmid {
			n++
		}
	}
	return n
}

func defaultOpts() auditOptions {
	return auditOptions{diskStart: 9000, diskEnd: 29999, parkerStart: 90000, parkerEnd: 90999, strategy: "parked"}
}

func images(name string, extra map[string]any) map[string]any {
	st := map[string]any{"storage": name, "content": "images,rootdir"}
	maps.Copy(st, extra)
	return st
}

func row(vmid int, name, tags, typ string) map[string]any {
	r := map[string]any{"vmid": float64(vmid), "node": "pve1", "name": name, "type": typ}
	if tags != "" {
		r["tags"] = tags
	}
	return r
}

// fixRows is the guest set most discovery tests run against.
func fixRows() []map[string]any {
	return []map[string]any{
		row(777, "web-0", "", "qemu"),
		row(778, "web-1", "", "qemu"),
		row(500, "operator-vm", "", "qemu"),
		row(90656, "bosh-parker-90656", "bosh-cpi;bosh-parker", "qemu"),
	}
}

type auditRun struct {
	inv    *inventory
	disks  map[string]*diskRecord
	stderr string
}

func runInventory(t *testing.T, client inventoryClient, opts auditOptions) auditRun {
	t.Helper()
	var errBuf bytes.Buffer
	inv, err := collectInventory(context.Background(), client, opts, &errBuf)
	require.NoError(t, err)
	disks := map[string]*diskRecord{}
	for _, d := range inv.disks {
		disks[d.volid] = d
	}
	return auditRun{inv: inv, disks: disks, stderr: errBuf.String()}
}

func diskStub(rows []map[string]any, configs map[int]map[string]string, volumes ...string) *stubClient {
	s := newStub(rows, configs)
	s.storages = []map[string]any{images("a", nil)}
	s.content["a"] = volumes
	return s
}

// The double-reference rows: a guest and a parker naming one volume, and a
// container that must never be read.
func doubleRows() []map[string]any {
	return []map[string]any{
		row(777, "web-0", "", "qemu"),
		row(90656, "bosh-parker-90656", "bosh-cpi;bosh-parker", "qemu"),
		row(200, "ct", "", "lxc"),
	}
}

func doubleConfigs() map[int]map[string]string {
	return map[int]map[string]string{
		777:   {"unused0": "a:123/vm-123-disk-0.raw"},
		90656: {"scsi1": "a:123/vm-123-disk-0.raw,serial=" + testBPD},
	}
}

func TestCollect_DoubleReference_ReadsGuestsAndSkipsTheContainer(t *testing.T) {
	client := newStub(doubleRows(), doubleConfigs())
	run := runInventory(t, client, defaultOpts())
	mr := run.inv.multiRef
	require.Len(t, mr.records, 1)
	assert.Equal(t, "a:123/vm-123-disk-0.raw", mr.records[0].volid)
	assert.Empty(t, mr.unreadableVMIDs)
	assert.NotContains(t, client.reads, "pve1/200")
	assert.True(t, mr.complete())
}

func TestCollect_DoubleReference_MissingVMAuditLimitsVisibility(t *testing.T) {
	client := newStub(doubleRows(), doubleConfigs())
	client.privs = map[string]any{}
	mr := runInventory(t, client, defaultOpts()).inv.multiRef
	assert.Equal(t, "limited", mr.visibility)
	assert.False(t, mr.complete())

	client.privs = map[string]any{"VM.Audit": float64(0)}
	assert.Equal(t, "limited", runInventory(t, client, defaultOpts()).inv.multiRef.visibility)
}

func TestCollect_DoubleReference_FailedPermissionsReadMakesVisibilityUnknown(t *testing.T) {
	client := newStub(doubleRows(), doubleConfigs())
	client.permErr = "HTTP 403 Forbidden"
	mr := runInventory(t, client, defaultOpts()).inv.multiRef
	assert.Equal(t, "unknown", mr.visibility)
	assert.Equal(t, "HTTP 403 Forbidden", mr.visibilityError)
	assert.False(t, mr.complete())
}

func TestCollect_StepEight_SoftFailuresAreListedAndTheAuditCompletes(t *testing.T) {
	rows := []map[string]any{
		{"vmid": float64(777), "node": "pve1", "type": "qemu"},
		{"vmid": float64(778), "node": "pve2", "type": "qemu"},
		{"vmid": float64(779), "node": "pve1", "type": "qemu"},
		{"vmid": float64(780), "node": "pve1", "type": "qemu"},
		{"vmid": float64(781), "type": "qemu"},
	}
	client := newStub(rows, map[int]map[string]string{
		777: {}, 778: {},
		779: {"scsi1": "a:779/vm-779-disk-1.raw"},
		780: {"unused0": "a:779/vm-779-disk-1.raw"},
	})
	client.softFail = map[int]bool{777: true, 778: true}
	client.hardFail = map[int]bool{777: true, 778: true}
	mr := runInventory(t, client, defaultOpts()).inv.multiRef
	assert.Equal(t, []int{777, 778, 781}, mr.unreadableVMIDs, "a guest with no node is unreadable too")
	require.Len(t, mr.records, 1)
	assert.Equal(t, "a:779/vm-779-disk-1.raw", mr.records[0].volid)
	assert.False(t, mr.complete())
	assert.Empty(t, client.hardReads, "with no volumes, step 8 reads softly only")
}

func TestCollect_DisabledStorages_AreSkippedWithALine(t *testing.T) {
	for _, flags := range []map[string]any{
		{"enabled": float64(0)}, {"active": float64(0)}, {"enabled": "0"}, {"active": "0"},
		{"active": false}, {"enabled": false},
	} {
		client := newStub(nil, nil)
		client.storages = []map[string]any{images("off", flags), images("on", nil)}
		client.content = map[string][]string{"off": {"off:vm-9001-disk-0"}, "on": {"on:vm-9002-disk-0"}}
		client.failContent = map[string]bool{"off": true}
		run := runInventory(t, client, defaultOpts())
		assert.NotContains(t, client.contentReads, "off", flags)
		assert.Equal(t, []string{"on:vm-9002-disk-0"}, volids(run.inv.disks), flags)
		assert.Contains(t, run.stderr, "SKIPPED: node pve1 storage off:", flags)
		assert.Equal(t, 1, bytes.Count([]byte(run.stderr), []byte("SKIPPED")), flags)
		require.Len(t, run.inv.skipped, 1)
		assert.Equal(t, "pve1", run.inv.skipped[0].node)
		assert.Equal(t, "off", run.inv.skipped[0].storage)
	}
}

func TestCollect_SetOrMissingStorageFlagsAreRead(t *testing.T) {
	for _, flags := range []map[string]any{
		nil, {"enabled": float64(1), "active": float64(1)}, {"enabled": "1", "active": "1"},
	} {
		client := newStub(nil, nil)
		client.storages = []map[string]any{images("on", flags)}
		client.content = map[string][]string{"on": {"on:vm-9002-disk-0"}}
		run := runInventory(t, client, defaultOpts())
		assert.Equal(t, []string{"on:vm-9002-disk-0"}, volids(run.inv.disks), flags)
		assert.NotContains(t, run.stderr, "SKIPPED")
		assert.Empty(t, run.inv.skipped)
	}
}

func TestCollect_StorageWithoutImagesContentIsNotRead(t *testing.T) {
	client := newStub(nil, nil)
	client.storages = []map[string]any{
		{"storage": "iso", "content": "iso,vztmpl"},
		{"storage": "", "content": "images"},
		{"storage": "img", "content": " images "},
	}
	client.content = map[string][]string{"iso": {"iso:vm-9001-disk-0"}, "img": {"img:vm-9001-disk-0"}}
	run := runInventory(t, client, defaultOpts())
	assert.Equal(t, []string{"img"}, client.contentReads)
	assert.Equal(t, []string{"img:vm-9001-disk-0"}, volids(run.inv.disks))
}

func TestCollect_ContentReadFailureOnAnEnabledStorageFailsTheRun(t *testing.T) {
	client := newStub(nil, nil)
	client.storages = []map[string]any{images("on", nil)}
	client.content = map[string][]string{"on": {"on:vm-9002-disk-0"}}
	client.failContent = map[string]bool{"on": true}
	_, err := collectInventory(context.Background(), client, defaultOpts(), &bytes.Buffer{})
	require.ErrorIs(t, err, errStubRead)
}

func TestCollect_CancelledContextEndsTheRunWithoutAnInventory(t *testing.T) {
	cases := map[string]func(*stubClient, context.CancelFunc){
		"during the guest reads":     func(c *stubClient, cancel context.CancelFunc) { c.cancelOnSoft = cancel },
		"during the visibility read": func(c *stubClient, cancel context.CancelFunc) { c.cancelOnPerms = cancel },
	}
	for name, arm := range cases {
		t.Run(name, func(t *testing.T) {
			client := newStub([]map[string]any{row(777, "web", "", "qemu"), row(778, "db", "", "qemu")},
				map[int]map[string]string{777: {}, 778: {}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			arm(client, cancel)
			inv, err := collectInventory(ctx, client, defaultOpts(), &bytes.Buffer{})
			require.ErrorIs(t, err, context.Canceled)
			assert.Nil(t, inv)
		})
	}
}

func TestCollect_SharedStorageVolumeIsKeptOnce(t *testing.T) {
	client := newStub(nil, nil)
	client.nodes = []string{"pve1", "pve2"}
	client.storages = []map[string]any{images("shared", nil)}
	client.content = map[string][]string{"shared": {"shared:vm-9001-disk-0"}}
	run := runInventory(t, client, defaultOpts())
	require.Len(t, run.inv.disks, 1)
	assert.Equal(t, "pve1", run.inv.disks[0].node)
}

func TestCollect_NoOnlineNodesWarns(t *testing.T) {
	client := newStub(nil, nil)
	client.nodes = nil
	run := runInventory(t, client, defaultOpts())
	assert.Contains(t, run.stderr, "WARN: no online nodes found; inventory may be empty")
}

func TestCollect_ExplicitNodeScopesTheScan(t *testing.T) {
	client := newStub(nil, nil)
	client.nodes = []string{"pve1", "pve2"}
	client.storages = []map[string]any{images("a", nil)}
	client.content = map[string][]string{"a": {"a:vm-9001-disk-0"}}
	opts := defaultOpts()
	opts.node = "pve9"
	run := runInventory(t, client, opts)
	require.Len(t, run.inv.disks, 1)
	assert.Equal(t, "pve9", run.inv.disks[0].node)
}

func volids(disks []*diskRecord) []string {
	out := []string{}
	for _, d := range disks {
		out = append(out, d.volid)
	}
	return out
}

func TestCollect_BandDiscovery(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{777: {"scsi1": "a:vm-9001-disk-0"}},
		"a:vm-9001-disk-0", "a:vm-9002-disk-0")
	run := runInventory(t, client, defaultOpts())
	assert.Equal(t, "attached", run.disks["a:vm-9001-disk-0"].classification)
	assert.Equal(t, "free-floating", run.disks["a:vm-9002-disk-0"].classification)
	assert.Equal(t, []string{"band"}, run.disks["a:vm-9001-disk-0"].discoveredBy)
}

func TestCollect_GuestVMIDDiskWithBPDSerialIsAttached(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{
		777: {"scsi1": "a:vm-777-disk-1,serial=" + testBPD + ",size=8G"},
	}, "a:vm-777-disk-1")
	rec := runInventory(t, client, defaultOpts()).disks["a:vm-777-disk-1"]
	require.NotNil(t, rec)
	assert.Equal(t, "attached", rec.classification)
	assert.Equal(t, 777, *rec.holderVMID)
	assert.Equal(t, []string{"serial"}, rec.discoveredBy)
}

func TestCollect_ParkerVMIDDiskNamedInASentinelIsParked(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{testBPD: map[string]any{
		"volid": "a:vm-90656-disk-0", "disk_cid": "cid-1", "node": "pve1",
		"source_vm_cid": "vm-1", "parked_at": "2026-01-01T00:00:00Z", "director_id": "dir-1",
	}}})
	client := diskStub(fixRows(), map[int]map[string]string{
		90656: {"scsi0": "a:vm-90656-disk-0", "description": desc},
	}, "a:vm-90656-disk-0")
	rec := runInventory(t, client, defaultOpts()).disks["a:vm-90656-disk-0"]
	require.NotNil(t, rec)
	assert.Equal(t, "parked", rec.classification)
	assert.Equal(t, "cid-1", rec.diskCID)
	assert.Equal(t, "vm-1", rec.sourceVMCID)
	assert.Equal(t, "2026-01-01T00:00:00Z", rec.parkedAt)
	assert.Equal(t, "pve1", rec.parkedNode)
	assert.Equal(t, "dir-1", rec.directorID)
	assert.Equal(t, []string{"parker", "sentinel"}, rec.discoveredBy)
}

func TestCollect_AttachedSentinelFullVolidKeyNamesTheVolume(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_attached_disks": map[string]any{"a:vm-777-disk-2": "cid-2"}})
	client := diskStub(fixRows(), map[int]map[string]string{
		777: {"scsi2": "a:vm-777-disk-2", "description": desc},
	}, "a:vm-777-disk-2")
	rec := runInventory(t, client, defaultOpts()).disks["a:vm-777-disk-2"]
	require.NotNil(t, rec)
	assert.Equal(t, "attached", rec.classification)
	assert.Equal(t, []string{"sentinel"}, rec.discoveredBy)
}

func TestCollect_RenamedDiskTwoGuestsNameIsListedAsShared(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{
		777: {"scsi1": "a:vm-777-disk-1,serial=" + testBPD},
		778: {"unused0": "a:vm-777-disk-1"},
	}, "a:vm-777-disk-1")
	run := runInventory(t, client, defaultOpts())
	assert.Contains(t, run.disks, "a:vm-777-disk-1")
	require.Len(t, run.inv.multiRef.records, 1)
	mr := run.inv.multiRef.records[0]
	assert.Equal(t, "a:vm-777-disk-1", mr.volid)
	assert.ElementsMatch(t, []int{777, 778}, []int{mr.references[0].vmid, mr.references[1].vmid})
}

func TestCollect_NonBOSHVolumeOutsideTheBandIsIgnored(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{
		777: {"scsi1": "a:vm-777-disk-1,serial=guest-serial", "scsi2": "a:vm-777-disk-2"},
	}, "a:vm-777-disk-1", "a:vm-777-disk-2", "a:vm-31000-disk-0")
	assert.Empty(t, runInventory(t, client, defaultOpts()).disks)
}

func TestCollect_UnreadableGuestIsWarnedAbout(t *testing.T) {
	client := diskStub(fixRows(), nil, "a:vm-777-disk-1")
	run := runInventory(t, client, defaultOpts())
	assert.Contains(t, run.stderr, "WARN: 4 guest config(s) could not be read; a renamed disk held only by "+
		"such a guest may be missing from this report.")
}

func TestCollect_SentinelBareNameNeverMatchesAVolumeOnAnotherStorage(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_attached_disks": map[string]any{"vm-500-disk-0": "cid"}})
	client := newStub(fixRows(), map[int]map[string]string{
		777: {"scsi1": "a:vm-500-disk-0", "description": desc},
		500: {"unused0": "b:vm-500-disk-0"},
	})
	client.storages = []map[string]any{images("a", nil), images("b", nil)}
	client.content = map[string][]string{"a": {"a:vm-500-disk-0"}, "b": {"b:vm-500-disk-0"}}
	assert.Empty(t, runInventory(t, client, defaultOpts()).disks)
}

// loneSentinel is a parker whose sentinel alone names a volume outside the
// disk band.
func loneSentinel(t *testing.T, configs map[int]map[string]string) *stubClient {
	t.Helper()
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{
		testBPD: map[string]any{"volid": "a:vm-31000-disk-0", "slot": "scsi9"},
	}})
	all := map[int]map[string]string{90656: {"description": desc}}
	maps.Copy(all, configs)
	return diskStub(fixRows(), all, "a:vm-31000-disk-0")
}

func TestCollect_LoneSentinelVolumeIsMarkedNotPlain(t *testing.T) {
	run := runInventory(t, loneSentinel(t, nil), defaultOpts())
	rec := run.disks["a:vm-31000-disk-0"]
	require.NotNil(t, rec)
	assert.Equal(t, "free-floating", rec.classification)
	assert.True(t, rec.sentinelOnly)
	assert.Equal(t, 90656, *rec.sentinelVMID)
	assert.Equal(t, "bosh-parker-90656", rec.sentinelName)
	d := diskToJSON(rec)
	assert.True(t, d.FoundOnlyBySentinel)
	assert.Equal(t, 90656, *d.SentinelVMID)
	assert.Equal(t, "bosh-parker-90656", d.SentinelName)
	assert.Nil(t, d.HolderVMID)
	assert.False(t, d.HeldByUnusedEntry)

	var out bytes.Buffer
	writeHumanReport(&out, run.inv, testHeader())
	assert.Contains(t, out.String(), "found only by a sentinel note on VM 90656 (bosh-parker-90656)")
	assert.Contains(t, out.String(), "`bosh disks --orphaned` before deleting")
}

func TestCollect_BandOrphanIsNotMarked(t *testing.T) {
	run := runInventory(t, diskStub(fixRows(), nil, "a:vm-9001-disk-0"), defaultOpts())
	rec := run.disks["a:vm-9001-disk-0"]
	assert.False(t, rec.sentinelOnly)
	b := mustJSON(t, diskToJSON(rec))
	assert.NotContains(t, b, "found_only_by_sentinel")
	assert.NotContains(t, b, "held_by_unused_entry")
}

func TestCollect_GuestNamingTheVolumeOnAnUnusedEntryIsItsHolder(t *testing.T) {
	run := runInventory(t, loneSentinel(t, map[int]map[string]string{500: {"unused0": "a:vm-31000-disk-0"}}),
		defaultOpts())
	rec := run.disks["a:vm-31000-disk-0"]
	assert.True(t, rec.sentinelOnly)
	assert.Equal(t, 500, *rec.holderVMID)
	assert.Equal(t, "operator-vm", rec.holderName)
	assert.Equal(t, "unused0", rec.holderSlot)
	d := diskToJSON(rec)
	assert.Equal(t, "unused0", d.HolderSlot)
	assert.True(t, d.HeldByUnusedEntry)

	var out bytes.Buffer
	writeHumanReport(&out, run.inv, testHeader())
	text := out.String()
	assert.Contains(t, text, "DO NOT DELETE: VM 500 (operator-vm) still names it on unused0, so PVE references it")
	assert.Less(t, bytes.Index(out.Bytes(), []byte("DO NOT DELETE")),
		bytes.Index(out.Bytes(), []byte("found only by a sentinel note")))

	err := findingsError(run.inv)
	require.Error(t, err)
	assert.Equal(t, "EXIT 9: 1 free-floating disk(s) found, a:vm-31000-disk-0 still named by VM 500.", err.Error())
}

func TestCollect_BandVolumeNamedOnAnUnusedEntryGetsTheHolder(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{500: {"unused0": "a:vm-9001-disk-0"}},
		"a:vm-9001-disk-0")
	rec := runInventory(t, client, defaultOpts()).disks["a:vm-9001-disk-0"]
	assert.Equal(t, "free-floating", rec.classification)
	assert.False(t, rec.sentinelOnly)
	assert.Equal(t, 500, *rec.holderVMID)
	assert.Equal(t, "unused0", rec.holderSlot)
	assert.True(t, diskToJSON(rec).HeldByUnusedEntry)
}

func TestCollect_FailClosed_UnreadableHolderFailsTheRun(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{
		testBPD: map[string]any{"volid": "a:vm-777-disk-2", "slot": "scsi3"},
	}})
	client := diskStub(fixRows(), map[int]map[string]string{
		777:   {"scsi1": "a:vm-777-disk-2,serial=" + testBPD},
		90656: {"description": desc},
	}, "a:vm-777-disk-2")
	client.softFail = map[int]bool{777: true}
	client.hardFail = map[int]bool{777: true}
	_, err := collectInventory(context.Background(), client, defaultOpts(), &bytes.Buffer{})
	require.ErrorIs(t, err, errStubRead)
}

func TestCollect_FailClosed_UnreadableParkerFailsTheRun(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{
		777:   {"scsi0": "a:vm-777-disk-0"},
		90656: {"scsi0": "a:vm-90656-disk-0,serial=" + testBPD},
	}, "a:vm-777-disk-0", "a:vm-90656-disk-0")
	client.softFail = map[int]bool{90656: true}
	client.hardFail = map[int]bool{90656: true}
	_, err := collectInventory(context.Background(), client, defaultOpts(), &bytes.Buffer{})
	require.ErrorIs(t, err, errStubRead)
}

func TestCollect_FailedSoftReadFallsBackToAHardReadThatCanSucceed(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{777: {"scsi1": "a:vm-777-disk-2,serial=" + testBPD}},
		"a:vm-777-disk-2")
	client.softFail = map[int]bool{777: true}
	run := runInventory(t, client, defaultOpts())
	assert.Contains(t, client.hardReads, 777)
	assert.Equal(t, "attached", run.disks["a:vm-777-disk-2"].classification)
}

func TestCollect_ParkerWhoseStepFourReadFailedIsHardReadAgain(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{777: {}}, "a:vm-9001-disk-0")
	client.softFail = map[int]bool{90656: true}
	run := runInventory(t, client, defaultOpts())
	assert.GreaterOrEqual(t, client.hardReadCount(90656), 2)
	require.Len(t, run.inv.parkers, 1)
	assert.False(t, run.inv.parkers[0].configRead)
	assert.False(t, run.inv.parkers[0].empty())
}

func TestCollect_NoVolumesKeepsTheSoftReads(t *testing.T) {
	client := diskStub(fixRows(), nil)
	client.softFail = map[int]bool{777: true}
	client.hardFail = map[int]bool{777: true}
	run := runInventory(t, client, defaultOpts())
	assert.Contains(t, run.inv.multiRef.unreadableVMIDs, 777)
}

func TestCollect_CrashWindowVolumeOnAParkerSlotIsFound(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{
		testBPD: map[string]any{"volid": "a:vm-777-disk-2", "slot": "scsi3", "disk_cid": "c"},
	}})
	client := diskStub(fixRows(), map[int]map[string]string{
		90656: {"scsi3": "a:vm-90656-disk-1", "description": desc},
	}, "a:vm-90656-disk-1")
	run := runInventory(t, client, defaultOpts())
	rec := run.disks["a:vm-90656-disk-1"]
	require.NotNil(t, rec)
	assert.Equal(t, "parked", rec.classification)
	assert.Contains(t, rec.discoveredBy, "parker")
	require.Len(t, run.inv.parkers, 1)
	assert.Equal(t, 1, run.inv.parkers[0].diskCount)
}

func TestCollect_AnyBusSlotVolumeOfAParkerCountsWithoutASentinel(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{90656: {"scsi0": "a:vm-90656-disk-0"}},
		"a:vm-90656-disk-0")
	rec := runInventory(t, client, defaultOpts()).disks["a:vm-90656-disk-0"]
	assert.Equal(t, []string{"parker"}, rec.discoveredBy)
	assert.Equal(t, "parked", rec.classification)
}

func TestCollect_UntaggedVMInTheParkerBandDoesNotCount(t *testing.T) {
	client := diskStub([]map[string]any{row(90700, "intruder", "", "qemu")},
		map[int]map[string]string{90700: {"scsi0": "a:vm-90700-disk-0"}}, "a:vm-90700-disk-0")
	assert.Empty(t, runInventory(t, client, defaultOpts()).disks)
}

func TestCollect_TaggedVMOutsideTheParkerBandDoesNotCount(t *testing.T) {
	client := diskStub([]map[string]any{row(600, "tagged", "bosh-parker", "qemu")},
		map[int]map[string]string{600: {"scsi0": "a:vm-600-disk-0"}}, "a:vm-600-disk-0")
	assert.Empty(t, runInventory(t, client, defaultOpts()).disks)
}

func TestCollect_PendingDeleteStillHoldsTheVolumeAndCarriesItsSerial(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{
		testBPD: map[string]any{"volid": "a:vm-777-disk-2", "slot": "scsi3"},
	}})
	client := diskStub(fixRows(), nil, "a:vm-777-disk-2")
	client.views = map[int]*pendingViews{
		777: {current: map[string]string{"scsi1": "a:vm-777-disk-2,serial=" + testBPD}, applied: map[string]string{}},
		90656: {
			current: map[string]string{"description": desc},
			applied: map[string]string{"description": desc},
		},
	}
	rec := runInventory(t, client, defaultOpts()).disks["a:vm-777-disk-2"]
	require.NotNil(t, rec)
	assert.Equal(t, "attached", rec.classification)
	assert.Equal(t, 777, *rec.holderVMID)
	assert.Equal(t, []string{"serial", "sentinel"}, rec.discoveredBy)
}

func TestCollect_PendingOnlyKeyIsHeld(t *testing.T) {
	client := diskStub(fixRows(), nil, "a:vm-777-disk-2")
	client.views = map[int]*pendingViews{
		777: {current: map[string]string{}, applied: map[string]string{"scsi1": "a:vm-777-disk-2,serial=" + testBPD}},
	}
	assert.Equal(t, "attached", runInventory(t, client, defaultOpts()).disks["a:vm-777-disk-2"].classification)
}

func TestCollect_EveryDiskCarriesDiscoveredBy(t *testing.T) {
	client := diskStub(fixRows(), map[int]map[string]string{
		777: {"scsi1": "a:vm-9001-disk-0", "scsi2": "a:vm-777-disk-2,serial=" + testBPD},
	}, "a:vm-9001-disk-0", "a:vm-777-disk-2")
	run := runInventory(t, client, defaultOpts())
	doc := buildDocument(run.inv, testHeader())
	require.Len(t, doc.Disks, 2)
	got := map[string][]string{}
	for _, d := range doc.Disks {
		got[d.Volid] = d.DiscoveredBy
	}
	assert.Equal(t, map[string][]string{"a:vm-9001-disk-0": {"band"}, "a:vm-777-disk-2": {"serial"}}, got)
	assert.Equal(t, "a:vm-777-disk-2", doc.Disks[0].Volid, "disks are sorted by volid")
	assert.Equal(t, 2, doc.Summary.Attached)
}

func TestCollect_DuplicateSerials(t *testing.T) {
	t.Run("a clone sharing a serial warns with both volids and holders", func(t *testing.T) {
		client := diskStub(fixRows(), map[int]map[string]string{
			777: {"scsi1": "a:vm-777-disk-1,serial=" + testBPD},
			778: {"scsi1": "a:vm-778-disk-1,serial=" + testBPD},
		}, "a:vm-777-disk-1", "a:vm-778-disk-1")
		run := runInventory(t, client, defaultOpts())
		assert.Len(t, run.disks, 2)
		assert.Contains(t, run.stderr, "WARN: serial "+testBPD+" appears on 2 different volumes: "+
			"a:vm-777-disk-1 (held by VM 777 (web-0)); a:vm-778-disk-1 (held by VM 778 (web-1)). "+
			"A clone of a BOSH VM copies the serial, so we cannot tell which volume the CPI means.")
	})
	t.Run("one volume named by two guests is not a duplicate serial", func(t *testing.T) {
		client := diskStub(fixRows(), map[int]map[string]string{
			777: {"scsi1": "a:vm-777-disk-1,serial=" + testBPD},
			778: {"scsi1": "a:vm-777-disk-1,serial=" + testBPD},
		}, "a:vm-777-disk-1")
		assert.NotContains(t, runInventory(t, client, defaultOpts()).stderr, "different volumes")
	})
	t.Run("distinct serials do not warn", func(t *testing.T) {
		client := diskStub(fixRows(), map[int]map[string]string{
			777: {"scsi1": "a:vm-777-disk-1,serial=" + testBPD},
			778: {"scsi1": "a:vm-778-disk-1,serial=bpd-1122334455667788"},
		}, "a:vm-777-disk-1", "a:vm-778-disk-1")
		assert.NotContains(t, runInventory(t, client, defaultOpts()).stderr, "different volumes")
	})
	t.Run("the same disk with a new volid in the pending view does not warn", func(t *testing.T) {
		client := diskStub(fixRows(), nil, "a:vm-777-disk-1", "a:vm-777-disk-2")
		client.views = map[int]*pendingViews{777: {
			current: map[string]string{"scsi1": "a:vm-777-disk-1,serial=" + testBPD},
			applied: map[string]string{"scsi1": "a:vm-777-disk-2,serial=" + testBPD},
		}}
		assert.NotContains(t, runInventory(t, client, defaultOpts()).stderr, "different volumes")
	})
}

func TestCollect_ParkerInventory(t *testing.T) {
	rows := []map[string]any{
		row(90002, "parker-b", "bosh-parker", "qemu"),
		row(90001, "parker-a", "bosh-parker", "qemu"),
		row(90003, "parker-c", "bosh-parker", "qemu"),
	}
	rows[0]["pool"] = "bosh-parker"
	client := newStub(rows, map[int]map[string]string{
		90001: {},
		90002: {"scsi0": "a:vm-90002-disk-0", "scsi1": "a:vm-90002-disk-1", "unused0": "a:vm-1-disk-0"},
	})
	run := runInventory(t, client, defaultOpts())
	require.Len(t, run.inv.parkers, 3)
	assert.Equal(t, []int{90001, 90002, 90003}, []int{
		run.inv.parkers[0].vmid, run.inv.parkers[1].vmid, run.inv.parkers[2].vmid,
	})
	empty, full, gone := run.inv.parkers[0], run.inv.parkers[1], run.inv.parkers[2]
	assert.True(t, empty.empty())
	assert.Equal(t, 2, full.diskCount)
	assert.Equal(t, 1, full.unusedCount)
	assert.Equal(t, "bosh-parker", full.pool)
	assert.False(t, full.empty())
	assert.False(t, gone.configRead)
	assert.False(t, gone.empty())
}

func TestCollect_DuplicateVMIDKeepsFirstPositionAndLastRow(t *testing.T) {
	rows := []map[string]any{
		row(777, "first", "", "qemu"),
		{"vmid": "not a number", "node": "pve1"},
		{"node": "pve1", "name": "no vmid"},
		row(778, "other", "", "qemu"),
		row(777, "last", "", "qemu"),
	}
	client := newStub(rows, map[int]map[string]string{777: {}, 778: {}})
	run := runInventory(t, client, defaultOpts())
	assert.Empty(t, run.inv.multiRef.unreadableVMIDs)
	vms := newVMIndex()
	for _, r := range rows {
		if id, ok := pyInt(r["vmid"]); ok && r["vmid"] != nil {
			vms.put(vmInfo{vmid: id, name: strOr(r["name"])})
		}
	}
	assert.Equal(t, []int{777, 778}, vms.order)
	assert.Equal(t, "last", vms.byID[777].name)
}

func TestCollect_HolderIsTheFirstGuestInIndexOrder(t *testing.T) {
	rows := []map[string]any{row(778, "web-1", "", "qemu"), row(777, "web-0", "", "qemu")}
	client := diskStub(rows, map[int]map[string]string{
		777: {"scsi1": "a:vm-9001-disk-0"},
		778: {"scsi1": "a:vm-9001-disk-0"},
	}, "a:vm-9001-disk-0")
	rec := runInventory(t, client, defaultOpts()).disks["a:vm-9001-disk-0"]
	assert.Equal(t, 778, *rec.holderVMID)
}
