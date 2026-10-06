package cpi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The parsers in this file read the shapes the CPI writes into PVE: volume
// names, drive option strings, guest tags, and the <!--BOSH:{...}--> sentinel
// in a VM description. Each one mirrors the CPI's own reader, so the audit
// recognizes exactly the volumes the CPI would.

const (
	// parkerTag marks a VM as a parker, a holder for detached disks.
	parkerTag = "bosh-parker"

	// stableIDPrefix starts the drive serial the CPI gives a persistent disk.
	// The serial identifies the disk independently of the VMID in the
	// volume's name, which the CPI rewrites on every attach and detach.
	stableIDPrefix = "bpd-"

	// parkerSlotCapacity is how many bus slots a parker can fill, scsi0
	// through scsi30.
	parkerSlotCapacity = 31
)

var (
	// diskVolidRE matches <storage>:vm-<vmid>-disk-<n>, the shape whose
	// embedded VMID the disk band is checked against.
	diskVolidRE = regexp.MustCompile(`^[^:]+:vm-(\d+)-disk-\d+$`)

	// sentinelRE finds the first sentinel in a VM description. It is the
	// same pattern the CPI's parker code reads, and it spans newlines.
	sentinelRE = regexp.MustCompile(`(?s)<!--BOSH:(.*?)-->`)

	// tagSepRE splits a tag string the way the CPI does. PVE writes
	// semicolons, and a hand-edited tag field can carry commas or spaces.
	tagSepRE = regexp.MustCompile(`[;, ]+`)

	// busKeyRE matches the config keys that hold a disk on a bus. efidisk0
	// and tpmstate0 hold firmware state rather than persistent disks, and an
	// unusedN entry is not on a bus at all.
	busKeyRE = regexp.MustCompile(`^(scsi|virtio|ide|sata)\d+$`)

	// unusedKeyRE matches the unusedN entries PVE demotes a detached volume
	// to. `qm destroy --purge` frees the volume behind each one.
	unusedKeyRE = regexp.MustCompile(`^unused\d+$`)

	// ownerNameRE reads the owning VMID from the last path segment of a
	// volume name, the way PVE does on every storage type.
	ownerNameRE = regexp.MustCompile(`^(?:vm|base)-(\d+)-`)

	// storageVolidRE matches a volid with a storage prefix. Anything else on
	// a bus key is a passthrough path or a placeholder.
	storageVolidRE = regexp.MustCompile(`^[^:/\s]+:\S+$`)
)

// sentinelVolumeCarriers are the sentinel keys that name volumes.
// bosh_parked_disks and bosh_disk_allocations map a volid, or a bpd- token,
// to an entry whose "volid" field names the volume. bosh_attached_disks maps
// a volid, or a bpd- token, to the Director's disk CID, so its entries name a
// volume only through the key.
var sentinelVolumeCarriers = []string{"bosh_parked_disks", "bosh_attached_disks", "bosh_disk_allocations"}

// orderedObject is a JSON object decoded with its key order kept. A key that
// appears twice keeps its first position and its last value, which is how
// the CPI's sentinel readers see a repeated key.
type orderedObject struct {
	keys []string
	vals map[string]any
}

func newOrderedObject() *orderedObject {
	return &orderedObject{vals: map[string]any{}}
}

func (o *orderedObject) get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[key]
	return v, ok
}

func (o *orderedObject) set(key string, v any) {
	if _, seen := o.vals[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *orderedObject) len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// MarshalJSON writes the object back out in its decoded key order.
func (o *orderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// decodeOrderedJSON decodes exactly one JSON value from text. Objects come
// back as *orderedObject, arrays as []any, and numbers as json.Number, so a
// value's literal text survives. Trailing data after the value is an error.
func decodeOrderedJSON(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	v, err := decodeOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("extra data after the JSON value")
	}
	return v, nil
}

func decodeOrderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := newOrderedObject()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := kt.(string)
			if !ok {
				return nil, fmt.Errorf("object key is %T, not a string", kt)
			}
			v, err := decodeOrderedValue(dec)
			if err != nil {
				return nil, err
			}
			obj.set(key, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := decodeOrderedValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delim)
	}
}

