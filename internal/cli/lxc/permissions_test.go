package lxc

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// recordFormBody reads a form-encoded request body and returns the raw
// url.Values, unlike recordBody (which coerces numeric/boolean strings), so
// string fields such as "path" and "roles" can be asserted verbatim.
func recordFormBody(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	require.NoError(t, r.ParseForm())
	return r.PostForm
}

// TestPermissions_List_PathDerivation confirms `permissions list` scopes the
// ACL read to the container's derived /vms/{vmid} path, dropping entries on
// other paths, using the numeric-VMID-plus-known-node fast path (no
// cluster/resources call needed).
func TestPermissions_List_PathDerivation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/vms/101", "type": "user", "ugid": "alice@pve", "roleid": "PVEVMUser", "propagate": 1},
		map[string]any{"path": "/vms/200", "type": "user", "ugid": "bob@pve", "roleid": "PVEVMAdmin", "propagate": 1},
		map[string]any{"path": "/", "type": "group", "ugid": "ops", "roleid": "Administrator", "propagate": 0},
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "permissions", "list", "101")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "PVEVMUser")
	require.NotContains(t, out, "PVEVMAdmin")
	require.NotContains(t, out, "Administrator")
}

// TestPermissions_List_ByName confirms the <vmid|name> positional is
// resolved through the same cluster/resources lookup every other lxc command
// uses, then the derived path is applied to the ACL filter.
func TestPermissions_List_ByName(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "lxc", "vmid": 101, "name": "web1", "node": "pve1"},
		})
	})
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/vms/101", "type": "user", "ugid": "alice@pve", "roleid": "PVEVMUser", "propagate": 1},
	})

	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "permissions", "list", "web1")
	require.NoError(t, run())
	require.Contains(t, buf.String(), "PVEVMUser")
}

// TestPermissions_List_Inherited confirms --inherited unions the ACL entries
// on every ancestor of the container's path (/ and /vms) with the entries on
// the container's own path, from the single ACL read already performed.
func TestPermissions_List_Inherited(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/", "type": "user", "ugid": "root@pam", "roleid": "Administrator", "propagate": 1},
		map[string]any{"path": "/vms", "type": "group", "ugid": "ops", "roleid": "PVEVMUser", "propagate": 1},
		map[string]any{"path": "/vms/101", "type": "user", "ugid": "alice@pve", "roleid": "PVEVMAdmin", "propagate": 0},
		map[string]any{"path": "/vms/200", "type": "user", "ugid": "bob@pve", "roleid": "PVEVMAdmin", "propagate": 0},
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "permissions", "list", "101", "--inherited")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "INHERITED") // table renderer spaces the header as "INHERITED - FROM"
	require.Contains(t, out, "Administrator")
	require.Contains(t, out, "ops")
	require.Contains(t, out, "PVEVMAdmin")
	require.NotContains(t, out, "bob@pve")
}

// TestPermissions_Grant_FormBody confirms `permissions grant` derives the
// container's ACL path and forwards --roles/--users verbatim, with no
// delete flag.
func TestPermissions_Grant_FormBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var form url.Values
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		form = recordFormBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf,
		"permissions", "grant", "101", "--roles", "PVEVMAdmin", "--users", "bob@pve")
	require.NoError(t, run())

	require.Equal(t, "/vms/101", form.Get("path"))
	require.Equal(t, "PVEVMAdmin", form.Get("roles"))
	require.Equal(t, "bob@pve", form.Get("users"))
	require.Empty(t, form.Get("delete"))
	require.Contains(t, buf.String(), "Granted roles PVEVMAdmin to users bob@pve on /vms/101.")
}

// TestPermissions_Revoke_FormBody confirms `permissions revoke` sends
// delete=1 alongside the derived path and subject flags.
func TestPermissions_Revoke_FormBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var form url.Values
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		form = recordFormBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf,
		"permissions", "revoke", "101", "--roles", "PVEVMAdmin", "--groups", "ops")
	require.NoError(t, run())

	require.Equal(t, "/vms/101", form.Get("path"))
	require.Equal(t, "1", form.Get("delete"))
	require.Equal(t, "ops", form.Get("groups"))
	require.Contains(t, buf.String(), "Revoked roles PVEVMAdmin from groups ops on /vms/101.")
}

// TestPermissions_Grant_RequiresSubject confirms grant/revoke reject an
// invocation naming no users/groups/tokens before ever calling the API.
func TestPermissions_Grant_RequiresSubject(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("UpdateAcl must not be called when no subject flag was passed")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "permissions", "grant", "101", "--roles", "PVEVMAdmin")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one of --users, --groups, or --tokens is required")
}

// TestPermissions_Effective_UseridPassthrough confirms --userid is forwarded
// to the ListPermissions query unvalidated.
func TestPermissions_Effective_UseridPassthrough(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var query string
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"/vms/101": map[string]any{"VM.Audit": 1}})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "permissions", "effective", "101", "--userid", "alice@pve")
	require.NoError(t, run())

	require.Contains(t, query, "userid=alice%40pve")
	require.Contains(t, query, "path=%2Fvms%2F101")
	require.Contains(t, buf.String(), "VM.Audit")
}

// TestPermissions_ResolutionError confirms an ambiguous <vmid|name>
// resolution error propagates before any ACL call is attempted.
func TestPermissions_ResolutionError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "lxc", "vmid": 101, "name": "dup", "node": "pve1"},
			map[string]any{"type": "lxc", "vmid": 102, "name": "dup", "node": "pve2"},
		})
	})
	f.HandleFunc("GET /api2/json/access/acl", func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("ListAcl must not be called when guest resolution fails")
	})

	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "permissions", "list", "dup")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ambiguous")
}
