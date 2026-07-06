package node_test

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/optionschema"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// nodeFirewallOptionsSetAllowlist lists schema flags the hand-written `node
// firewall options set` command does not expose. Every generated host
// firewall option is wired to a set flag today, so this is empty; a future
// apidoc regeneration that adds an unwired option must fail
// TestNodeFirewallOptionSchemas_DriftGuard until the option is either wired
// into the set command or explicitly allowlisted here.
var nodeFirewallOptionsSetAllowlist = map[string]bool{}

// findNodeFirewallOptionsSetCmd walks the real command tree to the `node
// firewall options set` subcommand.
func findNodeFirewallOptionsSetCmd(t *testing.T, root *cobra.Command) *cobra.Command {
	t.Helper()
	find := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}
	nodeCmd := find(root, "node")
	require.NotNil(t, nodeCmd)
	fw := find(nodeCmd, "firewall")
	require.NotNil(t, fw)
	options := find(fw, "options")
	require.NotNil(t, options)
	set := find(options, "set")
	require.NotNil(t, set)
	return set
}

// jsonTail strips any warning lines the root command's PersistentPreRunE may
// have written to the shared out/err buffer ahead of the JSON payload (e.g.
// the insecure-TLS notice from the fake test config) by returning the buffer
// from its first JSON delimiter onward.
func jsonTail(buf []byte) []byte {
	for i, b := range buf {
		if b == '[' || b == '{' {
			return buf[i:]
		}
	}
	return buf
}

// describeNodeFirewallOptions runs `node firewall options describe` with
// JSON output and decodes the resulting schema table. It never contacts the
// fake server: describe is offline.
func describeNodeFirewallOptions(t *testing.T, args ...string) []optionschema.Schema {
	t.Helper()
	f := testhelper.NewFakePVE(t)
	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(append(prefix, "node", "firewall", "options", "describe"), args...))
	require.NoError(t, root.Execute())
	var schemas []optionschema.Schema
	require.NoError(t, json.Unmarshal(jsonTail(buf.Bytes()), &schemas))
	return schemas
}

// TestNodeFirewallOptionSchemas_GeneratedTable sanity-checks the generated
// host firewall option table via the offline describe catalog.
func TestNodeFirewallOptionSchemas_GeneratedTable(t *testing.T) {
	schemas := describeNodeFirewallOptions(t)
	require.NotEmpty(t, schemas)
	for _, s := range schemas {
		require.NotEqual(t, "delete", s.Name)
		require.NotEqual(t, "digest", s.Name)
		require.NotEqual(t, "node", s.Name)
	}

	logLevelIn := optionschema.Find(schemas, "log_level_in")
	require.NotNil(t, logLevelIn)
	require.Equal(t, "log-level-in", logLevelIn.Flag)
	require.Equal(t,
		[]string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug", "nolog"},
		logLevelIn.Enum)
}

// TestNodeFirewallOptionSchemas_DriftGuard fails if a future apidoc
// regeneration introduces a settable host firewall option the hand-written
// set command does not expose as a flag, unless the option is explicitly
// allowlisted above.
func TestNodeFirewallOptionSchemas_DriftGuard(t *testing.T) {
	schemas := describeNodeFirewallOptions(t)

	f := testhelper.NewFakePVE(t)
	root, _, _ := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	set := findNodeFirewallOptionsSetCmd(t, root)

	for _, s := range schemas {
		if nodeFirewallOptionsSetAllowlist[s.Flag] {
			continue
		}
		require.NotNil(t, set.Flags().Lookup(s.Flag),
			"schema option %q (flag %q) has no matching `options set` flag: wire it or allowlist it", s.Name, s.Flag)
	}
}

// TestNodeFirewallOptions_SetHelpEnriched verifies the generated schema
// detail lands in the set flag help.
func TestNodeFirewallOptions_SetHelpEnriched(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, _ := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	set := findNodeFirewallOptionsSetCmd(t, root)

	logLevelIn := set.Flags().Lookup("log-level-in")
	require.NotNil(t, logLevelIn)
	require.Contains(t, logLevelIn.Usage, "values: emerg, alert, crit, err, warning, notice, info, debug, nolog")

	nfConntrackMax := set.Flags().Lookup("nf-conntrack-max")
	require.NotNil(t, nfConntrackMax)
	require.Contains(t, nfConntrackMax.Usage, "min: 32768")
	require.Contains(t, nfConntrackMax.Usage, "default: 262144")
}

// TestNodeFirewallOptions_Describe verifies the offline describe catalog and
// its unknown-option error.
func TestNodeFirewallOptions_Describe(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, buf, prefix := newNodeRoot(t, f, output.FormatPlain, exec.Fake())
	root.SetArgs(append(prefix, "node", "firewall", "options", "describe"))
	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "log-level-in")
	require.Contains(t, out, "emerg|alert|crit|err|warning|notice|info|debug|nolog")

	root2, _, prefix2 := newNodeRoot(t, f, output.FormatPlain, exec.Fake())
	root2.SetArgs(append(prefix2, "node", "firewall", "options", "describe", "bogus"))
	err := root2.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pve node firewall options describe")
}

// TestNodeFirewallOptions_GetDefaults verifies `get --defaults` merges
// built-in defaults under unset options.
func TestNodeFirewallOptions_GetDefaults(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/firewall/options", map[string]any{"log_level_in": "info"})

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "options", "get", "--defaults"))
	require.NoError(t, root.Execute())

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(jsonTail(buf.Bytes()), &got))
	require.Equal(t, "info", got.Set["log_level_in"])
	require.Equal(t, "true", got.Defaults["enable"])
	require.NotContains(t, got.Defaults, "log_level_in", "set options must not appear in defaults")
}
