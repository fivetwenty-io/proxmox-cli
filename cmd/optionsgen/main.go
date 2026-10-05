// Command optionsgen generates the option-schema tables used by the CLI's
// options/config command families. It extracts one endpoint's parameter
// schema (types, defaults, allowed values, numeric bounds, and dict sub-keys)
// from the proxmox-apiclient-go module's _data/apidoc.json and emits it as a Go
// source file declaring a []optionschema.Schema, so the CLI can describe
// every settable option — including ones not currently set — without a
// server round-trip.
//
// Usage (normally via go:generate in the owning package):
//
//	go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen \
//	    -path /cluster/options -out options_schema_gen.go
//
// Path parameters ({node}, {vmid}, …) leak into apidoc properties and are
// excluded automatically; -exclude drops meta-parameters (delete, digest,
// revert by default). Indexed slot options such as net[n] keep their bracket
// name and are marked Indexed with the flag spelling "net"; -flag-override
// handles the rare name that needs a hand-picked flag (numa[n]=numa-node).
// The package name defaults to $GOPACKAGE, which go generate sets.
//
// Composite parameter schemas (allOf members, oneOf variants with a
// discriminator, nested to any depth) are flattened into one parameter map
// with the same merge rules as the SDK's own pvegen generator, so PBS 4.2's
// allOf-wrapped create/update schemas and PVE's HA rule schemas generate the
// same way as plain ones.
//
// The apidoc.json location defaults to the proxmox-apiclient-go module directory
// reported by `go list -m`; pass -apidoc to point at another copy. -source
// picks a different file within that module's _data directory without
// spelling out the whole path — e.g. -source pbs-apidoc.json reads the
// Proxmox Backup Server API schema shipped alongside apidoc.json.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// clientModule is the module whose _data/apidoc.json carries the PVE API schema.
const clientModule = "github.com/fivetwenty-io/proxmox-apiclient-go/v3"

// schemaPackage is the package providing the Schema/SubKey types the
// generated file references.
const schemaPackage = "github.com/fivetwenty-io/proxmox-cli/internal/optionschema"

// genConfig carries one generation target.
type genConfig struct {
	// Path is the apidoc node path, e.g. "/nodes/{node}/config".
	Path string
	// Verb is the HTTP method whose parameters form the schema.
	Verb string
	// Symbol is the generated variable name.
	Symbol string
	// Pkg is the generated file's package name.
	Pkg string
	// Exclude names parameters dropped from the table (meta-parameters);
	// path parameters from {tokens} in Path are always dropped.
	Exclude map[string]bool
	// FlagOverrides maps API parameter names to hand-picked flag spellings.
	FlagOverrides map[string]string
	// Source is the apidoc file name recorded in the generated header.
	Source string
}

// node is the subset of an apidoc tree entry the generator needs.
type node struct {
	Path     string          `json:"path"`
	Children []node          `json:"children"`
	Info     map[string]verb `json:"info"`
}

// verb holds one HTTP method's schema inside a node's info map.
type verb struct {
	Parameters paramSchema `json:"parameters"`
}

// paramSchema is an endpoint's parameter object schema. Most endpoints list
// their parameters directly in Properties. Composite endpoints split them
// across AllOf members and OneOf variants instead: PBS 4.2 wraps every create
// and update schema in an allOf (identity parameters plus the shared config
// properties, sometimes nested further), and PVE 9.2's HA rule endpoints add a
// oneOf with one variant per rule type, selected by the TypeProperty
// discriminator. compositeProperties merges all of them back into one map.
type paramSchema struct {
	Properties map[string]property `json:"properties"`
	AllOf      []paramSchema       `json:"allOf"`
	OneOf      []paramSchema       `json:"oneOf"`
	// TypeProperty names the discriminator parameter selecting a OneOf
	// variant ("type" for HA rules); TypePropertySchema is its schema.
	TypeProperty       string    `json:"type-property"`
	TypePropertySchema *property `json:"type-property-schema"`
	// InstanceType is a OneOf variant's discriminator value, used to label
	// variant-specific descriptions.
	InstanceType string `json:"instance-type"`
}

