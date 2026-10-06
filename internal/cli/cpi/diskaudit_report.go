package cpi

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
)

// multiRefDocs points an operator at the CPI's guide to working out which
// guest really holds a volume more than one guest names.
const multiRefDocs = `see "Auditing parked disks with scripts/disk-audit" in docs/operations.md ` +
	"of bosh-proxmox-cpi-release"

// reportHeader is what the report says about the run itself.
type reportHeader struct {
	host string
	port int
	opts auditOptions
}

// classificationCounts counts the disks in each classification.
type classificationCounts struct {
	attached, parked, freeFloating, unknown int
}

func countClassifications(disks []*diskRecord) classificationCounts {
	var c classificationCounts
	for _, d := range disks {
		switch d.classification {
		case "attached":
			c.attached++
		case "parked":
			c.parked++
		case "free-floating":
			c.freeFloating++
		default:
			c.unknown++
		}
	}
	return c
}

// disksByClass returns the disks of one classification in volid order.
func disksByClass(disks []*diskRecord, class string) []*diskRecord {
	var out []*diskRecord
	for _, d := range disks {
		if d.classification == class {
			out = append(out, d)
		}
	}
	sortDisks(out)
	return out
}

func sortDisks(disks []*diskRecord) {
	sort.SliceStable(disks, func(i, j int) bool { return disks[i].volid < disks[j].volid })
}

// humanSize renders a byte count in binary units with one decimal, "—" for
// zero or less, and plain bytes below 1 KiB.
func humanSize(size int) string {
	if size <= 0 {
		return "—"
	}
	units := []struct {
		threshold int
		unit      string
	}{{1 << 40, "TiB"}, {1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}}
	for _, u := range units {
		if size >= u.threshold {
			return fmt.Sprintf("%.1f %s", float64(size)/float64(u.threshold), u.unit)
		}
	}
	return fmt.Sprintf("%d B", size)
}

// trunc shortens s to n characters, ending it with "…" when it was cut.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func vmidText(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprint(*v)
}

// referenceText renders one reference, as the report and the warning both
// print it.
func referenceText(ref multiRefEntry) string {
	var marks []string
	if ref.owns {
		marks = append(marks, "owns by name")
	}
	if ref.parker {
		marks = append(marks, "parker")
	}
	suffix := ""
	if len(marks) > 0 {
		suffix = " [" + strings.Join(marks, ", ") + "]"
	}
	node := ref.node
	if node == "" {
		node = "?"
	}
	return fmt.Sprintf("VM %d (%s) on %s %s%s", ref.vmid, orUnnamed(ref.name), node, ref.slot, suffix)
}

// ownerNote says who owns a volume by name when that matters for what not to
// do, or "" when the owner holds one of the references.
func ownerNote(mr multiRefRecord) string {
	switch {
	case mr.ownerVMID == nil:
		return "no VM owns this volume by name"
	case mr.ownerHolds:
		return ""
	case mr.ownerFound:
		return fmt.Sprintf("VM %d owns this volume by name and holds none of these references; "+
			"destroying it with destroy-unreferenced-disks deletes the volume", *mr.ownerVMID)
	default:
		return fmt.Sprintf("VM %d would own this volume by name, and no such VM is in the cluster index",
			*mr.ownerVMID)
	}
}

// coverageText says why the multiply-referenced report may be incomplete.
func coverageText(m multiRefReport) string {
	var parts []string
	switch m.visibility {
	case "limited":
		parts = append(parts, "limited (this token lacks VM.Audit on /vms)")
	case "unknown":
		parts = append(parts, "unknown (the permissions read failed)")
	}
	if len(m.unreadableVMIDs) > 0 {
		parts = append(parts, fmt.Sprintf("%d guest config(s) unreadable", len(m.unreadableVMIDs)))
	}
	return strings.Join(parts, "; ")
}

