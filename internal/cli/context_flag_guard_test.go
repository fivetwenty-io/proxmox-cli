package cli_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// apiConnectionFlagNames lists the nine --api-* root persistent flags
// RegisterConnectionFlags installs, without their leading dashes, in the
// order it registers them. Both guard tests below share the list, and
// TestAPIConnectionFlagNames_MatchesRoot keeps it in step with the root, so
// a tenth --api-* flag cannot slip past both guards unnoticed.
var apiConnectionFlagNames = []string{
	"api-endpoint",
	"api-jump",
	"api-proxy",
	"api-proxy-from-env",
	"api-ca-cert",
	"api-fingerprint",
	"api-connect-timeout",
	"api-tls-handshake-timeout",
	"api-request-timeout",
}

// TestAPIConnectionFlagNames_MatchesRoot pins apiConnectionFlagNames to the
// set of root persistent flags whose names start with "api-". The list is a
// hand-written copy of the unexported one refuseUnusedConnectionFlags walks,
// and a flag missing from it would get neither the shadowing guard nor the
// refusal guard below.
func TestAPIConnectionFlagNames_MatchesRoot(t *testing.T) {
	for _, name := range []string{"pmx", "pve", "pbs", "pdm"} {
		t.Run(name, func(t *testing.T) {
			root, cleanup := cli.NewRootCmd(name)
			defer cleanup()

			var registered []string
			root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
				if strings.HasPrefix(f.Name, "api-") {
					registered = append(registered, f.Name)
				}
			})

			require.ElementsMatch(t, apiConnectionFlagNames, registered,
				"apiConnectionFlagNames must list exactly the root's --api-* persistent flags")
		})
	}
}

// TestCommandTree_NoLocalAPIConnectionFlag guards every persona's whole
// command tree against a local flag that shadows one of the nine --api-*
// root flags, the same way TestCommandTree_NoLocalContextFlag guards
// --context above.
//
// A command that registers its own --api-jump, say, hides the root's
// persistent flag: lookupConnectionFlag finds the command's own local flag
// first and never reaches the root's, so $PMX_API_JUMP and the flag >
// environment > context > default precedence OverridesFromCommand
// implements both go dark for that one command. The operator's --api-jump
// then talks to the wrong host, or no bastion at all, and nothing reports
// an error.
func TestCommandTree_NoLocalAPIConnectionFlag(t *testing.T) {
	for _, name := range []string{"pmx", "pve", "pbs", "pdm"} {
		t.Run(name, func(t *testing.T) {
			root, cleanup := cli.NewRootCmd(name)
			defer cleanup()
			cli.AddGroups(root, &cli.Deps{}, persona.Factories(name))

			var offenders []string
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				if c != root {
					for _, flagName := range apiConnectionFlagNames {
						if c.LocalFlags().Lookup(flagName) != nil {
							offenders = append(offenders, c.CommandPath()+" --"+flagName)
						}
					}
				}
				for _, sub := range c.Commands() {
					walk(sub)
				}
			}
			walk(root)

			require.Empty(t, offenders,
				"these commands register a local flag shadowing one of the nine root --api-* connection flags")
		})
	}
}

// isBuiltinHelpOrCompletion reports whether cmd is cobra's own help command,
// or belongs to the completion subtree (including its per-shell
// subcommands), mirroring the exemption refuseUnusedConnectionFlags applies
// through its own isHelpOrCompletion: both print text and never touch a
// connection, so neither has to refuse a changed --api-* flag.
func isBuiltinHelpOrCompletion(cmd *cobra.Command) bool {
	for c := cmd; c.HasParent(); c = c.Parent() {
		if c.Parent() == c.Root() && (c.Name() == "help" || c.Name() == "completion") {
			return true
		}
	}
	return false
}

