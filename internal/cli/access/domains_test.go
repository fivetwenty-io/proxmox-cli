package access

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestAccess_DomainList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("GET /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"realm": "pam", "type": "pam", "comment": "Linux PAM"},
			map[string]any{"realm": "pve", "type": "pve", "comment": "PVE auth", "default": 1},
			map[string]any{"realm": "corp", "type": "ldap", "tfa": "oath"},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "list"))

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/access/domains", rec.path)

	out := buf.String()
	require.Contains(t, out, "REALM")
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "pam")
	require.Contains(t, out, "ldap")
	require.Contains(t, out, "oath")
}

func TestAccess_DomainGet_KeyValue(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/domains/corp", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"type": "ldap", "server1": "ldap.example.com", "port": 389,
			"base_dn": "dc=example,dc=com", "comment": "corp directory",
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "get", "corp"))

	out := buf.String()
	require.Contains(t, out, "REALM")
	require.Contains(t, out, "corp")
	require.Contains(t, out, "ldap.example.com")
	require.Contains(t, out, "389")
	// The underscore key must surface as a hyphenated upper-case header.
	require.Contains(t, out, "BASE-DN")
}

func TestAccess_DomainCreate_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "create", "corp",
		"--type", "ldap", "--server1", "ldap.example.com", "--port", "636",
		"--base-dn", "dc=example,dc=com", "--user-attr", "uid", "--comment", "corp"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/access/domains", rec.path)
	require.Equal(t, "corp", rec.body["realm"])
	require.Equal(t, "ldap", rec.body["type"])
	require.Equal(t, "ldap.example.com", rec.body["server1"])
	require.Equal(t, "636", rec.body["port"])
	require.Equal(t, "dc=example,dc=com", rec.body["base_dn"])
	require.Equal(t, "uid", rec.body["user_attr"])
	require.Contains(t, buf.String(), "created")
}

func TestAccess_DomainCreate_RequiresType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("POST /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "domain", "create", "corp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--type is required")
	require.False(t, called, "create must not POST without a realm type")
}

func TestAccess_DomainSet_OmitsUnchanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/domains/corp", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "set", "corp", "--comment", "updated", "--delete", "server2"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "updated", rec.body["comment"])
	require.Equal(t, "server2", rec.body["delete"])
	// Flags the caller never set must not be forwarded (no clobbering).
	_, hasServer1 := rec.body["server1"]
	require.False(t, hasServer1, "unset --server1 must not be sent")
	_, hasType := rec.body["type"]
	require.False(t, hasType, "set must not send a realm type (immutable)")
}

func TestAccess_DomainDelete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("DELETE /api2/json/access/domains/corp", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "domain", "delete", "corp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

func TestAccess_DomainDelete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("DELETE /api2/json/access/domains/corp", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "delete", "corp", "--yes"))
	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, buf.String(), "deleted")
}

func TestAccess_DomainSync_RendersUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	upid := "UPID:pve1:00001234:0000ABCD:AABBCCDD:srvreload:corp:root@pam:"
	f.HandleFunc("POST /api2/json/access/domains/corp/sync", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		rec.body = captureBody(r)
		testhelper.WriteData(w, upid)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "sync", "corp", "--dry-run", "--scope", "users"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/access/domains/corp/sync", rec.path)
	require.Equal(t, "1", rec.body["dry-run"])
	require.Equal(t, "users", rec.body["scope"])
	require.Contains(t, buf.String(), upid)
}

func TestAccess_DomainSync_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/domains/corp/sync", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "realm type 'pve' does not support sync")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "domain", "sync", "corp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "sync domain")
}

func TestAccess_DomainCreate_PasswordAndVerify(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "create", "ldapcorp",
		"--type", "ldap",
		"--server1", "ldap.example.com",
		"--password", "bindpass",
		"--verify"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "ldapcorp", rec.body["realm"])
	require.Equal(t, "bindpass", rec.body["password"])
	require.Equal(t, "1", rec.body["verify"])
	require.Contains(t, buf.String(), "created")
}

