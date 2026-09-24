package context

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
)

// validateFlags holds the raw flag values for `pmx context validate`.
type validateFlags struct {
	all     bool
	connect bool
}

// validateLong is the long help of `pmx context validate`.
const validateLong = "Validate one or all named contexts against structural rules.\n\n" +
	"  * host is present\n" +
	"  * auth type is \"token\" or \"password\"\n" +
	"  * token auth has token-id and secret set\n" +
	"  * password auth has username and secret set\n" +
	"  * port is within [1, 65535]\n" +
	"  * protocol is \"https\" or \"http\"\n" +
	"  * default-output, if set, is table, ascii, plain, json, or yaml\n" +
	"  * fingerprint, if set, is 32 colon-separated hex pairs\n" +
	"  * proxy.url, if set, is a socks5, socks5h, or http URL with a host and no embedded credentials\n" +
	"  * proxy.username and proxy.password are only set alongside proxy.url\n" +
	"  * ssh.jump, if set, parses as [user@]host[:port] or ssh://[user@]host[:port] (comma-separated for a " +
	"chain), where a host is a name, an IPv4 address, or a bracketed IPv6 literal, and an @ inside the user " +
	"of an ssh:// hop is written %40\n" +
	"  * each timeout, if set, is a positive duration such as 5s or 500ms\n\n" +
	"Two of those accept an empty value as \"use the default\": port 0 means the " +
	"product's own port (8006 pve, 8007 pbs, 8443 pdm), and an empty protocol means " +
	"https.\n\n" +
	"With --connect, every structurally valid context is also probed live. The " +
	"version endpoint is fetched through the same ssh jump, proxy, and timeouts a real " +
	"API call from that context would use, including any --api-* override and " +
	"PMX_API_* variable, and it trusts the certificate the way that call does, through " +
	"a pinned fingerprint, a CA bundle, or tls.insecure; the server's identification " +
	"header is compared against the context's product; and the stored secret is " +
	"checked for resolvability. " +
	"The probe does not read the trust-on-first-use cache, so a context that trusts " +
	"its host only through tls.tofu fails the probe with a certificate error and reads " +
	"as unreachable; pin the certificate with tls.fingerprint to probe it. The VIA column names the route each probe took, and an " +
	"unreachable context names that route in its error, so a bastion or proxy " +
	"failure is not mistaken for a dead host. " +
	"The probe no longer honours $HTTPS_PROXY on its own; set proxy.from-env: true on the context, " +
	"or pass --api-proxy-from-env, to probe through the proxy environment. " +
	"The probe allows five seconds for each context unless a request timeout is set, " +
	"plus the connect and handshake bounds when it goes through a jump.\n\n" +
	"A context whose jump or proxy cannot be set up, such as a proxy password that " +
	"does not resolve, is marked invalid with a \"connection:\" error and is not probed.\n\n" +
	"The --api-* flags only mean something with --connect, and without it they are " +
	"refused. With --all, the jump, proxy, and timeout overrides apply to every context " +
	"in the sweep, and each probe starts its own ssh, so a bastion that rejects the key " +
	"costs one failed attempt per context. An endpoint, --api-fingerprint, or " +
	"--api-ca-cert names one host and cannot stand in for every context, so with --all " +
	"each is refused, from the flag or from the environment.\n\n" +
	"An unreachable context fails the command. A probe cut short by an interrupt " +
	"such as Ctrl-C reads \"interrupted\" rather than unreachable, and the sweep " +
	"stops there. A product mismatch is only a warning. Scripts that read the table by column position should use --output json.\n\n" +
	"The config file is also re-read strictly, and any key no setting matches is " +
	"listed on stderr. Such a key is ignored when the config loads, so a misspelling " +
	"costs you the setting silently. It does not affect the exit status: a config " +
	"shared with a newer pmx may legitimately carry keys this build does not know.\n\n" +
	"Exit status is 0 when every validated context is valid, and 1 when any is not."

// reachableInterrupted is the REACHABLE value of a context whose probe was
// cut short by a cancelled command, such as one the operator pressed Ctrl-C
// on, rather than failed by the network.
const reachableInterrupted = "interrupted"

