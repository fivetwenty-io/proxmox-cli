package config

import (
	"fmt"
	"net"
	"strings"
)

// LabContextInput carries the product-neutral values a lab-derived context is
// built or refreshed from. It is deliberately free of any keychain/product
// specifics: the caller decides how Secret is stored (a keychain: reference on
// macOS, a literal secret otherwise) before handing it here, so Project 2 can
// reuse UpsertLabContext for PBS/PDM by supplying a different Port, Username,
// and Secret without changing this package.
type LabContextInput struct {
	// Host is the API host (node 0's mgmt IP for a PVE lab).
	Host string
	// Port is the API port (8006 for PVE).
	Port int
	// Username is the token owner (e.g. "pmx@pve").
	Username string
	// TokenID is the token name only (e.g. "pmx"), never the full user!name.
	TokenID string
	// Secret is the resolved secret reference to persist: a "keychain:..."
	// reference, a "${VAR}" env reference, or a literal.
	Secret string
	// Fingerprint is the colon-hex SHA-256 of the API cert, or "" to leave TLS
	// trust unpinned.
	Fingerprint string
	// DefaultNode is the nested node's PVE hostname; set only on fresh create.
	DefaultNode string
	// MgmtSubnet is the lab's mgmt /24 in CIDR form, used by the ownership
	// guard to recognise a lab-derived host. Empty disables the subnet check
	// (the username check still applies).
	MgmtSubnet string
}

// UpsertLabContext creates or updates the lab context named name in cfg from
// in, and returns the list of changed field labels for logging. It mutates
// cfg.Contexts in memory only; the caller persists with config.Save.
//
// Absent context: a fresh token context is built (product pve, https), passed
// through ApplyDefaults and StrictValidateContext, and inserted.
//
// Present context: an ownership guard runs first (see labContextOwned) so an
// unrelated user context that merely happens to share the lab-<name> name is
// never clobbered. When owned, the credential triple, host/port, and TLS
// fingerprint are overwritten while every field the operator may have
// hand-edited (DefaultNode, DefaultOutput, SSH, TLS.Tofu/Insecure/CACert) is
// preserved. The fingerprint is left untouched when the context opts into a
// different trust model (TLS.Insecure or a TLS.CACert path).
func UpsertLabContext(cfg *Config, name string, in LabContextInput) ([]string, error) {
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]*Context{}
	}

	existing, present := cfg.Contexts[name]
	if !present || existing == nil {
		ctx := &Context{
			Host:        in.Host,
			Port:        in.Port,
			Protocol:    "https",
			Product:     ProductPVE,
			DefaultNode: in.DefaultNode,
			Auth: AuthBlock{
				Type:     "token",
				Username: in.Username,
				TokenID:  in.TokenID,
				Secret:   in.Secret,
			},
			TLS: TLSBlock{Fingerprint: in.Fingerprint},
		}
		ApplyDefaults(ctx)
		if errs := StrictValidateContext(ctx); len(errs) > 0 {
			return nil, fmt.Errorf("invalid lab context %q: %s", name, strings.Join(errs, "; "))
		}
		cfg.Contexts[name] = ctx
		return []string{"host", "port", "username", "token-id", "secret", "fingerprint", "default-node"}, nil
	}

	if !labContextOwned(existing, in.MgmtSubnet) {
		return nil, fmt.Errorf(
			"refusing to overwrite existing context %q: it does not look lab-derived "+
				"(product=%s host=%s username=%s); rename or remove it first",
			name, existing.ProductOrDefault(), existing.Host, existing.Auth.Username)
	}

	var changed []string
	if existing.Host != in.Host {
		existing.Host = in.Host
		changed = append(changed, "host")
	}
	if existing.Port != in.Port {
		existing.Port = in.Port
		changed = append(changed, "port")
	}
	existing.Auth.Type = "token"
	if existing.Auth.Username != in.Username {
		existing.Auth.Username = in.Username
		changed = append(changed, "username")
	}
	if existing.Auth.TokenID != in.TokenID {
		existing.Auth.TokenID = in.TokenID
		changed = append(changed, "token-id")
	}
	if existing.Auth.Secret != in.Secret {
		existing.Auth.Secret = in.Secret
		changed = append(changed, "secret")
	}
	// Preserve a deliberately chosen alternative trust model; only pin the
	// fingerprint when the context relies on default CA-chain verification.
	if !existing.TLS.Insecure && existing.TLS.CACert == "" && in.Fingerprint != "" &&
		existing.TLS.Fingerprint != in.Fingerprint {
		existing.TLS.Fingerprint = in.Fingerprint
		changed = append(changed, "fingerprint")
	}

	ApplyDefaults(existing)
	if errs := StrictValidateContext(existing); len(errs) > 0 {
		return nil, fmt.Errorf("invalid lab context %q after update: %s", name, strings.Join(errs, "; "))
	}
	return changed, nil
}

// labContextOwned reports whether an existing context named lab-<name> looks
// like one this machinery created (and may therefore safely overwrite in
// place), rather than an unrelated user context that happens to share the
// name. It is owned when it targets PVE AND either its token owner is the
// well-known lab user "pmx@pve" OR its host falls inside the lab's mgmt
// subnet. An empty mgmtSubnet disables the subnet check.
func labContextOwned(c *Context, mgmtSubnet string) bool {
	if c.ProductOrDefault() != ProductPVE {
		return false
	}
	if c.Auth.Username == "pmx@pve" {
		return true
	}
	if mgmtSubnet != "" {
		if _, cidr, err := net.ParseCIDR(mgmtSubnet); err == nil {
			if ip := net.ParseIP(c.Host); ip != nil && cidr.Contains(ip) {
				return true
			}
		}
	}
	return false
}
