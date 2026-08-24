package capview

import (
	"encoding/json"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// cpuModel is one entry of GET /nodes/{node}/capabilities/qemu/cpu: a CPU
// model this node's QEMU can emulate, and so a value the guest's cpu= will
// take.
type cpuModel struct {
	Name   string `json:"name"`
	Vendor string `json:"vendor"`

	// Custom and Abstract are PVE's 0/1 capability flags, and they are
	// pointers because PVE omits abstract on the models that are not
	// abstract rather than sending it as 0. Decoding into a plain bool would
	// work, but the pointer says in the type what the flagCell comment says
	// in words: absent is one of the values these arrive in.
	Custom   *pve.PVEBool `json:"custom"`
	Abstract *pve.PVEBool `json:"abstract"`
}

// cpuModelHeaders leads with the model name, which is the value the operator
// came for and the one every other command wants back.
var cpuModelHeaders = []string{"NAME", "VENDOR", "CUSTOM", "ABSTRACT"}

// CPUModels renders the QEMU CPU models a node can emulate. The rows keep
// PVE's order, which pins the default model first.
func CPUModels(raws []json.RawMessage) (output.Result, error) {
	rows := make([][]string, 0, len(raws))
	for _, raw := range raws {
		var model cpuModel
		if err := json.Unmarshal(raw, &model); err != nil {
			return output.Result{}, err
		}
		rows = append(rows, []string{
			model.Name,
			model.Vendor,
			flagCell(model.Custom),
			flagCell(model.Abstract),
		})
	}
	return output.Result{Headers: cpuModelHeaders, Rows: rows, Raw: raws}, nil
}

// flagCell renders one of PVE's capability flags as a word.
//
// An absent flag reads as "no" here, unlike the absent list in CPUFlags: PVE
// sets these from a Perl hash and leaves the key out when the answer is no,
// so absence is one of the two answers rather than a sign the field went
// missing. A blank column would also say nothing at all on a node where none
// of the models is custom, which is every ordinary node.
func flagCell(v *pve.PVEBool) string {
	if v != nil && v.Bool() {
		return "yes"
	}
	return "no"
}
