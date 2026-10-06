package cpi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBPD = "bpd-0011223344556677"

// sentinel wraps data in the <!--BOSH:{...}--> marker the CPI writes into a
// VM description.
func sentinel(t *testing.T, data any) string {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	return "<!--BOSH:" + string(b) + "-->"
}

func volidSet(names ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func TestStableIDFromDriveOptStr(t *testing.T) {
	cases := map[string]string{
		"a:vm-1-disk-0,serial=" + testBPD + ",size=8G": testBPD,
		"a:vm-1-disk-0,size=8G,serial=" + testBPD:      testBPD,
		"a:vm-1-disk-0,serial=guest123,size=8G":        "",
		"a:vm-1-disk-0,size=8G":                        "",
		"serial=" + testBPD:                            "", // the volid slot is never an option
		"a:vm-1-disk-0":                                "",
		// The first serial decides, even when a later one is a bpd- token.
		"a:vm-1-disk-0,serial=guest,serial=" + testBPD: "",
	}
	for in, want := range cases {
		assert.Equal(t, want, stableIDFromDriveOptStr(in), in)
	}
}

func TestParsePendingViews_Shape(t *testing.T) {
	rows := []any{
		map[string]any{"key": "scsi1", "value": "a:vm-777-disk-2,serial=" + testBPD, "delete": float64(1)},
		map[string]any{"key": "scsi2", "pending": "a:vm-777-disk-3,serial=bpd-aabbccddeeff0011"},
		map[string]any{"key": "scsi3", "value": "a:vm-777-disk-4", "pending": "a:vm-777-disk-5"},
		map[string]any{"key": "digest", "value": "abc"},
		map[string]any{"key": "memory", "value": float64(2048)},
		map[string]any{"key": "onboot", "value": true},
		map[string]any{"key": "scsi4", "value": "a:vm-777-disk-6", "delete": "0"},
		map[string]any{"key": "scsi5", "value": "a:vm-777-disk-7", "delete": true},
		map[string]any{"key": "", "value": "skipped"},
		map[string]any{"value": "no key"},
		"not a row",
		map[string]any{"key": "nested", "value": map[string]any{"x": 1}},
	}
	v := parsePendingViews(rows)
	require.NotNil(t, v)

	assert.Contains(t, v.current, "scsi1")
	assert.NotContains(t, v.applied, "scsi1")
	assert.NotContains(t, v.current, "scsi2")
	assert.Equal(t, "a:vm-777-disk-3,serial=bpd-aabbccddeeff0011", v.applied["scsi2"])
	assert.Equal(t, "a:vm-777-disk-4", v.current["scsi3"])
	assert.Equal(t, "a:vm-777-disk-5", v.applied["scsi3"])
	assert.Equal(t, "2048", v.applied["memory"])
	assert.Equal(t, "1", v.current["onboot"])
	assert.Equal(t, "a:vm-777-disk-6", v.applied["scsi4"], "delete=0 is no pending delete")
	assert.NotContains(t, v.applied, "scsi5")
	assert.NotContains(t, v.current, "")
	assert.NotContains(t, v.current, "nested")
	assert.NotContains(t, v.applied, "nested")

	assert.Nil(t, parsePendingViews(nil))
	assert.Nil(t, parsePendingViews(map[string]any{"scsi0": "x"}))
}

func TestMergeViews_KeepsAPendingDelete(t *testing.T) {
	v := parsePendingViews([]any{
		map[string]any{"key": "scsi1", "value": "a:vm-777-disk-2", "delete": float64(1)},
		map[string]any{"key": "scsi3", "value": "a:vm-777-disk-4", "pending": "a:vm-777-disk-5"},
	})
	merged := mergeViews(v)
	assert.Contains(t, merged, "scsi1")
	assert.Equal(t, "a:vm-777-disk-5", merged["scsi3"])
}

func TestViewsBusValues_DistinctAndOrdered(t *testing.T) {
	v := &pendingViews{
		current: map[string]string{"scsi1": "a:x", "scsi0": "a:y", "efidisk0": "a:efi", "unused0": "a:u"},
		applied: map[string]string{"scsi0": "a:y", "virtio0": "a:z", "ide2": ""},
	}
	assert.Equal(t, []string{"a:y", "a:x", "a:z"}, viewsBusValues(v))
}

func TestParseSentinelVolumeNames_OnlyFullVolids(t *testing.T) {
	desc := sentinel(t, map[string]any{
		"bosh_attached_disks": map[string]any{"vm-1-disk-0": "c", "a:vm-1-disk-1": "c", testBPD: "c"},
		"bosh_parked_disks": map[string]any{
			testBPD:         map[string]any{"volid": "vm-2-disk-0"},
			"a:vm-3-disk-0": map[string]any{"volid": "a:vm-3-disk-0"},
		},
		"bosh_disk_allocations": map[string]any{"bpd-1": map[string]any{"volid": " b:vm-4-disk-0 "}},
	})
	assert.Equal(t, volidSet("a:vm-1-disk-1", "a:vm-3-disk-0", "b:vm-4-disk-0"), parseSentinelVolumeNames(desc, nil))
}

func TestParseSentinelVolumeNames_SlotNamesTheVolumeOnThatSlot(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{
		testBPD: map[string]any{"volid": "a:vm-1-disk-0", "slot": "scsi3"},
	}})
	got := parseSentinelVolumeNames(desc, map[string]string{"scsi3": "a:vm-90656-disk-1,size=8G"})
	assert.Equal(t, volidSet("a:vm-1-disk-0", "a:vm-90656-disk-1"), got)
	assert.Equal(t, volidSet("a:vm-1-disk-0"), parseSentinelVolumeNames(desc, map[string]string{}))

	// Only bosh_parked_disks entries read the slot.
	alloc := sentinel(t, map[string]any{"bosh_disk_allocations": map[string]any{
		testBPD: map[string]any{"volid": "a:vm-1-disk-0", "slot": "scsi3"},
	}})
	assert.Equal(t, volidSet("a:vm-1-disk-0"),
		parseSentinelVolumeNames(alloc, map[string]string{"scsi3": "a:vm-90656-disk-1"}))
}

