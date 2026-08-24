package cephview

import (
	"fmt"
	"sort"
	"strings"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// poolEntry is one element of GET /nodes/{node}/ceph/pools. The endpoint
// returns seventeen top-level keys per pool, one of them a twenty-field
// autoscale report, so the generic renderer produced seventeen columns of
// which most were truncated past usefulness.
type poolEntry struct {
	Pool            pve.PVEInt   `json:"pool"`
	PoolName        string       `json:"pool_name"`
	Type            string       `json:"type"`
	Size            pve.PVEInt   `json:"size"`
	MinSize         pve.PVEInt   `json:"min_size"`
	PgNum           pve.PVEInt   `json:"pg_num"`
	PgNumFinal      pve.PVEInt   `json:"pg_num_final"`
	PgAutoscaleMode string       `json:"pg_autoscale_mode"`
	CrushRuleName   string       `json:"crush_rule_name"`
	BytesUsed       pve.PVEInt   `json:"bytes_used"`
	PercentUsed     pve.PVEFloat `json:"percent_used"`

	ApplicationMetadata map[string]any `json:"application_metadata"`
	AutoscaleStatus     struct {
		LogicalUsed pve.PVEInt `json:"logical_used"`
	} `json:"autoscale_status"`
}

// poolHeaders names what `ceph osd pool ls detail` and the PVE pool view
// report between them. RULE and APP are abbreviated because a pool name is
// what an operator scans this table for, and the long forms squeezed it.
var poolHeaders = []string{
	"NAME", "ID", "TYPE", "SIZE", "MIN-SIZE", "PG-NUM", "AUTOSCALE",
	"RULE", "APP", "STORED", "USED", "USE%",
}

// Pools renders the pool list as the columns an operator reads it for, with
// the autoscale report reduced to the number it exists to produce.
func Pools(resp any) (output.Result, error) {
	var pools []poolEntry
	payload, err := decode(resp, &pools)
	if err != nil {
		return output.Result{}, err
	}

	rows := make([][]string, 0, len(pools))
	for _, p := range pools {
		rows = append(rows, []string{
			p.PoolName,
			fmt.Sprintf("%d", pveInt(p.Pool)),
			p.Type,
			fmt.Sprintf("%d", pveInt(p.Size)),
			fmt.Sprintf("%d", pveInt(p.MinSize)),
			pgNumCell(p),
			p.PgAutoscaleMode,
			p.CrushRuleName,
			applicationCell(p),
			bytesCell(pveInt(p.AutoscaleStatus.LogicalUsed)),
			bytesCell(pveInt(p.BytesUsed)),
			percentCell(p.PercentUsed.Float()),
		})
	}
	return output.Result{Headers: poolHeaders, Rows: rows, Raw: payload}, nil
}

// pgNumCell names the placement-group count, and names the target too when
// the autoscaler is moving the pool towards a different one.
func pgNumCell(p poolEntry) string {
	num, final := pveInt(p.PgNum), pveInt(p.PgNumFinal)
	if final != 0 && final != num {
		return fmt.Sprintf("%d → %d", num, final)
	}
	return fmt.Sprintf("%d", num)
}

// applicationCell names the applications enabled on the pool. Ceph keys the
// map by application name and leaves the value an empty object, so the key is
// the whole of the information.
func applicationCell(p poolEntry) string {
	if len(p.ApplicationMetadata) == 0 {
		return ""
	}
	names := sortedKeys(p.ApplicationMetadata)
	sort.Strings(names)
	return strings.Join(names, ", ")
}
