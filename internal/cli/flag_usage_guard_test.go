package cli_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/persona"
)

// TestCommandTree_FlagUsageHasNoBackticks guards every persona's whole command
// tree against flag usage text that pflag misreads.
//
// pflag's UnquoteUsage takes the first back-quoted word in a flag's usage
// string as the flag's value placeholder, removes the quotes, and prints the
// word in the type column of --help. A usage string that back-quotes a command
// or a literal therefore renders `--keep-daily pmx pbs prune job add` where the
// reader expects `--keep-daily int`. Flag usage text must not carry a backtick
// pair; quote with plain quotes instead.
func TestCommandTree_FlagUsageHasNoBackticks(t *testing.T) {
	for _, name := range []string{"pmx", "pve", "pbs", "pdm"} {
		t.Run(name, func(t *testing.T) {
			root, cleanup := cli.NewRootCmd(name)
			defer cleanup()
			cli.AddGroups(root, &cli.Deps{}, persona.Factories(name))

			var offenders []string
			flags := 0
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				check := func(f *pflag.Flag) {
					flags++
					if strings.Contains(f.Usage, "`") {
						offenders = append(offenders, c.CommandPath()+" --"+f.Name+": "+f.Usage)
					}
				}
				c.LocalFlags().VisitAll(check)
				c.PersistentFlags().VisitAll(check)
				for _, sub := range c.Commands() {
					walk(sub)
				}
			}
			walk(root)

			require.Empty(t, offenders,
				"these flags back-quote text in their usage string, which pflag renders as the value placeholder")
			require.Greater(t, flags, 100, "the walk saw too few flags to be guarding anything")
		})
	}
}
