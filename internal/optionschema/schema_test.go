package optionschema

import (
	"fmt"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestFind(t *testing.T) {
	schemas := []Schema{
		{Name: "mac_prefix", Flag: "mac-prefix"},
		{Name: "console", Flag: "console"},
	}
	require.Same(t, &schemas[0], Find(schemas, "mac_prefix"), "API name lookup")
	require.Same(t, &schemas[0], Find(schemas, "mac-prefix"), "flag spelling lookup")
	require.Nil(t, Find(schemas, "nope"))
}

func TestSuffix(t *testing.T) {
	s := Schema{Enum: []string{"a", "b"}, Default: "a"}
	require.Equal(t, " (values: a, b; default: a)", s.Suffix())

	require.Empty(t, (&Schema{}).Suffix())

	bounded := Schema{Type: "integer", Minimum: "1", Maximum: "50000"}
	require.Equal(t, " (range: 1…50000)", bounded.Suffix())

	enumWins := Schema{Enum: []string{"a"}, Minimum: "1"}
	require.Equal(t, " (values: a)", enumWins.Suffix(), "range is noise when an enum constrains values")

	dict := Schema{SubKeys: []SubKey{{Name: "type"}, {Name: "network"}}}
	require.Equal(t, " (keys: type, network)", dict.Suffix())
}

func TestSuffix_KeyCap(t *testing.T) {
	var subs []SubKey
	for i := range 12 {
		subs = append(subs, SubKey{Name: fmt.Sprintf("k%02d", i)})
	}
	got := (&Schema{SubKeys: subs}).Suffix()
	require.Contains(t, got, "k07")
	require.NotContains(t, got, "k08", "key list must cap at %d entries", suffixKeyCap)
	require.Contains(t, got, "…")
}

func TestDefaultValue(t *testing.T) {
	require.Equal(t, "watchdog", (&Schema{Default: "watchdog"}).DefaultValue())

	dict := Schema{SubKeys: []SubKey{
		{Name: "type", Default: "secure"},
		{Name: "network"},
		{Name: "algo", Default: "zstd"},
	}}
	require.Equal(t, "algo=zstd,type=secure", dict.DefaultValue(), "composed dict default is sorted")

	require.Empty(t, (&Schema{}).DefaultValue())
}

func TestEnrichFlags(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	var v string
	fs.StringVar(&v, "fencing", "", "HA fencing mode")

	EnrichFlags(fs, []Schema{
		{Name: "fencing", Flag: "fencing", Enum: []string{"watchdog", "hardware"}, Default: "watchdog"},
		{Name: "ghost", Flag: "ghost", Default: "x"},
	})
	require.Equal(t, "HA fencing mode (values: watchdog, hardware; default: watchdog)", fs.Lookup("fencing").Usage)
}

func TestMergeDefaults(t *testing.T) {
	schemas := []Schema{
		{Name: "fencing", Flag: "fencing", Default: "watchdog"},
		{Name: "migration", Flag: "migration", SubKeys: []SubKey{{Name: "type", Default: "secure"}}},
		{Name: "console", Flag: "console"},
		{Name: "net[n]", Flag: "net", Indexed: true, Default: "x"},
	}

	single := map[string]string{"fencing": "hardware"}
	got, raw := MergeDefaults(schemas, single, map[string]any{"fencing": "hardware"}, MergeOpts{})

	require.Equal(t, "hardware", got["fencing"], "server-set value must win")
	require.Equal(t, "type=secure (default)", got["migration"])
	require.Equal(t, "(unset)", got["console"])
	require.NotContains(t, got, "net[n]", "indexed schemas are always skipped")

	obj, ok := raw.(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]string{"migration": "type=secure"}, obj["defaults"])
	require.Equal(t, map[string]any{"fencing": "hardware"}, obj["set"])
}

func TestMergeDefaults_SkipUnset(t *testing.T) {
	schemas := []Schema{
		{Name: "cores", Flag: "cores", Default: "1"},
		{Name: "affinity", Flag: "affinity"},
	}
	got, _ := MergeDefaults(schemas, map[string]string{}, nil, MergeOpts{SkipUnset: true})
	require.Equal(t, "1 (default)", got["cores"])
	require.NotContains(t, got, "affinity", "SkipUnset must drop options without a default")
}
