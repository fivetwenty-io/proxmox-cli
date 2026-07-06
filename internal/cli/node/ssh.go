package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/cli/remote"
	"github.com/fivetwenty-io/pve-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/pve-cli/internal/sshcmd"
)

// sshFlags holds the connection options shared by ssh, shell, console, and exec.
type sshFlags = sshcmd.Flags

// registerSSHFlags installs the shared SSH connection flags on cmd.
func registerSSHFlags(cmd *cobra.Command, f *sshFlags) {
	sshcmd.RegisterFlags(cmd, f)
}

// sshBaseArgs builds the leading ssh argv (options + user@host) for the given
// host using the supplied flags. The remote command, if any, is appended by the
// caller.
func sshBaseArgs(f *sshFlags, host string) []string {
	return sshcmd.BaseArgs(f, host)
}

// resolveHost resolves node to an SSH host (IP) via cluster status, falling back
// to the node name when no address is available.
func resolveHost(cmd *cobra.Command, deps *cli.Deps, node string) (string, error) {
	host, err := nodeaddr.Resolve(cmd.Context(), deps.API.Cluster, node)
	if err != nil {
		return "", fmt.Errorf("resolve address for node %q: %w", node, err)
	}
	return host, nil
}

// newSSHCmd builds `pve node <node> ssh [ssh-option...] [command...]`. It
// shares its passthrough splitting and connection logic with the top-level
// `pve ssh` command via remote.RunSSH; SetInterspersed(false) applies the
// same "flags before <node>, ssh-option/command passthrough after" grammar
// (see remote.SSH's Long help for the full contract), so a leading-dash
// token after <node> is now treated as an ssh option/remote-command token
// rather than rejected — "pve node ssh <node> -- <cmd>..." still works
// exactly as before via the explicit "--" boundary.
func newSSHCmd() *cobra.Command {
	var f sshFlags
	cmd := &cobra.Command{
		Use:   "ssh <node> [ssh-option...] [command...]",
		Short: "Open an SSH session to a node (optionally run a remote command)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return remote.RunSSH(cmd, cli.GetDeps(cmd), &f, args[0], args[1:])
		},
	}
	cmd.Flags().SetInterspersed(false)
	registerSSHFlags(cmd, &f)
	return cmd
}

// newShellCmd builds `pve node <node> shell`.
func newShellCmd() *cobra.Command {
	var f sshFlags
	cmd := &cobra.Command{
		Use:   "shell <node>",
		Short: "Open an interactive shell on a node over SSH",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd, &f, args[0])
		},
	}
	registerSSHFlags(cmd, &f)
	return cmd
}

// newConsoleCmd builds `pve node <node> console`. In v1 this is an alias for
// shell (an interactive SSH session).
func newConsoleCmd() *cobra.Command {
	var f sshFlags
	cmd := &cobra.Command{
		Use:   "console <node>",
		Short: "Open a node console (alias for shell)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd, &f, args[0])
		},
	}
	registerSSHFlags(cmd, &f)
	return cmd
}

// runShell resolves the node host and opens an interactive SSH session with no
// remote command, allocating a login shell.
func runShell(cmd *cobra.Command, f *sshFlags, node string) error {
	deps := cli.GetDeps(cmd)

	host, err := resolveHost(cmd, deps, node)
	if err != nil {
		return err
	}

	argv := sshBaseArgs(f, host)
	if err := deps.Runner.RunInteractive("ssh", argv, nil); err != nil {
		return fmt.Errorf("shell to node %q: %w", node, err)
	}
	return nil
}

// newExecCmd builds `pve node <node> exec -- <cmd...>`. It runs a remote command
// over SSH and passes through its output.
func newExecCmd() *cobra.Command {
	var f sshFlags
	cmd := &cobra.Command{
		Use:   "exec <node> -- <cmd>...",
		Short: "Run a command on a node over SSH",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			remote := args[1:]

			host, err := resolveHost(cmd, deps, node)
			if err != nil {
				return err
			}

			argv := sshBaseArgs(&f, host)
			argv = append(argv, remote...)

			if err := deps.Runner.Run("ssh", argv, nil, cmd.InOrStdin(),
				cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("exec on node %q: %w", node, err)
			}
			return nil
		},
	}
	registerSSHFlags(cmd, &f)
	return cmd
}
