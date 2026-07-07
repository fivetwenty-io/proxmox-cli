package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseKeyValues_Basic(t *testing.T) {
	kvs, err := ParseKeyValues([]string{"cores=4", "balloon=2048"})
	require.NoError(t, err)
	require.Equal(t, []KeyValue{{Key: "cores", Value: "4"}, {Key: "balloon", Value: "2048"}}, kvs)
}

func TestParseKeyValues_SplitsOnFirstEquals(t *testing.T) {
	kvs, err := ParseKeyValues([]string{"boot=order=scsi0;net0"})
	require.NoError(t, err)
	require.Equal(t, "boot", kvs[0].Key)
	require.Equal(t, "order=scsi0;net0", kvs[0].Value)
}

func TestParseKeyValues_MissingEquals_Errors(t *testing.T) {
	_, err := ParseKeyValues([]string{"cores4"})
	require.Error(t, err)
	require.ErrorContains(t, err, "want KEY=VALUE")
}

func TestParseKeyValues_EmptyKey_Errors(t *testing.T) {
	_, err := ParseKeyValues([]string{"=4"})
	require.Error(t, err)
	require.ErrorContains(t, err, "key must not be empty")
}

func TestParseKeyValues_DuplicateKey_Errors(t *testing.T) {
	_, err := ParseKeyValues([]string{"cores=4", "cores=8"})
	require.Error(t, err)
	require.ErrorContains(t, err, "specified more than once")
}

func TestParseKeyValues_Empty(t *testing.T) {
	kvs, err := ParseKeyValues(nil)
	require.NoError(t, err)
	require.Empty(t, kvs)
}

type overlayParams struct {
	Cores  *int64  `json:"cores,omitempty"`
	Memory *string `json:"memory,omitempty"`
}

func TestParamsToMap_OmitsNilFields(t *testing.T) {
	cores := int64(4)
	body, err := ParamsToMap(&overlayParams{Cores: &cores})
	require.NoError(t, err)
	require.Contains(t, body, "cores")
	require.NotContains(t, body, "memory")
}

func TestOverlayKeyValues_AddsNewKey(t *testing.T) {
	body := map[string]any{}
	var buf bytes.Buffer
	out, err := OverlayKeyValues(&buf, body, []KeyValue{{Key: "balloon", Value: "2048"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "2048", out["balloon"])
	require.Empty(t, buf.String())
}

func TestOverlayKeyValues_CollisionWithDedicatedFlag_Errors(t *testing.T) {
	body := map[string]any{"cores": json.Number("4")}
	_, err := OverlayKeyValues(&bytes.Buffer{}, body, []KeyValue{{Key: "cores", Value: "8"}}, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "--set cores=8")
	require.ErrorContains(t, err, "--cores")
}

func TestOverlayKeyValues_UnknownKey_WritesNote(t *testing.T) {
	body := map[string]any{}
	var buf bytes.Buffer
	isKnown := func(key string) bool { return key == "cores" }
	_, err := OverlayKeyValues(&buf, body, []KeyValue{{Key: "brand-new-option", Value: "1"}}, isKnown)
	require.NoError(t, err)
	require.Contains(t, buf.String(), `note: "brand-new-option" is not in this CLI's known config schema; sending it anyway`)
}

func TestOverlayKeyValues_KnownKey_NoNote(t *testing.T) {
	body := map[string]any{}
	var buf bytes.Buffer
	isKnown := func(key string) bool { return key == "cores" }
	_, err := OverlayKeyValues(&buf, body, []KeyValue{{Key: "cores", Value: "8"}}, isKnown)
	require.NoError(t, err)
	require.Empty(t, buf.String())
}
