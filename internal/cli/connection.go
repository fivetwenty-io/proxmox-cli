package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
)

// The nine per-invocation connection flags, without their leading dashes.
const (
	flagAPIEndpoint            = "api-endpoint"
	flagAPIJump                = "api-jump"
	flagAPIProxy               = "api-proxy"
	flagAPIProxyFromEnv        = "api-proxy-from-env"
	flagAPICACert              = "api-ca-cert"
	flagAPIFingerprint         = "api-fingerprint"
	flagAPIConnectTimeout      = "api-connect-timeout"
	flagAPITLSHandshakeTimeout = "api-tls-handshake-timeout"
	flagAPIRequestTimeout      = "api-request-timeout"
)

// The eight environment mirrors of the connection flags. --api-proxy-from-env
// has none, because an ambient toggle for an ambient proxy would be one more
// thing a shell could leave set by accident.
const (
	envAPIEndpoint            = "PMX_API_ENDPOINT"
	envAPIJump                = "PMX_API_JUMP"
	envAPIProxy               = "PMX_API_PROXY"
	envAPICACert              = "PMX_API_CA_CERT"
	envAPIFingerprint         = "PMX_API_FINGERPRINT"
	envAPIConnectTimeout      = "PMX_API_CONNECT_TIMEOUT"
	envAPITLSHandshakeTimeout = "PMX_API_TLS_HANDSHAKE_TIMEOUT"
	envAPIRequestTimeout      = "PMX_API_REQUEST_TIMEOUT"
)

const (
	// connectionNone is the literal that disables a configured jump or proxy
	// when --api-jump, --api-proxy, or their environment mirrors carry it.
	connectionNone = "none"

	// sourceTLSFingerprint is the FingerprintSource of a pin the context
	// itself stores, the only pin WrapPinMismatch rewrites a failure for.
	sourceTLSFingerprint = "tls.fingerprint"

	// sourceSSHJump names the context's own ssh.jump in a chain error.
	sourceSSHJump = "ssh.jump"

	// sourceProxyURL names the context's own proxy.url in a proxy error.
	sourceProxyURL = "proxy.url"

	// kitCannotVerifyFingerprint is the text of the kit's
	// ErrCannotVerifyFingerprint, which pmx cannot import because it lives in
	// the kit's internal/ssl package. A kit client reports a certificate that
	// fails its pin with this text.
	kitCannotVerifyFingerprint = "cannot verify certificate fingerprint"

	// kitUnknownFingerprint is the text of the kit's
	// ErrUnknownCertificateFingerprint, which a kit client reports for a
	// certificate that a trust-on-first-use cache without a callback does not
	// hold, and for a pinned certificate that fails its pin while such a
	// cache is configured.
	kitUnknownFingerprint = "unknown certificate fingerprint (manual verification required)"
)

// pinFingerprintRE is the rule config.StrictValidateContext applies to a
// stored tls.fingerprint: a colon-separated hex SHA-256, 32 pairs, in either
// case. A per-invocation pin is held to the same rule, so a malformed value
// fails before any client is built rather than as an opaque handshake error.
var pinFingerprintRE = regexp.MustCompile(`^(?i)[0-9a-f]{2}(?::[0-9a-f]{2}){31}$`)

// ErrPinMismatch is the sentinel pmx's own fingerprint verifier wraps, so a
// pin failure that the probe raises can be recognised with errors.Is.
var ErrPinMismatch = errors.New("certificate does not match the pinned fingerprint")

// ConnectionOverrides carries per-invocation overrides of a context's API
// connection, collected from the root's persistent flags and the eight
// PMX_API_* environment variables. A zero value overrides nothing, and a
// variable that is set but empty counts as unset.
//
// OverridesFromCommand fills every source field. A value built by hand whose
// source field is empty is treated as if it came from the matching flag, so
// it is honoured and never announced with a note, because only an ambient
// environment variable earns one.
type ConnectionOverrides struct {
	// Host, Port, and Protocol are the parsed components of whichever of
	// --api-endpoint and $PMX_API_ENDPOINT won. The endpoint resolves as a
	// unit, so the two sources never mix, and a zero field means the winning
	// value omitted that component. Host holds an IPv6 literal in brackets.
	Host     string
	Port     int
	Protocol string

	// Jump overrides ssh.jump, and it is either a chain or the literal
	// "none", which forces a direct dial.
	Jump string

	// Proxy is a proxy URL or the literal "none". A URL from --api-proxy
	// never carries userinfo, because OverridesFromCommand rejects it, while
	// a URL from $PMX_API_PROXY may.
	Proxy string

	// ProxyFromEnv and ProxyFromEnvSet carry the tri-state of
	// --api-proxy-from-env, which has no environment mirror. ProxyFromEnvSet
	// is true when cobra reports the flag as changed, so an explicit false
	// overrides a context that enables the setting.
	ProxyFromEnv    bool
	ProxyFromEnvSet bool

	CACert      string
	Fingerprint string

	Connect      time.Duration
	TLSHandshake time.Duration
	Request      time.Duration

	// ConnectRaw, TLSHandshakeRaw, and RequestRaw hold each timeout exactly
	// as the flag or the variable carried it, such as "1m", so a note echoes
	// what the operator typed or exported rather than the parsed "1m0s". A
	// hand-built override may leave them empty, and a note then prints the
	// parsed duration.
	ConnectRaw, TLSHandshakeRaw, RequestRaw string

	// Each source field names where the matching value came from, such as
	// "--api-endpoint" or "$PMX_API_ENDPOINT", and it is empty when the value
	// is unset. Every error message and note that names a source reads it
	// from here.
	EndpointSource, JumpSource, ProxySource          string
	FingerprintSource, CACertSource                  string
	ConnectSource, TLSHandshakeSource, RequestSource string

	// Insecure is the root's persistent --insecure, read through
	// cmd.Root().PersistentFlags() and never from a local --insecure such as
	// the one context add and context update register to store
	// tls.insecure. It is OR'd with tls.insecure.
	Insecure bool
}

