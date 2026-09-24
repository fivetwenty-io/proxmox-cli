package context

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
)

// probeCeiling is the probe's own bound on a whole request. It applies
// unless the operator set a request timeout, so a server that completes the
// handshake and then goes quiet costs `validate --all --connect` five
// seconds per context rather than the thirty a real API call would wait.
const probeCeiling = 5 * time.Second

// probeResult carries the outcome of one live --connect probe.
type probeResult struct {
	// Reachable is true when the version endpoint answered over TLS/HTTP.
	Reachable bool

	// Err is the transport error when Reachable is false. Its text is raw,
	// so a caller renders it only through unreachableText, which masks any
	// URL userinfo and can find a *apiclient.JumpError or a pin failure in
	// its chain.
	Err error

	// ProductGuess is the product the endpoint identified itself as via its
	// Server response header ("pve", "pbs", or "pdm"), or "" when the header
	// gave no reliable signal. The probe never guesses beyond the header.
	ProductGuess string

	// Via is the route the probe took, as Connection.Via names it.
	Via string
}

// probeContext performs the live half of `context validate --connect`: an
// unauthenticated GET of /api2/json/version, which every Proxmox product
// serves without credentials. It builds a bare http.Client from the resolved
// connection rather than a product API client, so the validate verb keeps its
// noClient annotation, and the probe takes exactly the jump, proxy, TLS
// trust, and timeouts a real API call through conn would take.
//
// ctx is the command's own context, so cancelling the command cancels an
// in-flight probe. A nil ctx means context.Background.
//
// The returned error is a setup failure, such as a proxy password that does
// not resolve or a CA bundle that cannot be read, and nothing was dialled
// when it is set. An endpoint that could not be reached is not an error: it
// is reported in the result, with Reachable false.
//
// The transport refuses keep-alives, and the deferred CloseIdleConnections
// cancels any dial the transport detached from the request, so a probe
// through a jump never leaves an ssh child running once it returns.
func probeContext(ctx context.Context, conn cli.Connection) (probeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	client, tr, err := newProbeClient(conn)
	if err != nil {
		return probeResult{}, err
	}
	defer tr.CloseIdleConnections()

	via := conn.Via()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL(conn), nil)
	if err != nil {
		return probeResult{}, fmt.Errorf("build the probe request for context %q: %w", conn.ContextName, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return probeResult{Reachable: false, Err: err, Via: via}, nil
	}

	// The body is never read. Keep-alives are off, so closing it closes the
	// connection, and through a jump that ends the ssh child.
	_ = resp.Body.Close()

	return probeResult{
		Reachable:    true,
		ProductGuess: productFromServerHeader(resp.Header.Get("Server")),
		Via:          via,
	}, nil
}

// newProbeClient builds the probe's client and returns its transport too, so
// probeContext can close it. The transport carries conn's proxy, dialer, and
// handshake bound, and refuses keep-alives. The error from
// ApplyToHTTPTransport is checked before the transport is used, because it
// returns a nil transport on failure.
func newProbeClient(conn cli.Connection) (*http.Client, *http.Transport, error) {
	tlsCfg, err := probeTLSConfig(conn)
	if err != nil {
		return nil, nil, err
	}

	tr, err := conn.ApplyToHTTPTransport(&http.Transport{TLSClientConfig: tlsCfg, DisableKeepAlives: true})
	if err != nil {
		return nil, nil, err
	}

	return &http.Client{Timeout: probeBound(conn), Transport: tr}, tr, nil
}

// probeURL renders the version endpoint of conn. conn.Host already carries
// the brackets of an IPv6 literal.
func probeURL(conn cli.Connection) string {
	protocol := conn.Protocol
	if protocol == "" {
		protocol = "https"
	}

	return fmt.Sprintf("%s://%s:%d/api2/json/version", protocol, conn.Host, conn.Port)
}

// probeBound is the probe's whole-request bound. It is the resolved request
// bound when a flag, an environment variable, or the context's
// timeout.request set one. Otherwise, through a jump, it is the connect
// bound plus the handshake bound plus five seconds, because the bastion's
// connect, key exchange, and authentication all run inside the first two,
// and the probe keeps its own five seconds for the exchange that follows.
// Otherwise it is five seconds. It never returns zero, which http.Client
// would read as no bound at all.
func probeBound(conn cli.Connection) time.Duration {
	if conn.TimeoutsSet.Request && conn.Timeouts.Request > 0 {
		return conn.Timeouts.Request
	}

	if strings.TrimSpace(conn.Jump.Chain) == "" {
		return probeCeiling
	}

	defaults := apiclient.DefaultTimeoutSpec()

	connect := conn.Timeouts.Connect
	if connect <= 0 {
		connect = defaults.Connect
	}

	handshake := conn.Timeouts.TLSHandshake
	if handshake <= 0 {
		handshake = defaults.TLSHandshake
	}

	return saturatingAdd(saturatingAdd(connect, handshake), probeCeiling)
}

// saturatingAdd adds two non-negative durations and caps the result at the
// largest duration instead of wrapping negative. It is a copy of the helper
// of the same name in internal/cli/connection.go, which sizes the jump
// route's first-byte timer, and the two must change together.
func saturatingAdd(a, b time.Duration) time.Duration {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}

	return a + b
}

