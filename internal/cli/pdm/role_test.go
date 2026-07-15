package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestRoleLs_SortsByRoleidWithPairedRaw asserts that `role ls` sorts
// entries by roleid, with Rows and Raw paired together.
func TestRoleLs_SortsByRoleidWithPairedRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/roles", []map[string]any{
		{"roleid": "PVEVMAdmin", "privs": []string{"VM.Audit", "VM.Config.Disk"}},
		{"roleid": "Administrator", "privs": []string{"Sys.Modify"}, "comment": "built-in"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRoleLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "Administrator", got[0]["roleid"], "entries must sort by roleid")
	require.Equal(t, "built-in", got[0]["comment"], "raw entry must correspond to the sorted role")
	require.Equal(t, "PVEVMAdmin", got[1]["roleid"])
}
