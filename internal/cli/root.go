// Package cli wires the cobra root command, persistent flags, Deps / Ctx types,
// and the Execute / Main entry points.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/exitcode"
	"github.com/fivetwenty-io/pve-cli/internal/logx"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// noopLogCloser satisfies io.Closer for the log-init fallback path where no
// file was opened and there is nothing to close.
type noopLogCloser struct{}

func (noopLogCloser) Close() error { return nil }

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

	// Node is the resolved --node flag value (flag > PVE_NODE > context DefaultNode).
	Node string

	// Cfg is the loaded config. Never nil after PersistentPreRunE.
	Cfg *config.Config

	// ConfigPath is the resolved --config file path. Config-mutating commands
	// persist to this path via config.Save / config.SaveForce.
	ConfigPath string

	// Runner is the exec.Runner for shell-outs (ssh, rsync).
	Runner exec.Runner

	// Insecure is the raw --insecure persistent flag value, populated before
	// the noClient early-return so that noClient commands (e.g. api auth,
	// which builds its own API client outside PersistentPreRunE) can still
	// honor the flag. It is NOT merged with any context's tls.insecure here;
	// callers must OR it with the resolved context's TLS.Insecure themselves,
	// mirroring the merge PersistentPreRunE performs for normal commands
	// (see the "insecure := pf.insecure || ctx.TLS.Insecure" line below).
	Insecure bool
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

// GroupFactory is a function that constructs a cobra sub-command group given
// the placeholder Deps passed by Execute during command-tree assembly. Each
// group package exports one or more GroupFactory values; cmd/pve/main.go
// passes them as an explicit slice to Execute so there is no package-level
// mutable state.
type GroupFactory = func(*Deps) *cobra.Command

// persistentFlags holds the raw flag values read by cobra before PersistentPreRunE runs.
type persistentFlags struct {
	config   string
	context  string
	node     string
	output   string
	debug    bool
	verbose  bool
	trace    bool
	noLog    bool
	async    bool
	insecure bool
}

