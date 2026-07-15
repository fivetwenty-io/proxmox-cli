package sdn

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newFakeClient returns a FakePVE and a constructed APIClient pointing at it.
// FakePVE reports its address as Options.Host="host:port" with Port=0; split
// it into the separate Host/Port fields apiclient.NewAPIClient expects so
// requests reach the fake server (mirrors internal/cli/cluster's helper of the
// same name).
func newFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()
	f := testhelper.NewFakePVE(t)

	opts := f.Options
	if host, portStr, err := net.SplitHostPort(opts.Host); err == nil {
		port, perr := strconv.Atoi(portStr)
		require.NoError(t, perr)
		opts.Host = host
		opts.Port = port
	}

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return f, ac
}

// runDeps builds the sdn group command with deps injected directly via
// context (rather than the config-file-driven run() in sdn_test.go, which
// hardcodes table output), so a test can pick its own output.Format.
func runDeps(deps *cli.Deps, buf *bytes.Buffer, args ...string) error {
	cmd := Group(&cli.Deps{})
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// TestVnetFirewallOptionSchemas_GeneratedTable sanity-checks the generated
// vnet firewall option table.
func TestVnetFirewallOptionSchemas_GeneratedTable(t *testing.T) {
	require.NotEmpty(t, vnetFirewallOptionSchemas)
	for _, s := range vnetFirewallOptionSchemas {
		require.NotEqual(t, "vnet", s.Name)
		require.NotEqual(t, "delete", s.Name)
		require.NotEqual(t, "digest", s.Name)
	}

	policyForward := optionschema.Find(vnetFirewallOptionSchemas, "policy_forward")
	require.NotNil(t, policyForward)
	require.Equal(t, "policy-forward", policyForward.Flag)
	require.Equal(t, []string{"ACCEPT", "DROP"}, policyForward.Enum)

	enable := optionschema.Find(vnetFirewallOptionSchemas, "enable")
	require.NotNil(t, enable)
	require.Equal(t, "false", enable.Default)
}

// TestVnetFirewallOptionsSet_HelpEnriched verifies the generated schema detail
// lands in the set flag help.
func TestVnetFirewallOptionsSet_HelpEnriched(t *testing.T) {
	set := newVnetFirewallOptionsSetCmd()

	policyForward := set.Flags().Lookup("policy-forward")
	require.NotNil(t, policyForward)
	require.Contains(t, policyForward.Usage, "values: ACCEPT, DROP")

	enable := set.Flags().Lookup("enable")
	require.NotNil(t, enable)
	require.Contains(t, enable.Usage, "default: false")
}

// TestVnetFirewallOptionsDescribe verifies the offline describe catalog runs
// without a vnet argument and rejects unknown options.
func TestVnetFirewallOptionsDescribe(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/firewall/options", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "options", "describe")
	require.NoError(t, err)
	require.Contains(t, out, "policy-forward")
	require.Contains(t, out, "ACCEPT|DROP")
	require.Empty(t, rec, "describe runs offline: it must not contact the cluster")

	_, err = run(t, f, "", "vnet", "firewall", "options", "describe", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pmx pve sdn vnet firewall options describe")
	require.Empty(t, rec, "describe runs offline even for an unknown option")
}

// TestVnetFirewallOptionsGetPlainUnchanged verifies plain `get` output (no
// --defaults) is unaffected by the schema wiring.
func TestVnetFirewallOptionsGetPlainUnchanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/firewall/options", map[string]any{
		"policy_forward": "DROP",
	}, 200)

	out, err := run(t, f, "", "vnet", "firewall", "options", "get", "pmxcli0")
	require.NoError(t, err)
	require.Contains(t, out, "DROP")
	require.NotContains(t, out, "log_level_forward")
	require.NotContains(t, out, "log-level-forward")
	require.Len(t, rec, 1)
}

// TestVnetFirewallOptionsGetDefaults verifies `get --defaults` merges built-in
// defaults under unset options and leaves server-set options out of the
// defaults map.
func TestVnetFirewallOptionsGetDefaults(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/sdn/vnets/pmxcli0/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"policy_forward": "DROP"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, runDeps(deps, &buf, "vnet", "firewall", "options", "get", "pmxcli0", "--defaults"))

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "DROP", got.Set["policy_forward"])
	require.Equal(t, "false", got.Defaults["enable"])
	require.NotContains(t, got.Defaults, "policy_forward", "set options must not appear in defaults")
}

// TestVnetFirewallOptionSchemas_SetFlagsExist is a drift guard: every
// generated schema entry must correspond to a real `set` flag.
func TestVnetFirewallOptionSchemas_SetFlagsExist(t *testing.T) {
	set := newVnetFirewallOptionsSetCmd()
	for _, s := range vnetFirewallOptionSchemas {
		require.NotNilf(t, set.Flags().Lookup(s.Flag),
			"schema option %q (flag %q) has no matching `set` flag", s.Name, s.Flag)
	}
}
