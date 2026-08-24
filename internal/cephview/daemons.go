package cephview

import (
	"fmt"
	"sort"
	"strings"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// daemonEntry is one element of the MON, MGR, and MDS list endpoints. The
// three payloads share a shape: an address, a state, a version banner, and
// the two flags PVE adds to say whether the node still has the daemon
// installed. Only the rank, quorum, and filesystem fields are specific to a
// kind, and each endpoint simply omits the ones that do not apply.
type daemonEntry struct {
	Name             string     `json:"name"`
	Host             string     `json:"host"`
	Addr             string     `json:"addr"`
	State            string     `json:"state"`
	CephVersion      string     `json:"ceph_version"`
	CephVersionShort string     `json:"ceph_version_short"`
	Rank             pve.PVEInt `json:"rank"`
	FsName           string     `json:"fs_name"`

	// These four are pointers because their absence and their false differ.
	// PVE sends direxists and service on every daemon it knows about, so a
	// missing flag means the endpoint did not report on it, not that the
	// daemon is broken.
	Quorum        *pve.PVEBool `json:"quorum"`
	StandbyReplay *pve.PVEBool `json:"standby_replay"`
	DirExists     *pve.PVEBool `json:"direxists"`
	Service       *pve.PVEBool `json:"service"`
}

// The daemon tables share their leading and trailing columns; the difference
// between the three is what each kind reports in between. The version banner
// is not a column of its own: it repeats "ceph version 20.2.2 (06f6a7ab...)"
// on every row beside the short version that says the same thing.
var (
	monHeaders = []string{"NAME", "HOST", "STATE", "RANK", "QUORUM", "ADDR", "VERSION", "NOTES"}
	mgrHeaders = []string{"NAME", "HOST", "STATE", "ADDR", "VERSION", "NOTES"}
	mdsHeaders = []string{"NAME", "HOST", "STATE", "RANK", "FS", "ADDR", "VERSION", "NOTES"}
)

// MonList renders the monitor list, naming each monitor's rank and whether it
// is in quorum.
func MonList(resp any) (output.Result, error) {
	return daemonList(resp, monHeaders, func(d daemonEntry) []string {
		return []string{rankCell(d), flagCell(d.Quorum)}
	})
}

// MgrList renders the manager list. A manager has neither a rank nor a
// quorum; which one is active is the whole of its state.
func MgrList(resp any) (output.Result, error) {
	return daemonList(resp, mgrHeaders, func(daemonEntry) []string { return nil })
}

// MdsList renders the metadata server list, naming the rank each MDS holds
// and the filesystem it serves.
func MdsList(resp any) (output.Result, error) {
	return daemonList(resp, mdsHeaders, func(d daemonEntry) []string {
		return []string{rankCell(d), d.FsName}
	})
}

// daemonList renders one row per daemon, with middle naming the columns that
// belong to this kind of daemon.
func daemonList(resp any, headers []string, middle func(daemonEntry) []string) (output.Result, error) {
	var daemons []daemonEntry
	payload, err := decode(resp, &daemons)
	if err != nil {
		return output.Result{}, err
	}

	// PVE returns these lists in whatever order the Ceph mon map yields, so
	// the rows move between runs. Sort by name to hold them still.
	sort.SliceStable(daemons, func(i, j int) bool { return daemons[i].Name < daemons[j].Name })

	rows := make([][]string, 0, len(daemons))
	for _, d := range daemons {
		row := []string{d.Name, d.Host, d.State}
		row = append(row, middle(d)...)
		row = append(row,
			d.Addr,
			shortVersion(d.CephVersion, d.CephVersionShort),
			daemonNotes(d),
		)
		rows = append(rows, row)
	}
	return output.Result{Headers: headers, Rows: rows, Raw: payload}, nil
}

// daemonNotes reports what is unusual about a daemon, and nothing at all
// about one that is in order. The two flags PVE adds, direxists and service,
// are true on every daemon of a working cluster, so a column of "yes" spent
// width to say nothing; they matter only when they are false.
func daemonNotes(d daemonEntry) string {
	var notes []string
	if d.Service != nil && !d.Service.Bool() {
		notes = append(notes, "no systemd unit")
	}
	if d.DirExists != nil && !d.DirExists.Bool() {
		notes = append(notes, "no data directory")
	}
	if d.StandbyReplay != nil && d.StandbyReplay.Bool() {
		notes = append(notes, "standby-replay")
	}
	return strings.Join(notes, ", ")
}

// rankCell renders a daemon's rank, and blanks the -1 Ceph uses for a standby
// that holds no rank at all.
func rankCell(d daemonEntry) string {
	rank := pveInt(d.Rank)
	if rank < 0 {
		return ""
	}
	return fmt.Sprintf("%d", rank)
}

// flagCell renders a flag PVE may not have sent, leaving the cell blank when
// the endpoint said nothing rather than claiming a "no".
func flagCell(b *pve.PVEBool) string {
	if b == nil {
		return ""
	}
	return yesNo(b.Bool())
}
