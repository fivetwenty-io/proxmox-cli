package access

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// newDeps constructs a Deps wired to the fake server with the given format. It
// splits the fake's host:port so the underlying client does not append a default
// port to an address that already carries one.
func newDeps(t *testing.T, f *testhelper.FakePVE, format output.Format) *cli.Deps {
	t.Helper()

	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTP,
		APIToken: "root@pam!test=00000000-0000-0000-0000-000000000000",
		SSLOptions: &pve.SSLOptions{
			VerifyMode: pve.SSLVerifyNone,
		},
	}
	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return &cli.Deps{API: ac, Out: output.New(), Format: format}
}

// run builds the access group command, injects deps via context, captures
// output in buf, and executes it.
func run(deps *cli.Deps, buf *bytes.Buffer, args ...string) error {
	cmd := Group(&cli.Deps{})
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// runWithStdin behaves like run but feeds stdin so commands that prompt (such
// as `access password set` without --password) can read interactive input.
func runWithStdin(deps *cli.Deps, buf *bytes.Buffer, stdin string, args ...string) error {
	cmd := Group(&cli.Deps{})
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	return cmd.Execute()
}

// recordReq captures the method, path, and decoded body of an incoming request.
type recordReq struct {
	method string
	path   string
	body   map[string]any
}

// captureBody decodes a request's form or JSON body into a generic map.
func captureBody(r *http.Request) map[string]any {
	out := map[string]any{}
	if err := r.ParseForm(); err == nil && len(r.PostForm) > 0 {
		for k, v := range r.PostForm {
			if len(v) == 1 {
				out[k] = v[0]
			} else {
				out[k] = v
			}
		}
		return out
	}
	raw, _ := io.ReadAll(r.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// ---------------------------------------------------------------------------
// registration
// ---------------------------------------------------------------------------

func TestAccess_GroupRegistered(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{Group})

	found := false
	for _, c := range root.Commands() {
		if c.Name() == "access" {
			found = true
		}
	}
	require.True(t, found, "access group must be registered with the root command")
}

// ---------------------------------------------------------------------------
// users
// ---------------------------------------------------------------------------

func TestAccess_UserList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("GET /api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"userid": "root@pam", "enable": 1, "expire": 0,
				"firstname": "Super", "lastname": "User", "email": "root@example.com",
				"comment": "admin", "groups": "admins",
			},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "list"))

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/access/users", rec.path)

	out := buf.String()
	require.Contains(t, out, "USERID")
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "admins")
}

func TestAccess_UserList_EnabledFilterFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var query string
	f.HandleFunc("GET /api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "list", "--enabled"))
	require.Contains(t, query, "enabled=1")
}

func TestAccess_UserList_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/users", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "user", "list"))
}

func TestAccess_UserGet_Single(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/users/root@pam", map[string]any{
		"enable": true, "comment": "admin", "email": "root@example.com",
		"groups": []string{"admins", "ops"},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "get", "root@pam"))

	out := buf.String()
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "admins,ops")
}

func TestAccess_UserGet_NotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/users/ghost@pam", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such user")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "user", "get", "ghost@pam"))
}

func TestAccess_UserCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.body = r.Method, r.URL.Path, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "create", "alice@pve",
		"--email", "a@x.io", "--comment", "qa",
		"--groups", "admins,ops", "--expire", "3600", "--enable=false"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "alice@pve", rec.body["userid"])
	require.Equal(t, "a@x.io", rec.body["email"])
	// PART B input mapping: groups/expire/enable must be encoded into the body.
	require.Equal(t, "admins,ops", rec.body["groups"])
	require.Equal(t, "3600", rec.body["expire"])
	require.Equal(t, "0", rec.body["enable"])
	require.Contains(t, buf.String(), "User 'alice@pve' created.")
}

func TestAccess_UserSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/alice@pve", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "set", "alice@pve", "--comment", "updated"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "updated", rec.body["comment"])
	require.Contains(t, buf.String(), "User 'alice@pve' updated.")
}

