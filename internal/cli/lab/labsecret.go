package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// Lab token secrets live in the macOS login keychain as generic-password
// items. Two service-name schemes exist:
//
//   - Legacy, "pmx-lab-<name>": keyed by the lab name alone, so two labs that
//     share a name on different outer clusters (or in different configs)
//     shared one item. pmx no longer writes this form, but contexts that
//     already reference it keep working unchanged.
//   - Namespaced, "pmx-lab.<outer>-<config>.<name>": <outer> is derived from
//     the name of the outer pmx context the lab was reached through (see
//     labKeychainNamespace) and <config> is a short hash of the config file's
//     absolute path (see labConfigHash), so same-named labs on different outer
//     contexts, or in different configs that reuse an outer context name such
//     as the `pmx init config` template's "lab", never share an item.
//
// Every reader finds the secret through the lab context's stored
// "keychain:<service>/<account>" reference, never by recomputing a service
// name, so the scheme can change without breaking existing contexts.

// labLegacyKeychainServicePrefix prefixes the legacy service form.
const labLegacyKeychainServicePrefix = "pmx-lab-"

// labKeychainServicePrefix prefixes the namespaced service form. The "." after
// "pmx-lab" can never follow it in a legacy name, so the two forms are
// disjoint.
const labKeychainServicePrefix = "pmx-lab."

// labKeychainNamespaceMaxLen bounds a context name used verbatim as the
// namespace segment; longer names take the sanitized, hashed form.
const labKeychainNamespaceMaxLen = 64

// labKeychainNamespaceSanitizedPrefixLen bounds the readable prefix kept in a
// sanitized namespace segment before its hash suffix.
const labKeychainNamespaceSanitizedPrefixLen = 32

// labConfigHashLen is the number of hex digits of the config path's SHA-256
// that labConfigHash keeps. The suffix has a fixed length, so the namespace
// segment before it is recovered by length alone.
const labConfigHashLen = 8

// labKeychainNamespaceVerbatimRE is the charset a context name must match to
// be used verbatim as the namespace segment. It excludes "_", which only the
// sanitized form emits, so a verbatim segment can never equal a sanitized one.
// It excludes "." (the service separator) and "/" (the reference's
// service/account separator), and every character in it survives the
// `security -i` tokenizer unchanged (see config.StoreKeychainSecret).
var labKeychainNamespaceVerbatimRE = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// labKeychainNamespaceSegmentRE matches any namespace segment of a namespaced
// service: the "<outer>-<config>" segment pmx writes, and the bare "<outer>"
// segment of items written before the config hash was added.
var labKeychainNamespaceSegmentRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// errNoOuterContext is returned when no outer pmx context is active to
// namespace a new lab secret under.
var errNoOuterContext = errors.New(
	"there is no active pmx context to namespace the lab's keychain secret under, " +
		"so select the outer context with --context/-c")

// errNoConfigPath is returned when the invocation has no config file path to
// namespace a lab secret under.
var errNoConfigPath = errors.New(
	"there is no config file path to namespace the lab's keychain secret under, so pass --config")

// labLegacyKeychainService returns the legacy keychain service of a lab's
// token secret, "pmx-lab-" + name. pmx never writes it any more; it is used
// only to recognize a context reference that predates namespacing.
func labLegacyKeychainService(name string) string {
	return labLegacyKeychainServicePrefix + name
}

// labKeychainService returns the keychain service a new token secret for the
// lab named name is stored under when the lab is reached through the outer
// pmx context named outerCtx in the config file whose labConfigHash is
// configHash: "pmx-lab.<namespace>-<configHash>.<name>". Lab names are
// limited to lowercase letters, digits, and hyphens (validateLabNameCharset),
// and the namespace segment never contains ".", so the service splits back
// into its parts unambiguously; configHash has a fixed length, so the segment
// splits back into namespace and hash too.
func labKeychainService(outerCtx, configHash, name string) string {
	return labKeychainServicePrefix + labKeychainNamespace(outerCtx) + "-" + configHash + "." + name
}

// labConfigHash returns the first labConfigHashLen hex digits of the SHA-256
// of configPath made absolute and cleaned. It ties a lab secret to the config
// file whose context references it: two configs that both name their outer
// context "lab" still get distinct items. Symlinks are not resolved, so the
// hash depends only on the path the operator passed (or the default path),
// not on the file existing. A config that moves gets a new hash; an item
// written under the old one then no longer matches and is kept by destroy and
// rm rather than deleted.
func labConfigHash(configPath string) (string, error) {
	if configPath == "" {
		return "", errNoConfigPath
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("resolve config path %q: %w", configPath, err)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:labConfigHashLen], nil
}

// labCurrentKeychainService returns the namespaced keychain service this
// invocation stores, and may delete, the lab named name's token secret under:
// the outer context from labOuterContextName and the config path from
// deps.ConfigPath. An error means the namespace cannot be determined, in
// which case callers neither store a new secret nor delete a namespaced one.
func labCurrentKeychainService(deps *cli.Deps, name string) (string, error) {
	outer, err := labOuterContextName(deps)
	if err != nil {
		return "", err
	}
	hash, err := labConfigHash(deps.ConfigPath)
	if err != nil {
		return "", err
	}
	return labKeychainService(outer, hash, name), nil
}

