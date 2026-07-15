package node

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// rsyncFlags holds the options for the rsync sub-command.
type rsyncFlags struct {
	user     string
	identity string
	port     int
	dryRun   bool
	delete   bool
	progress bool
	exclude  []string
}

// newRsyncCmd builds `pmx pve node <node> rsync <src> <dst>`.
//
// Either <src> or <dst> may carry a "node:" prefix to denote the remote side;
// that prefix is rewritten to "<user>@<host>:" using the resolved node address.
func newRsyncCmd() *cobra.Command {
	var f rsyncFlags
	cmd := &cobra.Command{
		Use:   "rsync <node> <src> <dst>",
		Short: "Synchronise files to or from a node over SSH",
		Long: "Run rsync between the local machine and a cluster node over SSH. Prefix " +
			"<src> or <dst> with \"<node>:\" to denote the remote side; that prefix is " +
			"rewritten to \"<user>@<host>:\" using the node's resolved address before rsync " +
			"runs.",
		Example: `  pmx pve node rsync pve1 /local/path pve1:/remote/path
  pmx pve node rsync pve1 pve1:/remote/path /local/path --delete`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			src := args[1]
			dst := args[2]

			host, err := resolveHost(cmd, deps, node)
			if err != nil {
				return err
			}

			remotePrefix := fmt.Sprintf("%s@%s:", f.user, host)
			src = rewriteNodeRef(src, node, remotePrefix)
			dst = rewriteNodeRef(dst, node, remotePrefix)

			argv := buildRsyncArgs(&f, src, dst)

			if err := deps.Runner.Run("rsync", argv, nil, cmd.InOrStdin(),
				cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("rsync to node %q: %w", node, err)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&f.user, "user", "l", "root", "SSH login user")
	cmd.Flags().StringVarP(&f.identity, "identity", "i", "", "path to SSH identity (private key) file")
	cmd.Flags().IntVarP(&f.port, "port", "p", 22, "SSH port")
	cmd.Flags().BoolVarP(&f.dryRun, "dry-run", "n", false, "perform a trial run with no changes")
	cmd.Flags().BoolVar(&f.delete, "delete", false, "delete extraneous files from the destination")
	cmd.Flags().BoolVar(&f.progress, "progress", true, "show transfer progress")
	cmd.Flags().StringArrayVar(&f.exclude, "exclude", nil, "exclude files matching pattern (repeatable)")

	return cmd
}

// rewriteNodeRef replaces a leading "node:" prefix in path with prefix when the
// prefix matches the target node name. Paths without that prefix are returned
// unchanged.
func rewriteNodeRef(path, node, prefix string) string {
	if strings.HasPrefix(path, node+":") {
		return prefix + strings.TrimPrefix(path, node+":")
	}
	return path
}

// buildRsyncArgs assembles the rsync argv from the flags and rewritten paths.
func buildRsyncArgs(f *rsyncFlags, src, dst string) []string {
	args := make([]string, 0, 16)
	args = append(args, "-a")
	if f.progress {
		args = append(args, "--progress")
	}
	if f.dryRun {
		args = append(args, "--dry-run")
	}
	if f.delete {
		args = append(args, "--delete")
	}
	for _, ex := range f.exclude {
		args = append(args, "--exclude", ex)
	}

	sshFlags := sshcmd.Flags{Port: f.port, Identity: f.identity}
	args = append(args, "-e", sshcmd.RemoteShell(&sshFlags))

	args = append(args, src, dst)
	return args
}
