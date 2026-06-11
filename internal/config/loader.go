package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	yaml "github.com/goccy/go-yaml"
)

// strictFingerprintRE matches a colon-separated hex SHA-256 fingerprint as
// produced by Proxmox VE: 32 hex-digit pairs separated by colons (case-insensitive).
var strictFingerprintRE = regexp.MustCompile(`^(?i)[0-9a-f]{2}(?::[0-9a-f]{2}){31}$`)

// DefaultPath returns the canonical config file path.
// Uses $XDG_CONFIG_HOME if set, otherwise falls back to ~/.config.
// Final path: <base>/pve/config.yml
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// Fallback: relative path if home is unavailable.
			return filepath.Join(".config", "pve", "config.yml")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "pve", "config.yml")
}

// Load reads and parses the YAML config file at path.
// If the file does not exist, an empty Config is returned without error.
// Returns an error if the file exists but cannot be read or parsed.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return &cfg, nil
}

// ResolveContext selects and validates a context from cfg.
// If nameOverride is non-empty it is used; otherwise cfg.CurrentContext is used.
// Returns the resolved Context, its canonical name, and any error.
// Applies default values: Port=8006, Protocol="https", Realm="pam".
func ResolveContext(cfg *Config, nameOverride string) (*Context, string, error) {
	if cfg == nil {
		return nil, "", errors.New("config is nil")
	}

	name := nameOverride
	if name == "" {
		name = cfg.CurrentContext
	}
	if name == "" {
		return nil, "", errors.New("no context specified and no current-context set in config")
	}

	if cfg.Contexts == nil {
		return nil, "", fmt.Errorf("context %q not found: config has no contexts", name)
	}

	ctx, ok := cfg.Contexts[name]
	if !ok {
		return nil, "", fmt.Errorf("context %q not found in config", name)
	}
	if ctx == nil {
		return nil, "", fmt.Errorf("context %q is nil in config", name)
	}

	// Apply defaults before validation.
	applyDefaults(ctx)

	if err := validateContext(ctx); err != nil {
		return nil, "", fmt.Errorf("context %q: %w", name, err)
	}

	return ctx, name, nil
}

// ApplyDefaults is the exported form of applyDefaults.  CLI packages that
// construct or edit a Context struct independently of ResolveContext call this
// to ensure Port, Protocol, and Realm are populated with standard values.
func ApplyDefaults(c *Context) {
	applyDefaults(c)
}

// applyDefaults fills in missing optional fields with standard values.
func applyDefaults(c *Context) {
	if c.Port == 0 {
		c.Port = 8006
	}
	if c.Protocol == "" {
		c.Protocol = "https"
	}
	if c.Realm == "" {
		c.Realm = "pam"
	}
}

// ValidateContext is the exported form of validateContext.  It applies the
// same leniency as the load-time check: token auth requires only a secret,
// not a token-id.  This is intentionally lenient so that CLI startup
// (ResolveContext) does not hard-fail on existing configs written before
// token-id became mandatory on write paths.
//
// Write paths (add, edit) and the validate verb call StrictValidateContext
// instead to enforce the fuller rule set.
func ValidateContext(c *Context) error {
	return validateContext(c)
}

// StrictValidateContext runs the full write-time validation that all
// writable paths (context add, context edit) and the validate verb enforce.
// Rules beyond validateContext:
//   - token auth: token-id must be present (in addition to secret).
//   - default-output, if set, must be one of: table, plain, json, yaml.
//   - fingerprint, if set, must match the colon-separated hex SHA-256 pattern.
//   - port, if non-zero, must be in [1, 65535].
//   - protocol, if non-empty, must be "https" or "http".
//
// Keeping this separate from validateContext preserves load-time leniency:
// tightening validateContext would break CLI startup for contexts written
// before token-id was required, blocking unrelated commands on users who
// have not yet updated their config.
func StrictValidateContext(c *Context) []string {
	var errs []string

	if c.Host == "" {
		errs = append(errs, "host is required")
	}

	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		errs = append(errs, fmt.Sprintf("port %d is out of range [1, 65535]", c.Port))
	}

	if c.Protocol != "" && c.Protocol != "https" && c.Protocol != "http" {
		errs = append(errs, fmt.Sprintf("protocol %q must be \"https\" or \"http\"", c.Protocol))
	}

	switch c.Auth.Type {
	case "token":
		if c.Auth.TokenID == "" {
			errs = append(errs, "auth.token-id is required for token auth")
		}
		if c.Auth.Secret == "" {
			errs = append(errs, "auth.secret is required for token auth")
		}
	case "password":
		if c.Auth.Username == "" {
			errs = append(errs, "auth.username is required for password auth")
		}
		if c.Auth.Secret == "" {
			errs = append(errs, "auth.secret is required for password auth")
		}
	case "":
		errs = append(errs, "auth.type is required")
	default:
		errs = append(errs, fmt.Sprintf("auth.type %q must be \"token\" or \"password\"", c.Auth.Type))
	}

	if c.DefaultOutput != "" {
		switch c.DefaultOutput {
		case "table", "plain", "json", "yaml":
		default:
			errs = append(errs, fmt.Sprintf(
				"default-output %q must be one of: table, plain, json, yaml",
				c.DefaultOutput,
			))
		}
	}

	if c.TLS.Fingerprint != "" && !strictFingerprintRE.MatchString(c.TLS.Fingerprint) {
		errs = append(errs, fmt.Sprintf(
			"fingerprint %q must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)",
			c.TLS.Fingerprint,
		))
	}

	return errs
}

// validateContext checks that mandatory fields are present and auth type is
// recognised.  It is intentionally lenient: token auth requires only a secret,
// not a token-id.  This leniency preserves CLI startup compatibility for
// configs written before token-id was required on write paths.  Write paths
// call StrictValidateContext instead.
func validateContext(c *Context) error {
	if c.Host == "" {
		return errors.New("host is required")
	}

	switch c.Auth.Type {
	case "token":
		if c.Auth.Secret == "" {
			return errors.New("auth.secret is required for token auth")
		}
	case "password":
		if c.Auth.Username == "" {
			return errors.New("auth.username is required for password auth")
		}
		if c.Auth.Secret == "" {
			return errors.New("auth.secret is required for password auth")
		}
	default:
		return fmt.Errorf("auth.type must be \"token\" or \"password\", got %q", c.Auth.Type)
	}

	return nil
}
