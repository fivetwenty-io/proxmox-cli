// Package peppi guards production peppi workloads against accidental
// mutation by lab tooling. Every mutating verb resolves a Target from the
// lab resource it is about to act on and calls Guard before issuing any API
// request or shell command; a non-nil error means the target overlaps a
// production peppi resource and the caller must abort.
package peppi

import (
	"fmt"
	"strings"
)

// protectedVMIDs are VM IDs that belong to production peppi workloads and
// must never be created, destroyed, or otherwise mutated by lab tooling.
var protectedVMIDs = map[int]bool{
	50000: true,
	50001: true,
	50010: true,
	50020: true,
}

// protectedNamePatterns are substrings that identify production peppi
// resources across pools, vnets, storage IDs, DNS zones, and VM names. A
// match is case-insensitive and checked as a substring, since a production
// name may be embedded in a larger identifier (e.g. a storage ID such as
// "tank-peppiprd-data").
var protectedNamePatterns = []string{
	"peppiprd",
	"peppivn0",
}

// Target describes the identifiers of a resource a mutating verb is about
// to act on. VMID is the resolved VM ID, or 0 when unknown or not
// applicable to the verb. Names holds every string identifier associated
// with the target that could reveal a production peppi resource: pool
// name, vnet ID, storage ID, DNS zone, and VM name.
type Target struct {
	VMID  int
	Names []string
}

// Guard returns a non-nil error if t overlaps a production peppi resource,
// either by an exact VMID match or by a protected name substring found in
// any entry of t.Names (case-insensitive). Callers must invoke Guard before
// issuing any mutating request and abort on a non-nil error.
func Guard(t Target) error {
	if protectedVMIDs[t.VMID] {
		return fmt.Errorf("peppi guard: VMID %d is a production-protected peppi resource; refusing to proceed", t.VMID)
	}

	for _, name := range t.Names {
		lower := strings.ToLower(name)
		for _, pattern := range protectedNamePatterns {
			if strings.Contains(lower, pattern) {
				return fmt.Errorf("peppi guard: name %q matches protected pattern %q; refusing to proceed against a production-protected peppi resource", name, pattern)
			}
		}
	}

	return nil
}