// nearestPersistentPreRunOwner returns the command whose PersistentPreRunE
// or PersistentPreRun cobra would invoke for cmd, which is the first one set
// while walking from cmd itself up to the root. That is exactly the selection
// (*cobra.Command).execute performs before calling RunE, because the
// repository never sets cobra.EnableTraverseRunHooks. It returns nil when no
// command on the path sets either hook.
//
// A command's own PreRun or PreRunE is not part of this selection. cobra runs
// it after the persistent hook, so it can never stand in for the root's.
func nearestPersistentPreRunOwner(cmd *cobra.Command) *cobra.Command {
	for p := cmd; p != nil; p = p.Parent() {
		if p.PersistentPreRunE != nil || p.PersistentPreRun != nil {
			return p
		}
	}
	return nil
}

// runNearestPersistentPreRunE runs the persistent hook cobra would actually
// invoke for cmd, as nearestPersistentPreRunOwner selects it. A command that
// installs its own PersistentPreRunE without delegating to the root's is the
// regression this guard exists to catch, so the check runs through that
// broken hook, never through the root's, which would otherwise mask the bug.
// A bare PersistentPreRun has no error return, and a path with no hook at all
// runs nothing, so in both cases no flag can ever be refused on cmd and the
// function reports a nil error.
func runNearestPersistentPreRunE(cmd *cobra.Command, args []string) error {
	owner := nearestPersistentPreRunOwner(cmd)
	if owner == nil || owner.PersistentPreRunE == nil {
		return nil
	}
	return owner.PersistentPreRunE(cmd, args)
}

// rootHookBypass reports why cmd, a command without the noClient annotation,
// would never reach the root's PersistentPreRunE and so never build the root
// client its missing annotation promises. It returns "" when cmd does reach
// the root's hook.
//
// The root's own hook is the good case. A bare PersistentPreRun on cmd or an
// ancestor replaces it outright, since it cannot return the root hook's
// error. A PersistentPreRunE on cmd or an ancestor passes only when it
// delegates, which the check proves by swapping the root's hook for a
// recorder, running the nearest hook, and requiring the recorder to see cmd.
// A delegating hook therefore has to look the root's hook up when it runs,
// through cmd.Root(), rather than capture it when the command is built.
//
// The nearest hook runs with one placeholder operand rather than an empty
// argv. A hook may legitimately answer an empty argv with help and skip the
// client build, as pmx rsync does, while any argv that asks for real work
// has to reach the root's hook.
func rootHookBypass(root, cmd *cobra.Command) string {
	owner := nearestPersistentPreRunOwner(cmd)
	switch {
	case owner == root:
		return ""
	case owner == nil:
		return "no command on its path sets a PersistentPreRunE, so no root client is ever built"
	case owner.PersistentPreRunE == nil:
		return fmt.Sprintf("the PersistentPreRun on %q replaces the root's hook, so no root client is ever built",
			owner.CommandPath())
	}

	rootHook := root.PersistentPreRunE
	defer func() { root.PersistentPreRunE = rootHook }()

	reachedRoot := false
	root.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		if c == cmd {
			reachedRoot = true
		}
		return nil
	}

	cmd.SetContext(context.Background())
	err := owner.PersistentPreRunE(cmd, []string{"guard-probe-operand"})
	if reachedRoot {
		return ""
	}
	return fmt.Sprintf("the PersistentPreRunE on %q never delegates to the root's hook (it returned %v), "+
		"so no root client is ever built", owner.CommandPath(), err)
}

