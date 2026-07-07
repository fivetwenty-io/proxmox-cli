package access

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// This file audits every scalar flag on every write-path (POST/PUT/DELETE)
// command in the access package, asserting each one lands in the outgoing
// request body under the exact PVE API parameter key. Several domain
// (realm) fields use an underscore in the wire key while every other flag
// in the package uses a hyphen (or no separator); those are called out
// explicitly below since they are the easiest place for a flag-to-param
// mapping regression to hide.

// ---------------------------------------------------------------------------
// acl set
// ---------------------------------------------------------------------------

func TestAccessAudit_ACLSet_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "acl", "set",
		"--path", "/vms/100",
		"--roles", "PVEVMUser,PVEAuditor",
		"--users", "alice@pve,bob@pve",
		"--groups", "ops,admins",
		"--tokens", "alice@pve!ci",
		"--propagate=false",
		"--delete",
	))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/vms/100", rec.body["path"])
	require.Equal(t, "PVEVMUser,PVEAuditor", rec.body["roles"])
	require.Equal(t, "alice@pve,bob@pve", rec.body["users"])
	require.Equal(t, "ops,admins", rec.body["groups"])
	require.Equal(t, "alice@pve!ci", rec.body["tokens"])
	require.Equal(t, "0", rec.body["propagate"])
	require.Equal(t, "1", rec.body["delete"])
}

// ---------------------------------------------------------------------------
// domain create / set — full realm-config flag surface
// ---------------------------------------------------------------------------

// TestAccessAudit_DomainCreate_AllFlags asserts every domain create flag
// reaches POST /access/domains under its exact wire key. Note the underscore
// keys (base_dn, bind_dn, group_dn, group_filter, group_classes,
// group_name_attr, sync_attributes, user_attr, user_classes): these are the
// only fields in the whole access package whose wire key does not match the
// flag's hyphenated spelling.
func TestAccessAudit_DomainCreate_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "create", "corp-ldap",
		"--type", "ldap",
		"--username-claim", "preferred_username",
		"--comment", "corp directory",
		"--default",
		"--server1", "ldap1.example.com",
		"--server2", "ldap2.example.com",
		"--port", "636",
		"--mode", "ldaps",
		"--base-dn", "dc=example,dc=com",
		"--bind-dn", "cn=svc,dc=example,dc=com",
		"--user-attr", "uid",
		"--domain", "example.com",
		"--issuer-url", "https://idp.example.com",
		"--client-id", "pve-client",
		"--client-key", "client-secret",
		"--autocreate",
		"--password", "bindpw",
		"--verify",
		"--acr-values", "urn:mace:incommon:iap:silver",
		"--audiences", "aud1,aud2",
		"--capath", "/etc/ssl/certs",
		"--case-sensitive",
		"--cert", "/etc/pve/client-cert.pem",
		"--certkey", "/etc/pve/client-key.pem",
		"--filter", "(objectClass=person)",
		"--group-dn", "ou=groups,dc=example,dc=com",
		"--group-filter", "(objectClass=group)",
		"--groups-autocreate",
		"--groups-claim", "groups",
		"--groups-overwrite",
		"--prompt", "consent",
		"--scopes", "email,profile",
		"--sync-defaults-options", "scope=both",
		"--sync-attributes", "email=mail",
		"--tfa", "oath",
		"--check-connection",
		"--group-classes", "groupOfNames",
		"--group-name-attr", "cn",
		"--query-userinfo",
		"--sslversion", "tlsv1_3",
		"--user-classes", "inetOrgPerson",
	))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "ldap", rec.body["type"])
	require.Equal(t, "preferred_username", rec.body["username-claim"])
	require.Equal(t, "corp directory", rec.body["comment"])
	require.Equal(t, "1", rec.body["default"])
	require.Equal(t, "ldap1.example.com", rec.body["server1"])
	require.Equal(t, "ldap2.example.com", rec.body["server2"])
	require.Equal(t, "636", rec.body["port"])
	require.Equal(t, "ldaps", rec.body["mode"])
	require.Equal(t, "dc=example,dc=com", rec.body["base_dn"])
	require.Equal(t, "cn=svc,dc=example,dc=com", rec.body["bind_dn"])
	require.Equal(t, "uid", rec.body["user_attr"])
	require.Equal(t, "example.com", rec.body["domain"])
	require.Equal(t, "https://idp.example.com", rec.body["issuer-url"])
	require.Equal(t, "pve-client", rec.body["client-id"])
	require.Equal(t, "client-secret", rec.body["client-key"])
	require.Equal(t, "1", rec.body["autocreate"])
	require.Equal(t, "bindpw", rec.body["password"])
	require.Equal(t, "1", rec.body["verify"])
	require.Equal(t, "urn:mace:incommon:iap:silver", rec.body["acr-values"])
	require.Equal(t, "aud1,aud2", rec.body["audiences"])
	require.Equal(t, "/etc/ssl/certs", rec.body["capath"])
	require.Equal(t, "1", rec.body["case-sensitive"])
	require.Equal(t, "/etc/pve/client-cert.pem", rec.body["cert"])
	require.Equal(t, "/etc/pve/client-key.pem", rec.body["certkey"])
	require.Equal(t, "(objectClass=person)", rec.body["filter"])
	require.Equal(t, "ou=groups,dc=example,dc=com", rec.body["group_dn"])
	require.Equal(t, "(objectClass=group)", rec.body["group_filter"])
	require.Equal(t, "1", rec.body["groups-autocreate"])
	require.Equal(t, "groups", rec.body["groups-claim"])
	require.Equal(t, "1", rec.body["groups-overwrite"])
	require.Equal(t, "consent", rec.body["prompt"])
	require.Equal(t, "email,profile", rec.body["scopes"])
	require.Equal(t, "scope=both", rec.body["sync-defaults-options"])
	require.Equal(t, "email=mail", rec.body["sync_attributes"])
	require.Equal(t, "oath", rec.body["tfa"])
	require.Equal(t, "1", rec.body["check-connection"])
	require.Equal(t, "groupOfNames", rec.body["group_classes"])
	require.Equal(t, "cn", rec.body["group_name_attr"])
	require.Equal(t, "1", rec.body["query-userinfo"])
	require.Equal(t, "tlsv1_3", rec.body["sslversion"])
	require.Equal(t, "inetOrgPerson", rec.body["user_classes"])
}

