package cpi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// inventoryClient is every read the disk audit makes. Each method is a GET;
// nothing here can change the cluster.
type inventoryClient interface {
	// clusterResourcesVMs returns the object rows of
	// GET /cluster/resources?type=vm.
	clusterResourcesVMs(ctx context.Context) ([]map[string]any, error)
	// listNodes returns the names of the online nodes in GET /nodes.
	listNodes(ctx context.Context) ([]string, error)
	// nodeStorages returns the object rows of GET /nodes/{node}/storage, or
	// none when the node answers 404 or 501.
	nodeStorages(ctx context.Context, node string) ([]map[string]any, error)
	// storageContent returns the object rows of
	// GET /nodes/{node}/storage/{storage}/content?content=images, or none
	// when the storage answers 404 or 501.
	storageContent(ctx context.Context, node, storage string) ([]map[string]any, error)
	// vmViews reads a qemu guest's pending endpoint and fails on any error
	// other than 404 or 501. It returns nil views and no error when the
	// guest is missing, and an error when the reply is not a list of rows.
	vmViews(ctx context.Context, node string, vmid int) (*pendingViews, error)
	// vmViewsSoft reads the same endpoint and returns nil on any failure.
	vmViewsSoft(ctx context.Context, node string, vmid int) *pendingViews
	// vmsPermissions returns this principal's privileges on /vms, which may
	// be empty, or nil and the reason when the read failed.
	vmsPermissions(ctx context.Context) (map[string]any, string)
}

// rawGetter is the one method of the SDK's raw client the audit needs. The
// reader holds nothing else, so it cannot issue anything but a GET.
type rawGetter interface {
	GetCtx(ctx context.Context, path string, params map[string]any) (any, error)
}

// pveReader is the inventoryClient backed by the live API.
type pveReader struct {
	raw rawGetter
}

// requestText is how a request reads in an error: the path plus its query.
func requestText(path string, params map[string]any) string {
	if len(params) == 0 {
		return path
	}
	q := url.Values{}
	for k, v := range params {
		q.Set(k, fmt.Sprint(v))
	}
	return path + "?" + q.Encode()
}

// httpStatus returns the HTTP status and message of an API error, or false
// when err is not one (a transport failure, for instance).
func httpStatus(err error) (int, string, bool) {
	var base *pveerrors.APIError
	var perm *pveerrors.PermissionError
	var param *pveerrors.ParameterError
	var auth *pveerrors.AuthenticationError
	switch {
	case errors.As(err, &perm):
		base = &perm.APIError
	case errors.As(err, &param):
		base = &param.APIError
	case errors.As(err, &auth):
		base = &auth.APIError
	case errors.As(err, &base):
	default:
		return 0, "", false
	}
	if base.HTTPCode == 0 {
		return 0, "", false
	}
	msg := base.Message
	if msg == "" {
		msg = http.StatusText(base.HTTPCode)
	}
	return base.HTTPCode, msg, true
}

// isMissing reports whether err is the 404 or 501 a vanished or unsupported
// resource answers with.
func isMissing(err error) bool {
	code, _, ok := httpStatus(err)
	return ok && (code == http.StatusNotFound || code == http.StatusNotImplemented)
}

// softReason describes a failed read for the report.
func softReason(err error) string {
	if code, msg, ok := httpStatus(err); ok {
		return fmt.Sprintf("HTTP %d %s", code, msg)
	}
	return err.Error()
}

// get issues a GET and wraps any failure with the request, keeping the
// error's type so the exit code still follows it.
func (r *pveReader) get(ctx context.Context, path string, params map[string]any) (any, error) {
	data, err := r.raw.GetCtx(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", requestText(path, params), err)
	}
	return data, nil
}

