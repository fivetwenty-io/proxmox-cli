package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	yaml "github.com/goccy/go-yaml"
)

// strictFingerprintRE matches a colon-separated hex SHA-256 fingerprint as
// produced by Proxmox VE: 32 hex-digit pairs separated by colons (case-insensitive).
var strictFingerprintRE = regexp.MustCompile(`^(?i)[0-9a-f]{2}(?::[0-9a-f]{2}){31}$`)

// DefaultPath returns the canonical config file path.
// Uses $XDG_CONFIG_HOME if set, otherwise falls back to ~/.config.
// Final path: <base>/pmx/config.yml
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// Fallback: relative path if home is unavailable.
			return filepath.Join(".config", "pmx", "config.yml")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "pmx", "config.yml")
}

// Load reads and parses the YAML config file at path.
// If the file does not exist, an empty Config is returned without error.
// Returns an error if the file exists but cannot be read or parsed.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is caller-resolved XDG/home config file, not untrusted input
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
// Applies default values: Product="pve", Port=8006 (8007 for Product="pbs", 8443 for Product="pdm"),
// Protocol="https", Realm="pam".
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
		available := ContextNamesWithProducts(cfg)
		if len(available) == 0 {
			return nil, "", fmt.Errorf("context %q not found: config has no contexts", name)
		}
		return nil, "", fmt.Errorf("context %q not found; available: %s",
			name, strings.Join(available, ", "))
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
// to ensure Product, Port, Protocol, and Realm are populated with standard
// values.
func ApplyDefaults(c *Context) {
	applyDefaults(c)
}

// defaultPortForProduct returns the standard API port for product, delegating
// to the exported DefaultPortForProduct.
func defaultPortForProduct(product string) int {
	return DefaultPortForProduct(product)
}