// TestAccessAudit_DomainSet_AllFlags mirrors the create audit for `domain
// set`, which is coded via a separate applyDomainFlagsToUpdate function and
// so could independently drift from create's wire-key mapping. Also covers
// the update-only --delete and --digest flags.
func TestAccessAudit_DomainSet_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/domains/corp-ldap", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "set", "corp-ldap",
		"--comment", "updated",
		"--default=false",
		"--server1", "ldap3.example.com",
		"--server2", "ldap4.example.com",
		"--port", "389",
		"--mode", "ldap+starttls",
		"--base-dn", "dc=updated,dc=com",
		"--bind-dn", "cn=svc2,dc=updated,dc=com",
		"--user-attr", "sAMAccountName",
		"--domain", "updated.com",
		"--issuer-url", "https://idp2.example.com",
		"--client-id", "pve-client-2",
		"--client-key", "client-secret-2",
		"--autocreate=false",
		"--password", "newbindpw",
		"--verify=false",
		"--acr-values", "urn:mace:incommon:iap:bronze",
		"--audiences", "aud3",
		"--capath", "/etc/ssl/certs2",
		"--case-sensitive=false",
		"--cert", "/etc/pve/client-cert2.pem",
		"--certkey", "/etc/pve/client-key2.pem",
		"--filter", "(objectClass=organizationalPerson)",
		"--group-dn", "ou=groups2,dc=updated,dc=com",
		"--group-filter", "(objectClass=groupOfNames)",
		"--groups-autocreate=false",
		"--groups-claim", "roles",
		"--groups-overwrite=false",
		"--prompt", "login",
		"--scopes", "openid,email",
		"--sync-defaults-options", "scope=users",
		"--sync-attributes", "email=userPrincipalName",
		"--tfa", "yubico",
		"--check-connection=false",
		"--group-classes", "group",
		"--group-name-attr", "sAMAccountName",
		"--query-userinfo=false",
		"--sslversion", "tlsv1_2",
		"--user-classes", "user",
		"--delete", "comment,server2",
		"--digest", "abcdef1234567890",
	))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "updated", rec.body["comment"])
	require.Equal(t, "0", rec.body["default"])
	require.Equal(t, "ldap3.example.com", rec.body["server1"])
	require.Equal(t, "ldap4.example.com", rec.body["server2"])
	require.Equal(t, "389", rec.body["port"])
	require.Equal(t, "ldap+starttls", rec.body["mode"])
	require.Equal(t, "dc=updated,dc=com", rec.body["base_dn"])
	require.Equal(t, "cn=svc2,dc=updated,dc=com", rec.body["bind_dn"])
	require.Equal(t, "sAMAccountName", rec.body["user_attr"])
	require.Equal(t, "updated.com", rec.body["domain"])
	require.Equal(t, "https://idp2.example.com", rec.body["issuer-url"])
	require.Equal(t, "pve-client-2", rec.body["client-id"])
	require.Equal(t, "client-secret-2", rec.body["client-key"])
	require.Equal(t, "0", rec.body["autocreate"])
	require.Equal(t, "newbindpw", rec.body["password"])
	require.Equal(t, "0", rec.body["verify"])
	require.Equal(t, "urn:mace:incommon:iap:bronze", rec.body["acr-values"])
	require.Equal(t, "aud3", rec.body["audiences"])
	require.Equal(t, "/etc/ssl/certs2", rec.body["capath"])
	require.Equal(t, "0", rec.body["case-sensitive"])
	require.Equal(t, "/etc/pve/client-cert2.pem", rec.body["cert"])
	require.Equal(t, "/etc/pve/client-key2.pem", rec.body["certkey"])
	require.Equal(t, "(objectClass=organizationalPerson)", rec.body["filter"])
	require.Equal(t, "ou=groups2,dc=updated,dc=com", rec.body["group_dn"])
	require.Equal(t, "(objectClass=groupOfNames)", rec.body["group_filter"])
	require.Equal(t, "0", rec.body["groups-autocreate"])
	require.Equal(t, "roles", rec.body["groups-claim"])
	require.Equal(t, "0", rec.body["groups-overwrite"])
	require.Equal(t, "login", rec.body["prompt"])
	require.Equal(t, "openid,email", rec.body["scopes"])
	require.Equal(t, "scope=users", rec.body["sync-defaults-options"])
	require.Equal(t, "email=userPrincipalName", rec.body["sync_attributes"])
	require.Equal(t, "yubico", rec.body["tfa"])
	require.Equal(t, "0", rec.body["check-connection"])
	require.Equal(t, "group", rec.body["group_classes"])
	require.Equal(t, "sAMAccountName", rec.body["group_name_attr"])
	require.Equal(t, "0", rec.body["query-userinfo"])
	require.Equal(t, "tlsv1_2", rec.body["sslversion"])
	require.Equal(t, "user", rec.body["user_classes"])
	require.Equal(t, "comment,server2", rec.body["delete"])
	require.Equal(t, "abcdef1234567890", rec.body["digest"])
}