// pyTruthy reports whether a decoded JSON value is truthy: false, zero, an
// empty string, an empty array or object, and null are not.
func pyTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	case *orderedObject:
		return t.len() > 0
	default:
		return true
	}
}

// pyStr renders a decoded JSON value as text. A string is itself, a whole
// number has no fraction, and an array or object is its JSON text.
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// strOr renders v as text, or "" when v is falsy, so a null, empty, or zero
// field reads as absent.
func strOr(v any) string {
	if !pyTruthy(v) {
		return ""
	}
	return pyStr(v)
}

// fieldStr reads key from a decoded object as text: "" when the key is
// absent, and pyStr of the value otherwise.
func fieldStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	return pyStr(v)
}

// pyInt converts a decoded JSON value to an integer. A number is truncated
// toward zero, a string must hold a base-10 integer, and a boolean is 1 or 0.
// Anything else does not convert.
func pyInt(v any) (int, bool) {
	switch t := v.(type) {
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || math.Abs(t) > math.MaxInt64 {
			return 0, false
		}
		return int(math.Trunc(t)), true
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n), true
		}
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return pyInt(f)
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

// pendingViews holds a qemu guest's config in both of PVE's views. current
// is what the running guest has now. applied is the config with pending
// changes applied, which is what the config endpoint returns. A drive whose
// delete is pending is in current only, and the running guest still has it
// plugged in.
type pendingViews struct {
	current map[string]string
	applied map[string]string
}

// pendingScalar renders one JSON scalar of a pending row the way the config
// map's values read. An object, an array, and null have no scalar form.
func pendingScalar(v any) (string, bool) {
	switch t := v.(type) {
	case nil, []any, map[string]any, *orderedObject:
		return "", false
	case bool:
		if t {
			return "1", true
		}
		return "0", true
	default:
		return pyStr(t), true
	}
}

// parsePendingViews splits the rows of GET /nodes/{node}/qemu/{vmid}/pending
// into the current and applied views. Each row carries a key, its current
// value, an optional pending value, and a delete flag. The current view takes
// every row's value. The applied view takes the pending value over the
// current one and leaves out a key whose delete is pending. A row that is not
// an object or has no key is skipped. It returns nil when data is not a list,
// which reads as an unreadable or missing guest.
func parsePendingViews(data any) *pendingViews {
	rows, ok := data.([]any)
	if !ok {
		return nil
	}
	views := &pendingViews{current: map[string]string{}, applied: map[string]string{}}
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		key, ok := row["key"].(string)
		if !ok || key == "" {
			continue
		}
		value, hasValue := pendingScalar(row["value"])
		pending, hasPending := pendingScalar(row["pending"])
		flag, hasFlag := pendingScalar(row["delete"])
		if hasValue {
			views.current[key] = value
		}
		if hasFlag && flag != "" && flag != "0" {
			continue
		}
		if hasPending {
			views.applied[key] = pending
		} else if hasValue {
			views.applied[key] = value
		}
	}
	return views
}

// mergeViews folds both views into one config that names a key when either
// view does. The applied value wins for a key both views have, and a key
// whose delete is pending keeps its current value, because the running guest
// still holds it.
func mergeViews(v *pendingViews) map[string]string {
	merged := make(map[string]string, len(v.current)+len(v.applied))
	maps.Copy(merged, v.current)
	maps.Copy(merged, v.applied)
	return merged
}

// sortedKeys returns the keys of m in byte order, which for UTF-8 text is
// code point order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// busValues returns every distinct non-empty bus-slot value in the given
// configs, each config walked in sorted key order.
func busValues(configs ...map[string]string) []string {
	var seen []string
	have := map[string]bool{}
	for _, cfg := range configs {
		for _, key := range sortedKeys(cfg) {
			val := cfg[key]
			if busKeyRE.MatchString(key) && val != "" && !have[val] {
				have[val] = true
				seen = append(seen, val)
			}
		}
	}
	return seen
}

// viewsBusValues returns every distinct raw bus-slot value either view holds,
// current first, in a stable order.
func viewsBusValues(v *pendingViews) []string {
	return busValues(v.current, v.applied)
}

