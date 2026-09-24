// Package cli wires the cobra root command, persistent flags, Deps / Ctx types,
// and the Execute / Main entry points.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
	"github.com/fivetwenty-io/proxmox-cli/internal/logx"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
	"github.com/fivetwenty-io/proxmox-cli/internal/version"
)

// noopLogCloser satisfies io.Closer for the log-init fallback path where no
// file was opened and there is nothing to close.
type noopLogCloser struct{}

func (noopLogCloser) Close() error { return nil }

// contextKey is an unexported type used as a key in cobra.Command Context values
// so that pmx CLI data does not collide with keys from other packages.
type contextKey int

const ctxKey contextKey = 0

// Deps holds all runtime dependencies resolved once in PersistentPreRunE and
// passed to every sub-command via the cobra context.
type Deps struct {
	// API is the constructed API client. Nil for commands annotated with
	// noClient and for commands that require the PBS product (see PBS).
	API *apiclient.APIClient

	// PBS is the constructed Proxmox Backup Server client. It is non-nil
	// only for commands whose annotation chain requires the "pbs" product
	// (see ProductAnnotation); every other command gets API instead.
	PBS *apiclient.PBSClient

	// PDM is the constructed Proxmox Datacenter Manager client. It is
	// non-nil only for commands whose annotation chain requires the "pdm"
	// product (see ProductAnnotation); every other command gets API or PBS
	// instead.
	PDM *apiclient.PDMClient

	// Out is the output renderer used by all commands.
	Out output.Renderer

	// Format is the resolved --output/-o flag value.
	Format output.Format

	// Async controls whether lifecycle commands block on task completion.
	Async bool

	// WaitTimeout is the resolved --wait-timeout flag value in seconds. It says
	// how long a command waits for a task it started before it gives up and
	// names the task for the operator to follow. Zero, the default, waits until
	// the task ends. The same value is handed to apiclient.SetDefaultWaitTimeout,
	// so a command only needs to read this field when it builds its own wait
	// options rather than letting the wait funnels apply the policy.
	WaitTimeout int64

	// Log is the slog.Logger for this invocation.
	Log *slog.Logger

	// Started is when persistentPreRunE began this invocation; Execute uses
	// it for the exit audit record's duration_ms attribute.
	Started time.Time

	// Node is the resolved --node flag value (flag > PMX_NODE > context DefaultNode).
	Node string

	// NodeExplicit reports whether --node was passed on the command line with a
	// non-empty value, as opposed to Node being an ambient default from
	// PMX_NODE or the context's DefaultNode. Guest resolution (ResolveGuest)
	// trusts Node as a guest's location only when this is true; an ambient
	// default only says where node-scoped commands run, not where any
	// particular guest lives. `--node ""` is treated as if the flag were
	// absent, so it can never bypass the empty-node guards downstream.
	NodeExplicit bool

	// Cfg is the loaded config. Never nil after PersistentPreRunE.
	Cfg *config.Config

	// Ctx is the resolved active *config.Context for this invocation (the
	// entry selected by --context/-c, $PMX_CONTEXT, or current-context,
	// after ResolveContext applies its defaults). Nil for commands annotated
	// with noClient, since the noClient early-return in persistentPreRunE
	// runs before context resolution; such commands must nil-check before use.
	Ctx *config.Context

	// CtxName is the name the --context/-c flag, $PMX_CONTEXT, or
	// current-context resolved to for this invocation. Unlike Ctx it is
	// populated for noClient commands too: it is assigned before the noClient
	// early return in persistentPreRunE, precisely because those commands
	// (the context group, the auth group) are the ones that must act on the
	// context the user named rather than on current-context.
	//
	// It is empty only when nothing named a context and the config has no
	// current-context, so callers fall back to cfg.CurrentContext.
	CtxName string

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

	// Conn resolves this invocation's per-invocation connection overrides,
	// meaning the nine --api-* root flags, the eight PMX_API_* variables, and
	// the root's --insecure. persistentPreRunE assigns it before the noClient
	// early return, as it does Insecure and CtxName, so the auth and context
	// groups can reach the overrides too.
	//
	// It is a function rather than a value so that nothing parses the
	// overrides until a command asks for them. A shell-completion request
	// passes through persistentPreRunE before it reaches the target command's
	// ValidArgsFunction, and an eager parse would let a malformed
	// $PMX_API_ENDPOINT exported long ago fail every tab press. Parsed on
	// demand, the same value fails only the command that tried to connect.
	// The first call parses and every later call returns the same answer.
	//
	// Callers use ConnectionOverrides, which tolerates a nil Conn, rather
	// than calling the field directly.
	Conn func() (ConnectionOverrides, error)
}

// ConnectionOverrides returns this invocation's connection overrides by
// calling Conn. It returns the zero value, which overrides nothing, and no
// error when d or d.Conn is nil, which is the case for a Deps a test builds
// by hand and for the placeholder Deps the group factories receive.
func (d *Deps) ConnectionOverrides() (ConnectionOverrides, error) {
	if d == nil || d.Conn == nil {
		return ConnectionOverrides{}, nil
	}

	return d.Conn()
}

// GetDeps retrieves *Deps from cmd's context. It panics if called before
// PersistentPreRunE has run (i.e. the context value is absent).
func GetDeps(cmd *cobra.Command) *Deps {
	v := cmd.Context().Value(ctxKey)
	if v == nil {
		panic("cli.GetDeps: Deps not set in command context (called before PersistentPreRunE?)")
	}
	return v.(*Deps)
}

// setDeps stashes deps into cmd's context.
func setDeps(cmd *cobra.Command, deps *Deps) {
	cmd.SetContext(context.WithValue(cmd.Context(), ctxKey, deps))
}

// peekDeps returns the *Deps stashed on cmd's context, or nil when absent.
// Unlike GetDeps it never panics; Execute's error path uses it after the
// command has run, where Deps may legitimately be missing (--help, early
// flag errors).
func peekDeps(cmd *cobra.Command) *Deps {
	if cmd == nil || cmd.Context() == nil {
		return nil
	}
	d, _ := cmd.Context().Value(ctxKey).(*Deps)
	return d
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

// ProductAnnotation is the cobra Annotations key a command group sets to
// declare which Proxmox product its API calls target: config.ProductPVE
// (the default when the key is absent), config.ProductPBS, or
// config.ProductPDM. The root command's PersistentPreRunE reads the nearest
// annotation up the parent chain to decide whether to build Deps.API (PVE),
// Deps.PBS (PBS), or Deps.PDM (PDM), and the corresponding client builder
// rejects a context whose product does not match, so a PVE command can never
// silently talk to a PBS or PDM host, or vice versa.
const ProductAnnotation = "product"

// AnnotationPassthroughArgs is the cobra Annotations key a command sets to
// "true" to declare that its positional arguments carry a foreign command
// line passed through to another program (ssh remote commands, rsync argv,
// guest-agent exec argv). Such argv can embed credentials in forms no
// key=value scan recognises (e.g. `mysql -pSECRET`), so the invocation audit
// record replaces the whole args list with a redaction placeholder instead
// of logging it. Commands whose DisableFlagParsing is set are treated the
// same way without needing the annotation, since their raw argv (flags
// included) reaches PersistentPreRunE unparsed.
const AnnotationPassthroughArgs = "passthroughArgs"

// AnnotationUsesConnection is the cobra Annotations key a noClient command
// sets to "true" to declare that it resolves an API connection of its own
// and consumes the --api-* overrides through Deps.ConnectionOverrides, as
// the auth verbs that dial and context validate do. persistentPreRunE
// refuses a changed --api-* flag on every other noClient command, because
// the flag would otherwise do nothing and exit 0.
const AnnotationUsesConnection = "pmx-uses-connection"

// annotationGroupOnly marks a grouping command whose only action is the RunE
// RequireSubcommands installs, which prints help when it has no arguments
// and reports an unknown command when it has some. The refusal of an unused
// --api-* flag steps aside for such a command when it has arguments, so the
// operator sees the unknown command rather than a complaint about the flag.
const annotationGroupOnly = "pmx-group-only"

// connectionFlagNames lists the nine --api-* root flags, without their
// leading dashes, in the order RegisterConnectionFlags registers them. The
// refusal of an unused flag walks it in this order, so the first flag the
// help lists is the one it names.
var connectionFlagNames = []string{
	flagAPIEndpoint,
	flagAPIJump,
	flagAPIProxy,
	flagAPIProxyFromEnv,
	flagAPICACert,
	flagAPIFingerprint,
	flagAPIConnectTimeout,
	flagAPITLSHandshakeTimeout,
	flagAPIRequestTimeout,
}

// storedConnectionFlagHints names, for each --api-* flag, the context add
// and context update flag that stores the same setting in a context. An
// operator who passes an --api-* flag to either command most likely meant to
// store the setting, so the refusal appends the matching hint on those two
// commands only.
var storedConnectionFlagHints = map[string]string{
	flagAPIEndpoint:            "use --host, --port, and --protocol to store the endpoint",
	flagAPIJump:                "use --ssh-jump to store a bastion",
	flagAPIProxy:               "use --proxy-url to store a proxy",
	flagAPIProxyFromEnv:        "use --proxy-from-env to store the setting",
	flagAPICACert:              "use --ca-cert to store a CA certificate",
	flagAPIFingerprint:         "use --fingerprint to store a pin",
	flagAPIConnectTimeout:      "use --timeout-connect to store a timeout",
	flagAPITLSHandshakeTimeout: "use --timeout-tls-handshake to store a timeout",
	flagAPIRequestTimeout:      "use --timeout-request to store a timeout",
}

// connectionPrecedenceLine closes each persona root's long description, so
// the root's --help states once, and only there, how every connection
// setting resolves.
const connectionPrecedenceLine = "Connection settings resolve flag > environment variable > context config > " +
	"built-in default."

// ProductFromContext is the ProductAnnotation value a shared command sets to
// declare that its client should target whichever product the active context
// selects, rather than a fixed product. persistentPreRunE resolves the context
// first, then builds the matching client without a cross-product guard.
const ProductFromContext = "context"

// requiredProduct returns the product cmd requires: the nearest
// ProductAnnotation value on cmd or any ancestor, defaulting to
// config.ProductPVE when no command in the chain declares one.
func requiredProduct(cmd *cobra.Command) string {
	for c := cmd; c != nil; c = c.Parent() {
		if p, ok := c.Annotations[ProductAnnotation]; ok {
			return p
		}
	}

	return config.ProductPVE
}

// GroupFactory is a function that constructs a cobra sub-command group given
// the placeholder Deps passed by Execute during command-tree assembly. Each
// group package exports one or more GroupFactory values; cmd/pmx/main.go
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
	// waitTimeout bounds how long a command waits for a task it started.
	// Zero waits until the task ends.
	waitTimeout int64
	// wide disables the table layout budget, so columns keep their natural
	// width. The per-cell cap still applies.
	wide bool
	// warningsAsErrors is read together with its Changed state, since
	// --warnings-as-errors=false must be able to override a config file
	// that enables it (see config.ResolveBool).
	warningsAsErrors bool
}