// TestAccessAudit_DomainCreate_OmitsUnsetFlags verifies unset domain create
// flags are absent from the body rather than sent as empty strings/zero
// values, so a partial create does not clobber server defaults.
func TestAccessAudit_DomainCreate_OmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "create", "pve-realm", "--type", "pve"))

	for _, key := range []string{
		"username-claim", "comment", "default", "server1", "server2", "port", "mode",
		"base_dn", "bind_dn", "user_attr", "domain", "issuer-url", "client-id", "client-key",
		"autocreate", "password", "verify", "acr-values", "audiences", "capath",
		"case-sensitive", "cert", "certkey", "filter", "group_dn", "group_filter",
		"groups-autocreate", "groups-claim", "groups-overwrite", "prompt", "scopes",
		"sync-defaults-options", "sync_attributes", "tfa", "check-connection",
		"group_classes", "group_name_attr", "query-userinfo", "sslversion", "user_classes",
	} {
		_, present := rec.body[key]
		require.False(t, present, "%s must be omitted when unset", key)
	}
}

// ---------------------------------------------------------------------------
// domain sync
// ---------------------------------------------------------------------------

func TestAccessAudit_DomainSync_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/domains/corp-ldap/sync", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, "UPID:pve1:00000001:00000002:AABBCCDD:domsync::root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "domain", "sync", "corp-ldap",
		"--dry-run",
		"--enable-new",
		"--remove-vanished", "entry;properties;acl",
		"--scope", "both",
	))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "1", rec.body["dry-run"])
	require.Equal(t, "1", rec.body["enable-new"])
	require.Equal(t, "entry;properties;acl", rec.body["remove-vanished"])
	require.Equal(t, "both", rec.body["scope"])
}

