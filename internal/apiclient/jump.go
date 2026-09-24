package apiclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

const (
	// jumpMemoTTL is how long a dialer refuses new dials after a terminal
	// failure, so a dead bastion costs one authentication per client rather
	// than one per retry or per fan-out worker.
	jumpMemoTTL = 10 * time.Second

	// jumpFailureBudget bounds how long a failed Read or Write, and the
	// reaper, wait for ssh's exit and for its standard error to reach
	// end-of-file before composing the message. A descendant that keeps
	// standard error open costs at most this much and never hides the exit.
	jumpFailureBudget = 500 * time.Millisecond

	// jumpCloseGrace is how long ssh gets to exit on end-of-file after Close
	// before it is sent a terminate signal, and again before it is killed.
	jumpCloseGrace = 2 * time.Second

	// jumpWaitDelay is the os/exec backstop for Wait. Every standard stream
	// is an *os.File, so os/exec runs no copy goroutine and Wait returns as
	// soon as ssh exits; the delay only matters if that ever changes.
	jumpWaitDelay = 2 * time.Second

	// jumpShutdownSettle is how long ShutdownJumps waits after its kill for
	// every registry entry to end.
	jumpShutdownSettle = time.Second

	// jumpStderrLineMax caps one kept standard-error line, so a peer that
	// writes an endless line cannot grow the buffer without bound.
	jumpStderrLineMax = 4096
)

// ErrJump is what every JumpError unwraps to, so a caller can recognise a
// bastion failure with errors.Is.
var ErrJump = errors.New("ssh jump failed")

// errJumpsShutdown is the cause a dial reports when its child registers after
// ShutdownJumps or KillJumps has closed the registry.
var errJumpsShutdown = errors.New("the ssh jump dialer is shut down because pmx is exiting")

// JumpSpec describes how to reach the API through an ssh bastion chain.
type JumpSpec struct {
	// Chain is ssh ProxyJump syntax, [user@]host[:port] or
	// ssh://[user@]host[:port], comma-separated. The bastion takes its user
	// and port from the hop string and everything else from ~/.ssh/config and
	// the agent, exactly as `pmx ssh -J` does, so pmx never passes it -l,
	// -p, or -i.
	Chain string

	// ConnectTimeout bounds the bastion's TCP connect through ssh's own
	// ConnectTimeout option. It must be set, and it is rounded up to whole
	// seconds with a floor of one, because ssh reads ConnectTimeout=0 as "no
	// bound" rather than as "no delay".
	ConnectTimeout time.Duration

	// FirstByteTimeout bounds everything between Start and the first byte
	// read from ssh, which is the bastion's connect, key exchange,
	// authentication, the channel open, and the TLS handshake. The resolver
	// sets it only on a route where a TLS handshake or a SOCKS negotiation
	// comes first, capped below the request bound so it always fires first.
	// Zero arms no timer.
	FirstByteTimeout time.Duration

	// Program is the ssh binary to run. Empty means "ssh"; tests point it at
	// a stand-in script.
	Program string
}

// JumpError is a bastion failure with the fields a caller needs to explain
// it. Error() has a fixed format, "ssh jump <Chain> -> <Addr>: <detail>",
// where the detail is what Detail returns and the chain is masked by
// RedactJumpChain. Stderr holds the most recent standard-error line that
// contains "open failed:", then "; ", then ssh's last standard-error line,
// and it holds the last line alone when no such line exists or when that
// line is itself the last one. Each line has
// already dropped its trailing carriage return, because ssh ends every log
// line with "\r\n".
type JumpError struct {
	Chain, Addr, Stderr string
	TimedOut            bool

	// Timeout is the FirstByteTimeout that fired, and it appears only in the
	// message.
	Timeout time.Duration

	// Err is the cause when ssh wrote nothing of its own, such as the exec
	// error of a start failure or the refusal of a dial that registers its
	// child after ShutdownJumps or KillJumps has closed the registry.
	Err error

	// ExitStatus is ssh's exit status once the exit is known. It is -1 until
	// then, as on the timer path, which builds its error before the reaper
	// has seen the exit, and it stays -1 when ssh never started or a signal
	// ended it.
	ExitStatus int

	// Signaled is true when the process state shows that a signal ended ssh,
	// and Detail reads it rather than inferring a signal from -1. On
	// Windows, where Process.Kill leaves exit code 1 and no signal state, a
	// killed ssh.exe reads as an exit with status 1.
	Signaled bool
}

func (e *JumpError) Error() string {
	return fmt.Sprintf("ssh jump %s -> %s: %s", printableChain(RedactJumpChain(e.Chain)), e.Addr, e.Detail())
}

// printableChain returns chain unchanged when it holds no control character
// other than a tab, and Go-quoted otherwise. A chain the allow-list accepted
// never holds one, so only a rejected chain is quoted, and a newline or an
// escape sequence in the operator's config never reaches the terminal raw.
func printableChain(chain string) string {
	if strings.ContainsFunc(chain, func(r rune) bool { return r != '\t' && unicode.IsControl(r) }) {
		return strconv.Quote(chain)
	}

	return chain
}

// Detail returns the text after the colon in Error(). It is "no response
// within <Timeout>" when TimedOut is set, then Stderr when that is not
// empty, then Err's text when Err is set, then "ssh was ended by a signal
// and printed nothing" when Signaled is set, and otherwise "ssh exited
// with status <ExitStatus> and printed nothing", so no caller can print an
// empty detail.
func (e *JumpError) Detail() string {
	switch {
	case e.TimedOut:
		return "no response within " + e.Timeout.String()
	case e.Stderr != "":
		return e.Stderr
	case e.Err != nil:
		return e.Err.Error()
	case e.Signaled:
		return "ssh was ended by a signal and printed nothing"
	default:
		return fmt.Sprintf("ssh exited with status %d and printed nothing", e.ExitStatus)
	}
}

// Unwrap returns ErrJump together with Err, or ErrJump alone when Err is
// nil, so errors.Is finds either one.
func (e *JumpError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrJump}
	}

	return []error{ErrJump, e.Err}
}

