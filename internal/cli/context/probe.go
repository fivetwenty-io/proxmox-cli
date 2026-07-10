package context

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fivetwenty-io/pmx-cli/internal/config"
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
// its noClient annotation. TLS verification honors the context's
// tls.insecure flag. ctx must already have defaults applied
// (config.ApplyDefaults) so Port and Protocol are populated.
func probeContext(ctx *config.Context, timeout time.Duration) probeResult {
	url := fmt.Sprintf("%s://%s:%d/api2/json/version", ctx.Protocol, ctx.Host, ctx.Port)

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			//nolint:gosec // G402: honors the context's explicit tls.insecure opt-in, mirroring the API clients
			TLSClientConfig: &tls.Config{InsecureSkipVerify: ctx.TLS.Insecure},
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
