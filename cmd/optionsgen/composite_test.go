package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// loadCompositeFixture returns the flattened parameter map for one endpoint of
// the composite-schema fixture.
func loadCompositeFixture(t *testing.T, path, verbName string) map[string]property {
	t.Helper()
	raw, err := os.ReadFile("testdata/composite_apidoc_mini.json")
	require.NoError(t, err)
	props, err := loadProperties(raw, path, verbName)
	require.NoError(t, err)
	return props
}

// TestCompositeProperties_NestedAllOf verifies that plain properties declared
// next to an allOf, the allOf members, and members nested two levels deep
// (the PBS 4.2 datastore and job-config shape) all land in one map.
func TestCompositeProperties_NestedAllOf(t *testing.T) {
	props := loadCompositeFixture(t, "/config/widget", "POST")

	require.ElementsMatch(t,
		[]string{"plain", "id", "strict-first", "strict-later", "limit", "tuning"},
		mapKeys(props))
	require.Equal(t, "integer", props["limit"].Type)
	require.Equal(t, "64", rawScalar(props["limit"].Maximum), "deeply nested property keeps its bounds")
	require.NotEmpty(t, props["tuning"].subKeys(), "deeply nested dict property keeps its sub-keys")
}

// TestCompositeProperties_AllOfRequiredWins verifies the allOf merge keeps the
// stricter declaration in either order: an optional redeclaration never relaxes
// a required one, and a required redeclaration tightens an optional one.
func TestCompositeProperties_AllOfRequiredWins(t *testing.T) {
	props := loadCompositeFixture(t, "/config/widget", "POST")

	require.False(t, isOptional(props["strict-first"]))
	require.Equal(t, "Required declaration seen first.", props["strict-first"].Description)
	require.False(t, isOptional(props["strict-later"]))
	require.Equal(t, "Required redeclaration that tightens it.", props["strict-later"].Description)
	require.False(t, isOptional(props["id"]))
	require.True(t, isOptional(props["plain"]))
}

// TestCompositeProperties_OneOfUnion verifies the oneOf merge: the union of all
// variants, required only when every variant requires the property, the
// discriminator forced required from type-property-schema, and differing
// variant descriptions joined with variant labels.
func TestCompositeProperties_OneOfUnion(t *testing.T) {
	props := loadCompositeFixture(t, "/cluster/rules", "POST")

	require.ElementsMatch(t,
		[]string{"rule", "comment", "type", "resources", "nodes", "strict", "affinity"},
		mapKeys(props))

	require.False(t, isOptional(props["resources"]), "required in every variant stays required")
	require.True(t, isOptional(props["nodes"]), "required in one variant only becomes optional")
	require.True(t, isOptional(props["affinity"]), "required in one variant only becomes optional")
	require.True(t, isOptional(props["strict"]))
	require.Equal(t, []string{"positive", "negative"}, props["affinity"].Enum)

	require.False(t, isOptional(props["type"]), "discriminator is forced required")
	require.Equal(t, "Rule type.", props["type"].Description, "type-property-schema replaces the variant's own")
	require.Equal(t, []string{"node-affinity", "resource-affinity"}, props["type"].Enum)

	require.Equal(t, "HA resources.", props["resources"].Description, "shared description is not repeated")
	require.Equal(t,
		"With type=node-affinity: Restrict to listed nodes. With type=resource-affinity: Unused for resource rules.",
		props["strict"].Description)
	require.Equal(t, "0", rawScalar(props["strict"].Default), "first variant supplies the schema")
}