// getAllowMissing is get, except that a 404 or 501 reads as no data.
func (r *pveReader) getAllowMissing(ctx context.Context, path string, params map[string]any) (any, error) {
	data, err := r.get(ctx, path, params)
	if err != nil {
		if isMissing(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// objectRows keeps the object rows of a list reply and drops everything
// else. A reply that is not a list has no rows.
func objectRows(data any) []map[string]any {
	list, ok := data.([]any)
	if !ok {
		return nil
	}
	rows := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	return rows
}

func (r *pveReader) clusterResourcesVMs(ctx context.Context) ([]map[string]any, error) {
	data, err := r.get(ctx, "/cluster/resources", map[string]any{"type": "vm"})
	if err != nil {
		return nil, err
	}
	return objectRows(data), nil
}

func (r *pveReader) listNodes(ctx context.Context) ([]string, error) {
	data, err := r.get(ctx, "/nodes", nil)
	if err != nil {
		return nil, err
	}
	var nodes []string
	for _, row := range objectRows(data) {
		name := strings.TrimSpace(fieldStr(row, "node"))
		status := strings.TrimSpace(fieldStr(row, "status"))
		if name != "" && status == "online" {
			nodes = append(nodes, name)
		}
	}
	return nodes, nil
}

func (r *pveReader) nodeStorages(ctx context.Context, node string) ([]map[string]any, error) {
	data, err := r.getAllowMissing(ctx, "/nodes/"+url.PathEscape(node)+"/storage", nil)
	if err != nil {
		return nil, err
	}
	return objectRows(data), nil
}

func (r *pveReader) storageContent(ctx context.Context, node, storage string) ([]map[string]any, error) {
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content"
	data, err := r.getAllowMissing(ctx, path, map[string]any{"content": "images"})
	if err != nil {
		return nil, err
	}
	return objectRows(data), nil
}

func pendingPath(node string, vmid int) string {
	return fmt.Sprintf("/nodes/%s/qemu/%d/pending", url.PathEscape(node), vmid)
}

// jsonTypeName names the JSON type of a decoded value for an error message.
func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func (r *pveReader) vmViews(ctx context.Context, node string, vmid int) (*pendingViews, error) {
	path := pendingPath(node, vmid)
	data, err := r.getAllowMissing(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	rows, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("GET %s: expected a list of config rows, got %s", path, jsonTypeName(data))
	}
	for _, r := range rows {
		row, ok := r.(map[string]any)
		key, isStr := row["key"].(string)
		if !ok || !isStr || key == "" {
			b, _ := json.Marshal(r)
			return nil, fmt.Errorf("GET %s: malformed config row %s", path, b)
		}
	}
	return parsePendingViews(data), nil
}

func (r *pveReader) vmViewsSoft(ctx context.Context, node string, vmid int) *pendingViews {
	data, err := r.raw.GetCtx(ctx, pendingPath(node, vmid), nil)
	if err != nil {
		return nil
	}
	return parsePendingViews(data)
}

func (r *pveReader) vmsPermissions(ctx context.Context) (map[string]any, string) {
	data, err := r.raw.GetCtx(ctx, "/access/permissions", map[string]any{"path": "/vms"})
	if err != nil {
		return nil, softReason(err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		return map[string]any{}, ""
	}
	privs, ok := m["/vms"].(map[string]any)
	if !ok {
		return map[string]any{}, ""
	}
	return privs, ""
}

// auditOptions is the resolved configuration of one audit run.
type auditOptions struct {
	diskStart, diskEnd     int
	parkerStart, parkerEnd int
	// node limits the storage scan to one node; "" scans every online node.
	node string
	// strategy is the CPI's detached_disk_strategy, "parked" or "free".
	strategy string
	// parkerPools are the pools named on --parker-pool.
	parkerPools []string
}

func (o auditOptions) inParkerBand(vmid int) bool {
	return o.parkerStart <= vmid && vmid <= o.parkerEnd
}

func (o auditOptions) inDiskBand(volid string) bool {
	n, ok := diskVMIDFromVolid(volid)
	return ok && o.diskStart <= n && n <= o.diskEnd
}

// vmInfo is one guest row of the cluster index.
type vmInfo struct {
	vmid int
	node string
	name string
	tags string
	pool string
	typ  string
}

func (v *vmInfo) isQemu() bool {
	return v.typ == "" || v.typ == "qemu"
}

// vmIndex is the cluster index keyed by VMID, in the order the rows came.
// A VMID listed twice keeps its first position and its last row.
type vmIndex struct {
	order []int
	byID  map[int]*vmInfo
}

func newVMIndex() *vmIndex {
	return &vmIndex{byID: map[int]*vmInfo{}}
}

func (x *vmIndex) put(info vmInfo) {
	if _, seen := x.byID[info.vmid]; !seen {
		x.order = append(x.order, info.vmid)
	}
	x.byID[info.vmid] = &info
}

func (x *vmIndex) has(vmid int) bool {
	_, ok := x.byID[vmid]
	return ok
}

// each calls fn for every guest in index order.
func (x *vmIndex) each(fn func(*vmInfo)) {
	for _, id := range x.order {
		fn(x.byID[id])
	}
}

func (x *vmIndex) sortedIDs() []int {
	ids := append([]int(nil), x.order...)
	sort.Ints(ids)
	return ids
}

// diskRecord is the classification of one CPI-managed volume.
type diskRecord struct {
	volid          string
	storage        string
	node           string
	sizeBytes      int
	classification string
	holderVMID     *int
	holderNode     string
	holderName     string
	// The provenance fields come from a parker's sentinel, for parked disks.
	diskCID     string
	sourceVMCID string
	parkedAt    string
	parkedNode  string
	directorID  string
	// discoveredBy says how the audit found the volume: "band", "serial",
	// "parker", and "sentinel", in that order.
	discoveredBy []string
	// A free-floating volume that only a sentinel note named carries the VM
	// whose sentinel pointed at it.
	sentinelOnly bool
	sentinelVMID *int
	sentinelName string
	// holderSlot is the unusedN entry of a guest that still names a
	// free-floating volume.
	holderSlot string
}

// parkerRecord is one parker VM.
type parkerRecord struct {
	vmid int
	node string
	name string
	// pool is the resource pool the cluster index reports, or "".
	pool      string
	diskCount int
	// unusedCount counts unusedN references. `qm destroy --purge` frees the
	// volume behind each one, so a parker holding one is not empty.
	unusedCount int
	// configRead is false when the config did not come back. An unread
	// config proves nothing about what the parker holds.
	configRead bool
}

func (p parkerRecord) empty() bool {
	return p.configRead && p.diskCount == 0 && p.unusedCount == 0
}

// poolIntruder is a VM without the parker tag that sits in a parker pool.
type poolIntruder struct {
	vmid int
	node string
	name string
	pool string
}

// multiRefEntry is one guest's reference to a volume more than one guest
// names.
type multiRefEntry struct {
	vmid   int
	node   string
	name   string
	slot   string
	kind   string
	parker bool
	owns   bool
}

// multiRefRecord is one volume more than one guest config names.
type multiRefRecord struct {
	volid      string
	ownerVMID  *int
	ownerFound bool // a guest with the owning VMID is in the cluster index
	ownerHolds bool // the owning guest holds one of the references
	references []multiRefEntry
}

// multiRefReport is the multiply-referenced findings and how much of the
// cluster they cover. visibility is "full" when the principal can audit
// every guest, "limited" when it lacks VM.Audit on /vms, and "unknown" when
// its permissions could not be read.
type multiRefReport struct {
	records         []multiRefRecord
	unreadableVMIDs []int
	visibility      string
	visibilityError string
}

func (m multiRefReport) complete() bool {
	return m.visibility == "full" && len(m.unreadableVMIDs) == 0
}

// skippedStorage is a storage the scan did not read.
type skippedStorage struct {
	node    string
	storage string
	reason  string
}

// inventory is everything one audit run found.
type inventory struct {
	disks     []*diskRecord
	parkers   []parkerRecord
	intruders []poolIntruder
	multiRef  multiRefReport
	skipped   []skippedStorage
}

// parkerPoolNames decides which pools count as parker pools: every pool a
// parker belongs to, plus every pool named on --parker-pool, which covers a
// parker pool that stands empty.
func parkerPoolNames(parkers []parkerRecord, extra []string) map[string]bool {
	names := map[string]bool{}
	for _, p := range parkers {
		if p.pool != "" {
			names[p.pool] = true
		}
	}
	for _, pool := range extra {
		if pool != "" {
			names[pool] = true
		}
	}
	return names
}

// findPoolIntruders returns every VM in a parker pool that carries no
// bosh-parker tag, ordered by VMID. It keys on the tag alone, because an
// untagged VM inside the parker band is still something to look at.
func findPoolIntruders(vms *vmIndex, parkerPools map[string]bool) []poolIntruder {
	var out []poolIntruder
	if len(parkerPools) == 0 {
		return out
	}
	for _, id := range vms.sortedIDs() {
		info := vms.byID[id]
		if !parkerPools[info.pool] || tagsContainParker(info.tags) {
			continue
		}
		out = append(out, poolIntruder{vmid: id, node: info.node, name: info.name, pool: info.pool})
	}
	return out
}

// findMultiplyReferenced finds every volume that two or more guests name on
// an active slot or an unused entry. Only QEMU guests count, and a row
// without a type counts as one. A container is skipped before its config is
// looked at. One guest naming a volume twice is not a finding, because PVE
// refuses to free a volume its own VM still names. It returns the records in
// volid order and the VMIDs of QEMU guests whose config is nil.
func findMultiplyReferenced(
	vms *vmIndex, configs map[int]map[string]string, opts auditOptions,
) ([]multiRefRecord, []int) {
	byVolid := map[string][]multiRefEntry{}
	unreadable := []int{}
	for _, id := range vms.sortedIDs() {
		info := vms.byID[id]
		if !info.isQemu() {
			continue
		}
		config := configs[id]
		if config == nil {
			unreadable = append(unreadable, id)
			continue
		}
		parker := opts.inParkerBand(id) && tagsContainParker(info.tags)
		for _, ref := range referencedVolids(config) {
			byVolid[ref.volid] = append(byVolid[ref.volid], multiRefEntry{
				vmid: id, node: info.node, name: info.name, slot: ref.slot, kind: ref.kind, parker: parker,
			})
		}
	}

	volids := make([]string, 0, len(byVolid))
	for v := range byVolid {
		volids = append(volids, v)
	}
	sort.Strings(volids)

	records := []multiRefRecord{}
	for _, volid := range volids {
		refs := byVolid[volid]
		if distinctVMIDs(refs) < 2 {
			continue
		}
		rec := multiRefRecord{volid: volid, references: refs}
		if owner, ok := volumeOwnerVMID(volid); ok {
			rec.ownerVMID = &owner
			rec.ownerFound = vms.has(owner)
			for i := range refs {
				refs[i].owns = refs[i].vmid == owner
				rec.ownerHolds = rec.ownerHolds || refs[i].owns
			}
		}
		records = append(records, rec)
	}
	return records, unreadable
}

func distinctVMIDs(refs []multiRefEntry) int {
	seen := map[int]bool{}
	for _, r := range refs {
		seen[r.vmid] = true
	}
	return len(seen)
}

// referenceVisibility says how much of the cluster the principal can see.
// A token scoped to a pool sees only the guests it can audit, so a second
// holder outside its view would make the report read clean.
func referenceVisibility(ctx context.Context, client inventoryClient) (string, string) {
	privs, reason := client.vmsPermissions(ctx)
	if privs == nil {
		if reason == "" {
			reason = "permissions read failed"
		}
		return "unknown", reason
	}
	if pyTruthy(privs["VM.Audit"]) {
		return "full", ""
	}
	return "limited", ""
}

// warn writes one advisory line to w.
func warn(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, "WARN: "+format+"\n", args...)
}

// volumeEntry is one image volume the storage scan found.
type volumeEntry struct {
	volid     string
	storage   string
	node      string // the first node the volume was seen on
	sizeBytes int
}

// guestCache holds the guest configs read so far. A VMID present with a nil
// config is a guest whose config did not come back.
type guestCache struct {
	configs        map[int]map[string]string
	bus            map[int][]string
	currentSerials map[int]map[string]string
}

func newGuestCache() *guestCache {
	return &guestCache{
		configs:        map[int]map[string]string{},
		bus:            map[int][]string{},
		currentSerials: map[int]map[string]string{},
	}
}

// store records a guest's views, or that its config did not come back.
func (c *guestCache) store(vmid int, views *pendingViews) {
	if views == nil {
		c.configs[vmid] = nil
		delete(c.bus, vmid)
		return
	}
	c.configs[vmid] = mergeViews(views)
	c.bus[vmid] = viewsBusValues(views)
	serials := map[string]string{}
	for _, raw := range busValues(views.current) {
		if token := stableIDFromDriveOptStr(raw); token != "" {
			if _, seen := serials[token]; !seen {
				serials[token] = busDiskVolidFromSlot(raw)
			}
		}
	}
	c.currentSerials[vmid] = serials
}

// readHard reads a guest with the read that ends the audit on a real
// failure, and caches what came back.
func (c *guestCache) readHard(ctx context.Context, client inventoryClient, vmid int, node string) error {
	views, err := client.vmViews(ctx, node, vmid)
	if err != nil {
		return err
	}
	c.store(vmid, views)
	return nil
}

// softReadAll reads every listed guest softly, in parallel, and returns the
// views in the same order.
func softReadAll(ctx context.Context, client inventoryClient, guests []*vmInfo) []*pendingViews {
	out := make([]*pendingViews, len(guests))
	cli.ForEachIndex(ctx, len(guests), cli.DefaultFanout, func(ctx context.Context, i int) {
		out[i] = client.vmViewsSoft(ctx, guests[i].node, guests[i].vmid)
	})
	return out
}

// collectInventory gathers and classifies every CPI-managed volume, the
// parker VMs, the workload VMs in parker pools, and the volumes more than
// one guest names. Lines for skipped storages and warnings found while
// collecting go to warnOut. It returns an error, and no inventory, when a
// read the classification depends on fails.
//
// The steps are:
//
//  1. Index the cluster's guests from /cluster/resources.
//  2. Pick the nodes to scan: the --node one, or every online node.
//  3. List each node's image storages, skip any marked enabled=0 or
//     active=0, and collect the image volumes of the rest. Shared storage
//     shows a volume on every node, so each volid is kept once.
//  4. Read every QEMU guest's config in both views. A soft read that fails
//     falls back to a hard read, so an unreadable guest never turns a held
//     volume into a false orphan. The configs say which volumes are the
//     CPI's: a VMID in the disk band, a bpd- serial on a drive line, a full
//     volid a sentinel names, or a bus slot of a parker. The CPI renames a
//     stable-ID disk to the VMID of whichever guest or parker holds it, so
//     the band alone misses renamed disks.
//  5. Classify each volume by the guest whose bus slot holds it.
//  6. Inventory the parker VMs, empty ones included.
//  7. Find the workload VMs in a parker pool.
//  8. Find the volumes more than one guest names, cluster-wide.
func collectInventory(
	ctx context.Context, client inventoryClient, opts auditOptions, warnOut io.Writer,
) (*inventory, error) {
	// Step 1: the cluster index.
	rows, err := client.clusterResourcesVMs(ctx)
	if err != nil {
		return nil, err
	}
	vms := newVMIndex()
	for _, row := range rows {
		raw, ok := row["vmid"]
		if !ok || raw == nil {
			continue
		}
		vmid, ok := pyInt(raw)
		if !ok {
			continue
		}
		vms.put(vmInfo{
			vmid: vmid,
			node: strOr(row["node"]),
			name: strOr(row["name"]),
			tags: strOr(row["tags"]),
			pool: strOr(row["pool"]),
			typ:  strOr(row["type"]),
		})
	}

	// Step 2: the nodes to scan.
	var nodes []string
	if opts.node != "" {
		nodes = []string{opts.node}
	} else {
		nodes, err = client.listNodes(ctx)
		if err != nil {
			return nil, err
		}
		if len(nodes) == 0 {
			warn(warnOut, "no online nodes found; inventory may be empty")
		}
	}

	// Step 3: the image volumes on every readable storage.
	var volumes []volumeEntry
	seenVolid := map[string]bool{}
	skipped := []skippedStorage{}
	for _, node := range nodes {
		storages, err := client.nodeStorages(ctx, node)
		if err != nil {
			return nil, err
		}
		for _, st := range storages {
			name := strings.TrimSpace(strOr(st["storage"]))
			if name == "" || !hasImagesContent(strOr(st["content"])) {
				continue
			}
			// PVE answers a content read on a disabled storage with HTTP
			// 500. The listing already says the storage is off, so it is
			// skipped here and the skip is carried into the report.
			if reason := storageSkipReason(st); reason != "" {
				skipped = append(skipped, skippedStorage{node: node, storage: name, reason: reason})
				_, _ = fmt.Fprintf(warnOut, "SKIPPED: node %s storage %s: %s\n", node, name, reason)
				continue
			}
			items, err := client.storageContent(ctx, node, name)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				volid := strings.TrimSpace(strOr(item["volid"]))
				if volid == "" || seenVolid[volid] {
					continue
				}
				size := 0
				if sizeRaw := item["size"]; pyTruthy(sizeRaw) {
					if n, ok := pyInt(sizeRaw); ok {
						size = n
					}
				}
				seenVolid[volid] = true
				volumes = append(volumes, volumeEntry{volid: volid, storage: name, node: node, sizeBytes: size})
			}
		}
	}

	// Step 4: read the guest configs and find the CPI's volumes in them.
	cache := newGuestCache()
	serialVolids := map[string]bool{}
	serialHolders := map[string]map[string]map[holderKey]bool{}
	sentinelRefs := map[string]holderKey{}
	parkerVolids := map[string]bool{}
	if len(volumes) > 0 {
		if err := scanGuests(ctx, client, vms, opts, cache, scanSinks{
			serialVolids: serialVolids, serialHolders: serialHolders,
			sentinelRefs: sentinelRefs, parkerVolids: parkerVolids,
		}, warnOut); err != nil {
			return nil, err
		}
	}

	type candidate struct {
		vol     volumeEntry
		signals []string
	}
	var candidates []candidate
	targets := map[string]bool{}
	for _, vol := range volumes {
		var signals []string
		if opts.inDiskBand(vol.volid) {
			signals = append(signals, "band")
		}
		if serialVolids[vol.volid] {
			signals = append(signals, "serial")
		}
		if parkerVolids[vol.volid] {
			signals = append(signals, "parker")
		}
		if _, ok := sentinelRefs[vol.volid]; ok {
			signals = append(signals, "sentinel")
		}
		if len(signals) > 0 {
			candidates = append(candidates, candidate{vol: vol, signals: signals})
			targets[vol.volid] = true
		}
	}

	// The holder of a volume is the first guest, in index order, with the
	// volume on a bus slot. It is found by scanning configs, never by the
	// VMID in the volume's name.
	holders := map[string]*vmInfo{}
	vms.each(func(info *vmInfo) {
		for _, raw := range cache.bus[info.vmid] {
			volid := busDiskVolidFromSlot(raw)
			if targets[volid] && holders[volid] == nil {
				holders[volid] = info
			}
		}
	})

	// The first guest that names a volume on an unusedN entry. A volume held
	// only this way is free-floating, and PVE still references it.
	type unusedHolder struct {
		info *vmInfo
		slot string
	}
	unusedHolders := map[string]unusedHolder{}
	vms.each(func(info *vmInfo) {
		config := cache.configs[info.vmid]
		if config == nil {
			return
		}
		for _, ref := range referencedVolids(config) {
			if _, seen := unusedHolders[ref.volid]; ref.kind == "unused" && targets[ref.volid] && !seen {
				unusedHolders[ref.volid] = unusedHolder{info: info, slot: ref.slot}
			}
		}
	})

	// Step 5: classify each volume.
	provenance := map[int]*orderedObject{}
	disks := make([]*diskRecord, 0, len(candidates))
	for _, c := range candidates {
		rec := &diskRecord{
			volid: c.vol.volid, storage: c.vol.storage, node: c.vol.node,
			sizeBytes: c.vol.sizeBytes, discoveredBy: c.signals,
		}
		holder := holders[c.vol.volid]
		if holder == nil {
			rec.classification = "free-floating"
			if u, ok := unusedHolders[c.vol.volid]; ok {
				vmid := u.info.vmid
				rec.holderVMID = &vmid
				rec.holderNode, rec.holderName, rec.holderSlot = u.info.node, u.info.name, u.slot
			}
			if len(c.signals) == 1 && c.signals[0] == "sentinel" {
				// Only a sentinel note named this volume, and sentinels go
				// stale, so it is not a plain orphan.
				ref := sentinelRefs[c.vol.volid]
				vmid := ref.vmid
				rec.sentinelOnly = true
				rec.sentinelVMID = &vmid
				rec.sentinelName = ref.name
			}
			disks = append(disks, rec)
			continue
		}

		vmid := holder.vmid
		rec.holderVMID = &vmid
		rec.holderNode, rec.holderName = holder.node, holder.name
		if opts.inParkerBand(vmid) && tagsContainParker(holder.tags) {
			rec.classification = "parked"
			provMap, ok := provenance[vmid]
			if !ok {
				provMap = parseParkerSentinel(cache.configs[vmid]["description"])
				provenance[vmid] = provMap
			}
			applyProvenance(rec, findProvenance(provMap, c.vol.volid))
		} else {
			rec.classification = "attached"
		}
		disks = append(disks, rec)
	}

	// Step 6: the parker VMs, empty ones included. A parker whose config
	// step 4 could not read, or never read, gets a hard read here, so an
	// unreadable parker ends the audit rather than reading as empty. Tags
	// come from the cluster index, and a parker without them there is not
	// counted.
	var parkers []parkerRecord
	for _, id := range vms.order {
		info := vms.byID[id]
		if !opts.inParkerBand(id) || !tagsContainParker(info.tags) {
			continue
		}
		config := cache.configs[id]
		if config == nil {
			if err := cache.readHard(ctx, client, id, info.node); err != nil {
				return nil, err
			}
			config = cache.configs[id]
		}
		rec := parkerRecord{vmid: id, node: info.node, name: info.name, pool: info.pool, configRead: config != nil}
		if config != nil {
			rec.diskCount = len(parseBusDisks(config))
			rec.unusedCount = countUnusedRefs(config)
		}
		parkers = append(parkers, rec)
	}
	sort.SliceStable(parkers, func(i, j int) bool { return parkers[i].vmid < parkers[j].vmid })

	// Step 7: workload VMs sitting in a parker pool.
	intruders := findPoolIntruders(vms, parkerPoolNames(parkers, opts.parkerPools))

	// Step 8: volumes more than one guest names. Every QEMU guest is read,
	// whatever --node says, because the second reference can sit on any
	// node. These reads are soft: a guest whose config does not come back
	// is listed as unreadable rather than ending the audit.
	var unread []*vmInfo
	vms.each(func(info *vmInfo) {
		if !info.isQemu() {
			return
		}
		if _, cached := cache.configs[info.vmid]; cached {
			return
		}
		if info.node == "" {
			cache.configs[info.vmid] = nil
			return
		}
		unread = append(unread, info)
	})
	step8 := softReadAll(ctx, client, unread)
	// A soft read swallows its failure, so a cancelled context would
	// otherwise mark every remaining guest unreadable and report clean.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, views := range step8 {
		if views == nil {
			cache.configs[unread[i].vmid] = nil
		} else {
			cache.configs[unread[i].vmid] = mergeViews(views)
		}
	}
	records, unreadable := findMultiplyReferenced(vms, cache.configs, opts)
	visibility, visErr := referenceVisibility(ctx, client)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &inventory{
		disks:     disks,
		parkers:   parkers,
		intruders: intruders,
		multiRef: multiRefReport{
			records: records, unreadableVMIDs: unreadable, visibility: visibility, visibilityError: visErr,
		},
		skipped: skipped,
	}, nil
}

func hasImagesContent(content string) bool {
	for p := range strings.SplitSeq(content, ",") {
		if strings.TrimSpace(p) == "images" {
			return true
		}
	}
	return false
}

// holderKey names a guest in a warning: its VMID and name.
type holderKey struct {
	vmid int
	name string
}

// scanSinks are the indexes step 4 fills from the guest configs.
type scanSinks struct {
	// serialVolids holds every volid whose drive line carries a bpd- serial.
	serialVolids map[string]bool
	// serialHolders maps a bpd- serial to the volids carrying it and the
	// guests holding each.
	serialHolders map[string]map[string]map[holderKey]bool
	// sentinelRefs maps a full volid a sentinel names to the first guest
	// whose sentinel named it.
	sentinelRefs map[string]holderKey
	// parkerVolids holds every volid on a bus slot of a parker.
	parkerVolids map[string]bool
}

// scanGuests is step 4 of collectInventory: it reads every QEMU guest with a
// node, caches its config, and fills the sinks.
func scanGuests(
	ctx context.Context, client inventoryClient, vms *vmIndex, opts auditOptions,
	cache *guestCache, sinks scanSinks, warnOut io.Writer,
) error {
	var guests []*vmInfo
	vms.each(func(info *vmInfo) {
		if info.isQemu() && info.node != "" {
			guests = append(guests, info)
		}
	})
	soft := softReadAll(ctx, client, guests)
	if err := ctx.Err(); err != nil {
		return err
	}

	unreadGuests := 0
	for i, info := range guests {
		if soft[i] != nil {
			cache.store(info.vmid, soft[i])
		} else if err := cache.readHard(ctx, client, info.vmid, info.node); err != nil {
			// An unread guest could hold a volume the audit would then call
			// free-floating, so a real failure ends the audit.
			return err
		}
		config := cache.configs[info.vmid]
		if config == nil {
			unreadGuests++
			continue
		}
		isParker := opts.inParkerBand(info.vmid) && tagsContainParker(info.tags)
		holder := holderKey{vmid: info.vmid, name: info.name}
		for _, raw := range cache.bus[info.vmid] {
			slotVolid := busDiskVolidFromSlot(raw)
			if serial := stableIDFromDriveOptStr(raw); serial != "" {
				sinks.serialVolids[slotVolid] = true
				// A volid only the pending view names, for a token the
				// current view gave to another volid, is the same disk
				// mid-change and not a second holder.
				current, ok := cache.currentSerials[info.vmid][serial]
				if !ok || current == slotVolid {
					byVolid := sinks.serialHolders[serial]
					if byVolid == nil {
						byVolid = map[string]map[holderKey]bool{}
						sinks.serialHolders[serial] = byVolid
					}
					if byVolid[slotVolid] == nil {
						byVolid[slotVolid] = map[holderKey]bool{}
					}
					byVolid[slotVolid][holder] = true
				}
			}
			if isParker {
				sinks.parkerVolids[slotVolid] = true
			}
		}
		for named := range parseSentinelVolumeNames(config["description"], config) {
			if _, seen := sinks.sentinelRefs[named]; !seen {
				sinks.sentinelRefs[named] = holder
			}
		}
	}
	if unreadGuests > 0 {
		warn(warnOut, "%d guest config(s) could not be read; a renamed disk held only by such a guest "+
			"may be missing from this report.", unreadGuests)
	}
	warnDuplicateSerials(warnOut, sinks.serialHolders)
	return nil
}

// warnDuplicateSerials warns about every bpd- serial on more than one
// volume. qm clone copies drive options, so a clone of a BOSH VM carries the
// original's serial on its own volumes, and one token on two volumes is not
// a stable identity.
func warnDuplicateSerials(warnOut io.Writer, serialHolders map[string]map[string]map[holderKey]bool) {
	tokens := make([]string, 0, len(serialHolders))
	for t := range serialHolders {
		tokens = append(tokens, t)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		byVolid := serialHolders[token]
		if len(byVolid) < 2 {
			continue
		}
		volids := make([]string, 0, len(byVolid))
		for v := range byVolid {
			volids = append(volids, v)
		}
		sort.Strings(volids)
		parts := make([]string, 0, len(volids))
		for _, volid := range volids {
			hs := make([]holderKey, 0, len(byVolid[volid]))
			for h := range byVolid[volid] {
				hs = append(hs, h)
			}
			sort.Slice(hs, func(i, j int) bool {
				if hs[i].vmid != hs[j].vmid {
					return hs[i].vmid < hs[j].vmid
				}
				return hs[i].name < hs[j].name
			})
			names := make([]string, 0, len(hs))
			for _, h := range hs {
				names = append(names, fmt.Sprintf("VM %d (%s)", h.vmid, orUnnamed(h.name)))
			}
			parts = append(parts, fmt.Sprintf("%s (held by %s)", volid, strings.Join(names, ", ")))
		}
		warn(warnOut, "serial %s appears on %d different volumes: %s. A clone of a BOSH VM copies the "+
			"serial, so we cannot tell which volume the CPI means.", token, len(byVolid), strings.Join(parts, "; "))
	}
}

func orUnnamed(name string) string {
	if name == "" {
		return "unnamed"
	}
	return name
}

// findProvenance finds a volume's entry in a parker's bosh_parked_disks map.
// A legacy entry is keyed by the full volid. A stable-ID entry is keyed by
// the disk's bpd- token and names the volid the parker holds the volume
// under in its "volid" field. It returns nil when no entry describes the
// volume.
func findProvenance(provMap *orderedObject, volid string) any {
	if direct, ok := provMap.get(volid); ok && pyTruthy(direct) {
		return direct
	}
	for _, key := range provMap.keys {
		entry, ok := provMap.vals[key].(*orderedObject)
		if !ok {
			continue
		}
		if v, _ := entry.get("volid"); v == volid {
			return entry
		}
	}
	return nil
}

// applyProvenance copies the sentinel's provenance fields onto a parked
// disk's record. An entry that is not an object carries none.
func applyProvenance(rec *diskRecord, prov any) {
	entry, ok := prov.(*orderedObject)
	if !ok {
		return
	}
	field := func(key string) string {
		v, _ := entry.get(key)
		return strOr(v)
	}
	rec.diskCID = field("disk_cid")
	rec.sourceVMCID = field("source_vm_cid")
	rec.parkedAt = field("parked_at")
	rec.parkedNode = field("node")
	rec.directorID = field("director_id")
}
