// Package optionschema holds the option-schema types emitted by
// cmd/optionsgen and the shared surfaces built on them: set-flag help
// enrichment, offline describe commands, and merging built-in defaults into
// get output. Each command tree owns a generated []Schema table extracted
// from the PVE API parameter schema of its options/config endpoint.
package optionschema

import (
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

// SubKey describes one sub-key of a dict-encoded option value (for example
// migration's "type" and "network").
type SubKey struct {
	// Name is the sub-key as written inside the option value.
	Name string `json:"name"`
	// Type is the schema type (string, boolean, number, integer).
	Type string `json:"type"`
	// Default is the built-in default applied when the sub-key is unset.
	Default string `json:"default,omitempty"`
	// Enum lists the allowed values, when the schema constrains them.
	Enum []string `json:"values,omitempty"`
	// Description is the schema description, whitespace-normalised.
	Description string `json:"description,omitempty"`
	// Required marks sub-keys that must be present when the option is set.
	Required bool `json:"required,omitempty"`
	// Minimum is the lower bound for numeric sub-keys, empty when unbounded.
	Minimum string `json:"minimum,omitempty"`
	// Maximum is the upper bound for numeric sub-keys, empty when unbounded.
	Maximum string `json:"maximum,omitempty"`
}

// Schema describes one settable option from a PVE API parameter schema.
type Schema struct {
	// Name is the API parameter / config key (e.g. "mac_prefix", "net[n]").
	Name string `json:"name"`
	// Flag is the CLI flag spelling (e.g. "mac-prefix", "net").
	Flag string `json:"flag"`
	// Type is the schema type (string, boolean, number, integer).
	Type string `json:"type"`
	// Default is the built-in default applied when the option is unset.
	Default string `json:"default,omitempty"`
	// Enum lists the allowed values, when the schema constrains them.
	Enum []string `json:"values,omitempty"`
	// Description is the schema description, whitespace-normalised.
	Description string `json:"description,omitempty"`
	// SubKeys describes the components of dict-encoded option values.
	SubKeys []SubKey `json:"sub_keys,omitempty"`
	// Indexed marks slot options such as net[n] that expand to net0, net1, ….
	Indexed bool `json:"indexed,omitempty"`
	// Minimum is the lower bound for numeric options, empty when unbounded.
	Minimum string `json:"minimum,omitempty"`
	// Maximum is the upper bound for numeric options, empty when unbounded.
	Maximum string `json:"maximum,omitempty"`
}

// Find returns the schema whose API name or flag spelling matches, or nil
// when the option is unknown.
func Find(schemas []Schema, nameOrFlag string) *Schema {
	for i := range schemas {
		if schemas[i].Name == nameOrFlag || schemas[i].Flag == nameOrFlag {
			return &schemas[i]
		}
	}
	return nil
}

// suffixKeyCap bounds the sub-key list appended to flag help so options with
// dozens of sub-keys (e.g. qemu net[n]) don't overwhelm --help output.
const suffixKeyCap = 8

// Suffix builds the generated tail appended to a set flag's help text:
// allowed values and default for scalar options, the numeric range when the
// schema bounds one, and the sub-key list for dict-encoded options.
func (s *Schema) Suffix() string {
	var parts []string
	if len(s.Enum) > 0 {
		parts = append(parts, "values: "+strings.Join(s.Enum, ", "))
	}
	if r := rangeText(s.Minimum, s.Maximum); r != "" && len(s.Enum) == 0 {
		parts = append(parts, r)
	}
	if s.Default != "" {
		parts = append(parts, "default: "+s.Default)
	}
	if len(s.SubKeys) > 0 {
		keys := make([]string, 0, len(s.SubKeys))
		for _, sk := range s.SubKeys {
			keys = append(keys, sk.Name)
		}
		if len(keys) > suffixKeyCap {
			keys = append(keys[:suffixKeyCap], "…")
		}
		parts = append(parts, "keys: "+strings.Join(keys, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// DefaultValue returns the value an unset option effectively has: the scalar
// default, or for dict-encoded options the composition of every sub-key that
// carries a default (e.g. migration → "type=secure"). Empty when the schema
// defines no default at all.
func (s *Schema) DefaultValue() string {
	if s.Default != "" {
		return s.Default
	}
	var parts []string
	for _, sk := range s.SubKeys {
		if sk.Default != "" {
			parts = append(parts, sk.Name+"="+sk.Default)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// rangeText renders numeric bounds for describe/help output: "range: a…b",
// "min: a", or "max: b". Empty when the schema bounds nothing.
func rangeText(minimum, maximum string) string {
	switch {
	case minimum != "" && maximum != "":
		return "range: " + minimum + "…" + maximum
	case minimum != "":
		return "min: " + minimum
	case maximum != "":
		return "max: " + maximum
	default:
		return ""
	}
}

// EnrichFlags appends each schema's Suffix to the usage text of the matching
// flag in fs. Schemas without a registered flag (deprecated or slot options
// exposed differently) are skipped.
func EnrichFlags(fs *pflag.FlagSet, schemas []Schema) {
	for i := range schemas {
		if fl := fs.Lookup(schemas[i].Flag); fl != nil {
			fl.Usage += schemas[i].Suffix()
		}
	}
}

// MergeOpts controls MergeDefaults.
type MergeOpts struct {
	// SkipUnset drops options that have no schema default instead of listing
	// them as "(unset)" — used for large guest-config tables where the unset
	// noise would drown the real values.
	SkipUnset bool
}

// MergeDefaults adds every settable option absent from the API response to
// the table view, annotated with its built-in default (or "(unset)" when the
// schema defines none and opts.SkipUnset is false). Indexed slot options are
// always skipped — there is no single "net" key a default could attach to.
// For JSON/YAML the returned raw value is an object with the server-set
// options under "set" and the derived defaults under "defaults".
func MergeDefaults(schemas []Schema, single map[string]string, raw any, opts MergeOpts) (map[string]string, any) {
	defaults := make(map[string]string)
	for i := range schemas {
		s := &schemas[i]
		if s.Indexed {
			continue
		}
		if _, ok := single[s.Name]; ok {
			continue
		}
		if dv := s.DefaultValue(); dv != "" {
			single[s.Name] = dv + " (default)"
			defaults[s.Name] = dv
		} else if !opts.SkipUnset {
			single[s.Name] = "(unset)"
		}
	}
	return single, map[string]any{"set": raw, "defaults": defaults}
}
