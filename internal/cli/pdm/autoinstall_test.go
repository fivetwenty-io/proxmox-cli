package pdm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// --- installation ------------------------------------------------------------

// TestAutoInstallInstallationLs_SortsByUuid asserts that `installation ls`
// sorts entries by uuid and strips post-hook-token defensively.
func TestAutoInstallInstallationLs_SortsByUuid(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/auto-install/installations", []map[string]any{
		{"uuid": "zzzzzzzz-0000-0000-0000-000000000000", "status": "finished", "received-at": 200},
		{
			"uuid": "aaaaaaaa-0000-0000-0000-000000000000", "status": "in-progress", "received-at": 100,
			"post-hook-token": "leak-me-not",
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallInstallationLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "aaaaaaaa-0000-0000-0000-000000000000", got[0]["uuid"], "entries must sort by uuid")
	require.Equal(t, "zzzzzzzz-0000-0000-0000-000000000000", got[1]["uuid"])
	require.NotContains(t, got[0], "post-hook-token", "installation ls must never carry the post-hook token")
}

// TestAutoInstallInstallationDelete_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `installation delete` blocks the request entirely when unset.
func TestAutoInstallInstallationDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallInstallationDeleteCmd(), "delete", "aaaaaaaa-0000-0000-0000-000000000000")
	require.Error(t, err)
	require.ErrorContains(t, err,
		`refusing to delete installation "aaaaaaaa-0000-0000-0000-000000000000" without confirmation: pass --yes/-y`)
}

