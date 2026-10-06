package cpi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
)

func testHeader() reportHeader {
	return reportHeader{host: "pve.example.com", port: 8006, opts: defaultOpts()}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func humanReport(inv *inventory) string {
	var out bytes.Buffer
	writeHumanReport(&out, inv, testHeader())
	return out.String()
}

func warnings(inv *inventory, opts auditOptions) string {
	var out bytes.Buffer
	emitWarnings(&out, inv, opts)
	return out.String()
}

// fullCoverage is an empty multiply-referenced report that covers the whole
// cluster.
func fullCoverage() multiRefReport {
	return multiRefReport{visibility: "full", unreadableVMIDs: []int{}}
}

func lineContaining(t *testing.T, text, needle string) string {
	t.Helper()
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line contains %q in:\n%s", needle, text)
	return ""
}

func TestHumanReport_ParkerTablePoolColumn(t *testing.T) {
	inv := &inventory{multiRef: fullCoverage(), parkers: []parkerRecord{
		{vmid: 90000, node: "pve1", name: "parker-svc-a", pool: "ops-pool", diskCount: 3, unusedCount: 1,
			configRead: true},
		{vmid: 90001, node: "pve1", name: "parker-svc-b", configRead: true},
	}}
	out := humanReport(inv)
	header := lineContaining(t, out, "VMID")
	assert.Contains(t, header, "POOL")

	rowA := lineContaining(t, out, "parker-svc-a")
	rowB := lineContaining(t, out, "parker-svc-b")
	assert.Equal(t, "ops-pool", strings.TrimSpace(rowA[60:80]))
	assert.Equal(t, "", strings.TrimSpace(rowB[60:80]))
	assert.NotContains(t, out, "None")
	assert.Equal(t, strings.Index(rowA, "/31"), strings.Index(rowB, "/31"))
	assert.Equal(t, strings.Index(header, "/31"), strings.Index(rowA, "/31"))
	assert.True(t, strings.HasSuffix(rowB, "YES"))
	assert.Equal(t, "  "+strings.Repeat("-", 7)+"  "+strings.Repeat("-", 15)+"  "+strings.Repeat("-", 30)+"  "+
		strings.Repeat("-", 20)+"  "+strings.Repeat("-", 8)+"  "+strings.Repeat("-", 6)+"  "+strings.Repeat("-", 5),
		lineContaining(t, out, "-------  ---------------"))
}

func TestHumanReport_UnreadParkerShowsAQuestionMark(t *testing.T) {
	out := humanReport(&inventory{multiRef: fullCoverage(), parkers: []parkerRecord{
		{vmid: 90003, node: "pve1", name: "parker-gone"},
	}})
	assert.True(t, strings.HasSuffix(lineContaining(t, out, "parker-gone"), "    ?"))
}

func TestHumanReport_NoParkers(t *testing.T) {
	out := humanReport(&inventory{multiRef: fullCoverage()})
	assert.Contains(t, out, "PARKER VMs: none found\n")
	assert.True(t, strings.HasPrefix(out, "\nBOSH Persistent Disk Audit\n  Host:  pve.example.com:8006\n"+
		"  Disk band:   [9000, 29999]\n  Parker band: [90000, 90999]\n  Total disk volumes: 0\n"), out)
	assert.NotContains(t, out, "more than one guest: 0\n    coverage")
	assert.NotContains(t, out, "Scope:")
}

func TestHumanReport_ScopeLineFollowsAnExplicitNode(t *testing.T) {
	h := testHeader()
	h.opts.node = "pve2"
	var out bytes.Buffer
	writeHumanReport(&out, &inventory{multiRef: fullCoverage()}, h)
	assert.Contains(t, out.String(), "  Parker band: [90000, 90999]\n  Scope: node=pve2\n")
}

func TestHumanReport_SkippedStorages(t *testing.T) {
	inv := &inventory{multiRef: fullCoverage(), skipped: []skippedStorage{
		{node: "pve1", storage: "off", reason: "disabled (enabled=0); its disks are not in this report"},
	}}
	out := humanReport(inv)
	block := "  Skipped storages (disks on a skipped storage are not in this report):\n" +
		"    node pve1 storage off: disabled (enabled=0); its disks are not in this report\n"
	assert.Contains(t, out, block)
	assert.Less(t, strings.Index(out, "Parker band:"), strings.Index(out, block))
	assert.Less(t, strings.Index(out, block), strings.Index(out, "Total disk volumes"))

	assert.NotContains(t, humanReport(&inventory{multiRef: fullCoverage()}), "Skipped storages")
}

func TestHumanReport_DiskSections(t *testing.T) {
	inv := &inventory{multiRef: fullCoverage(), disks: []*diskRecord{
		{volid: "a:vm-9002-disk-0", storage: "a", node: "pve1", sizeBytes: 1 << 30, classification: "free-floating"},
		{volid: "a:vm-9001-disk-0", storage: "a", node: "pve1", sizeBytes: 10 << 30, classification: "attached",
			holderVMID: new(777), holderName: "web-0"},
		{volid: "a:vm-90656-disk-0", storage: "a", node: "pve1", sizeBytes: 512, classification: "parked",
			holderVMID: new(90656), diskCID: "cid-1", sourceVMCID: "vm-1", parkedAt: "2026-01-01T00:00:00Z",
			directorID: "dir-1"},
	}}
	out := humanReport(inv)
	assert.Contains(t, out, "    attached:      1\n    parked:        1\n    free-floating: 1\n    unknown:       0\n")
	assert.Contains(t, out, "ATTACHED DISKS\n")
	assert.Contains(t, out, "PARKED DISKS\n")
	assert.Contains(t, out, "FREE-FLOATING DISKS  [WARNING: potential orphans]\n")
	assert.NotContains(t, out, "UNKNOWN VOLUMES")
	assert.Equal(t, "  a:vm-9001-disk-0"+strings.Repeat(" ", 45-16)+"   10.0 GiB          777  web-0"+
		strings.Repeat(" ", 25), lineContaining(t, out, "a:vm-9001-disk-0"))
	parked := lineContaining(t, out, "a:vm-90656-disk-0")
	assert.Contains(t, parked, "  90656  cid-1")
	assert.Contains(t, parked, "512 B")
	free := lineContaining(t, out, "a:vm-9002-disk-0")
	assert.Equal(t, "  a:vm-9002-disk-0"+strings.Repeat(" ", 55-16)+"    1.0 GiB  a"+strings.Repeat(" ", 19)+
		"  pve1"+strings.Repeat(" ", 11), free)
	assert.Less(t, strings.Index(out, "ATTACHED DISKS"), strings.Index(out, "PARKED DISKS"))
	assert.Less(t, strings.Index(out, "PARKED DISKS"), strings.Index(out, "FREE-FLOATING DISKS"))
}

func TestHumanReport_UnknownSectionListsEachVolume(t *testing.T) {
	out := humanReport(&inventory{multiRef: fullCoverage(), disks: []*diskRecord{
		{volid: "a:vm-9003-disk-0", classification: "unknown"},
	}})
	assert.Contains(t, out, "UNKNOWN VOLUMES  [could not determine holder]\n  a:vm-9003-disk-0\n")
}

func TestHumanReport_LongValuesAreTruncated(t *testing.T) {
	long := "a:" + strings.Repeat("x", 80)
	out := humanReport(&inventory{multiRef: fullCoverage(), disks: []*diskRecord{
		{volid: long, storage: "a", node: "pve1", classification: "attached", holderVMID: new(1),
			holderName: strings.Repeat("n", 40)},
	}})
	row := lineContaining(t, out, "a:xxx")
	assert.Contains(t, row, "a:"+strings.Repeat("x", 42)+"… ")
	assert.Contains(t, row, strings.Repeat("n", 29)+"…")
}

func TestHumanReport_HolderWithoutAVMIDShowsADash(t *testing.T) {
	out := humanReport(&inventory{multiRef: fullCoverage(), disks: []*diskRecord{
		{volid: "a:vm-9001-disk-0", classification: "attached"},
	}})
	assert.Contains(t, lineContaining(t, out, "a:vm-9001-disk-0"), "          —  ")
}

// doubleReport runs the double-reference fixture through collectInventory.
func doubleReport(t *testing.T, mutate func(*stubClient)) multiRefReport {
	t.Helper()
	client := newStub(doubleRows(), doubleConfigs())
	if mutate != nil {
		mutate(client)
	}
	return runInventory(t, client, defaultOpts()).inv.multiRef
}

func TestDoubleReference_HumanReport(t *testing.T) {
	out := humanReport(&inventory{multiRef: doubleReport(t, nil)})
	assert.Contains(t, out, "Volumes named by more than one guest: 1")
	assert.Contains(t, out, "MULTIPLY REFERENCED VOLUMES  [WARNING: more than one guest names these]\n")
	assert.Contains(t, out, "\n  a:123/vm-123-disk-0.raw\n")
	assert.Contains(t, out, "    VM 777 (web-0) on pve1 unused0\n")
	assert.Contains(t, out, "    VM 90656 (bosh-parker-90656) on pve1 scsi1 [parker]\n")
	assert.Contains(t, out, "    VM 123 would own this volume by name, and no such VM is in the cluster index\n")
	assert.NotContains(t, out, "coverage:")
}

func TestDoubleReference_HumanReportCoverageLine(t *testing.T) {
	limited := doubleReport(t, func(s *stubClient) { s.privs = map[string]any{} })
	limited.unreadableVMIDs = []int{778}
	assert.Contains(t, humanReport(&inventory{multiRef: limited}),
		"    coverage: limited (this token lacks VM.Audit on /vms); 1 guest config(s) unreadable\n")
	unknown := doubleReport(t, func(s *stubClient) { s.permErr = "HTTP 500 boom" })
	assert.Contains(t, humanReport(&inventory{multiRef: unknown}),
		"    coverage: unknown (the permissions read failed)\n")
}

func TestDoubleReference_JSON(t *testing.T) {
	report := doubleReport(t, func(s *stubClient) { s.privs = map[string]any{} })
	doc := buildDocument(&inventory{multiRef: report}, testHeader())
	assert.Equal(t, 1, doc.Summary.MultiplyReferenced)
	require.Len(t, doc.MultiplyReferenced, 1)
	assert.Equal(t, 123, *doc.MultiplyReferenced[0].OwnerVMID)
	assert.False(t, doc.MultiplyReferenced[0].OwnerPresent)
	assert.Equal(t, []int{}, doc.MultiplyReferencedUnreadableVMIDs)
	assert.Equal(t, "limited", doc.MultiplyReferencedVisibility)
	assert.False(t, doc.MultiplyReferencedComplete)
	assert.Contains(t, mustJSON(t, doc.MultiplyReferenced[0].References[0]),
		`{"vmid":777,"node":"pve1","name":"web-0","slot":"unused0","kind":"unused","parker":false,"owns":false}`)
}

func TestDoubleReference_JSONListsUnreadableVMIDs(t *testing.T) {
	report := doubleReport(t, func(s *stubClient) { delete(s.views, 777) })
	doc := buildDocument(&inventory{multiRef: report}, testHeader())
	assert.Equal(t, []int{777}, doc.MultiplyReferencedUnreadableVMIDs)
	assert.False(t, doc.MultiplyReferencedComplete)
}

func TestDoubleReference_NullOwnerRendersAsNull(t *testing.T) {
	vms := newVMIndex()
	vms.put(vmInfo{vmid: 777})
	vms.put(vmInfo{vmid: 888})
	records, _ := findMultiplyReferenced(vms, map[int]map[string]string{
		777: {"scsi1": "nfs:custom/data.raw"}, 888: {"unused0": "nfs:custom/data.raw"},
	}, defaultOpts())
	doc := buildDocument(&inventory{multiRef: multiRefReport{records: records, visibility: "full"}}, testHeader())
	assert.Contains(t, mustJSON(t, doc.MultiplyReferenced[0]), `"owner_vmid":null`)
}

func TestDoubleReference_Warnings(t *testing.T) {
	out := warnings(&inventory{multiRef: doubleReport(t, nil)}, defaultOpts())
	assert.Contains(t, out, "WARN: volume a:123/vm-123-disk-0.raw is named by 2 guests: "+
		"VM 777 (web-0) on pve1 unused0; VM 90656 (bosh-parker-90656) on pve1 scsi1 [parker]. "+
		"Leave every reference in place until we know which guest really holds the disk; "+
		"VM 123 would own this volume by name, and no such VM is in the cluster index; "+
		`see "Auditing parked disks with scripts/disk-audit" in docs/operations.md of bosh-proxmox-cpi-release`+"\n")
	assert.NotContains(t, out, "qm ")
	assert.NotContains(t, out, "VM.Audit")
	assert.NotContains(t, out, "did not come back")
}

func TestDoubleReference_WarningNamesTheOwnerHazard(t *testing.T) {
	vms := newVMIndex()
	vms.put(vmInfo{vmid: 777, node: "pve1", name: "web-0"})
	vms.put(vmInfo{vmid: 888, node: "pve1", name: "web-1"})
	records, _ := findMultiplyReferenced(vms, map[int]map[string]string{
		777: {"scsi1": "a:777/vm-777-disk-2.raw"}, 888: {"unused0": "a:777/vm-777-disk-2.raw"},
	}, defaultOpts())
	out := warnings(&inventory{multiRef: multiRefReport{records: records, visibility: "full"}}, defaultOpts())
	assert.Contains(t, out, "VM 777 (web-0) on pve1 scsi1 [owns by name]")
	assert.Contains(t, out, "destroying VM 777, or removing its unused entry, deletes the volume "+
		"while another guest still names it; ")
}

func TestDoubleReference_VisibilityWarnings(t *testing.T) {
	limited := warnings(&inventory{multiRef: doubleReport(t, func(s *stubClient) { s.privs = map[string]any{} })},
		defaultOpts())
	assert.Contains(t, limited, "WARN: this token lacks VM.Audit on /vms, so the report of volumes named by more "+
		"than one guest covers only the guests this token can see; a second guest outside its view would not "+
		"show up\n")
	unknown := warnings(&inventory{multiRef: doubleReport(t, func(s *stubClient) { s.permErr = "HTTP 500 boom" })},
		defaultOpts())
	assert.Contains(t, unknown, "WARN: could not read this token's permissions on /vms (HTTP 500 boom), so the "+
		"coverage of the report of volumes named by more than one guest is unknown\n")
}

func TestDoubleReference_UnreadableWarning(t *testing.T) {
	report := fullCoverage()
	report.unreadableVMIDs = []int{777, 781}
	assert.Equal(t, "WARN: the configs of 2 QEMU guest(s) did not come back (777, 781), so the report of "+
		"volumes named by more than one guest may be incomplete\n", warnings(&inventory{multiRef: report},
		defaultOpts()))
}

func TestDoubleReference_AloneDoesNotExitNine(t *testing.T) {
	assert.NoError(t, findingsError(&inventory{multiRef: doubleReport(t, nil)}))
}

func TestWarnings_ParkedDisksUnderTheFreeStrategy(t *testing.T) {
	inv := &inventory{multiRef: fullCoverage(), disks: []*diskRecord{{volid: "a:x", classification: "parked"}}}
	opts := defaultOpts()
	assert.Empty(t, warnings(inv, opts))
	opts.strategy = "free"
	assert.Equal(t, "WARN: 1 parked disk(s) found but detached_disk_strategy='free'. They stay recognized "+
		"(the parker band resolves under every strategy) and each unparks on its next attach_disk or "+
		"delete_disk; no new detaches will park.\n", warnings(inv, opts))
}

func TestWarnings_Parkers(t *testing.T) {
	inv := &inventory{multiRef: fullCoverage(), parkers: []parkerRecord{
		{vmid: 90001, node: "pve1", name: "p-empty", configRead: true},
		{vmid: 90002, node: "pve1", name: "p-unused", pool: "bosh-parker", unusedCount: 2, configRead: true},
		{vmid: 90003, node: "pve2", name: "p-gone"},
	}}
	assert.Equal(t,
		"WARN: parker VM 90001 (p-empty, pool none) on node pve1 is empty (0 disks) — teardown candidate: "+
			"qm set 90001 --protection 0 && qm destroy 90001 --purge\n"+
			"WARN: parker VM 90002 (p-unused, pool bosh-parker) on node pve1 carries 2 unusedN reference(s) to a "+
			"live volume; do NOT destroy it. Clear each with: qm set 90002 --protection 0 && "+
			"qm unlink 90002 --idlist <unusedN> && qm set 90002 --protection 1\n"+
			"WARN: parker VM 90003 (p-gone, pool none) on node pve2 config was not returned (vanished during the "+
			"scan, HTTP 404/501); its contents are unknown and it is not reported as empty\n",
		warnings(inv, defaultOpts()))
}

func TestWarnings_PoolIntruder(t *testing.T) {
	inv := &inventory{multiRef: fullCoverage(), intruders: []poolIntruder{
		{vmid: 140, node: "pve2", name: "bosh-web-0", pool: "bosh-parker"},
	}}
	assert.Equal(t, "WARN: VM 140 (bosh-web-0, pool bosh-parker) on node pve2 sits in a parker pool and carries "+
		"no bosh-parker tag. The CPI never moves it out, so move it into the workload pool it belongs in with: "+
		"pvesh set /pools/<workload-pool> --vms 140 --allow-move 1\n", warnings(inv, defaultOpts()))
	assert.Empty(t, warnings(&inventory{multiRef: fullCoverage()}, defaultOpts()))
}

func TestParkerPoolNames(t *testing.T) {
	parkers := []parkerRecord{{vmid: 90000, pool: "bosh-parker"}, {vmid: 90001}}
	assert.Equal(t, map[string]bool{"bosh-parker": true}, parkerPoolNames(parkers, nil))
	assert.Empty(t, parkerPoolNames([]parkerRecord{{vmid: 90001}}, nil))
	assert.Equal(t, map[string]bool{"blue-parker": true}, parkerPoolNames(nil, []string{"blue-parker"}))
	assert.Equal(t, map[string]bool{"bosh-parker": true, "blue-parker": true},
		parkerPoolNames(parkers, []string{"blue-parker", "bosh-parker"}))
}

func TestFindPoolIntruders(t *testing.T) {
	pools := map[string]bool{"bosh-parker": true}
	index := func(rows ...vmInfo) *vmIndex {
		vms := newVMIndex()
		for _, r := range rows {
			vms.put(r)
		}
		return vms
	}
	parker := vmInfo{vmid: 90000, node: "pve1", name: "parker", tags: "bosh-cpi;bosh-parker", pool: "bosh-parker"}

	assert.Empty(t, findPoolIntruders(index(parker), pools))
	assert.Equal(t, []poolIntruder{{vmid: 140, node: "pve2", name: "bosh-web-0", pool: "bosh-parker"}},
		findPoolIntruders(index(parker, vmInfo{vmid: 140, node: "pve2", name: "bosh-web-0", pool: "bosh-parker"}),
			pools))
	assert.Empty(t, findPoolIntruders(index(vmInfo{vmid: 140, pool: "workload"}), pools))
	assert.Empty(t, findPoolIntruders(index(vmInfo{vmid: 140}), pools))
	assert.Empty(t, findPoolIntruders(index(vmInfo{vmid: 140, pool: "bosh-parker"}), map[string]bool{}))
	got := findPoolIntruders(index(vmInfo{vmid: 300, pool: "bosh-parker"}, vmInfo{vmid: 100, pool: "bosh-parker"},
		vmInfo{vmid: 200, pool: "bosh-parker"}), pools)
	assert.Equal(t, []int{100, 200, 300}, []int{got[0].vmid, got[1].vmid, got[2].vmid})
}

func TestCollect_PoolIntrudersFromTheIndex(t *testing.T) {
	rows := []map[string]any{
		row(90000, "parker", "bosh-parker", "qemu"),
		row(140, "bosh-web-0", "", "qemu"),
		row(141, "bosh-web-1", "", "qemu"),
	}
	rows[0]["pool"] = "bosh-parker"
	rows[1]["pool"] = "bosh-parker"
	rows[2]["pool"] = "blue-parker"
	client := newStub(rows, map[int]map[string]string{90000: {}, 140: {}, 141: {}})
	run := runInventory(t, client, defaultOpts())
	require.Len(t, run.inv.intruders, 1)
	assert.Equal(t, 140, run.inv.intruders[0].vmid)

	opts := defaultOpts()
	opts.parkerPools = []string{"blue-parker"}
	assert.Len(t, runInventory(t, client, opts).inv.intruders, 2)
}

func referencesOf(r multiRefRecord) map[[2]any]multiRefEntry {
	out := map[[2]any]multiRefEntry{}
	for _, ref := range r.references {
		out[[2]any{ref.vmid, ref.slot}] = ref
	}
	return out
}

func TestFindMultiplyReferenced(t *testing.T) {
	index := func(ids ...vmInfo) *vmIndex {
		vms := newVMIndex()
		for _, v := range ids {
			vms.put(v)
		}
		return vms
	}
	find := func(vms *vmIndex, configs map[int]map[string]string) ([]multiRefRecord, []int) {
		return findMultiplyReferenced(vms, configs, defaultOpts())
	}

	t.Run("active slot and unused entry on two guests", func(t *testing.T) {
		records, unreadable := find(index(vmInfo{vmid: 777, name: "web-0"},
			vmInfo{vmid: 90656, name: "bosh-parker-90656", tags: "bosh-cpi;bosh-parker"}), map[int]map[string]string{
			777:   {"unused0": "a:123/vm-123-disk-0.raw"},
			90656: {"scsi1": "a:123/vm-123-disk-0.raw,serial=" + testBPD},
		})
		assert.Equal(t, []int{}, unreadable)
		require.Len(t, records, 1)
		assert.Equal(t, 123, *records[0].ownerVMID)
		assert.False(t, records[0].ownerFound)
		refs := referencesOf(records[0])
		assert.Equal(t, "unused", refs[[2]any{777, "unused0"}].kind)
		assert.False(t, refs[[2]any{777, "unused0"}].parker)
		assert.Equal(t, "active", refs[[2]any{90656, "scsi1"}].kind)
		assert.True(t, refs[[2]any{90656, "scsi1"}].parker)
	})
	t.Run("two active slots on two guests", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 777}, vmInfo{vmid: 888, node: "pve2"}), map[int]map[string]string{
			777: {"scsi1": "a:777/vm-777-disk-2.raw,size=5G"}, 888: {"virtio2": "a:777/vm-777-disk-2.raw"},
		})
		require.Len(t, records, 1)
		for _, ref := range records[0].references {
			assert.Equal(t, "active", ref.kind)
		}
	})
	t.Run("same guest twice is not a finding", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 777}), map[int]map[string]string{
			777: {"scsi1": "a:777/vm-777-disk-2.raw", "unused0": "a:777/vm-777-disk-2.raw"},
		})
		assert.Empty(t, records)
	})
	t.Run("cdrom, none, and passthrough are skipped", func(t *testing.T) {
		shared := func() map[string]string {
			return map[string]string{
				"ide2":  "local:iso/ubuntu.iso,media=cdrom",
				"ide3":  "local-lvm:vm-100-cloudinit,media=cdrom",
				"ide0":  "none,media=cdrom",
				"sata0": "/dev/disk/by-id/ata-shared",
				"scsi3": "none",
			}
		}
		records, _ := find(index(vmInfo{vmid: 777}, vmInfo{vmid: 888}),
			map[int]map[string]string{777: shared(), 888: shared()})
		assert.Empty(t, records)
	})
	t.Run("a volume outside the disk band is reported", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 777}, vmInfo{vmid: 90100, tags: "bosh-parker"}),
			map[int]map[string]string{
				777: {"unused0": "a:90100/vm-90100-disk-3.raw"}, 90100: {"scsi0": "a:90100/vm-90100-disk-3.raw"},
			})
		require.Len(t, records, 1)
		assert.Equal(t, "a:90100/vm-90100-disk-3.raw", records[0].volid)
	})
	t.Run("the owner marker goes on the owner only", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 777}, vmInfo{vmid: 888}), map[int]map[string]string{
			777: {"scsi1": "a:777/vm-777-disk-2.raw"}, 888: {"unused0": "a:777/vm-777-disk-2.raw"},
		})
		refs := referencesOf(records[0])
		assert.True(t, refs[[2]any{777, "scsi1"}].owns)
		assert.False(t, refs[[2]any{888, "unused0"}].owns)
		assert.True(t, records[0].ownerHolds)
	})
	t.Run("a non-owner unused reference carries no owner marker", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 777}, vmInfo{vmid: 888}), map[int]map[string]string{
			777: {"unused0": "a:888/vm-888-disk-1.raw"}, 888: {"scsi1": "a:888/vm-888-disk-1.raw"},
		})
		unused := referencesOf(records[0])[[2]any{777, "unused0"}]
		assert.Equal(t, "unused", unused.kind)
		assert.False(t, unused.owns)
	})
	t.Run("the owner is present but holds no reference", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 123, name: "reused-vmid"}, vmInfo{vmid: 777}, vmInfo{vmid: 888}),
			map[int]map[string]string{
				123: {}, 777: {"unused0": "a:123/vm-123-disk-0.raw"}, 888: {"scsi1": "a:123/vm-123-disk-0.raw"},
			})
		mr := records[0]
		assert.Equal(t, 123, *mr.ownerVMID)
		assert.True(t, mr.ownerFound)
		assert.False(t, mr.ownerHolds)
		assert.Contains(t, ownerNote(mr), "destroy-unreferenced-disks")
	})
	t.Run("a name with no owner gives a null owner", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 777}, vmInfo{vmid: 888}), map[int]map[string]string{
			777: {"scsi1": "nfs:custom/data.raw"}, 888: {"unused0": "nfs:custom/data.raw"},
		})
		assert.Nil(t, records[0].ownerVMID)
		assert.Equal(t, "no VM owns this volume by name", ownerNote(records[0]))
	})
	t.Run("a container row is skipped and is not unreadable", func(t *testing.T) {
		records, unreadable := find(index(vmInfo{vmid: 200, typ: "lxc"}, vmInfo{vmid: 777}),
			map[int]map[string]string{777: {}})
		assert.Empty(t, records)
		assert.Equal(t, []int{}, unreadable)
	})
	t.Run("an unreadable QEMU guest is listed", func(t *testing.T) {
		_, unreadable := find(index(vmInfo{vmid: 778}, vmInfo{vmid: 777}), map[int]map[string]string{777: {}})
		assert.Equal(t, []int{778}, unreadable)
	})
	t.Run("records come back ordered by volid", func(t *testing.T) {
		records, _ := find(index(vmInfo{vmid: 777}, vmInfo{vmid: 888}), map[int]map[string]string{
			777: {"scsi1": "b:vm-1-disk-0", "scsi2": "a:vm-1-disk-0"},
			888: {"unused0": "b:vm-1-disk-0", "unused1": "a:vm-1-disk-0"},
		})
		require.Len(t, records, 2)
		assert.Equal(t, "a:vm-1-disk-0", records[0].volid)
		assert.Equal(t, "b:vm-1-disk-0", records[1].volid)
	})
}

