// Package cli wires the cobra root command, persistent flags, Deps / Ctx types,
// the group-registration registry, and the Execute / Main entry points.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/exitcode"
	"github.com/fivetwenty-io/pve-cli/internal/logx"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// contextKey is an unexported type used as a key in cobra.Command Context values
// so that pve CLI data does not collide with keys from other packages.
type contextKey int

const ctxKey contextKey = 0

// Deps holds all runtime dependencies resolved once in PersistentPreRunE and
// passed to every sub-command via the cobra context.
type Deps struct {
	// API is the constructed API client. Nil for commands annotated with noClient.
	API *apiclient.APIClient

	// Out is the output renderer used by all commands.
	Out output.Renderer

	// Format is the resolved --output/-o flag value.
	Format output.Format

	// Async controls whether lifecycle commands block on task completion.
	Async bool

	// Log is the slog.Logger for this invocation.
	Log *slog.Logger

	// Node is the resolved --node flag value (flag > PVE_NODE > config DefaultNode).
	Node string

	// Cfg is the loaded config. Never nil after PersistentPreRunE.
	Cfg *config.Config

	// ConfigPath is the resolved --config file path. Config-mutating commands
	// (the api group) persist to this path via config.Save / config.SaveForce.
	ConfigPath string

	// Runner is the exec.Runner for shell-outs (ssh, rsync).
	Runner exec.Runner
}

// GetDeps retrieves *Deps from cmd's context. It panics if called before
// PersistentPreRunE has run (i.e. the context value is absent).
func GetDeps(cmd *cobra.Command) *Deps {
	v := cmd.Context().Value(ctxKey)
	if v == nil {
		panic("cli.GetDeps: Deps not set in command context — called before PersistentPreRunE?")
	}
	return v.(*Deps)
}

// setDeps stashes deps into cmd's context.
func setDeps(cmd *cobra.Command, deps *Deps) {
	cmd.SetContext(context.WithValue(cmd.Context(), ctxKey, deps))
}

// WithDeps returns ctx with deps attached so that GetDeps can later retrieve
// them. It is the supported way for group package tests to inject a pre-built
// *Deps without exercising the full PersistentPreRunE wiring:
//
//	cmd.SetContext(cli.WithDeps(context.Background(), deps))
//
// Production code does not call this directly; PersistentPreRunE uses setDeps.
func WithDeps(ctx context.Context, deps *Deps) context.Context {
	return context.WithValue(ctx, ctxKey, deps)
}

// groupFactories is the package-level registry of group command factories.
// Each command group package calls RegisterGroup from its init, and the binary
// main blank-imports the group packages to trigger that registration.
var groupFactories []func(*Deps) *cobra.Command

// RegisterGroup appends a group command factory to the global registry.
// Group packages call this function to declare their sub-tree to the root.
func RegisterGroup(f func(*Deps) *cobra.Command) {
	groupFactories = append(groupFactories, f)
}

// persistentFlags holds the raw flag values read by cobra before PersistentPreRunE runs.
type persistentFlags struct {
	config   string
	target   string
	node     string
	output   string
	debug    bool
	verbose  bool
	trace    bool
	noLog    bool
	async    bool
	insecure bool
	ascii    bool
}

// NewRootCmd constructs the top-level 'pve' cobra.Command.
// It registers all persistent flags and the PersistentPreRunE hook that wires
// config, auth, API client, logger, and output renderer.
// AddGroups must be called after NewRootCmd to attach group sub-commands from
// the registry.
func NewRootCmd() *cobra.Command {
	var pf persistentFlags

	root := &cobra.Command{
		Use:   "pve",
		Short: "pve — Proxmox VE CLI",
		Long: `pve is a command-line interface for the Proxmox VE API.

It supports multiple named targets, token and password authentication, and
structured output in table, plain, JSON, and YAML formats.`,
		// Silence cobra's built-in error printing; Execute() handles it.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// --- persistent flags ---
	root.PersistentFlags().StringVar(&pf.config, "config",
		config.DefaultPath(),
		"path to pve config file")

	root.PersistentFlags().StringVarP(&pf.target, "target", "t", "",
		"target name from config (overrides current-target)")

	root.PersistentFlags().StringVar(&pf.node, "node",
		os.Getenv("PVE_NODE"),
		"Proxmox node name ($PVE_NODE)")

	root.PersistentFlags().StringVarP(&pf.output, "output", "o",
		resolveOutputDefault(),
		"output format: table|plain|json|yaml ($PVE_OUTPUT)")

	root.PersistentFlags().BoolVar(&pf.debug, "debug", false, "enable debug logging")
	root.PersistentFlags().BoolVar(&pf.verbose, "verbose", false, "enable verbose (debug-level) logging")
	root.PersistentFlags().BoolVar(&pf.trace, "trace", false, "enable trace (debug-level) logging")
	root.PersistentFlags().BoolVar(&pf.noLog, "no-log", false, "suppress JSONL log file creation")
	root.PersistentFlags().BoolVar(&pf.async, "async", false, "return task UPID immediately without waiting")
	root.PersistentFlags().BoolVar(&pf.insecure, "insecure", false, "disable TLS certificate verification")
	root.PersistentFlags().BoolVar(&pf.ascii, "ascii", false, "ASCII-only table borders")

	// PersistentPreRunE is invoked for every sub-command unless that command
	// overrides it explicitly.
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return persistentPreRunE(cmd, args, &pf)
	}

	return root
}