// TestAutoInstallInstallationDelete_SendsRequestWithConfirmation asserts that
// passing --yes issues the delete request.
func TestAutoInstallInstallationDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/auto-install/installations/aaaaaaaa-0000-0000-0000-000000000000", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallInstallationDeleteCmd(),
		"delete", "aaaaaaaa-0000-0000-0000-000000000000", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Installation "aaaaaaaa-0000-0000-0000-000000000000" deleted.`)
}

// --- prepared ------------------------------------------------------------

// TestAutoInstallPreparedLs_SortsById asserts that `prepared ls` sorts
// entries by id.
func TestAutoInstallPreparedLs_SortsById(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/auto-install/prepared", []map[string]any{
		{"id": "zeta", "country": "us", "disk-mode": "disk_list", "fqdn": "zeta.example.com",
			"reboot-mode": "reboot", "timezone": "UTC"},
		{"id": "alpha", "country": "de", "disk-mode": "disk_filter", "fqdn": "alpha.example.com",
			"reboot-mode": "power-off", "timezone": "Europe/Berlin"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["id"], "entries must sort by id")
	require.Equal(t, "zeta", got[1]["id"])
}

// TestAutoInstallPreparedShow_FindsEntryInList asserts that `prepared show`
// decodes the typed GetPrepared response for the given id.
func TestAutoInstallPreparedShow_FindsEntryInList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/auto-install/prepared/beta", map[string]any{
		"id": "beta", "country": "us", "fqdn": "beta.example.com",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedShowCmd(), "show", "beta")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "beta", got["id"])
	require.Equal(t, "us", got["country"])
}

// TestAutoInstallPreparedShow_NotFound asserts that `prepared show` surfaces
// the server's error when no entry with the given id exists.
func TestAutoInstallPreparedShow_NotFound(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/auto-install/prepared/missing", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such prepared answer")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedShowCmd(), "show", "missing")
	require.Error(t, err)
	require.ErrorContains(t, err, `get prepared answer "missing"`)
}

// TestAutoInstallPreparedAdd_SendsRequiredFieldsAndJSONFlags asserts that
// `prepared add` sends the required scalar fields as form values and the
// --filesystem JSON-text flag verbatim (unmangled by the form encoder).
func TestAutoInstallPreparedAdd_SendsRequiredFieldsAndJSONFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/auto-install/prepared", &rec,
		map[string]any{"config": map[string]any{"id": "web01"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedAddCmd(), "add", "web01",
		"--country", "de",
		"--disk-mode", "disk_list",
		"--fqdn", "web01.example.com",
		"--keyboard", "de",
		"--mailto", "admin@example.com",
		"--timezone", "Europe/Berlin",
		"--filesystem", `{"type":"ext4"}`,
		"--gateway", "192.0.2.1",
	)
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "web01", rec.form.Get("id"))
	require.Equal(t, "de", rec.form.Get("country"))
	require.Equal(t, "disk_list", rec.form.Get("disk-mode"))
	require.Equal(t, "web01.example.com", rec.form.Get("fqdn"))
	require.Equal(t, `{"type":"ext4"}`, rec.form.Get("filesystem"), "JSON-text flags must be sent unmangled")
	require.Equal(t, "reboot", rec.form.Get("reboot-mode"), "reboot-mode default must always be sent")
	require.Equal(t, "0", rec.form.Get("reboot-on-error"), "required booleans must always be sent")
	require.Equal(t, "192.0.2.1", rec.form.Get("gateway"), "--gateway must reach the server")
	require.Contains(t, buf.String(), `Prepared answer "web01" created.`)
}

// TestAutoInstallPreparedAdd_RejectsInvalidJSON asserts that `prepared add`
// validates --filesystem as JSON before issuing any request.
func TestAutoInstallPreparedAdd_RejectsInvalidJSON(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedAddCmd(), "add", "web01",
		"--country", "de",
		"--disk-mode", "disk_list",
		"--fqdn", "web01.example.com",
		"--keyboard", "de",
		"--mailto", "admin@example.com",
		"--timezone", "Europe/Berlin",
		"--filesystem", `not-json`,
	)
	require.Error(t, err)
	require.ErrorContains(t, err, `add prepared answer "web01": --filesystem is not valid JSON`)
}

// TestAutoInstallPreparedAdd_RejectsInvalidRebootMode asserts that `prepared
// add` validates --reboot-mode against the enum.
func TestAutoInstallPreparedAdd_RejectsInvalidRebootMode(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedAddCmd(), "add", "web01",
		"--country", "de",
		"--disk-mode", "disk_list",
		"--fqdn", "web01.example.com",
		"--keyboard", "de",
		"--mailto", "admin@example.com",
		"--timezone", "Europe/Berlin",
		"--filesystem", `{"type":"ext4"}`,
		"--reboot-mode", "bogus",
	)
	require.Error(t, err)
	require.ErrorContains(t, err, `add prepared answer "web01": --reboot-mode must be one of reboot, power-off (got "bogus")`)
}

// TestAutoInstallPreparedUpdate_RejectsNoChanges asserts that `prepared
// update` refuses to issue a request when no flag was explicitly set.
func TestAutoInstallPreparedUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedUpdateCmd(), "update", "web01")
	require.Error(t, err)
	require.ErrorContains(t, err, `update prepared answer "web01": no changes requested: pass at least one flag`)
}

// TestAutoInstallPreparedUpdate_SendsOnlyChangedFields asserts that
// `prepared update` sends only explicitly-set fields, including a JSON-text
// flag unmangled.
func TestAutoInstallPreparedUpdate_SendsOnlyChangedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/auto-install/prepared/web01", &rec,
		map[string]any{"config": map[string]any{"id": "web01"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedUpdateCmd(), "update", "web01",
		"--target-filter", `{"/product":"pve*"}`,
		"--gateway", "192.0.2.1",
	)
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, `{"/product":"pve*"}`, rec.form.Get("target-filter"), "JSON-text flags must be sent unmangled")
	require.Equal(t, "192.0.2.1", rec.form.Get("gateway"), "--gateway must reach the server")
	require.Empty(t, rec.form.Get("country"), "unset fields must not be sent")
	require.Contains(t, buf.String(), `Prepared answer "web01" updated.`)
}