func TestParseSentinelVolumeNames_MissingOrCorrupt(t *testing.T) {
	for _, desc := range []string{
		"",
		"plain description",
		"<!--BOSH:{not json}-->",
		"<!--BOSH:[1,2]-->",
		`<!--BOSH:{"bosh_parked_disks": {}} trailing-->`,
	} {
		assert.Empty(t, parseSentinelVolumeNames(desc, nil), desc)
	}
}

func TestParseSentinel_SpansNewlinesAndTakesTheFirst(t *testing.T) {
	desc := "notes\n<!--BOSH:{\"bosh_parked_disks\":\n{\"a:vm-1-disk-0\": {}}}-->\n" +
		`<!--BOSH:{"bosh_parked_disks": {"a:vm-2-disk-0": {}}}-->`
	assert.Equal(t, volidSet("a:vm-1-disk-0"), parseSentinelVolumeNames(desc, nil))
}

func TestParseParkerSentinel(t *testing.T) {
	desc := sentinel(t, map[string]any{
		"bosh_disk_metadata": map[string]any{"x": "y"},
		"bosh_parked_disks":  map[string]any{"a:vm-1-disk-0": map[string]any{"disk_cid": "c"}},
	})
	got := parseParkerSentinel(desc)
	assert.Equal(t, []string{"a:vm-1-disk-0"}, got.keys)

	assert.Equal(t, 0, parseParkerSentinel("").len())
	assert.Equal(t, 0, parseParkerSentinel("<!--BOSH:{oops-->").len())
	assert.Equal(t, 0, parseParkerSentinel(`<!--BOSH:{"bosh_parked_disks": []}-->`).len())
}

func TestDecodeOrderedJSON_RepeatedKeyKeepsFirstPositionAndLastValue(t *testing.T) {
	v, err := decodeOrderedJSON(`{"b": 1, "a": 2, "b": 3}`)
	require.NoError(t, err)
	obj := v.(*orderedObject)
	assert.Equal(t, []string{"b", "a"}, obj.keys)
	assert.Equal(t, json.Number("3"), obj.vals["b"])

	b, err := json.Marshal(obj)
	require.NoError(t, err)
	assert.JSONEq(t, `{"b":3,"a":2}`, string(b))
	assert.Equal(t, `{"b":3,"a":2}`, string(b))

	_, err = decodeOrderedJSON(`{"a": 1} {"b": 2}`)
	assert.Error(t, err)
	_, err = decodeOrderedJSON(``)
	assert.Error(t, err)
}

