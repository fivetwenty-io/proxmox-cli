package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestACLLs_SortsByPathThenUgidAndSendsPathFilter asserts that `acl ls`
// sorts entries by (path, ugid) with Rows and Raw paired, and forwards
// --path to the request.
func TestACLLs_SortsByPathThenUgidAndSendsPathFilter(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/access/acl", &rec, []map[string]any{
		{"path": "/vms", "ugid": "zeta@pam", "ugid_type": "user", "roleid": "PVEAdmin", "propagate": true},
		{"path": "/vms", "ugid": "alpha@pam", "ugid_type": "user", "roleid": "PVEAuditor", "propagate": false},
		{"path": "/storage", "ugid": "alpha@pam", "ugid_type": "user", "roleid": "PVEAdmin"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newACLLsCmd(), "ls", "--path", "/vms")
	require.NoError(t, err)

	require.Equal(t, "/vms", rec.query.Get("path"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 3)
	require.Equal(t, "/storage", got[0]["path"], "entries must sort by path first")
	require.Equal(t, "/vms", got[1]["path"])
	require.Equal(t, "alpha@pam", got[1]["ugid"], "then by ugid within the same path")
	require.Equal(t, "/vms", got[2]["path"])
	require.Equal(t, "zeta@pam", got[2]["ugid"])
}

// TestACLUpdate_RequiresAuthIdOrGroup asserts that `acl update` refuses to
// issue a request unless exactly one of --auth-id or --group is set.
func TestACLUpdate_RequiresAuthIdOrGroup(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newACLUpdateCmd(), "update", "--path", "/vms", "--role", "PVEAdmin")
	require.Error(t, err)
	require.ErrorContains(t, err, "one of --auth-id or --group is required")
}

// TestACLUpdate_RejectsBothAuthIdAndGroup asserts that `acl update` refuses
// to issue a request when both --auth-id and --group are set.
func TestACLUpdate_RejectsBothAuthIdAndGroup(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newACLUpdateCmd(), "update",
		"--path", "/vms", "--role", "PVEAdmin", "--auth-id", "alpha@pam", "--group", "admins")
	require.Error(t, err)
	require.ErrorContains(t, err, "--auth-id and --group are mutually exclusive")
}

// TestACLUpdate_GrantsRole asserts that `acl update` sends a grant request
// with the expected form fields.
func TestACLUpdate_GrantsRole(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/acl", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newACLUpdateCmd(), "update",
		"--path", "/vms", "--role", "PVEAdmin", "--auth-id", "alpha@pam")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "/vms", rec.form.Get("path"))
	require.Equal(t, "PVEAdmin", rec.form.Get("role"))
	require.Equal(t, "alpha@pam", rec.form.Get("auth-id"))
	require.NotContains(t, rec.form, "delete")
	require.Contains(t, buf.String(), `Role "PVEAdmin" granted on "/vms".`)
}

// TestACLUpdate_RevokesRole asserts that `acl update --delete` sends a
// revoke request and reports "revoked" instead of "granted".
func TestACLUpdate_RevokesRole(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/acl", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newACLUpdateCmd(), "update",
		"--path", "/vms", "--role", "PVEAdmin", "--group", "admins", "--delete")
	require.NoError(t, err)

	require.Equal(t, "1", rec.form.Get("delete"))
	require.Equal(t, "admins", rec.form.Get("group"))
	require.Contains(t, buf.String(), `Role "PVEAdmin" revoked on "/vms".`)
}