// ApplyJumpSpec routes opts' connections through j, and returns opts
// unchanged when j.Chain is blank.
//
// The Proxmox API is plain HTTPS, so reaching a host that is not directly
// routable is purely a matter of where the TCP connection comes from. That
// makes Options.DialContext the whole mechanism: TLS is still negotiated
// against opts.Host at the far end, so certificate verification, fingerprint
// pinning, and TOFU all behave exactly as they do on a direct connection. This
// deliberately runs after ApplyTOFUOptions for that reason: it changes how the
// bytes travel, never what is checked at the other end.
func ApplyJumpSpec(opts pve.Options, j JumpSpec) pve.Options {
	if strings.TrimSpace(j.Chain) == "" {
		return opts
	}

	opts.DialContext = JumpDialContext(j)

	return opts
}

// ApplyJumpOptions is the string-shaped entry point, kept as a thin wrapper
// that calls ApplyJumpSpec with a five-second connect bound and no
// first-byte timer, so its callers keep their existing timing until they
// pass a resolved JumpSpec themselves.
func ApplyJumpOptions(opts pve.Options, jump string) pve.Options {
	return ApplyJumpSpec(opts, JumpSpec{Chain: jump, ConnectTimeout: 5 * time.Second})
}

// JumpDialContext returns the dial function ApplyJumpSpec installs, for
// callers that build an http.Transport directly, such as the probe. It
// reaches addr through the chain by running `ssh -W <addr> <hop>`, the same
// mechanism OpenSSH's own ProxyJump uses, so the operator's config, keys,
// agent, known_hosts, and certificates all apply unchanged.
//
// The function keeps a failure memo in its closure. After a terminal
// failure, any dial through the same function within ten seconds returns the
// stored error at once without starting ssh. Only a connection's first
// verdict can write the memo, so an exit that follows the timer's verdict
// or a close never replaces the memo or creates one. The function is also
// single-flight before the first byte. While an earlier dial through it has
// no verdict yet, a new dial waits for that verdict. Four events on the
// earlier connection trigger the classification of its outcome, namely its
// first byte, any Read or Write failure, its child's exit, and its Close, and
// the verdict is published only once that classification is done, so a
// terminal failure is already in the memo when the gate clears. The waiter
// takes the memoised error when the verdict is a terminal failure, and it
// starts its own ssh once the earlier connection has read a byte. After any
// other verdict that came before a first byte, one waiter becomes the new
// leader and the others stay gated behind it. A waiter also returns when its
// own dial context is cancelled, but that bounds nothing in practice, because
// the transport cancels a dial context only from CloseIdleConnections.
func JumpDialContext(j JumpSpec) func(context.Context, string, string) (net.Conn, error) {
	d := &jumpDialer{spec: j}

	return d.dial
}

// jumpDialer is the state one JumpDialContext closure carries: the spec, the
// failure memo, and the single-flight gate.
type jumpDialer struct {
	spec  JumpSpec
	hooks jumpHooks

	mu     sync.Mutex
	memo   error
	memoAt time.Time
	flight *jumpFlight
}

// jumpHooks lets a test observe and pace how a dialer's connections reach
// their verdicts. Every field is nil outside tests.
type jumpHooks struct {
	// published is called with the path that published a connection's
	// verdict, after any memo write and before the gate clears.
	published func(source string, v jumpVerdict)

	// classify is called by the reaper just before it classifies an exit
	// that came before the first byte, and it may block.
	classify func()

	// reaped is called when the reaper returns.
	reaped func()

	// closeGrace, when positive, replaces jumpCloseGrace for this dialer's
	// connections, so a test can hold a child past Close for as long as it
	// needs without the escalation racing the scheduler.
	closeGrace time.Duration
}

// jumpFlight is one leader's pending verdict. done closes once the verdict is
// published, and established, written before done closes, tells the waiters
// whether the leader read a byte.
type jumpFlight struct {
	done        chan struct{}
	established bool
}

func (d *jumpDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("ssh jump host cannot carry %q connections (tcp only)", network)
	}

	args, err := jumpSSHArgs(d.spec, addr)
	if err != nil {
		// A chain the allow-list rejects fails the same way on every
		// attempt, so it is terminal rather than something to retry.
		return nil, jumpDialError(addr, &JumpError{Chain: d.spec.Chain, Addr: addr, Err: err, ExitStatus: -1})
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	flight, err := d.acquire(ctx)
	if err != nil {
		return nil, err
	}

	return startJump(ctx, jumpStart{
		program:          d.spec.Program,
		args:             args,
		chain:            d.spec.Chain,
		addr:             addr,
		firstByteTimeout: d.spec.FirstByteTimeout,
		dialer:           d,
		flight:           flight,
	})
}

