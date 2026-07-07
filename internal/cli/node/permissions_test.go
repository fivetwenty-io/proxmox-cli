package node_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestNodePermissionsList_ExactPath verifies `permissions list` filters the
// full ACL list down to the exact /nodes/{node} path by default.
func TestNodePermissionsList_ExactPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/nodes/pve1", "type": "user", "ugid": "alice@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/nodes/pve2", "type": "user", "ugid": "bob@pve", "roleid": "PVEAuditor", "propagate": 0},
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "permissions", "list", "pve1"))

	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "alice@pve")
	require.Contains(t, out, "PVEAuditor")
	require.NotContains(t, out, "bob@pve")
	require.NotContains(t, out, "admins")
}

// TestNodePermissionsList_Inherited verifies --inherited unions matches
// across the client-side parent chain (/, /nodes, /nodes/pve1).
func TestNodePermissionsList_Inherited(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/nodes/pve1", "type": "user", "ugid": "alice@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/nodes", "type": "group", "ugid": "node-ops", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
		map[string]any{"path": "/nodes/pve2", "type": "user", "ugid": "bob@pve", "roleid": "PVEAuditor", "propagate": 0},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "permissions", "list", "pve1", "--inherited"))

	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "alice@pve")
	require.Contains(t, out, "node-ops")
	require.Contains(t, out, "admins")
	require.Contains(t, out, "INHERITED") // exact header spacing is a tablewriter concern, not asserted here
	require.NotContains(t, out, "bob@pve")
}

// TestNodePermissionsGrant_PutsACLPath verifies `permissions grant` issues a
// PUT to /access/acl with the derived /nodes/{node} path, the requested
// roles/users, and no propagate field when --no-propagate is not passed.
func TestNodePermissionsGrant_PutsACLPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/access/acl", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "permissions", "grant", "pve1",
		"--roles", "PVEAuditor",
		"--users", "alice@pve,bob@pve",
	))

	require.NoError(t, root.Execute())

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/api2/json/access/acl", rec.path)
	require.Contains(t, rec.body, "path=%2Fnodes%2Fpve1")
	require.Contains(t, rec.body, "roles=PVEAuditor")
	require.Contains(t, rec.body, "users=alice%40pve%2Cbob%40pve")
	require.NotContains(t, rec.body, "groups=")
	require.NotContains(t, rec.body, "delete=")
	require.NotContains(t, rec.body, "propagate=")

	require.Contains(t, buf.String(), "Granted")
	require.Contains(t, buf.String(), "/nodes/pve1")
}

// TestNodePermissionsGrant_NoPropagateSendsFalse verifies --no-propagate
// forwards propagate=0 explicitly rather than leaving it unset.
func TestNodePermissionsGrant_NoPropagateSendsFalse(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/access/acl", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "permissions", "grant", "pve1",
		"--roles", "PVEAuditor",
		"--groups", "node-ops",
		"--no-propagate",
	))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "propagate=0")
}

// TestNodePermissionsGrant_RequiresSubjectFlag verifies grant/revoke refuse to
// contact the server when none of --users/--groups/--tokens is set.
func TestNodePermissionsGrant_RequiresSubjectFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "permissions", "grant", "pve1", "--roles", "PVEAuditor"))

	err := root.Execute()
	require.Error(t, err)
	require.False(t, called, "no request must be made without at least one subject flag")
}

// TestNodePermissionsRevoke_SetsDeleteFlag verifies `permissions revoke` sends
// delete=1 alongside the derived path and requested roles/tokens.
func TestNodePermissionsRevoke_SetsDeleteFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/access/acl", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "permissions", "revoke", "pve1",
		"--roles", "PVEAuditor",
		"--tokens", "root@pam!ci",
	))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "path=%2Fnodes%2Fpve1")
	require.Contains(t, rec.body, "roles=PVEAuditor")
	require.Contains(t, rec.body, "delete=1")
	require.Contains(t, buf.String(), "Revoked")
}

// TestNodePermissionsEffective_UseridPassthrough verifies `permissions
// effective` queries GET /access/permissions with the derived path and, when
// --userid is passed, forwards it unvalidated via the query string.
func TestNodePermissionsEffective_UseridPassthrough(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var query string
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"/nodes/pve1": map[string]any{"Sys.Audit": 1, "Sys.Modify": 1},
		})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "permissions", "effective", "pve1", "--userid", "alice@pve"))

	require.NoError(t, root.Execute())
	require.Contains(t, query, "path=%2Fnodes%2Fpve1")
	require.Contains(t, query, "userid=alice%40pve")
	require.Contains(t, buf.String(), "/nodes/pve1")
	require.Contains(t, buf.String(), "Sys.Audit")
}
