package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestUserLs_SortsByUserid asserts that `user ls` sorts entries by userid
// and pairs each Raw entry with its corresponding row.
func TestUserLs_SortsByUserid(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/users", []map[string]any{
		{"userid": "zeta@pam", "enable": true, "email": "zeta@example.com"},
		{"userid": "alpha@pam", "enable": false, "email": "alpha@example.com"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newUserLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha@pam", got[0]["userid"], "entries must sort by userid")
	require.Equal(t, "zeta@pam", got[1]["userid"])
}

// TestUserShow_RendersFields asserts that `user show` renders the user's
// populated configuration fields.
func TestUserShow_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/users/alpha@pam", map[string]any{
		"userid": "alpha@pam", "enable": true, "email": "alpha@example.com",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newUserShowCmd(), "show", "alpha@pam")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "alpha@pam", got["userid"])
	require.Equal(t, "alpha@example.com", got["email"])
}

// TestUserAdd_SendsParamsAndNeverEchoesPassword asserts that `user add`
// encodes every flag onto the expected form field names and that the
// password is never echoed back in the command's own output.
func TestUserAdd_SendsParamsAndNeverEchoesPassword(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/access/users", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newUserAddCmd(), "add", "alpha@pam",
		"--password", "s3cr3t-pw", "--email", "alpha@example.com", "--enable=false")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "alpha@pam", rec.form.Get("userid"))
	require.Equal(t, "s3cr3t-pw", rec.form.Get("password"))
	require.Equal(t, "alpha@example.com", rec.form.Get("email"))
	require.Equal(t, "0", rec.form.Get("enable"))
	require.Contains(t, buf.String(), `User "alpha@pam" created.`)
	require.NotContains(t, buf.String(), "s3cr3t-pw", "password must never be echoed back")
}

// TestUserUpdate_RejectsNoChanges asserts that `user update` refuses to
// issue a request when no flag was explicitly set.
func TestUserUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newUserUpdateCmd(), "update", "alpha@pam")
	require.Error(t, err)
	require.ErrorContains(t, err, `update user "alpha@pam": no changes requested: pass at least one flag`)
}

// TestUserUpdate_SendsChangedFlagsOnly asserts that `user update` sends
// only the flags explicitly set, leaving unset fields off the wire.
func TestUserUpdate_SendsChangedFlagsOnly(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/users/alpha@pam", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newUserUpdateCmd(), "update", "alpha@pam", "--comment", "updated")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.NotContains(t, rec.form, "email")
	require.NotContains(t, rec.form, "firstname")
	require.Contains(t, buf.String(), `User "alpha@pam" updated.`)
}

// TestUserDelete_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `user delete` blocks the request entirely when unset.
func TestUserDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newUserDeleteCmd(), "delete", "alpha@pam")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to delete user "alpha@pam" without confirmation: pass --yes/-y`)
}

// TestUserDelete_SendsRequestWithConfirmation asserts that passing --yes
// issues the delete request.
func TestUserDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/access/users/alpha@pam", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newUserDeleteCmd(), "delete", "alpha@pam", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `User "alpha@pam" deleted.`)
}