// acquire passes the memo and the single-flight gate. It returns the flight
// the caller now leads, nil when the caller was released by an established
// leader and so starts its ssh without gating anyone, or the memoised error.
func (d *jumpDialer) acquire(ctx context.Context) (*jumpFlight, error) {
	for {
		d.mu.Lock()

		if d.memo != nil && time.Since(d.memoAt) < jumpMemoTTL {
			err := d.memo
			d.mu.Unlock()

			return nil, err
		}

		f := d.flight
		if f == nil {
			f = &jumpFlight{done: make(chan struct{})}
			d.flight = f
			d.mu.Unlock()

			return f, nil
		}

		d.mu.Unlock()

		select {
		case <-f.done:
			if f.established {
				return nil, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// remember stores a terminal failure in the memo. It is nil-safe, because a
// connection started without a dialer has no memo to write.
func (d *jumpDialer) remember(err error) {
	if d == nil {
		return
	}

	d.mu.Lock()
	d.memo = err
	d.memoAt = time.Now()
	d.mu.Unlock()
}

// publish reports verdict v, which source settled, and then clears the gate
// for f. Every caller holds the only right to publish f, so done is closed
// exactly once. The dialer is nil-safe, and so is f, which is nil for a
// connection that was released by an established leader and gates no one.
func (d *jumpDialer) publish(f *jumpFlight, source string, v jumpVerdict) {
	if d == nil {
		return
	}

	if d.hooks.published != nil {
		d.hooks.published(source, v)
	}

	if f == nil {
		return
	}

	d.mu.Lock()
	if d.flight == f {
		d.flight = nil
	}
	f.established = v == verdictEstablished
	d.mu.Unlock()

	close(f.done)
}

// hookClassify and hookReaped call the matching test hook when one is set.
func (d *jumpDialer) hookClassify() {
	if d != nil && d.hooks.classify != nil {
		d.hooks.classify()
	}
}

func (d *jumpDialer) hookReaped() {
	if d != nil && d.hooks.reaped != nil {
		d.hooks.reaped()
	}
}

// connectTimeoutSeconds rounds d up to whole seconds with a floor of one,
// because ssh reads ConnectTimeout=0 as "no bound" and rounding down would
// shorten a bound the operator asked for.
func connectTimeoutSeconds(d time.Duration) int {
	return max(1, int((d+time.Second-1)/time.Second))
}

// jumpSSHArgs builds the ssh argument list that forwards a connection to addr
// through the jump chain. It validates the chain itself, so a caller that
// skipped the resolver still fails closed.
//
// ssh -W takes exactly one destination, so a multi-hop chain puts every hop
// but the last behind -J and connects to the last: "a,b" becomes
// `ssh -J a -W addr -- b`. The final hop's optional :port needs -p, because
// ssh's destination argument does not accept the [user@]host:port form that -J
// does. The -- keeps a destination from ever being read as an option.
//
// LogLevel=INFO pins ssh's default level on the command line, which wins over
// ~/.ssh/config, so an operator's "LogLevel QUIET" cannot suppress the INFO
// line in which ssh says why the bastion could not reach the node.
func jumpSSHArgs(j JumpSpec, addr string) ([]string, error) {
	hops, err := parseJumpChain(j.Chain)
	if err != nil {
		return nil, err
	}

	last := hops[len(hops)-1]

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds(j.ConnectTimeout)),
	}
	args = append(args, sshcmd.KeepaliveOptionArgs()...)
	args = append(args, "-o", "LogLevel=INFO")

	if len(hops) > 1 {
		inner := make([]string, 0, len(hops)-1)
		for _, h := range hops[:len(hops)-1] {
			inner = append(inner, h.jumpForm())
		}

		args = append(args, "-J", strings.Join(inner, ","))
	}

	if last.port != "" {
		args = append(args, "-p", last.port)
	}

	return append(args, "-W", addr, "--", last.destination()), nil
}

var (
	// jumpUserRE admits a directory login such as alice@corp.example, which
	// SSSD, FreeIPA, and AD-trust bastions commonly need. An @ is inert under
	// /bin/sh -c, so admitting it costs no safety.
	jumpUserRE = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._@-]*$`)

	// jumpHostRE admits a hostname, an IPv4 address, or a ~/.ssh/config alias
	// such as my_bastion, and never a leading "-".
	jumpHostRE = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

	jumpPortRE = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// jumpHop is one validated, normalised hop.
type jumpHop struct {
	user string
	host string
	port string
	ipv6 bool
}

// jumpForm renders the hop for ssh's -J, bracketing an IPv6 literal so its
// colons are never read as a port separator.
func (h jumpHop) jumpForm() string {
	var b strings.Builder

	if h.user != "" {
		b.WriteString(h.user + "@")
	}

	if h.ipv6 {
		b.WriteString("[" + h.host + "]")
	} else {
		b.WriteString(h.host)
	}

	if h.port != "" {
		b.WriteString(":" + h.port)
	}

	return b.String()
}

// destination renders the hop as ssh's destination argument, which takes no
// port and no brackets.
func (h jumpHop) destination() string {
	if h.user == "" {
		return h.host
	}

	return h.user + "@" + h.host
}

// ValidateJumpChain reports whether chain is usable, applying a strict
// allow-list to every hop. It never resolves a name and it never dials.
//
// The strictness is deliberate: OpenSSH releases before 10.3 expand -J into a
// ProxyCommand that runs under /bin/sh -c, so a hop must never carry a shell
// metacharacter, a control character, or a leading "-".
func ValidateJumpChain(chain string) error {
	_, err := parseJumpChain(chain)

	return err
}

// CheckJumpChain runs ValidateJumpChain and, when the chain is rejected,
// returns the error every caller prints: `<name> "<chain>" is not valid:
// <reason>`, with the chain quoted through RedactJumpChain. name labels
// where the chain came from, such as ssh.jump or --api-jump.
func CheckJumpChain(name, chain string) error {
	if err := ValidateJumpChain(chain); err != nil {
		return fmt.Errorf("%s %q is not valid: %v", name, RedactJumpChain(chain), err)
	}

	return nil
}

// jumpRedacted replaces the password part of a misused hop.
const jumpRedacted = "<redacted>"

// jumpEncodedColonRE matches a percent-encoded colon, including one encoded
// more than once, such as "%253A", at the start of its input.
var jumpEncodedColonRE = regexp.MustCompile(`^(?i)%(?:25)*3a`)

// RedactJumpChain returns chain with the password of any misused
// user:password hop replaced by "<redacted>", for quoting a chain in an
// error.
//
// A chain that ValidateJumpChain accepts comes back unchanged. The user
// allow-list admits no ":", so every colon in an accepted chain is a port
// separator or part of an IPv6 literal, and ssh sees no password in it. An
// operator who meant "u:22,x@h" as user u with password "22,x" has written
// a chain ssh accepts as the two hops "u:22" and "x@h", which no rule can
// tell from a real chain, so that chain is also returned as written.
//
// A rejected chain is masked by a deliberately wide rule, because a comma
// inside a password splits it across hops before anything can tell a
// password from a port. The mask starts after the first colon, or after the
// first percent-encoded colon, that has an "@" somewhere after it in the
// chain, and it runs to the last "@" in the chain. The colon of a hop's own
// "ssh://" prefix does not count, but the parser reads "SSH://" as a plain
// hop, so the colon in that one does. Everything between those two points
// is masked, even when part of it is a port, a host, or a whole hop, so
// that no reading of the chain can leave a password byte visible. A
// rejected chain with no such colon, or with no "@", comes back unchanged.
func RedactJumpChain(chain string) string {
	if ValidateJumpChain(chain) == nil {
		return chain
	}

	return jumpPasswordSpan(chain).mask(0, chain)
}

// jumpSpan is the byte range [start, end) of a chain that RedactJumpChain
// masks. It is set only when the chain has a password to mask, and it may
// be empty, as for "u:@host", where the mask still marks the spot.
type jumpSpan struct {
	start, end int
	set        bool
}

// jumpPasswordSpan finds the range RedactJumpChain masks in chain, by the
// rule its comment states.
func jumpPasswordSpan(chain string) jumpSpan {
	last := strings.LastIndexByte(chain, '@')
	if last < 0 {
		return jumpSpan{}
	}

	schemeColon := -1

	for i := range last {
		if i == 0 || chain[i-1] == ',' {
			body := i + len(chain[i:]) - len(strings.TrimLeft(chain[i:], " \t"))
			schemeColon = -1

			if strings.HasPrefix(chain[body:], "ssh://") {
				schemeColon = body + len("ssh")
			}
		}

		switch {
		case chain[i] == ':' && i != schemeColon:
			return jumpSpan{start: i + 1, end: last, set: true}
		case chain[i] == '%':
			if m := jumpEncodedColonRE.FindString(chain[i:]); m != "" {
				return jumpSpan{start: i + len(m), end: last, set: true}
			}
		}
	}

	return jumpSpan{}
}

// mask returns s, which sits at byte offset off in the chain the span was
// found in, with the part of it inside the span replaced by "<redacted>". A
// value wholly inside the span comes back as "<redacted>" alone, and a
// value the span misses comes back unchanged. An empty span marks a value
// only when it falls within that value or at its end.
func (m jumpSpan) mask(off int, s string) string {
	if !m.set {
		return s
	}

	lo, hi := m.start-off, m.end-off

	if m.start == m.end {
		if lo < 0 || lo > len(s) {
			return s
		}
	} else if hi <= 0 || lo >= len(s) {
		return s
	}

	lo, hi = max(lo, 0), min(hi, len(s))

	return s[:lo] + jumpRedacted + s[hi:]
}

// parseJumpChain validates and normalises every hop of chain. Hops are
// numbered from one in its messages, and every value a message quotes is
// masked by the same span RedactJumpChain would mask, so a reason never
// holds a byte that the quoted chain hides.
func parseJumpChain(chain string) ([]jumpHop, error) {
	raw := strings.Split(chain, ",")
	hops := make([]jumpHop, 0, len(raw))
	span := jumpPasswordSpan(chain)
	off := 0

	for i, r := range raw {
		h, err := parseJumpHop(i+1, r, i == len(raw)-1, jumpQuoter{span: span, off: off})
		if err != nil {
			return nil, err
		}

		hops = append(hops, h)
		off += len(r) + len(",")
	}

	return hops, nil
}

// jumpQuoter quotes a value from one hop for an error, masking the part of
// it inside the chain's password span. off is the hop's byte offset in the
// chain, and every value is passed with its own offset within the hop.
type jumpQuoter struct {
	span jumpSpan
	off  int
}

// quote returns s, found at byte offset at within the hop, masked and
// Go-quoted.
func (q jumpQuoter) quote(at int, s string) string {
	return strconv.Quote(q.span.mask(q.off+at, s))
}

// parseJumpHop validates one hop. A plain hop is split at its last @, as
// OpenSSH's parse_user_host_port splits it; an ssh:// hop follows OpenSSH's
// parse_uri instead, splitting at its first @ and percent-decoding the user,
// so the two forms accept exactly what OpenSSH's -J accepts. A bare IPv6
// literal is accepted on the final plain hop only, where no port can follow.
// q quotes every value a message names, at its offset within raw.
func parseJumpHop(n int, raw string, final bool, q jumpQuoter) (jumpHop, error) {
	hop := strings.Trim(raw, " \t")
	if hop == "" {
		return jumpHop{}, fmt.Errorf("hop %d is empty", n)
	}

	var (
		h        jumpHop
		hostport string
	)

	at := len(raw) - len(strings.TrimLeft(raw, " \t"))

	if rest, isURI := strings.CutPrefix(hop, "ssh://"); isURI {
		at += len("ssh://")
		hostport = rest

		if user, hp, found := strings.Cut(rest, "@"); found {
			decoded, err := url.PathUnescape(user)
			if err != nil || !jumpUserRE.MatchString(decoded) {
				return jumpHop{}, fmt.Errorf("hop %d: user %s has a disallowed character", n, q.quote(at, user))
			}

			h.user, hostport = decoded, hp
			at += len(user) + len("@")
		}

		// OpenSSH's URI parser splits an unbracketed host at its first
		// colon, so a bare IPv6 literal never survives in this form.
		final = false
	} else {
		hostport = hop

		if i := strings.LastIndex(hop, "@"); i >= 0 {
			user := hop[:i]
			if !jumpUserRE.MatchString(user) {
				return jumpHop{}, fmt.Errorf("hop %d: user %s has a disallowed character", n, q.quote(at, user))
			}

			h.user, hostport = user, hop[i+1:]
			at += i + len("@")
		}
	}

	host, port, ipv6, err := splitJumpHostPort(n, hostport, final, q, at)
	if err != nil {
		return jumpHop{}, err
	}

	h.host, h.port, h.ipv6 = host, port, ipv6

	return h, nil
}

// splitJumpHostPort splits host[:port], [ipv6][:port], or, when bareIPv6 is
// true, a bare IPv6 literal, and validates both halves. hp sits at byte
// offset at within the hop that q quotes for.
func splitJumpHostPort(
	n int, hp string, bareIPv6 bool, q jumpQuoter, at int,
) (host, port string, ipv6 bool, err error) {
	hostErr := fmt.Errorf("hop %d: host %s is not a hostname, IPv4 address, or bracketed IPv6 literal",
		n, q.quote(at, hp))

	hasPort := false
	portAt := 0

	switch {
	case strings.HasPrefix(hp, "["):
		end := strings.IndexByte(hp, ']')
		if end < 0 || !isIPv6Literal(hp[1:end]) {
			return "", "", false, hostErr
		}

		host, ipv6 = hp[1:end], true

		switch rest := hp[end+1:]; {
		case rest == "":
		case strings.HasPrefix(rest, ":"):
			port, hasPort, portAt = rest[1:], true, end+len("]:")
		default:
			return "", "", false, hostErr
		}

	case strings.Count(hp, ":") > 1:
		if !bareIPv6 || !isIPv6Literal(hp) {
			return "", "", false, hostErr
		}

		host, ipv6 = hp, true

	default:
		host, port, hasPort = strings.Cut(hp, ":")
		portAt = len(host) + len(":")

		if !jumpHostRE.MatchString(host) {
			return "", "", false, fmt.Errorf(
				"hop %d: host %s is not a hostname, IPv4 address, or bracketed IPv6 literal", n, q.quote(at, host))
		}
	}

	if hasPort {
		p, perr := strconv.Atoi(port)
		if !jumpPortRE.MatchString(port) || perr != nil || p < 1 || p > 65535 {
			return "", "", false, fmt.Errorf("hop %d: port %s is out of range [1, 65535]", n, q.quote(at+portAt, port))
		}
	}

	return host, port, ipv6, nil
}

// isIPv6Literal reports whether s is an IPv6 address with no zone, which is
// the only form a bracket or a bare final hop may carry.
func isIPv6Literal(s string) bool {
	a, err := netip.ParseAddr(s)

	return err == nil && a.Is6() && a.Zone() == ""
}

// jumpDialError wraps a terminal failure the way the kit's retry loop treats
// as final: an OpError whose Op is "dial".
func jumpDialError(addr string, je *JumpError) *net.OpError {
	return &net.OpError{Op: "dial", Net: "tcp", Addr: jumpAddr(addr), Err: je}
}

// newJumpCommand builds the ssh child isolated from pmx's terminal: its own
// session or process group from the platform file, and an environment in
// which no askpass can be reached, so no hop of a chain can prompt.
func newJumpCommand(program string, args []string) *exec.Cmd {
	if program == "" {
		program = "ssh"
	}

	cmd := exec.Command(program, args...) //nolint:gosec // args are validated by the jump allow-list
	cmd.Env = jumpEnv(os.Environ())
	cmd.WaitDelay = jumpWaitDelay
	isolateJumpCommand(cmd)

	return cmd
}

// jumpEnv returns env without DISPLAY and WAYLAND_DISPLAY and with
// SSH_ASKPASS_REQUIRE=never. OpenSSH before 8.4 ignores SSH_ASKPASS_REQUIRE
// and falls through to a graphical askpass whenever a display variable is set
// and it has no terminal, so dropping both covers every version.
func jumpEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)

	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(key, "DISPLAY") || strings.EqualFold(key, "WAYLAND_DISPLAY") ||
			strings.EqualFold(key, "SSH_ASKPASS_REQUIRE") {
			continue
		}

		out = append(out, kv)
	}

	return append(out, "SSH_ASKPASS_REQUIRE=never")
}