// optionalTrue is the optional marker written onto a property that the
// oneOf merge relaxes from required to optional.
var optionalTrue = json.RawMessage("1")

// property is one parameter schema entry; Format is either a string alias or a
// map of sub-key properties for dict-encoded options.
type property struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Enum        []string        `json:"enum"`
	Default     json.RawMessage `json:"default"`
	Optional    json.RawMessage `json:"optional"`
	Format      json.RawMessage `json:"format"`
	Minimum     json.RawMessage `json:"minimum"`
	Maximum     json.RawMessage `json:"maximum"`
}

// subKeys decodes p.Format as a dict of sub-key properties, returning nil when
// the format is a plain string alias (e.g. "mac-prefix") or absent.
func (p property) subKeys() map[string]property {
	if len(p.Format) == 0 || p.Format[0] != '{' {
		return nil
	}
	var m map[string]property
	if err := json.Unmarshal(p.Format, &m); err != nil {
		return nil
	}
	return m
}

func main() {
	apidoc := flag.String("apidoc", "", "path to apidoc.json (default: <"+clientModule+" module dir>/_data/<source>)")
	source := flag.String("source", "apidoc.json",
		"apidoc filename within <module dir>/_data, e.g. pbs-apidoc.json (ignored when -apidoc is set)")
	out := flag.String("out", "options_schema_gen.go", "output Go source file")
	path := flag.String("path", "", "apidoc node path, e.g. /cluster/options (required)")
	verbName := flag.String("verb", "PUT", "HTTP method whose parameters form the schema")
	symbol := flag.String("symbol", "optionSchemas", "generated variable name")
	pkg := flag.String("pkg", os.Getenv("GOPACKAGE"), "generated file's package name (default: $GOPACKAGE)")
	exclude := flag.String("exclude", "delete,digest,revert", "comma-separated parameter names to drop")
	overrides := flag.String("flag-override", "", "comma-separated apiname=flag pairs overriding derived flag spellings")
	flag.Parse()
	log.SetFlags(0)
	log.SetPrefix("optionsgen: ")

	if *path == "" {
		log.Fatal("-path is required")
	}
	if *pkg == "" {
		log.Fatal("-pkg is required when $GOPACKAGE is not set")
	}

	apidocPath := *apidoc
	if apidocPath == "" {
		dir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", clientModule).Output()
		if err != nil {
			log.Fatalf("locate %s module dir: %v", clientModule, err)
		}
		apidocPath = filepath.Join(strings.TrimSpace(string(dir)), "_data", *source)
	}

	cfg := genConfig{
		Path:          *path,
		Verb:          *verbName,
		Symbol:        *symbol,
		Pkg:           *pkg,
		Exclude:       splitSet(*exclude),
		FlagOverrides: splitPairs(*overrides),
		Source:        filepath.Base(apidocPath),
	}

	raw, err := os.ReadFile(apidocPath)
	if err != nil {
		log.Fatalf("read apidoc: %v", err)
	}
	src, count, err := generate(raw, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("optionsgen: wrote %s (%d options)\n", *out, count)
}

// splitSet parses a comma-separated list into a set, ignoring empty entries.
func splitSet(s string) map[string]bool {
	set := make(map[string]bool)
	for name := range strings.SplitSeq(s, ",") {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = true
		}
	}
	return set
}

// splitPairs parses comma-separated key=value pairs.
func splitPairs(s string) map[string]string {
	m := make(map[string]string)
	for pair := range strings.SplitSeq(s, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" || v == "" {
			log.Fatalf("invalid -flag-override entry %q: want apiname=flag", pair)
		}
		m[k] = v
	}
	return m
}

