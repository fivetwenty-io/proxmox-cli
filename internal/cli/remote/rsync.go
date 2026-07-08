package remote

import (
	"fmt"
	"net"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/pmx-cli/internal/sshcmd"
)

// Rsync builds the top-level `pmx rsync` command: a thin wrapper over the
// system rsync(1) binary that rewrites scp-like "[user@]node:path" operands
// to the active context's target — the resolved cluster address of a PVE
// node, or a PBS context's single endpoint host directly — and injects
// "-e ssh ..." so the transfer authenticates the same way `pmx ssh` does. It
// carries the product:context annotation (see cli.ProductFromContext) so the
// root resolves whichever client the active context needs.
//
// DisableFlagParsing is set because rsync owns most of the short flags pmx
// would otherwise want (-l, -p, -i, ...), so cobra must never attempt to
// parse this command's own argv. Instead the command installs its own
// PersistentPreRunE, which front-extracts pmx-owned flags (long-only
// --ssh-*/--no-strict, plus -c/--context, --config, --insecure, --debug,
// -h/--help), applies them to the appropriate flag set, and then manually
// delegates to the root command's PersistentPreRunE to build *cli.Deps —
// cobra only runs the NEAREST PersistentPreRunE in the chain by default, and
// this command's own hook is nearest, so root's would otherwise never run.
func Rsync(_ *cli.Deps) *cobra.Command {
	var f sshcmd.Flags
	f.User = "root"
	f.Port = 22

	var showHelp bool
	var rsyncArgs []string

	cmd := &cobra.Command{
		Use:   "rsync [flags] <rsync-arg>...",
		Short: "Synchronise files to or from a PVE node or PBS host over SSH (node:path operands)",
		Long: `pmx rsync execs the system rsync(1) binary, rewriting any "node:path"
operand (optionally "user@node:path") to the active context's target, and
injects "-e ssh ..." so the transfer authenticates the same way "pmx ssh"
does. At least one operand must reference a remote target; every remote
operand must reference the SAME target.

Against a PVE context, the host portion names a cluster node and resolves to
its cluster management address; every remote operand must name the same
node. Against a PBS context, every remote operand is rewritten to the
context's single endpoint host directly — the host portion you type is not
looked up, so any label (e.g. "pbs:/path") works.

Because rsync owns most short flags, pmx's own connection flags are
long-only and must precede the rsync arguments: --ssh-user, --ssh-port,
--ssh-identity, --ssh-agent, --no-strict. -c/--context, --config,
--insecure, and --debug are also recognised in that same leading position.

Supplying your own -e/--rsh is rejected: pmx always injects its own.`,
		DisableFlagParsing: true,
		Annotations:        map[string]string{cli.ProductAnnotation: cli.ProductFromContext},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showHelp {
				return cmd.Help()
			}
			return runRsync(cmd, cli.GetDeps(cmd), &f, rsyncArgs)
		},
	}

	cmd.Flags().StringVar(&f.User, "ssh-user", "root", "SSH login user")
	cmd.Flags().StringVar(&f.Identity, "ssh-identity", "", "path to SSH identity (private key) file")
	cmd.Flags().IntVar(&f.Port, "ssh-port", 22, "SSH port")
	cmd.Flags().BoolVar(&f.Agent, "ssh-agent", false, "enable SSH agent forwarding")
	cmd.Flags().BoolVar(&f.NoStrict, "no-strict", false, "disable strict host key checking")

	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		vals, rest, err := extractPMXFlags(args)
		if err != nil {
			return err
		}

		// A bare "-h"/"--help" or an argv that carries no rsync arguments at
		// all (once pmx's own flags are stripped) both just show help; skip
		// building *cli.Deps entirely so a context-less invocation of
		// "pmx rsync" (or "pmx rsync -h") never fails on context resolution.
		if vals.Help || len(rest) == 0 {
			showHelp = true
			return nil
		}

		for name, val := range vals.Root {
			if err := cmd.Root().PersistentFlags().Set(name, val); err != nil {
				return fmt.Errorf("apply --%s: %w", name, err)
			}
		}
		for name, val := range vals.SSH {
			if err := cmd.Flags().Set(name, val); err != nil {
				return fmt.Errorf("apply --%s: %w", name, err)
			}
		}

		if root := cmd.Root(); root.PersistentPreRunE != nil {
			if err := root.PersistentPreRunE(cmd, rest); err != nil {
				return err
			}
		}

		rsyncArgs = rest
		return nil
	}

	return cmd
}

// runRsync classifies rsyncArgs, resolves the agreed remote target once,
// rewrites every remote operand in place (preserving an explicit "user@"
// prefix or defaulting to f.User, and bracketing a resolved IPv6 address),
// injects "-e "+sshcmd.RemoteShell(f) ahead of the caller's own arguments,
// and execs rsync.
//
// Target resolution branches on the active context's product: a PBS context
// (deps.Ctx.IsPBS()) rewrites every remote operand to deps.Ctx.Host directly
// — the host label classifyRsyncArgs extracted from the operand is not
// looked up — performing no cluster lookup; any other context resolves the
// agreed node to its cluster management address via nodeaddr.Resolve.
func runRsync(cmd *cobra.Command, deps *cli.Deps, f *sshcmd.Flags, rsyncArgs []string) error {
	ApplyContextSSHDefaults(cmd, deps, f, "ssh-user", "ssh-port", "ssh-identity")

	operands, node, err := classifyRsyncArgs(rsyncArgs)
	if err != nil {
		return err
	}

	var host string
	if deps.Ctx != nil && deps.Ctx.IsPBS() {
		host = deps.Ctx.Host
	} else {
		host, err = nodeaddr.Resolve(cmd.Context(), deps.API.Cluster, node)
		if err != nil {
			return fmt.Errorf("resolve address for node %q: %w", node, err)
		}
	}
	hostForOperand := bracketIfIPv6(host)

	rewritten := make([]string, len(rsyncArgs))
	copy(rewritten, rsyncArgs)
	for _, op := range operands {
		if !op.Remote {
			continue
		}
		user := op.User
		if user == "" {
			user = f.User
		}
		rewritten[op.Index] = fmt.Sprintf("%s@%s:%s", user, hostForOperand, op.Path)
	}

	argv := make([]string, 0, len(rewritten)+2)
	argv = append(argv, "-e", sshcmd.RemoteShell(f))
	argv = append(argv, rewritten...)

	if err := deps.Runner.Run("rsync", argv, nil, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("rsync to node %q: %w", node, err)
	}
	return nil
}

// bracketIfIPv6 wraps host in "[...]" when it parses as an IPv6 address (not
// IPv4, and not already bracketed), matching rsync's expectation that an
// IPv6 literal host in a "user@host:path" operand be bracketed so the split
// colon before the path is unambiguous.
func bracketIfIPv6(host string) string {
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() != nil {
		return host
	}
	return "[" + host + "]"
}
