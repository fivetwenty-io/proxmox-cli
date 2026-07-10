package cluster

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
// datacenter firewall option table.
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

	ratelimit := optionschema.Find(firewallOptionSchemas, "log-ratelimit")
	require.NotNil(t, ratelimit)
	require.NotEmpty(t, ratelimit.SubKeys, "log_ratelimit is dict-encoded")
}

// TestClusterFirewallOptions_SetHelpEnriched verifies the generated schema
// detail lands in the set flag help.
func TestClusterFirewallOptions_SetHelpEnriched(t *testing.T) {
	set := newClusterFirewallOptionsSetCmd()

	policyIn := set.Flags().Lookup("policy-in")
	require.NotNil(t, policyIn)
	require.Contains(t, policyIn.Usage, "values: ACCEPT, REJECT, DROP")

	ratelimit := set.Flags().Lookup("log-ratelimit")
	require.NotNil(t, ratelimit)
	require.Contains(t, ratelimit.Usage, "keys: burst, enable, rate")
}

// TestClusterFirewallOptions_Describe verifies the offline describe catalog.
func TestClusterFirewallOptions_Describe(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "describe"))
	out := buf.String()
	require.Contains(t, out, "policy-in")
	require.Contains(t, out, "ACCEPT|REJECT|DROP")
	require.Contains(t, out, "log-ratelimit.rate")

	buf.Reset()
	err := run(deps, &buf, "firewall", "options", "describe", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pmx pve cluster firewall options describe")
}

// TestClusterFirewallOptions_GetDefaults verifies `get --defaults` merges
// built-in defaults under unset options.
func TestClusterFirewallOptions_GetDefaults(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"policy_in": "DROP"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "get", "--defaults"))

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "DROP", got.Set["policy_in"])
	require.Equal(t, "true", got.Defaults["ebtables"])
	require.Contains(t, got.Defaults["log_ratelimit"], "rate=1/second")
	require.NotContains(t, got.Defaults, "policy_in", "set options must not appear in defaults")
}
