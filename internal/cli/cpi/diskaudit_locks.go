package cpi

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// lockPoolPrefix is the prefix the CPI gives every sentinel pool it uses as a
// cross-process lock: bosh-lock-vm-<vmid> for the per-VMID lock, and
// bosh-lock-aa-<group> for the anti-affinity lock. Both carry the claim
// comment "owner=<token> exp=<unix-seconds>".
const lockPoolPrefix = "bosh-lock-"

// vmLockPoolPrefix is the per-VMID lock family inside lockPoolPrefix.
const vmLockPoolPrefix = lockPoolPrefix + "vm-"

// lockFieldLimit caps how much of a pool name, owner token, or comment a
// warning quotes. The CPI writes short claims, but anyone with Pool.Allocate
// can put any text on a pool.
const lockFieldLimit = 128

// lockPool is one CPI lock pool as the audit read it.
type lockPool struct {
	name    string
	owner   string
	comment string
	// expiry is the claim's expiry, and is meaningful only when expiryOK.
	expiry   time.Time
	expiryOK bool
}

// lockPoolReport is what the audit found when it read the pool list. When
// readError is not empty the read failed, and pools is empty.
type lockPoolReport struct {
	pools     []lockPool
	readError string
}

// outOfBandParker is a VM that carries the bosh-parker tag with a VMID
// outside the parker band, so the audit does not count it as a parker.
type outOfBandParker struct {
	vmid int
	node string
	pool string
}

// flattenText keeps printable ASCII, replaces everything else with '?', and
// cuts the result at lockFieldLimit bytes.
func flattenText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= lockFieldLimit {
			b.WriteString("...")
			break
		}
		if r < 0x20 || r > 0x7e {
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// parseLockClaim reads the owner and the expiry from a lock pool comment the
// way the CPI does. The first owner= field and the first exp= field win. The
// expiry is not ok when the field is missing or is not a base-10 integer. A
// negative expiry parses, and like the CPI we read it as long past.
func parseLockClaim(comment string) (owner string, expiry time.Time, expiryOK bool) {
	ownerSeen, expSeen := false, false
	for field := range strings.FieldsSeq(comment) {
		if v, ok := strings.CutPrefix(field, "owner="); ok && !ownerSeen {
			owner, ownerSeen = v, true
		} else if v, ok := strings.CutPrefix(field, "exp="); ok && !expSeen {
			expSeen = true
			if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
				expiry, expiryOK = time.Unix(secs, 0).UTC(), true
			}
		}
	}
	return owner, expiry, expiryOK
}

// lockPoolsFromRows picks the CPI lock pools out of the GET /pools rows, in
// name order. A row that is not a lock pool is ignored.
func lockPoolsFromRows(rows []map[string]any) []lockPool {
	var out []lockPool
	for _, r := range rows {
		name := strings.TrimSpace(strOr(r["poolid"]))
		if !strings.HasPrefix(name, lockPoolPrefix) {
			continue
		}
		comment := strOr(r["comment"])
		owner, expiry, ok := parseLockClaim(comment)
		out = append(out, lockPool{name: name, owner: owner, comment: comment, expiry: expiry, expiryOK: ok})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// findOutOfBandParkers returns every guest that carries the bosh-parker tag
// and has a VMID outside the parker band, ordered by VMID.
func findOutOfBandParkers(vms *vmIndex, opts auditOptions) []outOfBandParker {
	var out []outOfBandParker
	for _, id := range vms.sortedIDs() {
		info := vms.byID[id]
		if opts.inParkerBand(id) || !tagsContainParker(info.tags) {
			continue
		}
		out = append(out, outOfBandParker{vmid: id, node: info.node, pool: info.pool})
	}
	return out
}

// lockOwnerText is the owner a warning names.
func lockOwnerText(p lockPool) string {
	if p.owner == "" {
		return "unknown"
	}
	return flattenText(p.owner)
}

// lockHolderText says whose operation the lock guards, for the check an
// operator makes before deleting it.
func lockHolderText(name string) string {
	if v, ok := strings.CutPrefix(name, vmLockPoolPrefix); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return fmt.Sprintf("for VMID %d", n)
		}
	}
	return "that uses this lock"
}

// emitLockWarnings writes the warnings about the CPI lock pools. An expired
// claim and a claim with no readable expiry each get their own line, and a
// failed read gets one line and nothing else.
func emitLockWarnings(w io.Writer, locks lockPoolReport, now time.Time) {
	if locks.readError != "" {
		warn(w, "could not check the CPI lock pools for expired claims (%s); the audit result is not affected",
			locks.readError)
		return
	}
	for _, p := range locks.pools {
		name := flattenText(p.name)
		if !p.expiryOK {
			warn(w, "CPI lock pool %s has a missing or unreadable expiry (owner %s, comment %q), so it is "+
				"not treated as expired. Inspect it with: pmx pve pool get %s",
				name, lockOwnerText(p), flattenText(p.comment), name)
			continue
		}
		// The CPI holds a claim only while now is before exp, so a claim is
		// stale from the exp second on.
		if !now.Before(p.expiry) {
			warn(w, "CPI lock pool %s expired at %s (owner %s) and was never released. Make sure no CPI "+
				"operation is still running %s, then remove the pool with: pmx pve pool delete %s",
				name, p.expiry.UTC().Format(time.RFC3339), lockOwnerText(p), lockHolderText(p.name), name)
		}
	}
}

// emitOutOfBandParkerWarning writes one warning that lists every VM with the
// bosh-parker tag outside the parker band.
func emitOutOfBandParkerWarning(w io.Writer, parkers []outOfBandParker, opts auditOptions) {
	if len(parkers) == 0 {
		return
	}
	items := make([]string, len(parkers))
	for i, p := range parkers {
		node := p.node
		if node == "" {
			node = "?"
		}
		items[i] = fmt.Sprintf("VM %d (node %s, pool %s)", p.vmid, flattenText(node), flattenText(poolOrNone(p.pool)))
	}
	warn(w, "%d VM(s) carry the bosh-parker tag but have a VMID outside --parker-band %d-%d, so they are "+
		"not counted as parkers: %s. If they are real parkers, rerun with a wider --parker-band",
		len(parkers), opts.parkerStart, opts.parkerEnd, strings.Join(items, ", "))
}