func TestAccess_UserSet_Append(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/alice@pve", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "set", "alice@pve", "--groups", "ops", "--append"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "ops", rec.body["groups"])
	require.Equal(t, "1", rec.body["append"])
	require.Contains(t, buf.String(), "User 'alice@pve' updated.")
}

func TestAccess_UserSet_AppendFlagRegistered(t *testing.T) {
	cmd := newUserSetCmd()
	require.NotNil(t, cmd.Flags().Lookup("append"), "user set must expose --append")
}

func TestAccess_UserDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("DELETE /api2/json/access/users/alice@pve", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "delete", "alice@pve", "--yes"))

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "/api2/json/access/users/alice@pve", rec.path)
	require.Contains(t, buf.String(), "User 'alice@pve' deleted.")
}

func TestAccess_UserDelete_RequiresConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/access/users/alice@pve", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "user", "delete", "alice@pve"))
	require.False(t, called, "delete must not call the API without --yes")
}

// ---------------------------------------------------------------------------
// tokens
// ---------------------------------------------------------------------------

func TestAccess_TokenList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/users/root@pam/token", []any{
		map[string]any{"tokenid": "ci", "expire": 0, "privsep": 1, "comment": "ci token"},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "list", "root@pam"))

	out := buf.String()
	require.Contains(t, out, "TOKENID")
	require.Contains(t, out, "ci")
	require.Contains(t, out, "ci token")
}

func TestAccess_TokenList_Error(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/users/root@pam/token", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "user", "token", "list", "root@pam"))
}

func TestAccess_TokenGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/users/root@pam/token/ci", map[string]any{
		"expire": 0, "privsep": true, "comment": "ci token",
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "get", "root@pam", "ci"))
	require.Contains(t, buf.String(), "ci token")
}

func TestAccess_TokenCreate_PrintsValue(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/users/root@pam/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{
			"full-tokenid": "root@pam!ci",
			"value":        "secret-uuid-value",
			"info":         map[string]any{"privsep": 1},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "create", "root@pam", "ci",
		"--comment", "ci token", "--expire", "3600", "--privsep"))

	require.Equal(t, http.MethodPost, rec.method)
	// PART B specifies --privsep/--expire/--comment must be encoded into the body.
	require.Equal(t, "ci token", rec.body["comment"])
	require.Equal(t, "3600", rec.body["expire"])
	require.Equal(t, "1", rec.body["privsep"])

	out := buf.String()
	require.Contains(t, out, "root@pam!ci")
	require.Contains(t, out, "secret-uuid-value")
}

// TestAccess_TokenCreate_PrivsepDisabled verifies the boolean encoding for the
// false case (--privsep=false -> "0") so a dropped flag value is caught.
func TestAccess_TokenCreate_PrivsepDisabled(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/users/root@pam/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{
			"full-tokenid": "root@pam!ci",
			"value":        "secret-uuid-value",
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "create", "root@pam", "ci", "--privsep=false"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "0", rec.body["privsep"])
}

func TestAccess_TokenSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/root@pam/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{"comment": "rotated"})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "set", "root@pam", "ci", "--comment", "rotated"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "rotated", rec.body["comment"])
	require.Contains(t, buf.String(), "Token updated.")
}

func TestAccess_TokenSet_Regenerate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/root@pam/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{
			"full-tokenid": "root@pam!ci",
			"value":        "new-secret-value",
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "set", "root@pam", "ci", "--regenerate"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "1", rec.body["regenerate"])

	out := buf.String()
	require.Contains(t, out, "root@pam!ci")
	require.Contains(t, out, "new-secret-value")
}

func TestAccess_TokenSet_Delete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/root@pam/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "set", "root@pam", "ci", "--delete", "comment"))

	require.Equal(t, "comment", rec.body["delete"])
	require.Contains(t, buf.String(), "Token updated.")
}

func TestAccess_TokenSet_FlagsRegistered(t *testing.T) {
	cmd := newTokenSetCmd()
	require.NotNil(t, cmd.Flags().Lookup("regenerate"), "token set must expose --regenerate")
	require.NotNil(t, cmd.Flags().Lookup("delete"), "token set must expose --delete")
}

