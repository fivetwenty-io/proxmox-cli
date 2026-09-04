package cli_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/persona"
)

// TestCommandTree_AsyncHelpNamesTheWaitBound guards every persona's command
// tree against a help text that explains half of the wait contract.
//
// A runnable command whose Long text tells the operator that it blocks until
// its task finishes unless --async is set is describing a wait, and the
// global --wait-timeout flag is what bounds that wait. A reader of that
// paragraph has no reason to scroll down to the global flags for a bound they
// were not told exists, so the paragraph names the flag itself.
// cli.WaitBoundHelp is the sentence that does it, and a new task-producing
// verb appends it the same way. Grouping commands are left alone, because
// their Long text points at the verbs and the verbs carry the sentence. A
// group is runnable too, since cli.RequireSubcommands installs a RunE on it
// to reject stray arguments, but that installer also marks it noClient, and a
// verb that starts a task is never noClient. A verb with sub-commands of its
// own, such as lxc migrate or node vzdump, is therefore still checked. A verb
// that mentions --async only to say it has no effect opts out with
// cli.AnnotationNoWaitBound, and the guard checks that the opt-out is not
// carrying the sentence either.
func TestCommandTree_AsyncHelpNamesTheWaitBound(t *testing.T) {
	for _, name := range []string{"pmx", "pve", "pbs", "pdm"} {
		t.Run(name, func(t *testing.T) {
			root, cleanup := cli.NewRootCmd(name)
			defer cleanup()
			cli.AddGroups(root, &cli.Deps{}, persona.Factories(name))

			var offenders, contradictions []string
			visited := 0
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				verb := c.Runnable() && c.Annotations["noClient"] == ""
				mentionsAsync := verb && strings.Contains(c.Long, "--async")
				namesBound := strings.Contains(c.Long, "--wait-timeout")
				optedOut := c.Annotations[cli.AnnotationNoWaitBound] != ""
				switch {
				case mentionsAsync && optedOut && namesBound:
					contradictions = append(contradictions, c.CommandPath())
				case mentionsAsync && !optedOut && !namesBound:
					offenders = append(offenders, c.CommandPath())
				}
				if mentionsAsync {
					visited++
				}
				for _, sub := range c.Commands() {
					walk(sub)
				}
			}
			walk(root)

			require.Empty(t, offenders,
				"these commands describe the --async wait in their help without naming the global --wait-timeout bound")
			require.Empty(t, contradictions,
				"these commands opted out of the wait bound yet their help still promises one")
			require.Greater(t, visited, 10, "the walk saw too few --async help texts to be guarding anything")
		})
	}
}