// jumpStart carries what startJump needs for one ssh child.
type jumpStart struct {
	program          string
	args             []string
	chain            string
	addr             string
	firstByteTimeout time.Duration
	dialer           *jumpDialer
	flight           *jumpFlight
}

// startJump starts ssh and returns its standard input and output as a
// net.Conn. Every return that does not hand back a connection publishes the
// flight's verdict itself, so no path can leave the gate closed.
func startJump(ctx context.Context, s jumpStart) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		s.dialer.publish(s.flight, "start", verdictNonTerminal)

		return nil, err
	}

	// Own every pipe end rather than using cmd.StdinPipe and friends: those
	// hand back wrappers, *os.File is what carries the deadlines net.Conn
	// requires, and an *os.File standard error means os/exec runs no copy
	// goroutine, so Wait returns the moment ssh exits.
	files, err := openJumpPipes()
	if err != nil {
		s.dialer.publish(s.flight, "start", verdictNonTerminal)

		return nil, fmt.Errorf("ssh jump %s: %w", s.chain, err)
	}

	cmd := newJumpCommand(s.program, s.args)
	cmd.Stdin = files.inR
	cmd.Stdout = files.outW
	cmd.Stderr = files.errW

	if err := cmd.Start(); err != nil {
		closeAll(files.inR, files.inW, files.outR, files.outW, files.errR, files.errW)

		// There is no process to retry, so a start failure is terminal, and
		// the exec error is the detail the operator needs.
		op := jumpDialError(s.addr, &JumpError{Chain: s.chain, Addr: s.addr, Err: err, ExitStatus: -1})
		s.dialer.remember(op)
		s.dialer.publish(s.flight, "start", verdictTerminal)

		return nil, op
	}

	// The child holds its own descriptors now. Keeping the parent's copies
	// open would stop reads from ever seeing end-of-file when ssh exits, and
	// a standard-error write end left open here would hold every failure for
	// the whole budget and leak a descriptor per dial.
	closeAll(files.inR, files.outW, files.errW)

	c := &jumpConn{
		cmd:              cmd,
		in:               files.inW,
		out:              files.outR,
		chain:            s.chain,
		addr:             s.addr,
		firstByteTimeout: s.firstByteTimeout,
		dialer:           s.dialer,
		flight:           s.flight,
		exited:           make(chan struct{}),
		stderrDone:       make(chan struct{}),
		outDone:          make(chan struct{}),
		gone:             make(chan struct{}),
	}

	refused := !registerJump(c)
	if refused {
		// The verdict is settled before any goroutine can observe the
		// connection, so neither the reaper nor Close publishes it again.
		c.verdict = verdictTerminal
	}

	go c.copyStderr(files.errR)
	go c.reap()
	go c.track()

	if refused {
		killJumpChild(cmd.Process)
		_ = c.closePipes()

		op := jumpDialError(s.addr, &JumpError{Chain: s.chain, Addr: s.addr, Err: errJumpsShutdown, ExitStatus: -1})
		s.dialer.remember(op)
		s.dialer.publish(s.flight, "start", verdictTerminal)

		return nil, op
	}

	if s.firstByteTimeout > 0 {
		c.mu.Lock()
		c.timer = time.AfterFunc(s.firstByteTimeout, c.fireTimer)
		c.mu.Unlock()
	}

	if err := ctx.Err(); err != nil {
		_ = c.Close()

		return nil, err
	}

	return c, nil
}

