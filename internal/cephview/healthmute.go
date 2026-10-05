package cephview

import (
	"sort"
	"time"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// healthMuteEntry is one element of GET /cluster/ceph/health-mute: a Ceph
// health check an operator has muted, and the terms of the mute. The apidoc
// types code, ttl, and summary as strings, but PVE builds this list in Perl,
// so those three decode into any and render through output.Cell rather than
// risk failing the whole table on a number. Despite its name, ttl carries the
// absolute expiry time, such as 2026-10-05T14:40:46.223595-0400. The sticky
// flag is a boolean in the apidoc but arrives as 0/1, so it decodes through
// the tolerant PVEBool.
type healthMuteEntry struct {
	Code    any         `json:"code"`
	Sticky  pve.PVEBool `json:"sticky"`
	TTL     any         `json:"ttl"`
	Summary any         `json:"summary"`
}

// healthMuteHeaders names one muted check per row.
var healthMuteHeaders = []string{"CODE", "STICKY", "EXPIRES", "SUMMARY"}

// HealthMute renders the muted Ceph health checks, sorted by code. A mute
// without an expiry leaves its EXPIRES cell blank, because the API leaves the
// field out for a mute that does not expire.
func HealthMute(resp any) (output.Result, error) {
	var entries []healthMuteEntry
	payload, err := decode(resp, &entries)
	if err != nil {
		return output.Result{}, err
	}

	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			output.Cell(e.Code),
			yesNo(e.Sticky.Bool()),
			expiresCell(e.TTL),
			output.Cell(e.Summary),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return output.Result{Headers: healthMuteHeaders, Rows: rows, Raw: payload}, nil
}

// healthMuteExpiryLayout is the timestamp PVE writes into a mute's ttl field.
// The fractional seconds are optional on parse.
const healthMuteExpiryLayout = "2006-01-02T15:04:05.999999999-0700"

// expiresCell renders a mute's expiry to the second, in the offset the server
// reported, because the microseconds Ceph keeps say nothing to an operator.
// A value that is not such a timestamp renders as the server sent it.
func expiresCell(ttl any) string {
	if s, ok := ttl.(string); ok {
		if t, err := time.Parse(healthMuteExpiryLayout, s); err == nil {
			return t.Format("2006-01-02 15:04:05 -0700")
		}
	}
	return output.Cell(ttl)
}
