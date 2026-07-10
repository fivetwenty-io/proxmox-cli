package cli_test

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// completionCmd returns a bare command carrying a --config flag pointing at
// cfgPath, the only flag ContextNamesCompletion reads.
func completionCmd(cfgPath string) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("config", cfgPath, "")
	return cmd
}

func TestContextNamesCompletion_ListsAllWithDescriptions(t *testing.T) {
	cfgPath := makeProductConfig(t)

	got, directive := cli.ContextNamesCompletion(completionCmd(cfgPath), nil, "")

	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Equal(t, []string{
		"pbs1\tpbs@10.0.0.2",
		"pdm1\tpdm@10.0.0.3",
		"pve1\tpve@10.0.0.1",
	}, got)
}

func TestContextNamesCompletion_PrefixFilter(t *testing.T) {
	cfgPath := makeProductConfig(t)

	got, _ := cli.ContextNamesCompletion(completionCmd(cfgPath), nil, "pb")

	require.Equal(t, []string{"pbs1\tpbs@10.0.0.2"}, got)
}

func TestContextNamesCompletion_MissingConfig_SilentlyEmpty(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nope", "config.yml")

	got, directive := cli.ContextNamesCompletion(completionCmd(cfgPath), nil, "")

	require.Empty(t, got, "completion must never error the shell on a missing config")
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestProductCompletion(t *testing.T) {
	got, directive := cli.ProductCompletion(nil, nil, "")

	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Equal(t, []string{
		"pve\tProxmox VE",
		"pbs\tProxmox Backup Server",
		"pdm\tProxmox Datacenter Manager",
	}, got)
}

func TestFirstArgContextNames_SecondArgEmpty(t *testing.T) {
	cfgPath := makeProductConfig(t)

	got, _ := cli.FirstArgContextNames(completionCmd(cfgPath), []string{"already"}, "")

	require.Empty(t, got, "only the first positional completes to context names")
}
