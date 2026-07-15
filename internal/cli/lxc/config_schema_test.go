package lxc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestConfigSchemas_GeneratedTable sanity-checks the generated container
// config option table.
func TestConfigSchemas_GeneratedTable(t *testing.T) {
	require.NotEmpty(t, configSchemas)
	for _, name := range []string{"delete", "digest", "revert", "node", "vmid"} {
		require.Nil(t, optionschema.Find(configSchemas, name), "meta/path parameter %q must be excluded", name)
	}

	cmode := optionschema.Find(configSchemas, "cmode")
	require.NotNil(t, cmode)
	require.Equal(t, []string{"shell", "console", "tty"}, cmode.Enum)
	require.Equal(t, "tty", cmode.Default)

	memory := optionschema.Find(configSchemas, "memory")
	require.NotNil(t, memory)
	require.Equal(t, "512", memory.Default)
	require.Equal(t, "16", memory.Minimum)

	net := optionschema.Find(configSchemas, "net")
	require.NotNil(t, net)
	require.Equal(t, "net[n]", net.Name)
	require.Equal(t, "net", net.Flag)
	require.True(t, net.Indexed)

	// drift guard: every generated flag must exist on the set command (no new
	// set flags were added to close the gap — the schema flags already match
	// the hand-written ones 1:1; delete/digest/revert are intentionally
	// excluded meta-parameters that keep their own hand-written flags).
	set := newConfigSetCmd()
	for _, s := range configSchemas {
		require.NotNil(t, set.Flags().Lookup(s.Flag), "schema flag %q must have a matching set flag", s.Flag)
	}
}

// TestLxcConfig_SetHelpEnriched verifies the generated schema detail lands in
// the set flag help, including the capped sub-key list on --net.
func TestLxcConfig_SetHelpEnriched(t *testing.T) {
	set := newConfigSetCmd()

	cmode := set.Flags().Lookup("cmode")
	require.NotNil(t, cmode)
	require.Contains(t, cmode.Usage, "values: shell, console, tty")

	net := set.Flags().Lookup("net")
	require.NotNil(t, net)
	require.Contains(t, net.Usage, "keys:")
	require.Contains(t, net.Usage, "…", "net[n] has more than 8 sub-keys and must be capped")
}

// TestLxcConfig_Describe verifies the offline describe catalog suppresses
// sub-key rows while a single-option lookup shows them, and runs without a
// container argument or any API access.
func TestLxcConfig_Describe(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "describe")
	require.NoError(t, run())
	out := buf.String()
	require.Contains(t, out, "net[n]")
	require.NotContains(t, out, "net[n].bridge", "catalog view must suppress sub-key rows for config")

	buf.Reset()
	run = newTestCmd(t, deps, &buf, "config", "describe", "net")
	require.NoError(t, run())
	require.Contains(t, buf.String(), "net[n].bridge", "single-option view must show sub-keys")

	buf.Reset()
	run = newTestCmd(t, deps, &buf, "config", "describe", "bogus")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pmx pve lxc config describe")
}

// TestLxcConfig_GetDefaults verifies `get --defaults` merges built-in
// defaults under unset options and skips unset options with no default.
func TestLxcConfig_GetDefaults(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"hostname": "web", "digest": "abc123"})
	})
	deps := newDeps(t, f, output.FormatJSON, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "get", "100", "--defaults")
	require.NoError(t, run())

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "web", got.Set["hostname"])
	require.Equal(t, "tty", got.Defaults["cmode"])
	require.NotContains(t, got.Defaults, "hostname", "set options must not appear in defaults")
	require.NotContains(t, got.Defaults, "description", "options with no built-in default are skipped with SkipUnset")
	require.NotContains(t, got.Defaults, "net", "indexed slot options have no single default")
}
