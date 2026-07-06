package optionschema

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// describeSchemas is a small table exercising enums, bounds, sub-keys, and
// indexed options.
var describeSchemas = []Schema{
	{Name: "fencing", Flag: "fencing", Type: "string", Default: "watchdog",
		Enum: []string{"watchdog", "hardware"}, Description: "Fencing mode."},
	{Name: "max_workers", Flag: "max-workers", Type: "integer", Minimum: "1", Maximum: "64",
		Description: "Worker cap."},
	{Name: "net[n]", Flag: "net", Type: "string", Indexed: true, Description: "Network device.",
		SubKeys: []SubKey{
			{Name: "bridge", Type: "string", Description: "Bridge name."},
			{Name: "model", Type: "string", Required: true, Description: "NIC model."},
		}},
}

// runDescribe executes a describe command built from cfg with plain output.
func runDescribe(t *testing.T, cfg DescribeConfig, args ...string) (string, error) {
	t.Helper()
	cmd := NewDescribeCmd(cfg)
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestDescribe_Catalog(t *testing.T) {
	out, err := runDescribe(t, DescribeConfig{
		Schemas:             describeSchemas,
		CommandHint:         "pve x describe",
		SubKeyRowsInCatalog: true,
	})
	require.NoError(t, err)
	require.Contains(t, out, "fencing")
	require.Contains(t, out, "watchdog|hardware")
	require.Contains(t, out, "range: 1…64", "bounded option renders its range in VALUES")
	require.Contains(t, out, "net[n]", "indexed option shows its bracket name")
	require.Contains(t, out, "net[n].bridge")
	require.Contains(t, out, "required. NIC model.")
}

func TestDescribe_SubKeySuppression(t *testing.T) {
	out, err := runDescribe(t, DescribeConfig{
		Schemas:     describeSchemas,
		CommandHint: "pve x describe",
	})
	require.NoError(t, err)
	require.Contains(t, out, "net[n]")
	require.NotContains(t, out, "net[n].bridge", "catalog must hide sub-key rows when suppressed")

	out, err = runDescribe(t, DescribeConfig{
		Schemas:     describeSchemas,
		CommandHint: "pve x describe",
	}, "net")
	require.NoError(t, err)
	require.Contains(t, out, "net[n].bridge", "single view always shows sub-keys")
	require.NotContains(t, out, "fencing")
}

func TestDescribe_UnknownOption(t *testing.T) {
	_, err := runDescribe(t, DescribeConfig{
		Schemas:     describeSchemas,
		CommandHint: "pve x describe",
	}, "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, `unknown option "bogus"`)
	require.ErrorContains(t, err, "pve x describe")
}

// describeTypeSets is a two-type discriminator mapping over describeSchemas.
var describeTypeSets = map[string]map[string]TypeUse{
	"alpha": {
		"fencing":     {Fixed: true, Required: true},
		"max_workers": {},
	},
	"beta": {
		"max_workers": {},
	},
}

// TestDescribe_TypeSetsCatalog verifies the TYPES column in the unfiltered
// catalog: per-option accepting types, collapsed to "all" when universal.
func TestDescribe_TypeSetsCatalog(t *testing.T) {
	out, err := runDescribe(t, DescribeConfig{
		Schemas:     describeSchemas,
		CommandHint: "pve x describe",
		TypeSets:    describeTypeSets,
	})
	require.NoError(t, err)
	require.Contains(t, out, "TYPES")
	require.Contains(t, out, "alpha", "single-type option lists its type")
	require.Contains(t, out, "all", "universally accepted option collapses to all")
}

// TestDescribe_TypeSetsFilter verifies --type filtering and usage markers.
func TestDescribe_TypeSetsFilter(t *testing.T) {
	cfg := DescribeConfig{
		Schemas:     describeSchemas,
		CommandHint: "pve x describe",
		TypeSets:    describeTypeSets,
	}

	out, err := runDescribe(t, cfg, "--type", "alpha")
	require.NoError(t, err)
	require.Contains(t, out, "USE")
	require.Contains(t, out, "required, create-only")
	require.NotContains(t, out, "net[n]", "options outside the type set are filtered out")

	out, err = runDescribe(t, cfg, "--type", "beta")
	require.NoError(t, err)
	require.NotContains(t, out, "fencing")

	_, err = runDescribe(t, cfg, "--type", "gamma")
	require.Error(t, err)
	require.ErrorContains(t, err, `unknown type "gamma"`)
	require.ErrorContains(t, err, "alpha, beta")
}

// TestDescribe_NoTypeSetsNoFlag verifies trees without a discriminator get no
// --type flag and no TYPES column.
func TestDescribe_NoTypeSetsNoFlag(t *testing.T) {
	cmd := NewDescribeCmd(DescribeConfig{Schemas: describeSchemas, CommandHint: "pve x describe"})
	require.Nil(t, cmd.Flags().Lookup("type"))

	out, err := runDescribe(t, DescribeConfig{Schemas: describeSchemas, CommandHint: "pve x describe"})
	require.NoError(t, err)
	require.NotContains(t, out, "TYPES")
}

// TestDescribe_NoClientAnnotation asserts describe skips context resolution
// and client construction: the catalog must render on a host with no PVE
// context configured. Every tree's describe comes from this constructor, so
// this single check covers them all.
func TestDescribe_NoClientAnnotation(t *testing.T) {
	cmd := NewDescribeCmd(DescribeConfig{CommandHint: "pve x describe"})
	require.Equal(t, "true", cmd.Annotations["noClient"])
}

func TestDescribe_FindsByAPIName(t *testing.T) {
	out, err := runDescribe(t, DescribeConfig{
		Schemas:     describeSchemas,
		CommandHint: "pve x describe",
	}, "max_workers")
	require.NoError(t, err)
	require.Contains(t, out, "max-workers")
}