// labKeychainDeleteCommand returns the `security` command that removes the
// keychain item (service, account) by hand. pmx prints it wherever it leaves
// an item in place that the operator may want gone.
func labKeychainDeleteCommand(service, account string) string {
	return fmt.Sprintf("security delete-generic-password -s %s -a '%s'", service, account)
}

// labKeychainNamespace maps an outer context name onto a keychain-safe
// namespace segment. A name of at most labKeychainNamespaceMaxLen ASCII
// letters, digits, and hyphens is used verbatim, so the common case stays
// readable in Keychain Access. Any other name is sanitized: each character
// outside that set becomes "_", the result is truncated, and "_" plus the
// first 12 hex digits of the name's SHA-256 is appended. The sanitized form
// always contains "_" and the verbatim form never does, so distinct context
// names cannot map to the same segment through the two forms.
func labKeychainNamespace(outerCtx string) string {
	if len(outerCtx) <= labKeychainNamespaceMaxLen && labKeychainNamespaceVerbatimRE.MatchString(outerCtx) {
		return outerCtx
	}

	var b strings.Builder
	for _, r := range outerCtx {
		if b.Len() >= labKeychainNamespaceSanitizedPrefixLen {
			break
		}
		if isKeychainNamespaceRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	sum := sha256.Sum256([]byte(outerCtx))
	return b.String() + "_" + hex.EncodeToString(sum[:6])
}

// isKeychainNamespaceRune reports whether r is an ASCII letter, digit, or
// hyphen, the characters labKeychainNamespace keeps verbatim.
func isKeychainNamespaceRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
}

// labOuterContextName returns the name of the outer pmx context this
// invocation reaches the lab's PVE through: the context selected by
// --context/-c or $PMX_CONTEXT, else current-context. Labs carry no reference
// to their outer cluster in config; every lab verb reaches it through this
// context, so it is the identity a new secret is namespaced by.
func labOuterContextName(deps *cli.Deps) (string, error) {
	if deps != nil && deps.CtxName != "" {
		return deps.CtxName, nil
	}
	if deps != nil && deps.Cfg != nil && deps.Cfg.CurrentContext != "" {
		return deps.Cfg.CurrentContext, nil
	}
	return "", errNoOuterContext
}

// labOwnsKeychainService reports whether service is a keychain service pmx
// stores the lab named name's token secret under, in either the legacy or the
// namespaced form.
func labOwnsKeychainService(service, name string) bool {
	return labLegacyKeychainServiceOf(service, name) || labNamespacedKeychainServiceOf(service, name)
}

// labLegacyKeychainServiceOf reports whether service is the lab named name's
// legacy "pmx-lab-<name>" service.
func labLegacyKeychainServiceOf(service, name string) bool {
	return service == labLegacyKeychainService(name)
}

// labNamespacedKeychainServiceOf reports whether service is a namespaced
// "pmx-lab.<namespace>.<name>" service of the lab named name, under any
// namespace.
func labNamespacedKeychainServiceOf(service, name string) bool {
	rest, ok := strings.CutPrefix(service, labKeychainServicePrefix)
	if !ok {
		return false
	}
	ns, labName, ok := strings.Cut(rest, ".")
	return ok && labName == name && labKeychainNamespaceSegmentRE.MatchString(ns)
}

// labOwnedSecretRef parses ref and reports the keychain item it names when
// that item is one of the lab named name's own token secrets: a
// "keychain:<service>/<account>" reference whose account is the lab token
// account and whose service is a legacy or namespaced service of this lab.
// Any other reference (an env reference, a literal, a bare service with no
// account, or another keychain item) yields ok == false.
func labOwnedSecretRef(ref, name string) (service, account string, ok bool) {
	service, account, ok = config.ParseKeychainRef(ref)
	if !ok || account != labCtxAccount() || !labOwnsKeychainService(service, name) {
		return "", "", false
	}
	return service, account, true
}

// keychainRefCovers reports whether the secret reference ref resolves through
// the keychain item (service, account). A bare "keychain:<service>" reference
// matches any account under the service, the same way keychainLookup treats
// it, so it covers every account.
func keychainRefCovers(ref, service, account string) bool {
	s, a, ok := config.ParseKeychainRef(ref)
	return ok && s == service && (a == "" || a == account)
}

// labKeychainItemReferencedBy returns the name of a context in cfg, other than
// exclude, whose auth secret or proxy password resolves through the keychain
// item (service, account), or "" when none does. Names are checked in sorted
// order so the result is deterministic.
func labKeychainItemReferencedBy(cfg *config.Config, exclude, service, account string) string {
	if cfg == nil {
		return ""
	}
	names := make([]string, 0, len(cfg.Contexts))
	for n := range cfg.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := cfg.Contexts[n]
		if n == exclude || c == nil {
			continue
		}
		if keychainRefCovers(c.Auth.Secret, service, account) || keychainRefCovers(c.Proxy.Password, service, account) {
			return n
		}
	}
	return ""
}
