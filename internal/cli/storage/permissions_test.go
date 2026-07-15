package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestStoragePermissionsList_ExactPath verifies `permissions list` filters the
// full ACL list down to the exact /storage/{storage} path by default.
func TestStoragePermissionsList_ExactPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{
			"path": "/storage/local", "type": "user", "ugid": "alice@pve",
			"roleid": "PVEDatastoreAdmin", "propagate": 1,
		},
		map[string]any{
			"path": "/storage/other", "type": "user", "ugid": "bob@pve",
			"roleid": "PVEDatastoreUser", "propagate": 0,
		},
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
	})

	out, err := run(t, f, "permissions", "list", "local")
	require.NoError(t, err)
	require.Contains(t, out, "alice@pve")
	require.Contains(t, out, "PVEDatastoreAdmin")
	require.NotContains(t, out, "bob@pve")
	require.NotContains(t, out, "admins")
}

// TestStoragePermissionsList_Inherited verifies --inherited unions matches
// across the client-side parent chain (/, /storage, /storage/local).
func TestStoragePermissionsList_Inherited(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{
			"path": "/storage/local", "type": "user", "ugid": "alice@pve",
			"roleid": "PVEDatastoreAdmin", "propagate": 1,
		},
		map[string]any{
			"path": "/storage", "type": "group", "ugid": "storage-ops",
			"roleid": "PVEDatastoreUser", "propagate": 1,
		},
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
		map[string]any{
			"path": "/storage/other", "type": "user", "ugid": "bob@pve",
			"roleid": "PVEDatastoreUser", "propagate": 0,
		},
	})

	out, err := run(t, f, "permissions", "list", "local", "--inherited")
	require.NoError(t, err)
	require.Contains(t, out, "alice@pve")
	require.Contains(t, out, "storage-ops")
	require.Contains(t, out, "admins")
	require.Contains(t, out, "INHERITED") // exact header spacing is a tablewriter concern, not asserted here
	require.NotContains(t, out, "bob@pve")
}

// TestStoragePermissionsGrant_PutsACLPath verifies `permissions grant` issues a
// PUT to /access/acl with the derived /storage/{storage} path, the requested
// roles/users, and no propagate field when --no-propagate is not passed.
func TestStoragePermissionsGrant_PutsACLPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/acl", &rec, nil)

	out, err := run(t, f, "permissions", "grant", "local",
		"--roles", "PVEDatastoreAdmin",
		"--users", "alice@pve,bob@pve",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/api2/json/access/acl", rec.path)
	require.Equal(t, "/storage/local", rec.form.Get("path"))
	require.Equal(t, "PVEDatastoreAdmin", rec.form.Get("roles"))
	require.Equal(t, "alice@pve,bob@pve", rec.form.Get("users"))
	require.False(t, rec.form.Has("groups"), "groups must not be sent when --groups is unset")
	require.False(t, rec.form.Has("delete"), "delete must not be sent on grant")
	require.False(t, rec.form.Has("propagate"), "propagate must not be sent when --no-propagate is unset")

	require.Contains(t, out, "Granted")
	require.Contains(t, out, "/storage/local")
}

// TestStoragePermissionsGrant_NoPropagateSendsFalse verifies --no-propagate
// forwards propagate=0 explicitly rather than leaving it unset.
func TestStoragePermissionsGrant_NoPropagateSendsFalse(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/acl", &rec, nil)

	_, err := run(t, f, "permissions", "grant", "local",
		"--roles", "PVEDatastoreUser",
		"--groups", "storage-ops",
		"--no-propagate",
	)
	require.NoError(t, err)
	require.Equal(t, "0", rec.form.Get("propagate"))
}

// TestStoragePermissionsGrant_RequiresSubjectFlag verifies grant/revoke refuse
// to contact the server when none of --users/--groups/--tokens is set.
func TestStoragePermissionsGrant_RequiresSubjectFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	_, err := run(t, f, "permissions", "grant", "local", "--roles", "PVEDatastoreUser")
	require.Error(t, err)
	require.False(t, called, "no request must be made without at least one subject flag")
}

// TestStoragePermissionsRevoke_SetsDeleteFlag verifies `permissions revoke`
// sends delete=1 alongside the derived path and requested roles/tokens.
func TestStoragePermissionsRevoke_SetsDeleteFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/acl", &rec, nil)

	out, err := run(t, f, "permissions", "revoke", "local",
		"--roles", "PVEDatastoreAdmin",
		"--tokens", "root@pam!ci",
	)
	require.NoError(t, err)

	require.Equal(t, "/storage/local", rec.form.Get("path"))
	require.Equal(t, "PVEDatastoreAdmin", rec.form.Get("roles"))
	require.Equal(t, "root@pam!ci", rec.form.Get("tokens"))
	require.Equal(t, "1", rec.form.Get("delete"))
	require.Contains(t, out, "Revoked")
}

// TestStoragePermissionsEffective_UseridPassthrough verifies `permissions
// effective` queries GET /access/permissions with the derived path and, when
// --userid is passed, forwards it unvalidated. GET parameters are encoded into
// the URL query string (not the form body), so the query is captured directly.
func TestStoragePermissionsEffective_UseridPassthrough(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var method, query string
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		query = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"/storage/local": map[string]any{"Datastore.Allocate": 1, "Datastore.Audit": 1},
		})
	})

	out, err := run(t, f, "permissions", "effective", "local", "--userid", "alice@pve")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, method)
	require.Contains(t, query, "path=%2Fstorage%2Flocal")
	require.Contains(t, query, "userid=alice%40pve")
	require.Contains(t, out, "/storage/local")
	require.Contains(t, out, "Datastore.Allocate")
}

// TestStoragePermissionsEffective_ServerError verifies an API failure on the
// effective-permissions read is surfaced.
func TestStoragePermissionsEffective_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})

	_, err := run(t, f, "permissions", "effective", "local")
	require.Error(t, err)
}
