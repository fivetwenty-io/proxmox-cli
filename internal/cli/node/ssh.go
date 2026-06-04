package node

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/nodeaddr"
)

// sshFlags holds the connection options shared by ssh, shell, console, and exec.
type sshFlags struct {
	user     string
	identity string
	port     int
	agent    bool
	noStrict bool
}

// registerSSHFlags installs the shared SSH connection flags on cmd.
func registerSSHFlags(cmd *cobra.Command, f *sshFlags) {
	cmd.Flags().StringVarP(&f.user, "user", "l", "root", "SSH login user")
	cmd.Flags().StringVarP(&f.identity, "identity", "i", "", "path to SSH identity (private key) file")
	cmd.Flags().IntVarP(&f.port, "port", "p", 22, "SSH port")
	cmd.Flags().BoolVarP(&f.agent, "agent", "A", false, "enable SSH agent forwarding")
	cmd.Flags().BoolVar(&f.noStrict, "no-strict", false, "disable strict host key checking")
}

// sshBaseArgs builds the leading ssh argv (options + user@host) for the given
// host using the supplied flags. The remote command, if any, is appended by the
// caller.
func sshBaseArgs(f *sshFlags, host string) []string {
	args := make([]string, 0, 12)
	args = append(args, "-p", strconv.Itoa(f.port))
	if f.identity != "" {
		args = append(args, "-i", f.identity)
	}
	if f.agent {
		args = append(args, "-A")
	}
	if f.noStrict {
		args = append(args, "-o", "StrictHostKeyChecking=no")
	}
	args = append(args, fmt.Sprintf("%s@%s", f.user, host))
	return args
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

// newSSHCmd builds `pve node <node> ssh [-- <cmd...>]`.
func newSSHCmd() *cobra.Command {
	var f sshFlags
	cmd := &cobra.Command{
		Use:   "ssh <node> [-- <cmd>...]",
		Short: "Open an SSH session to a node (optionally run a remote command)",
		Args:  cobra.MinimumNArgs(1),
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

			if err := deps.Runner.RunInteractive("ssh", argv, nil); err != nil {
				return fmt.Errorf("ssh to node %q: %w", node, err)
			}
			return nil
		},
	}
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
