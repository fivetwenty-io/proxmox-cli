// Package cephview turns the Ceph payloads PVE passes through into tables an
// operator can read.
//
// Every one of these endpoints answers with Ceph's own JSON: a nested tree
// that the generic renderer can only summarise or, before it learned to,
// marshal whole into one cell. `pmx pve node ceph status` wrote 3.95 MB that
// way. These views select what `ceph -s`, `ceph osd tree`, and `ceph df`
// report, and leave the rest to -o json, which still carries the payload
// verbatim.
package cephview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// fieldHeaders is the two-column shape a summary view renders in. It is a
// Headers/Rows table rather than a Single map because the order of the rows
// is the point: a Single renders its keys alphabetically, which puts capacity
// above health.
var fieldHeaders = []string{"FIELD", "VALUE"}

// decode reassembles an SDK response into the JSON PVE actually sent, then
// unmarshals it into v and, separately, into an untyped payload for
// output.Result.Raw, so -o json and -o yaml still carry everything.
//
// The SDK types these endpoints as a struct of json.RawMessage members, or as
// a bare json.RawMessage, because the payload underneath is Ceph's own and
// not statically describable. Marshalling that back reproduces the original
// document in both shapes.
func decode(resp, v any) (any, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return nil, err
	}
	var payload any
	// The typed decode above already proved raw is well-formed.
	_ = json.Unmarshal(raw, &payload)
	return payload, nil
}

// bytesCell renders a byte count the way Ceph's own tools do. Ceph reports
// capacity in bytes, and a 644219928576 in a table tells nobody anything.
func bytesCell(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, exp := float64(n), 0
	for value >= unit && exp < 5 {
		value /= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", value, "KMGTPE"[exp-1])
}

// percentCell renders a Ceph usage ratio, which arrives as a fraction rather
// than as a percentage.
func percentCell(ratio float64) string {
	return fmt.Sprintf("%.2f%%", ratio*100)
}

// ageCell renders a duration in seconds as the coarse "3d" or "4h" Ceph uses
// for quorum age, where the exact second has never mattered.
func ageCell(seconds int64) string {
	switch {
	case seconds <= 0:
		return ""
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}

// countCell renders a total, avoiding the bare "0" that reads as missing data
// when it sits beside a name.
func countCell(n int) string { return strconv.Itoa(n) }

// sortedKeys returns m's keys in a stable order, so a view built from a map
// does not reshuffle between runs.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// shortVersion trims Ceph's full version banner down to its number. The
// banner repeats the same 90 characters on every row of every daemon table.
func shortVersion(full, short string) string {
	if short != "" {
		return short
	}
	fields := strings.Fields(full)
	if len(fields) >= 3 && fields[0] == "ceph" && fields[1] == "version" {
		return fields[2]
	}
	return full
}

// pveInt reads a tolerant integer, which is what these payloads need: PVE
// re-encodes parts of Ceph's JSON through Perl, so the OSD tree carries its
// ids as strings and its placement-group counts as numbers in one response.
func pveInt(v pve.PVEInt) int64 { return v.Int() }
