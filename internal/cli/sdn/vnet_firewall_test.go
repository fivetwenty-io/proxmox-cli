package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- vnet firewall rules ---

func TestVnetFirewallRulesList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules", []any{
		map[string]any{"pos": 0, "type": "forward", "action": "ACCEPT", "proto": "tcp",
			"dport": "22", "enable": 1, "comment": "ssh"},
		map[string]any{"pos": 1, "type": "forward", "action": "DROP", "enable": 0},
	}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "rules", "list", "pvecli0")
	require.NoError(t, err)
	require.Contains(t, out, "ACCEPT")
	require.Contains(t, out, "ssh")
	require.Contains(t, out, "DROP")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/firewall/rules", rec[0].path)
}

func TestVnetFirewallRulesListError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules", nil, 500)

	_, err := run(t, f, "", "vnet", "firewall", "rules", "list", "pvecli0")
	require.Error(t, err)
	require.ErrorContains(t, err, "list firewall rules for vnet")
}

func TestVnetFirewallRulesCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "rules", "create", "pvecli0",
		"--type", "forward", "--action", "DROP", "--enable", "0", "--comment", "pve-cli-e2e")
	require.NoError(t, err)
	require.Contains(t, out, "Firewall rule added")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/firewall/rules", rec[0].path)
	require.Equal(t, "forward", rec[0].body["type"])
	require.Equal(t, "DROP", rec[0].body["action"])
	require.Equal(t, "0", rec[0].body["enable"])
	require.Equal(t, "pve-cli-e2e", rec[0].body["comment"])
	// Unset optional fields must not be forwarded.
	require.NotContains(t, rec[0].body, "source")
	require.NotContains(t, rec[0].body, "dport")
	require.NotContains(t, rec[0].body, "pos")
}

// TestVnetFirewallRulesCreateRequiresTypeAction verifies --type and --action are
// mandatory and no request is issued when they are missing.
func TestVnetFirewallRulesCreateRequiresTypeAction(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "firewall", "rules", "create", "pvecli0", "--type", "forward")
	require.Error(t, err)
	require.ErrorContains(t, err, "action")
	require.Empty(t, rec, "no request must be issued when a required flag is missing")
}

func TestVnetFirewallRulesGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	// PVE returns pos as a STRING on the single-rule GET; the command must use
	// the raw fetch path and render it without a decode error.
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules/0", map[string]any{
		"pos": "0", "type": "forward", "action": "ACCEPT", "proto": "tcp", "dport": "22",
	}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "rules", "get", "pvecli0", "0")
	require.NoError(t, err)
	require.Contains(t, out, "ACCEPT")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/firewall/rules/0", rec[0].path)
}

// TestVnetFirewallRulesSetRequiresChange verifies a set with no field flags is
// rejected before any request is issued.
func TestVnetFirewallRulesSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules/0", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "firewall", "rules", "set", "pvecli0", "0")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes to set")
	require.Empty(t, rec)
}

func TestVnetFirewallRulesSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules/0", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "rules", "set", "pvecli0", "0",
		"--action", "REJECT", "--comment", "updated")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "REJECT", rec[0].body["action"])
	require.Equal(t, "updated", rec[0].body["comment"])
	// Unchanged fields must not be sent.
	require.NotContains(t, rec[0].body, "type")
	require.NotContains(t, rec[0].body, "moveto")
	require.NotContains(t, rec[0].body, "delete")
}

func TestVnetFirewallRulesDeleteWithoutConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules/0", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "firewall", "rules", "delete", "pvecli0", "0")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec, "no request must be issued without --yes")
}

func TestVnetFirewallRulesDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pvecli0/firewall/rules/0", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "rules", "delete", "pvecli0", "0", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/firewall/rules/0", rec[0].path)
}

// --- vnet firewall options ---

func TestVnetFirewallOptionsGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pvecli0/firewall/options", map[string]any{
		"enable": 1, "policy_forward": "ACCEPT", "log_level_forward": "info",
	}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "options", "get", "pvecli0")
	require.NoError(t, err)
	require.Contains(t, out, "ACCEPT")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/firewall/options", rec[0].path)
}

// TestVnetFirewallOptionsSetRequiresChange verifies a no-op set is rejected.
func TestVnetFirewallOptionsSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/firewall/options", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "firewall", "options", "set", "pvecli0")
	require.Error(t, err)
	require.ErrorContains(t, err, "no options to set")
	require.Empty(t, rec)
}

func TestVnetFirewallOptionsSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/firewall/options", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "options", "set", "pvecli0",
		"--enable", "--policy-forward", "DROP")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "1", rec[0].body["enable"], "bool true encodes as 1")
	require.Equal(t, "DROP", rec[0].body["policy_forward"])
	// Unchanged fields must not be sent.
	require.NotContains(t, rec[0].body, "log_level_forward")
	require.NotContains(t, rec[0].body, "delete")
}

// TestVnetFirewallCommandTree asserts the firewall sub-tree wires the expected
// verbs under rules and options.
func TestVnetFirewallCommandTree(t *testing.T) {
	fw := newVnetFirewallCmd()
	sub := map[string]*[]string{}
	for _, c := range fw.Commands() {
		names := []string{}
		for _, gc := range c.Commands() {
			names = append(names, gc.Name())
		}
		cp := names
		sub[c.Name()] = &cp
	}
	require.Contains(t, sub, "rules")
	require.Contains(t, sub, "options")
	require.ElementsMatch(t, []string{"list", "get", "create", "set", "delete"}, *sub["rules"])
	require.ElementsMatch(t, []string{"get", "set", "describe"}, *sub["options"])
}