// Persona maps the invocation name (os.Args[0]) to a command surface:
// "pve", "pbs", or "pdm" expose that product hoisted to the root; anything
// else (including "pmx", `go run` temp names, and tests) exposes the full
// tree.
func Persona(arg0 string) string {
	name := strings.TrimSuffix(filepath.Base(arg0), ".exe")
	switch name {
	case "pve", "pbs", "pdm":
		return name
	default:
		return "pmx"
	}
}

// NewRootCmd constructs the top-level cobra.Command for the given persona
// ("pmx", "pve", "pbs", or "pdm"; see Persona). It registers all persistent flags
// and the PersistentPreRunE hook that wires config, auth, API client, logger,
// and output renderer.
// AddGroups must be called after NewRootCmd to attach group sub-commands from
// the registry.
//
// The second return value is a cleanup function that closes the log file opened
// by PersistentPreRunE. It must be called after root.Execute() returns so that
// log records written during RunE are flushed before the fd is released. The
// function is safe to call even if PersistentPreRunE never ran (e.g. --help).
func NewRootCmd(persona string) (*cobra.Command, func()) {
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
		Use: persona,
		// Version enables cobra's built-in --version/-v flag. It resolves at
		// build time: `make build` injects the git-describe tag (or "dev"
		// fallback) plus the short commit; release binaries get the tag and
		// short sha from GoReleaser ldflags. Cobra prints it and exits before
		// PersistentPreRunE, so no config or API client is constructed.
		Version: version.String(),
		// Silence cobra's built-in error printing; Execute() handles it.
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	// version.String() already includes the "pmx version ..." prefix; the
	// default template would prepend "pmx version" a second time.
	root.SetVersionTemplate("{{.Version}}\n")

	// pve/pbs personas hoist that product's commands onto the root; tag the
	// root itself with ProductAnnotation so the parent-walking requiredProduct
	// lookup resolves correctly for children hoisted there. Short/Long are set
	// per persona too, so `pve --help`/`pbs --help` describe the product
	// actually exposed instead of the combined pmx tree.
	switch persona {
	case "pve":
		root.Annotations = map[string]string{ProductAnnotation: config.ProductPVE}
		root.Short = "pve: Proxmox VE CLI"
		root.Long = `pve is a command-line interface for the Proxmox VE API.

It supports multiple named contexts, token and password authentication, and
structured output in table, ascii, plain, JSON, and YAML formats.

The full combined tree (Proxmox VE and Proxmox Backup Server) is available
via the pmx binary as ` + "`pmx pve ...`" + ` / ` + "`pmx pbs ...`" + `.`
	case "pbs":
		root.Annotations = map[string]string{ProductAnnotation: config.ProductPBS}
		root.Short = "pbs: Proxmox Backup Server CLI"
		root.Long = `pbs is a command-line interface for the Proxmox Backup Server API.

It supports multiple named contexts, token and password authentication, and
structured output in table, ascii, plain, JSON, and YAML formats.

The full combined tree (Proxmox VE and Proxmox Backup Server) is available
via the pmx binary as ` + "`pmx pve ...`" + ` / ` + "`pmx pbs ...`" + `.`
	case "pdm":
		root.Annotations = map[string]string{ProductAnnotation: config.ProductPDM}
		root.Short = "pdm: Proxmox Datacenter Manager CLI"
		root.Long = `pdm is a command-line interface for the Proxmox Datacenter Manager API.

It supports multiple named contexts, token and password authentication, and
structured output in table, ascii, plain, JSON, and YAML formats. Create a
context with ` + "`pmx context add <name> --product pdm ...`" + ` before use.

The full combined tree (Proxmox VE, Proxmox Backup Server, and Proxmox
Datacenter Manager) is available via the pmx binary as ` + "`pmx pve ...`" + ` /
` + "`pmx pbs ...`" + ` / ` + "`pmx pdm ...`" + `.`
	default:
		root.Short = "pmx: Proxmox CLI"
		root.Long = `pmx is a command-line interface for the Proxmox VE and Proxmox Backup Server APIs.

It supports multiple named contexts, token and password authentication, and
structured output in table, ascii, plain, JSON, and YAML formats.`
	}

	// Every persona's long description ends with the connection precedence.
	// It lives here rather than in the usage template, because every
	// sub-command inherits the template and would repeat the line.
	root.Long += "\n\n" + connectionPrecedenceLine

	// --- persistent flags ---
	root.PersistentFlags().StringVar(&pf.config, "config",
		config.DefaultPath(),
		"path to pmx config file")

	root.PersistentFlags().StringVarP(&pf.context, "context", "c", "",
		"context name override (overrides $PMX_CONTEXT and current-context in config)")
	_ = root.RegisterFlagCompletionFunc("context", ContextNamesCompletion)

	root.PersistentFlags().StringVar(&pf.node, "node",
		os.Getenv("PMX_NODE"),
		"Proxmox node name ($PMX_NODE); guest commands pin a guest to this node only when the flag is passed explicitly")

	root.PersistentFlags().StringVarP(&pf.output, "output", "o",
		resolveOutputDefault(),
		"output format: table|ascii|plain|json|yaml ($PMX_OUTPUT)")

	root.PersistentFlags().BoolVar(&pf.debug, "debug", false, "enable debug logging")
	root.PersistentFlags().BoolVar(&pf.verbose, "verbose", false, "enable verbose (debug-level) logging")
	root.PersistentFlags().BoolVar(&pf.trace, "trace", false, "enable trace (debug-level) logging")
	root.PersistentFlags().BoolVar(&pf.noLog, "no-log", false, "suppress JSONL log file creation")
	root.PersistentFlags().BoolVar(&pf.async, "async", false, "return task UPID immediately without waiting")
	root.PersistentFlags().Int64Var(&pf.waitTimeout, "wait-timeout", 0,
		"seconds to wait for a task to finish: 0 waits until it ends; N stops waiting after N seconds "+
			"while the server keeps running it")
	root.PersistentFlags().BoolVar(&pf.insecure, "insecure", false, "disable TLS certificate verification")
	root.PersistentFlags().BoolVar(&pf.warningsAsErrors, "warnings-as-errors", false,
		"treat a task that finishes with warnings as a failure (exit 8)")
	root.PersistentFlags().BoolVar(&pf.wide, "wide", false,
		"do not shorten table columns to fit the terminal")

	// The nine --api-* connection overrides bind no field of pf, because
	// OverridesFromCommand reads them off the parsed command, together with
	// whether each one was changed and with their PMX_API_* mirrors.
	RegisterConnectionFlags(root.PersistentFlags())

	wrapFlagUsages(root)

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

// wrapFlagUsages makes `--help` wrap flag descriptions to the terminal width
// instead of emitting one unbroken line per flag. cobra's stock template calls
// pflag's FlagUsages, which never wraps, so the longest descriptions in this
// tree run past 250 columns and the terminal reflows them under the flag name
// with no indentation, hiding where one flag ends and the next begins.
//
// The template is patched rather than replaced so cobra's own layout (usage
// line, command groups, help footer) stays whatever the vendored version
// emits. Sub-commands inherit the root's template, so setting it here covers
// the whole tree.
func wrapFlagUsages(root *cobra.Command) {
	cobra.AddTemplateFunc("wrapFlags", func(fs *pflag.FlagSet) string {
		return fs.FlagUsagesWrapped(helpWidth())
	})
	root.SetUsageTemplate(strings.NewReplacer(
		".LocalFlags.FlagUsages", "wrapFlags .LocalFlags",
		".InheritedFlags.FlagUsages", "wrapFlags .InheritedFlags",
	).Replace(root.UsageTemplate()))
}

// helpWidth returns the column count to wrap flag help at, or 0 for pflag's
// unwrapped output. $COLUMNS wins so the width can be pinned in a script or a
// test; otherwise the width comes from the terminal on stdout. Output that is
// not going to a terminal stays unwrapped, keeping piped and captured help
// byte-identical to what earlier versions produced.
func helpWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 0
}

