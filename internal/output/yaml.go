package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"

	yaml "github.com/goccy/go-yaml"
)

// renderYAML writes res to w as YAML. Follows the same field priority as
// renderJSON: Raw > Single > Rows/Headers > Message.
func renderYAML(w io.Writer, res Result) error {
	var v any

	if res.Raw != nil {
		// Raw is frequently the SDK's untouched json.RawMessage (or a pointer,
		// slice, or struct field of one). goccy/go-yaml treats those as plain
		// []byte and emits a sequence of integers, so re-encode through JSON
		// first: the YAML document is then exactly the JSON document `-o json`
		// prints, in the same key order.
		yv, err := yamlValueFromJSON(res.Raw)
		if err != nil {
			return err
		}
		v = yv
	} else if res.Single != nil {
		// Emit the same {data: {k: v}} shape as renderJSON so JSON and YAML are
		// isomorphic for single-map Results. goccy/go-yaml sorts map keys, giving
		// deterministic output.
		data := make(map[string]string, len(res.Single))
		maps.Copy(data, res.Single)
		v = map[string]any{"data": data}
	} else if len(res.Headers) > 0 || len(res.Rows) > 0 {
		rows := res.Rows
		if rows == nil {
			rows = [][]string{}
		}
		v = map[string]any{
			"headers": res.Headers,
			"rows":    rows,
		}
	} else if res.Message != "" {
		v = map[string]string{"message": res.Message}
	} else {
		v = map[string]any{}
	}

	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s", b); err != nil {
		return fmt.Errorf("yaml write: %w", err)
	}
	return nil
}

// yamlValueFromJSON marshals v with encoding/json and rebuilds the document as
// values goccy/go-yaml renders faithfully: objects become yaml.MapSlice (key
// order preserved), arrays become []any, and numbers become int64 when they
// are integral and float64 otherwise, so `1` renders as 1 rather than 1.0 or
// "1".
func yamlValueFromJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: encode raw as json: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	out, err := decodeJSONValue(dec)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: decode raw json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("yaml marshal: decode raw json: trailing data after document")
	}
	return out, nil
}

// decodeJSONValue consumes one complete JSON value from dec.
func decodeJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := yaml.MapSlice{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("object key %v is not a string", kt)
				}
				val, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				obj = append(obj, yaml.MapItem{Key: key, Value: val})
			}
			if _, err := dec.Token(); err != nil { // closing '}'
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // closing ']'
				return nil, err
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter %q", t)
		}
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		if f, err := t.Float64(); err == nil {
			return f, nil
		}
		return t.String(), nil
	default:
		// string, bool, or nil
		return t, nil
	}
}
