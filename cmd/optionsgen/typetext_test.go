package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// wantSubKey describes the expected shape of one sub-key parsed from a
// typetext.
type wantSubKey struct {
	typ      string
	enum     []string
	required bool
}

// TestTypetextSubKeys verifies the typetext parser over the shapes the PVE
// apidoc uses: required and optional components, default-key forms, boolean,
// number, and integer placeholders, literal enums, numeric ranges that stay
// plain strings, and unparseable input that yields no sub-keys.
func TestTypetextSubKeys(t *testing.T) {
	tests := []struct {
		name     string
		typetext string
		want     map[string]wantSubKey
	}{
		{
			name:     "node location",
			typetext: "latitude=<number> ,longitude=<number> [,name=<name>]",
			want: map[string]wantSubKey{
				"latitude":  {typ: "number", required: true},
				"longitude": {typ: "number", required: true},
				"name":      {typ: "string"},
			},
		},
		{
			name:     "prune counts",
			typetext: "[keep-all=<1|0>] [,keep-daily=<N>] [,keep-last=<N>]",
			want: map[string]wantSubKey{
				"keep-all":   {typ: "boolean"},
				"keep-daily": {typ: "integer"},
				"keep-last":  {typ: "integer"},
			},
		},
		{
			name: "cpu with default key and numeric range",
			typetext: "[[cputype=]<string>] [,flags=<+FLAG[;-FLAG...]>] [,guest-phys-bits=<integer>] " +
				"[,hidden=<1|0>] [,phys-bits=<8-64|host>] [,reported-model=<enum>]",
			want: map[string]wantSubKey{
				"cputype":         {typ: "string"},
				"flags":           {typ: "string"},
				"guest-phys-bits": {typ: "integer"},
				"hidden":          {typ: "boolean"},
				"phys-bits":       {typ: "string"},
				"reported-model":  {typ: "string"},
			},
		},
		{
			name:     "required default key and required plain key",
			typetext: "[type=]<tdx-type> ,attestation=<1|0> [,vsock-cid=<integer>]",
			want: map[string]wantSubKey{
				"type":        {typ: "string", required: true},
				"attestation": {typ: "boolean", required: true},
				"vsock-cid":   {typ: "integer"},
			},
		},
		{
			name:     "host pci with list value and enum",
			typetext: "[[host=]<HOSTPCIID[;HOSTPCIID2...]>] [,driver=<vfio|keep>] [,device-id=<hex id>]",
			want: map[string]wantSubKey{
				"host":      {typ: "string"},
				"driver":    {typ: "string", enum: []string{"vfio", "keep"}},
				"device-id": {typ: "string"},
			},
		},
		{
			name:     "rng with device path enum",
			typetext: "[source=]</dev/urandom|/dev/random|/dev/hwrng> [,max_bytes=<integer>]",
			want: map[string]wantSubKey{
				"source":    {typ: "string", enum: []string{"/dev/urandom", "/dev/random", "/dev/hwrng"}, required: true},
				"max_bytes": {typ: "integer"},
			},
		},
		{
			name:     "startup without angle brackets and trailing space",
			typetext: `[[order=]\d+] [,up=\d+] [,down=\d+] `,
			want: map[string]wantSubKey{
				"order": {typ: "integer"},
				"up":    {typ: "integer"},
				"down":  {typ: "integer"},
			},
		},
		{
			name:     "watchdog enum and bare enum word",
			typetext: "[[model=]<i6300esb|ib700>] [,action=<enum>]",
			want: map[string]wantSubKey{
				"model":  {typ: "string", enum: []string{"i6300esb", "ib700"}},
				"action": {typ: "string"},
			},
		},
		{
			name:     "boot with bracketed character class",
			typetext: "[[legacy=]<[acdn]{1,4}>] [,order=<device[;device...]>]",
			want: map[string]wantSubKey{
				"legacy": {typ: "string"},
				"order":  {typ: "string"},
			},
		},
		{
			name:     "placeholder containing a space",
			typetext: "[enabled=<1|0>] [,storage=<storage ID>]",
			want: map[string]wantSubKey{
				"enabled": {typ: "boolean"},
				"storage": {typ: "string"},
			},
		},
		{
			name:     "unparseable components are skipped",
			typetext: "<storage ID> [,good=<integer>] [=<broken>] [1bad=<integer>]",
			want:     map[string]wantSubKey{"good": {typ: "integer"}},
		},
		{name: "no key value shape", typetext: "<storage ID>"},
		{name: "empty", typetext: ""},
		{name: "unbalanced bracket", typetext: "[,oops=<integer>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := typetextSubKeys(tt.typetext)
			require.Len(t, got, len(tt.want))
			for name, want := range tt.want {
				p, ok := got[name]
				require.True(t, ok, "sub-key %q missing", name)
				require.Equal(t, want.typ, p.Type, name)
				require.Equal(t, want.enum, p.Enum, name)
				require.Equal(t, want.required, !isOptional(p), name)
				require.Empty(t, p.Description, name)
				require.Empty(t, p.Default, name)
				require.Empty(t, p.Minimum, name)
				require.Empty(t, p.Maximum, name)
			}
		})
	}
}

// TestSubKeys_NamedFormatFallback verifies a property whose format is a
// registered format name takes its sub-keys from the typetext, while an
// inline format dict still wins and a property without a format gets none.
func TestSubKeys_NamedFormatFallback(t *testing.T) {
	raw, err := os.ReadFile("testdata/typetext_apidoc_mini.json")
	require.NoError(t, err)
	props, err := loadProperties(raw, "/sites/options", "PUT")
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"latitude", "longitude", "name"}, mapKeys(props["location"].subKeys()))
	require.ElementsMatch(t, []string{"mode"}, mapKeys(props["inline"].subKeys()), "inline dict wins over typetext")
	require.Empty(t, props["alias"].subKeys(), "typetext without key=value components yields nothing")
	require.Empty(t, props["untyped"].subKeys(), "typetext is ignored without a named format")
}

// TestGenerate_NamedFormatSubKeys verifies the rendered table carries the
// typetext-derived sub-keys in lexical order with required markers and enums.
func TestGenerate_NamedFormatSubKeys(t *testing.T) {
	raw, err := os.ReadFile("testdata/typetext_apidoc_mini.json")
	require.NoError(t, err)
	cfg := genConfig{
		Path:   "/sites/options",
		Verb:   "PUT",
		Symbol: "siteSchemas",
		Pkg:    "sites",
		Source: "typetext_apidoc_mini.json",
	}
	src, count, err := generate(raw, cfg)
	require.NoError(t, err)
	out := string(src)

	require.Equal(t, 5, count)
	require.Regexp(t, `Name:\s+"latitude",\s+Type:\s+"number",\s+Required:\s+true`, out)
	require.Regexp(t, `Name:\s+"longitude",\s+Type:\s+"number",\s+Required:\s+true`, out)
	require.Regexp(t, `Name:\s+"name",\s+Type:\s+"string",\s+\}`, out, "optional sub-key carries no Required marker")
	require.Regexp(t, `Name:\s+"model",\s+Type:\s+"string",\s+Enum:\s+\[\]string\{"i6300esb", "ib700"\}`, out)
	require.Regexp(t, `Name:\s+"mode"`, out)
	require.NotContains(t, out, `"other"`, "inline dict suppresses the typetext")
	require.NotContains(t, out, `"key"`, "typetext without a named format is ignored")
}
