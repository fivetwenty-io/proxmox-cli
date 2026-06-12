package storage

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// recordedRequest captures the method, path, and form-encoded body of a request
// the fake server received, for assertion in tests. The Proxmox client encodes
// POST and PUT parameters as application/x-www-form-urlencoded values.
type recordedRequest struct {
	method string
	path   string
	form   url.Values
}

// run constructs the real root command, registers only the storage group, writes
// a temporary config that points the default target at the fake HTTP server, and
// executes `pve storage <args...>`. It returns the captured stdout/stderr buffer
// and the execution error. Driving through the root command wires live Deps the
// same way the binary does, so each sub-command resolves its dependencies through
// cli.GetDeps exactly as in production.
func run(t *testing.T, f *testhelper.FakePVE, args ...string) (string, error) {
	t.Helper()

	// f.Options.Host is "host:port"; split it for the config target.
	host, port := splitHostPort(t, f.Server.Listener.Addr().String())
	// f.Options.APIToken is "tokenid=secret"; split it for the config target.
	tokenID, secret := splitToken(t, f.Options.APIToken)

	cfg := strings.Join([]string{
		"current-context: fake",
		"contexts:",
		"  fake:",
		"    host: " + host,
		"    port: " + strconv.Itoa(port),
		"    protocol: http",
		"    realm: pam",
		"    auth:",
		"      type: token",
		"      username: root@pam",
		"      token-id: " + tokenID,
		"      secret: " + secret,
		"    tls:",
		"      insecure: true",
		"",
	}, "\n")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	t.Setenv("PVE_CONTEXT", "")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_OUTPUT", "")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(newGroupCmd(&cli.Deps{}))

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"--config", cfgPath, "storage"}, args...))

	err := root.Execute()
	return buf.String(), err
}

// splitHostPort splits a "host:port" string, failing the test on malformed input.
func splitHostPort(t *testing.T, hostport string) (string, int) {
	t.Helper()
	i := strings.LastIndex(hostport, ":")
	require.Greater(t, i, 0, "host:port must contain a port: %q", hostport)
	port, err := strconv.Atoi(hostport[i+1:])
	require.NoError(t, err)
	return hostport[:i], port
}

// splitToken splits a "tokenid=secret" string, failing the test on malformed input.
func splitToken(t *testing.T, token string) (string, string) {
	t.Helper()
	i := strings.Index(token, "=")
	require.Greater(t, i, 0, "token must be tokenid=secret: %q", token)
	return token[:i], token[i+1:]
}

// recordJSON registers a handler that records the request and replies with the
// PVE-shaped {"data": payload} envelope.
func recordJSON(f *testhelper.FakePVE, pattern string, rec *recordedRequest, payload any) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.form = r.PostForm
		testhelper.WriteData(w, payload)
	})
}

// TestStorageList_Table verifies that `pve storage list` queries GET /storage and
// renders the configured storage definitions in table form.
func TestStorageList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/storage", &rec, []map[string]any{
		{"storage": "local", "type": "dir", "content": "iso,vztmpl", "path": "/var/lib/vz", "shared": 0, "disable": 0},
		{"storage": "nfs-backup", "type": "nfs", "content": "backup", "server": "10.0.0.5",
			"export": "/export/backup", "shared": 1, "disable": 0},
	})

	out, err := run(t, f, "list")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/storage", rec.path)

	require.Contains(t, out, "STORAGE")
	// the table renderer may pad around the slash; assert the words individually.
	require.Contains(t, out, "PATH")
	require.Contains(t, out, "SERVER")
	require.Contains(t, out, "ENABLED")
	require.Contains(t, out, "local")
	require.Contains(t, out, "/var/lib/vz")
	require.Contains(t, out, "nfs-backup")
	require.Contains(t, out, "10.0.0.5:/export/backup")
	// nfs-backup is shared; local is not.
	require.Contains(t, out, "yes")
	require.Contains(t, out, "no")
}

