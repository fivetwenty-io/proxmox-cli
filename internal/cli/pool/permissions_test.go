package pool

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestPoolPermissionsList_ExactPath verifies `permissions list` filters the
// full ACL list down to the exact /pool/{poolid} path (singular) by default.
func TestPoolPermissionsList_ExactPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/pool/prod", "type": "user", "ugid": "alice@pve", "roleid": "PVEPoolAdmin", "propagate": 1},
		map[string]any{"path": "/pool/dev", "type": "user", "ugid": "bob@pve", "roleid": "PVEPoolUser", "propagate": 0},
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
	})

	out, err := run(t, f, "", "permissions", "list", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "alice@pve")
	require.Contains(t, out, "PVEPoolAdmin")
	require.NotContains(t, out, "bob@pve")
	require.NotContains(t, out, "admins")
}

// TestPoolPermissionsList_Inherited verifies --inherited unions matches
// across the client-side parent chain (/, /pool, /pool/prod).
func TestPoolPermissionsList_Inherited(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/pool/prod", "type": "user", "ugid": "alice@pve", "roleid": "PVEPoolAdmin", "propagate": 1},
		map[string]any{"path": "/pool", "type": "group", "ugid": "pool-ops", "roleid": "PVEPoolUser", "propagate": 1},
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
		map[string]any{"path": "/pool/dev", "type": "user", "ugid": "bob@pve", "roleid": "PVEPoolUser", "propagate": 0},
	})

	out, err := run(t, f, "", "permissions", "list", "prod", "--inherited")
	require.NoError(t, err)
	require.Contains(t, out, "alice@pve")
	require.Contains(t, out, "pool-ops")
	require.Contains(t, out, "admins")
	require.Contains(t, out, "INHERITED") // exact header spacing is a tablewriter concern, not asserted here
	require.NotContains(t, out, "bob@pve")
}

// TestPoolPermissionsGrant_PutsSingularPath is the dedicated regression case
// pinning the ACL path grammar: the request body's path must be exactly
// "/pool/prod", never "/pools/prod" (the API/command noun is plural, the ACL
// path segment is singular — see the doc comment on poolACLPath).
func TestPoolPermissionsGrant_PutsSingularPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/access/acl", nil, 200)

	out, err := run(t, f, "", "permissions", "grant", "prod",
		"--roles", "PVEPoolAdmin",
		"--users", "alice@pve",
	)
	require.NoError(t, err)
	require.Len(t, rec, 1)

	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/api2/json/access/acl", rec[0].path)
	require.Equal(t, "/pool/prod", rec[0].body["path"], "ACL path must be singular /pool/, not /pools/")
	require.NotEqual(t, "/pools/prod", rec[0].body["path"])
	require.Equal(t, "PVEPoolAdmin", rec[0].body["roles"])
	require.Equal(t, "alice@pve", rec[0].body["users"])
	require.NotContains(t, rec[0].body, "delete", "grant must not send delete")

	require.Contains(t, out, "Granted")
	require.Contains(t, out, "/pool/prod")
}

// TestPoolPermissionsGrant_NoPropagateSendsFalse verifies --no-propagate
// forwards propagate=0 explicitly rather than leaving it unset.
func TestPoolPermissionsGrant_NoPropagateSendsFalse(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/access/acl", nil, 200)

	_, err := run(t, f, "", "permissions", "grant", "prod",
		"--roles", "PVEPoolUser",
		"--groups", "pool-ops",
		"--no-propagate",
	)
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "0", rec[0].body["propagate"])
}

// TestPoolPermissionsGrant_RequiresSubjectFlag verifies grant/revoke refuse to
// contact the server when none of --users/--groups/--tokens is set.
func TestPoolPermissionsGrant_RequiresSubjectFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/access/acl", nil, 200)

	_, err := run(t, f, "", "permissions", "grant", "prod", "--roles", "PVEPoolUser")
	require.Error(t, err)
	require.Empty(t, rec, "no request must be made without at least one subject flag")
}

// TestPoolPermissionsRevoke_SetsDeleteFlag verifies `permissions revoke` sends
// delete=1 alongside the singular pool path and requested roles/tokens.
func TestPoolPermissionsRevoke_SetsDeleteFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/access/acl", nil, 200)

	out, err := run(t, f, "", "permissions", "revoke", "prod",
		"--roles", "PVEPoolAdmin",
		"--tokens", "root@pam!ci",
	)
	require.NoError(t, err)
	require.Len(t, rec, 1)

	require.Equal(t, "/pool/prod", rec[0].body["path"])
	require.Equal(t, "PVEPoolAdmin", rec[0].body["roles"])
	require.Equal(t, "root@pam!ci", rec[0].body["tokens"])
	require.Equal(t, "1", rec[0].body["delete"])
	require.Contains(t, out, "Revoked")
}

// TestPoolPermissionsEffective_UseridPassthrough verifies `permissions
// effective` queries GET /access/permissions with the singular pool path and,
// when --userid is passed, forwards it unvalidated via the query string.
func TestPoolPermissionsEffective_UseridPassthrough(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/access/permissions", map[string]any{
		"/pool/prod": map[string]any{"Pool.Audit": 1, "Pool.Allocate": 1},
	}, 200)

	out, err := run(t, f, "", "permissions", "effective", "prod", "--userid", "alice@pve")
	require.NoError(t, err)
	require.Len(t, rec, 1)

	require.Equal(t, http.MethodGet, rec[0].method)
	require.Contains(t, rec[0].query, "path=%2Fpool%2Fprod")
	require.Contains(t, rec[0].query, "userid=alice%40pve")
	require.Contains(t, out, "/pool/prod")
	require.Contains(t, out, "Pool.Audit")
}

// TestPoolPermissionsEffective_ServerError verifies an API failure on the
// effective-permissions read is surfaced.
func TestPoolPermissionsEffective_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/access/permissions", nil, 403)

	_, err := run(t, f, "", "permissions", "effective", "prod")
	require.Error(t, err)
}