// validateResult is one row of the validation table.
type validateResult struct {
	name      string
	status    string
	errors    []string
	reachable string
	via       string
	product   string
	auth      string

	// unreachable is set when a structurally valid context failed its
	// probe. Its status stays OK, and it still fails the command.
	unreachable bool
}

// newValidateCmd builds `pmx context validate [<name>] [--all]`.
// With no argument it validates the current context; with a name it validates
// that specific context; with --all it validates every context in the config.
// Renders a table of context → OK / error-list; exits non-zero if any invalid.
func newValidateCmd() *cobra.Command {
	var f validateFlags

	cmd := &cobra.Command{
		Use:         "validate [<name>]",
		Short:       "Validate one or all contexts",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		Long:        validateLong,
		Example: `  pmx context validate
  pmx context validate lab
  pmx context validate --all --connect
  pmx context validate lab --connect --api-jump admin@bastion.example.com
  pmx context validate --all --connect --api-proxy socks5h://127.0.0.1:1080`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, args, f)
		},
	}

	// Set after the map literal above, so the literal can never drop it. It
	// tells the root that this command consumes the --api-* overrides, under
	// --connect, instead of having the root refuse them.
	cmd.Annotations[cli.AnnotationUsesConnection] = "true"

	cmd.Flags().BoolVar(&f.all, "all", false, "validate all contexts in the config")
	cmd.Flags().BoolVar(&f.connect, "connect", false,
		"probe each structurally valid context live through its jump, proxy, timeouts, and pinned, CA, "+
			"or insecure TLS trust (not the tls.tofu cache): "+
			"reachability of the version endpoint, plus a product sanity check from the server's "+
			"identification header")

	cmd.ValidArgsFunction = cli.FirstArgContextNames

	return cmd
}

// runValidate is the body of `pmx context validate`.
func runValidate(cmd *cobra.Command, args []string, f validateFlags) error {
	deps := cli.GetDeps(cmd)
	cfg := deps.Cfg

	renderer := deps.Out
	if renderer == nil {
		renderer = output.New()
	}

	format := deps.Format
	if format == "" {
		format = output.FormatTable
	}

	var ov cli.ConnectionOverrides

	if !f.connect {
		if err := refuseConnectionFlagsWithoutConnect(cmd); err != nil {
			return err
		}
	} else {
		var err error

		ov, err = deps.ConnectionOverrides()
		if err != nil {
			return err
		}

		// A hand-built Deps carries the root's --insecure only in Insecure.
		ov.Insecure = ov.Insecure || deps.Insecure

		if f.all {
			if err := refuseHostOverridesInSweep(ov); err != nil {
				return err
			}
		}
	}

	names, err := validateNames(cfg, deps, args, f.all)
	if err != nil {
		return err
	}

	if len(names) == 0 {
		res := output.Result{Message: "No contexts configured."}
		return renderer.Render(cmd.OutOrStdout(), res, format)
	}

	stderr := cmd.ErrOrStderr()

	if f.connect && f.all {
		for _, note := range sweepNotes(ov) {
			_, _ = fmt.Fprintln(stderr, note)
		}
	}

	v := validator{cmd: cmd, cfg: cfg, ov: ov, connect: f.connect, sweep: f.all, stderr: stderr}

	results := make([]validateResult, 0, len(names))
	anyInvalid := false

	var interrupted error

	for _, n := range names {
		// After a Ctrl-C, stop and report only the contexts already probed,
		// rather than blaming every remaining one for being unreachable.
		if err := cmd.Context().Err(); err != nil {
			interrupted = err
			break
		}

		r := v.validate(n)
		if r.status != "OK" || r.unreachable {
			anyInvalid = true
		}

		results = append(results, r)
	}

	// A cancel that landed during the last probe still reports as an
	// interruption, not as that context being unreachable.
	if interrupted == nil && f.connect {
		interrupted = cmd.Context().Err()
	}

	if err := renderer.Render(cmd.OutOrStdout(), validateTable(results, f.connect), format); err != nil {
		return err
	}

	if interrupted != nil {
		return interrupted
	}

	reportUnknownConfigKeys(cmd, deps)

	if anyInvalid {
		return fmt.Errorf("one or more contexts are invalid or unreachable")
	}

	return nil
}

