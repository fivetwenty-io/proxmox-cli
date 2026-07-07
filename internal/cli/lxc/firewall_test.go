package lxc

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

// --- firewall rules ---------------------------------------------------------

func TestLxcFirewallRulesList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/rules", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"pos": 0, "type": "in", "action": "ACCEPT", "proto": "tcp", "dport": "22", "enable": 1, "comment": "ssh"},
			map[string]any{"pos": 1, "type": "in", "action": "DROP"},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "list", "100")
	require.NoError(t, run())
	out := buf.String()
	require.Contains(t, out, "POS")
	require.Contains(t, out, "ACCEPT")
	require.Contains(t, out, "ssh")
}

func TestLxcFirewallRulesGet_Single(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/rules/0", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		// PVE returns `pos` as a string on this endpoint (unlike the list view);
		// the raw-fetch path must tolerate that where the typed client cannot.
		testhelper.WriteData(w, map[string]any{"pos": "0", "type": "in", "action": "ACCEPT", "dport": "22"})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "get", "100", "0")
	require.NoError(t, run())
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/rules/0", gotPath)
	require.Contains(t, buf.String(), "ACCEPT")
}

func TestLxcFirewallRulesCreate_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/100/firewall/rules", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "create", "100",
		"--type", "in", "--action", "ACCEPT", "--proto", "tcp",
		"--source", "172.30.0.0/24", "--comment", "ssh")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/rules", gotPath)
	require.Equal(t, "in", body["type"])
	require.Equal(t, "ACCEPT", body["action"])
	require.Equal(t, "tcp", body["proto"])
	require.Equal(t, "172.30.0.0/24", body["source"])
	require.Equal(t, "ssh", body["comment"])
	require.Contains(t, buf.String(), "rule added")
}

func TestLxcFirewallRulesCreate_RequiresFlag(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing --type",
			args:    []string{"firewall", "rules", "create", "100", "--action", "ACCEPT"},
			wantErr: "--type is required",
		},
		{
			name:    "missing --action",
			args:    []string{"firewall", "rules", "create", "100", "--type", "in"},
			wantErr: "--action is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			deps := newDeps(t, f, output.FormatTable, "pve1", false)
			var buf bytes.Buffer
			run := newTestCmd(t, deps, &buf, tc.args...)
			err := run()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLxcFirewallRulesUpdate_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/100/firewall/rules/2", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "update", "100", "2",
		"--action", "DROP", "--moveto", "3", "--delete", "comment")
	require.NoError(t, run())

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/rules/2", gotPath)
	require.Equal(t, "DROP", body["action"])
	require.EqualValues(t, 3, body["moveto"])
	require.Equal(t, "comment", body["delete"])
	require.Contains(t, buf.String(), "updated")
}

func TestLxcFirewallRulesDelete_Confirm(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/lxc/100/firewall/rules/0", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "delete", "100", "0", "--yes")
	require.NoError(t, run())
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/rules/0", gotPath)
	require.Contains(t, buf.String(), "deleted")
}

func TestLxcFirewallRulesDelete_RequiresConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/lxc/100/firewall/rules/0", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "delete", "100", "0")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirm")
	require.False(t, called, "DELETE must not be issued without confirmation")
}

func TestLxcFirewallRulesList_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/rules", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "list", "100")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "list firewall rules")
}

// --- firewall ipset ---------------------------------------------------------

func TestLxcFirewallIpsetList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/ipset", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "trusted", "comment": "trusted nets"},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "list", "100")
	require.NoError(t, run())
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "trusted")
}

func TestLxcFirewallIpsetListMembers_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"cidr": "172.30.0.0/24", "nomatch": false, "comment": "lab"},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "list", "100", "trusted")
	require.NoError(t, run())
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted", gotPath)
	out := buf.String()
	require.Contains(t, out, "CIDR")
	require.Contains(t, out, "172.30.0.0/24")
}

func TestLxcFirewallIpsetCreate_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/100/firewall/ipset", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "create", "100", "trusted", "--comment", "lab nets")
	require.NoError(t, run())
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "trusted", body["name"])
	require.Equal(t, "lab nets", body["comment"])
	require.Contains(t, buf.String(), "created")
}

func TestLxcFirewallIpsetAdd_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "add", "100", "trusted", "172.30.0.0/24", "--nomatch")
	require.NoError(t, run())
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted", gotPath)
	require.Equal(t, "172.30.0.0/24", body["cidr"])
	require.Equal(t, true, body["nomatch"])
	require.Contains(t, buf.String(), "added to IP set")
}

func TestLxcFirewallIpsetRemove_Confirm(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted/172.30.0.0/24", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "remove", "100", "trusted", "172.30.0.0/24", "--yes")
	require.NoError(t, run())
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted/172.30.0.0/24", gotPath)
	require.Contains(t, buf.String(), "removed from IP set")
}

func TestLxcFirewallIpsetRemove_RequiresConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "remove", "100", "trusted", "172.30.0.0/24")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirm")
}