// resolveOutputDefault returns the --output/-o default: PMX_OUTPUT env if set,
// otherwise "table".
func resolveOutputDefault() string {
	if v := os.Getenv("PMX_OUTPUT"); v != "" {
		return v
	}
	return string(output.FormatTable)
}

// persistentPreRunE is the implementation of root.PersistentPreRunE.
// It:
//  1. Loads config from --config path.
//  2. Initialises the slog logger via logx.Init.
//  3. Skips client construction for commands annotated with Annotations["noClient"],
//     after refusing an --api-* flag such a command would ignore.
//  4. Resolves the context and constructs the *apiclient.APIClient via BuildContextClient.
//  5. Injects the logger into the client.
//  6. Builds and stashes *Deps in cmd context.
//
// It returns the log file closer alongside any error. The caller (the
// PersistentPreRunE closure in NewRootCmd) captures the closer so that
// Execute() can defer it after root.Execute() returns — ensuring log records
// written during RunE are flushed before the fd is released.
func persistentPreRunE(cmd *cobra.Command, args []string, pf *persistentFlags) (io.Closer, error) {
	started := time.Now()

	// Load config file; an absent file is not an error (empty Config returned).
	cfg, err := config.Load(pf.config)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Resolve output format.
	format := output.Format(pf.output)

	// Derive command + subcommand labels for the logger from the cobra chain.
	cmdName, subName := commandLabels(cmd)

	// Full command path below the root (e.g. ["pve","storage","volume","copy"])
	// drives the nested log directory layout.
	var cmdPath []string
	if chain := commandChain(cmd); len(chain) > 1 {
		cmdPath = chain[1:]
	}

	// Log level: --trace flag > $PMX_LOG_LEVEL > config log.level > "info".
	// (--debug/--verbose force debug inside logx regardless of this string.)
	var flagLevel string
	if pf.trace {
		flagLevel = "trace"
	}
	logLevel := config.Resolve(flagLevel, "PMX_LOG_LEVEL", cfg.Log.Level, config.DefaultLogLevel)

	// Log layout: $PMX_LOG_LAYOUT > config log.layout > nested default.
	logLayout := config.Resolve("", "PMX_LOG_LAYOUT", cfg.Log.Layout, config.LogLayoutNested)

	// Whether a "WARNINGS: N" task fails the command. Resolved here, before
	// any command runs, because the wait helpers that observe it sit far
	// below this hook: --warnings-as-errors > $PMX_WARNINGS_AS_ERRORS >
	// config warnings-as-errors > false (the historical behaviour).
	apiclient.SetWarningsAsErrors(config.ResolveBool(
		cmd.Flags().Changed("warnings-as-errors"), pf.warningsAsErrors,
		"PMX_WARNINGS_AS_ERRORS", cfg.WarningsAsErrors))

	// A shell-completion request is a keystroke, not an operation: it mutates
	// nothing, and cobra dispatches it through this same PersistentPreRunE.
	// Logging it opened a file per tab press under a "__complete" directory,
	// which is most of what fills the log tree on an interactive shell, and
	// no audit question is ever answered by it.
	noLog := pf.noLog || cmd.Name() == cobra.ShellCompRequestCmd

	// Initialise slog JSONL logger.
	logger, logCloser, err := logx.Init(logx.Config{
		Level:       logLevel,
		Debug:       pf.debug,
		Verbose:     pf.verbose,
		NoLog:       noLog,
		Flat:        logLayout == config.LogLayoutFlat,
		CommandPath: cmdPath,
		Command:     cmdName,
		Subcommand:  subName,
		Node:        pf.node,
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

	// Invocation audit record: every log file opens with what ran, against
	// which context and target, from which build; Execute writes the matching
	// exit record. Together they guarantee a log file is never empty and turn
	// the log into a per-invocation audit trail even for commands that make no
	// API calls.
	ctxName := config.Resolve(pf.context, "PMX_CONTEXT", cfg.CurrentContext, "")
	attrs := []any{
		slog.String("command_path", cmd.CommandPath()),
		slog.Any("args", invocationArgs(cmd, args)),
		slog.String("context", ctxName),
		slog.String("version", version.Version),
	}
	attrs = append(attrs, invocationTargetAttrs(cfg, ctxName)...)
	logger.Info("invocation", attrs...)

	renderer := output.NewWidth(resolveMaxWidth(pf.wide, cfg))

	deps := &Deps{
		Out:          renderer,
		Format:       format,
		Async:        pf.async,
		Log:          logger,
		Started:      started,
		Node:         pf.node,
		NodeExplicit: cmd.Flags().Changed("node") && pf.node != "",
		Cfg:          cfg,
		ConfigPath:   pf.config,
		Runner:       exec.Real(),
		Insecure:     pf.insecure,
		CtxName:      ctxName,
		WaitTimeout:  pf.waitTimeout,
		// Assigned here, before the noClient early return, and resolved only
		// when a command asks, so nothing below can fail on a malformed
		// override before a completion request returns.
		Conn: sync.OnceValues(func() (ConnectionOverrides, error) {
			return OverridesFromCommand(cmd)
		}),
	}

	// Stash deps NOW, before any client construction can fail: Execute's
	// exit-record and auto-prune hooks reach these deps via peekDeps after
	// ExecuteC returns, and the invocation record above must always be paired
	// with an exit record — including when context resolution or client build
	// below errors out. deps is a pointer, so the client/context fields
	// filled in further down remain visible to those hooks.
	setDeps(cmd, deps)

	// Reject a nonsensical wait bound here rather than in each of the hundred
	// or so verbs that can start a task. This runs before the noClient early
	// return below, so every command answers the same way, and it runs before
	// any client is built, so a bad flag costs no connection.
	if pf.waitTimeout < 0 {
		return logCloser, fmt.Errorf(
			"invalid --wait-timeout %d: want 0 (wait until the task ends) or a positive number of seconds",
			pf.waitTimeout)
	}

	// Now that the bound is known to make sense, record how long a command may
	// wait for a task it starts. It is process-wide state for the same reason
	// the warning policy above is, since the three wait funnels sit far below
	// this hook and every task-producing verb of every product reaches one of
	// them without carrying a bound of its own.
	apiclient.SetDefaultWaitTimeout(pf.waitTimeout)

	// cobra's own hidden shell-completion dispatcher command ("__complete",
	// aliased as "__completeNoDesc") skips everything below, and it returns
	// first, before the refusal of an unused --api-* flag, so a completion
	// request can never fail. The refusal could not fire for it anyway,
	// because "__complete" is never a noClient command, so this ordering is
	// defensive and no test can observe it.
	//
	// That command has DisableFlagParsing set by cobra itself (see
	// completions.go). cobra runs THIS PersistentPreRunE for "__complete"
	// itself, as the nearest parent with a PersistentPreRunE, before the
	// dispatcher's own Run reaches the target command's ValidArgsFunction, so
	// this invocation never sees the real --config/--context/--insecure the
	// user typed and would resolve and build a client for the DEFAULT context
	// instead. Building that client here is worse than merely wrong: if
	// resolving the default context's secret errors for any reason (missing
	// keychain entry, no context configured at all, ...), the error aborts
	// "__complete" before its Run ever calls the target's ValidArgsFunction,
	// so EVERY completion request would print an error and exit non-zero,
	// which is exactly what ValidArgsFunction implementations (e.g.
	// remote.completeNodeNames) are designed to never do. Skipping client
	// construction here leaves that correctness to the target command's OWN
	// flag parsing (which sees the real flag values) and its own
	// ValidArgsFunction, which builds any client it needs itself via
	// BuildContextClient. Deps.Conn, assigned above, is a lazy resolver for
	// the same reason, so a malformed $PMX_API_ENDPOINT cannot fail a
	// completion request either.
	if cmd.Name() == cobra.ShellCompRequestCmd {
		return logCloser, nil
	}

	// A noClient command that does not resolve a connection of its own would
	// ignore an --api-* flag and exit 0, which on context add would store a
	// context without the bastion or proxy the operator thought they gave it.
	// Refuse the flag instead. The PMX_API_* variables stay silent here,
	// because an exported variable is meant for the commands that dial.
	if err := refuseUnusedConnectionFlags(cmd, args); err != nil {
		return logCloser, err
	}

	// Commands that set Annotations["noClient"]="true" skip API client build.
	// This applies to: version (build-info only), context group verbs.
	if cmd.Annotations["noClient"] == "true" {
		return logCloser, nil
	}

	// Every command past this point dials, so parse the connection overrides
	// now. A malformed --api-* value or PMX_API_* variable then fails before
	// any context is resolved or any client is built, naming the flag or the
	// variable, instead of being ignored. Deps.Conn caches the answer, so the
	// client builders read the same overrides without parsing them again.
	if _, err := deps.ConnectionOverrides(); err != nil {
		return logCloser, err
	}

	// Resolve context — flag > env > config — and build the client for the
	// product this command requires (see ProductAnnotation): PVE commands
	// get Deps.API, 'pmx pbs' commands get Deps.PBS, and the builders
	// reject a context whose product does not match. See
	// BuildContextClient's doc comment for why this is factored out.
	isTTY := func() bool { return isInteractiveInput(cmd.InOrStdin()) }

	var (
		ac  *apiclient.APIClient
		pc  *apiclient.PBSClient
		dc  *apiclient.PDMClient
		ctx *config.Context
	)

	switch requiredProduct(cmd) {
	case config.ProductPBS:
		pc, ctx, err = BuildContextPBSClient(cmd, cfg, pf.config, pf.context, pf.insecure, isTTY)
	case config.ProductPDM:
		dc, ctx, err = BuildContextPDMClient(cmd, cfg, pf.config, pf.context, pf.insecure, isTTY)
	case ProductFromContext:
		var clients Clients
		clients, ctx, err = BuildContextAnyClient(cmd, cfg, pf.config, pf.context, pf.insecure, isTTY)
		ac, pc, dc = clients.API, clients.PBS, clients.PDM
	case config.ProductPVE:
		ac, ctx, err = BuildContextClient(cmd, cfg, pf.config, pf.context, pf.insecure, isTTY)
	default:
		err = fmt.Errorf("unsupported product %q", requiredProduct(cmd))
	}

	if err != nil {
		return logCloser, err
	}
	deps.Ctx = ctx

	// Apply per-context defaults for --node and --output.
	// Precedence: explicit flag > context default > existing global default.
	// pf.node is empty only when PMX_NODE is unset and --node was not passed.
	// Apply per-context DefaultNode when --node was not explicitly set.
	// pf.node is empty only when neither PMX_NODE nor --node was provided;
	// the node flag has no non-empty global default, so the empty-string check is safe.
	if deps.Node == "" && ctx.DefaultNode != "" {
		deps.Node = ctx.DefaultNode
	}

	// Apply per-context DefaultOutput only when --output/-o was NOT explicitly
	// set by the user AND $PMX_OUTPUT is unset.
	//
	// Precedence (high → low): explicit flag > $PMX_OUTPUT > context default-output > global default.
	//
	// $PMX_OUTPUT is baked into the flag's default value by resolveOutputDefault,
	// so cmd.Flags().Changed("output") stays false even when $PMX_OUTPUT is set.
	// The additional os.Getenv guard preserves $PMX_OUTPUT over context default-output,
	// matching the parallel treatment of $PMX_NODE (which is never overridden by context DefaultNode).
	if !cmd.Flags().Changed("output") && os.Getenv("PMX_OUTPUT") == "" && ctx.DefaultOutput != "" {
		deps.Format = output.Format(ctx.DefaultOutput)
	}

	// Inject the logger so HTTP request/response activity is captured in the
	// JSONL log with secret redaction enabled, and stash whichever client
	// was built for this command's product.
	if ac != nil {
		ac.SetSlogLogger(logger)
		deps.API = ac
	}

	if pc != nil {
		pc.SetSlogLogger(logger)
		deps.PBS = pc
	}

	if dc != nil {
		dc.SetSlogLogger(logger)
		deps.PDM = dc
	}

	return logCloser, nil
}

// refuseUnusedConnectionFlags fails when cmd is a noClient command that does
// not carry AnnotationUsesConnection and the operator changed one of the
// nine --api-* flags on its command line. Such a command builds no client
// and resolves no connection, so the flag would have no effect. It names the
// first changed flag in registration order, as
// "--api-endpoint has no effect on pmx version client", and on context add
// and context update it points each flag at the flag that stores the same
// setting instead.
//
// Three cases pass through without a refusal. cobra's help command and the
// completion subtree print text and never act on a connection, and an alias
// that bakes in an --api-* flag must not break help, as --help already
// accepts one. A grouping command given positional arguments is a mistyped
// verb, and its own RunE reports the unknown command, which is the error the
// operator needs.
//
// It reads only whether each flag was changed and never a value, so an
// empty or malformed value is refused the same way. The PMX_API_* variables
// are never consulted. It returns nil for every command that builds a root
// client or resolves a connection itself.
func refuseUnusedConnectionFlags(cmd *cobra.Command, args []string) error {
	if cmd.Annotations["noClient"] != "true" || cmd.Annotations[AnnotationUsesConnection] == "true" {
		return nil
	}

	if isHelpOrCompletion(cmd) || (cmd.Annotations[annotationGroupOnly] == "true" && len(args) > 0) {
		return nil
	}

	for _, name := range connectionFlagNames {
		f := lookupConnectionFlag(cmd, name)
		if f == nil || !f.Changed {
			continue
		}

		msg := fmt.Sprintf("--%s has no effect on %s", name, cmd.CommandPath())
		if hint, ok := storedConnectionFlagHints[name]; ok && storesContextSettings(cmd) {
			msg += "; " + hint
		}

		return errors.New(msg)
	}

	return nil
}

// storesContextSettings reports whether cmd is context add or context
// update, the two commands that write a context's stored connection
// settings, under the context group or its hidden ctx alias on the root. It
// matches the canonical names, so a verb alias such as "context create" is
// covered too, and a same-named verb elsewhere in the tree, such as a lab's
// context group, is not.
func storesContextSettings(cmd *cobra.Command) bool {
	parent := cmd.Parent()
	if parent == nil || parent.Parent() != cmd.Root() {
		return false
	}

	if parent.Name() != "context" && parent.Name() != "ctx" {
		return false
	}

	return cmd.Name() == "add" || cmd.Name() == "update"
}

// isHelpOrCompletion reports whether cmd is cobra's help command or belongs
// to the completion subtree, the built-in commands markBuiltinCommandsNoClient
// installs on the root.
func isHelpOrCompletion(cmd *cobra.Command) bool {
	for c := cmd; c.HasParent(); c = c.Parent() {
		if c.Parent() == c.Root() && (c.Name() == "help" || c.Name() == "completion") {
			return true
		}
	}

	return false
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

// BuildContextClient resolves the active *config.Context from cfg (flag >
// env > config current-context) and constructs the corresponding
// *apiclient.APIClient, applying no per-invocation connection override
// except insecureFlag. It is BuildContextClientConn with
// ConnectionOverrides{Insecure: insecureFlag}, without the resolved
// Connection. A context whose product is "pbs" or "pdm" is rejected, because
// PVE commands must never talk to a Proxmox Backup Server or Datacenter
// Manager host.
//
// It is meant for callers that target a context other than the invocation's
// own, such as the lab verbs that probe a nested lab's context. Those callers
// must not inherit an --api-* flag or a PMX_API_* variable meant for the
// outer context, so they pass only the insecure setting. A caller that builds
// a client for the invocation's own context should use BuildContextClientConn
// with the full overrides instead.
//
// contextFlag, configPath, and insecureFlag are the raw --context, --config,
// and --insecure flag values. cmd supplies ErrOrStderr and InOrStdin for the
// WarnInsecureTLS write and the TOFU prompt. isTTY decides whether the TOFU
// manual-verify callback may prompt interactively; callers that must never
// block, such as a completion helper, pass a func that always returns false.
func BuildContextClient(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, insecureFlag bool, isTTY func() bool,
) (*apiclient.APIClient, *config.Context, error) {
	ac, ctx, _, err := BuildContextClientConn(
		cmd, cfg, configPath, contextFlag, ConnectionOverrides{Insecure: insecureFlag}, isTTY)
	if err != nil {
		return nil, nil, err
	}

	return ac, ctx, nil
}

// BuildContextClientConn resolves the active context, applies the
// per-invocation connection overrides ov to it, and constructs the Proxmox VE
// client, returning the client, the resolved context, and the Connection the
// client dials. It rejects a context whose product is "pbs" or "pdm". A
// client construction failure names the host that was dialled, which is the
// overridden host under an endpoint override.
func BuildContextClientConn(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, ov ConnectionOverrides, isTTY func() bool,
) (*apiclient.APIClient, *config.Context, Connection, error) {
	opts, conn, ctx, contextName, err := buildContextOptions(cmd, cfg, configPath, contextFlag, ov, isTTY)
	if err != nil {
		return nil, nil, Connection{}, err
	}

	switch ctx.Product {
	case config.ProductPVE, "":
		// ok
	case config.ProductPBS, config.ProductPDM:
		return nil, nil, Connection{}, productMismatchError(cmd, contextName, ctx.Product, config.ProductPVE)
	default:
		return nil, nil, Connection{}, fmt.Errorf("unsupported product %q", ctx.Product)
	}

	ac, err := apiclient.NewAPIClient(opts)
	if err != nil {
		return nil, nil, Connection{}, fmt.Errorf("connect to %s: %w", conn.Host, err)
	}

	return ac, ctx, conn, nil
}

// BuildContextPBSClient is BuildContextClient's Proxmox Backup Server
// counterpart. It is BuildContextPBSClientConn with
// ConnectionOverrides{Insecure: insecureFlag}, without the resolved
// Connection.
func BuildContextPBSClient(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, insecureFlag bool, isTTY func() bool,
) (*apiclient.PBSClient, *config.Context, error) {
	pc, ctx, _, err := BuildContextPBSClientConn(
		cmd, cfg, configPath, contextFlag, ConnectionOverrides{Insecure: insecureFlag}, isTTY)
	if err != nil {
		return nil, nil, err
	}

	return pc, ctx, nil
}

// BuildContextPBSClientConn is BuildContextClientConn's Proxmox Backup Server
// counterpart: identical context resolution, override handling, secret
// handling, and TLS/TOFU wiring, but it requires the resolved context to
// declare product "pbs" and constructs an *apiclient.PBSClient.
// apiclient.NewPBSClient fills the PBS-specific option defaults (port 8007,
// PBSAPIToken, PBSAuthCookie) for any of those fields still zero-valued after
// context resolution.
func BuildContextPBSClientConn(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, ov ConnectionOverrides, isTTY func() bool,
) (*apiclient.PBSClient, *config.Context, Connection, error) {
	opts, conn, ctx, contextName, err := buildContextOptions(cmd, cfg, configPath, contextFlag, ov, isTTY)
	if err != nil {
		return nil, nil, Connection{}, err
	}

	if ctx.Product != config.ProductPBS {
		return nil, nil, Connection{}, productMismatchError(cmd, contextName, ctx.Product, config.ProductPBS)
	}

	pc, err := apiclient.NewPBSClient(opts)
	if err != nil {
		return nil, nil, Connection{}, fmt.Errorf("connect to %s: %w", conn.Host, err)
	}

	return pc, ctx, conn, nil
}

// BuildContextPDMClient is BuildContextClient's Proxmox Datacenter Manager
// counterpart. It is BuildContextPDMClientConn with
// ConnectionOverrides{Insecure: insecureFlag}, without the resolved
// Connection.
func BuildContextPDMClient(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, insecureFlag bool, isTTY func() bool,
) (*apiclient.PDMClient, *config.Context, error) {
	dc, ctx, _, err := BuildContextPDMClientConn(
		cmd, cfg, configPath, contextFlag, ConnectionOverrides{Insecure: insecureFlag}, isTTY)
	if err != nil {
		return nil, nil, err
	}

	return dc, ctx, nil
}

// BuildContextPDMClientConn is BuildContextClientConn's Proxmox Datacenter
// Manager counterpart: identical context resolution, override handling,
// secret handling, and TLS/TOFU wiring, but it requires the resolved context
// to declare product "pdm" and constructs an *apiclient.PDMClient.
// apiclient.NewPDMClient fills the PDM-specific option defaults (port 8443,
// PDMAPIToken, PDMAuthCookie) for any of those fields still zero-valued after
// context resolution.
func BuildContextPDMClientConn(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, ov ConnectionOverrides, isTTY func() bool,
) (*apiclient.PDMClient, *config.Context, Connection, error) {
	opts, conn, ctx, contextName, err := buildContextOptions(cmd, cfg, configPath, contextFlag, ov, isTTY)
	if err != nil {
		return nil, nil, Connection{}, err
	}

	if ctx.Product != config.ProductPDM {
		return nil, nil, Connection{}, productMismatchError(cmd, contextName, ctx.Product, config.ProductPDM)
	}

	dc, err := apiclient.NewPDMClient(opts)
	if err != nil {
		return nil, nil, Connection{}, fmt.Errorf("connect to %s: %w", conn.Host, err)
	}

	return dc, ctx, conn, nil
}

// productMismatchError builds the cross-product guard error for a command
// that requires the `required` product but resolved a context targeting
// ctxProduct. The advice is persona-aware: it composes commands with
// CommandPrefix so it never suggests a command the current binary does not
// provide, and it points at the right home for the context's own product
// (the product binary under a persona, the `pmx <product>` group otherwise).
func productMismatchError(cmd *cobra.Command, contextName, ctxProduct, required string) error {
	prefix := CommandPrefix(cmd)
	ctxProd := normalizedProduct(ctxProduct)

	advice := fmt.Sprintf(
		"select a %s context with '%s context select <name>' or create one with '%s context add <name> --product %s ...'",
		strings.ToUpper(required), prefix, prefix, required,
	)
	if PersonaOf(cmd) == "pmx" {
		advice += fmt.Sprintf("; %s commands for this context live under 'pmx %s'",
			ProductDisplayName(ctxProduct), ctxProd)
	} else {
		advice += fmt.Sprintf("; or use the '%s' binary for %s commands",
			ctxProd, ProductDisplayName(ctxProduct))
	}

	return fmt.Errorf(
		"context %q targets %s (product: %s); this command requires a %s context: %s",
		contextName, ProductDisplayName(ctxProduct), ctxProd, strings.ToUpper(required), advice,
	)
}

// ProductDisplayName returns the human-readable product name for user-facing
// messages, treating "" (backward-compat configs) the same as ProductPVE.
func ProductDisplayName(product string) string {
	switch product {
	case config.ProductPBS:
		return "Proxmox Backup Server"
	case config.ProductPDM:
		return "Proxmox Datacenter Manager"
	default:
		return "Proxmox VE"
	}
}

// normalizedProduct returns product, substituting ProductPVE for "" so error
// messages never print an empty product identifier for backward-compat
// configs.
func normalizedProduct(product string) string {
	if product == "" {
		return config.ProductPVE
	}
	return product
}

// Clients carries the per-product API clients a context can resolve to.
// Exactly one field is non-nil, matching the context's product.
type Clients struct {
	// API is set when the context targets Proxmox VE (product "pve" or "").
	API *apiclient.APIClient

	// PBS is set when the context targets Proxmox Backup Server.
	PBS *apiclient.PBSClient

	// PDM is set when the context targets Proxmox Datacenter Manager.
	PDM *apiclient.PDMClient
}

// BuildContextAnyClient resolves the active context product-agnostically and
// builds the client matching its product, applying no per-invocation
// connection override except insecureFlag. It is BuildContextAnyClientConn
// with ConnectionOverrides{Insecure: insecureFlag}, without the resolved
// Connection.
func BuildContextAnyClient(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, insecureFlag bool, isTTY func() bool,
) (Clients, *config.Context, error) {
	clients, ctx, _, err := BuildContextAnyClientConn(
		cmd, cfg, configPath, contextFlag, ConnectionOverrides{Insecure: insecureFlag}, isTTY)
	if err != nil {
		return Clients{}, nil, err
	}

	return clients, ctx, nil
}

// BuildContextAnyClientConn resolves the active context product-agnostically,
// applies the per-invocation connection overrides ov, and builds the client
// matching its product, returning it with the resolved context and the
// Connection the client dials. Exactly one field of the returned Clients is
// non-nil. Unlike the per-product builders it applies no cross-product guard:
// it is used only by shared commands annotated ProductFromContext, which are
// valid against any product. An unrecognized ctx.Product errors rather than
// silently falling back to a PVE client.
func BuildContextAnyClientConn(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string, ov ConnectionOverrides, isTTY func() bool,
) (Clients, *config.Context, Connection, error) {
	opts, conn, ctx, _, err := buildContextOptions(cmd, cfg, configPath, contextFlag, ov, isTTY)
	if err != nil {
		return Clients{}, nil, Connection{}, err
	}

	switch ctx.Product {
	case config.ProductPBS:
		pc, err := apiclient.NewPBSClient(opts)
		if err != nil {
			return Clients{}, nil, Connection{}, fmt.Errorf("connect to %s: %w", conn.Host, err)
		}

		return Clients{PBS: pc}, ctx, conn, nil
	case config.ProductPDM:
		dc, err := apiclient.NewPDMClient(opts)
		if err != nil {
			return Clients{}, nil, Connection{}, fmt.Errorf("connect to %s: %w", conn.Host, err)
		}

		return Clients{PDM: dc}, ctx, conn, nil
	case config.ProductPVE, "":
		ac, err := apiclient.NewAPIClient(opts)
		if err != nil {
			return Clients{}, nil, Connection{}, fmt.Errorf("connect to %s: %w", conn.Host, err)
		}

		return Clients{API: ac}, ctx, conn, nil
	default:
		return Clients{}, nil, Connection{}, fmt.Errorf("unsupported product %q", ctx.Product)
	}
}

// buildContextOptions resolves the active context in flag, environment, and
// current-context order, resolves its secret and credentials, resolves its
// connection, and returns the fully wired pve.Options that every product
// client is built from, together with the Connection it resolved, the
// resolved context, and its name, so the callers can apply their product
// guard and construct the product-appropriate client.
func buildContextOptions(
	cmd *cobra.Command, cfg *config.Config, configPath, contextFlag string,
	ov ConnectionOverrides, isTTY func() bool,
) (pve.Options, Connection, *config.Context, string, error) {
	if cfg == nil {
		return pve.Options{}, Connection{}, nil, "", fmt.Errorf("no configuration is loaded (config: %s)", configPath)
	}

	contextName := config.Resolve(contextFlag, "PMX_CONTEXT", cfg.CurrentContext, "")
	if contextName == "" {
		prefix := CommandPrefix(cmd)
		return pve.Options{}, Connection{}, nil, "", fmt.Errorf(
			"no context specified: use --context/-c, set $PMX_CONTEXT, or run '%s context select' (config: %s)",
			prefix, configPath,
		)
	}

	ctx, _, err := config.ResolveContext(cfg, contextName)
	if err != nil {
		return pve.Options{}, Connection{}, nil, "", fmt.Errorf("resolve context %q: %w", contextName, err)
	}

	opts, conn, err := ContextOptions(cmd, ctx, contextName, configPath, ov, Credentials{}, isTTY)
	if err != nil {
		return pve.Options{}, Connection{}, nil, "", err
	}

	return opts, conn, ctx, contextName, nil
}

// Credentials selects which credential BuildOptions embeds, overriding what
// ctx.Auth would produce. The auth verbs use it, because they authenticate
// with values typed on the command line rather than stored ones.
//
// When Override is false the other fields are ignored and ContextOptions
// derives the credential from the context. When it is true the fields are
// embedded exactly as given, including an empty Realm, so the caller decides
// every value. BuildOptions picks the token first, then the ticket with its
// CSRF token, then the username and password pair.
type Credentials struct {
	Override                                       bool
	Username, Realm, Token, Password, Ticket, CSRF string
}

// ContextOptions is buildContextOptions for a context the caller already
// looked up, with an explicit credential selection. The caller may pass the
// raw context stored in cfg.Contexts, because ResolveConnection clones it
// before applying defaults, and the credential is derived from a separate
// defaults-applied clone, so nothing ContextOptions does can write to it.
// It returns the Connection it resolved alongside the options, so a caller
// resolves once and reads the endpoint and the route from that one result.
// A zero creds value means "derive the credential from ctx.Auth", which is
// what every command outside the auth group wants.
//
// It runs in this order. It resolves the connection with ResolveConnection,
// so a malformed override fails before any secret is read. It derives the
// credential, resolving ctx.Auth.Secret unless creds overrides it. It warns
// on standard error when TLS verification is off and prints each of the
// connection's notes there, once per call. It builds the options for the
// resolved endpoint and trust settings, wires a CA bundle when the
// connection is not insecure, and wires trust on first use through
// ApplyTOFUOptions. When an endpoint override left the context's
// fingerprint cache read-only, it points the kit at that cache with no
// manual-verify callback, so a certificate the context already trusts
// verifies and no new one is ever written. Last, Connection.ApplyToOptions
// installs the proxy, the timeouts, and the jump dialer, and a proxy
// password that does not resolve fails there.
func ContextOptions(
	cmd *cobra.Command, ctx *config.Context, contextName, configPath string,
	ov ConnectionOverrides, creds Credentials, isTTY func() bool,
) (pve.Options, Connection, error) {
	if cmd == nil {
		return pve.Options{}, Connection{}, fmt.Errorf("build options for context %q: the command is nil", contextName)
	}

	if contextName == "" {
		return pve.Options{}, Connection{}, errors.New("build options: the context name is empty")
	}

	conn, err := ResolveConnection(contextName, ctx, ov)
	if err != nil {
		return pve.Options{}, Connection{}, err
	}

	if !creds.Override {
		creds, err = contextCredentials(ctx, contextName)
		if err != nil {
			return pve.Options{}, Connection{}, err
		}
	}

	stderr := cmd.ErrOrStderr()

	if conn.Insecure {
		WarnInsecureTLS(stderr)
	}

	for _, note := range conn.Notes {
		_, _ = fmt.Fprintln(stderr, note)
	}

	opts := apiclient.BuildOptions(
		conn.Host,
		conn.Port,
		conn.Protocol,
		creds.Username,
		creds.Realm,
		creds.Token,
		creds.Password,
		creds.Ticket,
		creds.CSRF,
		conn.Insecure,
		conn.Fingerprint,
	)

	if conn.CACert != "" && !conn.Insecure {
		// A declared CA bundle replaces system roots and enables hostname
		// verification. Explicit fingerprint or TOFU policies retain their
		// existing precedence over CA verification in the SDK.
		opts.SSLOptions = &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyPeer,
			VerifyHostname: true,
			CACert:         conn.CACert,
		}
	}

	opts = ApplyTOFUOptions(
		opts,
		conn.TOFU,
		conn.Insecure,
		configPath,
		contextName,
		stderr,
		cmd.InOrStdin(),
		isTTY,
	)

	if conn.TOFUReadOnly && !conn.Insecure {
		// The kit reads the cache and, with no callback to ask, rejects a
		// certificate it does not hold instead of recording it, so an
		// overridden host can never be trusted into this context's cache.
		opts.FingerprintCachePath = apiclient.FingerprintCachePath(configPath, contextName)
		opts.ManualVerifyCallback = nil
	}

	// The transport comes last deliberately: the proxy and the jump change
	// only where the TCP connection originates, and every TLS decision above
	// still applies against conn.Host at the far end.
	opts, err = conn.ApplyToOptions(opts)
	if err != nil {
		return pve.Options{}, Connection{}, err
	}

	return opts, conn, nil
}

// contextCredentials derives the credential BuildOptions embeds from ctx's
// auth block, on a defaults-applied clone so a context that omits its realm
// still authenticates against "pam" without that default being written back.
// It resolves the secret whatever the auth type, as the root always has, so
// a secret reference that does not resolve fails every client build. A
// password context with a stored session uses the session's ticket and CSRF
// token instead of the password, and a token context embeds the token as
// "tokenid=secret", or the bare secret when no token ID is stored.
func contextCredentials(ctx *config.Context, contextName string) (Credentials, error) {
	resolved := config.CloneContext(ctx)
	config.ApplyDefaults(resolved)

	secret, err := config.ResolveSecret(resolved.Auth.Secret)
	if err != nil {
		return Credentials{}, fmt.Errorf("resolve secret for context %q: %w", contextName, err)
	}

	creds := Credentials{Username: resolved.Auth.Username, Realm: resolved.Realm}

	switch resolved.Auth.Type {
	case "password":
		if session := resolved.Auth.Session; session != nil && session.Ticket != "" {
			creds.Ticket, creds.CSRF = session.Ticket, session.CSRF
		} else {
			creds.Password = secret
		}
	case "token":
		if resolved.Auth.TokenID != "" {
			creds.Token = resolved.Auth.TokenID + "=" + secret
		} else {
			creds.Token = secret
		}
	}

	return creds, nil
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

// sensitiveArgMarkers are the lowercase substrings that mark the key of a
// key=value positional argument as containing a credential; redactArgs masks
// the value of any match.
var sensitiveArgMarkers = []string{
	"password", "passwd", "passphrase", "secret", "token", "ticket", "csrf",
	"credential", "apikey", "privatekey",
}

// redactArgs returns a copy of args with the value of every sensitive
// key=value pair replaced by "***". Only positional arguments reach this
// function — cobra has already consumed flags — and no current command
// accepts a credential positionally (`pmx api` form params travel via
// -d/--data, and `access password set` refuses a positional password), so
// this is defence in depth for future commands and operator typos, not a
// hot path. Over-matching is deliberate: masking a non-secret costs
// nothing, leaking a secret into the audit log is unrecoverable. Passthrough
// argv (ssh/rsync/guest exec) never reaches this function — see
// invocationArgs and AnnotationPassthroughArgs.
func redactArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a
		key, _, ok := strings.Cut(a, "=")
		if !ok {
			continue
		}
		lower := strings.ToLower(key)
		for _, marker := range sensitiveArgMarkers {
			if strings.Contains(lower, marker) {
				out[i] = key + "=***"
				break
			}
		}
	}
	return out
}

// invocationArgs returns the args slice to embed in the invocation audit
// record. For passthrough commands — DisableFlagParsing set, or
// AnnotationPassthroughArgs declared — the entire argv is replaced with a
// count-only placeholder, because a foreign command line can embed secrets
// in forms redactArgs cannot recognise (`mysql -pSECRET`, `curl -u u:p`).
// Everything else gets key=value masking via redactArgs.
func invocationArgs(cmd *cobra.Command, args []string) []string {
	if cmd.DisableFlagParsing || cmd.Annotations[AnnotationPassthroughArgs] == "true" {
		return []string{fmt.Sprintf("(%d passthrough args redacted)", len(args))}
	}
	return redactArgs(args)
}

// invocationTargetAttrs returns the audit attributes naming what an
// invocation was pointed at: the host, its product, and the authenticated
// user. The context name alone does not answer "which machine did this
// mutation hit" — contexts get renamed, repointed at a different host, and
// copied between machines, so a log read months later cannot resolve one back
// to a target.
//
// Resolution is config-only (no network, no secret lookup), and a context that
// does not resolve simply contributes nothing: enriching an audit record must
// never be able to fail a command. The secret itself is never recorded.
func invocationTargetAttrs(cfg *config.Config, name string) []any {
	if cfg == nil || name == "" {
		return nil
	}
	ctx, _, err := config.ResolveContext(cfg, name)
	if err != nil || ctx == nil {
		return nil
	}

	attrs := []any{
		slog.String("host", ctx.Host),
		slog.Int("port", ctx.Port),
		slog.String("product", ctx.Product),
	}
	if ctx.Auth.Username != "" {
		attrs = append(attrs, slog.String("user", ctx.Auth.Username))
	}
	return attrs
}

// logInvocationExit writes the exit audit record matching the invocation
// record persistentPreRunE opened the log with: the semantic exit code,
// the wall-clock duration, and (on failure, at error level so it survives
// any configured log.level) the error text.
func logInvocationExit(c *cobra.Command, err error) {
	deps := peekDeps(c)
	if deps == nil || deps.Log == nil {
		return
	}

	attrs := []any{slog.Int("exit_code", exitcode.FromError(err))}
	if !deps.Started.IsZero() {
		attrs = append(attrs, slog.Int64("duration_ms", time.Since(deps.Started).Milliseconds()))
	}

	if err != nil {
		// The error text routinely quotes the failing request URL, whose
		// query string carries GET/DELETE parameters — including a
		// --password on the commands that take one.
		deps.Log.Error("exit", append(attrs, slog.String("error", redact.QueryParams(err.Error())))...)
		return
	}
	deps.Log.Info("exit", attrs...)
}

// maybeAutoPrune runs the best-effort daily log prune when the loaded config
// sets a positive log.retention. Outcomes land only in the invocation log;
// pruning must never change the command's own result or output.
func maybeAutoPrune(c *cobra.Command) {
	deps := peekDeps(c)
	if deps == nil {
		return
	}
	retention := config.EffectiveLogRetention(deps.Cfg)
	if retention <= 0 {
		return
	}

	dir, err := logx.DefaultDir()
	if err != nil {
		return
	}

	stats, ran, err := logx.AutoPrune(dir, retention)
	if !ran || deps.Log == nil {
		return
	}
	if err != nil {
		deps.Log.Warn("log auto-prune", slog.String("error", err.Error()))
		return
	}
	deps.Log.Info("log auto-prune",
		slog.Int("files", stats.Files),
		slog.Int("empty", stats.Empty),
		slog.Int64("bytes", stats.Bytes),
		slog.Int("dirs", stats.Dirs),
	)
}

// commandLabels extracts the command and subcommand names from the full cobra
// chain for use as log attributes.
//
// Given a command chain like "pmx pve qemu start", it returns ("qemu", "start").
// For a top-level command like "pmx version" it returns ("version", "").
func commandLabels(cmd *cobra.Command) (cmdName, subName string) {
	// Build the full name chain.
	chain := commandChain(cmd)
	// chain[0] is always "pmx" (root); skip it.
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
// cmd/pmx/main.go; there is no package-level registry.
func AddGroups(root *cobra.Command, deps *Deps, factories []GroupFactory) {
	for _, factory := range factories {
		root.AddCommand(factory(deps))
	}
	NormalizeAliases(root)
	RequireSubcommands(root)
	markBuiltinCommandsNoClient(root)
}

// markBuiltinCommandsNoClient installs cobra's default "help" and
// "completion" commands early and marks them (and completion's per-shell
// subcommands) with Annotations["noClient"]="true".
//
// Cobra normally creates these commands lazily inside (*Command).ExecuteC,
// which runs after PersistentPreRunE would already need to see the
// annotation for that very invocation — so by the time they exist,
// persistentPreRunE has already required a configured context and failed.
// Calling InitDefaultHelpCmd/InitDefaultCompletionCmd here, once the rest of
// the tree is assembled, creates them up front so the annotation can be set
// before root.Execute() ever runs; cobra's own lazy init later sees each
// command already present (by name) and leaves it alone. The annotation
// check in persistentPreRunE is per-command, not inherited, so each
// completion subcommand (bash/zsh/fish/powershell) needs it set too.
func markBuiltinCommandsNoClient(root *cobra.Command) {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	for _, sub := range root.Commands() {
		switch sub.Name() {
		case "help", "completion":
			setNoClient(sub)
			for _, grand := range sub.Commands() {
				setNoClient(grand)
			}
		}
	}
}

// setNoClient sets Annotations["noClient"]="true" on cmd, initializing the
// map if necessary.
func setNoClient(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations["noClient"] = "true"
}

// RequireSubcommands walks the command tree rooted at cmd and, for every command
// that groups sub-commands but has no action of its own, installs a RunE that
// rejects stray positional arguments.
//
// Without it cobra treats an unknown or extra positional on a non-runnable
// grouping command (for example `pmx pve qemu config 100`, `pmx pve access token list`,
// or a mistyped `pmx pve qemu bogus`) as arguments to the parent, prints help, and
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
		// Installing that RunE made the grouping command runnable, which also
		// made it subject to client construction — so a bare `pmx pve`,
		// `pmx version`, or `pmx context` resolved the context secret and
		// shelled out to the keychain purely to print help, and failed with a
		// credential error instead of helping when that lookup failed. Neither
		// branch above touches an API client. noClient is checked per-command
		// rather than inherited, so this stays contained to the grouping
		// commands themselves, and the unknown-command branch still exits
		// non-zero.
		setNoClient(cmd)
		cmd.Annotations[annotationGroupOnly] = "true"
	}
}

