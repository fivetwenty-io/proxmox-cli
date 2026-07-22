// Package config provides types, loader, writer, and secret resolver
// for the pmx CLI configuration file (~/.config/pmx/config.yml).
package config

import (
	"fmt"
	"slices"
	"sort"
)

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

	// DefaultUserPassword is the password assigned to a lab's owner user when
	// `pmx lab access grant` creates it. It lives only on this top-level key
	// so a written lab file (Config.Labs entries persisted via the lab-config
	// writer) can never carry the secret.
	DefaultUserPassword string `yaml:"default_user_password,omitempty"`

	// LabsDir is a directory of `<name>.yaml` files, each holding one Lab,
	// merged into Labs at load time alongside any inline or Include entries.
	LabsDir string `yaml:"labs_dir,omitempty"`

	// Include lists glob patterns for additional lab YAML files to merge
	// into Labs at load time.
	Include []string `yaml:"include,omitempty"`

	// Labs is the named map of inline lab environment configurations.
	Labs map[string]*Lab `yaml:"labs,omitempty"`

	// Storage holds pool-wide storage reservations that are not
	// attributable to any single lab (e.g. the shared NFS service's
	// quota), read by `pmx lab create`'s capacity gate alongside per-lab
	// refquotas.
	Storage ConfigStorage `yaml:"storage,omitempty" json:"storage,omitempty"`

	// Log holds JSONL command-log preferences (layout and level).
	Log ConfigLog `yaml:"log,omitempty" json:"log,omitempty"`
}

// Log layout values for ConfigLog.Layout.
const (
	// LogLayoutNested writes each command's log under per-command
	// subdirectories: ~/.pmx/logs/pve/storage/volume/copy/{ts}.jsonl.
	LogLayoutNested = "nested"

	// LogLayoutFlat writes all logs directly under ~/.pmx/logs with the
	// command path encoded in the filename:
	// pve-storage-volume-copy-{ts}.jsonl.
	LogLayoutFlat = "flat"
)

// DefaultLogLevel is the log level used when log.level is unset.
const DefaultLogLevel = "info"

// ConfigLog holds logging preferences for the JSONL command logs written
// under ~/.pmx/logs.
type ConfigLog struct {
	// Layout selects the log file layout: "nested" (default) or "flat".
	// Overridable via $PMX_LOG_LAYOUT.
	Layout string `yaml:"layout,omitempty" json:"layout,omitempty"`

	// Level is the minimum level recorded: "trace", "debug", "info"
	// (default), "warn", or "error". Overridable via $PMX_LOG_LEVEL; the
	// --debug/--verbose/--trace flags force debug regardless.
	Level string `yaml:"level,omitempty" json:"level,omitempty"`

	// Retention is the number of days to keep log files. When positive it
	// supplies the default cutoff for `pmx logs prune` and enables a
	// best-effort automatic prune at most once per 24 hours after a command
	// completes. Zero or negative disables both.
	Retention int `yaml:"retention,omitempty" json:"retention,omitempty"`
}

// EffectiveLogLayout returns the configured log layout, defaulting to
// LogLayoutNested when unset or unrecognised. A nil cfg also returns the
// default, so callers need not nil-check cfg first.
func EffectiveLogLayout(cfg *Config) string {
	if cfg != nil && cfg.Log.Layout == LogLayoutFlat {
		return LogLayoutFlat
	}
	return LogLayoutNested
}

// EffectiveLogLevel returns the configured log level, defaulting to
// DefaultLogLevel when unset. A nil cfg also returns the default.
func EffectiveLogLevel(cfg *Config) string {
	if cfg != nil && cfg.Log.Level != "" {
		return cfg.Log.Level
	}
	return DefaultLogLevel
}

// EffectiveLogRetention returns the configured log retention in days, or 0
// when unset, non-positive, or cfg is nil (retention disabled).
func EffectiveLogRetention(cfg *Config) int {
	if cfg != nil && cfg.Log.Retention > 0 {
		return cfg.Log.Retention
	}
	return 0
}

