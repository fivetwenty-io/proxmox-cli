package persona_test

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/persona"
)

// buildRoot assembles a full command tree the same way cmd/pmx does.
func buildRoot(t *testing.T, name string) *cobra.Command {
	t.Helper()
	root, cleanup := cli.NewRootCmd(name)
	t.Cleanup(cleanup)
	root.SetContext(context.Background())
	cli.AddGroups(root, &cli.Deps{}, persona.Factories(name))
	return root
}

func TestNames(t *testing.T) {
	require.Equal(t, []string{"pmx", "pve", "pbs", "pdm"}, persona.Names())
}

func TestFactories_PmxNestsProducts(t *testing.T) {
	root := buildRoot(t, "pmx")
	for _, product := range []string{"pve", "pbs", "pdm"} {
		c, _, err := root.Find([]string{product})
		require.NoError(t, err)
		require.Equal(t, product, c.Name(), "pmx persona must nest %s as a subcommand", product)
	}
}

func TestFactories_PveHoistsProduct(t *testing.T) {
	root := buildRoot(t, "pve")
	c, _, err := root.Find([]string{"qemu"})
	require.NoError(t, err)
	require.Equal(t, "qemu", c.Name(), "pve persona must hoist qemu to root")
	_, _, err = root.Find([]string{"pbs"})
	require.Error(t, err, "pve persona must not expose the pbs group")
}

func TestFactories_PmxHasLab(t *testing.T) {
	root := buildRoot(t, "pmx")
	c, _, err := root.Find([]string{"lab"})
	require.NoError(t, err)
	require.Equal(t, "lab", c.Name(), "pmx persona must expose the lab group")
}

func TestFactories_ProductPersonasLackLab(t *testing.T) {
	for _, name := range []string{"pve", "pbs", "pdm"} {
		root := buildRoot(t, name)
		_, _, err := root.Find([]string{"lab"})
		require.Error(t, err, "%s persona must not expose the lab group", name)
	}
}

func TestFactories_SharedPresentEverywhere(t *testing.T) {
	for _, name := range persona.Names() {
		root := buildRoot(t, name)
		for _, shared := range []string{"context", "version"} {
			c, _, err := root.Find([]string{shared})
			require.NoError(t, err, "%s persona missing shared command %s", name, shared)
			require.Equal(t, shared, c.Name())
		}
	}
}
