package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

const acmeUPID = "UPID:pve:00001234:00005678:65000000:acme-register::root@pam:"

// TestAcmeAccount_List verifies `pve cluster acme account list` reads
// GET /cluster/acme/account and renders the account names.
func TestAcmeAccount_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/acme/account", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{map[string]any{"name": "default"}})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "account", "list"))
	require.Contains(t, buf.String(), "default")
}

// TestAcmeAccount_Get verifies get reads GET /cluster/acme/account/{name}.
func TestAcmeAccount_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/acme/account/default", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"location":  "https://acme.example/acct/1",
			"tos":       "https://acme.example/tos",
			"directory": "https://acme.example/dir",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "account", "get", "default"))
	require.Contains(t, buf.String(), "acme.example")
}

// TestAcmeAccount_CreateForwardsFields verifies create posts the required
// contact, the positional name, and changed optionals, and returns the UPID
// without blocking when --async is set.
func TestAcmeAccount_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/acme/account", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, acmeUPID)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "account", "create", "staging",
		"--contact", "admin@example.com", "--directory", "https://acme.example/dir", "--async"))
	require.Equal(t, "admin@example.com", gotForm.Get("contact"))
	require.Equal(t, "staging", gotForm.Get("name"))
	require.Equal(t, "https://acme.example/dir", gotForm.Get("directory"))
	_, hasTos := gotForm["tos_url"]
	require.False(t, hasTos, "unset --tos-url must be omitted from the request body")
	require.Contains(t, buf.String(), acmeUPID)
}

// TestAcmeAccount_CreateForwardsEabSecretWithoutEcho verifies the External
// Account Binding HMAC key (a secret) is forwarded to the request body but never
// echoed in the rendered output.
func TestAcmeAccount_CreateForwardsEabSecretWithoutEcho(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/acme/account", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, acmeUPID)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	const secret = "supersecreteabhmac"
	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "account", "create", "staging",
		"--contact", "admin@example.com", "--eab-kid", "kid-1", "--eab-hmac-key", secret, "--async"))
	require.Equal(t, "kid-1", gotForm.Get("eab-kid"))
	require.Equal(t, secret, gotForm.Get("eab-hmac-key"), "the HMAC key must reach the request body")
	require.NotContains(t, buf.String(), secret, "the HMAC key must never be echoed to output")
}

// TestAcmeAccount_CreateBlocksUntilDone covers the default synchronous path:
// without --async the command waits for the registration task to finish and
// renders the success message.
func TestAcmeAccount_CreateBlocksUntilDone(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/cluster/acme/account", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, acmeUPID)
	})
	// WaitTask polls the task status on the node named in the UPID ("pve").
	f.HandleJSON("GET /api2/json/nodes/pve/tasks/"+acmeUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": acmeUPID,
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "account", "create",
		"--contact", "admin@example.com"))
	require.Contains(t, buf.String(), "ACME account registered.")
	require.NotContains(t, buf.String(), acmeUPID, "synchronous create must not print the raw UPID")
}

// TestAcmeAccount_CreateRequiresContact verifies create rejects a missing contact.
func TestAcmeAccount_CreateRequiresContact(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "acme", "account", "create", "staging")
	require.Error(t, err)
	require.Contains(t, err.Error(), "contact")
}

// TestAcmeAccount_SetForwardsContact verifies set issues a PUT with the new contact.
func TestAcmeAccount_SetForwardsContact(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/acme/account/default", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, acmeUPID)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "account", "set", "default",
		"--contact", "ops@example.com", "--async"))
	require.Equal(t, "ops@example.com", gotForm.Get("contact"))
}

// TestAcmeAccount_SetRequiresContact verifies set rejects a missing contact.
func TestAcmeAccount_SetRequiresContact(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/acme/account/default", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, acmeUPID)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "acme", "account", "set", "default")
	require.Error(t, err)
	require.Contains(t, err.Error(), "contact")
	require.False(t, called, "set must not issue a PUT without --contact")
}

// TestAcmeAccount_DeleteRequiresYes verifies delete refuses without --yes.
func TestAcmeAccount_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/acme/account/default", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, acmeUPID)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "acme", "account", "delete", "default")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

// TestAcmeAccount_DeleteWithYes verifies delete issues the DELETE with --yes.
func TestAcmeAccount_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/acme/account/default", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, acmeUPID)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "account", "delete", "default", "--yes", "--async"))
	require.Equal(t, http.MethodDelete, gotMethod)
}