// signalContext returns the root command context, cancelled by the first
// SIGINT, SIGTERM, or SIGHUP, plus a stop function the caller defers.
//
// Cancelling rather than dying is what makes the cancellation handling the
// tree already carries reachable: the task-wait poll, the lab SSH wait, and
// the SDK's retry backoff all select on ctx.Done() but could never observe it
// while the root context was context.Background(). It also means Execute
// still reaches its exit audit record, its retention prune, and its log-file
// close on the interrupted path, instead of leaving an invocation record with
// no matching exit record — indistinguishable from a crash.
//
// SIGHUP is caught because an ssh jump child runs in a session of its own and
// no longer hears the terminal's hangup, so pmx has to end it. It is caught
// only when pmx did not start with it ignored: signal.Notify would un-ignore
// it, and a `nohup pmx ...`, or a pmx that a service manager or CI runner
// starts with SIGHUP ignored, must still run to completion when the operator
// logs out.
//
// Only the first signal is absorbed. The second always kills every jump
// child's process group at once, restores the disposition pmx started with,
// and delivers the same signal to pmx again, so an operator whose command is
// not unwinding fast enough is never trapped and no jump child outlives pmx.
// pmx then dies of that signal unless another handler still holds it, such
// as internal/exec's shield while it relays signals to a shielded child, in
// which case pmx keeps unwinding with the jump registry closed. The shield
// then also relays the re-raised copy, so its child receives that second
// signal once more than it would without this handler, which is harmless
// for the ssh sessions the shield protects. On Windows,
// where a signal cannot be delivered to pmx itself, it exits at once with
// the generic failure status.
func signalContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())

	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		sigs = append(sigs, syscall.SIGHUP)
	}

	ch := make(chan os.Signal, 2)
	signal.Notify(ch, sigs...)

	done := make(chan struct{})

	go func() {
		select {
		case <-ch:
			cancel()
		case <-done:
			return
		}

		select {
		case sig := <-ch:
			apiclient.KillJumps()
			signal.Stop(ch)
			redeliverSignal(sig)
		case <-done:
		}
	}()

	var once sync.Once

	return ctx, func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
			cancel()
		})
	}
}

