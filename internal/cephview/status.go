package cephview

import (
	"fmt"
	"sort"
	"strings"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// statusPayload is the part of GET /nodes/{node}/ceph/status and
// GET /cluster/ceph/status the summary reports. Both endpoints answer with
// the same `ceph status` document, so both commands share this view.
type statusPayload struct {
	Fsid   string `json:"fsid"`
	Health struct {
		Status string `json:"status"`
		Checks map[string]struct {
			Severity string `json:"severity"`
			Summary  struct {
				Message string `json:"message"`
			} `json:"summary"`
		} `json:"checks"`
	} `json:"health"`
	Monmap struct {
		Mons []struct {
			Name string `json:"name"`
		} `json:"mons"`
	} `json:"monmap"`
	QuorumNames []string   `json:"quorum_names"`
	QuorumAge   pve.PVEInt `json:"quorum_age"`
	Mgrmap      struct {
		ActiveName string `json:"active_name"`
		Available  bool   `json:"available"`
		Standbys   []struct {
			Name string `json:"name"`
		} `json:"standbys"`
	} `json:"mgrmap"`
	Fsmap struct {
		Up        pve.PVEInt `json:"up"`
		In        pve.PVEInt `json:"in"`
		Max       pve.PVEInt `json:"max"`
		UpStandby pve.PVEInt `json:"up:standby"`
	} `json:"fsmap"`
	Osdmap struct {
		NumOsds        pve.PVEInt `json:"num_osds"`
		NumUpOsds      pve.PVEInt `json:"num_up_osds"`
		NumInOsds      pve.PVEInt `json:"num_in_osds"`
		NumRemappedPgs pve.PVEInt `json:"num_remapped_pgs"`
	} `json:"osdmap"`
	Pgmap struct {
		NumPgs     pve.PVEInt `json:"num_pgs"`
		NumPools   pve.PVEInt `json:"num_pools"`
		NumObjects pve.PVEInt `json:"num_objects"`
		DataBytes  pve.PVEInt `json:"data_bytes"`
		BytesUsed  pve.PVEInt `json:"bytes_used"`
		BytesAvail pve.PVEInt `json:"bytes_avail"`
		BytesTotal pve.PVEInt `json:"bytes_total"`
		PgsByState []struct {
			StateName string     `json:"state_name"`
			Count     pve.PVEInt `json:"count"`
		} `json:"pgs_by_state"`
	} `json:"pgmap"`
}

// Status renders `ceph status` as the summary `ceph -s` prints: health first,
// then the daemons, then capacity. Anything not summarised here is still in
// output.Result.Raw for -o json and -o yaml.
func Status(resp any) (output.Result, error) {
	var st statusPayload
	payload, err := decode(resp, &st)
	if err != nil {
		return output.Result{}, err
	}

	// An empty body decodes cleanly, every field at its zero value, which
	// would render as a healthy cluster with nothing in it. Say what
	// actually happened instead.
	health := st.Health.Status
	if health == "" {
		health = "(no status reported)"
	}

	rows := [][]string{
		{"health", health},
	}
	rows = append(rows, healthCheckRows(st)...)
	rows = append(rows,
		[]string{"fsid", st.Fsid},
		[]string{"mons", monCell(st)},
		[]string{"mgr", mgrCell(st)},
	)
	if mds := mdsCell(st); mds != "" {
		rows = append(rows, []string{"mds", mds})
	}
	rows = append(rows,
		[]string{"osds", osdCell(st)},
		[]string{"pools", countCell(int(pveInt(st.Pgmap.NumPools)))},
		[]string{"pgs", pgCell(st)},
		[]string{"objects", countCell(int(pveInt(st.Pgmap.NumObjects)))},
		[]string{"data", bytesCell(pveInt(st.Pgmap.DataBytes))},
		[]string{"usage", usageCell(st)},
	)

	return output.Result{Headers: fieldHeaders, Rows: rows, Raw: payload}, nil
}

// healthCheckRows renders one row per failing health check, which is the part
// of the payload an operator reading "HEALTH_WARN" actually needs. The map is
// keyed by check code (POOL_NO_REDUNDANCY, OSD_DOWN) and empty on a healthy
// cluster.
func healthCheckRows(st statusPayload) [][]string {
	codes := make([]string, 0, len(st.Health.Checks))
	for code := range st.Health.Checks {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	rows := make([][]string, 0, len(codes))
	for _, code := range codes {
		check := st.Health.Checks[code]
		value := check.Summary.Message
		if check.Severity != "" {
			value = check.Severity + ": " + value
		}
		rows = append(rows, []string{"  " + code, value})
	}
	return rows
}

// monCell reports the monitor count with the quorum it forms, since a monmap
// count alone cannot say whether the cluster has quorum.
func monCell(st statusPayload) string {
	cell := countCell(len(st.Monmap.Mons))
	if len(st.QuorumNames) > 0 {
		cell += fmt.Sprintf(", quorum %s", strings.Join(st.QuorumNames, ", "))
	}
	if age := ageCell(pveInt(st.QuorumAge)); age != "" {
		cell += " (age " + age + ")"
	}
	return cell
}

// mgrCell reports the active manager and its standbys.
func mgrCell(st statusPayload) string {
	active := st.Mgrmap.ActiveName
	if active == "" {
		return "no active manager"
	}
	cell := active + " (active)"
	if !st.Mgrmap.Available {
		cell += ", not available"
	}
	if n := len(st.Mgrmap.Standbys); n > 0 {
		names := make([]string, 0, n)
		for _, s := range st.Mgrmap.Standbys {
			names = append(names, s.Name)
		}
		cell += ", standbys: " + strings.Join(names, ", ")
	}
	return cell
}

// mdsCell reports the CephFS metadata servers, and returns "" when the
// cluster has no filesystem, so the row is left out rather than showing a
// line of zeroes.
func mdsCell(st statusPayload) string {
	up, in, mx := pveInt(st.Fsmap.Up), pveInt(st.Fsmap.In), pveInt(st.Fsmap.Max)
	standby := pveInt(st.Fsmap.UpStandby)
	if up == 0 && in == 0 && mx == 0 && standby == 0 {
		return ""
	}
	cell := fmt.Sprintf("%d up / %d in / %d max", up, in, mx)
	if standby > 0 {
		cell += fmt.Sprintf(", %d standby", standby)
	}
	return cell
}

// osdCell reports the OSD counts, naming any remapped placement groups since
// that is what a rebalancing cluster looks like from here.
func osdCell(st statusPayload) string {
	cell := fmt.Sprintf("%d up / %d in / %d total",
		pveInt(st.Osdmap.NumUpOsds), pveInt(st.Osdmap.NumInOsds), pveInt(st.Osdmap.NumOsds))
	if n := pveInt(st.Osdmap.NumRemappedPgs); n > 0 {
		cell += fmt.Sprintf(", %d remapped pgs", n)
	}
	return cell
}

// pgCell reports the placement-group total with its state breakdown, which is
// where a degraded cluster shows itself.
func pgCell(st statusPayload) string {
	cell := countCell(int(pveInt(st.Pgmap.NumPgs)))
	if len(st.Pgmap.PgsByState) == 0 {
		return cell
	}
	states := make([]string, 0, len(st.Pgmap.PgsByState))
	for _, s := range st.Pgmap.PgsByState {
		states = append(states, fmt.Sprintf("%d %s", pveInt(s.Count), s.StateName))
	}
	sort.Strings(states)
	return cell + " (" + strings.Join(states, ", ") + ")"
}

// usageCell reports raw capacity the way `ceph -s` does, as used of total
// with the percentage that decides when a pool stops accepting writes.
func usageCell(st statusPayload) string {
	used, avail, total := pveInt(st.Pgmap.BytesUsed), pveInt(st.Pgmap.BytesAvail), pveInt(st.Pgmap.BytesTotal)
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("%s used / %s avail / %s total (%s)",
		bytesCell(used), bytesCell(avail), bytesCell(total),
		percentCell(float64(used)/float64(total)))
}