func TestAccess_TokenDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("DELETE /api2/json/access/users/root@pam/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "delete", "root@pam", "ci", "--yes"))

	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, buf.String(), "Token 'ci' deleted.")
}

func TestAccess_TokenDelete_RequiresConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "user", "token", "delete", "root@pam", "ci"))
}

// ---------------------------------------------------------------------------
// groups
// ---------------------------------------------------------------------------

func TestAccess_GroupList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/groups", []any{
		map[string]any{"groupid": "admins", "comment": "operators", "users": "root@pam"},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "group", "list"))

	out := buf.String()
	require.Contains(t, out, "GROUPID")
	require.Contains(t, out, "admins")
	require.Contains(t, out, "root@pam")
}

func TestAccess_GroupGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/groups/admins", map[string]any{
		"comment": "operators", "members": []string{"root@pam", "alice@pve"},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "group", "get", "admins"))
	require.Contains(t, buf.String(), "root@pam,alice@pve")
}

func TestAccess_GroupGet_Error(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/groups/missing", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no group")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "group", "get", "missing"))
}

func TestAccess_GroupCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/groups", func(w http.ResponseWriter, r *http.Request) {
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "group", "create", "ops", "--comment", "operations"))

	require.Equal(t, "ops", rec.body["groupid"])
	require.Equal(t, "operations", rec.body["comment"])
	require.Contains(t, buf.String(), "Group 'ops' created.")
}

func TestAccess_GroupSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/groups/ops", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "group", "set", "ops", "--comment", "new"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "new", rec.body["comment"])
	require.Contains(t, buf.String(), "Group updated.")
}

func TestAccess_GroupDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("DELETE /api2/json/access/groups/ops", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "group", "delete", "ops", "--yes"))

	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, buf.String(), "Group 'ops' deleted.")
}

func TestAccess_GroupDelete_RequiresConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "group", "delete", "ops"))
}

// ---------------------------------------------------------------------------
// roles
// ---------------------------------------------------------------------------

func TestAccess_RoleList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/roles", []any{
		map[string]any{"roleid": "Administrator", "special": 1, "privs": "VM.Audit,VM.Console"},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "list"))

	out := buf.String()
	require.Contains(t, out, "ROLEID")
	require.Contains(t, out, "Administrator")
	require.Contains(t, out, "VM.Audit")
}

func TestAccess_RoleGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/roles/PVEAuditor", map[string]any{
		"VM.Audit":        true,
		"Datastore.Audit": true,
		"VM.Console":      false,
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "get", "PVEAuditor"))

	out := buf.String()
	require.Contains(t, out, "VM.Audit")
	require.Contains(t, out, "Datastore.Audit")
	require.NotContains(t, out, "VM.Console")
}

func TestAccess_RoleGet_Error(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/roles/Nope", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no role")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "role", "get", "Nope"))
}

// ---------------------------------------------------------------------------
// acl
// ---------------------------------------------------------------------------

func TestAccess_ACLList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{
			"path": "/", "type": "user", "ugid": "root@pam", "roleid": "Administrator", "propagate": 1,
		},
		map[string]any{
			"path": "/vms", "type": "group", "ugid": "ops", "roleid": "PVEVMUser", "propagate": 0,
		},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "acl", "list"))

	out := buf.String()
	require.Contains(t, out, "PATH")
	require.Contains(t, out, "Administrator")
	require.Contains(t, out, "PVEVMUser")
}

func TestAccess_ACLList_PathFilter(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/acl", []any{
		map[string]any{"path": "/", "type": "user", "ugid": "root@pam", "roleid": "Administrator", "propagate": 1},
		map[string]any{"path": "/vms", "type": "group", "ugid": "ops", "roleid": "PVEVMUser", "propagate": 0},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "acl", "list", "--path", "/vms", "--exact"))

	out := buf.String()
	require.Contains(t, out, "PVEVMUser")
	require.NotContains(t, out, "Administrator")
}