// Connection is one context's fully resolved outbound transport, with every
// precedence question already answered and no secret resolved.
type Connection struct {
	// ContextName is the name the connection was resolved for, used in error
	// text and in notes.
	ContextName string

	Host        string // an IPv6 literal is bracketed
	Port        int
	Protocol    string
	Insecure    bool
	Fingerprint string
	CACert      string

	// TOFU is the context's tls.tofu, cleared when a per-invocation trust
	// override replaces the context's trust mode and cleared under an
	// endpoint override too, so another node's certificate can never be
	// trusted into this context's cache.
	TOFU bool

	// TOFUReadOnly is true when an endpoint override cleared TOFU on a
	// context that enables it, no trust override applies, and the connection
	// is not insecure, because insecure outranks trust on first use.
	// ContextOptions then still points the kit at the context's fingerprint
	// cache, with no manual-verify callback, so a certificate the context
	// already trusts for that host name verifies and no new one can ever be
	// written.
	TOFUReadOnly bool

	Jump     apiclient.JumpSpec
	Proxy    apiclient.ProxySpec
	Timeouts apiclient.TimeoutSpec

	// TimeoutsSet records which of the three bounds an operator set
	// explicitly, through a flag, an environment variable, or the context's
	// timeout block. A false field means the matching value in Timeouts is
	// the built-in default, which is how the probe decides whether to keep
	// its own five-second bound.
	TimeoutsSet TimeoutsSet

	// EndpointSource, JumpSource, and ProxySource copy the matching source
	// fields of the overrides that won, and they are empty when the context
	// or the default supplied the value. auth status marks its Host and Via
	// rows with them, and WrapPinMismatch names the endpoint's source.
	// ProxySource reads "--api-proxy-from-env" when that flag decided the
	// proxy.
	EndpointSource, JumpSource, ProxySource string

	// FingerprintSource names where Fingerprint came from, which is
	// "tls.fingerprint" for the context's own pin, "--api-fingerprint" or
	// "$PMX_API_FINGERPRINT" for a per-invocation one, and empty when no
	// pin is set. WrapPinMismatch rewrites a failure only for the context's
	// own pin.
	FingerprintSource string

	// Notes carries one line for every PMX_API_* variable that is set and
	// wins over the context. ContextOptions prints them to standard error
	// once per invocation, and auth status and context validate print them
	// too.
	Notes []string
}

// TimeoutsSet says which bounds of a Connection were chosen rather than
// defaulted.
type TimeoutsSet struct{ Connect, TLSHandshake, Request bool }

// ResolveConnection answers every connection-parameter precedence question
// for the context called name exactly once, in flag, environment, context,
// and product-default order, per parameter. It is pure, which means it
// performs no secret I/O, reads no keychain, and touches no network, so the
// audit record and auth status can call it on every invocation. It accepts
// ctx with or without defaults applied. It clones ctx with
// config.CloneContext and applies defaults to the clone, which is
// idempotent, so it never mutates ctx and a resolve can never write a port,
// a protocol, a realm, or a product back into cfg.Contexts. name appears
// only in error text, in notes, and in Connection.ContextName.
//
// It runs config.ValidateProxyBlock on the stored proxy block, and when that
// reports any message it fails with the messages joined by "; ", so a
// stored proxy.url that does not parse, or that carries userinfo, fails with
// exactly the texts that checker prints, already passed through
// redact.ProxyURL, and no url.Parse error is ever wrapped. A malformed
// stored timeout fails with config.ParseTimeout's text for its key.
//
// The endpoint resolves as a unit: the override's components win, and the
// ones it omits fall through to the context and then the product default.
// An override may never downgrade an https context to http. A trust
// override, meaning a fingerprint or a CA bundle from the flag or the
// environment, replaces the context's whole trust mode and may not be
// combined with the other one or with --insecure. An endpoint override
// keeps the context's trust settings, clears trust on first use, and marks
// the cache read-only when the context enabled it and the connection is not
// insecure.
func ResolveConnection(name string, ctx *config.Context, ov ConnectionOverrides) (Connection, error) {
	if ctx == nil {
		return Connection{}, fmt.Errorf("context %q is not defined", name)
	}

	stored := config.CloneContext(ctx)
	config.ApplyDefaults(stored)

	if msgs := config.ValidateProxyBlock(&stored.Proxy); len(msgs) > 0 {
		return Connection{}, fmt.Errorf("context %q: %s", name, strings.Join(msgs, "; "))
	}

	storedTimeouts, err := stored.ParsedTimeouts()
	if err != nil {
		return Connection{}, fmt.Errorf("context %q: %w", name, err)
	}

	conn := Connection{ContextName: name}

	if err := resolveEndpoint(&conn, name, stored, ov); err != nil {
		return Connection{}, err
	}

	if err := resolveTrust(&conn, stored, ov); err != nil {
		return Connection{}, err
	}

	if err := resolveTimeouts(&conn, storedTimeouts, ov); err != nil {
		return Connection{}, err
	}

	if err := resolveJump(&conn, stored, ov); err != nil {
		if ov.Jump == "" {
			// The chain came from the context's own ssh.jump, not from an
			// override that already names its flag or variable.
			return Connection{}, fmt.Errorf("context %q: %w", name, err)
		}

		return Connection{}, err
	}

	if err := resolveProxy(&conn, stored, ov); err != nil {
		return Connection{}, err
	}

	if conn.Jump.Chain != "" && firstByteTimerArmed(conn.Protocol, conn.Proxy) {
		conn.Jump.FirstByteTimeout = firstByteTimeout(conn.Timeouts)
	}

	conn.Notes = connectionNotes(name, stored, ov)

	return conn, nil
}

// resolveEndpoint fills conn's host, port, and protocol. The override wins
// as a unit, component by component, over the defaults-applied context.
//
// Both protocols are compared in lower case. Only the strict validator
// restricts a stored protocol to lower case, and the kit lowercases the
// scheme when it builds its URL, so the kit treats a hand-edited "HTTPS" as
// https, and the resolver must protect it from a downgrade, arm the
// first-byte timer, and select $HTTPS_PROXY exactly as "https" does.
func resolveEndpoint(conn *Connection, name string, stored *config.Context, ov ConnectionOverrides) error {
	storedProtocol := strings.ToLower(stored.Protocol)
	ovProtocol := strings.ToLower(ov.Protocol)

	conn.Host, conn.Port, conn.Protocol = stored.Host, stored.Port, storedProtocol

	if !endpointOverridden(ov) {
		if conn.Host == "" {
			return fmt.Errorf("context %q has no host; set host on the context or pass --%s",
				name, flagAPIEndpoint)
		}

		return nil
	}

	source := sourceOr(ov.EndpointSource, "--"+flagAPIEndpoint)

	if ovProtocol != "" && ovProtocol != "https" && ovProtocol != "http" {
		return fmt.Errorf("%s scheme %q must be https or http", source, ov.Protocol)
	}

	if ov.Port < 0 || ov.Port > 65535 {
		return fmt.Errorf("%s port %d is out of range [1, 65535]", source, ov.Port)
	}

	if storedProtocol == "https" && ovProtocol == "http" {
		return fmt.Errorf("%s would downgrade context %q from https to http; set protocol: http on the context to allow it",
			source, name)
	}

	if ov.Host != "" {
		conn.Host = ov.Host
	}

	if ov.Port != 0 {
		conn.Port = ov.Port
	}

	if ovProtocol != "" {
		conn.Protocol = ovProtocol
	}

	if conn.Host == "" {
		return fmt.Errorf("context %q has no host; set host on the context or pass --%s", name, flagAPIEndpoint)
	}

	conn.EndpointSource = source

	return nil
}

