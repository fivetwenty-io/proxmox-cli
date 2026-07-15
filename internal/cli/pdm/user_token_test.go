package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestTokenLs_SortsByTokenid asserts that `token ls` sorts entries by
// tokenid.
func TestTokenLs_SortsByTokenid(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/users/alpha@pam/token", []map[string]any{
		{"tokenid": "alpha@pam!zeta", "enable": true},
		{"tokenid": "alpha@pam!beta", "enable": false},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenLsCmd(), "ls", "alpha@pam")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha@pam!beta", got[0]["tokenid"], "entries must sort by tokenid")
	require.Equal(t, "alpha@pam!zeta", got[1]["tokenid"])
}

// TestTokenShow_RendersFields asserts that `token show` renders the token's
// metadata without a secret.
func TestTokenShow_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/users/alpha@pam/token/mytoken", map[string]any{
		"tokenid": "alpha@pam!mytoken", "enable": true, "comment": "ci token",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenShowCmd(), "show", "alpha@pam", "mytoken")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "alpha@pam!mytoken", got["tokenid"])
	require.NotContains(t, got, "value", "token show must never carry a secret")
}

// TestTokenAdd_DisplaysSecretOnce asserts that `token add` displays the
// returned secret in its output — the only time it is ever available.
func TestTokenAdd_DisplaysSecretOnce(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/access/users/alpha@pam/token/mytoken", &rec,
		map[string]any{"tokenid": "alpha@pam!mytoken", "value": "the-secret-value"})

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenAddCmd(), "add", "alpha@pam", "mytoken", "--comment", "ci token")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "ci token", rec.form.Get("comment"))
	require.Contains(t, buf.String(), "alpha@pam!mytoken")
	require.Contains(t, buf.String(), "the-secret-value", "the token secret must be displayed once on add")
}

// TestTokenUpdate_RejectsNoChanges asserts that `token update` refuses to
// issue a request when no flag was explicitly set.
func TestTokenUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenUpdateCmd(), "update", "alpha@pam", "mytoken")
	require.Error(t, err)
	require.ErrorContains(t, err, `update token "mytoken" for user "alpha@pam": no changes requested: pass at least one flag`)
}

// TestTokenUpdate_RegenerateDisplaysSecretOnce asserts that `token update
// --regenerate` displays the newly returned secret.
func TestTokenUpdate_RegenerateDisplaysSecretOnce(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/users/alpha@pam/token/mytoken", &rec,
		map[string]any{"tokenid": "alpha@pam!mytoken", "value": "the-new-secret"})

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenUpdateCmd(), "update", "alpha@pam", "mytoken", "--regenerate")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "1", rec.form.Get("regenerate"))
	require.Contains(t, buf.String(), "the-new-secret", "the new secret must be displayed once on regenerate")
}

// TestTokenUpdate_NonRegenerateShowsPlainMessage asserts that `token
// update` without --regenerate does not attempt to render an empty secret.
func TestTokenUpdate_NonRegenerateShowsPlainMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/users/alpha@pam/token/mytoken", &rec,
		map[string]any{"tokenid": "alpha@pam!mytoken", "value": nil})

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenUpdateCmd(), "update", "alpha@pam", "mytoken", "--comment", "updated")
	require.NoError(t, err)

	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Contains(t, buf.String(), `Token "mytoken" for user "alpha@pam" updated.`)
}

// TestTokenDelete_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `token delete` blocks the request entirely when unset.
func TestTokenDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenDeleteCmd(), "delete", "alpha@pam", "mytoken")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to delete token "mytoken" for user "alpha@pam" without confirmation: pass --yes/-y`)
}

// TestTokenDelete_SendsRequestWithConfirmation asserts that passing --yes
// issues the delete request.
func TestTokenDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/access/users/alpha@pam/token/mytoken", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTokenDeleteCmd(), "delete", "alpha@pam", "mytoken", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Token "mytoken" for user "alpha@pam" deleted.`)
}
