package cephview

import (
	"fmt"
	"strings"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// fsEntry is one element of GET /nodes/{node}/ceph/fs. The endpoint names the
// data pool twice, once as a scalar and once as a list, and carries the pool
// ids in parallel arrays, which the generic renderer turned into five columns
// holding three facts.
type fsEntry struct {
	Name           string       `json:"name"`
	MetadataPool   string       `json:"metadata_pool"`
	MetadataPoolID pve.PVEInt   `json:"metadata_pool_id"`
	DataPools      []string     `json:"data_pools"`
	DataPoolIDs    []pve.PVEInt `json:"data_pool_ids"`
}

// fsHeaders names one filesystem per row.
var fsHeaders = []string{"NAME", "METADATA POOL", "DATA POOLS"}

// FSList renders the CephFS filesystems, pairing each pool with its id rather
// than reporting the names and the ids as separate columns.
func FSList(resp any) (output.Result, error) {
	var filesystems []fsEntry
	payload, err := decode(resp, &filesystems)
	if err != nil {
		return output.Result{}, err
	}

	rows := make([][]string, 0, len(filesystems))
	for _, fs := range filesystems {
		rows = append(rows, []string{
			fs.Name,
			poolCell(fs.MetadataPool, &fs.MetadataPoolID),
			dataPoolsCell(fs),
		})
	}
	return output.Result{Headers: fsHeaders, Rows: rows, Raw: payload}, nil
}

// dataPoolsCell names every data pool backing the filesystem. CephFS allows
// more than one, and PVE reports the ids in a second array in the same order.
func dataPoolsCell(fs fsEntry) string {
	cells := make([]string, 0, len(fs.DataPools))
	for i, name := range fs.DataPools {
		var id *pve.PVEInt
		if i < len(fs.DataPoolIDs) {
			id = &fs.DataPoolIDs[i]
		}
		cells = append(cells, poolCell(name, id))
	}
	return strings.Join(cells, ", ")
}

// poolCell names a pool and its id together, which is how `ceph fs get`
// reports the pair and how an operator reads it.
func poolCell(name string, id *pve.PVEInt) string {
	if name == "" {
		return ""
	}
	if id == nil {
		return name
	}
	return fmt.Sprintf("%s (%d)", name, pveInt(*id))
}