// jumpPipes is the six ends of ssh's three standard streams.
type jumpPipes struct {
	inR, inW, outR, outW, errR, errW *os.File
}

func openJumpPipes() (jumpPipes, error) {
	var p jumpPipes

	var err error

	if p.inR, p.inW, err = os.Pipe(); err != nil {
		return jumpPipes{}, fmt.Errorf("create stdin pipe: %w", err)
	}

	if p.outR, p.outW, err = os.Pipe(); err != nil {
		closeAll(p.inR, p.inW)

		return jumpPipes{}, fmt.Errorf("create stdout pipe: %w", err)
	}

	if p.errR, p.errW, err = os.Pipe(); err != nil {
		closeAll(p.inR, p.inW, p.outR, p.outW)

		return jumpPipes{}, fmt.Errorf("create stderr pipe: %w", err)
	}

	return p, nil
}

// closeAll closes every file, discarding errors: each call site is either
// unwinding a failed setup (where the original error is the one worth
// reporting) or handing a descriptor to the child process it just started.
func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// jumpVerdict is a connection's classified outcome before its first byte,
// which is what the single-flight gate waits for.
type jumpVerdict int

const (
	verdictNone jumpVerdict = iota
	verdictEstablished
	verdictTerminal
	verdictNonTerminal
)

