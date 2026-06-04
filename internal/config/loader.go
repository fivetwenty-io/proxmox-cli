package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	yaml "github.com/goccy/go-yaml"
)

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

// ResolveTarget selects and validates a target from cfg.
// If nameOverride is non-empty it is used; otherwise cfg.CurrentTarget is used.
// Returns the resolved Target, its canonical name, and any error.
// Applies default values: Port=8006, Protocol="https", Realm="pam".
func ResolveTarget(cfg *Config, nameOverride string) (*Target, string, error) {
	if cfg == nil {
		return nil, "", errors.New("config is nil")
	}

	name := nameOverride
	if name == "" {
		name = cfg.CurrentTarget
	}
	if name == "" {
		return nil, "", errors.New("no target specified and no current-target set in config")
	}

	if cfg.Targets == nil {
		return nil, "", fmt.Errorf("target %q not found: config has no targets", name)
	}

	target, ok := cfg.Targets[name]
	if !ok {
		return nil, "", fmt.Errorf("target %q not found in config", name)
	}
	if target == nil {
		return nil, "", fmt.Errorf("target %q is nil in config", name)
	}

	// Apply defaults before validation.
	applyDefaults(target)

	if err := validateTarget(target); err != nil {
		return nil, "", fmt.Errorf("target %q: %w", name, err)
	}

	return target, name, nil
}

// applyDefaults fills in missing optional fields with standard values.
func applyDefaults(t *Target) {
	if t.Port == 0 {
		t.Port = 8006
	}
	if t.Protocol == "" {
		t.Protocol = "https"
	}
	if t.Realm == "" {
		t.Realm = "pam"
	}
}

// validateTarget checks that mandatory fields are present and auth type is recognised.
func validateTarget(t *Target) error {
	if t.Host == "" {
		return errors.New("host is required")
	}

	switch t.Auth.Type {
	case "token":
		if t.Auth.Secret == "" {
			return errors.New("auth.secret is required for token auth")
		}
	case "password":
		if t.Auth.Username == "" {
			return errors.New("auth.username is required for password auth")
		}
		if t.Auth.Secret == "" {
			return errors.New("auth.secret is required for password auth")
		}
	default:
		return fmt.Errorf("auth.type must be \"token\" or \"password\", got %q", t.Auth.Type)
	}

	return nil
}