// NewRootCmd constructs the top-level 'pve' cobra.Command.
// It registers all persistent flags and the PersistentPreRunE hook that wires
// config, auth, API client, logger, and output renderer.
// AddGroups must be called after NewRootCmd to attach group sub-commands from
// the registry.
//
// The second return value is a cleanup function that closes the log file opened
// by PersistentPreRunE. It must be called after root.Execute() returns so that
// log records written during RunE are flushed before the fd is released. The
// function is safe to call even if PersistentPreRunE never ran (e.g. --help).
func NewRootCmd() (*cobra.Command, func()) {
	var pf persistentFlags

	// logCloser is set by persistentPreRunE and closed by the cleanup func
	// returned to the caller. It is intentionally a closed-over variable so
	// that no global mutable state is needed and tests that bypass
	// PersistentPreRunE (WithDeps injection) are unaffected.
	var activeCloser io.Closer

	cleanup := func() {
		if activeCloser != nil {
			_ = activeCloser.Close() //nolint:errcheck // best-effort flush on exit
		}
	}

	root := &cobra.Command{
		Use:   "pve",
		Short: "pve — Proxmox VE CLI",
		Long: `pve is a command-line interface for the Proxmox VE API.

It supports multiple named contexts, token and password authentication, and
structured output in table, ascii, plain, JSON, and YAML formats.`,
		// Silence cobra's built-in error printing; Execute() handles it.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// --- persistent flags ---
	root.PersistentFlags().StringVar(&pf.config, "config",
		config.DefaultPath(),
		"path to pve config file")

	root.PersistentFlags().StringVarP(&pf.context, "context", "c", "",
		"context name override (overrides $PVE_CONTEXT and current-context in config)")

	root.PersistentFlags().StringVar(&pf.node, "node",
		os.Getenv("PVE_NODE"),
		"Proxmox node name ($PVE_NODE)")

	root.PersistentFlags().StringVarP(&pf.output, "output", "o",
		resolveOutputDefault(),
		"output format: table|ascii|plain|json|yaml ($PVE_OUTPUT)")

	root.PersistentFlags().BoolVar(&pf.debug, "debug", false, "enable debug logging")
	root.PersistentFlags().BoolVar(&pf.verbose, "verbose", false, "enable verbose (debug-level) logging")
	root.PersistentFlags().BoolVar(&pf.trace, "trace", false, "enable trace (debug-level) logging")
	root.PersistentFlags().BoolVar(&pf.noLog, "no-log", false, "suppress JSONL log file creation")
	root.PersistentFlags().BoolVar(&pf.async, "async", false, "return task UPID immediately without waiting")
	root.PersistentFlags().BoolVar(&pf.insecure, "insecure", false, "disable TLS certificate verification")

	// PersistentPreRunE is invoked for every sub-command unless that command
	// overrides it explicitly.
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		closer, err := persistentPreRunE(cmd, args, &pf)
		if closer != nil {
			activeCloser = closer
		}
		return err
	}

	return root, cleanup
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
//  2. Resolves the context (flag > env > config current-context).
//  3. Skips client construction for commands annotated with Annotations["noClient"].
//  4. Resolves the secret via config.ResolveSecret.
//  5. Constructs the *apiclient.APIClient via BuildOptions + NewAPIClient.
//  6. Initialises the slog logger via logx.Init.
//  7. Injects the logger into the client.
//  8. Builds and stashes *Deps in cmd context.
//
// It returns the log file closer alongside any error. The caller (the
// PersistentPreRunE closure in NewRootCmd) captures the closer so that
// Execute() can defer it after root.Execute() returns — ensuring log records
// written during RunE are flushed before the fd is released.
func persistentPreRunE(cmd *cobra.Command, _ []string, pf *persistentFlags) (io.Closer, error) {
	// Load config file; an absent file is not an error (empty Config returned).
	cfg, err := config.Load(pf.config)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Resolve output format.
	format := output.Format(pf.output)

	// Derive command + subcommand labels for the logger from the cobra chain.
	cmdName, subName := commandLabels(cmd)

	// Initialise slog JSONL logger.
	logger, logCloser, err := logx.Init(logx.Config{
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
		logCloser = noopLogCloser{}
		fmt.Fprintf(os.Stderr, "WARN: could not initialise log file: %v\n", err)
	}
	// logCloser is returned to the caller (NewRootCmd closure → Execute) so that
	// Close() fires after root.Execute() returns, not when PreRunE returns.
	// Do NOT defer here — deferring here was the F-01 regression.

	renderer := output.New()

	deps := &Deps{
		Out:        renderer,
		Format:     format,
		Async:      pf.async,
		Log:        logger,
		Node:       pf.node,
		Cfg:        cfg,
		ConfigPath: pf.config,
		Runner:     exec.Real(),
		Insecure:   pf.insecure,
	}

	// Commands that set Annotations["noClient"]="true" skip API client build.
	// This applies to: version (build-info only), context group verbs.
	if cmd.Annotations["noClient"] == "true" {
		setDeps(cmd, deps)
		return logCloser, nil
	}

	// Resolve context — flag > env > config.
	contextName := config.Resolve(pf.context, "PVE_CONTEXT", cfg.CurrentContext, "")
	if contextName == "" {
		return logCloser, fmt.Errorf(
			"no context specified: use --context/-c, set $PVE_CONTEXT, or run 'pve context select' (config: %s)",
			pf.config,
		)
	}

	ctx, _, err := config.ResolveContext(cfg, contextName)
	if err != nil {
		return logCloser, fmt.Errorf("resolve context %q: %w", contextName, err)
	}

	// Apply per-context defaults for --node and --output.
	// Precedence: explicit flag > context default > existing global default.
	// pf.node is empty only when PVE_NODE is unset and --node was not passed.
	// Apply per-context DefaultNode when --node was not explicitly set.
	// pf.node is empty only when neither PVE_NODE nor --node was provided;
	// the node flag has no non-empty global default, so the empty-string check is safe.
	if deps.Node == "" && ctx.DefaultNode != "" {
		deps.Node = ctx.DefaultNode
	}

	// Apply per-context DefaultOutput only when --output/-o was NOT explicitly
	// set by the user AND $PVE_OUTPUT is unset.
	//
	// Precedence (high → low): explicit flag > $PVE_OUTPUT > context default-output > global default.
	//
	// $PVE_OUTPUT is baked into the flag's default value by resolveOutputDefault,
	// so cmd.Flags().Changed("output") stays false even when $PVE_OUTPUT is set.
	// The additional os.Getenv guard preserves $PVE_OUTPUT over context default-output,
	// matching the parallel treatment of $PVE_NODE (which is never overridden by context DefaultNode).
	if !cmd.Flags().Changed("output") && os.Getenv("PVE_OUTPUT") == "" && ctx.DefaultOutput != "" {
		deps.Format = output.Format(ctx.DefaultOutput)
	}

	// Resolve the secret (env ref, keychain ref, or literal).
	secret, err := config.ResolveSecret(ctx.Auth.Secret)
	if err != nil {
		return logCloser, fmt.Errorf("resolve secret for context %q: %w", contextName, err)
	}

	// Determine TLS flag: --insecure flag overrides config.
	insecure := pf.insecure || ctx.TLS.Insecure
	if insecure {
		WarnInsecureTLS(cmd.ErrOrStderr())
	}

	// Build pve.Options and construct the API client.
	var ticket, csrf, password string
	switch ctx.Auth.Type {
	case "password":
		if ctx.Auth.Session != nil && ctx.Auth.Session.Ticket != "" {
			ticket = ctx.Auth.Session.Ticket
			csrf = ctx.Auth.Session.CSRF
		} else {
			password = secret
		}
	}

	var token string
	if ctx.Auth.Type == "token" {
		// secret may be just the value; token-id comes from TokenID field.
		// Format expected by BuildOptions: "tokenid=secret" or just secret.
		if ctx.Auth.TokenID != "" {
			token = ctx.Auth.TokenID + "=" + secret
		} else {
			token = secret
		}
	}

	opts := apiclient.BuildOptions(
		ctx.Host,
		ctx.Port,
		ctx.Protocol,
		ctx.Auth.Username,
		ctx.Realm,
		token,
		password,
		ticket,
		csrf,
		insecure,
		ctx.TLS.Fingerprint,
	)

	opts = ApplyTOFUOptions(
		opts,
		ctx.TLS.Tofu,
		insecure,
		pf.config,
		contextName,
		cmd.ErrOrStderr(),
		cmd.InOrStdin(),
		func() bool { return isInteractiveInput(cmd.InOrStdin()) },
	)

	ac, err := apiclient.NewAPIClient(opts)
	if err != nil {
		return logCloser, fmt.Errorf("connect to %s: %w", ctx.Host, err)
	}

	// Inject the logger so HTTP request/response activity is captured in the
	// JSONL log with secret redaction enabled.
	ac.SetSlogLogger(logger)

	deps.API = ac
	setDeps(cmd, deps)
	return logCloser, nil
}

// ApplyTOFUOptions augments opts with Trust-On-First-Use (TOFU) certificate
// wiring (see apiclient.NewManualVerifyCallback and
// apiclient.FingerprintCachePath) when tofuEnabled is true and insecure is
// false, and returns the result. In every other case — tofuEnabled false, or
// insecure true — opts is returned completely unmodified: no
// FingerprintCachePath, no ManualVerifyCallback, normal CA-chain-only
// certificate verification, byte-identical to the pre-TOFU behavior. This is
// deliberate: a context opts in per-context via tls.tofu, and --insecure (or
// a context's tls.insecure) already means the operator has chosen to skip
// certificate verification entirely, so TOFU pinning must never re-impose a
// trust decision on top of that explicit choice.
//
// configPath and contextName derive the per-context fingerprint cache file
// path (see apiclient.FingerprintCachePath); prompt, in, and isTTY are the
// writer/reader/terminal-detector the TOFU callback uses if it activates —
// see apiclient.NewManualVerifyCallback for their exact contract.
//
// Exported so the gating logic is directly unit-testable without a real or
// mocked network connection (pve.Client does not expose the Options it was
// built from).
func ApplyTOFUOptions(
	opts pve.Options,
	tofuEnabled, insecure bool,
	configPath, contextName string,
	prompt io.Writer,
	in io.Reader,
	isTTY func() bool,
) pve.Options {
	if !tofuEnabled || insecure {
		return opts
	}

	opts.FingerprintCachePath = apiclient.FingerprintCachePath(configPath, contextName)
	opts.ManualVerifyCallback = apiclient.NewManualVerifyCallback(prompt, in, isTTY)

	return opts
}

// isInteractiveInput reports whether in is an interactive terminal, used to
// decide whether the TOFU manual-verify callback (see
// apiclient.NewManualVerifyCallback) may prompt for a trust decision. Only a
// live *os.File that the terminal package recognises as a TTY counts as
// interactive; pipes, redirected files, and the in-memory readers/buffers
// used by tests are always treated as non-interactive, so the callback fails
// closed for them exactly as it does for a genuinely non-interactive process.
func isInteractiveInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}

	return term.IsTerminal(int(f.Fd()))
}

