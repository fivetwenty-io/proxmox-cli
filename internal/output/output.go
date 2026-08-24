// Package output provides the Result type, Format constants, and Renderer
// interface for structured CLI output across table, ascii, plain, JSON, and
// YAML formats.
package output

import (
	"fmt"
	"io"
)

// Format represents the --output/-o flag value.
type Format string

const (
	// FormatTable renders output as a bordered table using tablewriter.
	FormatTable Format = "table"
	// FormatASCII renders output as a bordered table using ASCII-only border
	// characters instead of Unicode box-drawing glyphs.
	FormatASCII Format = "ascii"
	// FormatPlain renders output as tab-separated columns with no borders.
	FormatPlain Format = "plain"
	// FormatJSON renders output as indented JSON.
	FormatJSON Format = "json"
	// FormatYAML renders output as YAML.
	FormatYAML Format = "yaml"
)

// Result carries structured data for rendering. Callers populate at least one
// of Rows/Single/Raw depending on the command; Message is for success notices
// that have no tabular data.
type Result struct {
	// Headers are column labels used by table and plain renderers.
	Headers []string
	// Rows are the data rows for table and plain renderers.
	Rows [][]string
	// Single is a key→value map rendered as a two-column key/value table.
	Single map[string]string
	// Raw is marshalled verbatim for JSON/YAML; if nil a synthetic object is built.
	Raw any
	// Message is printed as a plain line when format is table or plain and there
	// are no rows/single (e.g. "Created successfully.").
	Message string
}

// Renderer writes a Result to an io.Writer in the specified Format.
type Renderer interface {
	// Render serialises r to w using format f.
	Render(w io.Writer, r Result, f Format) error
}

// defaultRenderer is the concrete Renderer returned by New.
type defaultRenderer struct {
	// maxWidth is the table layout budget in columns: 0 detects one from
	// $COLUMNS or the terminal, WidthUnbounded disables it, and a positive
	// value is used as given. It applies to the table and ascii formats
	// only; plain, JSON, and YAML are never clamped.
	maxWidth int
}

// New returns the default Renderer backed by tablewriter v1.1.4, encoding/json,
// and goccy/go-yaml. Table width is detected from the output stream.
func New() Renderer {
	return &defaultRenderer{}
}

// NewWidth returns the default Renderer with an explicit table layout budget:
// a positive column count, or WidthUnbounded for none. Zero detects one, the
// same as New.
func NewWidth(maxWidth int) Renderer {
	return &defaultRenderer{maxWidth: maxWidth}
}

// Render dispatches to the appropriate format renderer.
func (r *defaultRenderer) Render(w io.Writer, res Result, f Format) error {
	switch f {
	case FormatTable:
		return renderTable(w, clamp(res, budget(w, r.maxWidth)), false)
	case FormatASCII:
		return renderTable(w, clamp(res, budget(w, r.maxWidth)), true)
	case FormatPlain:
		return renderPlain(w, res)
	case FormatJSON:
		return renderJSON(w, res)
	case FormatYAML:
		return renderYAML(w, res)
	default:
		return fmt.Errorf("unknown output format %q: valid formats are table, ascii, plain, json, yaml", f)
	}
}
