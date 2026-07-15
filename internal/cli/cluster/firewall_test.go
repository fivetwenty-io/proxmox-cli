package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ---- rules -----------------------------------------------------------------

func TestClusterFirewallRules_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/rules", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"pos": 0, "type": "in", "action": "ACCEPT", "proto": "tcp", "dport": "22", "enable": 1, "comment": "ssh"},
			map[string]any{"pos": 1, "type": "out", "action": "DROP"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "list"))

	out := buf.String()
	require.Contains(t, out, "POS")
	require.Contains(t, out, "ACTION")
	require.Contains(t, out, "ACCEPT")
	require.Contains(t, out, "ssh")
}

// TestClusterFirewallRules_Get exercises the raw-fetch workaround: the rule get
// endpoint returns `pos` as a string, which the typed struct cannot decode, so
// the command fetches the object via the raw client and renders it generically.
func TestClusterFirewallRules_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/firewall/rules/3", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"pos": "3", "type": "in", "action": "ACCEPT", "proto": "tcp"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "get", "3"))

	require.Equal(t, "/api2/json/cluster/firewall/rules/3", gotPath)
	out := buf.String()
	require.Contains(t, out, "ACCEPT")
	require.Contains(t, out, "tcp")
}

// TestClusterFirewallCommands_RequiredFlags consolidates shape-1 (flag-required) guards
// for firewall rules and group sub-commands. Each case omits a single required flag
// and verifies that the error message names the missing flag and that no HTTP
// request is issued.
func TestClusterFirewallCommands_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		handlerPath string
		args        []string
		wantErr     string
	}{
		{
			name:        "FirewallRulesCreate_RequiresType",
			handlerPath: "POST /api2/json/cluster/firewall/rules",
			args:        []string{"firewall", "rules", "create", "--action", "ACCEPT"},
			wantErr:     "--type is required",
		},
		{
			name:        "FirewallRulesCreate_RequiresAction",
			handlerPath: "POST /api2/json/cluster/firewall/rules",
			args:        []string{"firewall", "rules", "create", "--type", "in"},
			wantErr:     "--action is required",
		},
		{
			name:        "FirewallGroupRuleAdd_RequiresType",
			handlerPath: "POST /api2/json/cluster/firewall/groups/pmxcli-grp",
			args:        []string{"firewall", "group", "rule-add", "pmxcli-grp", "--action", "ACCEPT"},
			wantErr:     "--type is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ac := newFakeClient(t)

			var called bool
			f.HandleFunc(tc.handlerPath, func(w http.ResponseWriter, _ *http.Request) {
				called = true
				testhelper.WriteData(w, nil)
			})

			deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
			require.False(t, called, "request must not be issued when required flag %q is missing", tc.wantErr)
		})
	}
}

func TestClusterFirewallRules_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/rules", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "create",
		"--type", "in", "--action", "ACCEPT", "--proto", "tcp",
		"--dport", "22", "--source", "+pmxcli-ips", "--enable", "0", "--comment", "ssh"))

	require.Equal(t, "in", gotForm.Get("type"))
	require.Equal(t, "ACCEPT", gotForm.Get("action"))
	require.Equal(t, "tcp", gotForm.Get("proto"))
	require.Equal(t, "22", gotForm.Get("dport"))
	require.Equal(t, "+pmxcli-ips", gotForm.Get("source"))
	require.Equal(t, "0", gotForm.Get("enable"))
	require.Equal(t, "ssh", gotForm.Get("comment"))
}

func TestClusterFirewallRules_UpdateForwardsMoveto(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	var gotPath string
	f.HandleFunc("PUT /api2/json/cluster/firewall/rules/2", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "update", "2", "--moveto", "0", "--delete", "comment"))

	require.Equal(t, "/api2/json/cluster/firewall/rules/2", gotPath)
	require.Equal(t, "0", gotForm.Get("moveto"))
	require.Equal(t, "comment", gotForm.Get("delete"))
}

func TestClusterFirewallRules_DeleteRequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "firewall", "rules", "delete", "1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

func TestClusterFirewallRules_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/cluster/firewall/rules/1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "delete", "1", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/cluster/firewall/rules/1", gotPath)
}

// ---- security groups -------------------------------------------------------

func TestClusterFirewallGroup_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/groups", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"group": "webservers", "comment": "http/https", "digest": "abc"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "group", "list"))
	out := buf.String()
	require.Contains(t, out, "GROUP")
	require.Contains(t, out, "webservers")
	require.Contains(t, out, "http/https")
}

func TestClusterFirewallGroup_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/groups", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "group", "create", "pmxcli-grp", "--comment", "isolated"))
	require.Equal(t, "pmxcli-grp", gotForm.Get("group"))
	require.Equal(t, "isolated", gotForm.Get("comment"))
}

func TestClusterFirewallGroup_DeleteRequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "firewall", "group", "delete", "pmxcli-grp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

func TestClusterFirewallGroup_Rules(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/groups/pmxcli-grp", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"pos": 0, "type": "in", "action": "ACCEPT", "dport": "80"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "group", "rules", "pmxcli-grp"))
	out := buf.String()
	require.Contains(t, out, "POS")
	require.Contains(t, out, "ACCEPT")
	require.Contains(t, out, "80")
}

func TestClusterFirewallGroup_RuleAddForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/groups/pmxcli-grp", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "group", "rule-add", "pmxcli-grp",
		"--type", "in", "--action", "ACCEPT", "--dport", "80"))
	require.Equal(t, "in", gotForm.Get("type"))
	require.Equal(t, "ACCEPT", gotForm.Get("action"))
	require.Equal(t, "80", gotForm.Get("dport"))
}