// WarnInsecureTLS emits a stderr warning whenever TLS verification is disabled,
// so an operator who set --insecure (or context.TLS.Insecure) is reminded that
// the connection is vulnerable to interception. It is shared with the api auth
// commands, which build their own clients outside the root hook.
func WarnInsecureTLS(w io.Writer) {
	_, _ = fmt.Fprintln(w, "WARN: TLS certificate verification disabled (--insecure); connection is vulnerable to interception")
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

// AddGroups calls each factory with deps and adds the returned sub-command to
// root. It is called by Execute with the explicit factory slice provided by
// cmd/pve/main.go; there is no package-level registry.
func AddGroups(root *cobra.Command, deps *Deps, factories []GroupFactory) {
	for _, factory := range factories {
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

// Execute builds the root command, wires the provided group factories, and
// executes cobra. It returns the first error encountered, or nil on success.
//
// factories is the ordered list of GroupFactory values supplied by
// cmd/pve/main.go. The order determines the help-output listing order.
//
// The log file closer captured by PersistentPreRunE is deferred here, after
// root.Execute() returns, so that all log records written during RunE are
// flushed and the fd is released only once the full command has completed.
func Execute(factories []GroupFactory) error {
	root, cleanup := NewRootCmd()
	defer cleanup()

	// Inject a background context so that commands can always call cmd.Context().
	root.SetContext(context.Background())

	// AddGroups with a stub Deps so factories can register sub-commands; the
	// real Deps will be injected per-invocation in PersistentPreRunE.
	// Group commands MUST obtain their Deps via GetDeps(cmd), never from the
	// placeholder provided here.
	AddGroups(root, &Deps{}, factories)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	return nil
}

// Main is the entry point for cmd/pve/main.go.
// It accepts the ordered factory slice and maps the returned error to a
// semantic exit code.
func Main(factories []GroupFactory) int {
	if err := Execute(factories); err != nil {
		return exitcode.FromError(err)
	}
	return exitcode.OK
}
