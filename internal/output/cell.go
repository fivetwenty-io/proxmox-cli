package output

import (
	"fmt"
	"sort"
	"strings"
)

// nestedSummaryRunes is how much of a cell a nested value may spend before it
// is described by its shape rather than spelled out. A small object reads
// better inline than as a count; a large one reads as neither, and
// marshalling it whole is what made a cell 515,739 columns wide.
const nestedSummaryRunes = 96

// nestedSummaryDepth is how far into a payload a cell follows before it stops
// spelling values out. Two levels covers the shapes PVE actually nests (a
// list of small records, a record of small records) without letting a deep
// tree back into the cell.
const nestedSummaryDepth = 2

// Cell renders one decoded JSON value as one table cell.
//
// A scalar renders as itself, with a bool as yes/no and a whole float without
// its fractional part, since PVE sends every number as a float64 through
// encoding/json. A nested value is summarised rather than marshalled: a small
// object spells out its fields as "k=v", a small array lists its elements,
// and anything larger or deeper is described by its shape. The full value
// stays available through -o json and -o yaml.
func Cell(v any) string {
	return summarize(v, 0)
}

// summarize renders v at the given nesting depth. A container below the top
// level is bracketed, so "devices=[{dev=/dev/sdb}]" reads unambiguously even
// though its parts are separated by the same characters as its parent's.
func summarize(v any, depth int) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case map[string]any:
		return objectCell(t, depth)
	case []any:
		return arrayCell(t, depth)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// objectCell renders a nested object as "k=v k=v" when that is short enough,
// and as its field count otherwise.
func objectCell(m map[string]any, depth int) string {
	if len(m) == 0 {
		return ""
	}
	shape := fmt.Sprintf("{%s}", plural(len(m), "field"))
	if depth >= nestedSummaryDepth {
		return shape
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+summarize(m[k], depth+1))
	}
	return bracket(strings.Join(parts, " "), shape, depth, "{", "}")
}

// arrayCell renders a nested array as a comma-separated list when that is
// short enough, and as its length otherwise.
func arrayCell(a []any, depth int) string {
	if len(a) == 0 {
		return ""
	}
	shape := fmt.Sprintf("[%s]", plural(len(a), "item"))
	if depth >= nestedSummaryDepth {
		return shape
	}

	parts := make([]string, 0, len(a))
	for _, e := range a {
		parts = append(parts, summarize(e, depth+1))
	}
	return bracket(strings.Join(parts, ", "), shape, depth, "[", "]")
}

// bracket returns the spelled-out summary when it fits, wrapped in its
// delimiters below the top level, and the shape description when it does not.
func bracket(joined, shape string, depth int, open, close string) string {
	if depth > 0 {
		joined = open + joined + close
	}
	if len([]rune(joined)) > nestedSummaryRunes {
		return shape
	}
	return joined
}

// plural renders a count with its noun, so a summary reads as English.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