func TestFindProvenance_DirectKeyThenVolidField(t *testing.T) {
	desc := sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{
		"a:vm-1-disk-0": map[string]any{"disk_cid": "legacy"},
		testBPD:         map[string]any{"volid": "a:vm-2-disk-0", "disk_cid": "stable"},
		"a:vm-3-disk-0": map[string]any{},
		"bpd-ffff":      map[string]any{"volid": "a:vm-3-disk-0", "disk_cid": "fallback"},
	}})
	m := parseParkerSentinel(desc)

	rec := &diskRecord{}
	applyProvenance(rec, findProvenance(m, "a:vm-1-disk-0"))
	assert.Equal(t, "legacy", rec.diskCID)

	rec = &diskRecord{}
	applyProvenance(rec, findProvenance(m, "a:vm-2-disk-0"))
	assert.Equal(t, "stable", rec.diskCID)

	// An empty direct entry falls through to the volid search.
	rec = &diskRecord{}
	applyProvenance(rec, findProvenance(m, "a:vm-3-disk-0"))
	assert.Equal(t, "fallback", rec.diskCID)

	assert.Nil(t, findProvenance(m, "a:vm-9-disk-0"))
}

func TestApplyProvenance_RendersNonStringValues(t *testing.T) {
	v, err := decodeOrderedJSON(`{"disk_cid": 42, "source_vm_cid": null, "parked_at": "",` +
		` "node": true, "director_id": {"k": "v"}}`)
	require.NoError(t, err)
	rec := &diskRecord{}
	applyProvenance(rec, v)
	assert.Equal(t, "42", rec.diskCID)
	assert.Equal(t, "", rec.sourceVMCID)
	assert.Equal(t, "", rec.parkedAt)
	assert.Equal(t, "True", rec.parkedNode)
	assert.Equal(t, `{"k":"v"}`, rec.directorID)

	rec = &diskRecord{}
	applyProvenance(rec, "a string entry carries nothing")
	assert.Equal(t, diskRecord{}, *rec)
}

func TestStorageSkipReason(t *testing.T) {
	for _, entry := range []map[string]any{
		{"enabled": float64(0)}, {"enabled": "0"}, {"enabled": " 0 "}, {"enabled": false},
	} {
		assert.Equal(t, "storage is disabled (enabled=0)", storageSkipReason(entry), entry)
	}
	for _, entry := range []map[string]any{{"active": float64(0)}, {"active": "0"}, {"active": false}} {
		reason := storageSkipReason(entry)
		assert.Contains(t, reason, "not active", entry)
		assert.Contains(t, reason, "partial", entry)
	}
	for _, entry := range []map[string]any{
		{}, {"enabled": float64(1), "active": float64(1)}, {"enabled": "1", "active": "1"},
		{"enabled": nil, "active": nil}, {"enabled": true},
	} {
		assert.Equal(t, "", storageSkipReason(entry), entry)
	}
	// Disabled wins over inactive.
	assert.Contains(t, storageSkipReason(map[string]any{"enabled": float64(0), "active": float64(0)}), "disabled")
}

func TestDiskVMIDFromVolid(t *testing.T) {
	n, ok := diskVMIDFromVolid("local-lvm:vm-9001-disk-0")
	assert.True(t, ok)
	assert.Equal(t, 9001, n)
	for _, v := range []string{"local-lvm:vm-9001-cloudinit", "vm-9001-disk-0", "a:base-9001-disk-0", "a:vm-x-disk-0"} {
		_, ok := diskVMIDFromVolid(v)
		assert.False(t, ok, v)
	}
}

