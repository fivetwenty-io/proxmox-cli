package cpi

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
)

// lockNow is the instant the lock tests pin the clock to, one second after
// the expiry of the live example claim.
var lockNow = time.Unix(1788965001, 0).UTC()

func lockOpts() auditOptions {
	opts := defaultOpts()
	opts.now = func() time.Time { return lockNow }
	return opts
}

func poolRow(name, comment string) map[string]any {
	return map[string]any{"poolid": name, "comment": comment}
}

// lockWarnings collects the inventory for a stub whose pool list is pools,
// then renders the warnings with the pinned clock.
func lockWarnings(t *testing.T, pools ...map[string]any) string {
	t.Helper()
	client := newStub(nil, nil)
	client.pools = pools
	run := runInventory(t, client, lockOpts())
	return warnings(run.inv, lockOpts())
}

func TestLockPools_ExpiredClaimWarns(t *testing.T) {
	out := lockWarnings(t, poolRow("bosh-lock-vm-90283", "owner=unpark/18332/90283 exp=1788965000"))
	assert.Equal(t, "WARN: CPI lock pool bosh-lock-vm-90283 expired at 2026-09-09T14:43:20Z "+
		"(owner unpark/18332/90283) and was never released. Make sure no CPI operation is still running "+
		"for VMID 90283, then remove the pool with: pmx pve pool delete bosh-lock-vm-90283\n", out)
	assert.NotContains(t, out, "pvesh")
	assert.NotContains(t, out, "qm ")
}

func TestLockPools_ClaimExpiringExactlyNowIsExpired(t *testing.T) {
	client := newStub(nil, nil)
	client.pools = []map[string]any{poolRow("bosh-lock-vm-90283", "owner=a exp=1788965001")}
	run := runInventory(t, client, lockOpts())
	assert.Contains(t, warnings(run.inv, lockOpts()), "CPI lock pool bosh-lock-vm-90283 expired at")
}

func TestLockPools_NegativeExpiryIsExpired(t *testing.T) {
	out := lockWarnings(t, poolRow("bosh-lock-vm-90283", "owner=a exp=-5"))
	assert.Contains(t, out, "CPI lock pool bosh-lock-vm-90283 expired at 1969-12-31T23:59:55Z")
}

func TestLockPools_UnexpiredClaimDoesNotWarn(t *testing.T) {
	assert.Empty(t, lockWarnings(t, poolRow("bosh-lock-vm-90283", "owner=unpark/1/90283 exp=1788966000")))
}

func TestLockPools_ExpiredAntiAffinityLockWarnsWithoutAVMID(t *testing.T) {
	out := lockWarnings(t, poolRow("bosh-lock-aa-web", "owner=host/9/web exp=1788965000"))
	assert.Equal(t, "WARN: CPI lock pool bosh-lock-aa-web expired at 2026-09-09T14:43:20Z (owner host/9/web) "+
		"and was never released. Make sure no CPI operation is still running that uses this lock, then "+
		"remove the pool with: pmx pve pool delete bosh-lock-aa-web\n", out)
}

func TestLockPools_BadOrMissingExpiryIsNeverExpired(t *testing.T) {
	cases := map[string]string{
		"no exp":           "owner=unpark/1/90283",
		"empty comment":    "",
		"non-numeric exp":  "owner=unpark/1/90283 exp=soon",
		"fractional exp":   "owner=unpark/1/90283 exp=17889.5",
		"empty exp":        "owner=unpark/1/90283 exp=",
		"unrelated text":   "created by hand",
		"first exp is bad": "owner=unpark/1/90283 exp=x exp=1000",
	}
	for name, comment := range cases {
		t.Run(name, func(t *testing.T) {
			out := lockWarnings(t, poolRow("bosh-lock-vm-90283", comment))
			assert.Contains(t, out, "WARN: CPI lock pool bosh-lock-vm-90283 has a missing or unreadable expiry")
			assert.Contains(t, out, "so it is not treated as expired. Inspect it with: pmx pve pool get "+
				"bosh-lock-vm-90283\n")
			assert.NotContains(t, out, "expired at")
			assert.NotContains(t, out, "pool delete")
		})
	}
}

func TestLockPools_BadExpiryWarningNamesOwnerAndComment(t *testing.T) {
	out := lockWarnings(t, poolRow("bosh-lock-vm-90283", "owner=unpark/1/90283 exp=soon"))
	assert.Equal(t, "WARN: CPI lock pool bosh-lock-vm-90283 has a missing or unreadable expiry (owner "+
		"unpark/1/90283, comment \"owner=unpark/1/90283 exp=soon\"), so it is not treated as expired. "+
		"Inspect it with: pmx pve pool get bosh-lock-vm-90283\n", out)
}