// validateNames returns the contexts to validate: every configured one,
// sorted, under --all, and otherwise the named one, or the one the
// invocation selected.
func validateNames(cfg *config.Config, deps *cli.Deps, args []string, all bool) ([]string, error) {
	if all {
		if cfg == nil {
			return nil, nil
		}

		names := make([]string, 0, len(cfg.Contexts))
		for n := range cfg.Contexts {
			names = append(names, n)
		}

		sort.Strings(names)

		return names, nil
	}

	name := ""
	if len(args) == 1 {
		name = args[0]
	}

	if name == "" {
		name = targetName(deps)
	}

	if name == "" {
		return nil, fmt.Errorf(
			"no context name specified and no current-context is set; " +
				"use 'pmx context validate <name>' or 'pmx context select <name>' first",
		)
	}

	return []string{name}, nil
}

// validator validates one context at a time with the settings the
// invocation shares across all of them.
type validator struct {
	cmd     *cobra.Command
	cfg     *config.Config
	ov      cli.ConnectionOverrides
	connect bool
	sweep   bool
	stderr  io.Writer

	// warnedInsecure records that the insecure-TLS warning has been printed,
	// so a sweep prints it once rather than once per insecure context.
	warnedInsecure bool
}

// validate checks the context called n structurally and, under --connect,
// probes it live.
func (v *validator) validate(n string) validateResult {
	r := validateResult{name: n}

	if v.cfg == nil || v.cfg.Contexts == nil {
		r.errors = append(r.errors, "config has no contexts")
		r.status = "INVALID"

		return r
	}

	ctx, ok := v.cfg.Contexts[n]
	if !ok || ctx == nil {
		r.errors = append(r.errors, "context not found in config")
		r.status = "INVALID"

		return r
	}

	r.errors = append(r.errors, config.StrictValidateContext(ctx)...)

	// The config package imports nothing that can parse a jump chain, so the
	// chain is checked here. "none" exactly as written and a blank value
	// both mean a direct dial, as they do when a connection is resolved. The
	// resolver compares "none" untrimmed, so a padded "none" would dial
	// through a bastion named none, and it is reported rather than probed.
	switch jump := ctx.SSH.Jump; {
	case jump == "none" || strings.TrimSpace(jump) == "":
	case strings.TrimSpace(jump) == "none":
		r.errors = append(r.errors, fmt.Sprintf(`ssh.jump %q has whitespace around "none", so it names a bastion `+
			`called none instead of dialling direct; write none with nothing around it`, jump))
	default:
		if err := apiclient.CheckJumpChain("ssh.jump", jump); err != nil {
			r.errors = append(r.errors, err.Error())
		}
	}

	if len(r.errors) > 0 {
		r.status = "INVALID"

		return r
	}

	r.status = "OK"

	if v.connect {
		v.probe(n, ctx, &r)
	}

	return r
}

