// Package capview turns the QEMU capability payloads PVE reports into tables
// an operator can read.
//
// These endpoints answer with a list of objects, and the generic renderer can
// only union their keys and sort them alphabetically. That buries the name of
// the thing behind a sentence-long description, and it renders a field that is
// empty the same way it renders one that never arrived. The views here name
// their columns and order them, and leave the payload to -o json, which still
// carries it verbatim.
package capview

import (
	"encoding/json"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// cpuFlag is one entry of GET /nodes/{node}/capabilities/qemu/cpu-flags, and
// of the cluster-wide list, which answers with the same shape.
type cpuFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	// SupportedOn is a pointer because an empty list and an absent field mean
	// different things here. PVE sends [] for a flag none of the nodes offers,
	// which is the whole answer on a cluster whose CPUs expose no flags at
	// all; the field goes missing only if the endpoint stopped reporting it.
	SupportedOn *[]string `json:"supported-on"`
}

// cpuFlagHeaders leads with the flag's name, which is what the operator came
// to read and what every other command wants back.
var cpuFlagHeaders = []string{"NAME", "SUPPORTED ON", "DESCRIPTION"}

// CPUFlags renders the QEMU CPU flags a node or a cluster supports. The rows
// keep the order PVE sent, which pins nested-virt first and groups the rest.
func CPUFlags(raws []json.RawMessage) (output.Result, error) {
	rows := make([][]string, 0, len(raws))
	for _, raw := range raws {
		var flag cpuFlag
		if err := json.Unmarshal(raw, &flag); err != nil {
			return output.Result{}, err
		}
		rows = append(rows, []string{flag.Name, supportedOnCell(flag), flag.Description})
	}
	return output.Result{Headers: cpuFlagHeaders, Rows: rows, Raw: raws}, nil
}

// supportedOnCell names the nodes that offer a flag, and says so in as many
// words when none of them do. A cluster of nested nodes offers no flags at
// all, and a table of blank cells there reads as a column pmx failed to fill
// rather than as an answer.
func supportedOnCell(flag cpuFlag) string {
	if flag.SupportedOn == nil {
		return ""
	}
	if len(*flag.SupportedOn) == 0 {
		return "none"
	}
	return strings.Join(*flag.SupportedOn, ", ")
}