// TestCompositeProperties_DoesNotMutate verifies relaxing a property to
// optional and rewriting its description work on copies.
func TestCompositeProperties_DoesNotMutate(t *testing.T) {
	variant := func(desc string) paramSchema {
		return paramSchema{Properties: map[string]property{
			"shared": {Type: "string", Description: desc},
			"only":   {Type: "string"},
		}}
	}
	first, second := variant("first"), variant("second")
	delete(second.Properties, "only")
	s := paramSchema{OneOf: []paramSchema{first, second}}

	props := compositeProperties(s)

	require.True(t, isOptional(props["only"]))
	require.Equal(t, "first second", props["shared"].Description)
	require.False(t, isOptional(first.Properties["only"]), "input variant must not be relaxed in place")
	require.Equal(t, "first", first.Properties["shared"].Description, "input description must not change")
}

// TestCompositeProperties_SingleVariantKeepsRequired verifies a lone oneOf
// variant keeps its required properties required.
func TestCompositeProperties_SingleVariantKeepsRequired(t *testing.T) {
	s := paramSchema{OneOf: []paramSchema{{Properties: map[string]property{
		"must": {Type: "string"},
		"may":  {Type: "string", Optional: json.RawMessage("1")},
	}}}}

	props := compositeProperties(s)

	require.False(t, isOptional(props["must"]))
	require.True(t, isOptional(props["may"]))
}

// TestCompositeProperties_PlainPassThrough verifies a schema without
// composition yields exactly its own properties.
func TestCompositeProperties_PlainPassThrough(t *testing.T) {
	s := paramSchema{Properties: map[string]property{"a": {Type: "string", Description: "A."}}}

	props := compositeProperties(s)

	require.Equal(t, s.Properties, props)
}

// TestCompositeProperties_EmptyComposite verifies an endpoint whose composition
// resolves to no properties still fails loudly instead of emitting an empty
// table.
func TestCompositeProperties_EmptyComposite(t *testing.T) {
	raw, err := os.ReadFile("testdata/composite_apidoc_mini.json")
	require.NoError(t, err)

	_, err = loadProperties(raw, "/config/widget", "PUT")
	require.ErrorContains(t, err, `apidoc node "/config/widget" has no PUT parameter schema`)
}

// TestGenerate_Composite verifies the full render over an allOf-wrapped
// endpoint: nested members' options appear with their values and sub-keys,
// and identity parameters excluded by -exclude stay out.
func TestGenerate_Composite(t *testing.T) {
	raw, err := os.ReadFile("testdata/composite_apidoc_mini.json")
	require.NoError(t, err)

	cfg := genConfig{
		Path:    "/config/widget",
		Verb:    "POST",
		Symbol:  "widgetOptionSchemas",
		Pkg:     "pbs",
		Exclude: splitSet("id"),
		Source:  "pbs-apidoc.json",
	}
	src, count, err := generate(raw, cfg)
	require.NoError(t, err)
	out := string(src)

	require.Equal(t, 5, count)
	require.NotContains(t, out, `"id"`)
	require.Regexp(t, `Name:\s+"limit",\s+Flag:\s+"limit",\s+Type:\s+"integer",\s+Default:\s+"8",\s+`+
		`Minimum:\s+"1",\s+Maximum:\s+"64"`, out)
	require.Regexp(t, `Name:\s+"level",\s+Type:\s+"integer",\s+`+
		`Description:\s+"Required sub-key\.",\s+Required:\s+true`, out)
	require.Regexp(t, `Name:\s+"mode",\s+Type:\s+"string",\s+`+
		`Default:\s+"safe",\s+Enum:\s+\[\]string\{"fast", "safe"\}`, out)
}

// TestIsOptional verifies every optional-marker encoding the apidocs use.
func TestIsOptional(t *testing.T) {
	cases := map[string]bool{
		``:        false,
		`1`:       true,
		`0`:       false,
		`"1"`:     true,
		`"0"`:     false,
		`"true"`:  true,
		`"TRUE"`:  true,
		`true`:    true,
		`false`:   false,
		`null`:    false,
		`{"x":1}`: false,
	}
	for in, want := range cases {
		require.Equal(t, want, isOptional(property{Optional: json.RawMessage(in)}), "optional=%q", in)
	}
}

// mapKeys returns the keys of a property map.
func mapKeys(m map[string]property) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