// writeHumanReport writes the human-readable report.
func writeHumanReport(w io.Writer, inv *inventory, h reportHeader) {
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	counts := countClassifications(inv.disks)

	p("\nBOSH Persistent Disk Audit")
	p("  Host:  %s:%d", h.host, h.port)
	p("  Disk band:   [%d, %d]", h.opts.diskStart, h.opts.diskEnd)
	p("  Parker band: [%d, %d]", h.opts.parkerStart, h.opts.parkerEnd)
	if h.opts.node != "" {
		p("  Scope: node=%s", h.opts.node)
	}
	if len(inv.skipped) > 0 {
		p("  Skipped storages (disks on a skipped storage are not in this report):")
		for _, s := range inv.skipped {
			p("    node %s storage %s: %s", s.node, s.storage, s.reason)
		}
	}
	p("  Total disk volumes: %d", len(inv.disks))
	p("    attached:      %d", counts.attached)
	p("    parked:        %d", counts.parked)
	p("    free-floating: %d", counts.freeFloating)
	p("    unknown:       %d", counts.unknown)
	p("  Volumes named by more than one guest: %d", len(inv.multiRef.records))
	if !inv.multiRef.complete() {
		p("    coverage: %s", coverageText(inv.multiRef))
	}
	p("")

	if attached := disksByClass(inv.disks, "attached"); len(attached) > 0 {
		p("ATTACHED DISKS")
		p("  %-45s %10s  %11s  %-30s", "VOLID", "SIZE", "HOLDER VMID", "HOLDER NAME")
		p("  %s %s  %s  %s", dashes(45), dashes(10), dashes(11), dashes(30))
		for _, r := range attached {
			p("  %-45s %10s  %11s  %-30s",
				trunc(r.volid, 45), humanSize(r.sizeBytes), vmidText(r.holderVMID), trunc(r.holderName, 30))
		}
		p("")
	}

	if parked := disksByClass(inv.disks, "parked"); len(parked) > 0 {
		p("PARKED DISKS")
		p("  %-45s %10s  %7s  %-32s  %-20s  %-20s  %-12s",
			"VOLID", "SIZE", "PARKER", "DISK_CID", "SRC_VM_CID", "PARKED_AT", "DIR_ID")
		p("  %s %s  %s  %s  %s  %s  %s",
			dashes(45), dashes(10), dashes(7), dashes(32), dashes(20), dashes(20), dashes(12))
		for _, r := range parked {
			p("  %-45s %10s  %7s  %-32s  %-20s  %-20s  %-12s",
				trunc(r.volid, 45), humanSize(r.sizeBytes), vmidText(r.holderVMID),
				trunc(r.diskCID, 32), trunc(r.sourceVMCID, 20), trunc(r.parkedAt, 20), trunc(r.directorID, 12))
		}
		p("")
	}

	if floating := disksByClass(inv.disks, "free-floating"); len(floating) > 0 {
		p("FREE-FLOATING DISKS  [WARNING: potential orphans]")
		p("  %-55s %10s  %-20s  %-15s", "VOLID", "SIZE", "STORAGE", "NODE")
		p("  %s %s  %s  %s", dashes(55), dashes(10), dashes(20), dashes(15))
		for _, r := range floating {
			p("  %-55s %10s  %-20s  %-15s", trunc(r.volid, 55), humanSize(r.sizeBytes), r.storage, r.node)
			if r.holderSlot != "" {
				p("    DO NOT DELETE: VM %s (%s) still names it on %s, so PVE references it",
					vmidText(r.holderVMID), orUnnamed(r.holderName), r.holderSlot)
			}
			if r.sentinelOnly {
				who := "a VM"
				if r.sentinelVMID != nil {
					who = fmt.Sprintf("VM %d", *r.sentinelVMID)
				}
				if r.sentinelName != "" {
					who += " (" + r.sentinelName + ")"
				}
				p("    found only by a sentinel note on %s; verify with `bosh disks --orphaned` before deleting", who)
			}
		}
		p("")
	}

	if len(inv.multiRef.records) > 0 {
		p("MULTIPLY REFERENCED VOLUMES  [WARNING: more than one guest names these]")
		for _, mr := range inv.multiRef.records {
			p("  %s", mr.volid)
			if note := ownerNote(mr); note != "" {
				p("    %s", note)
			}
			for _, ref := range mr.references {
				p("    %s", referenceText(ref))
			}
		}
		p("")
	}

	if unknowns := disksByClass(inv.disks, "unknown"); len(unknowns) > 0 {
		p("UNKNOWN VOLUMES  [could not determine holder]")
		for _, r := range unknowns {
			p("  %s", r.volid)
		}
		p("")
	}

	if len(inv.parkers) == 0 {
		p("PARKER VMs: none found")
		p("")
		return
	}
	p("PARKER VMs")
	p("  %7s  %-15s  %-30s  %-20s  %5s/%d  %6s  %5s",
		"VMID", "NODE", "NAME", "POOL", "DISKS", parkerSlotCapacity, "UNUSED", "EMPTY")
	p("  %s  %s  %s  %s  %s  %s  %s", dashes(7), dashes(15), dashes(30), dashes(20), dashes(8), dashes(6), dashes(5))
	for _, pr := range inv.parkers {
		flag := ""
		switch {
		case pr.empty():
			flag = "YES"
		case !pr.configRead:
			flag = "?"
		}
		p("  %7d  %-15s  %-30s  %-20s  %5d/%d  %6d  %5s",
			pr.vmid, pr.node, trunc(pr.name, 30), trunc(pr.pool, 20),
			pr.diskCount, parkerSlotCapacity, pr.unusedCount, flag)
	}
	p("")
}

