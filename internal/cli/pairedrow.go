package cli

import (
	"encoding/json"
	"fmt"
)

// PairedRow couples one decoded list entry with the raw JSON object it was
// decoded from, so sorted list output keeps Rows[i] and Raw[i] describing
// the same API entry.
type PairedRow[T any] struct {
	Entry T
	Raw   map[string]any
}

// DecodePairedRows unmarshals every item into both T and a generic map,
// returning a hard error on the first malformed element rather than
// silently dropping it — a partially decoded list must never be mistaken
// for a complete one. what names the entry kind in error messages.
func DecodePairedRows[T any](items []json.RawMessage, what string) ([]PairedRow[T], error) {
	rows := make([]PairedRow[T], 0, len(items))
	for _, item := range items {
		var e T
		if err := json.Unmarshal(item, &e); err != nil {
			return nil, fmt.Errorf("decode %s entry: %w", what, err)
		}
		var m map[string]any
		if err := json.Unmarshal(item, &m); err != nil {
			return nil, fmt.Errorf("decode %s entry: %w", what, err)
		}
		rows = append(rows, PairedRow[T]{Entry: e, Raw: m})
	}
	return rows, nil
}
