package capview

import (
	"encoding/json"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// machine is one entry of GET /nodes/{node}/capabilities/qemu/machines: a
// machine type this node's QEMU can emulate, and so a value the guest's
// machine= will take.
type machine struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Version string `json:"version"`

	// Changes is what a PVE-patched machine version alters against the QEMU
	// version it was cut from. Only the +pveN versions carry one; a stock
	// QEMU version has nothing to say and sends no key.
	Changes string `json:"changes"`
}

// machineHeaders lead with the id, which is the string the guest config takes.
// The changes go last because they are a sentence and everything else is a
// word.
var machineHeaders = []string{"ID", "TYPE", "VERSION", "CHANGES"}

// Machines renders the QEMU machine types a node can emulate. The rows keep
// PVE's order, which is newest version first.
func Machines(raws []json.RawMessage) (output.Result, error) {
	rows := make([][]string, 0, len(raws))
	for _, raw := range raws {
		var m machine
		if err := json.Unmarshal(raw, &m); err != nil {
			return output.Result{}, err
		}
		rows = append(rows, []string{m.ID, m.Type, m.Version, m.Changes})
	}
	return output.Result{Headers: machineHeaders, Rows: rows, Raw: raws}, nil
}
