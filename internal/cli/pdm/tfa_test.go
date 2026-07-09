package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestTfaLs_SortsByUseridWithPairedRaw asserts that `tfa ls` sorts entries
// by userid, with Rows and Raw paired together.
func TestTfaLs_SortsByUseridWithPairedRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/tfa", []map[string]any{
		{
			"userid": "zeta@pam", "totp-locked": false,
			"entries": []map[string]any{{"id": "totp1", "type": "totp", "description": "phone", "enable": true, "created": 100}},
		},
		{
			"userid": "alpha@pam", "totp-locked": true, "tfa-locked-until": 999,
			"entries": []map[string]any{},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTfaLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha@pam", got[0]["userid"], "entries must sort by userid")
	require.InEpsilon(t, float64(999), got[0]["tfa-locked-until"], 0.0001, "raw entry must correspond to the sorted user")
	require.Equal(t, "zeta@pam", got[1]["userid"])
}

// TestTfaShow_ListsUsersEntries asserts that `tfa show <userid>` lists the
// user's TFA entries sorted by id.
func TestTfaShow_ListsUsersEntries(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/tfa/alpha@pam", []map[string]any{
		{"id": "zeta1", "type": "totp", "description": "phone", "enable": true, "created": 100},
		{"id": "alpha1", "type": "webauthn", "description": "key", "enable": false, "created": 200},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTfaShowCmd(), "show", "alpha@pam")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha1", got[0]["id"], "entries must sort by id")
	require.Equal(t, "zeta1", got[1]["id"])
}

// TestTfaUpdate_RequiresDescription asserts that `tfa update` refuses to
// issue a request unless --description is explicitly set.
func TestTfaUpdate_RequiresDescription(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTfaUpdateCmd(), "update", "alpha@pam", "totp1")
	require.Error(t, err)
	require.ErrorContains(t, err, `update tfa entry "totp1" for user "alpha@pam": no changes requested: pass --description`)
}

// TestTfaUpdate_SendsDescriptionAndPassword asserts that `tfa update` sends
// --description and, when set, --password on the wire.
func TestTfaUpdate_SendsDescriptionAndPassword(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/tfa/alpha@pam/totp1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTfaUpdateCmd(), "update", "alpha@pam", "totp1",
		"--description", "new description", "--password", "my-password")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "new description", rec.form.Get("description"))
	require.Equal(t, "my-password", rec.form.Get("password"))
	require.Contains(t, buf.String(), `TFA entry "totp1" for user "alpha@pam" updated.`)
}

// TestTfaDelete_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `tfa delete` blocks the request entirely when unset.
func TestTfaDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTfaDeleteCmd(), "delete", "alpha@pam", "totp1")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to delete tfa entry "totp1" for user "alpha@pam" without confirmation: pass --yes/-y`)
}

// TestTfaDelete_SendsRequestWithConfirmation asserts that passing --yes
// issues the delete request.
func TestTfaDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/access/tfa/alpha@pam/totp1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTfaDeleteCmd(), "delete", "alpha@pam", "totp1", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `TFA entry "totp1" for user "alpha@pam" deleted.`)
}