// probeTLSConfig builds the probe's TLS settings from conn's resolved trust,
// in the order the API client honours them. A pinned fingerprint is checked
// whatever the insecure switch says, as it is on every real call, and it
// takes precedence over a CA bundle. Insecure then disables chain
// verification. A CA bundle replaces the system roots. With none of them,
// the system roots verify the chain.
//
// A pinned context is the normal shape for Proxmox, whose certificates are
// self-signed and never chain to a system root, and it is the shape
// `pmx lab` mints automatically, so probing it with stock verification would
// report a healthy host unreachable.
func probeTLSConfig(conn cli.Connection) (*tls.Config, error) {
	switch {
	case conn.Fingerprint != "":
		// The pin replaces chain verification. It runs in VerifyConnection
		// rather than VerifyPeerCertificate because the latter is skipped on
		// a resumed session, which would let a resumed handshake past it.
		return &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // G402: replaced by the pin in VerifyConnection
			VerifyConnection:   fingerprintVerifier(conn.Fingerprint),
		}, nil

	case conn.Insecure:
		//nolint:gosec // G402: InsecureSkipVerify is the operator's explicit opt-in, warned about at the call site
		return &tls.Config{InsecureSkipVerify: true}, nil

	case conn.CACert != "":
		pool, err := loadCABundle(conn.CACert)
		if err != nil {
			return nil, err
		}

		return &tls.Config{RootCAs: pool}, nil

	default:
		return &tls.Config{}, nil
	}
}

// loadCABundle reads the PEM bundle at path into a pool that holds only its
// certificates. It holds the path to the rules the API client applies, so a
// bundle the probe accepts is one every real call accepts too: the path
// must be absolute, readable, and hold at least one PEM certificate.
func loadCABundle(path string) (*x509.CertPool, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return nil, fmt.Errorf("CA bundle %q must be an absolute path", path)
	}

	pemBytes, err := os.ReadFile(clean)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("CA bundle %q holds no PEM certificate", path)
	}

	return pool, nil
}

// fingerprintVerifier returns a tls.Config.VerifyConnection that accepts the
// peer only when its leaf certificate's SHA-256 matches want. Comparison is
// case-insensitive and ignores colons, so a fingerprint copied from the PVE
// UI, from `pvenode cert info`, or from a pmx-written context all compare
// equal. Every failure wraps cli.ErrPinMismatch, so a caller can recognise a
// pin failure with errors.Is and explain it under an endpoint override.
func fingerprintVerifier(want string) func(tls.ConnectionState) error {
	wantNorm := strings.ToLower(strings.ReplaceAll(want, ":", ""))

	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return fmt.Errorf("tls fingerprint pin: %w: the peer presented no certificate", cli.ErrPinMismatch)
		}

		sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
		got := hex.EncodeToString(sum[:])

		if got != wantNorm {
			return fmt.Errorf("tls fingerprint pin: %w: the peer presented %s, the pin is %s",
				cli.ErrPinMismatch, got, wantNorm)
		}

		return nil
	}
}

// unreachableText explains why a probe through conn failed, naming the route
// once. A pin failure under an endpoint override is first rewritten by
// conn.WrapPinMismatch. A bastion failure reads
// "unreachable via jump <chain>: <detail>", where the detail is the
// JumpError's own Detail, whatever wrapping the transport added around it.
// A failure on any other route that is not direct reads
// "unreachable via <route>: <cause>", where the cause drops the transport's
// `Get "<url>": ` prefix, so a refused proxy shows its proxyconnect error. A
// direct failure keeps the transport's whole text after "unreachable: ".
//
// Only the cause passes through redact.URLUserinfo. The route is left as
// written, because jumpChainText has already masked the chain and Via has
// already masked every proxy URL, and a second, free-text mask over a
// multi-hop chain could misread a hop's port and a later hop's "@" as a
// password.
func unreachableText(conn cli.Connection, err error) string {
	if err == nil {
		return "unreachable: the probe failed without an error"
	}

	err = conn.WrapPinMismatch(err)

	if je, ok := errors.AsType[*apiclient.JumpError](err); ok {
		return fmt.Sprintf("unreachable via jump %s: %s", jumpChainText(je.Chain), redact.URLUserinfo(je.Detail()))
	}

	if via := conn.Via(); via != "direct" && !strings.HasPrefix(via, "direct ") {
		return fmt.Sprintf("unreachable via %s: %s", via, redact.URLUserinfo(transportCause(err)))
	}

	return "unreachable: " + redact.URLUserinfo(err.Error())
}

// transportCause returns err's text without the `Get "<url>": ` prefix an
// http.Client adds when err is the *url.Error it returned. Any other error
// keeps its whole text, so a message another layer already rewrote is never
// cut short.
func transportCause(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && error(ue) == err && ue.Err != nil {
		return ue.Err.Error()
	}

	return err.Error()
}

// jumpChainText renders a bastion chain for an error message: masked by
// apiclient.RedactJumpChain, trimmed, and Go-quoted if it holds a control
// character, so a chain from a hand-edited config never reaches the terminal
// raw.
func jumpChainText(chain string) string {
	shown := strings.TrimSpace(apiclient.RedactJumpChain(chain))
	if strings.ContainsFunc(shown, func(r rune) bool { return r != '\t' && unicode.IsControl(r) }) {
		return strconv.Quote(shown)
	}

	return shown
}

// productFromServerHeader maps a Proxmox daemon's Server response header to
// a product identifier: pve-api-daemon → pve, proxmox-backup → pbs,
// proxmox-datacenter → pdm. Anything else (including an absent header)
// returns "" — the probe reports "not verifiable" rather than guessing.
func productFromServerHeader(server string) string {
	switch {
	case strings.HasPrefix(server, "pve-api-daemon"):
		return config.ProductPVE
	case strings.Contains(server, "proxmox-backup"):
		return config.ProductPBS
	case strings.Contains(server, "proxmox-datacenter"):
		return config.ProductPDM
	default:
		return ""
	}
}