// resolveTrust applies the three trust rules: a trust override replaces the
// context's whole trust mode, the two trust overrides exclude each other and
// --insecure, and an endpoint override keeps the context's trust but clears
// trust on first use, leaving its cache read-only unless the connection is
// insecure.
func resolveTrust(conn *Connection, stored *config.Context, ov ConnectionOverrides) error {
	fpSource := sourceOr(ov.FingerprintSource, "--"+flagAPIFingerprint)
	caSource := sourceOr(ov.CACertSource, "--"+flagAPICACert)

	switch {
	case ov.Fingerprint != "" && ov.CACert != "":
		return fmt.Errorf("%s and %s conflict; pass one trust override, not both", fpSource, caSource)
	case ov.Fingerprint != "" && ov.Insecure:
		return fmt.Errorf("%s cannot be combined with --insecure", fpSource)
	case ov.CACert != "" && ov.Insecure:
		return fmt.Errorf("%s cannot be combined with --insecure", caSource)
	}

	if ov.Fingerprint != "" {
		if err := checkFingerprint(fpSource, ov.Fingerprint); err != nil {
			return err
		}

		conn.Fingerprint = ov.Fingerprint
		conn.FingerprintSource = fpSource

		return nil
	}

	if ov.CACert != "" {
		conn.CACert = ov.CACert

		return nil
	}

	conn.Insecure = stored.TLS.Insecure || ov.Insecure
	conn.Fingerprint = stored.TLS.Fingerprint
	conn.CACert = stored.TLS.CACert
	conn.TOFU = stored.TLS.Tofu

	if conn.Fingerprint != "" {
		conn.FingerprintSource = sourceTLSFingerprint
	}

	// Insecure outranks trust on first use, as it does on a connection with
	// no override: a read-only cache would make the kit verify against it and
	// silently cancel the operator's insecure setting.
	if endpointOverridden(ov) && conn.TOFU {
		conn.TOFU = false
		conn.TOFUReadOnly = !conn.Insecure
	}

	return nil
}

// resolveTimeouts walks the four rungs of each bound: an override, the
// context's timeout block, and the built-in default.
func resolveTimeouts(conn *Connection, stored config.ParsedTimeouts, ov ConnectionOverrides) error {
	defaults := apiclient.DefaultTimeoutSpec()

	var err error

	conn.Timeouts.Connect, conn.TimeoutsSet.Connect, err = resolveTimeout(
		ov.Connect, sourceOr(ov.ConnectSource, "--"+flagAPIConnectTimeout), stored.Connect, defaults.Connect)
	if err != nil {
		return err
	}

	conn.Timeouts.TLSHandshake, conn.TimeoutsSet.TLSHandshake, err = resolveTimeout(
		ov.TLSHandshake, sourceOr(ov.TLSHandshakeSource, "--"+flagAPITLSHandshakeTimeout),
		stored.TLSHandshake, defaults.TLSHandshake)
	if err != nil {
		return err
	}

	conn.Timeouts.Request, conn.TimeoutsSet.Request, err = resolveTimeout(
		ov.Request, sourceOr(ov.RequestSource, "--"+flagAPIRequestTimeout), stored.Request, defaults.Request)
	if err != nil {
		return err
	}

	return nil
}

// resolveTimeout returns the override when it is set, then the stored value,
// then the default, and reports whether anything but the default answered.
// An override that is negative can only come from a hand-built value, since
// OverridesFromCommand rejects one, and it fails with the same text.
func resolveTimeout(override time.Duration, source string, stored, def time.Duration) (time.Duration, bool, error) {
	switch {
	case override < 0:
		return 0, false, fmt.Errorf("%s must be greater than zero", source)
	case override > 0:
		return override, true, nil
	case stored > 0:
		return stored, true, nil
	default:
		return def, false, nil
	}
}

// resolveJump walks the jump ladder. A non-empty override beats ssh.jump,
// the literal "none" from any source means a direct dial, and any other
// value must pass apiclient.ValidateJumpChain. Only a blank ssh.jump means
// no bastion: an override that holds nothing but whitespace is not the
// "none" sentinel, so it is validated and fails rather than silently
// dropping the context's bastion. A rejected chain is quoted through
// apiclient.RedactJumpChain.
func resolveJump(conn *Connection, stored *config.Context, ov ConnectionOverrides) error {
	chain, source := stored.SSH.Jump, sourceSSHJump
	if ov.Jump != "" {
		chain, source = ov.Jump, sourceOr(ov.JumpSource, "--"+flagAPIJump)
		conn.JumpSource = source
	}

	if chain == connectionNone || (ov.Jump == "" && strings.TrimSpace(chain) == "") {
		return nil
	}

	if err := apiclient.CheckJumpChain(source, chain); err != nil {
		return err
	}

	conn.Jump = apiclient.JumpSpec{
		Chain:          chain,
		ConnectTimeout: conn.Timeouts.Connect,
	}

	return nil
}