func TestTagsContainParker(t *testing.T) {
	for _, tags := range []string{"bosh-parker", "bosh-cpi;bosh-parker", "bosh-cpi, BOSH-PARKER", "a b bosh-parker"} {
		assert.True(t, tagsContainParker(tags), tags)
	}
	for _, tags := range []string{"", "bosh-cpi", "bosh-parker-x", "xbosh-parker"} {
		assert.False(t, tagsContainParker(tags), tags)
	}
}

func TestVolumeOwnerVMID(t *testing.T) {
	cases := map[string]int{
		"a:777/vm-777-disk-2.raw":                                 777, // dir-style
		"local-lvm:base-100-disk-0/vm-101-disk-0":                 101, // LVM-thin linked clone
		"local:100/base-100-disk-0.qcow2/101/vm-101-disk-0.qcow2": 101, // dir linked clone
		"ceph:vm-123-disk-0":                                      123, // RBD
		"local-lvm:base-100-disk-0":                               100, // template base volume
	}
	for volid, want := range cases {
		got, ok := volumeOwnerVMID(volid)
		assert.True(t, ok, volid)
		assert.Equal(t, want, got, volid)
	}
	for _, volid := range []string{"nfs:custom/data-volume.raw", "nfs:vm-disk-0.raw"} {
		_, ok := volumeOwnerVMID(volid)
		assert.False(t, ok, volid)
	}
}

func TestReferencedVolids_SkipsCdromNoneAndPassthrough(t *testing.T) {
	config := map[string]string{
		"ide2":     "local:iso/ubuntu.iso,media=cdrom",
		"ide3":     "local-lvm:vm-100-cloudinit,media=cdrom",
		"ide0":     "none,media=cdrom",
		"sata0":    "/dev/disk/by-id/ata-shared",
		"scsi3":    "none",
		"efidisk0": "local-lvm:vm-100-disk-9",
		"scsi1":    "a:vm-100-disk-1,size=8G",
		"unused0":  "a:vm-100-disk-2",
		"virtio0":  "a:vm-100-disk-3, MEDIA=CDROM ",
	}
	assert.Equal(t, []volumeRef{
		{slot: "scsi1", kind: "active", volid: "a:vm-100-disk-1"},
		{slot: "unused0", kind: "unused", volid: "a:vm-100-disk-2"},
	}, referencedVolids(config))
}

func TestParseParkerPoolArgs(t *testing.T) {
	assert.Equal(t, []string{}, parseParkerPoolArgs(nil))
	assert.Equal(t, []string{"bosh-parker", "blue-parker"}, parseParkerPoolArgs([]string{"bosh-parker", "blue-parker"}))
	assert.Equal(t, []string{"bosh-parker", "blue-parker"}, parseParkerPoolArgs([]string{"bosh-parker, blue-parker"}))
	assert.Equal(t, []string{"bosh-parker"}, parseParkerPoolArgs([]string{"bosh-parker,,  ", " bosh-parker "}))
}

func TestPyHelpers(t *testing.T) {
	n, ok := pyInt(float64(100))
	assert.True(t, ok)
	assert.Equal(t, 100, n)
	n, ok = pyInt(" 101 ")
	assert.True(t, ok)
	assert.Equal(t, 101, n)
	_, ok = pyInt("abc")
	assert.False(t, ok)
	_, ok = pyInt(map[string]any{})
	assert.False(t, ok)

	assert.Equal(t, "", strOr(nil))
	assert.Equal(t, "", strOr(float64(0)))
	assert.Equal(t, "5", strOr(float64(5)))
	assert.Equal(t, "1.5", strOr(float64(1.5)))
	assert.Equal(t, "x", strOr("x"))
}

func TestHumanSizeAndTrunc(t *testing.T) {
	assert.Equal(t, "—", humanSize(0))
	assert.Equal(t, "—", humanSize(-1))
	assert.Equal(t, "512 B", humanSize(512))
	assert.Equal(t, "1.0 KiB", humanSize(1024))
	assert.Equal(t, "1.5 GiB", humanSize(3<<29))
	assert.Equal(t, "2.0 TiB", humanSize(2<<40))

	assert.Equal(t, "abc", trunc("abc", 3))
	assert.Equal(t, "ab…", trunc("abcd", 3))
	assert.Equal(t, "äb…", trunc("äbcd", 3))
}
