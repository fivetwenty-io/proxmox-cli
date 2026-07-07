package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestQemuPermissions_List_PathDerivation confirms `permissions list` scopes
// the ACL read to the VM's derived /vms/{vmid} path, dropping entries on
// other paths, using the numeric-VMID-plus-known-node fast path (no
// cluster/resources call needed).
func TestQemuPermissions_List_PathDerivation(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/vms/100", "type": "user", "ugid": "alice@pve", "roleid": "PVEVMUser", "propagate": 1},
		map[string]any{"path": "/vms/200", "type": "user", "ugid": "bob@pve", "roleid": "PVEVMAdmin", "propagate": 1},
		map[string]any{"path": "/", "type": "group", "ugid": "ops", "roleid": "Administrator", "propagate": 0},
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "permissions", "list", "100"))

	out := buf.String()
	require.Contains(t, out, "PVEVMUser")
	require.NotContains(t, out, "PVEVMAdmin")
	require.NotContains(t, out, "Administrator")
}

// TestQemuPermissions_List_ByName confirms the <vmid|name> positional is
// resolved through the same cluster/resources lookup every other qemu
// command uses, then the derived path is applied to the ACL filter.
func TestQemuPermissions_List_ByName(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "web1", "node": "pve1"},
		})
	})
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/vms/100", "type": "user", "ugid": "alice@pve", "roleid": "PVEVMUser", "propagate": 1},
	})

	deps := depsFor(t, ac, output.FormatTable, "", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "permissions", "list", "web1"))
	require.Contains(t, buf.String(), "PVEVMUser")
}

// TestQemuPermissions_List_Inherited confirms --inherited unions the ACL
// entries on every ancestor of the VM's path (/ and /vms) with the entries
// on the VM's own path, from the single ACL read already performed.
func TestQemuPermissions_List_Inherited(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/", "type": "user", "ugid": "root@pam", "roleid": "Administrator", "propagate": 1},
		map[string]any{"path": "/vms", "type": "group", "ugid": "ops", "roleid": "PVEVMUser", "propagate": 1},
		map[string]any{"path": "/vms/100", "type": "user", "ugid": "alice@pve", "roleid": "PVEVMAdmin", "propagate": 0},
		map[string]any{"path": "/vms/200", "type": "user", "ugid": "bob@pve", "roleid": "PVEVMAdmin", "propagate": 0},
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "permissions", "list", "100", "--inherited"))

	out := buf.String()
	require.Contains(t, out, "INHERITED") // table renderer spaces the header as "INHERITED - FROM"
	require.Contains(t, out, "Administrator")
	require.Contains(t, out, "ops")
	require.Contains(t, out, "PVEVMAdmin")
	require.NotContains(t, out, "bob@pve")
}

// TestQemuPermissions_Grant_FormBody confirms `permissions grant` derives the
// VM's ACL path and forwards --roles/--users verbatim, with no delete flag.
func TestQemuPermissions_Grant_FormBody(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf,
		"permissions", "grant", "100", "--roles", "PVEVMAdmin", "--users", "bob@pve"))

	form := parseForm(t, body)
	require.Equal(t, "/vms/100", form.Get("path"))
	require.Equal(t, "PVEVMAdmin", form.Get("roles"))
	require.Equal(t, "bob@pve", form.Get("users"))
	require.Empty(t, form.Get("delete"))
	require.Contains(t, buf.String(), "Granted roles PVEVMAdmin to users bob@pve on /vms/100.")
}

// TestQemuPermissions_Revoke_FormBody confirms `permissions revoke` sends
// delete=1 alongside the derived path and subject flags.
func TestQemuPermissions_Revoke_FormBody(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf,
		"permissions", "revoke", "100", "--roles", "PVEVMAdmin", "--groups", "ops"))

	form := parseForm(t, body)
	require.Equal(t, "/vms/100", form.Get("path"))
	require.Equal(t, "1", form.Get("delete"))
	require.Equal(t, "ops", form.Get("groups"))
	require.Contains(t, buf.String(), "Revoked roles PVEVMAdmin from groups ops on /vms/100.")
}

// TestQemuPermissions_Grant_RequiresSubject confirms grant/revoke reject an
// invocation naming no users/groups/tokens before ever calling the API.
func TestQemuPermissions_Grant_RequiresSubject(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("UpdateAcl must not be called when no subject flag was passed")
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "permissions", "grant", "100", "--roles", "PVEVMAdmin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one of --users, --groups, or --tokens is required")
}

// TestQemuPermissions_Effective_UseridPassthrough confirms --userid is
// forwarded to the ListPermissions query unvalidated.
func TestQemuPermissions_Effective_UseridPassthrough(t *testing.T) {
	f, ac := newFakeClient(t)
	var query string
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"/vms/100": map[string]any{"VM.Audit": 1}})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "permissions", "effective", "100", "--userid", "alice@pve"))

	require.Contains(t, query, "userid=alice%40pve")
	require.Contains(t, query, "path=%2Fvms%2F100")
	require.Contains(t, buf.String(), "VM.Audit")
}

// TestQemuPermissions_ResolutionError confirms an ambiguous <vmid|name>
// resolution error propagates before any ACL call is attempted.
func TestQemuPermissions_ResolutionError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "dup", "node": "pve1"},
			map[string]any{"type": "qemu", "vmid": 101, "name": "dup", "node": "pve2"},
		})
	})
	f.HandleFunc("GET /api2/json/access/acl", func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("ListAcl must not be called when guest resolution fails")
	})

	deps := depsFor(t, ac, output.FormatTable, "", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "permissions", "list", "dup")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ambiguous")
}
