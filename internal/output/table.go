package output

import (
	"fmt"
	"io"
	"sort"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
)

// renderTable writes res to w using tablewriter v1.1.4. When ascii is true the
// table uses ASCII border characters instead of Unicode box-drawing glyphs.
//
// Field priority:
//  1. res.Single — rendered as two-column KEY / VALUE table.
//  2. res.Rows   — rendered with res.Headers as column headers.
//  3. res.Message — printed as a plain line when neither Single nor Rows is set.
func renderTable(w io.Writer, res Result, ascii bool) error {
	style := tw.StyleGraphical
	if ascii {
		style = tw.StyleASCII
	}
	symbols := tw.NewSymbols(style)

	if res.Single != nil {
		t := tablewriter.NewTable(w,
			tablewriter.WithSymbols(symbols),
			tablewriter.WithHeader([]string{"KEY", "VALUE"}),
		)

		// Stable key order for deterministic output.
		keys := make([]string, 0, len(res.Single))
		for k := range res.Single {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if err := t.Append(k, res.Single[k]); err != nil {
				return fmt.Errorf("tablewriter append single row: %w", err)
			}
		}
		if err := t.Render(); err != nil {
			return fmt.Errorf("tablewriter render single: %w", err)
		}
		return nil
	}

	if len(res.Rows) > 0 || len(res.Headers) > 0 {
		opts := []tablewriter.Option{tablewriter.WithSymbols(symbols)}
		if len(res.Headers) > 0 {
			opts = append(opts, tablewriter.WithHeader(res.Headers))
		}
		t := tablewriter.NewTable(w, opts...)

		for _, row := range res.Rows {
			irow := make([]any, len(row))
			for i, cell := range row {
				irow[i] = cell
			}
			if err := t.Append(irow...); err != nil {
				return fmt.Errorf("tablewriter append row: %w", err)
			}
		}
		if err := t.Render(); err != nil {
			return fmt.Errorf("tablewriter render: %w", err)
		}
		return nil
	}

	if res.Message != "" {
		_, err := fmt.Fprintln(w, res.Message)
		return err
	}
	return nil
}
