package qemu

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

// --- firewall rules ---------------------------------------------------------

func TestQemuFirewallRulesList_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/rules", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"pos": 0, "type": "in", "action": "ACCEPT", "proto": "tcp", "dport": "22", "enable": 1, "comment": "ssh"},
			map[string]any{"pos": 1, "type": "in", "action": "DROP"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "list", "100"))
	out := buf.String()
	require.Contains(t, out, "POS")
	require.Contains(t, out, "ACCEPT")
	require.Contains(t, out, "ssh")
}

func TestQemuFirewallRulesGet_Single(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/rules/0", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		// PVE returns `pos` as a string on this endpoint (unlike the list view);
		// the raw-fetch path must tolerate that where the typed client cannot.
		testhelper.WriteData(w, map[string]any{"pos": "0", "type": "in", "action": "ACCEPT", "dport": "22"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "get", "100", "0"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/rules/0", gotPath)
	require.Contains(t, buf.String(), "ACCEPT")
}

func TestQemuFirewallRulesCreate_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/firewall/rules", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "create", "100",
		"--type", "in", "--action", "ACCEPT", "--proto", "tcp",
		"--dport", "22", "--source", "172.30.0.0/24", "--comment", "ssh", "--enable", "1"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/rules", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "in", form.Get("type"))
	require.Equal(t, "ACCEPT", form.Get("action"))
	require.Equal(t, "tcp", form.Get("proto"))
	require.Equal(t, "22", form.Get("dport"))
	require.Equal(t, "172.30.0.0/24", form.Get("source"))
	require.Equal(t, "ssh", form.Get("comment"))
	require.Contains(t, buf.String(), "rule added")
}

// TestQemuFirewall_RequiredFlags consolidates shape-1 (flag-required) cases
// across firewall sub-commands. Each case omits a required flag or condition
// and expects the error substring listed; no HTTP handler is registered.
func TestQemuFirewall_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantContain string // matched via require.Contains(err.Error(), ...)
	}{
		{
			name:        "rules create missing type",
			args:        []string{"firewall", "rules", "create", "100", "--action", "ACCEPT"},
			wantContain: "--type is required",
		},
		{
			name:        "rules create missing action",
			args:        []string{"firewall", "rules", "create", "100", "--type", "in"},
			wantContain: "--action is required",
		},
		{
			name:        "rules delete requires confirmation",
			args:        []string{"firewall", "rules", "delete", "100", "0"},
			wantContain: "confirm",
		},
		{
			name:        "ipset remove requires confirmation",
			args:        []string{"firewall", "ipset", "remove", "100", "trusted", "172.30.0.0/24"},
			wantContain: "confirm",
		},
		{
			name:        "alias delete requires confirmation",
			args:        []string{"firewall", "alias", "delete", "100", "gw"},
			wantContain: "confirm",
		},
		{
			name:        "options set requires at least one flag",
			args:        []string{"firewall", "options", "set", "100"},
			wantContain: "no options to set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ac := newFakeClient(t)
			deps := depsFor(t, ac, output.FormatTable, "pve1", false)

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantContain)
		})
	}
}

func TestQemuFirewallRulesUpdate_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/firewall/rules/2", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "update", "100", "2",
		"--action", "DROP", "--moveto", "0", "--delete", "comment"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/rules/2", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "DROP", form.Get("action"))
	require.Equal(t, "0", form.Get("moveto"))
	require.Equal(t, "comment", form.Get("delete"))
	require.Contains(t, buf.String(), "updated")
}

func TestQemuFirewallRulesDelete_Confirm(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100/firewall/rules/0", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "delete", "100", "0", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/rules/0", gotPath)
	require.Contains(t, buf.String(), "deleted")
}

func TestQemuFirewallRulesList_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/rules", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "firewall", "rules", "list", "100")
	require.Error(t, err)
	require.ErrorContains(t, err, "list firewall rules")
}

// --- firewall ipset ---------------------------------------------------------

