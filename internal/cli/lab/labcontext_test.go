package lab

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLabContextName verifies derived context name formation.
func TestLabContextName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"alpha", "lab-alpha"},
		{"beta", "lab-beta"},
		{"test-lab", "lab-test-lab"},
		{"x", "lab-x"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := labContextName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLabKeychainService verifies keychain service name formation.
func TestLabKeychainService(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"alpha", "pmx-lab-alpha"},
		{"beta", "pmx-lab-beta"},
		{"test-lab", "pmx-lab-test-lab"},
		{"x", "pmx-lab-x"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := labKeychainService(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLabCtxConstants verifies lab context user and token name constants.
func TestLabCtxConstants(t *testing.T) {
	assert.Equal(t, "pmx@pve", labCtxUser)
	assert.Equal(t, "pmx", labCtxTokenName)
}

// TestLabCtxAccount verifies account identifier formation.
func TestLabCtxAccount(t *testing.T) {
	result := labCtxAccount()
	assert.Equal(t, "pmx@pve!pmx", result)
}

// TestParseTokenAddValue_Success verifies extraction of value from JSON
// output: {"value":"<secret>"}.
func TestParseTokenAddValue_Success(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple value",
			`{"value":"secret123"}`,
			"secret123",
		},
		{
			"value with special chars",
			`{"value":"abc!@#$%^&*()_+-=[]{}|;:,.<>?"}`,
			"abc!@#$%^&*()_+-=[]{}|;:,.<>?",
		},
		{
			"value with whitespace",
			`{"value":"sec ret 123"}`,
			"sec ret 123",
		},
		{
			"empty value",
			`{"value":""}`,
			"",
		},
		{
			"long value",
			`{"value":"` + strings.Repeat("a", 256) + `"}`,
			strings.Repeat("a", 256),
		},
		{
			"JSON with extra whitespace",
			`{ "value" : "secret123" }`,
			"secret123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTokenAddValue(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestParseTokenAddValue_InvalidJSON verifies errors on malformed JSON.
func TestParseTokenAddValue_InvalidJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"not JSON", "not json"},
		{"unclosed brace", `{"value":"secret`},
		{"empty string", ""},
		{"just whitespace", "   "},
		{"incomplete object", "{"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTokenAddValue(tt.input)
			require.Error(t, err)
		})
	}
}

// TestParseTokenAddValue_MissingValue verifies errors when JSON lacks value
// key or field is null.
func TestParseTokenAddValue_MissingValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"missing value key", `{"data":"secret"}`},
		{"null value", `{"value":null}`},
		{"empty object", `{}`},
		{"value is object", `{"value":{}}`},
		{"value is array", `{"value":[]}`},
		{"value is number", `{"value":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTokenAddValue(tt.input)
			require.Error(t, err)
		})
	}
}

// TestNormalizeFingerprint_Success verifies stripping of the sha256
// Fingerprint= prefix and validation of colon-hex SHA-256 format.
func TestNormalizeFingerprint_Success(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"with sha256 Fingerprint prefix",
			"sha256 Fingerprint=ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
			"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		},
		{
			"SHA256 uppercase",
			"SHA256 Fingerprint=AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90",
			"AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90",
		},
		{
			"just the hash without prefix",
			"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
			"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		},
		{
			"mixed case hex",
			"sha256 Fingerprint=aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90",
			"aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90:aB:Cd:eF:12:34:56:78:90",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := normalizeFingerprint(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNormalizeFingerprint_InvalidFormat verifies errors on invalid
// fingerprint format: non-colon-separated hex, invalid hex chars, wrong
// length, missing or extra colons.
func TestNormalizeFingerprint_InvalidFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no colons", "abcdef123456"},
		{"invalid hex chars", "ab:cd:ef:12:34:56:78:90:xz:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90"},
		{"too many colons", "ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab"},
		{"too few colons", "ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef"},
		{"space instead of colon", "ab cd ef 12 34 56 78 90 ab cd ef 12 34 56 78 90 ab cd ef 12 34 56 78 90 ab cd ef 12 34 56 78 90"},
		{"single digit parts", "a:b:c:d:e:f:1:2:3:4:5:6:7:8:9:0:a:b:c:d:e:f:1:2:3:4:5:6:7:8:9:0"},
		{"three digit parts", "abc:def:123:456:789:012:345:678:90a:bcd:ef1:234:567:890:abc:def:123:456:789:012:345:678:90a:bcd:ef1:234:567:890:abc"},
		{"missing prefix but wrong format", "sha256 Fingerprint=not-hex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeFingerprint(tt.input)
			require.Error(t, err, "expected error for input: %q", tt.input)
		})
	}
}

// TestFingerprintRE_Matches verifies the regexp matches valid SHA-256
// fingerprints in colon-hex format.
func TestFingerprintRE_Matches(t *testing.T) {
	tests := []string{
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		"AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90:AB:CD:EF:12:34:56:78:90",
		"00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00",
		"ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff:ff",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			assert.True(t, fingerprintRE.MatchString(tt),
				"expected fingerprintRE to match %q", tt)
		})
	}
}

// TestFingerprintRE_Rejects verifies the regexp rejects invalid formats.
func TestFingerprintRE_Rejects(t *testing.T) {
	tests := []string{
		"",
		"ab",
		"ab:cd",
		"ab:cd:ef:12:34:56:78:90",
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78",
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:99",
		"xb:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		"ab-cd-ef-12-34-56-78-90-ab-cd-ef-12-34-56-78-90-ab-cd-ef-12-34-56-78-90-ab-cd-ef-12-34-56-78-90",
		" ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90",
		"ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef:12:34:56:78:90 ",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			assert.False(t, fingerprintRE.MatchString(tt),
				"expected fingerprintRE to reject %q", tt)
		})
	}
}
