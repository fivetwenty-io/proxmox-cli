package pbs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// remoteConfigPath is the base /config/remote endpoint.
const remoteConfigPath = "/api2/json/config/remote"

// remoteName is the sample remote name reused across remote tests.
const remoteName = "remote1"

// --- remote ls ---------------------------------------------------------------

func TestRemoteLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+remoteConfigPath, &rec, []map[string]any{
		{"name": "remote2", "host": "pbs2.example.com", "auth-id": "root@pam", "port": 8007, "comment": "second"},
		{"name": "remote1", "host": "pbs1.example.com", "auth-id": "root@pam", "comment": "first"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, remoteConfigPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "remote1")
	require.Contains(t, out, "remote2")
	require.Contains(t, out, "pbs1.example.com")
}

func TestRemoteLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+remoteConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list remotes")
}

// --- remote show ---------------------------------------------------------------

func TestRemoteShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+remoteConfigPath+"/"+remoteName, map[string]any{
		"name": remoteName, "host": "pbs1.example.com", "auth-id": "root@pam", "comment": "primary backup target",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "show", remoteName)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "pbs1.example.com")
	require.Contains(t, out, "primary backup target")
}

func TestRemoteShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+remoteConfigPath+"/"+remoteName, map[string]any{
		"name": remoteName, "host": "pbs1.example.com", "auth-id": "root@pam",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "show", remoteName, "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "pbs1.example.com")
	require.Contains(t, out, "false (default)", "use-node-proxy defaults to false")
}

func TestRemoteShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+remoteConfigPath+"/"+remoteName, map[string]any{
		"name": remoteName, "host": "pbs1.example.com", "auth-id": "root@pam",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "show", remoteName, "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "pbs1.example.com", got.Set["host"])
	require.Equal(t, "false", got.Defaults["use-node-proxy"])
}

func TestRemoteShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+remoteConfigPath+"/"+remoteName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such remote")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "show", remoteName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get remote")
}

// --- remote add ---------------------------------------------------------------

func TestRemoteAdd_CreatesRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+remoteConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "add", remoteName,
		"--host", "pbs1.example.com", "--auth-id", "root@pam", "--password", "secret")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, remoteConfigPath, rec.path)
	require.Equal(t, remoteName, rec.form.Get("name"))
	require.Equal(t, "pbs1.example.com", rec.form.Get("host"))
	require.Equal(t, "root@pam", rec.form.Get("auth-id"))
	require.Equal(t, "secret", rec.form.Get("password"))
	require.Contains(t, buf.String(), "Remote \"remote1\" created.")
}

func TestRemoteAdd_RequiresHost(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "add", remoteName,
		"--auth-id", "root@pam", "--password", "secret")
	require.Error(t, err)
}

func TestRemoteAdd_RequiresAuthId(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "add", remoteName,
		"--host", "pbs1.example.com", "--password", "secret")
	require.Error(t, err)
}

func TestRemoteAdd_RequiresPassword(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "add", remoteName,
		"--host", "pbs1.example.com", "--auth-id", "root@pam")
	require.Error(t, err)
}

func TestRemoteAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+remoteConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid remote")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "add", remoteName,
		"--host", "pbs1.example.com", "--auth-id", "root@pam", "--password", "secret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create remote")
}

func TestRemoteAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+remoteConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "add", "audit-remote",
		"--host", "pbs2.example.com",
		"--auth-id", "root@pam",
		"--password", "secret",
		"--comment", "audit comment",
		"--fingerprint", "aa:bb:cc",
		"--port", "8443",
		"--use-node-proxy",
	)
	require.NoError(t, err)

	want := map[string]string{
		"name":           "audit-remote",
		"host":           "pbs2.example.com",
		"auth-id":        "root@pam",
		"password":       "secret",
		"comment":        "audit comment",
		"fingerprint":    "aa:bb:cc",
		"port":           "8443",
		"use-node-proxy": "1",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- remote update ---------------------------------------------------------------

func TestRemoteUpdate_UpdatesRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+remoteConfigPath+"/"+remoteName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "update", remoteName,
		"--host", "pbs1-new.example.com", "--digest", "abc123", "--delete", "comment", "--delete", "fingerprint")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, remoteConfigPath+"/"+remoteName, rec.path)
	require.Equal(t, "pbs1-new.example.com", rec.form.Get("host"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "fingerprint"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Remote \"remote1\" updated.")
}

func TestRemoteUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+remoteConfigPath+"/"+remoteName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "update", remoteName, "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{"host", "auth-id", "password", "fingerprint", "port", "use-node-proxy", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRemoteUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "update", remoteName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestRemoteUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "update", remoteName, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestRemoteUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+remoteConfigPath+"/"+remoteName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "update", remoteName, "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update remote")
}

// --- remote delete ---------------------------------------------------------------

func TestRemoteDelete_DeletesRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+remoteConfigPath+"/"+remoteName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "delete", remoteName, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, remoteConfigPath+"/"+remoteName, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "Remote \"remote1\" deleted.")
}

func TestRemoteDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+remoteConfigPath+"/"+remoteName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "delete", remoteName, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete remote")
}

func TestRemoteDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+remoteConfigPath+"/"+remoteName, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "delete", remoteName)
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}

// --- remote scan ls ---------------------------------------------------------------

func TestRemoteScanLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+remoteConfigPath+"/"+remoteName+"/scan", &rec, []map[string]any{
		{"store": "store2", "backend-type": "filesystem", "mount-status": "mounted"},
		{"store": "store1", "backend-type": "s3", "mount-status": "notmounted", "comment": "s3 backed"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "scan", "ls", remoteName)
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, remoteConfigPath+"/"+remoteName+"/scan", rec.path)

	out := buf.String()
	require.Contains(t, out, "store1")
	require.Contains(t, out, "store2")
	require.Contains(t, out, "s3 backed")
}

func TestRemoteScanLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+remoteConfigPath+"/"+remoteName+"/scan", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "scan failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "scan", "ls", remoteName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan remote")
}

// --- remote scan groups ---------------------------------------------------------------

func TestRemoteScanGroups_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+remoteConfigPath+"/"+remoteName+"/scan/store1/groups", &rec, []map[string]any{
		{"backup-type": "vm", "backup-id": "200", "backup-count": 3, "last-backup": 1700000000, "owner": "root@pam"},
		{"backup-type": "vm", "backup-id": "100", "backup-count": 5, "last-backup": 1690000000},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "scan", "groups", remoteName, "store1", "--namespace", "ns1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, remoteConfigPath+"/"+remoteName+"/scan/store1/groups", rec.path)
	require.Equal(t, "ns1", rec.query.Get("namespace"))

	out := buf.String()
	require.Contains(t, out, "100")
	require.Contains(t, out, "200")
	require.Contains(t, out, "root@pam")
}

func TestRemoteScanGroups_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+remoteConfigPath+"/"+remoteName+"/scan/store1/groups", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "scan groups failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "scan", "groups", remoteName, "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan groups on remote")
}

// --- remote scan namespaces ---------------------------------------------------------------

func TestRemoteScanNamespaces_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+remoteConfigPath+"/"+remoteName+"/scan/store1/namespaces", &rec, []map[string]any{
		{"ns": "team-b"},
		{"ns": "team-a", "comment": "team a namespace"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "scan", "namespaces", remoteName, "store1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, remoteConfigPath+"/"+remoteName+"/scan/store1/namespaces", rec.path)

	out := buf.String()
	require.Contains(t, out, "team-a")
	require.Contains(t, out, "team-b")
	require.Contains(t, out, "team a namespace")
}

func TestRemoteScanNamespaces_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+remoteConfigPath+"/"+remoteName+"/scan/store1/namespaces",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "scan namespaces failed")
		})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteCmd(), "remote", "scan", "namespaces", remoteName, "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan namespaces on remote")
}