func TestReferenceText(t *testing.T) {
	assert.Equal(t, "VM 1 (unnamed) on ? scsi0", referenceText(multiRefEntry{vmid: 1, slot: "scsi0"}))
	assert.Equal(t, "VM 1 (a) on pve1 scsi0 [owns by name, parker]",
		referenceText(multiRefEntry{vmid: 1, name: "a", node: "pve1", slot: "scsi0", owns: true, parker: true}))
}

func TestFindingsError(t *testing.T) {
	assert.NoError(t, findingsError(&inventory{multiRef: fullCoverage(), disks: []*diskRecord{
		{volid: "a:x", classification: "attached"},
	}}))

	err := findingsError(&inventory{multiRef: fullCoverage(), disks: []*diskRecord{
		{volid: "a:vm-9002-disk-0", classification: "free-floating"},
		{volid: "a:vm-9001-disk-0", classification: "free-floating", holderVMID: new(500), holderSlot: "unused0"},
		{volid: "a:vm-9000-disk-0", classification: "free-floating", holderVMID: new(501), holderSlot: "unused1"},
	}})
	require.Error(t, err)
	assert.Equal(t, exitcode.AuditFindings, exitcode.FromError(err))
	assert.Equal(t, "EXIT 9: 3 free-floating disk(s) found, a:vm-9000-disk-0 still named by VM 501, "+
		"a:vm-9001-disk-0 still named by VM 500.", err.Error())

	plain := findingsError(&inventory{multiRef: fullCoverage(), disks: []*diskRecord{
		{volid: "a:vm-9002-disk-0", classification: "free-floating"},
	}})
	assert.Equal(t, "EXIT 9: 1 free-floating disk(s) found.", plain.Error())
}

