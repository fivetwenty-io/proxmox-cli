package cli_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// treeCmd returns a child command attached under a root named rootName,
// mirroring how persona binaries mount shared groups.
func treeCmd(rootName string) *cobra.Command {
	root := &cobra.Command{Use: rootName}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	return child
}

func TestPersonaOf(t *testing.T) {
	require.Equal(t, "pve", cli.PersonaOf(treeCmd("pve")))
	require.Equal(t, "pbs", cli.PersonaOf(treeCmd("pbs")))
	require.Equal(t, "pdm", cli.PersonaOf(treeCmd("pdm")))
	require.Equal(t, "pmx", cli.PersonaOf(treeCmd("pmx")))
	require.Equal(t, "pmx", cli.PersonaOf(treeCmd("context")), "non-persona roots fall back to pmx")
	require.Equal(t, "pmx", cli.PersonaOf(nil))
}

func TestCommandPrefix(t *testing.T) {
	require.Equal(t, "pve", cli.CommandPrefix(treeCmd("pve")))
	require.Equal(t, "pmx", cli.CommandPrefix(treeCmd("pmx")))
}
