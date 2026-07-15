package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// aclPath is the /access/acl endpoint.
const aclPath = "/api2/json/access/acl"

// --- acl ls ---------------------------------------------------------------

func TestACLLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+aclPath, &rec, []map[string]any{
		{"path": "/datastore/store2", "ugid": "bob@pbs", "ugid_type": "user", "roleid": "DatastoreAudit", "propagate": true},
		{"path": "/datastore/store1", "ugid": "alice@pbs", "ugid_type": "user", "roleid": "DatastoreAdmin", "propagate": false},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, aclPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "/datastore/store1")
	require.Contains(t, out, "/datastore/store2")
	require.Contains(t, out, "DatastoreAdmin")
	require.Contains(t, out, "alice@pbs")
}

func TestACLLs_SendsPathAndExact(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+aclPath, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "ls", "--path", "/datastore/store1", "--exact")
	require.NoError(t, err)

	require.Equal(t, "/datastore/store1", rec.query.Get("path"))
	require.Equal(t, "1", rec.query.Get("exact"))
}

func TestACLLs_OmitsFiltersWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+aclPath, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "ls")
	require.NoError(t, err)

	for _, key := range []string{"path", "exact"} {
		_, present := rec.query[key]
		require.False(t, present, "%s must be omitted from the query when unset", key)
	}
}

func TestACLLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+aclPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list acl")
}

// --- acl update ---------------------------------------------------------------

func TestACLUpdate_GrantsRoleToAuthId(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+aclPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update",
		"--path", "/datastore/store1", "--role", "DatastoreAdmin", "--auth-id", "alice@pbs")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, aclPath, rec.path)
	require.Equal(t, "/datastore/store1", rec.form.Get("path"))
	require.Equal(t, "DatastoreAdmin", rec.form.Get("role"))
	require.Equal(t, "alice@pbs", rec.form.Get("auth-id"))
	require.Contains(t, buf.String(), "granted")
}

func TestACLUpdate_GrantsRoleToGroup(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+aclPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update",
		"--path", "/datastore/store1", "--role", "DatastoreAudit", "--group", "auditors")
	require.NoError(t, err)

	require.Equal(t, "auditors", rec.form.Get("group"))
	_, present := rec.form["auth-id"]
	require.False(t, present)
}

func TestACLUpdate_DeleteRevokesRole(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+aclPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update",
		"--path", "/datastore/store1", "--role", "DatastoreAdmin", "--auth-id", "alice@pbs", "--delete")
	require.NoError(t, err)

	require.Equal(t, "1", rec.form.Get("delete"))
	require.Contains(t, buf.String(), "revoked")
}

func TestACLUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+aclPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update",
		"--path", "/datastore/store1",
		"--role", "DatastoreAdmin",
		"--auth-id", "alice@pbs",
		"--propagate=false",
		"--delete",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"path":      "/datastore/store1",
		"role":      "DatastoreAdmin",
		"auth-id":   "alice@pbs",
		"propagate": "0",
		"delete":    "1",
		"digest":    "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestACLUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+aclPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update",
		"--path", "/datastore/store1", "--role", "DatastoreAdmin", "--auth-id", "alice@pbs")
	require.NoError(t, err)

	for _, key := range []string{"group", "propagate", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestACLUpdate_RequiresPath(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update", "--role", "DatastoreAdmin", "--auth-id", "alice@pbs")
	require.Error(t, err)
}

func TestACLUpdate_RequiresRole(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update", "--path", "/datastore/store1", "--auth-id", "alice@pbs")
	require.Error(t, err)
}

func TestACLUpdate_RequiresAuthIdOrGroup(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update", "--path", "/datastore/store1", "--role", "DatastoreAdmin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth-id")
}

func TestACLUpdate_RejectsBothAuthIdAndGroup(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update",
		"--path", "/datastore/store1", "--role", "DatastoreAdmin", "--auth-id", "alice@pbs", "--group", "auditors")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

func TestACLUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+aclPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newACLCmd(), "acl", "update",
		"--path", "/datastore/store1", "--role", "DatastoreAdmin", "--auth-id", "alice@pbs")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update acl")
}