// TestStorageList_TypeFilter verifies that --type is forwarded to the API as a
// query parameter on the storage index request.
func TestStorageList_TypeFilter(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/storage", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{
			{"storage": "local-lvm", "type": "lvmthin", "content": "images", "disable": 0},
		})
	})

	out, err := run(t, f, "list", "--type", "lvmthin")
	require.NoError(t, err)
	require.Contains(t, gotQuery, "type=lvmthin")
	require.Contains(t, out, "local-lvm")
}

// TestStorageList_ServerError verifies that an API failure on list is surfaced.
func TestStorageList_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/storage", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	_, err := run(t, f, "list")
	require.Error(t, err)
}

// TestStorageGet_Single verifies that `pve storage get` queries the per-storage
// path and renders the returned fields as a key/value detail.
func TestStorageGet_Single(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/storage/local", &rec, map[string]any{
		"storage": "local", "type": "dir", "content": "iso,vztmpl", "path": "/var/lib/vz", "maxfiles": 3,
	})

	out, err := run(t, f, "get", "local")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/storage/local", rec.path)

	require.Contains(t, out, "storage")
	require.Contains(t, out, "local")
	require.Contains(t, out, "path")
	require.Contains(t, out, "/var/lib/vz")
	// numeric field rendered without a trailing ".0"
	require.Contains(t, out, "maxfiles")
	require.Contains(t, out, "3")
}

// TestStorageGet_JSON verifies JSON output for a single storage carries the raw
// API fields verbatim.
func TestStorageGet_JSON(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/storage/local", map[string]any{
		"storage": "local", "type": "dir", "path": "/var/lib/vz",
	})

	out, err := run(t, f, "get", "local", "--output", "json")
	require.NoError(t, err)
	require.Contains(t, out, `"storage": "local"`)
	require.Contains(t, out, `"path": "/var/lib/vz"`)
}

// TestStorageGet_ServerError verifies an API failure on get is surfaced.
func TestStorageGet_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/storage/missing", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such storage")
	})

	_, err := run(t, f, "get", "missing")
	require.Error(t, err)
}

// TestStorageCreate_PostsParams verifies that `pve storage create` issues a POST
// to /storage with the storage id, type, and supplied attributes in the body.
func TestStorageCreate_PostsParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{
		"storage": "backups", "type": "dir",
	})

	out, err := run(t, f, "create",
		"--storage", "backups",
		"--type", "dir",
		"--path", "/srv/backups",
		"--content", "backup",
		"--shared",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/storage", rec.path)
	require.Equal(t, "backups", rec.form.Get("storage"))
	require.Equal(t, "dir", rec.form.Get("type"))
	require.Equal(t, "/srv/backups", rec.form.Get("path"))
	require.Equal(t, "backup", rec.form.Get("content"))
	require.Equal(t, "1", rec.form.Get("shared"))
	// --enabled defaults to true and was not changed, so disable must be absent.
	require.False(t, rec.form.Has("disable"), "disable must not be sent when --enabled is unchanged")

	require.Contains(t, out, `Storage "backups" created.`)
}

// TestStorageCreate_EnabledFalseSendsDisable verifies that --enabled=false maps to
// the inverted disable flag in the request body.
func TestStorageCreate_EnabledFalseSendsDisable(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{
		"storage": "off", "type": "dir",
	})

	_, err := run(t, f, "create",
		"--storage", "off",
		"--type", "dir",
		"--path", "/srv/off",
		"--enabled=false",
	)
	require.NoError(t, err)
	require.Equal(t, "1", rec.form.Get("disable"), "disable must be set when --enabled=false")
}

// TestStorageCreate_MissingRequired verifies that omitting required flags fails
// without contacting the server.
func TestStorageCreate_MissingRequired(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/storage", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, map[string]any{})
	})

	_, err := run(t, f, "create", "--type", "dir")
	require.Error(t, err)
	require.False(t, called, "no request must be made when a required flag is missing")
}