func TestAccess_ACLSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "acl", "set", "--path", "/vms", "--roles", "PVEVMUser", "--users", "alice@pve"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/vms", rec.body["path"])
	require.Equal(t, "PVEVMUser", rec.body["roles"])
	require.Equal(t, "alice@pve", rec.body["users"])
	require.Contains(t, buf.String(), "ACL updated.")
}

func TestAccess_ACL_RequiredFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "acl set missing --path",
			args:    []string{"acl", "set", "--roles", "PVEVMUser"},
			wantErr: "path",
		},
		{
			name:    "acl set missing --roles",
			args:    []string{"acl", "set", "--path", "/vms"},
			wantErr: "roles",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := testhelper.NewFakePVE(t)
			deps := newDeps(t, f, output.FormatTable)
			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// ---------------------------------------------------------------------------
// permissions
// ---------------------------------------------------------------------------

func TestAccess_Permissions(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/permissions", map[string]any{
		"/":        map[string]any{"VM.Audit": 1, "Sys.Audit": 1},
		"/vms/100": map[string]any{"VM.Console": 1},
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "permissions"))

	out := buf.String()
	require.Contains(t, out, "PATH")
	require.Contains(t, out, "/vms/100")
	require.Contains(t, out, "VM.Console")
}

func TestAccess_Permissions_UseridFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var query string
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "permissions", "--userid", "alice@pve"))
	require.Contains(t, query, "userid=alice%40pve")
}

func TestAccess_Permissions_Error(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "permissions"))
}

// ---------------------------------------------------------------------------
// password
// ---------------------------------------------------------------------------

func TestAccess_PasswordSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/password", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "password", "set", "--userid", "alice@pve", "--password", "s3cret"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "alice@pve", rec.body["userid"])
	require.Equal(t, "s3cret", rec.body["password"])
	require.Contains(t, buf.String(), "Password updated.")
}

func TestAccess_Password_RequiredFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "password set missing --userid",
			args:    []string{"password", "set", "--password", "s3cret"},
			wantErr: "userid",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := testhelper.NewFakePVE(t)
			deps := newDeps(t, f, output.FormatTable)
			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestAccess_PasswordSet_ConfirmationPassword(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/password", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "password", "set",
		"--userid", "alice@pve", "--password", "newpass", "--confirmation-password", "oldpass"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "alice@pve", rec.body["userid"])
	require.Equal(t, "newpass", rec.body["password"])
	require.Equal(t, "oldpass", rec.body["confirmation-password"])
	require.Contains(t, buf.String(), "Password updated.")
}

func TestAccess_PasswordSet_ConfirmationPasswordFlagRegistered(t *testing.T) {
	cmd := newPasswordSetCmd()
	require.NotNil(t, cmd.Flags().Lookup("confirmation-password"), "password set must expose --confirmation-password")
}

// TestAccess_PasswordSet_PromptsForPassword verifies the spec contract that
// `--password` "prompts if absent": when the flag is omitted the command reads
// the new password from stdin and sends it in the UpdatePassword request body.
func TestAccess_PasswordSet_PromptsForPassword(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/password", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, runWithStdin(deps, &buf, "prompted-pass\n", "password", "set", "--userid", "alice@pve"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "alice@pve", rec.body["userid"])
	require.Equal(t, "prompted-pass", rec.body["password"])
	require.Contains(t, buf.String(), "Password updated.")
}

// TestAccess_PasswordSet_EmptyPromptRejected verifies that an empty prompted
// password is rejected rather than sent to the API.
func TestAccess_PasswordSet_EmptyPromptRejected(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/access/password", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.Error(t, runWithStdin(deps, &buf, "\n", "password", "set", "--userid", "alice@pve"))
	require.False(t, called, "empty prompted password must not reach the API")
}

// ---------------------------------------------------------------------------
// json output sanity
// ---------------------------------------------------------------------------

func TestAccess_UserList_JSON(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/access/users", []any{
		map[string]any{"userid": "root@pam", "enable": 1},
	})

	deps := newDeps(t, f, output.FormatJSON)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "list"))
	require.Contains(t, buf.String(), `"userid": "root@pam"`)
}