func TestQemuFirewallIpsetList_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/ipset", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "trusted", "comment": "trusted nets"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "list", "100"))
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "trusted")
}

func TestQemuFirewallIpsetListMembers_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/ipset/trusted", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"cidr": "172.30.0.0/24", "nomatch": false, "comment": "lab"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "list", "100", "trusted"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/ipset/trusted", gotPath)
	out := buf.String()
	require.Contains(t, out, "CIDR")
	require.Contains(t, out, "172.30.0.0/24")
}

func TestQemuFirewallIpsetCreate_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/firewall/ipset", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotQuery = r.Method, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "create", "100", "trusted", "--comment", "lab nets"))
	require.Equal(t, http.MethodPost, gotMethod)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "trusted", form.Get("name"))
	require.Equal(t, "lab nets", form.Get("comment"))
	require.Contains(t, buf.String(), "created")
}

func TestQemuFirewallIpsetAdd_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/firewall/ipset/trusted", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "add", "100", "trusted", "172.30.0.0/24", "--nomatch"))
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/ipset/trusted", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "172.30.0.0/24", form.Get("cidr"))
	require.Equal(t, "1", form.Get("nomatch"))
	require.Contains(t, buf.String(), "added to IP set")
}

func TestQemuFirewallIpsetRemove_Confirm(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100/firewall/ipset/trusted/172.30.0.0/24", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "remove", "100", "trusted", "172.30.0.0/24", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/ipset/trusted/172.30.0.0/24", gotPath)
	require.Contains(t, buf.String(), "removed from IP set")
}

func TestQemuFirewallIpsetDelete_Force(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotQuery, body string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100/firewall/ipset/trusted", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotQuery = r.Method, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "delete", "100", "trusted", "--yes", "--force"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "1", parseForm(t, gotQuery+"&"+body).Get("force"))
	require.Contains(t, buf.String(), "deleted")
}

// --- firewall alias ---------------------------------------------------------

func TestQemuFirewallAliasList_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/aliases", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "gw", "cidr": "172.30.0.1", "comment": "gateway"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "alias", "list", "100"))
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "gw")
	require.Contains(t, out, "172.30.0.1")
}

func TestQemuFirewallAliasCreate_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/firewall/aliases", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotQuery = r.Method, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "alias", "create", "100", "gw", "172.30.0.1", "--comment", "gateway"))
	require.Equal(t, http.MethodPost, gotMethod)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "gw", form.Get("name"))
	require.Equal(t, "172.30.0.1", form.Get("cidr"))
	require.Equal(t, "gateway", form.Get("comment"))
	require.Contains(t, buf.String(), "created")
}

func TestQemuFirewallAliasUpdate_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/firewall/aliases/gw", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "alias", "update", "100", "gw", "172.30.0.254", "--rename", "gw2"))
	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/aliases/gw", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "172.30.0.254", form.Get("cidr"))
	require.Equal(t, "gw2", form.Get("rename"))
	require.Contains(t, buf.String(), "updated")
}

// --- firewall options -------------------------------------------------------

func TestQemuFirewallOptionsGet_Single(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"enable": 1, "policy_in": "DROP", "policy_out": "ACCEPT", "log_level_in": "info",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "get", "100"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/options", gotPath)
	out := buf.String()
	require.Contains(t, out, "policy_in")
	require.Contains(t, out, "DROP")
}

func TestQemuFirewallOptionsSet_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "set", "100",
		"--enable", "--policy-in", "DROP", "--log-level-in", "info"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/firewall/options", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "1", form.Get("enable"))
	require.Equal(t, "DROP", form.Get("policy_in"))
	require.Equal(t, "info", form.Get("log_level_in"))
	require.Contains(t, buf.String(), "updated")
}

// --- command tree + flag-collision regression -------------------------------

func TestQemuFirewallCommandTree(t *testing.T) {
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

// TestQemuFirewall_NoLocalTargetFlag guards against shadowing the root's
// persistent -t/--target selector with a local --target on any firewall command.
func TestQemuFirewall_NoLocalTargetFlag(t *testing.T) {
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
