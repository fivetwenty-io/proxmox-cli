package node_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	_ "github.com/fivetwenty-io/proxmox-cli/internal/cli/node"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// node firewall rules
// ---------------------------------------------------------------------------

func TestNodeFirewallRules_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/firewall/rules", []map[string]any{
		{"pos": 0, "type": "in", "action": "ACCEPT", "proto": "tcp", "dport": "22", "enable": 1, "comment": "ssh"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "list"))

	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "ACCEPT")
	require.Contains(t, out, "ssh")
}

// TestNodeFirewallRules_Get exercises the string-pos workaround: PVE returns
// `pos` as a string on the single-rule GET, so the command bypasses the typed
// method and renders the raw object fetched via Raw.GetCtx.
func TestNodeFirewallRules_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/firewall/rules/3", &rec, map[string]any{
		"pos": "3", "type": "in", "action": "ACCEPT", "proto": "tcp", "dport": "22",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "get", "3"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/firewall/rules/3", rec.path)
	require.Contains(t, buf.String(), "ACCEPT")
}

func TestNodeFirewallRules_RequiredFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "create missing type",
			args:    []string{"--node", "pve1", "node", "firewall", "rules", "create", "--action", "ACCEPT"},
			wantErr: "--type is required",
		},
		{
			name:    "create missing action",
			args:    []string{"--node", "pve1", "node", "firewall", "rules", "create", "--type", "in"},
			wantErr: "--action is required",
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

func TestNodeFirewallRules_CreateForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/nodes/pve1/firewall/rules", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "create",
		"--type", "in", "--action", "ACCEPT", "--proto", "tcp",
		"--dport", "22", "--source", "+pmxcli-ips", "--enable", "0", "--comment", "ssh"))

	require.NoError(t, root.Execute())
	require.Equal(t, "in", gotForm.Get("type"))
	require.Equal(t, "ACCEPT", gotForm.Get("action"))
	require.Equal(t, "tcp", gotForm.Get("proto"))
	require.Equal(t, "22", gotForm.Get("dport"))
	require.Equal(t, "+pmxcli-ips", gotForm.Get("source"))
	require.Equal(t, "0", gotForm.Get("enable"))
	require.Equal(t, "ssh", gotForm.Get("comment"))
}

func TestNodeFirewallRules_UpdateForwardsMoveto(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/nodes/pve1/firewall/rules/2", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "update", "2",
		"--moveto", "0", "--delete", "comment"))

	require.NoError(t, root.Execute())
	require.Equal(t, "0", gotForm.Get("moveto"))
	require.Equal(t, "comment", gotForm.Get("delete"))
}

func TestNodeFirewallRules_DeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "delete", "3"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

func TestNodeFirewallRules_DeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/firewall/rules/3", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "delete", "3", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "deleted")
}

// TestNodeFirewall_RequiresNode verifies that node-scoped firewall sub-commands
// fail clearly when no node is resolvable.
func TestNodeFirewall_RequiresNode(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "rules list",
			args: []string{"node", "firewall", "rules", "list"},
		},
		{
			name: "options get",
			args: []string{"node", "firewall", "options", "get"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
			root.SetArgs(append(prefix, tc.args...))
			err := root.Execute()
			require.Error(t, err)
			require.Contains(t, err.Error(), "no node specified")
		})
	}
}

func TestNodeFirewallRules_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/firewall/rules", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "list"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list firewall rules on node")
}

// ---------------------------------------------------------------------------
// node firewall options
// ---------------------------------------------------------------------------

func TestNodeFirewallOptions_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/firewall/options", map[string]any{
		"enable": 1, "log_level_in": "nolog", "nf_conntrack_max": 262144,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "options", "get"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "nolog")
}

func TestNodeFirewallOptions_SetRequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "options", "set"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no options to set")
}

func TestNodeFirewallOptions_SetForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/nodes/pve1/firewall/options", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "options", "set",
		"--enable", "--log-level-in", "info", "--nf-conntrack-max", "262144", "--nosmurfs=false"))

	require.NoError(t, root.Execute())
	require.Equal(t, "1", gotForm.Get("enable"))
	require.Equal(t, "info", gotForm.Get("log_level_in"))
	require.Equal(t, "262144", gotForm.Get("nf_conntrack_max"))
	require.Equal(t, "0", gotForm.Get("nosmurfs"))
}

// ---------------------------------------------------------------------------
// command tree
// ---------------------------------------------------------------------------

// TestNodeFirewall_CommandTree asserts the firewall sub-tree exposes the
// expected rules and options verb sets under `pmx node firewall`.
func TestNodeFirewall_CommandTree(t *testing.T) {
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
	fw := find(nodeCmd, "firewall")
	require.NotNil(t, fw, "node firewall command must be registered")

	rules := find(fw, "rules")
	require.NotNil(t, rules)
	for _, verb := range []string{"list", "get", "create", "update", "delete"} {
		require.NotNil(t, find(rules, verb), "rules must expose %q", verb)
	}

	options := find(fw, "options")
	require.NotNil(t, options)
	for _, verb := range []string{"get", "set"} {
		require.NotNil(t, find(options, verb), "options must expose %q", verb)
	}
}
