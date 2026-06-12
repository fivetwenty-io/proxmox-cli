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

// TestHaResourceList_Table verifies `pve cluster ha resource list` queries GET
// /cluster/ha/resources and renders the curated columns.
func TestHaResourceList_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/resources", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"sid": "vm:100", "state": "started", "group": "ha1",
				"max_restart": 2, "max_relocate": 1, "comment": "web",
			},
			map[string]any{"sid": "ct:101", "state": "stopped"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "resource", "list"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/ha/resources", gotPath)

	out := buf.String()
	require.Contains(t, out, "SID")
	require.Contains(t, out, "vm:100")
	require.Contains(t, out, "started")
	require.Contains(t, out, "ha1")
	require.Contains(t, out, "ct:101")
	require.Contains(t, out, "web")
}

// TestHaResourceList_TypeFilter verifies the --type flag is forwarded as a query
// parameter on the list request.
func TestHaResourceList_TypeFilter(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotType string
	f.HandleFunc("GET /api2/json/cluster/ha/resources", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotType = r.Form.Get("type")
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "resource", "list", "--type", "vm"))
	require.Equal(t, "vm", gotType)
}

// TestHaResourceGet_Single verifies `pve cluster ha resource get <sid>` reads the
// per-resource path and surfaces the fields.
func TestHaResourceGet_Single(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ha/resources/vm:100", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"sid": "vm:100", "type": "vm", "state": "started", "group": "ha1", "digest": "abc",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "resource", "get", "vm:100"))

	require.Equal(t, "/api2/json/cluster/ha/resources/vm:100", gotPath)
	out := buf.String()
	require.Contains(t, out, "vm:100")
	require.Contains(t, out, "started")
}

// TestHaResourceCreate_PostsFields verifies `pve cluster ha resource create <sid>`
// POSTs the sid plus the changed attributes.
func TestHaResourceCreate_PostsFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/ha/resources", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "resource", "create", "vm:100",
		"--state", "started", "--group", "ha1", "--max-restart", "3", "--comment", "web"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "vm:100", gotForm.Get("sid"))
	require.Equal(t, "started", gotForm.Get("state"))
	require.Equal(t, "ha1", gotForm.Get("group"))
	require.Equal(t, "3", gotForm.Get("max_restart"))
	require.Equal(t, "web", gotForm.Get("comment"))
	// Untouched attributes must not be sent.
	require.Empty(t, gotForm.Get("max_relocate"))
	require.Contains(t, buf.String(), "vm:100")
}

// TestHaResourceSet_PutsChangedFields verifies `pve cluster ha resource set <sid>`
// issues a PUT carrying only the changed fields plus --delete.
func TestHaResourceSet_PutsChangedFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/ha/resources/vm:100", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "resource", "set", "vm:100",
		"--state", "stopped", "--delete", "comment"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/cluster/ha/resources/vm:100", gotPath)
	require.Equal(t, "stopped", gotForm.Get("state"))
	require.Equal(t, "comment", gotForm.Get("delete"))
	require.Empty(t, gotForm.Get("group"))
	require.Contains(t, buf.String(), "updated")
}

// TestHaResourceDelete_RequiresYes verifies the delete guard refuses without
// --yes and issues a DELETE once confirmed.
func TestHaResourceDelete_RequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("DELETE /api2/json/cluster/ha/resources/vm:100", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "resource", "delete", "vm:100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not be issued without --yes")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "ha", "resource", "delete", "vm:100", "--yes"))
	require.True(t, called)
	require.Contains(t, buf.String(), "deleted")
}

// TestHaResourceMigrate_PostsTargetNode verifies `migrate <sid> --target-node`
// POSTs to the migrate endpoint with the node and renders the response.
func TestHaResourceMigrate_PostsTargetNode(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/ha/resources/vm:100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, map[string]any{
			"sid": "vm:100", "requested-node": "pve2",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "resource", "migrate", "vm:100", "--target-node", "pve2"))

	require.Equal(t, "/api2/json/cluster/ha/resources/vm:100/migrate", gotPath)
	require.Equal(t, "pve2", gotForm.Get("node"))
	require.Contains(t, buf.String(), "pve2")
}

// TestHaResourceMigrate_RequiresTargetNode verifies migrate refuses without a
// --target-node and never issues the request.
func TestHaResourceMigrate_RequiresTargetNode(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("POST /api2/json/cluster/ha/resources/vm:100/migrate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "resource", "migrate", "vm:100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--target-node")
	require.False(t, called, "migrate must not be issued without --target-node")
}

// TestHaResourceRelocate_PostsTargetNode verifies `relocate <sid> --target-node`
// POSTs to the relocate endpoint with the node.
func TestHaResourceRelocate_PostsTargetNode(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/ha/resources/vm:100/relocate", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, map[string]any{
			"sid": "vm:100", "requested-node": "pve2",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ha", "resource", "relocate", "vm:100", "--target-node", "pve2"))

	require.Equal(t, "/api2/json/cluster/ha/resources/vm:100/relocate", gotPath)
	require.Equal(t, "pve2", gotForm.Get("node"))
}

// TestHaResourceRelocate_RequiresTargetNode verifies relocate refuses without a
// --target-node and never issues the request.
func TestHaResourceRelocate_RequiresTargetNode(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("POST /api2/json/cluster/ha/resources/vm:100/relocate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ha", "resource", "relocate", "vm:100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--target-node")
	require.False(t, called, "relocate must not be issued without --target-node")
}

// TestHaResourceList_ServerError verifies a server failure on list surfaces an error.
func TestHaResourceList_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ha/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "ha", "resource", "list"))
}

// TestHaCommandTree verifies the ha → resource sub-tree exposes the expected verbs.
func TestHaCommandTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var ha *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "ha" {
			ha = c
		}
	}
	require.NotNil(t, ha, "cluster must expose an ha sub-command")

	var res *cobra.Command
	for _, c := range ha.Commands() {
		if c.Name() == "resource" {
			res = c
		}
	}
	require.NotNil(t, res, "ha must expose a resource sub-command")

	names := make(map[string]bool)
	for _, c := range res.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"list", "get", "create", "set", "delete", "migrate", "relocate"} {
		require.True(t, names[want], "expected ha resource sub-command %q", want)
	}
}