// ---------------------------------------------------------------------------
// group create / set
// ---------------------------------------------------------------------------

func TestAccessAudit_GroupCreate_CommentFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/groups", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "group", "create", "auditors", "--comment", "audit group"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "auditors", rec.body["groupid"])
	require.Equal(t, "audit group", rec.body["comment"])
}

func TestAccessAudit_GroupSet_CommentFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/groups/auditors", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "group", "set", "auditors", "--comment", "renamed"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "renamed", rec.body["comment"])
}

// ---------------------------------------------------------------------------
// password set
// ---------------------------------------------------------------------------

func TestAccessAudit_PasswordSet_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/password", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "password", "set",
		"--userid", "alice@pve",
		"--password", "newsecret",
		"--confirmation-password", "oldsecret",
	))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "alice@pve", rec.body["userid"])
	require.Equal(t, "newsecret", rec.body["password"])
	require.Equal(t, "oldsecret", rec.body["confirmation-password"])
}

// ---------------------------------------------------------------------------
// role create / set
// ---------------------------------------------------------------------------

func TestAccessAudit_RoleCreate_PrivsFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/roles", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "create", "CustomRole", "--privs", "VM.Audit,VM.Console"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "CustomRole", rec.body["roleid"])
	require.Equal(t, "VM.Audit,VM.Console", rec.body["privs"])
}

func TestAccessAudit_RoleSet_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/roles/CustomRole", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "set", "CustomRole", "--privs", "VM.Console", "--append"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "VM.Console", rec.body["privs"])
	require.Equal(t, "1", rec.body["append"])
}

// ---------------------------------------------------------------------------
// tfa delete / create / set
// ---------------------------------------------------------------------------

// TestAccessAudit_TfaDelete_PasswordFlag asserts --password reaches DELETE
// /access/tfa/{userid}/{id} under its exact wire key. DELETE requests carry
// their params in the URL query string, not a form/JSON body.
func TestAccessAudit_TfaDelete_PasswordFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var method, password string
	f.HandleFunc("DELETE /api2/json/access/tfa/alice@pve/entry1", func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		password = r.URL.Query().Get("password")
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "delete", "alice@pve", "entry1", "--yes", "--password", "opsecret"))

	require.Equal(t, http.MethodDelete, method)
	require.Equal(t, "opsecret", password)
}