// applyDefaults fills in missing optional fields with standard values.
// Product is defaulted first since the Port default is product-aware.
func applyDefaults(c *Context) {
	if c.Product == "" {
		c.Product = ProductPVE
	}
	if c.Port == 0 {
		c.Port = defaultPortForProduct(c.Product)
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
//   - product, if set, must be one of the supported products (pve, pbs, pdm).
//   - default-output, if set, must be one of: table, ascii, plain, json, yaml.
//   - fingerprint, if set, must match the colon-separated hex SHA-256 pattern.
//   - port, if non-zero, must be in [1, 65535].
//   - protocol, if non-empty, must be "https" or "http".
//   - ssh.port, if non-zero, must be in [1, 65535].
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

	if c.Product != "" && !IsValidProduct(c.Product) {
		errs = append(errs, fmt.Sprintf(
			"product %q must be %s", c.Product, strings.Join(Products(), ", ")))
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
		// The Proxmox API token header is USER@REALM!TOKENID=SECRET, so a token
		// context needs a username (the USER@REALM part) in addition to the token
		// name. Without it the client sends "@realm!tokenid=secret" and the API
		// answers 401.
		if c.Auth.Username == "" {
			errs = append(errs, "auth.username is required for token auth (user@realm, e.g. root@pam)")
		}
		// The "!token-name" belongs in auth.token-id, never in the username; a "!"
		// here means the full token id was pasted into the wrong field.
		if strings.Contains(c.Auth.Username, "!") {
			errs = append(errs,
				`auth.username must not contain "!"; put the token name in auth.token-id, not the username`)
		}
		// auth.token-id is the token NAME only; "@" or "!" means a full user@realm!name
		// identifier landed here instead of being split across username and token-id.
		if strings.ContainsAny(c.Auth.TokenID, "@!") {
			errs = append(errs,
				`auth.token-id must be the token name only (no "@" or "!"); put user@realm in auth.username`)
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
		case "table", "ascii", "plain", "json", "yaml":
		default:
			errs = append(errs, fmt.Sprintf(
				"default-output %q must be one of: table, ascii, plain, json, yaml",
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

	if c.SSH.Port != 0 && (c.SSH.Port < 1 || c.SSH.Port > 65535) {
		errs = append(errs, fmt.Sprintf("ssh.port %d is out of range [1, 65535]", c.SSH.Port))
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

// inlineLabProvenance is the provenance label recorded for a lab defined
// directly under cfg.Labs, as opposed to one loaded from an included file.
const inlineLabProvenance = "config.yml (inline)"

// ResolveLabs merges cfg's inline Labs map with the labs loaded from
// cfg.Include globs and cfg.LabsDir (sugar for one more include glob) into a
// single flat map keyed by lab name. configPath is the config file's own
// path: relative globs are resolved against its directory, and it is the
// file stat'd for the 0600 enforcement below.
//
// Resolution order:
//  1. Inline cfg.Labs seeds the result; an entry's Name defaults to its map
//     key when empty.
//  2. cfg.LabsDir, when non-empty, becomes one more include glob
//     (filepath.Join(cfg.LabsDir, "*.yaml")) — pure sugar, same code path as
//     an explicit entry in cfg.Include.
//  3. Every glob (relative ones resolved against configPath's directory,
//     absolute ones used as-is) is expanded with filepath.Glob; a glob
//     matching zero files is not an error. Each matched file is parsed as a
//     bare single-lab YAML document — the file body IS the Lab, not a
//     wrapping Config — and named from an explicit top-level `name:` key
//     in the file, else the filename stem.
//  4. Every lab from every source is merged into one flat map keyed by
//     name. A file matched by more than one glob (e.g. labs_dir overlapping
//     an include entry) is loaded once — same-file matches are deduplicated
//     by canonical path, not treated as duplicates. A duplicate name across
//     ANY pair of distinct sources (inline vs file, file vs file) is a hard
//     error naming both provenances; the merge never silently keeps the
//     last-seen definition.
//
// Finally, if cfg.DefaultUserPassword is non-empty, configPath is stat'd: a
// mode with any group or other bit set is a hard error telling the operator
// to chmod 0600 the file. When no secret is configured, no stat is
// performed and no error or warning is produced — this is a library
// function, not a place to print to the terminal.
func ResolveLabs(cfg *Config, configPath string) (map[string]*Lab, error) {
	if cfg == nil {
		return nil, errors.New("resolve labs: config is nil")
	}

	result := make(map[string]*Lab, len(cfg.Labs))
	provenance := make(map[string]string, len(cfg.Labs))

	for key, lab := range cfg.Labs {
		if lab == nil {
			return nil, fmt.Errorf("inline lab %q is nil in config.yml", key)
		}

		name := lab.Name
		if name == "" {
			name = key
		}

		if existing, ok := provenance[name]; ok {
			return nil, fmt.Errorf("duplicate lab %q in %s and %s", name, existing, inlineLabProvenance)
		}

		// Copy so callers that keep a reference to cfg.Labs after this call
		// never observe a Name we defaulted on their behalf.
		labCopy := *lab
		labCopy.Name = name
		result[name] = &labCopy
		provenance[name] = inlineLabProvenance
	}

	globs := make([]string, 0, len(cfg.Include)+1)
	globs = append(globs, cfg.Include...)
	if cfg.LabsDir != "" {
		globs = append(globs, filepath.Join(cfg.LabsDir, "*.yaml"))
	}

	baseDir := filepath.Dir(configPath)

	// One file can match several globs (labs_dir is sugar for an include
	// entry, so an explicit include of the same directory overlaps it);
	// load each distinct file exactly once.
	seenFiles := make(map[string]bool)

	for _, pattern := range globs {
		resolvedPattern := pattern
		if !filepath.IsAbs(resolvedPattern) {
			resolvedPattern = filepath.Join(baseDir, resolvedPattern)
		}

		matches, err := filepath.Glob(resolvedPattern)
		if err != nil {
			return nil, fmt.Errorf("expand lab include glob %q: %w", pattern, err)
		}

		for _, file := range matches {
			canonical := file
			if abs, absErr := filepath.Abs(file); absErr == nil {
				canonical = abs
			}
			if seenFiles[canonical] {
				continue
			}
			seenFiles[canonical] = true

			lab, err := loadLabFile(file)
			if err != nil {
				return nil, err
			}

			name := lab.Name
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
			}
			lab.Name = name

			if existing, ok := provenance[name]; ok {
				return nil, fmt.Errorf("duplicate lab %q in %s and %s", name, existing, file)
			}
			result[name] = lab
			provenance[name] = file
		}
	}

	if cfg.DefaultUserPassword != "" {
		info, err := os.Stat(configPath)
		if err != nil {
			return nil, fmt.Errorf("stat config %s: %w", configPath, err)
		}
		// groupWorldDirMask (writer.go) is the same group/other rwx mask
		// regardless of whether the target is a directory or, as here, the
		// config file itself.
		if info.Mode().Perm()&groupWorldDirMask != 0 {
			return nil, fmt.Errorf(
				"config file %s is group- or world-accessible (%04o) but sets default_user_password; chmod 0600 %s",
				configPath, info.Mode().Perm(), configPath)
		}
	}

	applyVnetIDDefaults(result)

	if err := validateVnetIDUniqueness(result); err != nil {
		return nil, err
	}

	if err := validateAllTopologies(result); err != nil {
		return nil, err
	}

	return result, nil
}

// applyVnetIDDefaults fills in Network.VnetID for every lab in labs whose
// config left it empty, using DeriveVnetID(name). A lab that sets
// network.vnet_id explicitly keeps that value verbatim, even if it diverges
// from what DeriveVnetID(name) would produce — explicit config always wins
// over the derived default.
func applyVnetIDDefaults(labs map[string]*Lab) {
	for name, lab := range labs {
		if lab.Network.VnetID == "" {
			lab.Network.VnetID = DeriveVnetID(name)
		}
	}
}

// validateVnetIDUniqueness reports an error naming every colliding pair when
// two or more labs in labs resolve to the same effective Network.VnetID
// (after applyVnetIDDefaults has already filled in any derived defaults).
// PVE vnet IDs must be unique cluster-wide; a collision here would silently
// make two labs share one vnet the moment either is created.
func validateVnetIDUniqueness(labs map[string]*Lab) error {
	byVnetID := make(map[string][]string, len(labs))
	for name, lab := range labs {
		byVnetID[lab.Network.VnetID] = append(byVnetID[lab.Network.VnetID], name)
	}

	names := make([]string, 0, len(byVnetID))
	for vnetID := range byVnetID {
		names = append(names, vnetID)
	}
	sort.Strings(names)

	for _, vnetID := range names {
		labNames := byVnetID[vnetID]
		if len(labNames) < 2 {
			continue
		}
		sort.Strings(labNames)
		return fmt.Errorf(
			"vnet ID %q collides across labs %s: set network.vnet_id explicitly on one of them to disambiguate",
			vnetID, strings.Join(labNames, ", "))
	}

	return nil
}

// validateAllTopologies runs ValidateTopology against every lab in labs and
// returns a single combined error naming every issue found across every lab,
// or nil when every lab's topology is valid.
func validateAllTopologies(labs map[string]*Lab) error {
	names := make([]string, 0, len(labs))
	for name := range labs {
		names = append(names, name)
	}
	sort.Strings(names)

	var issues []string
	for _, name := range names {
		issues = append(issues, ValidateTopology(name, labs[name].Topology)...)
	}

	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("invalid lab topology:\n  %s", strings.Join(issues, "\n  "))
}

// loadLabFile reads and parses path as a bare single-lab YAML document: the
// file body is one Lab, not a Config wrapping a labs map.
//
// Two checks guard against a silently-accepted phantom lab:
//   - The document must decode to a non-empty mapping. An empty file, a
//     whitespace- or comment-only file, and an explicit "{}" all decode
//     without error into a hollow map; each is rejected here rather than
//     handed back as a zero-value Lab named after the filename stem.
//   - The mapping is decoded into Lab with yaml.Strict(), so an unknown
//     top-level key errors by name instead of being silently dropped. This
//     catches both a typo'd field (e.g. "vxlan_tg") and a Config-shaped
//     file — a "labs:" wrapper map plausibly copy-pasted from config.yml —
//     which would otherwise unmarshal into an empty Lab with every field
//     at its zero value.
func loadLabFile(path string) (*Lab, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from a caller-configured include glob resolved above, not untrusted input
	if err != nil {
		return nil, fmt.Errorf("read lab file %s: %w", path, err)
	}

	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse lab file %s: %w", path, err)
	}
	if len(probe) == 0 {
		return nil, fmt.Errorf("lab file %s is empty: expected a lab definition with at least one field", path)
	}

	var lab Lab
	if err := yaml.UnmarshalWithOptions(data, &lab, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse lab file %s: %w", path, err)
	}

	return &lab, nil
}