func TestLxcFirewallIpsetDelete_Force(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotQuery string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotQuery = r.Method, r.URL.RawQuery
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "delete", "100", "trusted", "--yes", "--force")
	require.NoError(t, run())
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, gotQuery, "force=1")
	require.Contains(t, buf.String(), "deleted")
}

func TestLxcFirewallIpsetGetMember_Single(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted/172.30.0.0/24", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"cidr": "172.30.0.0/24", "nomatch": 0, "comment": "lab nets", "digest": "abc123",
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "get-member", "100", "trusted", "172.30.0.0/24")
	require.NoError(t, run())
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted/172.30.0.0/24", gotPath)
	out := buf.String()
	require.Contains(t, out, "172.30.0.0/24")
	require.Contains(t, out, "lab nets")
}

func TestLxcFirewallIpsetGetMember_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/ipset/trusted/172.30.0.0/24", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such member")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "get-member", "100", "trusted", "172.30.0.0/24")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "get 172.30.0.0/24 in IP set")
}

// --- firewall alias ---------------------------------------------------------

func TestLxcFirewallAliasList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/aliases", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "gw", "cidr": "172.30.0.1", "comment": "gateway"},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "alias", "list", "100")
	require.NoError(t, run())
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "gw")
	require.Contains(t, out, "172.30.0.1")
}

func TestLxcFirewallAliasGet_Single(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/aliases/gw", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"name": "gw", "cidr": "172.30.0.1", "comment": "gateway", "ipversion": 4, "digest": "abc123",
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "alias", "get", "100", "gw")
	require.NoError(t, run())
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/aliases/gw", gotPath)
	out := buf.String()
	require.Contains(t, out, "gw")
	require.Contains(t, out, "172.30.0.1")
	require.Contains(t, out, "gateway")
}

func TestLxcFirewallAliasGet_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/aliases/gw", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such alias")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "alias", "get", "100", "gw")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "get alias")
}

func TestLxcFirewallAliasCreate_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/100/firewall/aliases", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "alias", "create", "100", "gw", "172.30.0.1", "--comment", "gateway")
	require.NoError(t, run())
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "gw", body["name"])
	require.Equal(t, "172.30.0.1", body["cidr"])
	require.Equal(t, "gateway", body["comment"])
	require.Contains(t, buf.String(), "created")
}

func TestLxcFirewallAliasUpdate_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/100/firewall/aliases/gw", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "alias", "update", "100", "gw", "172.30.0.254", "--rename", "gw2")
	require.NoError(t, run())
	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/aliases/gw", gotPath)
	require.Equal(t, "172.30.0.254", body["cidr"])
	require.Equal(t, "gw2", body["rename"])
	require.Contains(t, buf.String(), "updated")
}

func TestLxcFirewallAliasDelete_RequiresConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "alias", "delete", "100", "gw")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirm")
}

// --- firewall options -------------------------------------------------------

func TestLxcFirewallOptionsGet_Single(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/options", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"enable": 1, "policy_in": "DROP", "policy_out": "ACCEPT", "log_level_in": "info",
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "options", "get", "100")
	require.NoError(t, run())
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/options", gotPath)
	out := buf.String()
	require.Contains(t, out, "policy_in")
	require.Contains(t, out, "DROP")
}

func TestLxcFirewallOptionsSet_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/100/firewall/options", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "options", "set", "100",
		"--enable", "--policy-in", "DROP", "--log-level-in", "info")
	require.NoError(t, run())

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/100/firewall/options", gotPath)
	require.Equal(t, true, body["enable"])
	require.Equal(t, "DROP", body["policy_in"])
	require.Equal(t, "info", body["log_level_in"])
	require.Contains(t, buf.String(), "updated")
}

func TestLxcFirewallOptionsSet_RequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "options", "set", "100")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no options to set")
}

// --- command tree + flag-collision regression -------------------------------

func TestLxcFirewallCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	var fw *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "firewall" {
			fw = c
		}
	}
	require.NotNil(t, fw, "firewall sub-command must be registered")

	sub := make(map[string]*cobra.Command)
	for _, c := range fw.Commands() {
		sub[c.Name()] = c
	}
	for _, want := range []string{"rules", "ipset", "alias", "options"} {
		require.Contains(t, sub, want, "expected firewall sub-command %q", want)
	}

	rulesVerbs := make(map[string]bool)
	for _, c := range sub["rules"].Commands() {
		rulesVerbs[c.Name()] = true
	}
	for _, want := range []string{"list", "get", "create", "update", "delete"} {
		require.True(t, rulesVerbs[want], "expected rules verb %q", want)
	}
}

// TestLxcFirewall_NoLocalTargetFlag guards against shadowing the root's
// persistent -t/--target selector with a local --target on any firewall command.
func TestLxcFirewall_NoLocalTargetFlag(t *testing.T) {
	root := Group(&cli.Deps{})
	var fw *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "firewall" {
			fw = c
		}
	}
	require.NotNil(t, fw)

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
	walk(fw)
}