func TestLockPools_MissingOwnerIsNamedUnknown(t *testing.T) {
	out := lockWarnings(t, poolRow("bosh-lock-vm-90283", "exp=1788965000"))
	assert.Contains(t, out, "(owner unknown)")
}

func TestLockPools_CommentIsFlattenedAndCapped(t *testing.T) {
	long := "owner=" + string(bytes.Repeat([]byte("x"), 500)) + " exp=bad\x1b[31m"
	out := lockWarnings(t, poolRow("bosh-lock-vm-1", long))
	assert.NotContains(t, out, "\x1b")
	assert.Contains(t, out, "...")
	assert.Less(t, len(out), 700)
}

func TestLockPools_OtherPoolsAreIgnored(t *testing.T) {
	out := lockWarnings(t,
		poolRow("bosh-parker", "exp=1"),
		poolRow("lock-vm-1", "owner=a exp=1"),
		map[string]any{"poolid": "bosh-lock-vm-5", "comment": "owner=a exp=1788965000"},
		map[string]any{"comment": "no poolid"},
		map[string]any{"poolid": "bosh-lock-vm-6"})
	assert.Contains(t, out, "bosh-lock-vm-5 expired at")
	assert.Contains(t, out, "bosh-lock-vm-6 has a missing or unreadable expiry")
	assert.NotContains(t, out, "bosh-parker")
	assert.NotContains(t, out, "lock-vm-1 ")
}

func TestLockPools_WarningsFollowPoolNameOrder(t *testing.T) {
	out := lockWarnings(t,
		poolRow("bosh-lock-vm-9", "owner=a exp=1788965000"),
		poolRow("bosh-lock-vm-10", "owner=a exp=1788965000"),
		poolRow("bosh-lock-aa-x", "owner=a exp=bad"))
	first := bytes.Index([]byte(out), []byte("bosh-lock-aa-x"))
	second := bytes.Index([]byte(out), []byte("bosh-lock-vm-10"))
	third := bytes.Index([]byte(out), []byte("bosh-lock-vm-9 "))
	assert.True(t, first >= 0 && first < second && second < third, out)
}

func TestLockPools_FailedReadWarnsOnceAndKeepsTheAudit(t *testing.T) {
	client := diskStub([]map[string]any{row(777, "web-0", "", "qemu")},
		map[int]map[string]string{777: {}}, "a:vm-9001-disk-0")
	client.poolErr = errors.New("GET /pools failed: connection reset")
	run := runInventory(t, client, lockOpts())

	out := warnings(run.inv, lockOpts())
	assert.Equal(t, "WARN: could not check the CPI lock pools for expired claims "+
		"(GET /pools failed: connection reset); the audit result is not affected\n", out)

	// The disk classification and the exit code come out as if the read had
	// worked: the one disk is free-floating, so the run exits 9.
	require.Len(t, run.inv.disks, 1)
	assert.Equal(t, "free-floating", run.inv.disks[0].classification)
	assert.Equal(t, exitcode.AuditFindings, exitcode.FromError(findingsError(run.inv)))
}

func TestLockPools_FailedReadDoesNotInventExpiredClaims(t *testing.T) {
	client := newStub(nil, nil)
	client.poolErr = errStubRead
	client.pools = []map[string]any{poolRow("bosh-lock-vm-1", "owner=a exp=1")}
	run := runInventory(t, client, lockOpts())
	out := warnings(run.inv, lockOpts())
	assert.NotContains(t, out, "expired at")
	assert.Equal(t, 1, bytes.Count([]byte(out), []byte("WARN:")))
}

func TestLockPools_ReadShowsAsAnHTTPReasonWhenPVERefuses(t *testing.T) {
	c := newFakeCluster(t)
	populate(t, c)
	c.fail("/pools", 403)
	res := c.run(t, "-o", "json")
	require.Equal(t, exitcode.AuditFindings, res.code(), res.stderr)
	assert.Contains(t, res.stderr, "WARN: could not check the CPI lock pools for expired claims (HTTP 403 ")
}

