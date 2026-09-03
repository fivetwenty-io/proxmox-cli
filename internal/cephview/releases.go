package cephview

import (
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// releaseEntry is one element of GET /nodes/{node}/ceph/releases: a Ceph
// release the node's package sources know about, and whether it can be
// installed here. The flags arrive as 0/1 integers on a live node and as
// booleans in the apidoc, so they decode through the tolerant PVEBool.
type releaseEntry struct {
	Release     string      `json:"release"`
	Version     string      `json:"version"`
	Available   pve.PVEBool `json:"available"`
	IsDefault   pve.PVEBool `json:"is-default"`
	Unsupported pve.PVEBool `json:"unsupported"`
}

// releasesHeaders names one release per row.
var releasesHeaders = []string{"RELEASE", "VERSION", "AVAILABLE", "DEFAULT", "UNSUPPORTED"}

// Releases renders the Ceph release catalogue: which releases have packages
// for this node, which one new installations get, and which are past
// support. The API flags are named for the question they answer, so the
// columns keep those names rather than folding them into one status word.
func Releases(resp any) (output.Result, error) {
	var releases []releaseEntry
	payload, err := decode(resp, &releases)
	if err != nil {
		return output.Result{}, err
	}

	rows := make([][]string, 0, len(releases))
	for _, r := range releases {
		rows = append(rows, []string{
			r.Release,
			r.Version,
			yesNo(r.Available.Bool()),
			yesNo(r.IsDefault.Bool()),
			yesNo(r.Unsupported.Bool()),
		})
	}
	return output.Result{Headers: releasesHeaders, Rows: rows, Raw: payload}, nil
}
