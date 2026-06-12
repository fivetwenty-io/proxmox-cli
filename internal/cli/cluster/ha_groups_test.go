package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestHaGroupList_Table verifies `pve cluster ha group list` queries
// GET /cluster/ha/groups and renders the curated columns.
func TestHaGroupList_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/groups", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"group": "ha1", "nodes": "pve:2,pve2:1", "restricted": 1,
				"nofailback": 0, "type": "group", "comment": "primary",
			},
			map[string]any{"group": "ha2", "nodes": "pve"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "group", "list"))

	require.Equal(t, "/api2/json/cluster/ha/groups", gotPath)
	out := buf.String()
	require.Contains(t, out, "GROUP")
	require.Contains(t, out, "ha1")
	require.Contains(t, out, "pve:2,pve2:1")
	require.Contains(t, out, "primary")
	require.Contains(t, out, "ha2")
}

// TestHaGroupGet_Single verifies `pve cluster ha group get <group>` reads the
// per-group path and surfaces its fields.
func TestHaGroupGet_Single(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/groups/ha1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"group": "ha1", "nodes": "pve:2", "restricted": 1, "digest": "abc",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "group", "get", "ha1"))

	require.Equal(t, "/api2/json/cluster/ha/groups/ha1", gotPath)
	require.Contains(t, buf.String(), "ha1")
}

// TestHaGroupCreate_PostsFields verifies create POSTs the group id, the required
// nodes list, and the changed attributes.
func TestHaGroupCreate_PostsFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/ha/groups", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "group", "create", "ha1",
		"--nodes", "pve:2,pve2", "--comment", "primary", "--restricted"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "ha1", gotForm.Get("group"))
	require.Equal(t, "pve:2,pve2", gotForm.Get("nodes"))
	require.Equal(t, "primary", gotForm.Get("comment"))
	require.Equal(t, "1", gotForm.Get("restricted"))
	require.Contains(t, buf.String(), "ha1")
}

// TestHaGroupCreate_RequiresNodes verifies create refuses without --nodes and
// never issues the request.
func TestHaGroupCreate_RequiresNodes(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("POST /api2/json/cluster/ha/groups", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "group", "create", "ha1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--nodes")
	require.False(t, called, "create must not be issued without --nodes")
}

// TestHaGroupSet_PutsChangedFields verifies set issues a PUT carrying only the
// changed fields plus --delete.
func TestHaGroupSet_PutsChangedFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/ha/groups/ha1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "group", "set", "ha1",
		"--nodes", "pve", "--delete", "comment"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/cluster/ha/groups/ha1", gotPath)
	require.Equal(t, "pve", gotForm.Get("nodes"))
	require.Equal(t, "comment", gotForm.Get("delete"))
	require.Empty(t, gotForm.Get("restricted"))
	require.Contains(t, buf.String(), "updated")
}

// TestHaGroupDelete_RequiresYes verifies the delete guard refuses without --yes
// and issues a DELETE once confirmed.
func TestHaGroupDelete_RequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("DELETE /api2/json/cluster/ha/groups/ha1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "group", "delete", "ha1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not be issued without --yes")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "ha", "group", "delete", "ha1", "--yes"))
	require.True(t, called)
	require.Contains(t, buf.String(), "deleted")
}

// TestHaRuleList_Table verifies `pve cluster ha rule list` queries
// GET /cluster/ha/rules and renders the curated columns.
func TestHaRuleList_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/rules", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"rule": "keep-apart", "type": "resource-affinity", "affinity": "negative",
				"resources": "vm:100,vm:101", "strict": 1, "disable": 0, "comment": "spread",
			},
			map[string]any{"rule": "pin", "type": "node-affinity", "nodes": "pve:2"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "rule", "list"))

	require.Equal(t, "/api2/json/cluster/ha/rules", gotPath)
	out := buf.String()
	require.Contains(t, out, "RULE")
	require.Contains(t, out, "keep-apart")
	require.Contains(t, out, "resource-affinity")
	require.Contains(t, out, "vm:100,vm:101")
	require.Contains(t, out, "spread")
	require.Contains(t, out, "pin")
}

// TestHaRuleList_Filters verifies the --resource and --type flags are forwarded
// as query parameters on the list request.
func TestHaRuleList_Filters(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotResource, gotType string
	f.HandleFunc("GET /api2/json/cluster/ha/rules", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotResource = r.Form.Get("resource")
		gotType = r.Form.Get("type")
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "rule", "list", "--resource", "vm:100", "--type", "node-affinity"))
	require.Equal(t, "vm:100", gotResource)
	require.Equal(t, "node-affinity", gotType)
}

