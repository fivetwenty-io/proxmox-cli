package output

import (
	"fmt"
	"io"

	yaml "github.com/goccy/go-yaml"
)

// renderYAML writes res to w as YAML. Follows the same field priority as
// renderJSON: Raw > Single > Rows/Headers > Message.
func renderYAML(w io.Writer, res Result) error {
	var v any

	if res.Raw != nil {
		v = res.Raw
	} else if res.Single != nil {
		// Emit the same {data: {k: v}} shape as renderJSON so JSON and YAML are
		// isomorphic for single-map Results. goccy/go-yaml sorts map keys, giving
		// deterministic output.
		data := make(map[string]string, len(res.Single))
		for k, val := range res.Single {
			data[k] = val
		}
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
