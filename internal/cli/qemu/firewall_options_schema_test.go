package qemu

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/optionschema"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestFirewallOptionSchemas_GeneratedTable sanity-checks the generated per-VM
// firewall option table.
func TestFirewallOptionSchemas_GeneratedTable(t *testing.T) {
	require.NotEmpty(t, firewallOptionSchemas)
	for _, s := range firewallOptionSchemas {
		require.NotEqual(t, "delete", s.Name)
		require.NotEqual(t, "digest", s.Name)
	}

	policyIn := optionschema.Find(firewallOptionSchemas, "policy_in")
	require.NotNil(t, policyIn)
	require.Equal(t, "policy-in", policyIn.Flag)
	require.Equal(t, []string{"ACCEPT", "REJECT", "DROP"}, policyIn.Enum)

	macfilter := optionschema.Find(firewallOptionSchemas, "macfilter")
	require.NotNil(t, macfilter)
	require.Equal(t, "true", macfilter.Default)

	logLevelIn := optionschema.Find(firewallOptionSchemas, "log_level_in")
	require.NotNil(t, logLevelIn)
	require.Equal(t, "log-level-in", logLevelIn.Flag)
	require.Contains(t, logLevelIn.Enum, "nolog")

	// The per-VM firewall options endpoint has no dict-encoded option (unlike
	// the datacenter firewall's log_ratelimit); every entry is scalar.
	for _, s := range firewallOptionSchemas {
		require.Empty(t, s.SubKeys, "option %q: per-VM firewall options are all scalar", s.Name)
	}
}

// TestQemuFirewallOptions_SetHelpEnriched verifies the generated schema detail
// lands in the set flag help.
func TestQemuFirewallOptions_SetHelpEnriched(t *testing.T) {
	set := newFirewallOptionsSetCmd()

	policyIn := set.Flags().Lookup("policy-in")
	require.NotNil(t, policyIn)
	require.Contains(t, policyIn.Usage, "values: ACCEPT, REJECT, DROP")

	logLevelIn := set.Flags().Lookup("log-level-in")
	require.NotNil(t, logLevelIn)
	require.Contains(t, logLevelIn.Usage, "nolog")

	macfilter := set.Flags().Lookup("macfilter")
	require.NotNil(t, macfilter)
	require.Contains(t, macfilter.Usage, "default: true")
}

// TestQemuFirewallOptions_Describe verifies the offline describe catalog.
func TestQemuFirewallOptions_Describe(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "describe"))
	out := buf.String()
	require.Contains(t, out, "policy-in")
	require.Contains(t, out, "ACCEPT|REJECT|DROP")

	buf.Reset()
	err := run(deps, &buf, "firewall", "options", "describe", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pve qemu firewall options describe")
}

// TestQemuFirewallOptions_DescribeOffline verifies describe never contacts
// the cluster: no VM argument is required and no fake HTTP handler is
// registered, yet the command still succeeds.
func TestQemuFirewallOptions_DescribeOffline(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "describe"))
	require.NotEmpty(t, buf.String())
}

// TestQemuFirewallOptions_GetDefaults verifies `get --defaults` merges
// built-in defaults under unset options.
func TestQemuFirewallOptions_GetDefaults(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"policy_in": "DROP"})
	})
	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "get", "100", "--defaults"))

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "DROP", got.Set["policy_in"])
	require.Equal(t, "true", got.Defaults["macfilter"])
	require.Equal(t, "false", got.Defaults["dhcp"])
	require.NotContains(t, got.Defaults, "policy_in", "set options must not appear in defaults")
}

// TestQemuFirewallOptionsSet_FlagsMatchSchema is a drift guard: every schema
// flag must resolve to a real flag on `firewall options set`. The per-VM
// firewall options set command wires every settable option directly (no
// hand-picked subset), so the allowlist is empty.
func TestQemuFirewallOptionsSet_FlagsMatchSchema(t *testing.T) {
	set := newFirewallOptionsSetCmd()
	for _, s := range firewallOptionSchemas {
		require.NotNil(t, set.Flags().Lookup(s.Flag), "schema flag %q has no matching set flag", s.Flag)
	}
}