func TestAccess_DomainCreate_MediumLowFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "create", "oidcrealm",
		"--type", "openid",
		"--acr-values", "phr",
		"--audiences", "app1,app2",
		"--capath", "/etc/ssl/certs",
		"--case-sensitive",
		"--cert", "/etc/pve/certs/client.pem",
		"--certkey", "/etc/pve/certs/client.key",
		"--filter", "(objectClass=person)",
		"--group-dn", "ou=groups,dc=example,dc=com",
		"--group-filter", "(objectClass=groupOfNames)",
		"--groups-autocreate",
		"--groups-claim", "groups",
		"--groups-overwrite",
		"--prompt", "login",
		"--scopes", "email,profile",
		"--sync-defaults-options", "scope=users",
		"--sync-attributes", "email=mail",
		"--tfa", "type=oath",
		"--check-connection",
		"--group-classes", "groupOfNames",
		"--group-name-attr", "cn",
		"--query-userinfo",
		"--sslversion", "tlsv1_3",
		"--user-classes", "inetOrgPerson",
	))

	require.Equal(t, "phr", rec.body["acr-values"])
	require.Equal(t, "app1,app2", rec.body["audiences"])
	require.Equal(t, "/etc/ssl/certs", rec.body["capath"])
	require.Equal(t, "1", rec.body["case-sensitive"])
	require.Equal(t, "/etc/pve/certs/client.pem", rec.body["cert"])
	require.Equal(t, "/etc/pve/certs/client.key", rec.body["certkey"])
	require.Equal(t, "(objectClass=person)", rec.body["filter"])
	require.Equal(t, "ou=groups,dc=example,dc=com", rec.body["group_dn"])
	require.Equal(t, "(objectClass=groupOfNames)", rec.body["group_filter"])
	require.Equal(t, "1", rec.body["groups-autocreate"])
	require.Equal(t, "groups", rec.body["groups-claim"])
	require.Equal(t, "1", rec.body["groups-overwrite"])
	require.Equal(t, "login", rec.body["prompt"])
	require.Equal(t, "email,profile", rec.body["scopes"])
	require.Equal(t, "scope=users", rec.body["sync-defaults-options"])
	require.Equal(t, "email=mail", rec.body["sync_attributes"])
	require.Equal(t, "type=oath", rec.body["tfa"])
	require.Equal(t, "1", rec.body["check-connection"])
	require.Equal(t, "groupOfNames", rec.body["group_classes"])
	require.Equal(t, "cn", rec.body["group_name_attr"])
	require.Equal(t, "1", rec.body["query-userinfo"])
	require.Equal(t, "tlsv1_3", rec.body["sslversion"])
	require.Equal(t, "inetOrgPerson", rec.body["user_classes"])
}

func TestAccess_DomainSet_PasswordAndVerify(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/domains/corp", func(w http.ResponseWriter, r *http.Request) {
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "set", "corp",
		"--password", "newbindpass",
		"--verify=false"))

	require.Equal(t, "newbindpass", rec.body["password"])
	require.Equal(t, "0", rec.body["verify"])
	require.Contains(t, buf.String(), "updated")
}

func TestAccess_DomainSet_Digest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/domains/corp", func(w http.ResponseWriter, r *http.Request) {
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "set", "corp",
		"--digest", "abc123", "--comment", "updated"))

	require.Equal(t, "abc123", rec.body["digest"])
	require.Equal(t, "updated", rec.body["comment"])
}

func TestAccess_DomainCreate_FlagsRegistered(t *testing.T) {
	cmd := newDomainCreateCmd()
	for _, name := range []string{
		"password", "verify", "acr-values", "audiences", "capath", "case-sensitive",
		"cert", "certkey", "filter", "group-dn", "group-filter", "groups-autocreate",
		"groups-claim", "groups-overwrite", "prompt", "scopes", "sync-defaults-options",
		"sync-attributes", "tfa", "check-connection", "group-classes", "group-name-attr",
		"query-userinfo", "sslversion", "user-classes",
	} {
		require.NotNilf(t, cmd.Flags().Lookup(name), "domain create must expose --%s", name)
	}
}

func TestAccess_DomainSet_FlagsRegistered(t *testing.T) {
	cmd := newDomainSetCmd()
	for _, name := range []string{
		"password", "verify", "acr-values", "audiences", "capath", "case-sensitive",
		"cert", "certkey", "filter", "group-dn", "group-filter", "groups-autocreate",
		"groups-claim", "groups-overwrite", "prompt", "scopes", "sync-defaults-options",
		"sync-attributes", "tfa", "check-connection", "group-classes", "group-name-attr",
		"query-userinfo", "sslversion", "user-classes", "digest",
	} {
		require.NotNilf(t, cmd.Flags().Lookup(name), "domain set must expose --%s", name)
	}
}

func TestAccess_DomainCommandTree(t *testing.T) {
	cmd := newDomainCmd()
	want := map[string]bool{"list": false, "get": false, "create": false, "set": false, "delete": false, "sync": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		require.Truef(t, found, "access domain must expose a %q sub-command", name)
	}
}

// TestAccess_NoLocalTargetOrNodeFlag guards against shadowing the root's
// persistent -t/--target and --node selectors with a local flag of the same
// name anywhere in the access command tree.
func TestAccess_NoLocalTargetOrNodeFlag(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{Group})
	var accessCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "access" {
			accessCmd = c
		}
	}
	require.NotNil(t, accessCmd, "access group must be registered")

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		require.Nil(t, c.Flags().Lookup("target"),
			"command %q must not define a local --target (collides with root -t/--target)", c.CommandPath())
		require.Nil(t, c.Flags().Lookup("node"),
			"command %q must not define a local --node (collides with root --node)", c.CommandPath())
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(accessCmd)
}