// generate parses the apidoc tree and renders the schema table for cfg.
func generate(apidoc []byte, cfg genConfig) ([]byte, int, error) {
	props, err := loadProperties(apidoc, cfg.Path, cfg.Verb)
	if err != nil {
		return nil, 0, err
	}
	names := selectNames(props, cfg)
	src, err := render(props, names, cfg)
	if err != nil {
		return nil, 0, err
	}
	return src, len(names), nil
}

// loadProperties parses apidoc.json and returns the parameter properties of
// verbName at path.
func loadProperties(raw []byte, path, verbName string) (map[string]property, error) {
	var tree []node
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("parse apidoc: %w", err)
	}
	n := findNode(tree, path)
	if n == nil {
		return nil, fmt.Errorf("apidoc node %q not found", path)
	}
	v, ok := n.Info[verbName]
	if !ok {
		return nil, fmt.Errorf("apidoc node %q has no %s parameter schema", path, verbName)
	}
	props := compositeProperties(v.Parameters)
	if len(props) == 0 {
		return nil, fmt.Errorf("apidoc node %q has no %s parameter schema", path, verbName)
	}
	return props, nil
}

// compositeProperties resolves the full parameter set of a schema that may
// combine plain Properties with AllOf members and OneOf variants, recursing
// into nested compositions. The merge rules match the SDK's pvegen generator
// and what the API server enforces on the wire:
//
//   - allOf: every member's properties apply. Members are merged in order
//     after the plain Properties; when two declare the same parameter, the
//     later one wins unless that would relax a required declaration to an
//     optional one, because the server enforces the stricter rule.
//   - oneOf: a request carries exactly one variant, so the result is the union
//     of every variant's properties. A parameter is required only when every
//     variant declares it required; the first variant declaring a parameter
//     supplies its schema, and differing variant descriptions are joined (see
//     variantDescriptions).
//   - The TypeProperty discriminator is taken from TypePropertySchema and
//     forced required, replacing any variant's own declaration of it.
//
// The input is never mutated; adjusted properties are copies.
func compositeProperties(s paramSchema) map[string]property {
	props := make(map[string]property, len(s.Properties))
	maps.Copy(props, s.Properties)
	for _, member := range s.AllOf {
		for name, p := range compositeProperties(member) {
			mergeRequiredWins(props, name, p)
		}
	}
	if len(s.OneOf) > 0 {
		for name, p := range oneOfUnion(s.OneOf, s.TypeProperty) {
			mergeRequiredWins(props, name, p)
		}
	}
	if s.TypeProperty != "" && s.TypePropertySchema != nil {
		discriminator := *s.TypePropertySchema
		discriminator.Optional = nil
		props[s.TypeProperty] = discriminator
	}
	return props
}

// mergeRequiredWins stores p under name unless an existing entry is required
// while p is optional: the stricter declaration is the one the server
// enforces when both apply.
func mergeRequiredWins(props map[string]property, name string, p property) {
	if existing, ok := props[name]; ok && !isOptional(existing) && isOptional(p) {
		return
	}
	props[name] = p
}

// oneOfUnion returns the union of every variant's properties with optionality
// recomputed: a property stays required only when it is present and required
// in every variant. The first variant declaring a property supplies its schema.
// typeProperty labels merged descriptions with each variant's InstanceType.
func oneOfUnion(variants []paramSchema, typeProperty string) map[string]property {
	resolved := make([]map[string]property, 0, len(variants))
	for _, v := range variants {
		resolved = append(resolved, compositeProperties(v))
	}
	union := make(map[string]property)
	for _, variantProps := range resolved {
		for name, p := range variantProps {
			if _, seen := union[name]; seen {
				continue
			}
			if !isOptional(p) && !requiredInAll(resolved, name) {
				p.Optional = optionalTrue
			}
			union[name] = p
		}
	}
	for name, p := range union {
		p.Description = variantDescriptions(variants, resolved, name, typeProperty)
		union[name] = p
	}
	return union
}

