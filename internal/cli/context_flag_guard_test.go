package cli_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/persona"
)

// TestCommandTree_NoLocalContextFlag guards every persona's whole command
// tree against the flag shadowing that broke the auth verbs.
//
// A command that registers its own flag named "context" hides the root's
// persistent one and takes its -c shorthand with it, so `pmx -c name auth
// status` failed outright with "unknown shorthand flag: 'c'". Worse, the
// local flag bypasses the root's resolution entirely, so $PMX_CONTEXT was
// ignored and `pmx auth set-token` could write a credential to a context the
// user had not named while the rest of the invocation targeted the one they
// had.
//
// A command that needs the context name must read deps.CtxName, which the
// root resolves in --context/-c > $PMX_CONTEXT > current-context order before
// the noClient early return.
func TestCommandTree_NoLocalContextFlag(t *testing.T) {
	for _, name := range []string{"pmx", "pve", "pbs", "pdm"} {
		t.Run(name, func(t *testing.T) {
			root, cleanup := cli.NewRootCmd(name)
			defer cleanup()
			cli.AddGroups(root, &cli.Deps{}, persona.Factories(name))

			var offenders []string
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				// LocalFlags omits a parent's persistent flag but keeps a
				// same-named flag the command registered itself, which is
				// exactly the shadowing this guards against.
				if c != root && c.LocalFlags().Lookup("context") != nil {
					offenders = append(offenders, c.CommandPath())
				}
				for _, sub := range c.Commands() {
					walk(sub)
				}
			}
			walk(root)

			require.Empty(t, offenders,
				"these commands register their own --context, shadowing the root's persistent flag and its -c shorthand")
		})
	}
}
