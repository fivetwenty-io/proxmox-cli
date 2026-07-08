// Package config provides types, loader, writer, and secret resolver
// for the pmx CLI configuration file (~/.config/pmx/config.yml).
package config

import "slices"

// Product identifies which Proxmox product a context targets.
const (
	// ProductPVE targets Proxmox VE (the default when Product is empty).
	ProductPVE = "pve"

	// ProductPBS targets Proxmox Backup Server.
	ProductPBS = "pbs"

	// ProductPDM targets Proxmox Datacenter Manager.
	ProductPDM = "pdm"
)

// Config is the top-level configuration file struct.
type Config struct {
	// CurrentContext is the name of the active context used when --context is not specified.
	CurrentContext string `yaml:"current-context"`

	// PreviousContext is the name of the last active context, used by `pmx context previous`.
	PreviousContext string `yaml:"previous-context,omitempty"`

	// DefaultOutput is the default output format (table|ascii|plain|json|yaml) for all commands.
	DefaultOutput string `yaml:"default-output"`

	// Contexts is the named map of PVE endpoint configurations.
	Contexts map[string]*Context `yaml:"contexts"`
}

// Context represents one named Proxmox VE API endpoint.
type Context struct {
	// Host is the hostname or IP address of the PVE node (required).
	Host string `yaml:"host"`

	// Port is the HTTPS API port (default 8006).
	Port int `yaml:"port"`

	// Protocol is the connection scheme; "https" or "http" (default "https").
	Protocol string `yaml:"protocol"`

	// Realm is the PVE authentication realm (default "pam").
	Realm string `yaml:"realm"`

	// DefaultNode is the PVE node name to use when --node is not specified.
	DefaultNode string `yaml:"default-node,omitempty"`

	// DefaultOutput overrides the global default output format for this context.
	DefaultOutput string `yaml:"default-output,omitempty"`

	// Auth holds the credential block for this context.
	Auth AuthBlock `yaml:"auth"`

	// TLS holds TLS verification settings.
	TLS TLSBlock `yaml:"tls,omitempty"`

	// SSH holds per-context defaults for the `pmx ssh` and `pmx rsync` commands.
	SSH SSHBlock `yaml:"ssh,omitempty"`

	// Product selects which Proxmox product this context targets: "pve" or
	// "pbs". Empty means "pve" (backward compatible with configs written
	// before Product existed).
	Product string `yaml:"product,omitempty"`
}

// IsPBS reports whether c targets Proxmox Backup Server. Empty Product
// (backward-compat configs) is treated as ProductPVE, so IsPBS returns false.
func (c *Context) IsPBS() bool {
	return c.Product == ProductPBS
}

// Products enumerates every supported product identifier, in display order.
func Products() []string {
	return []string{ProductPVE, ProductPBS, ProductPDM}
}

// IsValidProduct reports whether product names a supported product.
func IsValidProduct(product string) bool {
	return slices.Contains(Products(), product)
}

// DefaultPortForProduct returns the API port a product listens on by default.
func DefaultPortForProduct(product string) int {
	switch product {
	case ProductPBS:
		return 8007
	case ProductPDM:
		return 8443
	default:
		return 8006
	}
}

// AuthBlock holds credential configuration for a context.
// Secrets may be env refs (${VAR} or $VAR), keychain refs (keychain:path), or literals.
type AuthBlock struct {
	// Type is the authentication method: "token" or "password".
	Type string `yaml:"type"`

	// Username is the PVE username (e.g. "root@pam").
	Username string `yaml:"username,omitempty"`

	// TokenID is the API token identifier (e.g. "mytoken"), used when Type is "token".
	TokenID string `yaml:"token-id,omitempty"`

	// Secret is the token value or password; may be ${VAR}, $VAR, keychain:path, or literal.
	Secret string `yaml:"secret"`

	// Session holds a live ticket+CSRF pair stored after password login.
	Session *Session `yaml:"session,omitempty"`
}

// TLSBlock holds TLS verification settings for a context.
type TLSBlock struct {
	// Insecure disables TLS certificate verification when true.
	Insecure bool `yaml:"insecure"`

	// Fingerprint is the expected TLS certificate fingerprint (hex SHA-256).
	Fingerprint string `yaml:"fingerprint"`

	// CACert is the path to a PEM-encoded CA certificate file for custom TLS trust.
	CACert string `yaml:"ca-cert"`

	// Tofu opts this context into Trust-On-First-Use certificate fingerprint
	// pinning: on an interactive terminal, a certificate whose fingerprint is
	// not already trusted is shown to the operator (host + fingerprint) for a
	// one-time accept/reject decision, and an accepted fingerprint is persisted
	// per context so later connections do not prompt again. A non-interactive
	// invocation always rejects an unknown certificate outright (no prompt, no
	// blocking read). Default false preserves the original CA-chain-only
	// verification behavior unchanged; Tofu is ignored entirely when Insecure
	// is true, since that already disables certificate verification.
	Tofu bool `yaml:"tofu"`
}

// SSHBlock holds per-context defaults for the `pmx ssh` and `pmx rsync`
// commands. A zero value for any field means "not set": the command falls
// back to its own compiled-in default (user "root", port 22) rather than to
// an empty/zero override. Precedence at the command level is always
// explicit flag > SSHBlock value > compiled-in default.
type SSHBlock struct {
	// User is the default SSH login user for this context (falls back to "root").
	User string `yaml:"user,omitempty"`

	// Port is the default SSH port for this context (falls back to 22).
	Port int `yaml:"port,omitempty"`

	// Identity is the default path to an SSH private key (identity) file for this context.
	Identity string `yaml:"identity,omitempty"`
}

// Session holds a live ticket and CSRF token obtained after password login.
// Stored atomically in the config file; wiped on logout.
type Session struct {
	// Ticket is the PVE authentication cookie value.
	Ticket string `yaml:"ticket"`

	// CSRF is the PVECSRFPreventionToken header value.
	CSRF string `yaml:"csrf"`

	// ExpiresAt is the session expiry as a Unix timestamp (seconds).
	ExpiresAt int64 `yaml:"expires-at"`
}
