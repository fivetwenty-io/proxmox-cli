package context

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// probeResult carries the outcome of one live --connect probe.
type probeResult struct {
	// Reachable is true when the version endpoint answered over TLS/HTTP.
	Reachable bool

	// ReachErr holds the transport error when Reachable is false.
	ReachErr string

	// ProductGuess is the product the endpoint identified itself as via its
	// Server response header ("pve", "pbs", or "pdm"), or "" when the header
	// gave no reliable signal. The probe never guesses beyond the header.
	ProductGuess string
}

// probeContext performs the live half of `context validate --connect`: an
// unauthenticated GET of /api2/json/version, which every Proxmox product
// serves without credentials. It uses a bare http.Client built from the
// context fields — never a product API client — so the validate verb keeps
// its noClient annotation. ctx must already have defaults applied
// (config.ApplyDefaults) so Port and Protocol are populated.
//
// TLS trust mirrors what a real API call would do rather than a subset of it.
// insecure is the merged flag-or-context value the rest of the CLI computes,
// and a pinned tls.fingerprint is honored via a certificate pin. Probing with
// stock x509 verification alone reported a correctly pinned context — the
// normal shape for Proxmox, whose certificates are self-signed, and the shape
// `pmx lab` mints automatically — as unreachable. The one lever that made
// validate pass was then setting tls.insecure, so a diagnostic talked
// operators into permanently disabling verification for every real call that
// context made.
func probeContext(ctx *config.Context, timeout time.Duration, insecure bool) probeResult {
	url := fmt.Sprintf("%s://%s:%d/api2/json/version", ctx.Protocol, ctx.Host, ctx.Port)

	//nolint:gosec // G402: InsecureSkipVerify is the caller's explicit opt-in, warned about at the call site
	tlsCfg := &tls.Config{InsecureSkipVerify: insecure}
	if !insecure && ctx.TLS.Fingerprint != "" {
		// Pin instead of trusting the system roots: a self-signed Proxmox
		// certificate never chains to one, so this is the only way a pinned
		// context probes the same way it connects.
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // G402: replaced by the pin check below
		tlsCfg.VerifyPeerCertificate = fingerprintVerifier(ctx.TLS.Fingerprint)
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: tlsCfg,
		},
	}

	//nolint:noctx // bounded by client.Timeout; no request context to inherit in a config-only verb
	resp, err := client.Get(url)
	if err != nil {
		return probeResult{Reachable: false, ReachErr: err.Error()}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // drain for connection reuse
		_ = resp.Body.Close()
	}()

	return probeResult{
		Reachable:    true,
		ProductGuess: productFromServerHeader(resp.Header.Get("Server")),
	}
}

// fingerprintVerifier returns a tls.Config.VerifyPeerCertificate that accepts
// the peer only when its leaf certificate's SHA-256 matches want. Comparison
// is case-insensitive and ignores colons, so a fingerprint copied from the
// PVE UI, from `pvenode cert info`, or from a pmx-written context all compare
// equal.
func fingerprintVerifier(want string) func([][]byte, [][]*x509.Certificate) error {
	normalize := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(s, ":", ""))
	}
	wantNorm := normalize(want)

	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("tls fingerprint pin: peer presented no certificate")
		}
		sum := sha256.Sum256(rawCerts[0])
		got := hex.EncodeToString(sum[:])
		if got != wantNorm {
			return fmt.Errorf("tls fingerprint pin: peer certificate is %s, context pins %s", got, wantNorm)
		}
		return nil
	}
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
