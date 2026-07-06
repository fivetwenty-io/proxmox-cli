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

// TestConfigSchemas_GeneratedTable sanity-checks the generated VM
// configuration option table.
func TestConfigSchemas_GeneratedTable(t *testing.T) {
	require.NotEmpty(t, configSchemas)

	// Meta-parameters excluded at generation time must not appear.
	for _, name := range []string{"delete", "digest", "revert", "force", "skiplock", "background_delay"} {
		require.Nil(t, optionschema.Find(configSchemas, name), "excluded parameter %q must be absent", name)
	}

	netN := optionschema.Find(configSchemas, "net[n]")
	require.NotNil(t, netN)
	require.Equal(t, "net", netN.Flag)
	require.True(t, netN.Indexed)

	numaN := optionschema.Find(configSchemas, "numa[n]")
	require.NotNil(t, numaN)
	require.Equal(t, "numa-node", numaN.Flag, "numa[n] must use the overridden flag spelling; scalar numa already claims --numa")
	require.True(t, numaN.Indexed)

	cores := optionschema.Find(configSchemas, "cores")
	require.NotNil(t, cores)
	require.Equal(t, "1", cores.Default)
	require.Equal(t, "1", cores.Minimum)

	// An option the API leaves without a built-in default (used below to
	// verify --defaults omits it).
	affinity := optionschema.Find(configSchemas, "affinity")
	require.NotNil(t, affinity)
	require.Empty(t, affinity.Default)
}

// TestQemuConfigSet_HelpEnriched verifies the generated schema detail lands
// in the set flag help.
func TestQemuConfigSet_HelpEnriched(t *testing.T) {
	set := newConfigSetCmd()

	ostype := set.Flags().Lookup("ostype")
	require.NotNil(t, ostype)
	require.Contains(t, ostype.Usage, "values:")
	require.Contains(t, ostype.Usage, "win11")

	net := set.Flags().Lookup("net")
	require.NotNil(t, net)
	require.Contains(t, net.Usage, "keys:")
	require.Contains(t, net.Usage, "…", "net[n] has more sub-keys than the suffix cap; the list must be truncated")
}

// TestQemuConfig_Describe verifies the offline describe catalog: the catalog
// view suppresses dict sub-key rows (SubKeyRowsInCatalog: false) but the
// single-option view still shows them.
func TestQemuConfig_Describe(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "describe"))
	out := buf.String()
	require.Contains(t, out, "net[n]")
	require.NotContains(t, out, "net[n].model", "catalog view must not expand sub-keys for the large config table")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "config", "describe", "net"))
	require.Contains(t, buf.String(), "net[n].model")

	buf.Reset()
	err := run(deps, &buf, "config", "describe", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pve qemu config describe")
}

// TestQemuConfig_DescribeOffline verifies describe never contacts the
// cluster: no VM argument is required and no fake HTTP handler is
// registered, yet the command still succeeds.
func TestQemuConfig_DescribeOffline(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "describe"))
	require.NotEmpty(t, buf.String())
}

// TestQemuConfigGet_Defaults verifies `get --defaults` merges built-in
// defaults under unset options, using MergeOpts{SkipUnset: true} so options
// without a schema default are omitted rather than listed as "(unset)".
func TestQemuConfigGet_Defaults(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"name": "web01", "memory": "2048"})
	})
	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "get", "100", "--defaults"))

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "web01", got.Set["name"])

	require.Equal(t, "1", got.Defaults["cores"], "cores has a real built-in default")
	require.NotContains(t, got.Defaults, "affinity", "affinity has no built-in default and SkipUnset must drop it")
	require.NotContains(t, got.Defaults, "name", "server-set keys must not appear in defaults")
}

// TestQemuConfigSet_FlagsMatchSchema is a drift guard: every schema flag
// must resolve to a real flag on `config set`, except entries in the
// allowlist below — options the PVE schema exposes but this CLI does not
// (yet) surface as a dedicated set flag: `smp` (superseded by --sockets and
// --cores per its own schema description), `bootdisk` (deprecated by the
// schema in favor of --boot order=...), and `unused[n]` (orphaned disk
// references removed via `qemu disk`, not `config set`).
func TestQemuConfigSet_FlagsMatchSchema(t *testing.T) {
	allowlist := map[string]bool{
		"smp":      true,
		"bootdisk": true,
		"unused":   true,
	}
	set := newConfigSetCmd()
	for _, s := range configSchemas {
		if allowlist[s.Flag] {
			continue
		}
		require.NotNil(t, set.Flags().Lookup(s.Flag), "schema flag %q has no matching set flag and is not allowlisted", s.Flag)
	}
}
