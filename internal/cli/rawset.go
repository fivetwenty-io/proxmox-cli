package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// KeyValue is one parsed `--set KEY=VALUE` pair, in command-line order.
type KeyValue struct {
	Key   string
	Value string
}

// ParseKeyValues parses repeatable --set KEY=VALUE flag values into an
// ordered list of key/value pairs. Each value is split on the first '=';
// entries with no '=', an empty key, or a key repeated within the same
// invocation are rejected as almost certainly typos rather than intentional
// overwrites.
func ParseKeyValues(vals []string) ([]KeyValue, error) {
	out := make([]KeyValue, 0, len(vals))
	seen := make(map[string]bool, len(vals))
	for _, v := range vals {
		key, val, ok := strings.Cut(v, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --set %q: want KEY=VALUE", v)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid --set %q: key must not be empty", v)
		}
		if seen[key] {
			return nil, fmt.Errorf("invalid --set: key %q specified more than once", key)
		}
		seen[key] = true
		out = append(out, KeyValue{Key: key, Value: val})
	}
	return out, nil
}

// ParamsToMap marshals a typed API params struct into a map[string]any using
// the same JSON round-trip the generated apiclient-go bindings perform
// before calling PutRawCtx/PostRawCtx: json.Marshal (which runs any custom
// MarshalJSON the params type defines, expanding indexed map[int]string
// fields into net0, net1, … keys), then decode with a json.Decoder in
// UseNumber mode so integers survive as json.Number rather than float64.
// Overlaying --set pairs onto the resulting map and sending it through the
// raw transport reproduces exactly the body the typed Update/Create method
// would otherwise have sent.
func ParamsToMap(params any) (map[string]any, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	var body map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return nil, fmt.Errorf("decode params: %w", err)
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

// OverlayKeyValues overlays --set KEY=VALUE pairs onto body (produced by
// ParamsToMap), returning the same map mutated in place.
//
// It errors when a key was already populated by a dedicated flag in the same
// invocation: `--cores 4 --set cores=8` is a conflicting instruction, not a
// merge, so the caller must pick one spelling. When isKnown is non-nil, it is
// consulted for every key; keys it reports false for get a stderr note
// (never an error — a config key the CLI's offline schema does not know
// about yet is still sent to the API, since surfacing new PVE options is the
// point of the escape hatch).
func OverlayKeyValues(
	stderr io.Writer, body map[string]any, kvs []KeyValue, isKnown func(key string) bool,
) (map[string]any, error) {
	for _, kv := range kvs {
		if existing, ok := body[kv.Key]; ok && existing != nil {
			return nil, fmt.Errorf(
				"--set %s=%s collides with --%s, which is also set in this invocation: use only one",
				kv.Key, kv.Value, kv.Key)
		}
		if isKnown != nil && !isKnown(kv.Key) {
			_, _ = fmt.Fprintf(stderr,
				"note: %q is not in this CLI's known config schema; sending it anyway\n", kv.Key)
		}
		body[kv.Key] = kv.Value
	}
	return body, nil
}