// resolveProxy walks the seven-step proxy ladder and stops at the first step
// that matches.
func resolveProxy(conn *Connection, stored *config.Context, ov ConnectionOverrides) error {
	source := sourceOr(ov.ProxySource, "--"+flagAPIProxy)
	fromEnvFlag := ov.ProxyFromEnvSet && ov.ProxyFromEnv

	switch {
	case ov.Proxy == connectionNone:
		conn.ProxySource = source

		return nil

	case ov.Proxy != "" && fromEnvFlag:
		return fmt.Errorf("%s and --%s conflict; pass a proxy URL or the environment toggle, not both",
			source, flagAPIProxyFromEnv)

	case ov.Proxy != "":
		u, err := parseOverrideProxy(source, ov.Proxy)
		if err != nil {
			return err
		}

		conn.Proxy = apiclient.ProxySpec{URL: u}
		conn.ProxySource = source

	case fromEnvFlag:
		conn.Proxy = apiclient.ProxySpec{FromEnv: true}
		conn.ProxySource = "--" + flagAPIProxyFromEnv

	case stored.Proxy.URL != "":
		// ValidateProxyBlock has already accepted this URL, so it parses,
		// carries no userinfo, and has a port in range.
		u, err := url.Parse(stored.Proxy.URL)
		if err != nil {
			return fmt.Errorf("%s %s is not a valid URL", sourceProxyURL, redact.ProxyURL(stored.Proxy.URL))
		}

		conn.Proxy = apiclient.ProxySpec{
			URL:         u,
			Username:    stored.Proxy.Username,
			PasswordRef: stored.Proxy.Password,
		}

	case ov.ProxyFromEnvSet:
		// An explicit --api-proxy-from-env=false decides the toggle, so the
		// context's proxy.from-env no longer counts. The flag is the route's
		// source only when it turned off a proxy the context enabled.
		if stored.Proxy.FromEnv != nil && *stored.Proxy.FromEnv {
			conn.ProxySource = "--" + flagAPIProxyFromEnv
		}

	case stored.Proxy.FromEnv != nil && *stored.Proxy.FromEnv:
		conn.Proxy = apiclient.ProxySpec{FromEnv: true}
	}

	// ProxyFunc runs the scheme, host, and port checks the transport would
	// otherwise run on first use, so a bad proxy fails before anything is
	// built. The checks above have already run each of them with a message
	// that names its source, so this only guards against a gap between the
	// two, and its message is already redacted.
	if _, err := apiclient.ProxyFunc(conn.Proxy); err != nil {
		return fmt.Errorf("%s: %w", sourceOr(conn.ProxySource, sourceProxyURL), err)
	}

	return nil
}

// checkProxyPort reports an override proxy URL whose explicit port is
// outside 1 to 65535, by config.ProxyPortMessage, the rule the stored
// proxy.url is held to. shown is the URL already passed through
// redact.ProxyURL.
func checkProxyPort(source, shown string, u *url.URL) error {
	if msg := config.ProxyPortMessage(source, shown, u); msg != "" {
		return errors.New(msg)
	}

	return nil
}

// firstByteTimerArmed reports whether a jump route should arm the first-byte
// timer: every https route, where the TLS handshake comes first, and an http
// route through a SOCKS proxy, where the SOCKS negotiation comes first. On
// any other http route the first byte is the server's response, which may
// legitimately come late.
func firstByteTimerArmed(protocol string, p apiclient.ProxySpec) bool {
	if protocol == "https" {
		return true
	}

	return p.URL != nil && (p.URL.Scheme == "socks5" || p.URL.Scheme == "socks5h")
}

// firstByteTimeout is the connect bound plus the handshake bound, raised to
// one second, and then capped at the request bound minus the lesser of one
// second and a quarter of that bound, with no floor on the capped value, so
// the timer always fires before the request timeout.
func firstByteTimeout(t apiclient.TimeoutSpec) time.Duration {
	sum := max(saturatingAdd(t.Connect, t.TLSHandshake), time.Second)
	limit := t.Request - min(time.Second, t.Request/4)

	return min(sum, limit)
}

// saturatingAdd adds two non-negative durations and caps the result at the
// largest duration instead of wrapping negative.
func saturatingAdd(a, b time.Duration) time.Duration {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}

	return a + b
}

// connectionNotes returns one note for every PMX_API_* variable that won
// over the context. A value from a flag earns none, because a flag on the
// command line is never ambient.
func connectionNotes(name string, stored *config.Context, ov ConnectionOverrides) []string {
	var notes []string

	if isEnvSource(ov.EndpointSource) && endpointOverridden(ov) {
		notes = append(notes, fmt.Sprintf("note: $%s (%s) overrides the endpoint of context %q",
			envAPIEndpoint, formatEndpointOverride(ov), name))
	}

	if isEnvSource(ov.ProxySource) && ov.Proxy != "" {
		if ov.Proxy == connectionNone {
			// The variable outranks --api-proxy-from-env, so it must not cancel
			// an explicit toggle on the command line in silence either.
			storedProxy := stored.Proxy.URL != "" || (stored.Proxy.FromEnv != nil && *stored.Proxy.FromEnv)
			if storedProxy || (ov.ProxyFromEnvSet && ov.ProxyFromEnv) {
				notes = append(notes, fmt.Sprintf("note: $%s=none disables the proxy of context %q", envAPIProxy, name))
			}
		} else {
			notes = append(notes, fmt.Sprintf("note: $%s (%s) overrides the proxy of context %q",
				envAPIProxy, redact.ProxyURL(ov.Proxy), name))
		}
	}

	if isEnvSource(ov.JumpSource) && ov.Jump != "" {
		if ov.Jump == connectionNone {
			if strings.TrimSpace(stored.SSH.Jump) != "" && stored.SSH.Jump != connectionNone {
				notes = append(notes, fmt.Sprintf("note: $%s=none disables the bastion of context %q", envAPIJump, name))
			}
		} else {
			notes = append(notes, fmt.Sprintf("note: $%s (%s) overrides the bastion of context %q",
				envAPIJump, ov.Jump, name))
		}
	}

	if isEnvSource(ov.CACertSource) && ov.CACert != "" {
		notes = append(notes, fmt.Sprintf("note: $%s (%s) replaces the trust settings of context %q (%s)",
			envAPICACert, ov.CACert, name, trustModeName(stored.TLS)))
	}

	if isEnvSource(ov.FingerprintSource) && ov.Fingerprint != "" {
		notes = append(notes, fmt.Sprintf("note: $%s replaces the trust settings of context %q (%s)",
			envAPIFingerprint, name, trustModeName(stored.TLS)))
	}

	timeouts := []struct {
		value       time.Duration
		raw, source string
		word, label string
	}{
		{ov.Connect, ov.ConnectRaw, ov.ConnectSource, "CONNECT", "connect"},
		{ov.TLSHandshake, ov.TLSHandshakeRaw, ov.TLSHandshakeSource, "TLS_HANDSHAKE", "tls-handshake"},
		{ov.Request, ov.RequestRaw, ov.RequestSource, "REQUEST", "request"},
	}

	for _, t := range timeouts {
		if isEnvSource(t.source) && t.value > 0 {
			notes = append(notes, fmt.Sprintf("note: $PMX_API_%s_TIMEOUT (%s) overrides the %s timeout of context %q",
				t.word, sourceOr(t.raw, t.value.String()), t.label, name))
		}
	}

	return notes
}