// requiredInAll reports whether every resolved variant declares name and
// marks it required.
func requiredInAll(resolved []map[string]property, name string) bool {
	for _, variantProps := range resolved {
		p, ok := variantProps[name]
		if !ok || isOptional(p) {
			return false
		}
	}
	return true
}

// variantDescriptions returns the description of property name across the
// oneOf variants: the single shared description when every variant agrees,
// otherwise each distinct description in variant order, prefixed with
// "With <typeProperty>=<instance-type>:" where the variant is labelled.
func variantDescriptions(variants []paramSchema, resolved []map[string]property, name, typeProperty string) string {
	var distinct, labelled []string
	for i, variantProps := range resolved {
		p, ok := variantProps[name]
		if !ok {
			continue
		}
		desc := strings.TrimSpace(p.Description)
		if desc == "" || slices.Contains(distinct, desc) {
			continue
		}
		distinct = append(distinct, desc)
		if typeProperty != "" && variants[i].InstanceType != "" {
			desc = "With " + typeProperty + "=" + variants[i].InstanceType + ": " + desc
		}
		labelled = append(labelled, desc)
	}
	switch len(distinct) {
	case 0:
		return ""
	case 1:
		return distinct[0]
	default:
		return strings.Join(labelled, " ")
	}
}

// findNode depth-first searches the apidoc tree for the node with the given path.
func findNode(nodes []node, path string) *node {
	for i := range nodes {
		if nodes[i].Path == path {
			return &nodes[i]
		}
		if n := findNode(nodes[i].Children, path); n != nil {
			return n
		}
	}
	return nil
}

