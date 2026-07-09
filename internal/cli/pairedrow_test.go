package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

type pairedRowEntry struct {
	Name string `json:"name"`
}

// TestDecodePairedRows_PairsStayAligned verifies that each decoded entry and
// its raw map describe the same element at the same index, in item order.
func TestDecodePairedRows_PairsStayAligned(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"name":"b","extra":1}`),
		json.RawMessage(`{"name":"a","extra":2}`),
	}

	rows, err := cli.DecodePairedRows[pairedRowEntry](items, "widget")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != len(items) {
		t.Fatalf("got %d rows, want %d", len(rows), len(items))
	}

	want := []struct {
		name  string
		extra float64
	}{
		{"b", 1},
		{"a", 2},
	}
	for i, w := range want {
		if rows[i].Entry.Name != w.name {
			t.Errorf("rows[%d].Entry.Name = %q, want %q", i, rows[i].Entry.Name, w.name)
		}
		if rows[i].Raw["name"] != w.name {
			t.Errorf("rows[%d].Raw[name] = %v, want %q", i, rows[i].Raw["name"], w.name)
		}
		if rows[i].Raw["extra"] != w.extra {
			t.Errorf("rows[%d].Raw[extra] = %v, want %v", i, rows[i].Raw["extra"], w.extra)
		}
	}
}

// TestDecodePairedRows_MalformedElementErrors verifies that a malformed
// element produces a hard error naming what, rather than a partial result.
func TestDecodePairedRows_MalformedElementErrors(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"name":"ok"}`),
		json.RawMessage(`not json`),
	}

	rows, err := cli.DecodePairedRows[pairedRowEntry](items, "widget")
	if err == nil {
		t.Fatal("expected error for malformed element, got nil")
	}
	if rows != nil {
		t.Errorf("expected nil rows on error, got %v", rows)
	}
	if !strings.Contains(err.Error(), "decode widget entry") {
		t.Errorf("error %q does not contain %q", err.Error(), "decode widget entry")
	}
}

// TestDecodePairedRows_Empty verifies that an empty input yields an empty,
// non-nil slice and no error.
func TestDecodePairedRows_Empty(t *testing.T) {
	rows, err := cli.DecodePairedRows[pairedRowEntry](nil, "widget")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}