// trustModeName names the trust mode a context's TLS block selects, in the
// order the kit honours them: a pin is enforced even under insecure, insecure
// then disables chain verification, a CA bundle replaces the system roots,
// and trust on first use pins what the operator accepts.
func trustModeName(t config.TLSBlock) string {
	switch {
	case t.Fingerprint != "":
		return "fingerprint pin"
	case t.Insecure:
		return "insecure"
	case t.CACert != "":
		return "CA bundle"
	case t.Tofu:
		return "trust on first use"
	default:
		return "system roots"
	}
}

// endpointOverridden reports whether ov carries any endpoint component.
func endpointOverridden(ov ConnectionOverrides) bool {
	return ov.Host != "" || ov.Port != 0 || ov.Protocol != ""
}

// formatEndpointOverride renders the components an endpoint override
// carries as [scheme://]host[:port].
func formatEndpointOverride(ov ConnectionOverrides) string {
	var b strings.Builder

	if ov.Protocol != "" {
		b.WriteString(ov.Protocol + "://")
	}

	b.WriteString(ov.Host)

	if ov.Port != 0 {
		b.WriteString(":" + strconv.Itoa(ov.Port))
	}

	return b.String()
}

// sourceOr returns source, or fallback when source is empty.
func sourceOr(source, fallback string) string {
	if source == "" {
		return fallback
	}

	return source
}

// isEnvSource reports whether source names an environment variable.
func isEnvSource(source string) bool {
	return strings.HasPrefix(source, "$")
}

// checkFingerprint holds a per-invocation pin to the rule a stored
// tls.fingerprint obeys.
func checkFingerprint(source, fingerprint string) error {
	if !pinFingerprintRE.MatchString(fingerprint) {
		return fmt.Errorf("%s %q must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)",
			source, fingerprint)
	}

	return nil
}

// parseOverrideProxy parses a proxy URL from --api-proxy or $PMX_API_PROXY
// under the rules a stored proxy.url obeys, with the source in place of the
// key, plus the port range the transport enforces, so a bad port fails
// with its source named as early as OverridesFromCommand. The flag may
// carry no userinfo at all, because the process list would show it, and
// any "@" in it counts, since a stray one may open a password url.Parse did
// not recognise. The environment may carry userinfo. Every URL in a message
// passes through redact.ProxyURL, and no url.Parse error is ever appended,
// because it quotes its whole input.
func parseOverrideProxy(source, raw string) (*url.URL, error) {
	if source == "--"+flagAPIProxy && strings.Contains(raw, "@") {
		return nil, fmt.Errorf("--%s must not carry credentials, which would show in the process list; "+
			"use $%s or the context's proxy.username and proxy.password", flagAPIProxy, envAPIProxy)
	}

	shown := redact.ProxyURL(raw)

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s %s is not a valid URL", source, shown)
	}

	var msgs []string

	if u.Scheme != "socks5" && u.Scheme != "socks5h" && u.Scheme != "http" {
		msgs = append(msgs, fmt.Sprintf("%s %s must use scheme socks5, socks5h, or http", source, shown))
	}

	// Hostname, not Host: "socks5://:1080" has a Host of ":1080" and no
	// hostname, and dialling it would silently reach localhost.
	if u.Hostname() == "" {
		msgs = append(msgs, fmt.Sprintf("%s %s must include a host", source, shown))
	}

	if err := checkProxyPort(source, shown, u); err != nil {
		msgs = append(msgs, err.Error())
	}

	if len(msgs) > 0 {
		return nil, errors.New(strings.Join(msgs, "; "))
	}

	return u, nil
}

// ProxyCredentials is the one place the proxy password is resolved. It
// returns nil when the proxy carries no Username, url.User(Username) when no
// PasswordRef is stored, and url.UserPassword(Username, secret) after
// resolving PasswordRef with config.ResolveSecret otherwise. A URL that came
// from $PMX_API_PROXY keeps its own userinfo, and this returns nil for it. A
// failed resolve returns `resolve proxy.password for context %q: %w`.
func (c Connection) ProxyCredentials() (*url.Userinfo, error) {
	if c.Proxy.URL == nil || c.Proxy.Username == "" {
		return nil, nil
	}

	if c.Proxy.PasswordRef == "" {
		return url.User(c.Proxy.Username), nil
	}

	secret, err := config.ResolveSecret(c.Proxy.PasswordRef)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy.password for context %q: %w", c.ContextName, err)
	}

	return url.UserPassword(c.Proxy.Username, secret), nil
}

// transportProxy returns c's proxy with the context's credentials joined
// onto a copy of its URL, ready for apiclient.ProxyFunc. The copy keeps the
// stored ProxySpec free of any resolved secret.
func (c Connection) transportProxy() (apiclient.ProxySpec, error) {
	creds, err := c.ProxyCredentials()
	if err != nil {
		return apiclient.ProxySpec{}, err
	}

	spec := apiclient.ProxySpec{URL: c.Proxy.URL, FromEnv: c.Proxy.FromEnv}

	if creds != nil {
		u := *c.Proxy.URL
		u.User = creds
		spec.URL = &u
	}

	return spec, nil
}

// resolvedTimeouts returns c.Timeouts with any unset bound filled from the
// built-in defaults, so a zero Connection never reaches the appliers with a
// zero bound, which they would floor to one second.
func (c Connection) resolvedTimeouts() apiclient.TimeoutSpec {
	t := c.Timeouts
	defaults := apiclient.DefaultTimeoutSpec()

	if t.Connect <= 0 {
		t.Connect = defaults.Connect
	}

	if t.TLSHandshake <= 0 {
		t.TLSHandshake = defaults.TLSHandshake
	}

	if t.Request <= 0 {
		t.Request = defaults.Request
	}

	return t
}

// jumpSpec returns c.Jump with its connect bound filled from the resolved
// timeouts when a hand-built Connection left it unset.
func (c Connection) jumpSpec() apiclient.JumpSpec {
	j := c.Jump
	if j.ConnectTimeout <= 0 {
		j.ConnectTimeout = c.resolvedTimeouts().Connect
	}

	return j
}

// hasJump reports whether c routes through an ssh bastion.
func (c Connection) hasJump() bool {
	return strings.TrimSpace(c.Jump.Chain) != ""
}

// jumpHops renders the jump chain for Via and route. A resolved chain has
// passed the validator and prints as written, and apiclient.RedactJumpChain
// masks a hand-built Connection whose chain never did.
func (c Connection) jumpHops() string {
	return strings.TrimSpace(apiclient.RedactJumpChain(c.Jump.Chain))
}