// TestStorageSet_PutsParams verifies that `pve storage set` issues a PUT to the
// per-storage path with the changed attributes.
func TestStorageSet_PutsParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/storage/local", &rec, map[string]any{
		"storage": "local", "type": "dir",
	})

	out, err := run(t, f, "set", "local",
		"--content", "iso,backup",
		"--nodes", "pve1,pve2",
		"--enabled=false",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/api2/json/storage/local", rec.path)
	require.Equal(t, "iso,backup", rec.form.Get("content"))
	require.Equal(t, "pve1,pve2", rec.form.Get("nodes"))
	require.Equal(t, "1", rec.form.Get("disable"))

	require.Contains(t, out, `Storage "local" updated.`)
}

// TestStorageSet_ServerError verifies an API failure on update is surfaced.
func TestStorageSet_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/storage/local", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid content")
	})

	_, err := run(t, f, "set", "local", "--content", "bogus")
	require.Error(t, err)
}

// TestStorageDelete_RequiresYes verifies the delete guard refuses to act without
// --yes and makes no request.
func TestStorageDelete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/storage/local", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	_, err := run(t, f, "delete", "local")
	require.Error(t, err)
	require.False(t, called, "delete must not contact the server without --yes")
}

// TestStorageDelete_WithYes verifies that `pve storage delete --yes` issues a
// DELETE to the per-storage path and reports success.
func TestStorageDelete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/storage/local", &rec, nil)

	out, err := run(t, f, "delete", "local", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "/api2/json/storage/local", rec.path)
	require.Contains(t, out, `Storage "local" deleted.`)
}

// TestStorageDelete_ServerError verifies an API failure on delete is surfaced.
func TestStorageDelete_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("DELETE /api2/json/storage/busy", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "in use")
	})

	_, err := run(t, f, "delete", "busy", "--yes")
	require.Error(t, err)
}

// TestStorageCreate_CIFSForwardsPerTypeFields verifies the CIFS identity fields
// reach the request body and the password is never echoed to output.
func TestStorageCreate_CIFSForwardsPerTypeFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	const secret = "s3cr3tcifspw"
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{"storage": "cifs1", "type": "cifs"})

	out, err := run(t, f, "create",
		"--storage", "cifs1", "--type", "cifs",
		"--server", "10.0.0.3", "--share", "backup",
		"--username", "svc", "--password", secret,
		"--domain", "WORKGROUP", "--smbversion", "3.0",
	)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.3", rec.form.Get("server"))
	require.Equal(t, "backup", rec.form.Get("share"))
	require.Equal(t, "svc", rec.form.Get("username"))
	require.Equal(t, secret, rec.form.Get("password"), "password must reach the API")
	require.Equal(t, "WORKGROUP", rec.form.Get("domain"))
	require.Equal(t, "3.0", rec.form.Get("smbversion"))
	require.NotContains(t, out, secret, "password must never be echoed to output")
}

// TestStorageCreate_PBSForwardsCredentials verifies PBS fields including secret
// inputs are forwarded but kept out of output.
func TestStorageCreate_PBSForwardsCredentials(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	const encKey = "supersecretenckey"
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{"storage": "pbs1", "type": "pbs"})

	out, err := run(t, f, "create",
		"--storage", "pbs1", "--type", "pbs",
		"--server", "10.0.0.5", "--datastore", "store",
		"--fingerprint", "AA:BB", "--encryption-key", encKey,
		"--port", "8007", "--namespace", "team",
	)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.5", rec.form.Get("server"))
	require.Equal(t, "store", rec.form.Get("datastore"))
	require.Equal(t, "AA:BB", rec.form.Get("fingerprint"))
	require.Equal(t, encKey, rec.form.Get("encryption-key"), "encryption-key must reach the API")
	require.Equal(t, "8007", rec.form.Get("port"))
	require.Equal(t, "team", rec.form.Get("namespace"))
	require.NotContains(t, out, encKey, "encryption-key must never be echoed")
}

