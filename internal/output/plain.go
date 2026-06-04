package output

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// renderPlain writes res to w as tab-separated columns with no borders.
// A header line is emitted when res.Headers is non-empty.
// For res.Single, keys are sorted and each entry is printed as "KEY\tVALUE".
// For res.Message (no rows/single), the message is printed as a single line.
func renderPlain(w io.Writer, res Result) error {
	// Use text/tabwriter to align columns.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if res.Single != nil {
		keys := make([]string, 0, len(res.Single))
		for k := range res.Single {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, err := fmt.Fprintf(tw, "%s\t%s\n", k, res.Single[k]); err != nil {
				return fmt.Errorf("plain render single: %w", err)
			}
		}
		return tw.Flush()
	}

	if len(res.Headers) > 0 || len(res.Rows) > 0 {
		if len(res.Headers) > 0 {
			if _, err := fmt.Fprintln(tw, strings.Join(res.Headers, "\t")); err != nil {
				return fmt.Errorf("plain render headers: %w", err)
			}
		}
		for _, row := range res.Rows {
			if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
				return fmt.Errorf("plain render row: %w", err)
			}
		}
		return tw.Flush()
	}

	if res.Message != "" {
		_, err := fmt.Fprintln(w, res.Message)
		return err
	}
	return nil
}
