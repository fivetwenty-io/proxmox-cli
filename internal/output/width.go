package output

import (
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// WidthUnbounded is the maxWidth value that disables the layout budget, so
// columns keep their natural width. It is what --wide selects. maxCellRunes
// still applies: an unbounded table is a wide table, not a 200 KB one.
const WidthUnbounded = -1

// maxCellRunes caps the width of any single line of any cell, whatever the
// layout budget and whether or not output is a terminal.
//
// It is unconditional because the pathological case is not a terminal at all:
// `pmx pve node ceph status` returns a payload the generic renderer marshals
// into one cell, and tablewriter pads every other row to that cell's width,
// so the command wrote 3.95 MB and a 515,739-column line into a redirect.
// The full value is always available through -o json and -o yaml, which this
// layer never touches.
const maxCellRunes = 512

// minColumnRunes is the floor a column is never shrunk below. A budget too
// small to give every column this much is honoured as far as it goes and then
// exceeded: an unreadably narrow column helps nobody.
const minColumnRunes = 8

// ellipsis marks a cell whose line was shortened. One rune, so it costs a
// column rather than three.
const ellipsis = '…'

// tableFrameRunes is what tablewriter spends per column on borders and
// padding ("│ " before each cell and " " after), plus the closing "│".
const (
	tableFrameRunes      = 3
	tableFrameEdgeRunes  = 1
	singleKeyColumnIndex = 0
)

// budget resolves the layout width to lay out for, in columns.
//
// maxWidth is the caller's preference: a positive value is used as given,
// WidthUnbounded disables the budget, and 0 means "detect". Detection prefers
// $COLUMNS, which lets a test or a CI sweep pin a width without a pty, and
// falls back to the terminal size of w. A non-terminal writer (a pipe, a
// redirect, a test buffer) has no budget, so piped output keeps its columns.
func budget(w io.Writer, maxWidth int) int {
	if maxWidth != 0 {
		if maxWidth < 0 {
			return 0
		}
		return maxWidth
	}

	if cols, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && cols > 0 {
		return cols
	}

	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil || width <= 0 {
		return 0
	}
	return width
}

// clamp shortens cells so the rendered table fits within width columns, and
// so no line of any cell exceeds maxCellRunes regardless of width. A width of
// 0 applies only the per-cell cap.
//
// It returns a copy: Raw is carried through untouched, so -o json and -o yaml
// still render the full value.
func clamp(res Result, width int) Result {
	if res.Single != nil {
		return clampSingle(res, width)
	}
	if len(res.Rows) == 0 && len(res.Headers) == 0 {
		return res
	}
	return clampRows(res, width)
}

// clampRows clamps a Headers/Rows table. Headers are measured alongside the
// rows and shortened with them: a header wider than its column would set the
// column width on its own.
func clampRows(res Result, width int) Result {
	cols := len(res.Headers)
	for _, row := range res.Rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return res
	}

	natural := make([]int, cols)
	measure := func(row []string) {
		for i, cell := range row {
			natural[i] = max(natural[i], min(cellWidth(cell), maxCellRunes))
		}
	}
	measure(res.Headers)
	for _, row := range res.Rows {
		measure(row)
	}

	limits := columnLimits(natural, nil, width)

	out := res
	out.Headers = clampRow(res.Headers, limits)
	out.Rows = make([][]string, len(res.Rows))
	for i, row := range res.Rows {
		out.Rows[i] = clampRow(row, limits)
	}
	return out
}

// clampSingle clamps a KEY/VALUE table. The key column is exempt from the
// budget: a key shortened to "sma" or "hug" names nothing, and the value is
// where the width goes anyway.
func clampSingle(res Result, width int) Result {
	natural := []int{len("KEY"), len("VALUE")}
	for k, v := range res.Single {
		natural[0] = max(natural[0], min(cellWidth(k), maxCellRunes))
		natural[1] = max(natural[1], min(cellWidth(v), maxCellRunes))
	}

	limits := columnLimits(natural, []int{singleKeyColumnIndex}, width)

	out := res
	out.Single = make(map[string]string, len(res.Single))
	for k, v := range res.Single {
		out.Single[clampCell(k, limits[0])] = clampCell(v, limits[1])
	}
	return out
}

// columnLimits resolves the width each column is allowed, given its natural
// width and the total budget. Columns listed in exempt keep their natural
// width and are charged against the budget before the rest share what is
// left.
//
// The shareable columns are levelled rather than trimmed proportionally: the
// widest column gives up the most, and a column already narrower than the
// level keeps its width. That is what stops one 4,000-rune cell from
// squeezing every other column in the table.
func columnLimits(natural []int, exempt []int, width int) []int {
	limits := make([]int, len(natural))
	copy(limits, natural)
	if width <= 0 {
		return limits
	}

	isExempt := make([]bool, len(natural))
	for _, i := range exempt {
		if i >= 0 && i < len(isExempt) {
			isExempt[i] = true
		}
	}

	avail := width - tableFrameRunes*len(natural) - tableFrameEdgeRunes
	shareable := 0
	for i, n := range natural {
		if isExempt[i] {
			avail -= n
			continue
		}
		shareable++
	}
	if shareable == 0 {
		return limits
	}
	if avail < shareable*minColumnRunes {
		avail = shareable * minColumnRunes
	}

	// Highest level at which the shareable columns fit, found by bisection
	// rather than by shaving a rune at a time: a 515,739-rune cell makes the
	// linear form a half-million iterations.
	lo, hi := minColumnRunes, 0
	for i, n := range natural {
		if !isExempt[i] {
			hi = max(hi, n)
		}
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		total := 0
		for i, n := range natural {
			if !isExempt[i] {
				total += min(n, mid)
			}
		}
		if total <= avail {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	for i := range limits {
		if !isExempt[i] {
			limits[i] = min(limits[i], lo)
		}
	}
	return limits
}

// clampRow applies limits to one row, tolerating a row shorter than the
// header (some renderers emit ragged rows).
func clampRow(row []string, limits []int) []string {
	if row == nil {
		return nil
	}
	out := make([]string, len(row))
	for i, cell := range row {
		if i < len(limits) {
			out[i] = clampCell(cell, limits[i])
			continue
		}
		out[i] = clampCell(cell, maxCellRunes)
	}
	return out
}

// cellWidth is the width a cell occupies: the longest of its lines, since
// tablewriter lays a multi-line cell out over several rows.
func cellWidth(s string) int {
	w := 0
	for len(s) > 0 {
		line, rest, found := strings.Cut(s, "\n")
		w = max(w, len([]rune(line)))
		if !found {
			break
		}
		s = rest
	}
	return w
}

// clampCell shortens every line of s to at most limit runes, marking each
// shortened line with a single ellipsis rune. limit is additionally capped at
// maxCellRunes, so a caller that passes no budget still gets a bounded cell.
func clampCell(s string, limit int) string {
	limit = min(limit, maxCellRunes)
	if limit < 1 {
		limit = 1
	}
	if cellWidth(s) <= limit {
		return s
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		r := []rune(line)
		if len(r) <= limit {
			continue
		}
		lines[i] = string(append(r[:limit-1:limit-1], ellipsis))
	}
	return strings.Join(lines, "\n")
}
