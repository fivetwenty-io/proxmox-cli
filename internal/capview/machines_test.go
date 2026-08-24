package capview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMachines_LeadsWithTheMachineID pins the column order. The generic
// renderer sorted the keys alphabetically, which put a sentence-long changelog
// ahead of the id the guest config actually takes.
func TestMachines_LeadsWithTheMachineID(t *testing.T) {
	res, err := Machines(raws(t, `[
		{"changes":"Enable hv_emcs for Windows 11.","id":"pc-q35-11.0+pve2",
		 "type":"q35","version":"11.0+pve2"}
	]`))
	require.NoError(t, err)
	require.Equal(t, []string{"ID", "TYPE", "VERSION", "CHANGES"}, res.Headers)
	require.Equal(t, [][]string{{
		"pc-q35-11.0+pve2", "q35", "11.0+pve2", "Enable hv_emcs for Windows 11.",
	}}, res.Rows)
}

// TestMachines_StockVersionHasNoChanges covers the common row: only the +pveN
// versions carry a changelog, and the rest send no key at all.
func TestMachines_StockVersionHasNoChanges(t *testing.T) {
	res, err := Machines(raws(t, `[{"id":"pc-i440fx-11.0","type":"i440fx","version":"11.0"}]`))
	require.NoError(t, err)
	require.Equal(t, [][]string{{"pc-i440fx-11.0", "i440fx", "11.0", ""}}, res.Rows)
}

// TestMachines_KeepsThePayloadOrder holds the listing to PVE's order, which is
// newest version first. Sorting by id would interleave the two machine types
// and scatter the versions.
func TestMachines_KeepsThePayloadOrder(t *testing.T) {
	res, err := Machines(raws(t, `[
		{"id":"pc-i440fx-11.0","type":"i440fx","version":"11.0"},
		{"id":"pc-q35-11.0","type":"q35","version":"11.0"},
		{"id":"pc-i440fx-10.2","type":"i440fx","version":"10.2"}
	]`))
	require.NoError(t, err)
	require.Equal(t, []string{"pc-i440fx-11.0", "pc-q35-11.0", "pc-i440fx-10.2"},
		[]string{res.Rows[0][0], res.Rows[1][0], res.Rows[2][0]})
}

// TestMachines_CarriesThePayloadForJSON keeps -o json answering with what PVE
// sent, including a key the table has no column for.
func TestMachines_CarriesThePayloadForJSON(t *testing.T) {
	doc := `[{"id":"pc-q35-11.0","type":"q35","version":"11.0","future-key":"kept"}]`
	res, err := Machines(raws(t, doc))
	require.NoError(t, err)
	out, err := json.Marshal(res.Raw)
	require.NoError(t, err)
	require.JSONEq(t, doc, string(out))
}

// TestMachines_EmptyListRendersAnEmptyTable covers a node whose QEMU offers
// nothing for the requested architecture.
func TestMachines_EmptyListRendersAnEmptyTable(t *testing.T) {
	res, err := Machines(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"ID", "TYPE", "VERSION", "CHANGES"}, res.Headers)
	require.Empty(t, res.Rows)
}

// TestMachines_RejectsAMalformedEntry reports a payload it cannot read rather
// than rendering a row of blanks.
func TestMachines_RejectsAMalformedEntry(t *testing.T) {
	_, err := Machines([]json.RawMessage{json.RawMessage(`["not","an","object"]`)})
	require.Error(t, err)
}