func dashes(n int) string {
	return strings.Repeat("-", n)
}

func poolOrNone(pool string) string {
	if pool == "" {
		return "none"
	}
	return pool
}

// emitWarnings writes the advisory warnings to w.
func emitWarnings(w io.Writer, inv *inventory, opts auditOptions) {
	// Parked disks with the strategy set to free still drain: the parker
	// band resolves under every strategy, and each disk unparks on its next
	// attach_disk or delete_disk.
	if parked := countClassifications(inv.disks).parked; parked > 0 && opts.strategy != "parked" {
		warn(w, "%d parked disk(s) found but detached_disk_strategy='%s'. They stay recognized (the "+
			"parker band resolves under every strategy) and each unparks on its next attach_disk or "+
			"delete_disk; no new detaches will park.", parked, opts.strategy)
	}

	// A parker is a teardown candidate only when its config was read and
	// holds neither a bus disk nor an unusedN reference.
	for _, pr := range inv.parkers {
		if pr.empty() {
			warn(w, "parker VM %d (%s, pool %s) on node %s is empty (0 disks) — teardown candidate: "+
				"pmx pve qemu security protection disable %d && pmx pve qemu delete %d --purge --yes",
				pr.vmid, pr.name, poolOrNone(pr.pool), pr.node, pr.vmid, pr.vmid)
		}
	}

	// A stranded unusedN reference is a sweep that did not complete inside
	// the protection window, and it holds a live persistent disk.
	for _, pr := range inv.parkers {
		if pr.unusedCount > 0 {
			warn(w, "parker VM %d (%s, pool %s) on node %s carries %d unusedN reference(s) to a live "+
				"volume; do NOT destroy it. Clear each with: pmx pve qemu security protection disable %d && "+
				"pmx pve qemu disk unlink %d --disk <unusedN> && pmx pve qemu security protection enable %d",
				pr.vmid, pr.name, poolOrNone(pr.pool), pr.node, pr.unusedCount, pr.vmid, pr.vmid, pr.vmid)
		}
	}

	// A hard read ends the audit on a real failure, so a parker whose config
	// did not come back vanished between the cluster listing and the read.
	for _, pr := range inv.parkers {
		if !pr.configRead {
			warn(w, "parker VM %d (%s, pool %s) on node %s config was not returned (vanished during the "+
				"scan, HTTP 404/501); its contents are unknown and it is not reported as empty",
				pr.vmid, pr.name, poolOrNone(pr.pool), pr.node)
		}
	}

	// The CPI never creates a VM in a parker pool and never moves one out,
	// so this warning stays until an operator moves the VM by hand.
	for _, ir := range inv.intruders {
		warn(w, "VM %d (%s, pool %s) on node %s sits in a parker pool and carries no bosh-parker tag. "+
			"The CPI never moves it out, so move it into the workload pool it belongs in with: "+
			"pvesh set /pools/<workload-pool> --vms %d --allow-move 1",
			ir.vmid, orUnnamed(ir.name), ir.pool, ir.node, ir.vmid)
	}

	// No fix command for a volume more than one guest names: the right
	// repair depends on how the volume got there.
	for _, mr := range inv.multiRef.records {
		refs := make([]string, 0, len(mr.references))
		for _, ref := range mr.references {
			refs = append(refs, referenceText(ref))
		}
		hazard := ownerNote(mr)
		if mr.ownerVMID != nil && mr.ownerHolds {
			hazard = fmt.Sprintf("destroying VM %d, or removing its unused entry, deletes the volume "+
				"while another guest still names it", *mr.ownerVMID)
		}
		warn(w, "volume %s is named by %d guests: %s. Leave every reference in place until we know "+
			"which guest really holds the disk; %s; %s",
			mr.volid, distinctVMIDs(mr.references), strings.Join(refs, "; "), hazard, multiRefDocs)
	}

	if ids := inv.multiRef.unreadableVMIDs; len(ids) > 0 {
		texts := make([]string, len(ids))
		for i, id := range ids {
			texts[i] = fmt.Sprint(id)
		}
		warn(w, "the configs of %d QEMU guest(s) did not come back (%s), so the report of volumes named "+
			"by more than one guest may be incomplete", len(ids), strings.Join(texts, ", "))
	}
	switch inv.multiRef.visibility {
	case "limited":
		warn(w, "this token lacks VM.Audit on /vms, so the report of volumes named by more than one "+
			"guest covers only the guests this token can see; a second guest outside its view would not show up")
	case "unknown":
		warn(w, "could not read this token's permissions on /vms (%s), so the coverage of the report of "+
			"volumes named by more than one guest is unknown", inv.multiRef.visibilityError)
	}
}

