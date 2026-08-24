package output

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCell_Scalars pins the rendering of the types encoding/json produces for
// a PVE payload, including a whole float, which arrives for every integer.
func TestCell_Scalars(t *testing.T) {
	assert.Equal(t, "", Cell(nil))
	assert.Equal(t, "online", Cell("online"))
	assert.Equal(t, "yes", Cell(true))
	assert.Equal(t, "no", Cell(false))
	assert.Equal(t, "8006", Cell(float64(8006)))
	assert.Equal(t, "0.25", Cell(0.25))
}

// TestCell_SmallObjectSpellsOutItsFields covers the case the old renderer got
// least wrong and still made unreadable: a two-field object marshalled to
// JSON reads as punctuation, and the same object as "k=v" reads as data.
func TestCell_SmallObjectSpellsOutItsFields(t *testing.T) {
	assert.Equal(t, "hostname=pve1 id=0", Cell(map[string]any{
		"id": float64(0), "hostname": "pve1",
	}))
}

// TestCell_SmallArrayListsItsElements covers the same for a list, including a
// list of small records, which is the shape a Ceph metadata response uses.
func TestCell_SmallArrayListsItsElements(t *testing.T) {
	assert.Equal(t, "a, b", Cell([]any{"a", "b"}))
	assert.Equal(t, "{dev=/dev/sdb}", Cell([]any{map[string]any{"dev": "/dev/sdb"}}))
}

// TestCell_LargeValueIsDescribedNotMarshalled is the defect this exists for:
// `pve node ceph status` returns a payload the old renderer marshalled whole
// into one cell, which wrote 3.95 MB and a 515,739-column line.
func TestCell_LargeValueIsDescribedNotMarshalled(t *testing.T) {
	big := map[string]any{}
	for k := range strings.SplitSeq("abcdefghijklmnopqrstuvwxyz", "") {
		big[k] = strings.Repeat(k, 40)
	}

	assert.Equal(t, "{26 fields}", Cell(big))
	// The elements summarise before the list does, so the list reports their
	// shape while it fits and its own once it does not.
	assert.Equal(t, "{26 fields}, {26 fields}", Cell([]any{big, big}))
	assert.Equal(t, "[9 items]", Cell([]any{big, big, big, big, big, big, big, big, big}))
}

// TestCell_DeepValueStopsAtTheDepthLimit pins the bound that keeps a deep
// tree from reaching the cell one level at a time.
func TestCell_DeepValueStopsAtTheDepthLimit(t *testing.T) {
	got := Cell(map[string]any{
		"a": map[string]any{"b": map[string]any{"c": "d", "e": "f"}},
	})
	assert.Equal(t, "a={b={2 fields}}", got)
}

// TestCell_EmptyContainersRenderBlank covers a column whose value is present
// but carries nothing: "[]" names no more than an empty cell does.
func TestCell_EmptyContainersRenderBlank(t *testing.T) {
	assert.Equal(t, "", Cell([]any{}))
	assert.Equal(t, "", Cell(map[string]any{}))
}

// TestCell_SingularCountReadsAsEnglish covers the one-element case, which
// "1 items" would not.
func TestCell_SingularCountReadsAsEnglish(t *testing.T) {
	long := strings.Repeat("x", 200)
	assert.Equal(t, "[1 item]", Cell([]any{long}))
	assert.Equal(t, "k={1 field}", Cell(map[string]any{"k": map[string]any{"k2": long}}))
}