// TestHaRuleGet_RawObject verifies get fetches the raw per-rule object so every
// field is surfaced (the typed client method decodes only rule/type).
func TestHaRuleGet_RawObject(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/rules/pin", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"rule": "pin", "type": "node-affinity", "resources": "vm:100", "nodes": "pve:2", "strict": 1,
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "rule", "get", "pin"))

	require.Equal(t, "/api2/json/cluster/ha/rules/pin", gotPath)
	out := buf.String()
	require.Contains(t, out, "pin")
	require.Contains(t, out, "vm:100")
	require.Contains(t, out, "node-affinity")
}

// TestHaRuleCreate_PostsFields verifies create POSTs the rule id, required type
// and resources, plus changed attributes.
func TestHaRuleCreate_PostsFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/ha/rules", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "rule", "create", "keep-apart",
		"--type", "resource-affinity", "--resources", "vm:100,vm:101",
		"--affinity", "negative", "--strict"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "keep-apart", gotForm.Get("rule"))
	require.Equal(t, "resource-affinity", gotForm.Get("type"))
	require.Equal(t, "vm:100,vm:101", gotForm.Get("resources"))
	require.Equal(t, "negative", gotForm.Get("affinity"))
	require.Equal(t, "1", gotForm.Get("strict"))
	require.Contains(t, buf.String(), "keep-apart")
}

// TestHaRuleCreate_RequiresTypeAndResources verifies create refuses when either
// required flag is missing and never issues the request.
func TestHaRuleCreate_RequiresTypeAndResources(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("POST /api2/json/cluster/ha/rules", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "rule", "create", "r1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--type")

	buf.Reset()
	err = run(deps, &buf, "ha", "rule", "create", "r1", "--type", "node-affinity")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--resources")
	require.False(t, called, "create must not be issued when a required flag is missing")
}

// TestHaRuleSet_PutsChangedFields verifies set issues a PUT carrying the required
// type plus the changed fields.
func TestHaRuleSet_PutsChangedFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/ha/rules/pin", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "rule", "set", "pin",
		"--type", "node-affinity", "--nodes", "pve:3", "--disable"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/cluster/ha/rules/pin", gotPath)
	require.Equal(t, "node-affinity", gotForm.Get("type"))
	require.Equal(t, "pve:3", gotForm.Get("nodes"))
	require.Equal(t, "1", gotForm.Get("disable"))
	require.Contains(t, buf.String(), "updated")
}

// TestHaRuleSet_RequiresType verifies set refuses without --type.
func TestHaRuleSet_RequiresType(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("PUT /api2/json/cluster/ha/rules/pin", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "rule", "set", "pin", "--nodes", "pve")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--type")
	require.False(t, called, "set must not be issued without --type")
}

// TestHaRuleDelete_RequiresYes verifies the delete guard refuses without --yes
// and issues a DELETE once confirmed.
func TestHaRuleDelete_RequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("DELETE /api2/json/cluster/ha/rules/pin", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "rule", "delete", "pin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not be issued without --yes")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "ha", "rule", "delete", "pin", "--yes"))
	require.True(t, called)
	require.Contains(t, buf.String(), "deleted")
}

// TestHaGroupRuleTree verifies the ha sub-tree exposes group, rule, and status
// sub-commands with their expected verbs.
func TestHaGroupRuleTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var ha *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "ha" {
			ha = c
		}
	}
	require.NotNil(t, ha, "cluster must expose an ha sub-command")

	sub := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	for _, branch := range []struct {
		name  string
		verbs []string
	}{
		{"group", []string{"list", "get", "create", "set", "delete"}},
		{"rule", []string{"list", "get", "create", "set", "delete"}},
		{"status", []string{"list", "current", "manager", "arm", "disarm"}},
	} {
		b := sub(ha, branch.name)
		require.NotNil(t, b, "ha must expose a %q sub-command", branch.name)
		names := make(map[string]bool)
		for _, c := range b.Commands() {
			names[c.Name()] = true
		}
		for _, want := range branch.verbs {
			require.True(t, names[want], "expected ha %s sub-command %q", branch.name, want)
		}
	}
}
