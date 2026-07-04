// Package config provides types, loader, writer, and secret resolver
// for the pve CLI configuration file (~/.config/pve/config.yml).
package config

// Config is the top-level configuration file struct.
type Config struct {
	// CurrentContext is the name of the active context used when --context is not specified.
	CurrentContext string `yaml:"current-context"`

	// PreviousContext is the name of the last active context, used by `pve context previous`.
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