// redeliverSignal sends sig to pmx again once its handler has been stopped,
// so pmx dies of the signal the operator sent. There is deliberately no
// os.Exit fallback on Unix, because that would orphan the shielded child the
// exec runner exists to protect. Windows supports only a kill through
// Process.Signal, so pmx exits there with the generic failure status.
func redeliverSignal(sig os.Signal) {
	if runtime.GOOS == "windows" {
		os.Exit(exitcode.Generic)
	}

	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(sig)
	}
}

// Execute builds the root command, wires the provided group factories, and
// executes cobra. It returns the first error encountered, or nil on success.
//
// factories is the ordered list of GroupFactory values supplied by
// cmd/pmx/main.go. The order determines the help-output listing order.
//
// The log file closer captured by PersistentPreRunE is deferred here, after
// root.Execute() returns, so that all log records written during RunE are
// flushed and the fd is released only once the full command has completed.
func Execute(persona string, factories []GroupFactory) error {
	// jumpShutdownBound is how long established ssh jump children get to
	// exit on end-of-file before their process groups are killed.
	const jumpShutdownBound = 2 * time.Second

	root, cleanup := NewRootCmd(persona)
	defer cleanup()

	// Inject the signal-cancelled context so that commands can always call
	// cmd.Context(), and so ^C unwinds the command instead of killing pmx
	// outright.
	ctx, stopSignals := signalContext()
	defer stopSignals()
	root.SetContext(ctx)

	// AddGroups with a stub Deps so factories can register sub-commands; the
	// real Deps will be injected per-invocation in PersistentPreRunE.
	// Group commands MUST obtain their Deps via GetDeps(cmd), never from the
	// placeholder provided here.
	AddGroups(root, &Deps{}, factories)

	c, err := root.ExecuteC()

	// Exit audit record first (so duration reflects the command itself),
	// then the daily retention prune; both write to the still-open log file
	// closed by the deferred cleanup above.
	logInvocationExit(c, err)
	maybeAutoPrune(c)

	// Reap every ssh jump child before pmx exits: a background goroutine
	// dies with the process, and main calls os.Exit as soon as this returns.
	// It runs after the exit record, so the audited duration excludes the
	// teardown, and before the error is printed, on both paths.
	apiclient.ShutdownJumps(jumpShutdownBound)

	if err != nil {
		// A child process (ssh, rsync) that had our real stdout/stderr wired
		// to it directly (RunInteractive, or a Run call passed
		// cmd.OutOrStdout()/cmd.ErrOrStderr()) has already written its own
		// diagnostics to stderr; printing the wrapped *exec.ExitError here
		// too would duplicate that output with a redundant second line.
		//
		// But a Run call that captured the child's stdout/stderr into its
		// own in-memory buffers instead of passing them through (e.g.
		// internal/cli/lab.runGuestSSH) has NOT shown the user anything —
		// suppressing it here under the same assumption would silently
		// swallow the entire error, captured stderr and all. Such callers
		// mark their returned error with exec.CapturedError specifically so
		// this path knows to print it after all, in full, rather than
		// assume a captured-but-never-displayed diagnostic was already
		// shown (see TestExecute_CapturedGuestSSHExitErrorIsPrinted for the
		// exact silent-255-with-zero-output failure this fixes).
		var captured *exec.CapturedError
		isCaptured := errors.As(err, &captured)

		var exitErr *exec.ExitError
		if isCaptured || !errors.As(err, &exitErr) {
			// Redacted for the same reason as the exit record above:
			// terminal scrollback and CI job logs are no safer a home for a
			// credential than the log file is.
			fmt.Fprintln(os.Stderr, redact.QueryParams(err.Error()))
			if hint := AuthHint(err); hint != "" {
				fmt.Fprintln(os.Stderr, hint)
			}
			if deps := peekDeps(c); deps != nil && deps.Ctx != nil {
				// Port convention is the more specific diagnosis ("right host,
				// wrong product port"), so it wins; the unreachable hint covers
				// every other connection failure.
				if hint := PortConventionHint(err, deps.Ctx, deps.CtxName, CommandPrefix(c)); hint != "" {
					fmt.Fprintln(os.Stderr, hint)
				} else if hint := UnreachableHint(err, deps.Ctx, deps.CtxName, CommandPrefix(c)); hint != "" {
					fmt.Fprintln(os.Stderr, hint)
				}
			}
		}
		return err
	}
	return nil
}

// Main is the entry point for cmd/pmx/main.go.
// It accepts the ordered factory slice and maps the returned error to a
// semantic exit code.
func Main(persona string, factories []GroupFactory) int {
	if err := Execute(persona, factories); err != nil {
		return exitcode.FromError(err)
	}
	return exitcode.OK
}

// resolveMaxWidth returns the table layout budget for this invocation:
// --wide disables it outright, a positive output.max_width in the config
// pins it, and anything else leaves the renderer to detect one from $COLUMNS
// or the terminal.
func resolveMaxWidth(wide bool, cfg *config.Config) int {
	if wide {
		return output.WidthUnbounded
	}
	if cfg != nil && cfg.Output.MaxWidth > 0 {
		return cfg.Output.MaxWidth
	}
	return 0
}
