package node_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	_ "github.com/fivetwenty-io/pmx-cli/internal/cli/node"
	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// node network list / get
// ---------------------------------------------------------------------------

func TestNodeNetwork_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/network", []map[string]any{
		{"iface": "vmbr0", "type": "bridge", "method": "static", "active": 1, "autostart": 1, "cidr": "10.0.0.5/24", "gateway": "10.0.0.1"},
		{"iface": "eth0", "type": "eth", "method": "manual", "active": 1, "autostart": 0},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "list"))

	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "vmbr0")
	require.Contains(t, out, "bridge")
	require.Contains(t, out, "10.0.0.5/24")
	require.Contains(t, out, "eth0")
}

// TestNodeNetwork_ListForwardsType verifies the --type filter reaches the query.
func TestNodeNetwork_ListForwardsType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/network", &rec, []map[string]any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "list", "--type", "bridge"))

	require.NoError(t, root.Execute())
	q, err := url.ParseQuery(rec.query)
	require.NoError(t, err)
	require.Equal(t, "bridge", q.Get("type"))
}

// TestNodeNetwork_Get exercises the empty-struct workaround: the typed
// GetNetwork response decodes only method and type, so the command bypasses it
// and renders the raw object fetched via Raw.GetCtx.
func TestNodeNetwork_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/network/vmbr0", &rec, map[string]any{
		"type": "bridge", "method": "static", "address": "10.0.0.5",
		"netmask": "255.255.255.0", "gateway": "10.0.0.1", "bridge_ports": "eth0",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "get", "vmbr0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/network/vmbr0", rec.path)
	out := buf.String()
	// The raw render must preserve fields the typed struct discards.
	require.Contains(t, out, "10.0.0.5")
	require.Contains(t, out, "eth0")
}

// ---------------------------------------------------------------------------
// node network create
// ---------------------------------------------------------------------------

func TestNodeNetwork_RequiredFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "create missing iface",
			args:    []string{"--node", "pve1", "node", "network", "create", "--type", "bridge"},
			wantErr: "--iface is required",
		},
		{
			name:    "create missing type",
			args:    []string{"--node", "pve1", "node", "network", "create", "--iface", "vmbr1"},
			wantErr: "--type is required",
		},
		{
			name:    "update missing type",
			args:    []string{"--node", "pve1", "node", "network", "set", "vmbr0", "--cidr", "10.0.0.6/24"},
			wantErr: "--type is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
			root.SetArgs(append(prefix, tc.args...))
			err := root.Execute()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestNodeNetwork_CreateForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/nodes/pve1/network", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "create",
		"--iface", "vmbr1", "--type", "bridge", "--cidr", "172.30.0.1/24",
		"--bridge-ports", "none", "--bridge-vlan-aware", "--autostart", "--mtu", "1500"))

	require.NoError(t, root.Execute())
	require.Equal(t, "vmbr1", gotForm.Get("iface"))
	require.Equal(t, "bridge", gotForm.Get("type"))
	require.Equal(t, "172.30.0.1/24", gotForm.Get("cidr"))
	require.Equal(t, "none", gotForm.Get("bridge_ports"))
	require.Equal(t, "1", gotForm.Get("bridge_vlan_aware"))
	require.Equal(t, "1", gotForm.Get("autostart"))
	require.Equal(t, "1500", gotForm.Get("mtu"))
	// Unset optional flags must be omitted from the body.
	_, hasGateway := gotForm["gateway"]
	require.False(t, hasGateway, "gateway must be omitted when unset")
	require.Contains(t, buf.String(), "staged")
}

// ---------------------------------------------------------------------------
// node network set
// ---------------------------------------------------------------------------

func TestNodeNetwork_UpdateForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/nodes/pve1/network/vmbr0", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "set", "vmbr0",
		"--type", "bridge", "--cidr", "10.0.0.6/24", "--delete", "gateway"))

	require.NoError(t, root.Execute())
	require.Equal(t, "bridge", gotForm.Get("type"))
	require.Equal(t, "10.0.0.6/24", gotForm.Get("cidr"))
	require.Equal(t, "gateway", gotForm.Get("delete"))
	// Untouched optional flags must be omitted.
	_, hasAddress := gotForm["address"]
	require.False(t, hasAddress, "address must be omitted when unset")
}

// ---------------------------------------------------------------------------
// node network delete / apply / revert
// ---------------------------------------------------------------------------

func TestNodeNetwork_DeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "delete", "vmbr1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

func TestNodeNetwork_DeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/network/vmbr1", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "delete", "vmbr1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/network/vmbr1", rec.path)
	require.Contains(t, buf.String(), "deleted")
}

func TestNodeNetwork_ApplyRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "apply"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

// TestNodeNetwork_ApplyRendersMessage drives the reload through the synchronous
// (non-UPID) path: the fake returns an empty body, so the success message is
// rendered directly rather than waiting on a task.
func TestNodeNetwork_ApplyRendersMessage(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/nodes/pve1/network", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "apply", "--yes", "--regenerate-frr"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/network", rec.path)
	require.Contains(t, rec.body, "regenerate-frr")
	require.Contains(t, buf.String(), "applied")
}

// TestNodeNetwork_ApplyAsyncReturnsUPID drives the reload through the UPID +
// --async branch: the PUT returns a task UPID and --async echoes it without
// waiting on the task status endpoint.
func TestNodeNetwork_ApplyAsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:srvreload:networking:root@pam:"
	f.HandleFunc("PUT /api2/json/nodes/pve1/network", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "network", "apply", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

// TestNodeNetwork_ApplyWaitsForTask drives the reload through the UPID +
// synchronous branch: the PUT returns a UPID, the command blocks on the task
// status endpoint, and only renders the done message after the task ends OK.
func TestNodeNetwork_ApplyWaitsForTask(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:srvreload:networking:root@pam:"
	f.HandleFunc("PUT /api2/json/nodes/pve1/network", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "apply", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "applied")
}

func TestNodeNetwork_RevertRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "revert"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

func TestNodeNetwork_RevertWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/network", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "revert", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/network", rec.path)
	require.Contains(t, buf.String(), "reverted")
}

// TestNodeNetwork_RequiresNode verifies a node-scoped command fails clearly
// when no node is resolvable.
func TestNodeNetwork_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "network", "list"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// ---------------------------------------------------------------------------
// command tree
// ---------------------------------------------------------------------------

// TestNodeNetwork_CommandTree asserts the network sub-tree exposes the expected
// verb set under `pmx node network`.
func TestNodeNetwork_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
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
	network := find(nodeCmd, "network")
	require.NotNil(t, network, "node network command must be registered")

	for _, verb := range []string{"list", "get", "create", "set", "delete", "apply", "revert"} {
		require.NotNil(t, find(network, verb), "network must expose %q", verb)
	}
}