// DefaultNFSReservedGB is the fallback ZFS quota (in GB) EffectiveNFSReservedGB
// uses when ConfigStorage.NFSReservedGB is unset: 1024 (1T), the tank/nfs
// dataset's hard quota (multi-node lab plan §10 decision D1, amended). This
// applies only while the shared NFS service lives on the same pool as every
// lab's dataset; once it moves to its own dedicated nfs_pool, operators set
// storage.nfs_reserved_gb: 0 to opt out entirely.
const DefaultNFSReservedGB = 1024

// ConfigStorage holds pool-wide storage reservations that are not
// attributable to any single lab.
type ConfigStorage struct {
	// NFSReservedGB is the ZFS quota (in GB) reserved on the shared pool
	// for the tank/nfs dataset (decision D1, amended). A nil pointer means
	// "not set": EffectiveNFSReservedGB then falls back to
	// DefaultNFSReservedGB. An explicit 0 is a deliberate opt-out (the
	// state once NFS moves to its own dedicated nfs_pool), distinct from
	// "unset" — this is why the field is a pointer rather than a plain
	// int, whose zero value could not otherwise be told apart from
	// "unset".
	NFSReservedGB *int `yaml:"nfs_reserved_gb,omitempty" json:"nfs_reserved_gb,omitempty"`

	// CapacityStorageID is an explicit override for the PVE storage
	// identifier `pmx lab create`/`pmx lab scale`'s capacity gate reads
	// the base pool's live total/used size from (GET
	// /nodes/{node}/storage/{storage}/status). When empty, the gate
	// auto-discovers a zfspool-type storage registered in
	// /cluster/storage whose "pool" attribute is the lab's base pool
	// (e.g. "tank") or nested under it (e.g. "tank/labs/wayne"),
	// preferring one rooted at the base pool itself. Set this when no
	// such storage exists yet, or when the auto-discovered storage's
	// live status does not reflect the pool's true capacity (a nested
	// per-lab dataset's status is bound by that dataset's own refquota).
	CapacityStorageID string `yaml:"capacity_storage_id,omitempty" json:"capacity_storage_id,omitempty"`
}

// EffectiveNFSReservedGB returns cfg.Storage.NFSReservedGB when set
// (including an explicit 0, the documented opt-out once NFS moves to its
// own dedicated nfs_pool), else DefaultNFSReservedGB. A nil cfg also
// returns DefaultNFSReservedGB, so callers need not nil-check cfg first.
func EffectiveNFSReservedGB(cfg *Config) int {
	if cfg == nil || cfg.Storage.NFSReservedGB == nil {
		return DefaultNFSReservedGB
	}
	return *cfg.Storage.NFSReservedGB
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

	// Product selects which Proxmox product this context targets: "pve",
	// "pbs", or "pdm". Empty means "pve" (backward compatible with configs
	// written before Product existed).
	Product string `yaml:"product,omitempty"`
}

// IsPBS reports whether c targets Proxmox Backup Server. Empty Product
// (backward-compat configs) is treated as ProductPVE, so IsPBS returns false.
func (c *Context) IsPBS() bool {
	return c.Product == ProductPBS
}

// ProductOrDefault returns the context's product, treating an empty Product
// (backward-compat configs written before Product existed) as ProductPVE.
func (c *Context) ProductOrDefault() string {
	if c.Product == "" {
		return ProductPVE
	}
	return c.Product
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

// ContextNamesWithProducts returns sorted "name (product)" entries for every
// context in cfg, for not-found error messages and other listings that must
// tell the operator which product each candidate targets. An empty Product
// (backward-compat configs) renders as ProductPVE.
func ContextNamesWithProducts(cfg *Config) []string {
	entries := make([]string, 0, len(cfg.Contexts))
	for name, ctx := range cfg.Contexts {
		product := ProductPVE
		if ctx != nil {
			product = ctx.ProductOrDefault()
		}
		entries = append(entries, fmt.Sprintf("%s (%s)", name, product))
	}
	sort.Strings(entries)
	return entries
}