// TestCommandTree_ConnectionFlagsNeverSilent guards every persona's whole
// command tree against a command that silently ignores a changed --api-*
// flag, where `pmx context add lab --host h ... --api-jump bastion` would
// exit 0 and store no jump because operators mix up --api-jump with the
// context's own --ssh-jump.
//
// A runnable command reaches persistentPreRunE one of three ways. It can
// build a root API/PBS/PDM client itself (it is not a "noClient" command),
// and OverridesFromCommand feeds the overrides straight into that client
// build, so a changed flag always has an effect. It can carry
// cli.AnnotationUsesConnection and resolve a connection of its own without a
// root client (auth login/refresh/logout/status, context validate). Or it is
// neither, in which case refuseUnusedConnectionFlags must fail the command
// the moment a changed flag would otherwise do nothing.
//
// The first case is checked, not inferred from the missing annotation.
// rootHookBypass requires the command to reach the root's PersistentPreRunE,
// so a command that drops noClient but installs a hook of its own that never
// delegates, and so never builds the client, is reported.
//
// The third case is exercised for real, not just asserted structurally. For
// each of the nine flags in turn, the guard flips that flag's Changed bit on
// the shared root flag object (cobra keeps one *pflag.Flag per name and every
// command's merged flag set points at the same one), runs the exact
// PersistentPreRunE cobra would pick for that command through
// runNearestPersistentPreRunE, and requires the resulting error to name both
// the flag and the command path. help and the completion subtree are exempt,
// the same way refuseUnusedConnectionFlags exempts them, since neither one
// ever acts on a connection.
func TestCommandTree_ConnectionFlagsNeverSilent(t *testing.T) {
	for _, name := range []string{"pmx", "pve", "pbs", "pdm"} {
		t.Run(name, func(t *testing.T) {
			root, cleanup := cli.NewRootCmd(name)
			defer cleanup()
			cli.AddGroups(root, &cli.Deps{}, persona.Factories(name))

			// Point --config at a path that cannot exist and turn logging
			// off, so persistentPreRunE, which runs below for every noClient
			// command in the tree, never touches the developer's real config
			// file or writes a JSONL log entry for a check that never dials
			// anywhere.
			require.NoError(t, root.PersistentFlags().Set("config",
				filepath.Join(t.TempDir(), "unused-config.yaml")))
			require.NoError(t, root.PersistentFlags().Set("no-log", "true"))

			connectionFlags := make([]*pflag.Flag, 0, len(apiConnectionFlagNames))
			for _, flagName := range apiConnectionFlagNames {
				f := root.PersistentFlags().Lookup(flagName)
				require.NotNil(t, f, "root must register --%s", flagName)
				connectionFlags = append(connectionFlags, f)
			}

			var offenders []string
			checked := 0
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				if c != root && c.Runnable() && !isBuiltinHelpOrCompletion(c) {
					checked++
					switch {
					case c.Annotations["noClient"] != "true":
						if why := rootHookBypass(root, c); why != "" {
							offenders = append(offenders, fmt.Sprintf(
								"%s: has no noClient annotation, but %s", c.CommandPath(), why))
						}
					case c.Annotations[cli.AnnotationUsesConnection] == "true":
						// Resolves its own connection and reads the overrides itself.
					default:
						// cobra normally propagates root.ExecuteC's context down to
						// the resolved command before running it (see
						// (*cobra.Command).ExecuteC). Calling the hook directly
						// skips that, so setDeps's context.WithValue(cmd.Context(), ...)
						// would panic on a nil parent without this.
						c.SetContext(context.Background())

						for _, f := range connectionFlags {
							origChanged := f.Changed
							f.Changed = true
							err := runNearestPersistentPreRunE(c, []string{})
							f.Changed = origChanged

							want := fmt.Sprintf("--%s has no effect on %s", f.Name, c.CommandPath())
							if err == nil || !strings.Contains(err.Error(), want) {
								offenders = append(offenders, fmt.Sprintf(
									"%s: want an error containing %q, got %v", c.CommandPath(), want, err))
							}
						}
					}
				}
				for _, sub := range c.Commands() {
					walk(sub)
				}
			}
			walk(root)

			require.Empty(t, offenders,
				"these commands neither reach the root's client build, carry cli.AnnotationUsesConnection, "+
					"nor refuse every changed --api-* flag, so a flag would do nothing and exit 0")
			require.Greater(t, checked, 50,
				"the walk checked too few runnable commands to be guarding anything")
		})
	}
}