// effectiveTLSHandshake is the transport's handshake bound. Through a jump
// the dial returns as soon as ssh starts, so the bastion's connect, key
// exchange, and authentication all run inside the handshake, and the bound
// is the connect bound plus the handshake bound plus one second, which also
// keeps it past the jump's own first-byte timer.
func (c Connection) effectiveTLSHandshake() time.Duration {
	t := c.resolvedTimeouts()
	if !c.hasJump() {
		return t.TLSHandshake
	}

	return saturatingAdd(saturatingAdd(t.Connect, t.TLSHandshake), time.Second)
}

// ApplyToOptions wires c's transport onto opts in proxy, timeout, and jump
// order. It calls ProxyCredentials before it installs the proxy and returns
// its error unchanged, with zero options, so a caller that ignored the error
// could not build a client that silently skips the proxy. The jump stays
// last for the reason ApplyJumpSpec documents, and when a jump is set the
// TLS-handshake bound it writes is the connect bound plus the handshake
// bound plus one second, summed first and then rounded up. The endpoint and
// TLS-trust fields of c are not applied here; ContextOptions passes them to
// apiclient.BuildOptions.
func (c Connection) ApplyToOptions(opts pve.Options) (pve.Options, error) {
	spec, err := c.transportProxy()
	if err != nil {
		return pve.Options{}, err
	}

	opts, err = apiclient.ApplyProxyOptions(opts, spec)
	if err != nil {
		return pve.Options{}, err
	}

	t := c.resolvedTimeouts()
	t.TLSHandshake = c.effectiveTLSHandshake()
	opts = apiclient.ApplyTimeoutOptions(opts, t)

	if c.hasJump() {
		opts = apiclient.ApplyJumpSpec(opts, c.jumpSpec())
	}

	return opts, nil
}

// ApplyToHTTPTransport wires c's proxy, dialer, and effective handshake
// bound onto a bare transport, for callers that build no API client. It
// calls ProxyCredentials first, exactly as ApplyToOptions does. It installs
// the jump dialer when a jump is set and otherwise a net.Dialer bounded by
// the connect bound, and it sets the transport's proxy function even for a
// direct route, so an ambient proxy environment never applies unless the
// connection chose it. On failure it returns (nil, err) and leaves tr
// untouched, so a caller checks the error before it touches the transport.
func (c Connection) ApplyToHTTPTransport(tr *http.Transport) (*http.Transport, error) {
	if tr == nil {
		return nil, errors.New("apply connection: the http.Transport is nil")
	}

	spec, err := c.transportProxy()
	if err != nil {
		return nil, err
	}

	proxy, err := apiclient.ProxyFunc(spec)
	if err != nil {
		return nil, err
	}

	tr.Proxy = proxy

	if c.hasJump() {
		tr.DialContext = apiclient.JumpDialContext(c.jumpSpec())
	} else {
		tr.DialContext = (&net.Dialer{Timeout: c.resolvedTimeouts().Connect}).DialContext
	}

	tr.TLSHandshakeTimeout = c.effectiveTLSHandshake()

	return tr, nil
}

// Via names the route c takes, in the vocabulary `context validate --connect`
// and `auth status` both print: "direct", "jump <hops>", "proxy <url>",
// "jump <hops> + proxy <url>", "proxy <url> (from environment)", or
// "direct (environment proxy not applicable)". For the environment toggle
// it evaluates the environment proxy function against the resolved API URL,
// and a jump route whose environment proxy does not apply reads
// "jump <hops> (environment proxy not applicable)". An environment proxy
// URL that Go cannot use reads "proxy from environment (not a valid proxy
// URL)", because the parse error would quote the URL whole. Any proxy URL
// it returns is already redacted.
func (c Connection) Via() string {
	var parts []string

	if c.hasJump() {
		parts = append(parts, "jump "+c.jumpHops())
	}

	switch {
	case c.Proxy.URL != nil:
		parts = append(parts, "proxy "+c.Proxy.String())

	case c.Proxy.FromEnv:
		u, err := c.environmentProxy()

		switch {
		case err != nil:
			parts = append(parts, "proxy from environment (not a valid proxy URL)")
		case u == nil && len(parts) == 0:
			return "direct (environment proxy not applicable)"
		case u == nil:
			return parts[0] + " (environment proxy not applicable)"
		default:
			parts = append(parts, "proxy "+redact.ProxyURL(u.String())+" (from environment)")
		}
	}

	if len(parts) == 0 {
		return "direct"
	}

	return strings.Join(parts, " + ")
}

// environmentProxy asks http.ProxyFromEnvironment which proxy, if any, it
// would pick for c's API URL.
func (c Connection) environmentProxy() (*url.URL, error) {
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: sourceOr(c.Protocol, "https"), Host: c.hostPort(), Path: "/"},
		Header: http.Header{},
	}

	return http.ProxyFromEnvironment(req)
}

// hostPort renders c's endpoint as host:port, with an IPv6 host already in
// brackets.
func (c Connection) hostPort() string {
	return c.Host + ":" + strconv.Itoa(c.Port)
}

// route is Via without the environment lookup, for String and LogValue,
// which must not depend on the process environment.
func (c Connection) route() string {
	var parts []string

	if c.hasJump() {
		parts = append(parts, "jump "+c.jumpHops())
	}

	switch {
	case c.Proxy.URL != nil:
		parts = append(parts, "proxy "+c.Proxy.String())
	case c.Proxy.FromEnv:
		parts = append(parts, "proxy from environment")
	}

	if len(parts) == 0 {
		return "direct"
	}

	return strings.Join(parts, " + ")
}

// trust names c's resolved trust mode.
func (c Connection) trust() string {
	switch {
	case c.Fingerprint != "":
		return "fingerprint pin (" + c.FingerprintSource + ")"
	case c.Insecure:
		return "insecure"
	case c.CACert != "":
		return "CA bundle " + c.CACert
	case c.TOFU:
		return "trust on first use"
	case c.TOFUReadOnly:
		return "trust on first use (read-only)"
	default:
		return "system roots"
	}
}

// endpointURL renders c's endpoint as scheme://host:port.
func (c Connection) endpointURL() string {
	return sourceOr(c.Protocol, "https") + "://" + c.hostPort()
}

// String returns a form of c in which any proxy credential is redacted and
// the stored proxy password reference never appears.
func (c Connection) String() string {
	return fmt.Sprintf("connection to %s for context %q via %s, trust %s, timeouts connect %s tls-handshake %s request %s",
		c.endpointURL(), c.ContextName, c.route(), c.trust(),
		c.Timeouts.Connect, c.Timeouts.TLSHandshake, c.Timeouts.Request)
}