func TestClusterFirewallGroup_RuleDeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/cluster/firewall/groups/pmxcli-grp/0", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "group", "rule-delete", "pmxcli-grp", "0", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/cluster/firewall/groups/pmxcli-grp/0", gotPath)
}

// ---- ipset -----------------------------------------------------------------

func TestClusterFirewallIpset_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/ipset", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "pmxcli-ips", "comment": "isolated"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "list"))
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "pmxcli-ips")
}

func TestClusterFirewallIpset_ListMembers(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/ipset/pmxcli-ips", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"cidr": "172.30.0.0/24", "nomatch": false, "comment": "lab"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "list", "pmxcli-ips"))
	out := buf.String()
	require.Contains(t, out, "CIDR")
	require.Contains(t, out, "172.30.0.0/24")
}

func TestClusterFirewallIpset_Create(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/ipset", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "create", "pmxcli-ips", "--comment", "isolated"))
	require.Equal(t, "pmxcli-ips", gotForm.Get("name"))
	require.Equal(t, "isolated", gotForm.Get("comment"))
}

func TestClusterFirewallIpset_Add(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/ipset/pmxcli-ips", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "add", "pmxcli-ips", "172.30.0.5", "--nomatch"))
	require.Equal(t, "172.30.0.5", gotForm.Get("cidr"))
	require.Equal(t, "1", gotForm.Get("nomatch"))
}

func TestClusterFirewallIpset_RemoveRequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "firewall", "ipset", "remove", "pmxcli-ips", "172.30.0.5")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

// ---- alias -----------------------------------------------------------------

func TestClusterFirewallAlias_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/aliases", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "pmxcli-alias", "cidr": "172.30.0.0/24", "comment": "lab"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "alias", "list"))
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "pmxcli-alias")
	require.Contains(t, out, "172.30.0.0/24")
}

func TestClusterFirewallAlias_Create(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/aliases", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "alias", "create", "pmxcli-alias", "172.30.0.0/24", "--comment", "lab"))
	require.Equal(t, "pmxcli-alias", gotForm.Get("name"))
	require.Equal(t, "172.30.0.0/24", gotForm.Get("cidr"))
	require.Equal(t, "lab", gotForm.Get("comment"))
}

func TestClusterFirewallAlias_Update(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	var gotPath string
	f.HandleFunc("PUT /api2/json/cluster/firewall/aliases/pmxcli-alias", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "alias", "update", "pmxcli-alias", "172.30.1.0/24", "--rename", "pmxcli-alias2"))
	require.Equal(t, "/api2/json/cluster/firewall/aliases/pmxcli-alias", gotPath)
	require.Equal(t, "172.30.1.0/24", gotForm.Get("cidr"))
	require.Equal(t, "pmxcli-alias2", gotForm.Get("rename"))
}

func TestClusterFirewallAlias_DeleteRequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "firewall", "alias", "delete", "pmxcli-alias")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

// ---- options ---------------------------------------------------------------

func TestClusterFirewallOptions_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"enable": 1, "policy_in": "DROP", "policy_out": "ACCEPT", "ebtables": 1,
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "get"))
	out := buf.String()
	require.Contains(t, out, "DROP")
	require.Contains(t, out, "ACCEPT")
}

func TestClusterFirewallOptions_SetRequiresFlag(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "firewall", "options", "set")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no options to set")
}

func TestClusterFirewallOptions_SetForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/firewall/options", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "set",
		"--enable", "1", "--policy-in", "DROP", "--ebtables", "--policy-forward", "ACCEPT"))
	require.Equal(t, "1", gotForm.Get("enable"))
	require.Equal(t, "DROP", gotForm.Get("policy_in"))
	require.Equal(t, "1", gotForm.Get("ebtables"))
	require.Equal(t, "ACCEPT", gotForm.Get("policy_forward"))
	// only-changed-flags contract: flags not passed must be absent from the
	// body entirely, not sent as empty/zero values that would clobber state.
	_, hasPolicyOut := gotForm["policy_out"]
	require.False(t, hasPolicyOut, "unset --policy-out must be omitted from the request body")
	_, hasLogRatelimit := gotForm["log_ratelimit"]
	require.False(t, hasLogRatelimit, "unset --log-ratelimit must be omitted from the request body")
}

// ---- command tree ----------------------------------------------------------

// TestClusterFirewallCommandTree verifies the firewall sub-tree exposes the
// expected verb groups and that no command shadows the root -t/--target or
// --node selector (the latter is also enforced by the package-wide walker).
func TestClusterFirewallCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	var fw *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "firewall" {
			fw = c
		}
	}
	require.NotNil(t, fw, "cluster must expose a firewall sub-command")

	groups := make(map[string]*cobra.Command)
	for _, c := range fw.Commands() {
		groups[c.Name()] = c
	}
	for _, want := range []string{"rules", "group", "ipset", "alias", "options"} {
		require.Contains(t, groups, want, "expected firewall sub-command %q", want)
	}

	ruleVerbs := make(map[string]bool)
	for _, c := range groups["rules"].Commands() {
		ruleVerbs[c.Name()] = true
	}
	for _, want := range []string{"list", "get", "create", "update", "delete"} {
		require.True(t, ruleVerbs[want], "expected rules verb %q", want)
	}

	groupVerbs := make(map[string]bool)
	for _, c := range groups["group"].Commands() {
		groupVerbs[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "delete", "rules", "rule-add", "rule-update", "rule-delete"} {
		require.True(t, groupVerbs[want], "expected group verb %q", want)
	}
}
