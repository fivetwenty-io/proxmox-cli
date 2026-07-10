// Package remote implements the top-level `pmx ssh` and `pmx rsync` commands:
// thin wrappers over the system ssh(1)/rsync(1) binaries that connect to the
// active context's target — a PVE node's resolved cluster management address,
// or a PBS or PDM context's single endpoint host directly — before
// connecting.
package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	pmx "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/pmx-cli/internal/sshcmd"
)

// completeNodeNamesTimeout bounds how long completeNodeNames waits for the
// cluster/status request: shell completion must never make an operator wait
// noticeably, or hang, on a slow or unreachable node.
const completeNodeNamesTimeout = 3 * time.Second

// SSH builds the top-level `pmx ssh` command: a passthrough wrapper over the
// system ssh(1) binary that connects to the active context's target — a PVE
// node's resolved cluster management address, or a PBS or PDM context's
// single endpoint host directly. It carries the product:context annotation
// (see cli.ProductFromContext) so the root resolves whichever client the
// active context needs.
func SSH(deps *cli.Deps) *cobra.Command {
	var f sshcmd.Flags

	cmd := &cobra.Command{
		Use:   "ssh [node] [ssh-option...] [command...]",
		Short: "Open an SSH session to a PVE node, PBS host, or PDM host (optionally run a remote command)",
		Long: `pmx ssh connects to the active context's target and execs the system
ssh(1) binary against it. Against a PVE context, <node> resolves to its
cluster management address and is required. Against a PBS or PDM context,
ssh connects directly to the context's host; no node argument is needed or
accepted — the first token is treated as an ssh option/remote command.

The connection flags below (-l, -i, -p, -A, --no-strict) must precede
<node>. Everything after <node> is passed through to ssh verbatim: options
(e.g. -L 8080:localhost:80, -N) are reordered ahead of the destination
since ssh's own option parser does not permute arguments on every platform,
and the first token that is not an option starts the remote command. Use
"--" to force the remote-command boundary explicitly.`,
		Example: `  pmx ssh pve1
  pmx ssh pve1 -l root -- uptime
  pmx ssh --context backup
  pmx ssh -i ~/.ssh/lab_ed25519 pve1`,
		Args:        cobra.ArbitraryArgs,
		Annotations: map[string]string{cli.ProductAnnotation: cli.ProductFromContext},
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			var product string
			if deps.Ctx != nil {
				product = deps.Ctx.Product
			}
			switch product {
			case config.ProductPBS, config.ProductPDM:
				// Single-host products: address the context host directly, no
				// node argument consumed.
				return RunSSH(cmd, deps, &f, "", args)
			case config.ProductPVE, "":
				if len(args) == 0 {
					return fmt.Errorf("a node argument is required for a PVE context")
				}
				return RunSSH(cmd, deps, &f, args[0], args[1:])
			default:
				return fmt.Errorf("unsupported product %q", product)
			}
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completeNodeNames(cmd, deps, args)
		},
	}

	cmd.Flags().SetInterspersed(false)
	sshcmd.RegisterFlags(cmd, &f)

	return cmd
}

// RunSSH resolves the connection target, applies any context SSH default the
// caller did not explicitly set on f, splits rest into leading ssh options
// and a trailing remote command via sshcmd.SplitPassthrough, and execs ssh
// interactively. It is shared by `pmx ssh` and `pmx node <node> ssh` so both
// commands connect identically.
//
// Target resolution branches on the active context's product: a PBS or PDM
// context connects directly to deps.Ctx.Host, ignoring node (callers pass ""
// in that case) and performing no cluster lookup; a PVE (or empty-product)
// context requires a non-empty node and resolves it to its cluster
// management address via nodeaddr.Resolve; any other product is rejected.
func RunSSH(cmd *cobra.Command, deps *cli.Deps, f *sshcmd.Flags, node string, rest []string) error {
	ApplyContextSSHDefaults(cmd, deps, f, "user", "port", "identity")

	var product string
	if deps.Ctx != nil {
		product = deps.Ctx.Product
	}

	var host, target string
	switch product {
	case config.ProductPBS, config.ProductPDM:
		host = deps.Ctx.Host
		target = host
	case config.ProductPVE, "":
		if node == "" {
			return fmt.Errorf("a node argument is required for a PVE context")
		}
		var err error
		host, err = nodeaddr.Resolve(cmd.Context(), deps.API.Cluster, node)
		if err != nil {
			return fmt.Errorf("resolve address for node %q: %w", node, err)
		}
		target = node
	default:
		return fmt.Errorf("unsupported product %q", product)
	}

	opts, command := sshcmd.SplitPassthrough(rest)

	argv := make([]string, 0, len(opts)+len(command)+8)
	argv = append(argv, sshcmd.OptionArgs(f)...)
	argv = append(argv, opts...)
	argv = append(argv, sshcmd.Dest(f, host))
	argv = append(argv, command...)

	if err := deps.Runner.RunInteractive("ssh", argv, nil); err != nil {
		return fmt.Errorf("ssh to %q: %w", target, err)
	}
	return nil
}

