package node_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---- list (pending updates) ------------------------------------------------

func TestNodeApt_ListUpdates(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/apt/update", &rec, []any{
		map[string]any{
			"Package": "pve-manager", "OldVersion": "8.1.3", "Version": "8.1.4",
			"Priority": "important", "Origin": "Proxmox",
		},
		map[string]any{
			"Package": "bash", "OldVersion": "5.2-1", "Version": "5.2-2",
			"Priority": "standard", "Origin": "Debian",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "list"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/apt/update", rec.path)
	out := buf.String()
	require.Contains(t, out, "PACKAGE")
	require.Contains(t, out, "CANDIDATE")
	require.Contains(t, out, "pve-manager")
	require.Contains(t, out, "8.1.4")
	// Rows are sorted by package name: bash precedes pve-manager.
	require.Less(t, strings.Index(out, "bash"), strings.Index(out, "pve-manager"))
}

func TestNodeApt_ListUpdates_JSONLossless(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/apt/update", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"Package": "bash", "Version": "5.2-2", "Section": "shells"},
		})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "list"))

	require.NoError(t, root.Execute())
	// Fields outside the table columns (Section) must survive in JSON output.
	require.Contains(t, buf.String(), "shells")
}

// ---- versions (installed) --------------------------------------------------

func TestNodeApt_Versions(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/apt/versions", &rec, []any{
		map[string]any{
			"Package": "pve-kernel", "Version": "6.5.11-7", "CurrentState": "Installed",
			"Priority": "optional", "Origin": "Proxmox",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "versions"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/apt/versions", rec.path)
	out := buf.String()
	require.Contains(t, out, "STATE")
	require.Contains(t, out, "pve-kernel")
	require.Contains(t, out, "Installed")
}

// ---- changelog -------------------------------------------------------------

func TestNodeApt_Changelog(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/apt/changelog", &rec,
		"pve-manager (8.1.4) bookworm; urgency=medium\n  * bug fixes\n")

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "changelog", "--name", "pve-manager"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, rec.query, "name=pve-manager")
	require.Contains(t, buf.String(), "urgency=medium")
}

func TestNodeApt_Changelog_RequiresName(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "changelog"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "name")
}

// TestNodeApt_Changelog_NonStringFallback covers the branch where the changelog
// body is not a bare JSON string: the raw bytes are rendered verbatim.
func TestNodeApt_Changelog_NonStringFallback(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/apt/changelog", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"unexpected": "shape"})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "changelog", "--name", "bash"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "unexpected")
}

// ---- update ----------------------------------------------------------------

func TestNodeApt_Update_BlocksUntilDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:aptupdate::root@pam:"
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/apt/update", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "update", "--notify"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/apt/update", rec.path)
	require.Contains(t, rec.body, "notify=1")
	// --quiet was not passed, so it must be omitted from the request.
	require.NotContains(t, rec.body, "quiet")
	require.Contains(t, buf.String(), "refreshed")
}

func TestNodeApt_Update_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:aptupdate::root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/apt/update", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "apt", "update"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

// ---- repositories ----------------------------------------------------------

func TestNodeApt_ReposList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/apt/repositories", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"digest": "deadbeef",
			"standard-repos": []any{
				map[string]any{"handle": "enterprise", "name": "Enterprise", "status": nil},
				map[string]any{"handle": "no-subscription", "name": "No-Subscription", "status": 1},
				map[string]any{"handle": "test", "name": "Test", "status": 0},
			},
		})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "repositories", "list"))

	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "HANDLE")
	require.Contains(t, out, "enterprise")
	require.Contains(t, out, "not configured")
	require.Contains(t, out, "enabled")
	require.Contains(t, out, "disabled")
}

func TestNodeApt_ReposList_AliasRepos(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/apt/repositories", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"digest": "x", "standard-repos": []any{}})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	// `repos` is an alias for `repositories`.
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "repos", "list"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "HANDLE")
}

func TestNodeApt_ReposAdd_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/apt/repositories", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "repositories", "add", "--handle", "no-subscription"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no API call must be made without confirmation")
}

func TestNodeApt_ReposAdd_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/nodes/pve1/apt/repositories", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "repositories", "add",
		"--handle", "no-subscription", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.body, "handle=no-subscription")
	// --digest was not passed, so it must be omitted.
	require.NotContains(t, rec.body, "digest")
	require.Contains(t, buf.String(), "added")
}

func TestNodeApt_ReposEnable_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/apt/repositories", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "repositories", "enable",
		"--path", "/etc/apt/sources.list", "--index", "0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeApt_ReposEnable_Disable(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/apt/repositories", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "repositories", "enable",
		"--path", "/etc/apt/sources.list.d/pve.list", "--index", "1", "--enabled=false", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "path=")
	require.Contains(t, rec.body, "index=1")
	require.Contains(t, rec.body, "enabled=0")
	require.Contains(t, buf.String(), "disabled")
}

func TestNodeApt_ReposEnable_OmitsUnsetEnabled(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/apt/repositories", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "repositories", "enable",
		"--path", "/etc/apt/sources.list", "--index", "0", "--yes"))

	require.NoError(t, root.Execute())
	// --enabled defaults to true but was not explicitly set, so it is not forwarded.
	require.NotContains(t, rec.body, "enabled")
}

// ---- node scoping + command tree -------------------------------------------

func TestNodeApt_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "apt", "list"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeApt_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	addNodeGroup(root)

	find := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	nodeCmd := find(root, "node")
	require.NotNil(t, nodeCmd)
	apt := find(nodeCmd, "apt")
	require.NotNil(t, apt, "node apt command must be registered")

	for _, verb := range []string{"list", "versions", "changelog", "update", "repositories"} {
		require.NotNil(t, find(apt, verb), "apt must expose %q", verb)
	}
	repos := find(apt, "repositories")
	for _, verb := range []string{"list", "add", "enable"} {
		require.NotNil(t, find(repos, verb), "apt repositories must expose %q", verb)
	}
}
