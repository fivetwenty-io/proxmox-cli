package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// tableJSON is the synthetic JSON object emitted when Result.Raw is nil and
// tabular data (Headers + Rows) is present.
type tableJSON struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// singleJSON is the synthetic JSON object emitted when Result.Raw is nil and
// only Result.Single is set.
type singleJSON struct {
	Data map[string]string `json:"data"`
}

// messageJSON is the synthetic JSON object emitted for message-only Results.
type messageJSON struct {
	Message string `json:"message"`
}

// renderJSON writes res to w as indented JSON. If res.Raw is set it is
// marshalled directly. Otherwise a synthetic structure is built from the
// available fields (Single > Rows/Headers > Message).
func renderJSON(w io.Writer, res Result) error {
	var v any

	if res.Raw != nil {
		v = res.Raw
	} else if res.Single != nil {
		// Build an ordered representation: sort keys for determinism.
		keys := make([]string, 0, len(res.Single))
		for k := range res.Single {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]string, len(res.Single))
		for _, k := range keys {
			ordered[k] = res.Single[k]
		}
		v = singleJSON{Data: ordered}
	} else if len(res.Headers) > 0 || len(res.Rows) > 0 {
		rows := res.Rows
		if rows == nil {
			rows = [][]string{}
		}
		v = tableJSON{
			Headers: res.Headers,
			Rows:    rows,
		}
	} else if res.Message != "" {
		v = messageJSON{Message: res.Message}
	} else {
		v = struct{}{}
	}

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s\n", b); err != nil {
		return fmt.Errorf("json write: %w", err)
	}
	return nil
}