func TestLockPools_CancelledDuringThePoolReadEndsTheRun(t *testing.T) {
	client := newStub(nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.cancelOnPools = cancel
	inv, err := collectInventory(ctx, client, lockOpts(), &bytes.Buffer{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, inv)
}

func TestLockPools_NothingToReportMeansNoWarning(t *testing.T) {
	assert.Empty(t, lockWarnings(t))
	assert.Empty(t, lockWarnings(t, poolRow("backups", "nightly")))
}

func TestPveReader_ListPoolsRejectsAReplyThatIsNotAList(t *testing.T) {
	reader := &pveReader{raw: stubRaw{data: map[string]any{"poolid": "x"}}}
	_, err := reader.listPools(context.Background())
	require.ErrorContains(t, err, "GET /pools: expected a list of pools, got object")

	rows, err := (&pveReader{raw: stubRaw{}}).listPools(context.Background())
	require.NoError(t, err)
	assert.Empty(t, rows)

	rows, err = (&pveReader{raw: stubRaw{data: []any{map[string]any{"poolid": "a"}, "junk"}}}).
		listPools(context.Background())
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

// stubRaw is a rawGetter that answers every GET with one fixed reply.
type stubRaw struct {
	data any
	err  error
}

func (s stubRaw) GetCtx(context.Context, string, map[string]any) (any, error) {
	return s.data, s.err
}

func oobRows() []map[string]any {
	return []map[string]any{
		row(104166, "stray-b", "bosh-parker", "qemu"),
		row(90656, "bosh-parker-90656", "bosh-cpi;bosh-parker", "qemu"),
		row(91509, "stray-a", "bosh-cpi;BOSH-Parker", "qemu"),
		row(104059, "stray-c", "bosh-parker", "qemu"),
		row(91514, "stray-d", "bosh-parker", "qemu"),
		row(95000, "plain", "web", "qemu"),
		row(89999, "below-band", "", "qemu"),
	}
}

// emptyConfigs gives every row's guest an empty config, so each one reads.
func emptyConfigs(rows []map[string]any) map[int]map[string]string {
	configs := map[int]map[string]string{}
	for _, r := range rows {
		configs[int(r["vmid"].(float64))] = map[string]string{}
	}
	return configs
}

func TestOutOfBandParkers_WarnOnceOrderedByVMID(t *testing.T) {
	rows := oobRows()
	rows[0]["node"] = "pve2"
	rows[0]["pool"] = "legacy"
	client := newStub(rows, emptyConfigs(rows))
	run := runInventory(t, client, lockOpts())

	out := warnings(run.inv, lockOpts())
	assert.Equal(t, 1, strings.Count(out, "carry the bosh-parker tag"), out)
	assert.Contains(t, out, "WARN: 4 VM(s) carry the bosh-parker tag but have a VMID outside --parker-band "+
		"90000-90999, so they are not counted as parkers: VM 91509 (node pve1, pool none), "+
		"VM 91514 (node pve1, pool none), VM 104059 (node pve1, pool none), VM 104166 (node pve2, pool legacy). "+
		"If they are real parkers, rerun with a wider --parker-band\n")
}

func TestOutOfBandParkers_StayOutOfTheParkerCountAndTheClassification(t *testing.T) {
	configs := emptyConfigs(oobRows())
	configs[90656] = map[string]string{"scsi0": "a:vm-90656-disk-0"}
	client := newStub(oobRows(), configs)
	client.storages = []map[string]any{images("a", nil)}
	client.content["a"] = []string{"a:vm-90656-disk-0"}
	run := runInventory(t, client, lockOpts())

	require.Len(t, run.inv.parkers, 1)
	assert.Equal(t, 90656, run.inv.parkers[0].vmid)
	require.Len(t, run.inv.outOfBandParkers, 4)
	assert.Equal(t, "parked", run.disks["a:vm-90656-disk-0"].classification)
	assert.NoError(t, findingsError(run.inv))
}

func TestOutOfBandParkers_AWiderBandCountsThemAndSilencesTheWarning(t *testing.T) {
	opts := lockOpts()
	opts.parkerEnd = 110000
	client := newStub(oobRows(), emptyConfigs(oobRows()))
	run := runInventory(t, client, opts)
	assert.Empty(t, run.inv.outOfBandParkers)
	assert.NotContains(t, warnings(run.inv, opts), "--parker-band")
	assert.Len(t, run.inv.parkers, 5)
}

func TestOutOfBandParkers_NoneMeansNoWarning(t *testing.T) {
	rows := []map[string]any{
		row(90656, "bosh-parker-90656", "bosh-parker", "qemu"),
		row(104059, "untagged", "", "qemu"),
	}
	client := newStub(rows, emptyConfigs(rows))
	client.views[90656] = &pendingViews{current: map[string]string{"scsi0": "a:vm-90656-disk-0"},
		applied: map[string]string{"scsi0": "a:vm-90656-disk-0"}}
	run := runInventory(t, client, lockOpts())
	assert.Empty(t, run.inv.outOfBandParkers)
	assert.Empty(t, warnings(run.inv, lockOpts()))
}

// TestDiskAuditCmd_LockAndParkerWarningsLeaveTheDocumentAlone runs the audit
// against one cluster before and after an expired lock pool, a lock pool with
// a bad expiry, and out-of-band parkers appear. The JSON and YAML documents
// and the exit code must come out the same, and the warnings must land on
// stderr only.
func TestDiskAuditCmd_LockAndParkerWarningsLeaveTheDocumentAlone(t *testing.T) {
	c := newFakeCluster(t)
	populate(t, c)
	beforeJSON := c.run(t, "-o", "json")
	beforeYAML := c.run(t, "-o", "yaml")
	beforeText := c.run(t)
	require.Equal(t, exitcode.AuditFindings, beforeJSON.code(), beforeJSON.stderr)
	assert.NotContains(t, beforeJSON.stderr, "lock pool")
	assert.NotContains(t, beforeJSON.stderr, "parker-band")

	rows := append(fixRows(), row(91509, "stray-a", "bosh-parker", "qemu"), row(104059, "stray-b", "bosh-parker", "qemu"))
	configs := map[int]map[string]string{
		777:    {"scsi1": "a:vm-9001-disk-0,size=1G"},
		778:    {"unused0": "a:vm-9001-disk-0"},
		500:    {},
		90656:  {"scsi0": "a:vm-90656-disk-0", "description": sentinelDescription(t)},
		91509:  {},
		104059: {},
	}
	c.guests(rows, configs)
	c.set("/pools", []any{
		map[string]any{"poolid": "bosh-lock-vm-90283", "comment": "owner=unpark/18332/90283 exp=1000000000"},
		map[string]any{"poolid": "bosh-lock-vm-90284", "comment": "owner=unpark/1/90284 exp=later"},
		map[string]any{"poolid": "bosh-lock-vm-90285", "comment": "owner=unpark/2/90285 exp=4102444800"},
	})

	afterJSON := c.run(t, "-o", "json")
	afterYAML := c.run(t, "-o", "yaml")
	afterText := c.run(t)

	assert.Equal(t, beforeJSON.stdout, afterJSON.stdout)
	assert.Equal(t, beforeYAML.stdout, afterYAML.stdout)
	assert.Equal(t, beforeText.stdout, afterText.stdout)
	for _, pair := range [][2]cmdResult{{beforeJSON, afterJSON}, {beforeYAML, afterYAML}, {beforeText, afterText}} {
		assert.Equal(t, exitcode.AuditFindings, pair[1].code(), pair[1].stderr)
		assert.Equal(t, pair[0].err.Error(), pair[1].err.Error())
		assert.Contains(t, pair[1].stderr, "WARN: CPI lock pool bosh-lock-vm-90283 expired at 2001-09-09T01:46:40Z "+
			"(owner unpark/18332/90283)")
		assert.Contains(t, pair[1].stderr, "WARN: CPI lock pool bosh-lock-vm-90284 has a missing or unreadable expiry")
		assert.NotContains(t, pair[1].stderr, "bosh-lock-vm-90285")
		assert.Contains(t, pair[1].stderr, "WARN: 2 VM(s) carry the bosh-parker tag but have a VMID outside "+
			"--parker-band 90000-90999, so they are not counted as parkers: VM 91509 (node pve1, pool none), "+
			"VM 104059 (node pve1, pool none)")
	}
	for _, out := range []string{afterJSON.stdout, afterYAML.stdout} {
		assert.NotContains(t, out, "bosh-lock")
		assert.NotContains(t, out, "91509")
	}
}

func TestDiskAuditCmd_CleanClusterWithLockPoolsAndNoStraysStaysQuiet(t *testing.T) {
	c := newFakeCluster(t)
	c.guests(fixRows(), map[int]map[string]string{
		777: {"scsi1": "a:vm-9001-disk-0"}, 778: {}, 500: {}, 90656: {"scsi0": "a:vm-90656-disk-0"},
	})
	c.storage("pve1", "a", "a:vm-9001-disk-0", "a:vm-90656-disk-0")
	c.set("/pools", []any{
		map[string]any{"poolid": "bosh-lock-vm-90285", "comment": "owner=unpark/2/90285 exp=4102444800"},
		map[string]any{"poolid": "backups", "comment": "nightly"},
	})
	res := c.run(t)
	require.NoError(t, res.err, res.stderr)
	assert.Empty(t, res.stderr)
}

// sentinelDescription is the parker description populate gives guest 90656.
func sentinelDescription(t *testing.T) string {
	t.Helper()
	return sentinel(t, map[string]any{"bosh_parked_disks": map[string]any{testBPD: map[string]any{
		"volid": "a:vm-90656-disk-0", "disk_cid": "cid-1", "node": "pve1",
		"source_vm_cid": "vm-1", "parked_at": "2026-01-01T00:00:00Z", "director_id": "dir-1",
	}}})
}