// sentinelObject returns the top-level object of the first sentinel in a VM
// description, or nil when there is no sentinel or it does not decode to an
// object. A corrupt sentinel never ends the audit.
func sentinelObject(description string) *orderedObject {
	if description == "" {
		return nil
	}
	m := sentinelRE.FindStringSubmatch(description)
	if m == nil {
		return nil
	}
	v, err := decodeOrderedJSON(m[1])
	if err != nil {
		return nil
	}
	obj, _ := v.(*orderedObject)
	return obj
}

// parseParkerSentinel returns the bosh_parked_disks map from a parker VM
// description, or an empty map when the sentinel is missing or corrupt or
// carries no such map. Other BOSH keys may sit alongside it.
func parseParkerSentinel(description string) *orderedObject {
	top := sentinelObject(description)
	if v, ok := top.get("bosh_parked_disks"); ok {
		if disks, ok := v.(*orderedObject); ok {
			return disks
		}
	}
	return newOrderedObject()
}

// stableIDFromDriveOptStr returns the bpd- serial on a drive option string,
// "<volid>,serial=bpd-...,size=...", or "" when it has none. The first
// serial option decides: a serial without the bpd- prefix belongs to an
// operator or to guest tooling, and the scan stops there. The volid before
// the first comma is never read as an option.
func stableIDFromDriveOptStr(optStr string) string {
	rest := optStr
	for {
		comma := strings.IndexByte(rest, ',')
		if comma < 0 {
			return ""
		}
		rest = rest[comma+1:]
		if v, ok := strings.CutPrefix(rest, "serial="); ok {
			if end := strings.IndexByte(v, ','); end >= 0 {
				v = v[:end]
			}
			if strings.HasPrefix(v, stableIDPrefix) {
				return v
			}
			return ""
		}
	}
}

// parseSentinelVolumeNames returns the full volids a VM description's
// sentinel names, read from bosh_parked_disks, bosh_attached_disks, and
// bosh_disk_allocations. The CPI writes an entry under a bpd- token or under
// a full "storage:vm-N-disk-M" volid, and its "volid" field carries the
// storage prefix, so a name without a colon is not one the CPI wrote and is
// ignored. A bpd- token names no volume by itself, because the drive that
// carries the token already counts through its serial. When config is given,
// a bosh_parked_disks entry that carries a "slot" also names the volume on
// that slot, because a transfer that crashed before its finalize write leaves
// the entry naming the pre-move volid.
func parseSentinelVolumeNames(description string, config map[string]string) map[string]struct{} {
	full := map[string]struct{}{}
	top := sentinelObject(description)
	if top == nil {
		return full
	}
	add := func(name any) {
		s, ok := name.(string)
		if !ok {
			return
		}
		s = strings.TrimSpace(s)
		if s != "" && strings.Contains(s, ":") && !strings.HasPrefix(s, stableIDPrefix) {
			full[s] = struct{}{}
		}
	}
	slots := parseBusDisks(config)
	for _, carrier := range sentinelVolumeCarriers {
		v, _ := top.get(carrier)
		entries, ok := v.(*orderedObject)
		if !ok {
			continue
		}
		for _, key := range entries.keys {
			add(key)
			entry, ok := entries.vals[key].(*orderedObject)
			if !ok {
				continue
			}
			volid, _ := entry.get("volid")
			add(volid)
			slotV, _ := entry.get("slot")
			slot, ok := slotV.(string)
			if !ok || carrier != "bosh_parked_disks" {
				continue
			}
			if raw, ok := slots[slot]; ok {
				add(strings.TrimSpace(busDiskVolidFromSlot(raw)))
			}
		}
	}
	return full
}

// isZeroFlag reports whether a PVE flag field reads as zero, whether it came
// as 0, "0", or false. A missing field is not zero.
func isZeroFlag(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t) == "0"
	case bool:
		return !t
	case float64:
		return t == 0
	case json.Number:
		f, err := t.Float64()
		return err == nil && f == 0
	default:
		return false
	}
}