// resolveOutputDefault returns the --output/-o default: PVE_OUTPUT env if set,
// otherwise "table".
func resolveOutputDefault() string {
	if v := os.Getenv("PVE_OUTPUT"); v != "" {
		return v
	}
	return string(output.FormatTable)
}

// persistentPreRunE is the implementation of root.PersistentPreRunE.
// It:
//  1. Loads config from --config path.
//  2. Resolves the target (flag > env > config current-target).
//  3. Skips client construction for commands annotated with Annotations["noClient"].
//  4. Resolves the secret via config.ResolveSecret.
//  5. Constructs the *apiclient.APIClient via BuildOptions + NewAPIClient.
//  6. Initialises the slog logger via logx.Init.
//  7. Injects the logger into the client.
//  8. Builds and stashes *Deps in cmd context.
func persistentPreRunE(cmd *cobra.Command, _ []string, pf *persistentFlags) error {
	// Load config file; an absent file is not an error (empty Config returned).
	cfg, err := config.Load(pf.config)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Resolve output format.
	format := output.Format(pf.output)

	// Derive command + subcommand labels for the logger from the cobra chain.
	cmdName, subName := commandLabels(cmd)

	// Initialise slog JSONL logger.
	logger, err := logx.Init(logx.Config{
		Debug:      pf.debug,
		Verbose:    pf.verbose,
		NoLog:      pf.noLog,
		Command:    cmdName,
		Subcommand: subName,
		Node:       pf.node,
	})
	if err != nil {
		// Non-fatal: fall back to a discard logger so the command can still run.
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelError,
		}))
		fmt.Fprintf(os.Stderr, "WARN: could not initialise log file: %v\n", err)
	}

	renderer := output.New()
	renderer.SetASCII(pf.ascii)

	deps := &Deps{
		Out:        renderer,
		Format:     format,
		Async:      pf.async,
		Log:        logger,
		Node:       resolveNode(pf.node, cfg),
		Cfg:        cfg,
		ConfigPath: pf.config,
		Runner:     exec.Real(),
	}

	// Commands that set Annotations["noClient"]="true" skip API client build.
	// This applies to: version (build-info only), api target add/remove/show,
	// api targets, api switch.
	if cmd.Annotations["noClient"] == "true" {
		setDeps(cmd, deps)
		return nil
	}

	// Resolve target — flag > env > config.
	targetName := config.Resolve(pf.target, "PVE_TARGET", cfg.CurrentTarget, "")
	if targetName == "" {
		return fmt.Errorf(
			"no target specified: use --target/-t, set PVE_TARGET, or configure current-target in %s",
			pf.config,
		)
	}

	target, _, err := config.ResolveTarget(cfg, targetName)
	if err != nil {
		return fmt.Errorf("resolve target %q: %w", targetName, err)
	}

	// Resolve the secret (env ref, keychain ref, or literal).
	secret, err := config.ResolveSecret(target.Auth.Secret)
	if err != nil {
		return fmt.Errorf("resolve secret for target %q: %w", targetName, err)
	}

	// Determine TLS flag: --insecure flag overrides config.
	insecure := pf.insecure || target.TLS.Insecure
	if insecure {
		WarnInsecureTLS(cmd.ErrOrStderr())
	}

	// Build pve.Options and construct the API client.
	var ticket, csrf, password string
	switch target.Auth.Type {
	case "password":
		if target.Auth.Session != nil && target.Auth.Session.Ticket != "" {
			ticket = target.Auth.Session.Ticket
			csrf = target.Auth.Session.CSRF
		} else {
			password = secret
		}
	}

	var token string
	if target.Auth.Type == "token" {
		// secret may be just the value; token-id comes from TokenID field.
		// Format expected by BuildOptions: "tokenid=secret" or just secret.
		if target.Auth.TokenID != "" {
			token = target.Auth.TokenID + "=" + secret
		} else {
			token = secret
		}
	}

	opts := apiclient.BuildOptions(
		target.Host,
		target.Port,
		target.Protocol,
		target.Auth.Username,
		target.Realm,
		token,
		password,
		ticket,
		csrf,
		insecure,
		target.TLS.Fingerprint,
	)

	ac, err := apiclient.NewAPIClient(opts)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", target.Host, err)
	}

	// Inject the logger so HTTP request/response activity is captured in the
	// JSONL log with secret redaction enabled.
	ac.SetSlogLogger(logger)

	deps.API = ac
	setDeps(cmd, deps)
	return nil
}