// GoString returns the same redacted form, so %#v never falls back to Go's
// struct dump, which would print Proxy.PasswordRef in the clear.
func (c Connection) GoString() string {
	return "cli.Connection(" + strconv.Quote(c.String()) + ")"
}

// connectionView is the redacted form of a Connection that every structured
// rendering shares, so a log line, a JSON document, and a YAML document all
// carry the same fields and none of them can reach Proxy.PasswordRef or a
// proxy URL's password.
type connectionView struct {
	Context             string `json:"context"               yaml:"context"`
	Endpoint            string `json:"endpoint"              yaml:"endpoint"`
	Route               string `json:"route"                 yaml:"route"`
	Trust               string `json:"trust"                 yaml:"trust"`
	ConnectTimeout      string `json:"connect_timeout"       yaml:"connect_timeout"`
	TLSHandshakeTimeout string `json:"tls_handshake_timeout" yaml:"tls_handshake_timeout"`
	RequestTimeout      string `json:"request_timeout"       yaml:"request_timeout"`
}

// view returns c's redacted form.
func (c Connection) view() connectionView {
	return connectionView{
		Context:             c.ContextName,
		Endpoint:            c.endpointURL(),
		Route:               c.route(),
		Trust:               c.trust(),
		ConnectTimeout:      c.Timeouts.Connect.String(),
		TLSHandshakeTimeout: c.Timeouts.TLSHandshake.String(),
		RequestTimeout:      c.Timeouts.Request.String(),
	}
}

// LogValue returns the same redacted form for log/slog, as a group of
// fields.
func (c Connection) LogValue() slog.Value {
	v := c.view()

	return slog.GroupValue(
		slog.String("context", v.Context),
		slog.String("endpoint", v.Endpoint),
		slog.String("route", v.Route),
		slog.String("trust", v.Trust),
		slog.String("connect_timeout", v.ConnectTimeout),
		slog.String("tls_handshake_timeout", v.TLSHandshakeTimeout),
		slog.String("request_timeout", v.RequestTimeout),
	)
}

// MarshalJSON renders the fields LogValue logs, so encoding/json never falls
// back to the struct's own fields, which would print Proxy.PasswordRef in
// the clear.
func (c Connection) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.view())
}

// MarshalYAML renders the fields LogValue logs, for the same reason as
// MarshalJSON. go-yaml calls it for a value, a pointer, and a field alike.
func (c Connection) MarshalYAML() (any, error) {
	return c.view(), nil
}

// pinExplainedError is a certificate failure rewritten in terms of an
// endpoint override. It keeps the original error in its chain.
type pinExplainedError struct {
	msg string
	err error
}

func (e *pinExplainedError) Error() string { return e.msg }

func (e *pinExplainedError) Unwrap() error { return e.err }

// WrapPinMismatch explains a pinned-certificate failure under an endpoint
// override. When c.EndpointSource is set, c.FingerprintSource is
// "tls.fingerprint", and err either satisfies errors.Is(err,
// ErrPinMismatch) or carries either of the kit's two fingerprint failure
// texts, "cannot verify certificate fingerprint" or "unknown certificate
// fingerprint (manual verification required)", it returns an error reading
// `context %q pins a certificate that %s does not present; pass
// --api-fingerprint for that host`, where %s is the overridden endpoint and
// its source, such as `pve9:8006 (from --api-endpoint)`, and it wraps err.
// This pin rule takes precedence, because a context that pins a fingerprint
// and enables trust on first use keeps a cache path under an override, and
// the kit then reports a mismatch with the unknown-fingerprint text. When
// c.EndpointSource is set, c.TOFUReadOnly is true, the pin rule did not
// apply, and err carries the kit's unknown-fingerprint text, it returns an
// error reading `context %q has no trusted certificate for %s, and trust on
// first use is off under an endpoint override; pass --api-fingerprint for
// that host`, with the same %s, and it wraps err. Otherwise, including when
// the pin came from --api-fingerprint, it returns err unchanged. An error it
// has already rewritten comes back unchanged.
func (c Connection) WrapPinMismatch(err error) error {
	if err == nil || c.EndpointSource == "" {
		return err
	}

	if _, explained := errors.AsType[*pinExplainedError](err); explained {
		return err
	}

	text := err.Error()
	unknown := strings.Contains(text, kitUnknownFingerprint)
	mismatch := errors.Is(err, ErrPinMismatch) || strings.Contains(text, kitCannotVerifyFingerprint)
	endpoint := fmt.Sprintf("%s (from %s)", c.hostPort(), c.EndpointSource)

	switch {
	case c.FingerprintSource == sourceTLSFingerprint && (mismatch || unknown):
		return &pinExplainedError{
			msg: fmt.Sprintf("context %q pins a certificate that %s does not present; pass --%s for that host",
				c.ContextName, endpoint, flagAPIFingerprint),
			err: err,
		}

	case c.TOFUReadOnly && unknown:
		return &pinExplainedError{
			msg: fmt.Sprintf("context %q has no trusted certificate for %s, and trust on first use is off "+
				"under an endpoint override; pass --%s for that host", c.ContextName, endpoint, flagAPIFingerprint),
			err: err,
		}

	default:
		return err
	}
}