func TestBuildDocument_EmptyListsAreArrays(t *testing.T) {
	got := mustJSON(t, buildDocument(&inventory{multiRef: fullCoverage()}, testHeader()))
	assert.Equal(t, `{"host":"pve.example.com","port":8006,"disk_band":[9000,29999],"parker_band":[90000,90999],`+
		`"summary":{"total":0,"attached":0,"parked":0,"free_floating":0,"unknown":0,"multiply_referenced":0},`+
		`"disks":[],"parkers":[],"skipped_storages":[],"multiply_referenced":[],`+
		`"multiply_referenced_unreadable_vmids":[],"multiply_referenced_visibility":"full",`+
		`"multiply_referenced_complete":true}`, got)
}

func TestDiskToJSON_KeyOrder(t *testing.T) {
	rec := &diskRecord{
		volid: "a:v", storage: "a", node: "pve1", sizeBytes: 1, classification: "free-floating",
		holderVMID: new(0), holderNode: "pve1", holderName: "h", diskCID: "c", sourceVMCID: "s",
		parkedAt: "t", parkedNode: "pn", directorID: "d", holderSlot: "unused0", discoveredBy: []string{"sentinel"},
		sentinelOnly: true, sentinelVMID: new(90000), sentinelName: "p",
	}
	assert.Equal(t, `{"volid":"a:v","storage":"a","node":"pve1","size_bytes":1,"classification":"free-floating",`+
		`"holder_vmid":0,"holder_node":"pve1","holder_name":"h","disk_cid":"c","source_vm_cid":"s","parked_at":"t",`+
		`"parked_node":"pn","director_id":"d","holder_slot":"unused0","held_by_unused_entry":true,`+
		`"discovered_by":["sentinel"],"found_only_by_sentinel":true,"sentinel_vmid":90000,"sentinel_name":"p"}`,
		mustJSON(t, diskToJSON(rec)))
}