// TestStorageCreate_IscsiTargetMapsToTarget verifies the --iscsi-target flag
// (renamed to avoid colliding with the root --target) is sent as "target".
func TestStorageCreate_IscsiTargetMapsToTarget(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{"storage": "iscsi1", "type": "iscsi"})

	_, err := run(t, f, "create",
		"--storage", "iscsi1", "--type", "iscsi",
		"--portal", "10.0.0.4", "--iscsi-target", "iqn.2024-01.com.example:disk0",
	)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.4", rec.form.Get("portal"))
	require.Equal(t, "iqn.2024-01.com.example:disk0", rec.form.Get("target"))
	require.False(t, rec.form.Has("iscsi-target"), "the flag name must not leak into the body")
}

// TestStorageCreate_OmitsUnsetNewFlags verifies the expanded flag set is omitted
// from the body unless explicitly passed.
func TestStorageCreate_OmitsUnsetNewFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{"storage": "lvm1", "type": "lvm"})

	_, err := run(t, f, "create", "--storage", "lvm1", "--type", "lvm", "--vgname", "pve")
	require.NoError(t, err)
	require.Equal(t, "pve", rec.form.Get("vgname"))
	for _, omitted := range []string{"password", "keyring", "encryption-key", "master-pubkey",
		"datastore", "portal", "target", "pool", "fs-name", "port", "prune-backups", "format"} {
		require.False(t, rec.form.Has(omitted), "%q must be omitted when unset", omitted)
	}
}

// TestStorageSet_ForwardsNewTunablesAndDelete verifies the expanded update flag
// set, --delete, and --digest are forwarded, and create-only identity fields are
// never sent on update.
func TestStorageSet_ForwardsNewTunablesAndDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/storage/pbs1", &rec, map[string]any{"storage": "pbs1", "type": "pbs"})

	_, err := run(t, f, "set", "pbs1",
		"--prune-backups", "keep-last=3", "--max-protected-backups", "5",
		"--delete", "fingerprint", "--digest", "abc123",
	)
	require.NoError(t, err)
	require.Equal(t, "keep-last=3", rec.form.Get("prune-backups"))
	require.Equal(t, "5", rec.form.Get("max-protected-backups"))
	require.Equal(t, "fingerprint", rec.form.Get("delete"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	// Identity fields have no update parameter and a create-only flag, so they
	// can never appear on a PUT.
	require.False(t, rec.form.Has("datastore"))
	require.False(t, rec.form.Has("type"))
}

// TestStorageGet_ScrubsSecrets verifies that if the backend ever echoes a stored
// credential on get, the CLI strips it from every output format.
func TestStorageGet_ScrubsSecrets(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		f := testhelper.NewFakePVE(t)
		f.HandleJSON("GET /api2/json/storage/pbs1", map[string]any{
			"storage": "pbs1", "type": "pbs", "server": "10.0.0.5", "datastore": "store",
			"password":       "supersecretpw",
			"encryption-key": "supersecretkey",
			"keyring":        "supersecretkeyring",
			"master-pubkey":  "supersecretpubkey",
		})

		out, err := run(t, f, "get", "pbs1", "--output", format)
		require.NoError(t, err)
		require.Contains(t, out, "pbs1")
		require.NotContains(t, out, "supersecretpw", "password must not be echoed (%s)", format)
		require.NotContains(t, out, "supersecretkey", "encryption-key must not be echoed (%s)", format)
		require.NotContains(t, out, "supersecretkeyring", "keyring must not be echoed (%s)", format)
		require.NotContains(t, out, "supersecretpubkey", "master-pubkey must not be echoed (%s)", format)
	}
}

// TestStorageGroup_HasAllSubcommands verifies the storage group exposes every
// expected leaf sub-command.
func TestStorageGroup_HasAllSubcommands(t *testing.T) {
	cmd := newGroupCmd(&cli.Deps{})
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{
		"list", "get", "create", "set", "delete",
		"status", "identity", "rrddata", "rrd",
	} {
		require.True(t, names[want], "storage must expose sub-command %q", want)
	}
}