// jumpConn adapts an `ssh -W` process to net.Conn: writes go to ssh's stdin
// and reads come from its stdout, which is exactly the forwarded stream.
//
// The child's lifetime belongs to the connection, not to the dial context,
// which the transport detaches from the request anyway. One reaper goroutine
// is the only caller of Wait, one goroutine copies standard error into a
// guarded buffer, and one goroutine ends the registry entry once both are
// done.
type jumpConn struct {
	cmd              *exec.Cmd
	in               *os.File
	out              *os.File
	chain            string
	addr             string
	firstByteTimeout time.Duration
	dialer           *jumpDialer
	flight           *jumpFlight

	stderr jumpStderr

	// firstByte records whether a byte has been read. It is atomic because
	// the transport's read loop writes it while its write loop reads it; it
	// only ever changes under mu, so code holding mu sees it settled.
	firstByte atomic.Bool

	// closed is set once pmx itself has closed the pipes.
	closed atomic.Bool

	exited     chan struct{} // closed by the reaper once Wait has returned
	waitErr    error         // written by the reaper before exited closes
	stderrDone chan struct{} // closed once the standard-error copy reaches end-of-file
	outDone    chan struct{} // closed once a Read of ssh's output fails with anything but a timeout
	gone       chan struct{} // closed once the registry entry has ended

	mu       sync.Mutex
	verdict  jumpVerdict
	timer    *time.Timer
	timerErr error
	exitErr  *JumpError
	exitOp   *net.OpError

	pipesOnce   sync.Once
	pipesErr    error
	closeOnce   sync.Once
	endOnce     sync.Once
	outDoneOnce sync.Once
}

// Read marks the first byte before it reports the end of the stream, so the
// reaper, which waits for outDone, never classifies an exit ahead of a byte
// that ssh forwarded before it exited.
func (c *jumpConn) Read(b []byte) (int, error) {
	n, err := c.out.Read(b)
	if n > 0 && !c.firstByte.Load() {
		c.markFirstByte()
	}

	if err != nil {
		if !isTimeout(err) {
			c.outDoneOnce.Do(func() { close(c.outDone) })
		}

		return n, c.failure("read", err)
	}

	return n, nil
}

func (c *jumpConn) Write(b []byte) (int, error) {
	n, err := c.in.Write(b)
	if err != nil {
		return n, c.failure("write", err)
	}

	return n, nil
}

// markFirstByte records the first byte and publishes the established verdict
// when none was published before it.
func (c *jumpConn) markFirstByte() {
	c.mu.Lock()

	if c.firstByte.Load() {
		c.mu.Unlock()

		return
	}

	c.firstByte.Store(true)

	if c.timer != nil {
		c.timer.Stop()
	}

	first := c.verdict == verdictNone
	if first {
		c.verdict = verdictEstablished
	}

	c.mu.Unlock()

	if first {
		c.dialer.publish(c.flight, "read", verdictEstablished)
	}
}

// failure turns a Read or Write error into what the caller sees, and source
// names the call. A failure is classified by ssh's own verdict and by the
// jump's own timer, never by which call happened to fail: before the first
// byte, an exit with status 255 or a fired timer is terminal and becomes a
// dial OpError, unless the connection already published a non-terminal
// verdict; any other non-zero exit is a bare *JumpError; a clean exit
// returns err unchanged, so a server closing an idle keep-alive connection
// still reads as io.EOF.
func (c *jumpConn) failure(source string, err error) error {
	if isTimeout(err) {
		return err
	}

	c.mu.Lock()
	timerErr := c.timerErr
	c.mu.Unlock()

	if timerErr != nil {
		return timerErr
	}

	if c.closed.Load() && errors.Is(err, os.ErrClosed) {
		return err
	}

	if !c.awaitExit(jumpFailureBudget) {
		// ssh closed the stream but has not exited within the budget, so
		// there is no verdict of its own to report. The raw error is the
		// detail, and the outcome is not terminal.
		c.settleNonTerminal(source)

		return &JumpError{Chain: c.chain, Addr: c.addr, Stderr: c.stderr.text(), Err: err, ExitStatus: -1}
	}

	je, op := c.exitError()

	switch {
	case je.Err == nil && !je.Signaled && je.ExitStatus == 0:
		c.settleNonTerminal(source)

		return err
	case c.isTerminal(je):
		if c.recordTerminal(source, op) {
			return op
		}

		return je
	default:
		c.settleNonTerminal(source)

		return je
	}
}

// awaitExit waits for the reaper and then for the standard-error copy, both
// inside one budget, and reports whether the exit is known.
func (c *jumpConn) awaitExit(budget time.Duration) bool {
	t := time.NewTimer(budget)
	defer t.Stop()

	select {
	case <-c.exited:
	case <-t.C:
		return false
	}

	select {
	case <-c.stderrDone:
	case <-t.C:
	}

	return true
}

// exitError composes the error for a known exit, once, so Read, Write, and
// the reaper all report the same value. The caller must have seen exited
// close.
func (c *jumpConn) exitError() (*JumpError, *net.OpError) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.exitErr == nil {
		je := &JumpError{Chain: c.chain, Addr: c.addr, Stderr: c.stderr.text(), ExitStatus: -1}

		if ps := c.cmd.ProcessState; ps != nil {
			if ps.Exited() {
				je.ExitStatus = ps.ExitCode()
			} else {
				je.Signaled = true
			}
		} else if c.waitErr != nil {
			je.Err = c.waitErr
		}

		c.exitErr = je
		c.exitOp = jumpDialError(c.addr, je)
	}

	return c.exitErr, c.exitOp
}

// isTerminal reports whether a known exit is ssh's own failure before any
// byte arrived: ssh exits 255 when it fails itself, and otherwise exits with
// the forwarded stream's status.
func (c *jumpConn) isTerminal(je *JumpError) bool {
	return !c.firstByte.Load() && je.Err == nil && !je.Signaled && je.ExitStatus == 255
}

