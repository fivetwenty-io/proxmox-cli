package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// widest returns the longest line of s, in runes.
func widest(s string) int {
	w := 0
	for line := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		w = max(w, len([]rune(line)))
	}
	return w
}

// TestRenderTable_BoundsAPathologicalCell covers the defect that made
// `pmx pve node ceph status` write 3.95 MB and a 515,739-column line: the
// generic renderer marshals a nested payload into one cell, tablewriter pads
// every other row to that cell, and nothing anywhere bounded either.
//
// The cap is unconditional, because the pathological case is a redirect
// rather than a terminal.
func TestRenderTable_BoundsAPathologicalCell(t *testing.T) {
	res := Result{
		Headers: []string{"NAME", "PAYLOAD"},
		Rows: [][]string{
			{"health", strings.Repeat("x", 200_000)},
			{"quorum", "ok"},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, New().Render(&buf, res, FormatTable))

	assert.LessOrEqual(t, widest(buf.String()), maxCellRunes+32,
		"no line may approach the width of the raw cell")
	// Every row still pads to the widest cell, so the bound is the cap times
	// the row count rather than the raw 3.95 MB.
	assert.Less(t, buf.Len(), 8_000, "padding must not amplify one cell across every row")
	assert.Contains(t, buf.String(), string(ellipsis), "a shortened cell must say so")
	assert.Contains(t, buf.String(), "quorum", "the other rows must survive intact")
}

// TestRenderTable_FitsTheBudget pins the layout budget: the whole table,
// borders included, stays within the given width.
func TestRenderTable_FitsTheBudget(t *testing.T) {
	res := Result{
		Headers: []string{"NODE", "STATUS", "DESCRIPTION"},
		Rows: [][]string{
			{"lab-ceph-0", "online", strings.Repeat("long ", 200)},
			{"lab-ceph-1", "online", "short"},
		},
	}

	for _, width := range []int{80, 100, 120} {
		var buf bytes.Buffer
		require.NoError(t, NewWidth(width).Render(&buf, res, FormatTable))
		assert.LessOrEqual(t, widest(buf.String()), width, "table must fit a %d-column budget", width)
		assert.Contains(t, buf.String(), "lab-ceph-0", "a narrow column must not lose its content")
		assert.Contains(t, buf.String(), "online")
	}
}

// TestRenderTable_LevelsWidestColumnFirst covers the behaviour tablewriter's
// own WithColumnMax does not have: one oversized column is shortened, and the
// columns that already fit are left alone.
func TestRenderTable_LevelsWidestColumnFirst(t *testing.T) {
	res := Result{
		Headers: []string{"VMID", "NAME", "CONFIG"},
		Rows:    [][]string{{"100", "web-server-01", strings.Repeat("k=v,", 500)}},
	}

	var buf bytes.Buffer
	require.NoError(t, NewWidth(100).Render(&buf, res, FormatTable))

	out := buf.String()
	assert.LessOrEqual(t, widest(out), 100)
	assert.Contains(t, out, "100", "a column that fits keeps its content")
	assert.Contains(t, out, "web-server-01", "a column that fits is never shortened")
}

// TestRenderTable_SingleKeepsItsKeyColumn covers the other thing
// WithColumnMax gets wrong: it truncated the KEY column to "sma" and "hug",
// naming nothing. Keys are exempt from the budget; the value takes the cut.
func TestRenderTable_SingleKeepsItsKeyColumn(t *testing.T) {
	res := Result{Single: map[string]string{
		"a-deliberately-long-key-name": strings.Repeat("v", 5_000),
		"short":                        "ok",
	}}

	var buf bytes.Buffer
	require.NoError(t, NewWidth(80).Render(&buf, res, FormatTable))

	out := buf.String()
	assert.LessOrEqual(t, widest(out), 80)
	assert.Contains(t, out, "a-deliberately-long-key-name", "a key must never be shortened")
	assert.Contains(t, out, "short")
	assert.Contains(t, out, string(ellipsis), "the value takes the cut instead")
}

// TestRenderTable_Wide_KeepsColumnsButStillCaps pins what --wide does and
// does not do: it drops the layout budget, not the per-cell cap.
func TestRenderTable_Wide_KeepsColumnsButStillCaps(t *testing.T) {
	res := Result{
		Headers: []string{"NAME", "PAYLOAD"},
		Rows:    [][]string{{"health", strings.Repeat("x", 50_000)}, {"note", strings.Repeat("y", 300)}},
	}

	var buf bytes.Buffer
	require.NoError(t, NewWidth(WidthUnbounded).Render(&buf, res, FormatTable))

	out := buf.String()
	assert.Greater(t, widest(out), 300, "--wide must not shorten a column to fit a terminal")
	assert.LessOrEqual(t, widest(out), maxCellRunes+32, "--wide must still bound a pathological cell")
	assert.Contains(t, out, strings.Repeat("y", 300), "a cell under the cap is untouched")
}

// TestRender_LosslessFormatsAreNeverClamped is the guarantee the whole width
// policy rests on: the full value stays reachable.
func TestRender_LosslessFormatsAreNeverClamped(t *testing.T) {
	payload := strings.Repeat("x", 200_000)
	res := Result{Headers: []string{"NAME", "PAYLOAD"}, Rows: [][]string{{"health", payload}}}

	for _, f := range []Format{FormatJSON, FormatYAML, FormatPlain} {
		var narrow, wide bytes.Buffer
		require.NoError(t, NewWidth(40).Render(&narrow, res, f))
		require.NoError(t, NewWidth(WidthUnbounded).Render(&wide, res, f))

		assert.Equal(t, wide.String(), narrow.String(), "%s must not vary with the layout budget", f)
		assert.Contains(t, narrow.String(), payload, "%s must carry the full value", f)
	}
}

// TestClamp_MultiLineCellMeasuredByLongestLine covers a cell tablewriter lays
// out over several rows: its width is its longest line, and each line is
// shortened separately.
func TestClamp_MultiLineCellMeasuredByLongestLine(t *testing.T) {
	cell := "short\n" + strings.Repeat("z", 300) + "\nalso short"
	got := clamp(Result{Rows: [][]string{{cell}}}, 60)

	require.Len(t, got.Rows, 1)
	lines := strings.Split(got.Rows[0][0], "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "short", lines[0], "a line already inside the limit is untouched")
	assert.Equal(t, "also short", lines[2])
	assert.LessOrEqual(t, len([]rune(lines[1])), 60)
	assert.True(t, strings.HasSuffix(lines[1], string(ellipsis)))
}

// TestClamp_NoBudget_StillCapsCells pins the unconditional half of the
// policy: a piped or redirected table has no layout budget but is still
// bounded per cell.
func TestClamp_NoBudget_StillCapsCells(t *testing.T) {
	got := clamp(Result{Rows: [][]string{{strings.Repeat("x", 100_000), "ok"}}}, 0)
	assert.Equal(t, maxCellRunes, len([]rune(got.Rows[0][0])))
	assert.Equal(t, "ok", got.Rows[0][1])
}

// TestClamp_HeadersShortenWithTheirColumn covers a header wider than any of
// its cells, which would otherwise set the column width on its own.
func TestClamp_HeadersShortenWithTheirColumn(t *testing.T) {
	got := clamp(Result{
		Headers: []string{strings.Repeat("H", 400), "B"},
		Rows:    [][]string{{"a", "b"}},
	}, 60)

	assert.LessOrEqual(t, len([]rune(got.Headers[0])), 60)
	assert.Equal(t, "B", got.Headers[1])
}

// TestColumnLimits_NeverBelowTheFloor covers a budget too small to satisfy:
// the columns settle at the floor rather than collapsing to nothing.
func TestColumnLimits_NeverBelowTheFloor(t *testing.T) {
	limits := columnLimits([]int{400, 400, 400}, nil, 20)
	for _, l := range limits {
		assert.Equal(t, minColumnRunes, l)
	}
}

// TestBudget_ExplicitPreferenceWins pins the resolution order budget applies
// to its maxWidth argument.
func TestBudget_ExplicitPreferenceWins(t *testing.T) {
	t.Setenv("COLUMNS", "200")

	assert.Equal(t, 90, budget(&bytes.Buffer{}, 90), "a positive preference is used as given")
	assert.Equal(t, 0, budget(&bytes.Buffer{}, WidthUnbounded), "--wide disables the budget")
	assert.Equal(t, 200, budget(&bytes.Buffer{}, 0), "detection reads $COLUMNS")

	t.Setenv("COLUMNS", "")
	assert.Equal(t, 0, budget(&bytes.Buffer{}, 0), "a non-terminal writer has no budget")
}

// TestRenderTable_HeaderSeparatorsCountAgainstTheBudget covers the miss that
// let a table overrun its budget by two columns per separator: the renderer
// lays "WEB-URL" out as "WEB - URL", so measuring the header as given
// undersizes its column and every border after it shifts right.
func TestRenderTable_HeaderSeparatorsCountAgainstTheBudget(t *testing.T) {
	res := Result{
		Headers: []string{"ID", "TYPE", "AUTHID", "NODES", "WEB-URL"},
		Rows: [][]string{
			{"dbell", "pve", "root@pam!pdm", strings.Repeat("10.110.0.10,fingerprint=9D:ED,", 40), ""},
			{"drhu", "pve", "root@pam!pdm", "10.112.0.10", ""},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, NewWidth(120).Render(&buf, res, FormatTable))

	assert.LessOrEqual(t, widest(buf.String()), 120)
	assert.Contains(t, buf.String(), "WEB - URL", "the header is laid out with its separator spaced")
}

// TestClampHeader_ShortenedHeaderStillFits covers the second-order version of
// the same thing: the ellipsis a shortened header ends with is itself spaced
// out, so cutting a header to the limit can leave it two columns over.
func TestClampHeader_ShortenedHeaderStillFits(t *testing.T) {
	for _, limit := range []int{8, 10, 12} {
		got := clampHeader("PRESSURE-CPU-SOME", limit)
		assert.LessOrEqual(t, headerWidth(got), limit, "limit %d", limit)
	}
}