// findingsError returns the error that makes the run exit with
// exitcode.AuditFindings when free-floating disks were found, or nil.
func findingsError(inv *inventory) error {
	floating := disksByClass(inv.disks, "free-floating")
	if len(floating) == 0 {
		return nil
	}
	note := ""
	var held []string
	for _, r := range floating {
		if r.holderSlot != "" {
			held = append(held, fmt.Sprintf("%s still named by VM %s", r.volid, vmidText(r.holderVMID)))
		}
	}
	if len(held) > 0 {
		note = ", " + strings.Join(held, ", ")
	}
	return &exitcode.AuditFindingsError{
		Message: fmt.Sprintf("EXIT %d: %d free-floating disk(s) found%s.", exitcode.AuditFindings, len(floating), note),
	}
}

// The types below are the JSON and YAML document. Field order is the order
// the keys are written in.

type auditDocument struct {
	Host                              string         `json:"host"`
	Port                              int            `json:"port"`
	DiskBand                          [2]int         `json:"disk_band"`
	ParkerBand                        [2]int         `json:"parker_band"`
	Summary                           auditSummary   `json:"summary"`
	Disks                             []diskJSON     `json:"disks"`
	Parkers                           []parkerJSON   `json:"parkers"`
	SkippedStorages                   []skippedJSON  `json:"skipped_storages"`
	MultiplyReferenced                []multiRefJSON `json:"multiply_referenced"`
	MultiplyReferencedUnreadableVMIDs []int          `json:"multiply_referenced_unreadable_vmids"`
	MultiplyReferencedVisibility      string         `json:"multiply_referenced_visibility"`
	MultiplyReferencedComplete        bool           `json:"multiply_referenced_complete"`
}

type auditSummary struct {
	Total              int `json:"total"`
	Attached           int `json:"attached"`
	Parked             int `json:"parked"`
	FreeFloating       int `json:"free_floating"`
	Unknown            int `json:"unknown"`
	MultiplyReferenced int `json:"multiply_referenced"`
}

type diskJSON struct {
	Volid               string   `json:"volid"`
	Storage             string   `json:"storage"`
	Node                string   `json:"node"`
	SizeBytes           int      `json:"size_bytes"`
	Classification      string   `json:"classification"`
	HolderVMID          *int     `json:"holder_vmid,omitempty"`
	HolderNode          string   `json:"holder_node,omitempty"`
	HolderName          string   `json:"holder_name,omitempty"`
	DiskCID             string   `json:"disk_cid,omitempty"`
	SourceVMCID         string   `json:"source_vm_cid,omitempty"`
	ParkedAt            string   `json:"parked_at,omitempty"`
	ParkedNode          string   `json:"parked_node,omitempty"`
	DirectorID          string   `json:"director_id,omitempty"`
	HolderSlot          string   `json:"holder_slot,omitempty"`
	HeldByUnusedEntry   bool     `json:"held_by_unused_entry,omitempty"`
	DiscoveredBy        []string `json:"discovered_by"`
	FoundOnlyBySentinel bool     `json:"found_only_by_sentinel,omitempty"`
	SentinelVMID        *int     `json:"sentinel_vmid,omitempty"`
	SentinelName        string   `json:"sentinel_name,omitempty"`
}

type parkerJSON struct {
	VMID         int    `json:"vmid"`
	Node         string `json:"node"`
	Name         string `json:"name"`
	Pool         string `json:"pool"`
	DiskCount    int    `json:"disk_count"`
	UnusedCount  int    `json:"unused_count"`
	ConfigRead   bool   `json:"config_read"`
	SlotCapacity int    `json:"slot_capacity"`
	Empty        bool   `json:"empty"`
}