// recordTerminal settles a terminal failure that source classified, and it
// reports whether the connection's verdict is terminal afterwards. Only the
// connection's first verdict writes the memo, and it writes it before it
// publishes, so a waiter never finds an empty memo after a terminal verdict.
// A later exit never replaces a verdict that is already out: after the
// timer's verdict the memo keeps the timer's message, and after a close or
// another non-terminal verdict before the first byte, the 255 that ssh
// reports when pmx itself terminates it never becomes a memo.
func (c *jumpConn) recordTerminal(source string, op error) bool {
	c.mu.Lock()

	if c.firstByte.Load() || c.verdict != verdictNone {
		terminal := c.verdict == verdictTerminal
		c.mu.Unlock()

		return terminal
	}

	c.verdict = verdictTerminal
	c.dialer.remember(op)
	c.mu.Unlock()

	c.dialer.publish(c.flight, source, verdictTerminal)

	return true
}

// settleNonTerminal publishes a non-terminal verdict that source settled
// when the connection has none yet, which lets one waiter become the new
// leader.
func (c *jumpConn) settleNonTerminal(source string) {
	c.mu.Lock()

	first := c.verdict == verdictNone
	if first {
		c.verdict = verdictNonTerminal
	}

	c.mu.Unlock()

	if first {
		c.dialer.publish(c.flight, source, verdictNonTerminal)
	}
}

// fireTimer runs when FirstByteTimeout passes with no byte read. When no
// verdict is out yet, it records the terminal failure in the memo first, and
// only then closes the connection and publishes the verdict. It terminates
// the child at once in every case, because a
// child still authenticating ignores closed pipes and, on Windows, closing a
// pipe does not interrupt a blocked Read.
func (c *jumpConn) fireTimer() {
	c.mu.Lock()

	select {
	case <-c.exited:
		// The reaper classifies an exit that came first.
		c.mu.Unlock()

		return
	default:
	}

	if c.firstByte.Load() || c.closed.Load() || c.timerErr != nil {
		c.mu.Unlock()

		return
	}

	je := &JumpError{
		Chain:      c.chain,
		Addr:       c.addr,
		Stderr:     c.stderr.text(),
		TimedOut:   true,
		Timeout:    c.firstByteTimeout,
		ExitStatus: -1,
	}
	op := jumpDialError(c.addr, je)
	c.timerErr = op

	// As in recordTerminal, only the connection's first verdict is memoised.
	first := c.verdict == verdictNone
	if first {
		c.verdict = verdictTerminal
		c.dialer.remember(op)
	}

	c.mu.Unlock()

	_ = c.closePipes()
	terminateJumpChild(c.cmd.Process)

	if first {
		c.dialer.publish(c.flight, "timer", verdictTerminal)
	}

	c.end(true)
}

func (c *jumpConn) stopTimer() {
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
}

// reap is the only caller of Wait. After the exit it classifies an outcome
// that came before the first byte, memoising a terminal one before it
// publishes the verdict.
//
// Before it classifies, it waits, inside one budget, for standard error to
// reach end-of-file and for a reader to drain ssh's output to end-of-file.
// A byte that ssh forwarded just before it exited is then marked as the
// first byte ahead of the classification, so a connection that did carry
// data is never memoised as a dead bastion. With no reader, the budget runs
// out and the exit is classified as it stands.
func (c *jumpConn) reap() {
	defer c.dialer.hookReaped()

	c.waitErr = c.cmd.Wait()
	close(c.exited)
	c.stopTimer()

	if c.firstByte.Load() {
		return
	}

	awaitAll(jumpFailureBudget, c.stderrDone, c.outDone)
	c.dialer.hookClassify()

	je, op := c.exitError()
	if c.isTerminal(je) {
		c.recordTerminal("reaper", op)

		return
	}

	c.settleNonTerminal("reaper")
}

// awaitAll waits up to budget for every channel to close, in turn, and
// reports whether all of them did.
func awaitAll(budget time.Duration, chs ...<-chan struct{}) bool {
	t := time.NewTimer(budget)
	defer t.Stop()

	for _, ch := range chs {
		select {
		case <-ch:
		case <-t.C:
			return false
		}
	}

	return true
}

// copyStderr drains ssh's standard error into the guarded buffer until
// end-of-file, which on Unix means no member of the child's process group
// still holds the pipe. It never stops early, so ssh can never block on a
// full pipe.
func (c *jumpConn) copyStderr(r *os.File) {
	defer close(c.stderrDone)
	defer func() { _ = r.Close() }()

	br := bufio.NewReaderSize(r, jumpStderrLineMax)

	var line []byte

	for {
		chunk, err := br.ReadSlice('\n')
		if room := jumpStderrLineMax - len(line); room > 0 {
			line = append(line, chunk[:min(len(chunk), room)]...)
		}

		switch {
		case err == nil:
			c.stderr.add(line)
			line = line[:0]
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			if len(line) > 0 {
				c.stderr.add(line)
			}

			return
		}
	}
}

// track ends the registry entry once Wait has returned and, where the
// platform can signal a whole group, once standard error has reached
// end-of-file, so a group whose leader exited while a descendant still holds
// the pipe keeps being signalled.
func (c *jumpConn) track() {
	<-c.exited

	if jumpEntryAwaitsStderr {
		<-c.stderrDone
	}

	unregisterJump(c)
	close(c.gone)
}

// closePipes closes both stream pipes once and marks the connection closed.
func (c *jumpConn) closePipes() error {
	c.pipesOnce.Do(func() {
		c.closed.Store(true)

		err := c.in.Close()
		if oerr := c.out.Close(); err == nil {
			err = oerr
		}

		c.pipesErr = err
	})

	return c.pipesErr
}

// Close never blocks, because net/http calls it while holding
// transport-wide locks. It closes both pipes and returns; a goroutine then
// publishes a pending verdict and gives ssh its chance to exit on
// end-of-file before signalling it.
func (c *jumpConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.closePipes()
		c.stopTimer()

		go func() {
			select {
			case <-c.exited:
				// The reaper classifies an exit it has already seen.
			default:
				c.settleNonTerminal("close")
			}

			c.end(false)
		}()
	})

	return c.pipesErr
}

// end escalates once per connection: after the grace (or at once when
// immediate is set) it sends a terminate signal, and after a second grace it
// kills. On Unix both go to the child's whole process group.
func (c *jumpConn) end(immediate bool) {
	c.endOnce.Do(func() {
		grace := c.closeGrace()

		if !immediate && c.waitGone(grace) {
			return
		}

		terminateJumpChild(c.cmd.Process)

		if c.waitGone(grace) {
			return
		}

		killJumpChild(c.cmd.Process)
	})
}

