package lxc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestFirewallOptionSchemas_GeneratedTable sanity-checks the generated
// container firewall option table.
func TestFirewallOptionSchemas_GeneratedTable(t *testing.T) {
	require.NotEmpty(t, firewallOptionSchemas)
	for _, s := range firewallOptionSchemas {
		require.NotEqual(t, "delete", s.Name)
		require.NotEqual(t, "digest", s.Name)
	}

	logLevelIn := optionschema.Find(firewallOptionSchemas, "log_level_in")
	require.NotNil(t, logLevelIn)
	require.Equal(t, "log-level-in", logLevelIn.Flag)
	require.Equal(t, []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug", "nolog"},
		logLevelIn.Enum)

	// drift guard: every generated flag must exist on the set command (no new
	// set flags were added to close the gap — the schema flags already match
	// the hand-written ones 1:1).
	set := newFirewallOptionsSetCmd()
	for _, s := range firewallOptionSchemas {
		require.NotNil(t, set.Flags().Lookup(s.Flag), "schema flag %q must have a matching set flag", s.Flag)
	}
}

// TestLxcFirewallOptions_SetHelpEnriched verifies the generated schema detail
// lands in the set flag help.
func TestLxcFirewallOptions_SetHelpEnriched(t *testing.T) {
	set := newFirewallOptionsSetCmd()

	policyIn := set.Flags().Lookup("policy-in")
	require.NotNil(t, policyIn)
	require.Contains(t, policyIn.Usage, "values: ACCEPT, REJECT, DROP")

	macfilter := set.Flags().Lookup("macfilter")
	require.NotNil(t, macfilter)
	require.Contains(t, macfilter.Usage, "default: true")
}

// TestLxcFirewallOptions_Describe verifies the offline describe catalog runs
// without a container argument or any API access.
func TestLxcFirewallOptions_Describe(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "options", "describe")
	require.NoError(t, run())
	out := buf.String()
	require.Contains(t, out, "policy-in")
	require.Contains(t, out, "ACCEPT|REJECT|DROP")

	buf.Reset()
	run = newTestCmd(t, deps, &buf, "firewall", "options", "describe", "bogus")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pmx lxc firewall options describe")
}

// TestLxcFirewallOptions_GetDefaults verifies `get --defaults` merges built-in
// defaults under unset options.
func TestLxcFirewallOptions_GetDefaults(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"policy_in": "DROP"})
	})
	deps := newDeps(t, f, output.FormatJSON, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "options", "get", "100", "--defaults")
	require.NoError(t, run())

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "DROP", got.Set["policy_in"])
	require.Equal(t, "true", got.Defaults["macfilter"])
	require.NotContains(t, got.Defaults, "policy_in", "set options must not appear in defaults")
}
