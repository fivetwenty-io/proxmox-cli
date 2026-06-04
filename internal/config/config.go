// Package config provides types, loader, writer, and secret resolver
// for the pve CLI configuration file (~/.config/pve/config.yml).
package config

// Config is the top-level configuration file struct.
type Config struct {
	// CurrentTarget is the name of the active target used when --target is not specified.
	CurrentTarget string `yaml:"current-target"`

	// DefaultOutput is the default output format (table|plain|json|yaml) for all commands.
	DefaultOutput string `yaml:"default-output"`

	// Targets is the named map of PVE endpoint configurations.
	Targets map[string]*Target `yaml:"targets"`
}

// Target represents one named Proxmox VE API endpoint.
type Target struct {
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

	// Auth holds the credential block for this target.
	Auth AuthBlock `yaml:"auth"`

	// TLS holds TLS verification settings.
	TLS TLSBlock `yaml:"tls,omitempty"`

	// DefaultOutput overrides the global default output format for this target.
	DefaultOutput string `yaml:"default-output,omitempty"`
}

// AuthBlock holds credential configuration for a target.
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

// TLSBlock holds TLS verification settings for a target.
type TLSBlock struct {
	// Insecure disables TLS certificate verification when true.
	Insecure bool `yaml:"insecure"`

	// Fingerprint is the expected TLS certificate fingerprint (hex SHA-256).
	Fingerprint string `yaml:"fingerprint"`

	// CACert is the path to a PEM-encoded CA certificate file for custom TLS trust.
	CACert string `yaml:"ca-cert"`
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