// probe resolves the connection for the context called n and probes it,
// filling r's live columns. A connection that cannot be resolved or set up
// marks r invalid with a "connection:" error, and nothing is dialled.
func (v *validator) probe(n string, ctx *config.Context, r *validateResult) {
	conn, err := cli.ResolveConnection(n, ctx, v.ov)
	if err != nil {
		r.errors = append(r.errors, "connection: "+err.Error())
		r.status = "INVALID"

		return
	}

	// The warning follows the resolved trust, so a trust override that
	// replaced an insecure context's settings prints none.
	if conn.Insecure && !v.warnedInsecure {
		cli.WarnInsecureTLS(v.stderr)
		v.warnedInsecure = true
	}

	// A sweep has already printed one summary line per variable.
	if !v.sweep {
		for _, note := range conn.Notes {
			_, _ = fmt.Fprintln(v.stderr, note)
		}
	}

	pr, err := probeContext(v.cmd.Context(), conn)
	if err != nil {
		r.errors = append(r.errors, "connection: "+err.Error())
		r.status = "INVALID"

		return
	}

	r.via = pr.Via

	switch {
	case pr.Reachable:
		r.reachable = "yes"
	case v.cmd.Context().Err() != nil:
		// The operator cancelled the command while the probe waited, so
		// the failure says nothing about the context, and the sweep
		// reports the interruption itself.
		r.reachable = reachableInterrupted
	default:
		r.reachable = "no"
		r.errors = append(r.errors, unreachableText(conn, pr.Err))
		r.unreachable = true
	}

	// The product and the credential are read from a defaults-applied clone,
	// so the stored context never gains a default by being validated.
	resolved := config.CloneContext(ctx)
	config.ApplyDefaults(resolved)

	switch {
	case !pr.Reachable:
		r.product = ""
	case pr.ProductGuess == "":
		r.product = "unverified"
	case pr.ProductGuess == resolved.Product:
		r.product = fmt.Sprintf("match (%s)", resolved.Product)
	default:
		// Warn, never block: a mismatch is reported but does not flip the
		// exit code.
		r.product = fmt.Sprintf("mismatch (endpoint looks like %s)", pr.ProductGuess)
	}

	switch {
	case resolved.Auth.Secret == "":
		r.auth = "no credentials"
	case secretResolves(resolved.Auth.Secret):
		r.auth = "secret resolves (verify live with 'auth whoami')"
	default:
		r.auth = "secret does not resolve"
	}
}

// secretResolves reports whether a stored secret reference resolves.
func secretResolves(ref string) bool {
	_, err := config.ResolveSecret(ref)

	return err == nil
}

// validateTable renders results as the validation table: NAME, STATUS, and
// ERRORS, with REACHABLE, VIA, PRODUCT, and AUTH between them under
// --connect.
func validateTable(results []validateResult, connect bool) output.Result {
	type rawEntry struct {
		Name      string   `json:"name"`
		Status    string   `json:"status"`
		Reachable string   `json:"reachable,omitempty"`
		Via       string   `json:"via,omitempty"`
		Product   string   `json:"product_check,omitempty"`
		Auth      string   `json:"auth_check,omitempty"`
		Errors    []string `json:"errors"`
	}

	rawEntries := make([]rawEntry, 0, len(results))
	rows := make([][]string, 0, len(results))

	for _, r := range results {
		errStr := strings.Join(r.errors, "; ")

		if connect {
			rows = append(rows, []string{r.name, r.status, r.reachable, r.via, r.product, r.auth, errStr})
		} else {
			rows = append(rows, []string{r.name, r.status, errStr})
		}

		rawEntries = append(rawEntries, rawEntry{
			Name: r.name, Status: r.status,
			Reachable: r.reachable, Via: r.via, Product: r.product, Auth: r.auth,
			Errors: r.errors,
		})
	}

	headers := []string{"NAME", "STATUS", "ERRORS"}
	if connect {
		headers = []string{"NAME", "STATUS", "REACHABLE", "VIA", "PRODUCT", "AUTH", "ERRORS"}
	}

	return output.Result{Headers: headers, Rows: rows, Raw: rawEntries}
}

// connectionFlagNames returns the names of the root's --api-* flags, in the
// order the root registers them, by registering them on a scratch flag set,
// so this list can never drift from the root's.
func connectionFlagNames() []string {
	fs := pflag.NewFlagSet("api", pflag.ContinueOnError)
	fs.SortFlags = false
	cli.RegisterConnectionFlags(fs)

	var names []string

	fs.VisitAll(func(fl *pflag.Flag) { names = append(names, fl.Name) })

	return names
}

// refuseConnectionFlagsWithoutConnect refuses a changed --api-* flag when
// validate will not probe, because nothing would dial and the flag would do
// nothing. The PMX_API_* variables stay silent, as they do on every command
// that never resolves a connection.
func refuseConnectionFlagsWithoutConnect(cmd *cobra.Command) error {
	for _, name := range connectionFlagNames() {
		fl := cmd.Flags().Lookup(name)
		if fl == nil {
			fl = cmd.InheritedFlags().Lookup(name)
		}

		if fl != nil && fl.Changed {
			return fmt.Errorf("--%s has no effect without --connect", name)
		}
	}

	return nil
}

