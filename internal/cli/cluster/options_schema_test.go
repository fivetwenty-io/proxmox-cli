package cluster

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestOptionSchemas_GeneratedTable sanity-checks the generated schema table:
// it exists, excludes the delete meta-parameter, maps underscore API names to
// hyphenated flags, and carries known defaults, enums, and sub-keys.
func TestOptionSchemas_GeneratedTable(t *testing.T) {
	require.NotEmpty(t, optionSchemas)

	for _, s := range optionSchemas {
		require.NotEqual(t, "delete", s.Name, "delete meta-parameter must be excluded")
	}

	fencing := optionschema.Find(optionSchemas, "fencing")
	require.NotNil(t, fencing)
	require.Equal(t, "watchdog", fencing.Default)
	require.Equal(t, []string{"watchdog", "hardware", "both"}, fencing.Enum)

	macPrefix := optionschema.Find(optionSchemas, "mac_prefix")
	require.NotNil(t, macPrefix)
	require.Equal(t, "mac-prefix", macPrefix.Flag)
	require.Equal(t, "BC:24:11", macPrefix.Default)
	require.Same(t, macPrefix, optionschema.Find(optionSchemas, "mac-prefix"), "lookup must accept flag spelling too")

	migration := optionschema.Find(optionSchemas, "migration")
	require.NotNil(t, migration)
	var typ *optionschema.SubKey
	for i := range migration.SubKeys {
		if migration.SubKeys[i].Name == "type" {
			typ = &migration.SubKeys[i]
		}
	}
	require.NotNil(t, typ, "migration schema must describe the type sub-key")
	require.Equal(t, "secure", typ.Default)
	require.Equal(t, []string{"secure", "insecure"}, typ.Enum)
	require.Equal(t, "type=secure", migration.DefaultValue(), "dict default composes from sub-key defaults")

	require.Nil(t, optionschema.Find(optionSchemas, "no-such-option"))
}

// TestClusterOptions_SetHelpEnriched verifies the generated schema detail is
// appended to the hand-written set flag help.
func TestClusterOptions_SetHelpEnriched(t *testing.T) {
	set := newOptionsSetCmd()

	fencing := set.Flags().Lookup("fencing")
	require.NotNil(t, fencing)
	require.Contains(t, fencing.Usage, "values: watchdog, hardware, both")
	require.Contains(t, fencing.Usage, "default: watchdog")

	migration := set.Flags().Lookup("migration")
	require.NotNil(t, migration)
	require.Contains(t, migration.Usage, "keys: network, type")
}

// TestClusterOptions_Describe verifies `pmx pve cluster options describe` renders
// the full offline catalog, including dict sub-key rows, with no API call.
func TestClusterOptions_Describe(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "describe"))
	out := buf.String()
	require.Contains(t, out, "fencing")
	require.Contains(t, out, "watchdog|hardware|both")
	require.Contains(t, out, "migration.type")
	require.Contains(t, out, "tag-style.shape")
}

// TestClusterOptions_DescribeSingle verifies the single-option view accepts
// either name spelling and rejects unknown options.
func TestClusterOptions_DescribeSingle(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "describe", "mac_prefix"))
	require.Contains(t, buf.String(), "BC:24:11")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "options", "describe", "migration"))
	out := buf.String()
	require.Contains(t, out, "migration.type")
	require.NotContains(t, out, "fencing")

	buf.Reset()
	err := run(deps, &buf, "options", "describe", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown option "bogus"`)
}

// TestClusterOptions_GetDefaults verifies `get --defaults` adds unset options
// with their built-in (or composed dict) defaults while leaving server-set
// values untouched.
func TestClusterOptions_GetDefaults(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"console": "html5", "migration": "type=insecure"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "get", "--defaults"))
	out := buf.String()
	require.Contains(t, out, "html5")
	require.Contains(t, out, "watchdog (default)")
	require.Contains(t, out, "BC:24:11 (default)")
	require.Contains(t, out, "(unset)")

	// Server-set options keep their value; unset dict options compose theirs.
	rows := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 {
			rows[fields[0]] = strings.Join(fields[1:], " ")
		}
	}
	require.Equal(t, "type=insecure", rows["migration"], "server-set migration must not be overridden by its default")
	require.Equal(t, "type=secure (default)", rows["replication"])
}

// TestClusterOptions_GetDefaultsJSON verifies the JSON shape with --defaults:
// server-set options under "set", derived defaults under "defaults".
func TestClusterOptions_GetDefaultsJSON(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"console": "html5"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "get", "--defaults"))

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "html5", got.Set["console"])
	require.Equal(t, "watchdog", got.Defaults["fencing"])
	require.Equal(t, "type=secure", got.Defaults["migration"])
	require.NotContains(t, got.Defaults, "console", "set options must not appear in defaults")
}

// TestClusterOptions_GetWithoutDefaultsUnchanged verifies the plain get output
// shape is unchanged when --defaults is not passed.
func TestClusterOptions_GetWithoutDefaultsUnchanged(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"console": "html5"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "get"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "html5", got["console"])
	require.NotContains(t, got, "set")
	require.NotContains(t, got, "defaults")
}