// closeGrace is jumpCloseGrace unless the dialer's test hooks replace it.
func (c *jumpConn) closeGrace() time.Duration {
	if c.dialer != nil && c.dialer.hooks.closeGrace > 0 {
		return c.dialer.hooks.closeGrace
	}

	return jumpCloseGrace
}

func (c *jumpConn) waitGone(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-c.gone:
		return true
	case <-t.C:
		return false
	}
}

func (c *jumpConn) LocalAddr() net.Addr  { return jumpAddr("ssh-jump:" + c.chain) }
func (c *jumpConn) RemoteAddr() net.Addr { return jumpAddr(c.addr) }

func (c *jumpConn) SetDeadline(t time.Time) error {
	err := c.in.SetWriteDeadline(t)
	if rerr := c.out.SetReadDeadline(t); err == nil {
		err = rerr
	}

	return err
}

func (c *jumpConn) SetReadDeadline(t time.Time) error  { return c.out.SetReadDeadline(t) }
func (c *jumpConn) SetWriteDeadline(t time.Time) error { return c.in.SetWriteDeadline(t) }

// jumpStderr keeps the two standard-error lines a JumpError reports: the
// most recent line that says why a channel open failed, which ssh prints at
// INFO level before its fatal line, and the last line.
type jumpStderr struct {
	mu               sync.Mutex
	last             string
	openFailed       string
	lastIsOpenFailed bool
}

// add keeps line after dropping its newline and one trailing carriage
// return, because ssh ends every log line with "\r\n" and a kept "\r" would
// send a terminal's cursor back over the start of the message.
func (s *jumpStderr) add(raw []byte) {
	line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if strings.TrimSpace(line) == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.last = line
	s.lastIsOpenFailed = strings.Contains(line, "open failed:")

	if s.lastIsOpenFailed {
		s.openFailed = line
	}
}

func (s *jumpStderr) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.openFailed != "" && !s.lastIsOpenFailed {
		return s.openFailed + "; " + s.last
	}

	return s.last
}

// jumpRegistry records every live jump child, so pmx can reap them before it
// exits: a background goroutine dies with the process, and pmx leaves through
// os.Exit as soon as Execute returns.
var jumpRegistry = struct {
	mu     sync.Mutex
	closed bool
	live   map[*jumpConn]struct{}
}{live: map[*jumpConn]struct{}{}}

// registerJump adds c to the registry, or reports false when the registry is
// closed, in which case the caller kills the child.
func registerJump(c *jumpConn) bool {
	jumpRegistry.mu.Lock()
	defer jumpRegistry.mu.Unlock()

	if jumpRegistry.closed {
		return false
	}

	jumpRegistry.live[c] = struct{}{}

	return true
}

func unregisterJump(c *jumpConn) {
	jumpRegistry.mu.Lock()
	delete(jumpRegistry.live, c)
	jumpRegistry.mu.Unlock()
}

// closeJumpRegistry marks the registry closed and returns the entries alive
// at that moment. No entry can register after it.
func closeJumpRegistry() []*jumpConn {
	jumpRegistry.mu.Lock()
	defer jumpRegistry.mu.Unlock()

	jumpRegistry.closed = true

	live := make([]*jumpConn, 0, len(jumpRegistry.live))
	for c := range jumpRegistry.live {
		live = append(live, c)
	}

	return live
}

// ShutdownJumps ends every live jump child, and it returns within bound
// plus one second. It first marks the registry closed, so a dial that
// registers its child afterwards kills that child at once and fails
// terminally. It then closes the pipes of every registered connection, sends
// a terminate signal at once to each child that never read a byte, because
// ssh attaches its standard input only after authentication and the channel
// open, gives the others until bound to exit on end-of-file, and kills
// whatever is left at the deadline. After the kill it waits up to one second
// for every entry to end, so a descendant that closes standard error just
// after the kill still leaves the registry before it returns. On Unix every
// signal goes to the child's whole process group. Execute calls it once,
// after its exit record and before it prints the command's error.
func ShutdownJumps(bound time.Duration) {
	deadline := time.Now().Add(bound)
	live := closeJumpRegistry()

	for _, c := range live {
		_ = c.closePipes()

		if !c.firstByte.Load() {
			terminateJumpChild(c.cmd.Process)
		}
	}

	if waitAllGone(live, time.Until(deadline)) {
		return
	}

	for _, c := range live {
		select {
		case <-c.gone:
		default:
			killJumpChild(c.cmd.Process)
		}
	}

	waitAllGone(live, jumpShutdownSettle)
}

// waitAllGone waits up to d for every entry in live to end.
func waitAllGone(live []*jumpConn, d time.Duration) bool {
	t := time.NewTimer(max(d, 0))
	defer t.Stop()

	for _, c := range live {
		select {
		case <-c.gone:
		case <-t.C:
			return false
		}
	}

	return true
}

// KillJumps sends a kill at once to every registered child, to its whole
// process group on Unix, and marks the registry closed. It never waits for
// a reap and never blocks, so a second-signal handler can call it just
// before it re-raises the signal.
func KillJumps() {
	for _, c := range closeJumpRegistry() {
		killJumpChild(c.cmd.Process)
	}
}

// ReopenJumps clears the closed mark that ShutdownJumps and KillJumps set.
// Production code never calls it, because pmx runs Execute once and then
// exits. A test binary runs Execute many times in one process, so a test
// that dials through a jump in a binary that also runs Execute or a
// registry shutdown in-process calls it before it dials.
func ReopenJumps() {
	jumpRegistry.mu.Lock()
	jumpRegistry.closed = false
	jumpRegistry.mu.Unlock()
}

// jumpAddr is the net.Addr a jumpConn reports. There is no local socket to
// name (the connection is a pipe to an ssh process), so this exists to satisfy
// net.Conn and to make a logged address readable.
type jumpAddr string

func (a jumpAddr) Network() string { return "tcp" }
func (a jumpAddr) String() string  { return string(a) }

// isTimeout reports whether err is a deadline expiry rather than a real
// failure, so a timeout is never dressed up in ssh's stderr and never
// settles a verdict. An *os.File deadline expiry is an *fs.PathError that
// wraps os.ErrDeadlineExceeded, and the PathError itself is no net.Error,
// so the check unwraps. io.EOF, the ordinary end of a forwarded stream, is
// never a timeout.
func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}

	var netErr net.Error

	return errors.As(err, &netErr) && netErr.Timeout()
}