type skippedJSON struct {
	Node    string `json:"node"`
	Storage string `json:"storage"`
	Reason  string `json:"reason"`
}

type multiRefJSON struct {
	Volid               string              `json:"volid"`
	OwnerVMID           *int                `json:"owner_vmid"`
	OwnerPresent        bool                `json:"owner_present"`
	OwnerHoldsReference bool                `json:"owner_holds_reference"`
	References          []multiRefEntryJSON `json:"references"`
}

type multiRefEntryJSON struct {
	VMID   int    `json:"vmid"`
	Node   string `json:"node"`
	Name   string `json:"name"`
	Slot   string `json:"slot"`
	Kind   string `json:"kind"`
	Parker bool   `json:"parker"`
	Owns   bool   `json:"owns"`
}

func diskToJSON(r *diskRecord) diskJSON {
	d := diskJSON{
		Volid: r.volid, Storage: r.storage, Node: r.node, SizeBytes: r.sizeBytes,
		Classification: r.classification, HolderVMID: r.holderVMID, HolderNode: r.holderNode,
		HolderName: r.holderName, DiskCID: r.diskCID, SourceVMCID: r.sourceVMCID, ParkedAt: r.parkedAt,
		ParkedNode: r.parkedNode, DirectorID: r.directorID, HolderSlot: r.holderSlot,
		HeldByUnusedEntry: r.holderSlot != "",
		DiscoveredBy:      append([]string{}, r.discoveredBy...),
	}
	if r.sentinelOnly {
		d.FoundOnlyBySentinel = true
		d.SentinelVMID = r.sentinelVMID
		d.SentinelName = r.sentinelName
	}
	return d
}

// buildDocument builds the JSON and YAML document.
func buildDocument(inv *inventory, h reportHeader) auditDocument {
	counts := countClassifications(inv.disks)
	doc := auditDocument{
		Host:       h.host,
		Port:       h.port,
		DiskBand:   [2]int{h.opts.diskStart, h.opts.diskEnd},
		ParkerBand: [2]int{h.opts.parkerStart, h.opts.parkerEnd},
		Summary: auditSummary{
			Total: len(inv.disks), Attached: counts.attached, Parked: counts.parked,
			FreeFloating: counts.freeFloating, Unknown: counts.unknown,
			MultiplyReferenced: len(inv.multiRef.records),
		},
		Disks:                             []diskJSON{},
		Parkers:                           []parkerJSON{},
		SkippedStorages:                   []skippedJSON{},
		MultiplyReferenced:                []multiRefJSON{},
		MultiplyReferencedUnreadableVMIDs: append([]int{}, inv.multiRef.unreadableVMIDs...),
		MultiplyReferencedVisibility:      inv.multiRef.visibility,
		MultiplyReferencedComplete:        inv.multiRef.complete(),
	}
	disks := append([]*diskRecord(nil), inv.disks...)
	sortDisks(disks)
	for _, r := range disks {
		doc.Disks = append(doc.Disks, diskToJSON(r))
	}
	for _, pr := range inv.parkers {
		doc.Parkers = append(doc.Parkers, parkerJSON{
			VMID: pr.vmid, Node: pr.node, Name: pr.name, Pool: pr.pool, DiskCount: pr.diskCount,
			UnusedCount: pr.unusedCount, ConfigRead: pr.configRead, SlotCapacity: parkerSlotCapacity,
			Empty: pr.empty(),
		})
	}
	for _, s := range inv.skipped {
		doc.SkippedStorages = append(doc.SkippedStorages, skippedJSON{Node: s.node, Storage: s.storage, Reason: s.reason})
	}
	for _, mr := range inv.multiRef.records {
		refs := make([]multiRefEntryJSON, 0, len(mr.references))
		for _, ref := range mr.references {
			refs = append(refs, multiRefEntryJSON{
				VMID: ref.vmid, Node: ref.node, Name: ref.name, Slot: ref.slot, Kind: ref.kind,
				Parker: ref.parker, Owns: ref.owns,
			})
		}
		doc.MultiplyReferenced = append(doc.MultiplyReferenced, multiRefJSON{
			Volid: mr.volid, OwnerVMID: mr.ownerVMID, OwnerPresent: mr.ownerFound,
			OwnerHoldsReference: mr.ownerHolds, References: refs,
		})
	}
	return doc
}
