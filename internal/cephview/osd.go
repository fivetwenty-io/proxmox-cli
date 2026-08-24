package cephview

import (
	"fmt"
	"strings"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// osdTreePayload is GET /nodes/{node}/ceph/osd: the CRUSH tree, plus the
// cluster-wide OSD flags. The tree nests roots over hosts over OSDs, which is
// why the generic renderer could only ever show its outermost two keys.
type osdTreePayload struct {
	Flags string      `json:"flags"`
	Root  osdTreeNode `json:"root"`
}

// osdTreeNode is one CRUSH node. PVE re-encodes Ceph's JSON through Perl, so
// the id arrives as a string on some nodes and a number on others, and the
// same is true of the counts.
type osdTreeNode struct {
	ID          pve.PVEInt    `json:"id"`
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Status      string        `json:"status"`
	DeviceClass string        `json:"device_class"`
	In          pve.PVEInt    `json:"in"`
	Reweight    pve.PVEFloat  `json:"reweight"`
	CrushWeight pve.PVEFloat  `json:"crush_weight"`
	Pgs         pve.PVEInt    `json:"pgs"`
	BytesUsed   pve.PVEInt    `json:"bytes_used"`
	TotalSpace  pve.PVEInt    `json:"total_space"`
	PercentUsed pve.PVEFloat  `json:"percent_used"`
	Version     string        `json:"version"`
	VersionLong string        `json:"ceph_version"`
	VersionShrt string        `json:"ceph_version_short"`
	Children    []osdTreeNode `json:"children"`
}

// osdTreeHeaders names the columns `ceph osd tree` reports, plus the capacity
// columns PVE adds and the operator came here for.
var osdTreeHeaders = []string{
	"NAME", "TYPE", "ID", "CLASS", "STATUS", "IN", "WEIGHT", "REWEIGHT",
	"PGS", "USED", "TOTAL", "USE%", "VERSION",
}

// OSDTree renders the CRUSH tree as one indented row per node, the way
// `ceph osd tree` does. The cluster-wide flags are not in the table because
// they are not per-OSD; `pmx pve cluster ceph flags list` reports them, and
// -o json carries them here too.
func OSDTree(resp any) (output.Result, error) {
	var tree osdTreePayload
	payload, err := decode(resp, &tree)
	if err != nil {
		return output.Result{}, err
	}

	var rows [][]string
	// The outermost node is a synthetic container with no name of its own,
	// so the walk starts at its children and the roots sit flush left.
	for _, child := range tree.Root.Children {
		rows = append(rows, osdTreeRows(child, 0)...)
	}
	return output.Result{Headers: osdTreeHeaders, Rows: rows, Raw: payload}, nil
}

// osdTreeRows renders one CRUSH node and everything beneath it, indenting by
// depth so the hierarchy survives a flat table.
func osdTreeRows(n osdTreeNode, depth int) [][]string {
	rows := [][]string{{
		indent(depth) + n.Name,
		n.Type,
		fmt.Sprintf("%d", pveInt(n.ID)),
		n.DeviceClass,
		n.Status,
		inCell(n),
		bucketBlank(n, weightCell(n.CrushWeight.Float())),
		weightCell(n.Reweight.Float()),
		pgsCell(n),
		capacityCell(pveInt(n.BytesUsed)),
		capacityCell(pveInt(n.TotalSpace)),
		usePercentCell(n),
		shortVersion(n.VersionLong, firstNonEmpty(n.VersionShrt, n.Version)),
	}}
	for _, child := range n.Children {
		rows = append(rows, osdTreeRows(child, depth+1)...)
	}
	return rows
}

// indent renders the depth of a CRUSH node. It marks the levels with a
// visible glyph rather than with spaces because the renderer trims a cell,
// and leading whitespace would flatten the tree back out.
func indent(depth int) string {
	if depth == 0 {
		return ""
	}
	return strings.Repeat("·", depth) + " "
}

// bucketBlank blanks a value on anything that is not an OSD. PVE reports a
// CRUSH weight only on the leaves, and a 0.00000 on a root reads as a bucket
// holding nothing rather than as a field that was never sent.
func bucketBlank(n osdTreeNode, cell string) string {
	if n.Type != "osd" {
		return ""
	}
	return cell
}

// inCell renders the in/out flag, which only an OSD has. A host would
// otherwise report "out" merely because the field is absent.
func inCell(n osdTreeNode) string {
	if n.Type != "osd" {
		return ""
	}
	if pveInt(n.In) != 0 {
		return "in"
	}
	return "out"
}

// weightCell renders a CRUSH weight, and blanks the -1 PVE uses on a bucket
// to mean "not applicable".
func weightCell(w float64) string {
	if w < 0 {
		return ""
	}
	return fmt.Sprintf("%.5f", w)
}

// pgsCell blanks the placement-group count on a bucket, where it is always 0
// and means nothing.
func pgsCell(n osdTreeNode) string {
	if n.Type != "osd" {
		return ""
	}
	return countCell(int(pveInt(n.Pgs)))
}

// capacityCell blanks a zero byte count, which on a bucket means the field
// was not reported rather than that the bucket is empty.
func capacityCell(n int64) string {
	if n == 0 {
		return ""
	}
	return bytesCell(n)
}

// usePercentCell renders an OSD's fullness. Ceph reports it as a percentage
// here, unlike the ratios elsewhere in the same payload.
func usePercentCell(n osdTreeNode) string {
	if n.Type != "osd" {
		return ""
	}
	return fmt.Sprintf("%.2f%%", n.PercentUsed.Float())
}

// firstNonEmpty returns the first of its arguments that carries anything.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