// storageSkipReason says why a storage listing entry cannot be read, or ""
// when it can. PVE answers HTTP 500 to a content read on a disabled storage,
// so a storage the listing marks enabled=0 or active=0 is skipped. A missing
// field counts as enabled and active. An enabled storage that is not active
// may hold CPI disks, so that reason is worded as partial coverage.
func storageSkipReason(entry map[string]any) string {
	if isZeroFlag(entry["enabled"]) {
		return "storage is disabled (enabled=0)"
	}
	if isZeroFlag(entry["active"]) {
		return "storage is enabled but not active (active=0), so coverage of it is partial"
	}
	return ""
}

// diskVMIDFromVolid returns the VMID embedded in <storage>:vm-<vmid>-disk-<n>,
// or false when the volid has another shape.
func diskVMIDFromVolid(volid string) (int, bool) {
	m := diskVolidRE.FindStringSubmatch(volid)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// tagsContainParker reports whether a tag string includes bosh-parker,
// compared without case.
func tagsContainParker(tags string) bool {
	for _, t := range tagSepRE.Split(tags, -1) {
		if strings.ToLower(strings.TrimSpace(t)) == parkerTag {
			return true
		}
	}
	return false
}

// parseBusDisks returns slot -> raw value for every non-empty bus disk key in
// a config. A raw value may carry options after the volid.
func parseBusDisks(config map[string]string) map[string]string {
	out := map[string]string{}
	for key, val := range config {
		if busKeyRE.MatchString(key) && val != "" {
			out[key] = val
		}
	}
	return out
}

// busDiskVolidFromSlot returns the volid from a slot value, the part before
// the first comma, or the whole value when it has no options.
func busDiskVolidFromSlot(raw string) string {
	volid, _, _ := strings.Cut(raw, ",")
	return volid
}

// countUnusedRefs counts the non-empty unusedN entries in a config. A sweep
// that did not complete leaves one behind, and it still references a live
// volume.
func countUnusedRefs(config map[string]string) int {
	n := 0
	for key, val := range config {
		if unusedKeyRE.MatchString(key) && val != "" {
			n++
		}
	}
	return n
}

// parseParkerPoolArgs turns the raw --parker-pool values into a clean,
// ordered list of names. The flag may be repeated and each value may be a
// comma-separated list. Blanks are dropped and a repeated name is kept once.
func parseParkerPoolArgs(values []string) []string {
	names := []string{}
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !slices.Contains(names, part) {
				names = append(names, part)
			}
		}
	}
	return names
}

// volumeOwnerVMID returns the VMID that owns a volid by name, read from the
// vm-N- or base-N- prefix of its last path segment, or false when the name
// encodes none. A linked clone carries its base volume's path in front, so
// "base-100-disk-0/vm-101-disk-0" belongs to VM 101.
func volumeOwnerVMID(volid string) (int, bool) {
	name := volid
	if _, after, ok := strings.Cut(volid, ":"); ok {
		name = after
	}
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	m := ownerNameRE.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// volumeRef is one volume a guest config names.
type volumeRef struct {
	slot  string
	kind  string // "active" for a bus slot, "unused" for an unusedN entry
	volid string
}

// referencedVolids returns every volume a guest config names on a bus slot
// or an unusedN entry, in sorted key order. A cdrom drive is left out, and
// that includes the cloud-init drive, which sits on a bus key as
// "<storage>:vm-<vmid>-cloudinit,media=cdrom". The "none" placeholder and a
// passthrough path are left out too.
func referencedVolids(config map[string]string) []volumeRef {
	var out []volumeRef
	for _, key := range sortedKeys(config) {
		val := config[key]
		if val == "" {
			continue
		}
		var kind string
		switch {
		case busKeyRE.MatchString(key):
			kind = "active"
		case unusedKeyRE.MatchString(key):
			kind = "unused"
		default:
			continue
		}
		if hasCdromOption(val) {
			continue
		}
		volid := strings.TrimSpace(busDiskVolidFromSlot(val))
		if !storageVolidRE.MatchString(volid) {
			continue
		}
		out = append(out, volumeRef{slot: key, kind: kind, volid: volid})
	}
	return out
}

func hasCdromOption(val string) bool {
	opts := strings.Split(val, ",")[1:]
	for _, opt := range opts {
		if strings.ToLower(strings.TrimSpace(opt)) == "media=cdrom" {
			return true
		}
	}
	return false
}