// WarnInsecureTLS emits a stderr warning whenever TLS verification is disabled,
// so an operator who set --insecure (or target.TLS.Insecure) is reminded that
// the connection is vulnerable to interception. It is shared with the api auth
// commands, which build their own clients outside the root hook.
func WarnInsecureTLS(w io.Writer) {
	_, _ = fmt.Fprintln(w, "WARN: TLS certificate verification disabled (--insecure); connection is vulnerable to interception")
}

// resolveNode returns the effective --node value.
// Priority: flag value > PVE_NODE env (already pre-applied to pf.node via flag default) > config DefaultNode.
func resolveNode(flagNode string, cfg *config.Config) string {
	if flagNode != "" {
		return flagNode
	}
	// Attempt to read the DefaultNode from the current target in config.
	if cfg == nil {
		return ""
	}
	targetName := cfg.CurrentTarget
	if targetName == "" {
		return ""
	}
	if cfg.Targets == nil {
		return ""
	}
	t, ok := cfg.Targets[targetName]
	if !ok || t == nil {
		return ""
	}
	return t.DefaultNode
}

// commandLabels extracts the command and subcommand names from the full cobra
// chain for use as log attributes.
//
// Given a command chain like "pve qemu start", it returns ("qemu", "start").
// For a top-level command like "pve version" it returns ("version", "").
func commandLabels(cmd *cobra.Command) (cmdName, subName string) {
	// Build the full name chain.
	chain := commandChain(cmd)
	// chain[0] is always "pve" (root); skip it.
	switch len(chain) {
	case 0, 1:
		return "", ""
	case 2:
		return chain[1], ""
	default:
		return chain[1], strings.Join(chain[2:], "-")
	}
}

// commandChain returns the slice of command names from root to cmd (inclusive).
func commandChain(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	var chain []string
	for c := cmd; c != nil; c = c.Parent() {
		chain = append([]string{c.Name()}, chain...)
	}
	return chain
}

// AddGroups calls each registered factory with deps and adds the returned
// sub-command to root. It is called by Execute after building root.
func AddGroups(root *cobra.Command, deps *Deps) {
	for _, factory := range groupFactories {
		root.AddCommand(factory(deps))
	}
	RequireSubcommands(root)
}

// RequireSubcommands walks the command tree rooted at cmd and, for every command
// that groups sub-commands but has no action of its own, installs a RunE that
// rejects stray positional arguments.
//
// Without it cobra treats an unknown or extra positional on a non-runnable
// grouping command (for example `pve qemu config 100`, `pve access token list`,
// or a mistyped `pve qemu bogus`) as arguments to the parent, prints help, and
// exits 0 — a silent success that is unsafe in scripts. cobra.NoArgs alone does
// not help here: a non-runnable command short-circuits to help before argument
// validation runs. Installing a RunE makes the command runnable, so the same
// invocation fails with a non-zero "unknown command" error, while a bare
// grouping command (no args) still prints its help and exits 0.
func RequireSubcommands(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		RequireSubcommands(sub)
	}
	if cmd.HasSubCommands() && cmd.Run == nil && cmd.RunE == nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath())
			}
			return c.Help()
		}
	}
}

// Execute builds the root command, registers all groups, and executes cobra.
// It returns the first error encountered, or nil on success.
func Execute() error {
	root := NewRootCmd()

	// Inject a background context so that commands can always call cmd.Context().
	root.SetContext(context.Background())

	// AddGroups with a stub Deps so factories can register sub-commands; the
	// real Deps will be injected per-invocation in PersistentPreRunE.
	// Group commands MUST obtain their Deps via GetDeps(cmd), never from the
	// placeholder provided here.
	AddGroups(root, &Deps{})

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	return nil
}

// Main is the entry point for cmd/pve/main.go.
// It calls Execute and maps the returned error to a semantic exit code.
func Main() int {
	if err := Execute(); err != nil {
		return exitcode.FromError(err)
	}
	return exitcode.OK
}
