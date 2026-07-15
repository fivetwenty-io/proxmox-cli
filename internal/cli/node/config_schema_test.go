package node_test

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// nodeConfigSetAllowlist lists schema flags the hand-written `node config
// set` command does not expose. Every generated node config option is wired
// to a set flag today, so this is empty; a future apidoc regeneration that
// adds an unwired option must fail TestNodeConfigSchemas_DriftGuard until the
// option is either wired into the set command or explicitly allowlisted
// here. Indexed options (acmedomain[n]) are looked up by their base flag
// spelling ("acme-domain"), same as any other schema flag.
var nodeConfigSetAllowlist = map[string]bool{}

// findNodeConfigSetCmd walks the real command tree to the `node config set`
// subcommand.
func findNodeConfigSetCmd(t *testing.T, root *cobra.Command) *cobra.Command {
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
	cfg := find(nodeCmd, "config")
	require.NotNil(t, cfg)
	set := find(cfg, "set")
	require.NotNil(t, set)
	return set
}

// describeNodeConfig runs `node config describe` with JSON output and
// decodes the resulting schema table. It never contacts the fake server:
// describe is offline.
func describeNodeConfig(t *testing.T, args ...string) []optionschema.Schema {
	t.Helper()
	f := testhelper.NewFakePVE(t)
	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(append(prefix, "node", "config", "describe"), args...))
	require.NoError(t, root.Execute())
	var schemas []optionschema.Schema
	require.NoError(t, json.Unmarshal(jsonTail(buf.Bytes()), &schemas))
	return schemas
}

// TestNodeConfigSchemas_GeneratedTable sanity-checks the generated node
// config option table via the offline describe catalog.
func TestNodeConfigSchemas_GeneratedTable(t *testing.T) {
	schemas := describeNodeConfig(t)
	require.NotEmpty(t, schemas)
	for _, s := range schemas {
		require.NotEqual(t, "delete", s.Name)
		require.NotEqual(t, "digest", s.Name)
		require.NotEqual(t, "node", s.Name)
	}

	delay := optionschema.Find(schemas, "startall-onboot-delay")
	require.NotNil(t, delay)
	require.Equal(t, "0", delay.Minimum)
	require.Equal(t, "300", delay.Maximum)

	acmeDomain := optionschema.Find(schemas, "acmedomain[n]")
	require.NotNil(t, acmeDomain)
	require.Equal(t, "acme-domain", acmeDomain.Flag)
	require.True(t, acmeDomain.Indexed)
	require.NotEmpty(t, acmeDomain.SubKeys, "acmedomain[n] is dict-encoded")
}

// TestNodeConfigSchemas_DriftGuard fails if a future apidoc regeneration
// introduces a settable node config option the hand-written set command does
// not expose as a flag, unless the option is explicitly allowlisted above.
func TestNodeConfigSchemas_DriftGuard(t *testing.T) {
	schemas := describeNodeConfig(t)

	f := testhelper.NewFakePVE(t)
	root, _, _ := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	set := findNodeConfigSetCmd(t, root)

	for _, s := range schemas {
		if nodeConfigSetAllowlist[s.Flag] {
			continue
		}
		// Indexed schemas (acmedomain[n]) are exposed as a repeatable flag
		// under their base flag spelling; the same Lookup(s.Flag) covers both
		// indexed and scalar schemas.
		require.NotNil(t, set.Flags().Lookup(s.Flag),
			"schema option %q (flag %q) has no matching `config set` flag: wire it or allowlist it", s.Name, s.Flag)
	}
}

// TestNodeConfigOptions_SetHelpEnriched verifies the generated schema detail
// lands in the set flag help, for both a scalar option and the indexed
// acme-domain option.
func TestNodeConfigOptions_SetHelpEnriched(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, _ := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	set := findNodeConfigSetCmd(t, root)

	ballooningTarget := set.Flags().Lookup("ballooning-target")
	require.NotNil(t, ballooningTarget)
	require.Contains(t, ballooningTarget.Usage, "range: 0…100")
	require.Contains(t, ballooningTarget.Usage, "default: 80")

	acmeDomain := set.Flags().Lookup("acme-domain")
	require.NotNil(t, acmeDomain)
	require.Contains(t, acmeDomain.Usage, "keys: alias, domain, plugin")
}

// TestNodeConfigOptions_Describe verifies the offline describe catalog,
// including a sub-key row for a dict-encoded option, and its unknown-option
// error.
func TestNodeConfigOptions_Describe(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, buf, prefix := newNodeRoot(t, f, output.FormatPlain, exec.Fake())
	root.SetArgs(append(prefix, "node", "config", "describe"))
	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "wakeonlan")
	require.Contains(t, out, "range: 0…300", "startall-onboot-delay's numeric range must render in the VALUES column")
	require.Contains(t, out, "wakeonlan.mac", "dict-encoded wakeonlan must list its mac sub-key")

	root2, _, prefix2 := newNodeRoot(t, f, output.FormatPlain, exec.Fake())
	root2.SetArgs(append(prefix2, "node", "config", "describe", "bogus"))
	err := root2.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pmx pve node config describe")
}

// TestNodeConfigOptions_GetDefaults verifies `get --defaults` merges built-in
// defaults under unset options, keeping server-set options out of the
// defaults map.
func TestNodeConfigOptions_GetDefaults(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/config", map[string]any{"wakeonlan": "aa:bb:cc:dd:ee:ff"})

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "config", "get", "--defaults"))
	require.NoError(t, root.Execute())

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(jsonTail(buf.Bytes()), &got))
	require.Equal(t, "aa:bb:cc:dd:ee:ff", got.Set["wakeonlan"])
	require.Equal(t, "80", got.Defaults["ballooning-target"])
	require.NotContains(t, got.Defaults, "wakeonlan", "set options must not appear in defaults")
}
