package cluster

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestClusterConfigJoin_List verifies `pmx cluster config join list` reads
// GET /cluster/config/join and renders the join information.
func TestClusterConfigJoin_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/config/join", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"config_digest": "abc123", "preferred_node": "pve1", "nodelist": []any{}, "totem": map[string]any{},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "join", "list"))
	require.Contains(t, buf.String(), "pve1")
}

// TestClusterConfigNodes_List verifies `pmx cluster config nodes list` reads
// GET /cluster/config/nodes and renders the member rows.
func TestClusterConfigNodes_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/config/nodes", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"node": "pve1", "nodeid": 1, "quorum_votes": 1},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "nodes", "list"))
	require.Contains(t, buf.String(), "pve1")
}

// TestClusterConfigJoin_AddRequiresYes verifies join add refuses to change
// membership without --yes, even when the required peer flags are supplied.
func TestClusterConfigJoin_AddRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/config/join", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "join", "add",
		"--hostname", "pve1.example.com", "--fingerprint", "AA:BB", "--password", "secret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "join must not POST without --yes")
}

// TestClusterConfigJoin_AddWithYes verifies join add POSTs to /cluster/config/join
// once confirmed and renders the initiated message.
func TestClusterConfigJoin_AddWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("POST /api2/json/cluster/config/join", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "join", "add",
		"--hostname", "pve1.example.com", "--fingerprint", "AA:BB", "--password", "secret", "--yes"))
	require.Equal(t, http.MethodPost, gotMethod)
	require.Contains(t, buf.String(), "initiated")
}

// TestClusterConfigNodes_AddRequiresYes verifies nodes add refuses without --yes.
func TestClusterConfigNodes_AddRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/config/nodes/pve2", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "nodes", "add", "pve2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "nodes add must not POST without --yes")
}

// TestClusterConfigNodes_AddWithYes verifies nodes add POSTs to
// /cluster/config/nodes/<node> once confirmed and renders the response.
func TestClusterConfigNodes_AddWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("POST /api2/json/cluster/config/nodes/pve2", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, map[string]any{"corosync_authkey": "abc-key", "corosync_conf": "conf"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "nodes", "add", "pve2", "--yes"))
	require.Equal(t, http.MethodPost, gotMethod)
	require.Contains(t, buf.String(), "abc-key")
}

// TestClusterConfigNodes_DeleteRequiresYes verifies nodes delete refuses
// without --yes.
func TestClusterConfigNodes_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/config/nodes/pve2", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "nodes", "delete", "pve2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "nodes delete must not DELETE without --yes")
}

// TestClusterConfigNodes_DeleteWithYes verifies the DELETE is issued once
// confirmed.
func TestClusterConfigNodes_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/config/nodes/pve2", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "nodes", "delete", "pve2", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "removed")
}

// TestClusterI15CommandTree verifies the options, config, and replication
// sub-trees expose their expected verbs.
func TestClusterI15CommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	top := childCommands(root)

	require.Contains(t, top, "options")
	require.Contains(t, top, "config")
	require.Contains(t, top, "replication")

	optVerbs := childCommands(top["options"])
	for _, v := range []string{"get", "set"} {
		require.Contains(t, optVerbs, v, "options must expose %q", v)
	}

	replVerbs := childCommands(top["replication"])
	for _, v := range []string{"list", "create", "get", "set", "delete"} {
		require.Contains(t, replVerbs, v, "replication must expose %q", v)
	}

	cfgGroups := childCommands(top["config"])
	require.Contains(t, cfgGroups, "join")
	require.Contains(t, cfgGroups, "nodes")
	joinVerbs := childCommands(cfgGroups["join"])
	require.Contains(t, joinVerbs, "list")
	require.Contains(t, joinVerbs, "add")
	nodeVerbs := childCommands(cfgGroups["nodes"])
	for _, v := range []string{"list", "add", "delete"} {
		require.Contains(t, nodeVerbs, v, "config nodes must expose %q", v)
	}
}

// childCommands maps a command's immediate children by name.
func childCommands(cmd *cobra.Command) map[string]*cobra.Command {
	out := make(map[string]*cobra.Command)
	for _, c := range cmd.Commands() {
		out[c.Name()] = c
	}
	return out
}