// selectNames returns the sorted parameter names that survive exclusion:
// -exclude meta-parameters and the {token} path parameters that apidoc leaks
// into the property map.
func selectNames(props map[string]property, cfg genConfig) []string {
	pathParams := make(map[string]bool)
	for seg := range strings.SplitSeq(cfg.Path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			pathParams[seg[1:len(seg)-1]] = true
		}
	}
	names := make([]string, 0, len(props))
	for name := range props {
		if cfg.Exclude[name] || pathParams[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// render emits the generated Go source declaring cfg.Symbol.
func render(props map[string]property, names []string, cfg genConfig) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cmd/optionsgen from %s (%s %s %s); DO NOT EDIT.\n\n",
		cfg.Source, clientModule, cfg.Verb, cfg.Path)
	fmt.Fprintf(&b, "package %s\n\n", cfg.Pkg)
	fmt.Fprintf(&b, "import %q\n\n", schemaPackage)
	fmt.Fprintf(&b, "// %s describes every settable option from the %s API schema for\n", cfg.Symbol, productLabel(cfg.Source))
	fmt.Fprintf(&b, "// %s %s, in lexical order by API key.\n", cfg.Verb, cfg.Path)
	fmt.Fprintf(&b, "var %s = []optionschema.Schema{\n", cfg.Symbol)
	for _, name := range names {
		p := props[name]
		fmt.Fprintf(&b, "\t{\n\t\tName: %q,\n\t\tFlag: %q,\n\t\tType: %q,\n", name, flagName(name, cfg.FlagOverrides), p.Type)
		if d := defaultString(p); d != "" {
			fmt.Fprintf(&b, "\t\tDefault: %q,\n", d)
		}
		writeEnum(&b, "\t\t", p.Enum)
		writeBounds(&b, "\t\t", p)
		fmt.Fprintf(&b, "\t\tDescription: %q,\n", cleanDescription(p.Description))
		if indexedName(name) {
			b.WriteString("\t\tIndexed: true,\n")
		}
		if sub := p.subKeys(); len(sub) > 0 {
			subNames := make([]string, 0, len(sub))
			for sk := range sub {
				subNames = append(subNames, sk)
			}
			sort.Strings(subNames)
			b.WriteString("\t\tSubKeys: []optionschema.SubKey{\n")
			for _, sk := range subNames {
				sp := sub[sk]
				fmt.Fprintf(&b, "\t\t\t{\n\t\t\t\tName: %q,\n\t\t\t\tType: %q,\n", sk, sp.Type)
				if d := defaultString(sp); d != "" {
					fmt.Fprintf(&b, "\t\t\t\tDefault: %q,\n", d)
				}
				writeEnum(&b, "\t\t\t\t", sp.Enum)
				writeBounds(&b, "\t\t\t\t", sp)
				if desc := cleanDescription(sp.Description); desc != "" {
					fmt.Fprintf(&b, "\t\t\t\tDescription: %q,\n", desc)
				}
				if !isOptional(sp) {
					b.WriteString("\t\t\t\tRequired: true,\n")
				}
				b.WriteString("\t\t\t},\n")
			}
			b.WriteString("\t\t},\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}

// writeEnum emits an Enum field literal when values are present.
func writeEnum(b *bytes.Buffer, indent string, enum []string) {
	if len(enum) == 0 {
		return
	}
	fmt.Fprintf(b, "%sEnum: []string{", indent)
	for i, v := range enum {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", v)
	}
	b.WriteString("},\n")
}

// writeBounds emits Minimum/Maximum field literals when the schema bounds them.
func writeBounds(b *bytes.Buffer, indent string, p property) {
	if v := rawScalar(p.Minimum); v != "" {
		fmt.Fprintf(b, "%sMinimum: %q,\n", indent, v)
	}
	if v := rawScalar(p.Maximum); v != "" {
		fmt.Fprintf(b, "%sMaximum: %q,\n", indent, v)
	}
}

// indexedName reports whether an API parameter is an indexed slot option
// (net[n], scsi[n], …).
func indexedName(name string) bool {
	return strings.HasSuffix(name, "[n]")
}

// productLabel names the API schema's product for the generated doc comment,
// inferred from the apidoc source filename: "pbs-apidoc.json" (and any other
// name starting with "pbs-") is Proxmox Backup Server, "pdm-apidoc.json"
// (and any other name starting with "pdm-") is Proxmox Datacenter Manager,
// and everything else, including the default "apidoc.json", is Proxmox VE —
// preserving existing PVE invocations' generated output byte-for-byte.
func productLabel(source string) string {
	switch {
	case strings.HasPrefix(source, "pbs-"):
		return "Proxmox Backup Server"
	case strings.HasPrefix(source, "pdm-"):
		return "Proxmox Datacenter Manager"
	default:
		return "PVE"
	}
}

// flagName maps an API parameter name to its CLI flag spelling: overrides
// win, indexed names drop their [n] suffix, underscores become hyphens
// (email_from → email-from).
func flagName(name string, overrides map[string]string) string {
	if f, ok := overrides[name]; ok {
		return f
	}
	name = strings.TrimSuffix(name, "[n]")
	return strings.ReplaceAll(name, "_", "-")
}

// defaultString renders a schema default as the string shown to users. Boolean
// defaults arrive as JSON numbers 0/1 and are mapped to false/true.
func defaultString(p property) string {
	s := rawScalar(p.Default)
	if p.Type == "boolean" {
		if mapped, ok := map[string]string{"0": "false", "1": "true"}[s]; ok {
			return mapped
		}
	}
	return s
}

// rawScalar renders a raw JSON scalar as its user-facing string. UseNumber
// keeps large integers out of scientific notation.
func rawScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return fmt.Sprintf("%t", t)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

// isOptional reports whether a property is marked optional in the schema. The
// apidocs encode the marker as the number 1, the string "1" or "true", or the
// boolean true; anything else, including an absent marker, means required.
func isOptional(p property) bool {
	if len(p.Optional) == 0 {
		return false
	}
	var v any
	if err := json.Unmarshal(p.Optional, &v); err != nil {
		return false
	}
	switch t := v.(type) {
	case float64:
		return t == 1
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	case bool:
		return t
	default:
		return false
	}
}

// cleanDescription collapses runs of whitespace (including newlines) in a
// schema description into single spaces.
func cleanDescription(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