// TestAcmePlugin_List verifies plugin list reads GET /cluster/acme/plugins.
func TestAcmePlugin_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/acme/plugins", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"plugin": "dns-cf", "type": "dns", "api": "cf"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "plugin", "list"))
	out := buf.String()
	require.Contains(t, out, "dns-cf")
	require.Contains(t, out, "dns")
}

// TestAcmePlugin_ListTypeQuery verifies --type is query-encoded.
func TestAcmePlugin_ListTypeQuery(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotType string
	f.HandleFunc("GET /api2/json/cluster/acme/plugins", func(w http.ResponseWriter, r *http.Request) {
		gotType = r.URL.Query().Get("type")
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "plugin", "list", "--type", "dns"))
	require.Equal(t, "dns", gotType)
}

// TestAcmePlugin_Get verifies plugin get reads GET /cluster/acme/plugins/{id}.
func TestAcmePlugin_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/acme/plugins/dns-cf", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"plugin": "dns-cf", "type": "dns", "api": "cf"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "plugin", "get", "dns-cf"))
	require.Contains(t, buf.String(), "cf")
}

// TestAcmePlugin_CreateForwardsFields verifies create posts the required id and
// type plus changed optionals, and omits unset ones.
func TestAcmePlugin_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/acme/plugins", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "plugin", "create", "dns-cf",
		"--type", "dns", "--api", "cf", "--data", "Q0ZfVG9rZW49eA=="))
	require.Equal(t, "dns-cf", gotForm.Get("id"))
	require.Equal(t, "dns", gotForm.Get("type"))
	require.Equal(t, "cf", gotForm.Get("api"))
	require.Equal(t, "Q0ZfVG9rZW49eA==", gotForm.Get("data"))
	_, hasNodes := gotForm["nodes"]
	require.False(t, hasNodes, "unset --nodes must be omitted from the request body")
}

// TestAcmePlugin_CreateRequiresType verifies create rejects a missing type.
func TestAcmePlugin_CreateRequiresType(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "acme", "plugin", "create", "dns-cf", "--api", "cf")
	require.Error(t, err)
	require.Contains(t, err.Error(), "type")
}

// TestAcmePlugin_SetRequiresChange verifies set rejects an empty change set.
func TestAcmePlugin_SetRequiresChange(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/acme/plugins/dns-cf", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "acme", "plugin", "set", "dns-cf")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes")
	require.False(t, called, "set must not issue a PUT with no changed fields")
}

// TestAcmePlugin_SetForwardsChanged verifies set forwards only changed fields.
func TestAcmePlugin_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/acme/plugins/dns-cf", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "plugin", "set", "dns-cf", "--api", "route53"))
	require.Equal(t, "route53", gotForm.Get("api"))
	_, hasData := gotForm["data"]
	require.False(t, hasData, "unset --data must be omitted from the request body")
}

// TestAcmePlugin_DeleteRequiresYes verifies plugin delete refuses without --yes.
func TestAcmePlugin_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/acme/plugins/dns-cf", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "acme", "plugin", "delete", "dns-cf")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

// TestAcmePlugin_DeleteWithYes verifies plugin delete issues the DELETE with --yes.
func TestAcmePlugin_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/acme/plugins/dns-cf", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "plugin", "delete", "dns-cf", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "deleted")
}

// TestAcmeDirectories_List verifies directories reads GET /cluster/acme/directories.
func TestAcmeDirectories_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/acme/directories", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "Let's Encrypt V2", "url": "https://acme-v02.example/dir"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "directories"))
	require.Contains(t, buf.String(), "acme-v02.example")
}

// TestAcmeChallengeSchema_List verifies challenge-schema reads
// GET /cluster/acme/challenge-schema.
func TestAcmeChallengeSchema_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/acme/challenge-schema", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"id": "cf", "name": "Cloudflare", "type": "dns"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "acme", "challenge-schema"))
	require.Contains(t, buf.String(), "Cloudflare")
}

// TestAcmeCommandTree verifies the acme verb set is registered.
func TestAcmeCommandTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	acme := childCommands(root)["acme"]
	require.NotNil(t, acme, "cluster must have an acme command")

	children := childCommands(acme)
	for _, c := range []string{"account", "plugin", "directories", "challenge-schema"} {
		require.Containsf(t, children, c, "acme must have a %s command", c)
	}

	account := childCommands(acme)["account"]
	for _, v := range []string{"list", "get", "create", "set", "delete"} {
		require.Containsf(t, childCommands(account), v, "acme account must have a %s command", v)
	}
	plugin := childCommands(acme)["plugin"]
	for _, v := range []string{"list", "get", "create", "set", "delete"} {
		require.Containsf(t, childCommands(plugin), v, "acme plugin must have a %s command", v)
	}
}
