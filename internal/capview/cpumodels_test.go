package capview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCPUModels_LeadsWithTheModelName pins the column order. The generic
// renderer sorted the keys alphabetically, which put an abstract flag that is
// set on four models out of a hundred ahead of the name.
func TestCPUModels_LeadsWithTheModelName(t *testing.T) {
	res, err := CPUModels(raws(t, `[
		{"custom":0,"name":"EPYC-Rome","vendor":"AuthenticAMD"}
	]`))
	require.NoError(t, err)
	require.Equal(t, []string{"NAME", "VENDOR", "CUSTOM", "ABSTRACT"}, res.Headers)
	require.Equal(t, [][]string{{"EPYC-Rome", "AuthenticAMD", "no", "no"}}, res.Rows)
}

// TestCPUModels_ReadsPVEsNumericFlags covers the encoding these arrive in:
// PVE sends 0 and 1, not true and false.
func TestCPUModels_ReadsPVEsNumericFlags(t *testing.T) {
	res, err := CPUModels(raws(t, `[
		{"abstract":1,"custom":0,"name":"x86-64-v3","vendor":"default"},
		{"custom":1,"name":"my-model","vendor":"default"}
	]`))
	require.NoError(t, err)
	require.Equal(t, [][]string{
		{"x86-64-v3", "default", "no", "yes"},
		{"my-model", "default", "yes", "no"},
	}, res.Rows)
}

// TestCPUModels_AbsentFlagReadsAsNo covers how PVE reports a flag that is off:
// it leaves the key out rather than sending a 0, so a blank cell there would
// be pmx failing to answer a question the payload does answer.
func TestCPUModels_AbsentFlagReadsAsNo(t *testing.T) {
	res, err := CPUModels(raws(t, `[{"name":"kvm64","vendor":"default"}]`))
	require.NoError(t, err)
	require.Equal(t, [][]string{{"kvm64", "default", "no", "no"}}, res.Rows)
}

// TestCPUModels_KeepsThePayloadOrder holds the listing to PVE's order, which
// leads with the default model. Sorting by name would bury it.
func TestCPUModels_KeepsThePayloadOrder(t *testing.T) {
	res, err := CPUModels(raws(t, `[
		{"custom":0,"name":"kvm64","vendor":"default"},
		{"custom":0,"name":"Cascadelake-Server","vendor":"GenuineIntel"},
		{"custom":0,"name":"EPYC","vendor":"AuthenticAMD"}
	]`))
	require.NoError(t, err)
	require.Equal(t, []string{"kvm64", "Cascadelake-Server", "EPYC"},
		[]string{res.Rows[0][0], res.Rows[1][0], res.Rows[2][0]})
}

// TestCPUModels_CarriesThePayloadForJSON keeps -o json answering with what PVE
// sent, including a key the table has no column for.
func TestCPUModels_CarriesThePayloadForJSON(t *testing.T) {
	doc := `[{"custom":0,"name":"kvm64","vendor":"default","future-key":"kept"}]`
	res, err := CPUModels(raws(t, doc))
	require.NoError(t, err)
	out, err := json.Marshal(res.Raw)
	require.NoError(t, err)
	require.JSONEq(t, doc, string(out))
}

// TestCPUModels_EmptyListRendersAnEmptyTable covers a node whose QEMU offers
// nothing for the requested architecture.
func TestCPUModels_EmptyListRendersAnEmptyTable(t *testing.T) {
	res, err := CPUModels(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"NAME", "VENDOR", "CUSTOM", "ABSTRACT"}, res.Headers)
	require.Empty(t, res.Rows)
}

// TestCPUModels_RejectsAMalformedEntry reports a payload it cannot read rather
// than rendering a row of blanks.
func TestCPUModels_RejectsAMalformedEntry(t *testing.T) {
	_, err := CPUModels([]json.RawMessage{json.RawMessage(`"not an object"`)})
	require.Error(t, err)
}
