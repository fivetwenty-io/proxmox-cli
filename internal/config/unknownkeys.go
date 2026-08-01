package config

import (
	"fmt"
	"maps"
	"os"
	"reflect"
	"sort"
	"strings"

	yaml "github.com/goccy/go-yaml"
)

// UnknownKeys returns the dotted paths of every key in the config file at path
// that no field of Config accepts, sorted.
//
// Load parses permissively on purpose: a config carrying a key this binary
// does not know — written by a newer pmx, or by a feature branch — must still
// load, or the operator cannot run the CLI at all. The cost is that a typo is
// indistinguishable from silence: `fingerprnt` under a context is simply
// ignored, and the operator sees TLS pinning quietly not happening.
//
// So the strict pass lives here, called by `pmx context validate` — the verb
// whose whole job is answering "what is wrong with my config" — rather than in
// the load path.
//
// A missing file yields no keys and no error, matching Load. A file that does
// not parse as YAML at all is reported as an error, since no key list can be
// derived from it.
func UnknownKeys(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: same caller-resolved config path Load reads
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	var found []string
	collectUnknown(doc, reflect.TypeFor[Config](), "", &found)
	sort.Strings(found)

	return found, nil
}

// collectUnknown walks a decoded YAML value against the Go type it is
// unmarshalled into, appending the dotted path of every mapping key the type
// has no field for. prefix is the path of doc itself ("" at the root).
//
// Recursion stops at any type whose shape the config file does not dictate —
// an any-typed field, a scalar, a slice of scalars — because nothing there can
// be called unknown.
func collectUnknown(doc any, typ reflect.Type, prefix string, found *[]string) {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil {
		return
	}

	switch typ.Kind() {
	case reflect.Struct:
		m, ok := doc.(map[string]any)
		if !ok {
			// A scalar or list where a struct belongs is a type error, which
			// Load reports on its own; this pass only names unknown keys.
			return
		}
		fields := yamlFields(typ)
		for key, val := range m {
			ft, known := fields[key]
			if !known {
				*found = append(*found, join(prefix, key))
				continue
			}
			collectUnknown(val, ft, join(prefix, key), found)
		}

	case reflect.Map:
		// Map keys are the operator's own names (context names, lab names),
		// so only the values are checked, against the map's element type.
		m, ok := doc.(map[string]any)
		if !ok {
			return
		}
		for key, val := range m {
			collectUnknown(val, typ.Elem(), join(prefix, key), found)
		}

	case reflect.Slice, reflect.Array:
		items, ok := doc.([]any)
		if !ok {
			return
		}
		for i, val := range items {
			collectUnknown(val, typ.Elem(), fmt.Sprintf("%s[%d]", prefix, i), found)
		}
	}
}

// yamlFields maps the yaml key of each exported field of a struct type to that
// field's type. Anonymous (embedded) structs are flattened, since their fields
// appear at the parent's level in the document. A field tagged `yaml:"-"` is
// omitted, so a document that names it is correctly reported as unknown.
func yamlFields(typ reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for f := range typ.Fields() {
		if f.PkgPath != "" { // unexported
			continue
		}

		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				maps.Copy(out, yamlFields(ft))
				continue
			}
		}

		switch name {
		case "-":
			continue
		case "":
			// goccy/go-yaml lowercases an untagged field name.
			name = strings.ToLower(f.Name)
		}
		out[name] = f.Type
	}

	return out
}

// join builds a dotted path, avoiding a leading dot at the root.
func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