// refuseHostOverridesInSweep refuses an override that names one host, which
// is an endpoint, a fingerprint pin, or a CA bundle, under --all, because one
// host cannot stand in for every context in the sweep. A probe that dialled
// the same machine for every context would report each context reachable
// under its own name.
func refuseHostOverridesInSweep(ov cli.ConnectionOverrides) error {
	overrides := []struct {
		set          bool
		source, flag string
	}{
		{ov.Host != "" || ov.Port != 0 || ov.Protocol != "", ov.EndpointSource, "--api-endpoint"},
		{ov.CACert != "", ov.CACertSource, "--api-ca-cert"},
		{ov.Fingerprint != "", ov.FingerprintSource, "--api-fingerprint"},
	}

	for _, o := range overrides {
		if !o.set {
			continue
		}

		// A hand-built override with no source came from the flag.
		source := o.source
		if source == "" {
			source = o.flag
		}

		return fmt.Errorf("%s cannot be combined with --all; unset it or validate one context by name", source)
	}

	return nil
}

// sweepNotes returns one line for each PMX_API_* variable that applies to
// every context in a sweep, in place of the per-context notes, which would
// repeat once per context.
func sweepNotes(ov cli.ConnectionOverrides) []string {
	type envValue struct {
		source, shown string
	}

	values := []envValue{
		{ov.JumpSource, jumpNoteValue(ov.Jump)},
		{ov.ProxySource, proxyNoteValue(ov.Proxy)},
		{ov.ConnectSource, durationNoteValue(ov.ConnectRaw, ov.Connect.String(), ov.Connect > 0)},
		{ov.TLSHandshakeSource, durationNoteValue(ov.TLSHandshakeRaw, ov.TLSHandshake.String(), ov.TLSHandshake > 0)},
		{ov.RequestSource, durationNoteValue(ov.RequestRaw, ov.Request.String(), ov.Request > 0)},
	}

	var notes []string

	for _, v := range values {
		if !strings.HasPrefix(v.source, "$") || v.shown == "" {
			continue
		}

		notes = append(notes, fmt.Sprintf("note: %s (%s) applies to every context in this sweep", v.source, v.shown))
	}

	return notes
}

// jumpNoteValue renders a jump override for a note, with any misused
// password masked.
func jumpNoteValue(chain string) string {
	if chain == "" {
		return ""
	}

	return jumpChainText(chain)
}

// proxyNoteValue renders a proxy override for a note, with any password
// masked.
func proxyNoteValue(raw string) string {
	if raw == "" {
		return ""
	}

	return redact.ProxyURL(raw)
}

// durationNoteValue renders a timeout override for a note: the text the
// operator exported when it is known, and the parsed bound otherwise.
func durationNoteValue(raw, parsed string, set bool) string {
	switch {
	case !set:
		return ""
	case raw != "":
		return raw
	default:
		return parsed
	}
}

// reportUnknownConfigKeys names every key in the config file that no field
// accepts, on stderr, after the validation table.
//
// config.Load is permissive by design — a config carrying a key this binary
// does not know must still load — so a typo costs the operator the setting
// silently: `fingerprnt` under a context means TLS pinning is simply not
// happening. `validate` is the verb that exists to answer "what is wrong with
// my config", so the strict pass belongs here.
//
// It does not affect the exit code. An unknown key is not necessarily a
// mistake: a config shared with a newer pmx, or written by one, legitimately
// carries keys this build has never heard of, and failing on those would make
// the verb unusable exactly when an operator most needs it. The same reasoning
// makes an unreadable or unparseable file silent here — validate has already
// reported what it could, and the loader reports the rest.
func reportUnknownConfigKeys(cmd *cobra.Command, deps *cli.Deps) {
	if deps.ConfigPath == "" {
		return
	}

	keys, err := config.UnknownKeys(deps.ConfigPath)
	if err != nil || len(keys) == 0 {
		return
	}

	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(w, "WARN: %s contains %d key(s) no setting matches, which are ignored:\n",
		deps.ConfigPath, len(keys))
	for _, k := range keys {
		_, _ = fmt.Fprintf(w, "  %s\n", k)
	}
}
