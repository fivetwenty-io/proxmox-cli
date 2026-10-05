package cephview

import (
	"sort"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// healthMuteEntry is one element of GET /cluster/ceph/health-mute: a Ceph
// health check an operator has muted, and the terms of the mute. The apidoc
// types code, ttl, and summary as strings, but PVE builds this list in Perl
// and no live payload has been captured, so those three decode into any and
// render through output.Cell rather than risk failing the whole table on a
// number. The sticky flag is a boolean in the apidoc and may arrive as 0/1,
// so it decodes through the tolerant PVEBool.
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
			output.Cell(e.TTL),
			output.Cell(e.Summary),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return output.Result{Headers: healthMuteHeaders, Rows: rows, Raw: payload}, nil
}