// ApplyContextSSHDefaults fills any of f's User/Port/Identity fields the
// caller did not explicitly set (checked via cmd.Flags().Changed under the
// given flag names) from the active context's SSH block. An explicit flag
// always wins; a context value never overrides one the operator actually
// passed. deps or deps.Ctx being nil (no active context, e.g. a noClient
// command path) leaves f untouched.
func ApplyContextSSHDefaults(
	cmd *cobra.Command, deps *cli.Deps, f *sshcmd.Flags, userFlag, portFlag, identityFlag string,
) {
	if deps == nil || deps.Ctx == nil {
		return
	}
	block := deps.Ctx.SSH
	if block.User != "" && !cmd.Flags().Changed(userFlag) {
		f.User = block.User
	}
	if block.Port != 0 && !cmd.Flags().Changed(portFlag) {
		f.Port = block.Port
	}
	if block.Identity != "" && !cmd.Flags().Changed(identityFlag) {
		f.Identity = block.Identity
	}
}

// clusterStatusEntry is the minimal shape of each JSON object in the
// cluster.ListStatus response used for node-name completion.
type clusterStatusEntry struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// completeNodeNames completes PVE node names for the first positional
// argument of `pmx ssh` only, querying /cluster/status via a client built at
// completion time from cmd's already-parsed --config/--context/--insecure
// flag values.
//
// The deps parameter captured by the SSH factory at command-tree
// construction time is deliberately NOT used here: shell completion
// (`__complete`) never runs PersistentPreRunE, so that value is always the
// placeholder *cli.Deps{} AddGroups builds the command tree with (nil API
// client) — using it would make completion permanently dead. Cobra DOES
// parse flags before calling ValidArgsFunction on every platform, so reading
// them directly off cmd and building a fresh client via
// cli.BuildContextClient (the same helper persistentPreRunE uses) gives
// completion a real, independently-constructed client instead.
//
// It degrades silently — no completions, no file completion fallback, no
// printed error — on ANY failure (flag read, config load, context
// resolution, client construction, or the network request itself, which is
// bounded by completeNodeNamesTimeout), since a stale, unreachable, or
// misconfigured node list must never surface as a completion error or hang.
func completeNodeNames(cmd *cobra.Command, _ *cli.Deps, args []string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		// Node name is the only completed argument; nothing to suggest once
		// it has already been supplied.
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	root := cmd.Root().PersistentFlags()
	configPath, err := root.GetString("config")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	contextFlag, err := root.GetString("context")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	insecureFlag, err := root.GetBool("insecure")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// isTTY always false: completion must never block waiting on a TOFU
	// trust-decision prompt, regardless of whether cmd's stdin happens to be
	// a real terminal.
	ac, _, err := cli.BuildContextClient(cmd, cfg, configPath, contextFlag, insecureFlag, func() bool { return false })
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), completeNodeNamesTimeout)
	defer cancel()

	// Zero retries: the client's default retry policy (constants.DefaultMaxRetries,
	// linear backoff) sleeps between attempts with a plain time.Sleep that
	// ignores ctx entirely, so a slow or unreachable node's completion request
	// would otherwise run for tens of seconds regardless of the context
	// timeout above — completion must never wait that long.
	ctx = pmx.WithRetries(ctx, 0)

	resp, err := ac.Cluster.ListStatus(ctx)
	if err != nil || resp == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var names []string
	for _, raw := range *resp {
		var entry clusterStatusEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Type == "node" {
			names = append(names, entry.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