// TestAutoInstallPreparedDelete_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `prepared delete` blocks the request entirely when unset.
func TestAutoInstallPreparedDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedDeleteCmd(), "delete", "web01")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to delete prepared answer "web01" without confirmation: pass --yes/-y`)
}

// TestAutoInstallPreparedDelete_SendsRequestWithConfirmation asserts that
// passing --yes issues the delete request.
func TestAutoInstallPreparedDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/auto-install/prepared/web01", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedDeleteCmd(), "delete", "web01", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Prepared answer "web01" deleted.`)
}

// --- token ------------------------------------------------------------

// TestAutoInstallTokenLs_SortsById asserts that `token ls` sorts entries by
// id and strips the secret field defensively.
func TestAutoInstallTokenLs_SortsById(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/auto-install/tokens", []map[string]any{
		{"id": "zeta", "created-by": "root@pam", "enabled": true},
		{"id": "alpha", "created-by": "root@pam", "enabled": false, "secret": "leak-me-not"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallTokenLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["id"], "entries must sort by id")
	require.Equal(t, "zeta", got[1]["id"])
	require.NotContains(t, got[0], "secret", "token ls must never carry a secret")
}

// TestAutoInstallTokenAdd_DisplaysSecretOnce asserts that `token add`
// displays the returned secret in its output.
func TestAutoInstallTokenAdd_DisplaysSecretOnce(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/auto-install/tokens", &rec,
		map[string]any{"secret": "the-secret-value", "token": map[string]any{"id": "mytoken", "created-by": "root@pam"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallTokenAddCmd(), "add", "mytoken", "--comment", "ci token")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "mytoken", rec.form.Get("id"))
	require.Equal(t, "ci token", rec.form.Get("comment"))
	require.Contains(t, buf.String(), "mytoken")
	require.Contains(t, buf.String(), "the-secret-value", "the token secret must be displayed once on add")
}

// TestAutoInstallTokenUpdate_RejectsNoChanges asserts that `token update`
// refuses to issue a request when no flag was explicitly set.
func TestAutoInstallTokenUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallTokenUpdateCmd(), "update", "mytoken")
	require.Error(t, err)
	require.ErrorContains(t, err, `update token "mytoken": no changes requested: pass at least one flag`)
}

// TestAutoInstallTokenUpdate_RegenerateDisplaysSecretOnce asserts that
// `token update --regenerate` displays the newly returned secret.
func TestAutoInstallTokenUpdate_RegenerateDisplaysSecretOnce(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/auto-install/tokens/mytoken", &rec,
		map[string]any{"secret": "the-new-secret", "token": map[string]any{"id": "mytoken", "created-by": "root@pam"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallTokenUpdateCmd(), "update", "mytoken", "--regenerate")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "1", rec.form.Get("regenerate-secret"))
	require.Contains(t, buf.String(), "the-new-secret", "the new secret must be displayed once on regenerate")
}

// TestAutoInstallTokenUpdate_NonRegenerateShowsPlainMessage asserts that
// `token update` without --regenerate does not attempt to render an empty secret.
func TestAutoInstallTokenUpdate_NonRegenerateShowsPlainMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/auto-install/tokens/mytoken", &rec,
		map[string]any{"token": map[string]any{"id": "mytoken", "created-by": "root@pam"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallTokenUpdateCmd(), "update", "mytoken", "--comment", "updated")
	require.NoError(t, err)

	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Contains(t, buf.String(), `Token "mytoken" updated.`)
}

// TestAutoInstallTokenDelete_RefusesWithoutConfirmation asserts the --yes/-y
// gate on `token delete` blocks the request entirely when unset.
func TestAutoInstallTokenDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallTokenDeleteCmd(), "delete", "mytoken")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to delete token "mytoken" without confirmation: pass --yes/-y`)
}

// TestAutoInstallTokenDelete_SendsRequestWithConfirmation asserts that
// passing --yes issues the delete request.
func TestAutoInstallTokenDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/auto-install/tokens/mytoken", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallTokenDeleteCmd(), "delete", "mytoken", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Token "mytoken" deleted.`)
}