// RegisterConnectionFlags registers the nine --api-* flags on fs with their
// types, defaults, and help text. The root calls it for its persistent
// flags and tests call it on a bare flag set, so both share one
// registration. The three timeouts are strings rather than pflag durations,
// so a malformed value prints the same text from a flag, the environment,
// and the config file.
func RegisterConnectionFlags(fs *pflag.FlagSet) {
	fs.String(flagAPIEndpoint, "",
		"override the context's API endpoint for this invocation, as [scheme://]host[:port] ($PMX_API_ENDPOINT)")
	fs.String(flagAPIJump, "",
		"tunnel the API connection through this ssh jump host, as [user@]host[:port] (comma-separated for a chain); "+
			`"none" dials direct ($PMX_API_JUMP); -J on ssh commands is separate`)
	fs.String(flagAPIProxy, "",
		"send API requests through this socks5, socks5h, or http proxy URL, with no credentials in the URL; "+
			`"none" disables a configured proxy ($PMX_API_PROXY)`)
	fs.Bool(flagAPIProxyFromEnv, false,
		"honour $HTTPS_PROXY (or $HTTP_PROXY) and $NO_PROXY for API requests; "+
			"use --api-proxy-from-env=false to override a context that enables it")
	fs.String(flagAPICACert, "",
		"verify the server against this CA certificate (PEM) for this invocation, replacing the context's trust "+
			"settings; not with --insecure or --api-fingerprint ($PMX_API_CA_CERT)")
	fs.String(flagAPIFingerprint, "",
		"pin the server's TLS certificate to this hex SHA-256 fingerprint for this invocation, replacing the "+
			"context's trust settings with no trust-on-first-use prompt; not with --insecure or --api-ca-cert "+
			"($PMX_API_FINGERPRINT)")
	fs.String(flagAPIConnectTimeout, "",
		"bound TCP connection setup, e.g. 5s, in whole seconds rounded up; through a proxy, only the connect to "+
			"the proxy ($PMX_API_CONNECT_TIMEOUT)")
	fs.String(flagAPITLSHandshakeTimeout, "",
		"bound the TLS handshake, e.g. 10s, in whole seconds rounded up; through a jump, the connect bound is "+
			"added to it ($PMX_API_TLS_HANDSHAKE_TIMEOUT)")
	fs.String(flagAPIRequestTimeout, "",
		"bound each attempt of an API request, including an upload's whole body, e.g. 30s; an idempotent "+
			"request may make four attempts ($PMX_API_REQUEST_TIMEOUT)")
}

// OverridesFromCommand reads the nine connection flags off cmd, reads the
// root's persistent --insecure through cmd.Root().PersistentFlags(), merges
// the eight PMX_API_* variables, fills the source fields, and parses
// --api-endpoint, --api-proxy, --api-fingerprint, and the three durations,
// so a malformed value errors before any client is built. It rejects any
// userinfo in --api-proxy. It reads no configuration file and resolves no
// secret, so it is safe to call from a completion helper and from the
// invocation audit record.
//
// A flag counts only when cobra reports it as changed and its value is not
// empty, and a variable counts only when it is not empty, so an empty value
// from either source means unset. A flag that is not registered on cmd or
// its root reads as unset.
func OverridesFromCommand(cmd *cobra.Command) (ConnectionOverrides, error) {
	if cmd == nil {
		return ConnectionOverrides{}, errors.New("read connection overrides: the command is nil")
	}

	var ov ConnectionOverrides

	if f := cmd.Root().PersistentFlags().Lookup("insecure"); f != nil {
		insecure, err := strconv.ParseBool(f.Value.String())
		if err != nil {
			return ConnectionOverrides{}, fmt.Errorf("read --insecure: %w", err)
		}

		ov.Insecure = insecure
	}

	if raw, source := stringOverride(cmd, flagAPIEndpoint, envAPIEndpoint); raw != "" {
		ep, err := ParseEndpoint(source, raw)
		if err != nil {
			return ConnectionOverrides{}, err
		}

		ov.Host, ov.Port, ov.Protocol, ov.EndpointSource = ep.Host, ep.Port, ep.Protocol, source
	}

	ov.Jump, ov.JumpSource = stringOverride(cmd, flagAPIJump, envAPIJump)

	ov.Proxy, ov.ProxySource = stringOverride(cmd, flagAPIProxy, envAPIProxy)
	if ov.Proxy != "" && ov.Proxy != connectionNone {
		if _, err := parseOverrideProxy(ov.ProxySource, ov.Proxy); err != nil {
			return ConnectionOverrides{}, err
		}
	}

	if f := lookupConnectionFlag(cmd, flagAPIProxyFromEnv); f != nil && f.Changed {
		fromEnv, err := strconv.ParseBool(f.Value.String())
		if err != nil {
			return ConnectionOverrides{}, fmt.Errorf("read --%s: %w", flagAPIProxyFromEnv, err)
		}

		ov.ProxyFromEnv, ov.ProxyFromEnvSet = fromEnv, true
	}

	ov.CACert, ov.CACertSource = stringOverride(cmd, flagAPICACert, envAPICACert)

	ov.Fingerprint, ov.FingerprintSource = stringOverride(cmd, flagAPIFingerprint, envAPIFingerprint)
	if ov.Fingerprint != "" {
		if err := checkFingerprint(ov.FingerprintSource, ov.Fingerprint); err != nil {
			return ConnectionOverrides{}, err
		}
	}

	var err error

	if ov.Connect, ov.ConnectRaw, ov.ConnectSource, err = durationOverride(
		cmd, flagAPIConnectTimeout, envAPIConnectTimeout); err != nil {
		return ConnectionOverrides{}, err
	}

	if ov.TLSHandshake, ov.TLSHandshakeRaw, ov.TLSHandshakeSource, err = durationOverride(
		cmd, flagAPITLSHandshakeTimeout, envAPITLSHandshakeTimeout); err != nil {
		return ConnectionOverrides{}, err
	}

	if ov.Request, ov.RequestRaw, ov.RequestSource, err = durationOverride(
		cmd, flagAPIRequestTimeout, envAPIRequestTimeout); err != nil {
		return ConnectionOverrides{}, err
	}

	return ov, nil
}

// lookupConnectionFlag finds the flag called name on cmd, falling back to
// the flags cmd inherits, so it works on a parsed leaf, on the root itself,
// and on a command whose parents' persistent flags are not merged yet.
func lookupConnectionFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}

	return cmd.InheritedFlags().Lookup(name)
}

// stringOverride returns the value of the flag called flagName when cobra
// reports it as changed and it is not empty, and otherwise the value of the
// environment variable envName when it is not empty, together with the
// source that supplied it. It returns two empty strings when neither did.
func stringOverride(cmd *cobra.Command, flagName, envName string) (string, string) {
	if f := lookupConnectionFlag(cmd, flagName); f != nil && f.Changed {
		if v := f.Value.String(); v != "" {
			return v, "--" + flagName
		}
	}

	if v := os.Getenv(envName); v != "" {
		return v, "$" + envName
	}

	return "", ""
}

// durationOverride reads one timeout override and parses it with
// config.ParseTimeout, naming the flag or the variable in its error. It
// returns the parsed bound, the text it parsed, and the source, or three
// zero values when neither source is set.
func durationOverride(cmd *cobra.Command, flagName, envName string) (time.Duration, string, string, error) {
	raw, source := stringOverride(cmd, flagName, envName)
	if raw == "" {
		return 0, "", "", nil
	}

	d, err := config.ParseTimeout(source, raw)
	if err != nil {
		return 0, "", "", err
	}

	return d, raw, source, nil
}
