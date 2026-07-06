package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// testConfig returns the generation target used across tests, pointed at the
// small apidoc fixture in testdata.
func testConfig() genConfig {
	return genConfig{
		Path:          "/things/{id}/options",
		Verb:          "PUT",
		Symbol:        "thingSchemas",
		Pkg:           "things",
		Exclude:       splitSet("delete,digest,revert"),
		FlagOverrides: splitPairs("special[n]=extra-special"),
		Source:        "apidoc_mini.json",
	}
}

// generateFixture runs generate over the testdata fixture.
func generateFixture(t *testing.T, cfg genConfig) ([]byte, int) {
	t.Helper()
	raw, err := os.ReadFile("testdata/apidoc_mini.json")
	require.NoError(t, err)
	src, count, err := generate(raw, cfg)
	require.NoError(t, err)
	return src, count
}

// TestGenerate_Selection verifies path-param auto-exclusion, -exclude
// meta-parameters, and the option count.
func TestGenerate_Selection(t *testing.T) {
	src, count := generateFixture(t, testConfig())
	out := string(src)

	require.Equal(t, 7, count)
	require.Contains(t, out, "package things\n")
	require.Contains(t, out, "var thingSchemas = []optionschema.Schema{")
	require.NotContains(t, out, `"id"`, "path param leaked into the table")
	require.NotContains(t, out, `"delete"`)
	require.NotContains(t, out, `"digest"`)
}

// TestGenerate_FlagMapping verifies underscore→hyphen mapping, indexed [n]
// stripping, and -flag-override.
func TestGenerate_FlagMapping(t *testing.T) {
	src, _ := generateFixture(t, testConfig())
	out := string(src)

	require.Regexp(t, `Flag:\s+"plain-opt"`, out)
	require.Regexp(t, `Name:\s+"slot\[n\]"`, out)
	require.Regexp(t, `Flag:\s+"slot"`, out)
	require.Regexp(t, `Indexed:\s+true`, out)
	require.Regexp(t, `Flag:\s+"extra-special"`, out)
}

// TestGenerate_Values verifies defaults (bool 0/1 mapping, large ints kept out
// of scientific notation), numeric bounds including floats, enums, dict
// sub-keys with required detection, and whitespace-normalised descriptions.
func TestGenerate_Values(t *testing.T) {
	src, _ := generateFixture(t, testConfig())
	out := string(src)

	require.Regexp(t, `Default:\s+"true"`, out, "boolean 1 default must map to true")
	require.Regexp(t, `Default:\s+"1000000"`, out, "large int default must not use scientific notation")
	require.Regexp(t, `Minimum:\s+"100"`, out)
	require.Regexp(t, `Maximum:\s+"2000000"`, out)
	require.Regexp(t, `Minimum:\s+"0.5"`, out)
	require.Regexp(t, `Maximum:\s+"2.5"`, out)
	require.Regexp(t, `Enum:\s+\[\]string\{"alpha", "beta"\}`, out)
	require.Regexp(t, `Default:\s+"safe"`, out)
	require.Regexp(t, `Required:\s+true`, out, "sub-key without optional must be required")
	require.Regexp(t, `Description:\s+"A plain option with messy whitespace\."`, out)
	require.Regexp(t, `Minimum:\s+"1"`, out, "sub-key bounds must be emitted")
}

// TestGenerate_Deterministic verifies double generation is byte-identical.
func TestGenerate_Deterministic(t *testing.T) {
	a, _ := generateFixture(t, testConfig())
	b, _ := generateFixture(t, testConfig())
	require.Equal(t, string(a), string(b))
}

// TestGenerate_Errors verifies unknown paths and verbs fail loudly.
func TestGenerate_Errors(t *testing.T) {
	raw, err := os.ReadFile("testdata/apidoc_mini.json")
	require.NoError(t, err)

	cfg := testConfig()
	cfg.Path = "/no/such/node"
	_, _, err = generate(raw, cfg)
	require.ErrorContains(t, err, "not found")

	cfg = testConfig()
	cfg.Verb = "POST"
	_, _, err = generate(raw, cfg)
	require.ErrorContains(t, err, "no POST parameter schema")
}