func TestAccessAudit_TfaCreate_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/tfa/alice@pve", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{"id": "totp0"})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "create", "alice@pve",
		"--type", "totp",
		"--description", "phone authenticator",
		"--totp", "otpauth://totp/pve:alice@pve?secret=ABC",
		"--value", "123456",
		"--challenge", "orig-challenge",
		"--password", "opsecret",
	))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "totp", rec.body["type"])
	require.Equal(t, "phone authenticator", rec.body["description"])
	require.Equal(t, "otpauth://totp/pve:alice@pve?secret=ABC", rec.body["totp"])
	require.Equal(t, "123456", rec.body["value"])
	require.Equal(t, "orig-challenge", rec.body["challenge"])
	require.Equal(t, "opsecret", rec.body["password"])
}

func TestAccessAudit_TfaSet_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/tfa/alice@pve/entry1", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "set", "alice@pve", "entry1",
		"--description", "renamed entry",
		"--enable=false",
		"--password", "opsecret",
	))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "renamed entry", rec.body["description"])
	require.Equal(t, "0", rec.body["enable"])
	require.Equal(t, "opsecret", rec.body["password"])
}

// ---------------------------------------------------------------------------
// user token create / set
// ---------------------------------------------------------------------------

func TestAccessAudit_TokenCreate_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/users/alice@pve/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{"full-tokenid": "alice@pve!ci", "value": "secret-value"})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "create", "alice@pve", "ci",
		"--comment", "ci token",
		"--expire", "7200",
		"--privsep=false",
	))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "ci token", rec.body["comment"])
	require.Equal(t, "7200", rec.body["expire"])
	require.Equal(t, "0", rec.body["privsep"])
}

func TestAccessAudit_TokenSet_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/alice@pve/token/ci", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, map[string]any{"comment": "rotated"})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "token", "set", "alice@pve", "ci",
		"--comment", "rotated",
		"--expire", "3600",
		"--privsep",
		"--regenerate",
		"--delete", "comment",
	))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "rotated", rec.body["comment"])
	require.Equal(t, "3600", rec.body["expire"])
	require.Equal(t, "1", rec.body["privsep"])
	require.Equal(t, "1", rec.body["regenerate"])
	require.Equal(t, "comment", rec.body["delete"])
}

// ---------------------------------------------------------------------------
// user create / set
// ---------------------------------------------------------------------------

func TestAccessAudit_UserCreate_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "create", "bob@pve",
		"--password", "initialpw",
		"--firstname", "Bob",
		"--lastname", "Builder",
		"--email", "bob@example.com",
		"--groups", "ops,admins",
		"--expire", "1700000000",
		"--enable=false",
		"--comment", "contractor",
		"--keys", "yubico-key-1",
	))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "bob@pve", rec.body["userid"])
	require.Equal(t, "initialpw", rec.body["password"])
	require.Equal(t, "Bob", rec.body["firstname"])
	require.Equal(t, "Builder", rec.body["lastname"])
	require.Equal(t, "bob@example.com", rec.body["email"])
	require.Equal(t, "ops,admins", rec.body["groups"])
	require.Equal(t, "1700000000", rec.body["expire"])
	require.Equal(t, "0", rec.body["enable"])
	require.Equal(t, "contractor", rec.body["comment"])
	require.Equal(t, "yubico-key-1", rec.body["keys"])
}

func TestAccessAudit_UserSet_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/bob@pve", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "user", "set", "bob@pve",
		"--firstname", "Robert",
		"--lastname", "Builder-Jones",
		"--email", "robert@example.com",
		"--groups", "ops",
		"--expire", "1800000000",
		"--enable",
		"--comment", "full-time",
		"--keys", "yubico-key-2",
		"--append",
	))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "Robert", rec.body["firstname"])
	require.Equal(t, "Builder-Jones", rec.body["lastname"])
	require.Equal(t, "robert@example.com", rec.body["email"])
	require.Equal(t, "ops", rec.body["groups"])
	require.Equal(t, "1800000000", rec.body["expire"])
	require.Equal(t, "1", rec.body["enable"])
	require.Equal(t, "full-time", rec.body["comment"])
	require.Equal(t, "yubico-key-2", rec.body["keys"])
	require.Equal(t, "1", rec.body["append"])
}
