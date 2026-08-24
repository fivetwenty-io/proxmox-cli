package capview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// raws turns a JSON array literal into the raw element list the SDK hands the
// views, so a fixture reads as the payload PVE sends.
func raws(t *testing.T, doc string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(doc), &out))
	return out
}

// TestCPUFlags_LeadsWithTheFlagName pins the column order. The generic
// renderer sorted the keys alphabetically, which put the description first and
// pushed the flag name into the middle of the table.
func TestCPUFlags_LeadsWithTheFlagName(t *testing.T) {
	res, err := CPUFlags(raws(t, `[
		{"name":"aes","description":"Activate AES instruction set for HW acceleration.",
		 "supported-on":["pve1","pve2"]}
	]`))
	require.NoError(t, err)
	require.Equal(t, []string{"NAME", "SUPPORTED ON", "DESCRIPTION"}, res.Headers)
	require.Equal(t, [][]string{{
		"aes", "pve1, pve2", "Activate AES instruction set for HW acceleration.",
	}}, res.Rows)
}

// TestCPUFlags_UnsupportedFlagSaysSo is the case that a cluster of nested
// nodes answers with for every flag it knows: the list is empty, and the
// column has to read as an answer rather than as a column pmx failed to fill.
func TestCPUFlags_UnsupportedFlagSaysSo(t *testing.T) {
	res, err := CPUFlags(raws(t, `[
		{"name":"md-clear","description":"MDS mitigation","supported-on":[]}
	]`))
	require.NoError(t, err)
	require.Equal(t, [][]string{{"md-clear", "none", "MDS mitigation"}}, res.Rows)
}

// TestCPUFlags_MissingFieldStaysBlank keeps the always-blank column the render
// audit looks for. An empty list is an answer and reads as "none"; a field the
// endpoint never sent is not, and has to stay blank so the audit still catches
// the day this key changes name.
func TestCPUFlags_MissingFieldStaysBlank(t *testing.T) {
	res, err := CPUFlags(raws(t, `[{"name":"pcid","description":"Meltdown fix"}]`))
	require.NoError(t, err)
	require.Equal(t, [][]string{{"pcid", "", "Meltdown fix"}}, res.Rows)
}

// TestCPUFlags_KeepsThePayloadOrder holds the rows where PVE put them: it pins
// nested-virt first and sorts the rest itself, and re-sorting here would only
// bury the flag it meant to lead with.
func TestCPUFlags_KeepsThePayloadOrder(t *testing.T) {
	res, err := CPUFlags(raws(t, `[
		{"name":"nested-virt","supported-on":[]},
		{"name":"aes","supported-on":[]},
		{"name":"amd-ssbd","supported-on":[]}
	]`))
	require.NoError(t, err)
	require.Equal(t, []string{"nested-virt", "aes", "amd-ssbd"},
		[]string{res.Rows[0][0], res.Rows[1][0], res.Rows[2][0]})
}

// TestCPUFlags_CarriesThePayloadForJSON verifies -o json still answers with
// what PVE sent, including the fields the table leaves out.
func TestCPUFlags_CarriesThePayloadForJSON(t *testing.T) {
	in := raws(t, `[{"name":"aes","supported-on":["pve1"],"pve-flag":1}]`)
	res, err := CPUFlags(in)
	require.NoError(t, err)
	got, err := json.Marshal(res.Raw)
	require.NoError(t, err)
	require.JSONEq(t, `[{"name":"aes","supported-on":["pve1"],"pve-flag":1}]`, string(got))
}

// TestCPUFlags_EmptyListRendersAnEmptyTable covers an --arch nothing matches.
func TestCPUFlags_EmptyListRendersAnEmptyTable(t *testing.T) {
	res, err := CPUFlags(nil)
	require.NoError(t, err)
	require.Empty(t, res.Rows)
	require.Equal(t, []string{"NAME", "SUPPORTED ON", "DESCRIPTION"}, res.Headers)
}

// TestCPUFlags_RejectsAMalformedEntry reports a decode failure rather than
// rendering a row of blanks.
func TestCPUFlags_RejectsAMalformedEntry(t *testing.T) {
	_, err := CPUFlags([]json.RawMessage{json.RawMessage(`"aes"`)})
	require.Error(t, err)
}
