package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// JournalFilterFlags are the journal filters that GET /nodes/{node}/journal
// accepts on every product (PVE, PBS, and PDM share the parameter set):
// priority, syslog-identifier glob, systemd unit, kernel-only, and the
// structured output switch with its two listing helpers.
type JournalFilterFlags struct {
	Priority    string
	Service     string
	Unit        string
	Kernel      bool
	Structured  bool
	Identifiers bool
	Units       bool
}

// journalPriorityRE is the API's own pattern for the priority filter: empty
// (no filter), a single syslog level, or a LOW..HIGH range of levels.
var journalPriorityRE = regexp.MustCompile(`^([0-7](\.\.[0-7])?)?$`)

// Register binds the filter flags. --service is the syslog identifier glob
// the API calls "service", which is not the systemd unit that `node syslog
// --service` means, so both usages say which one they are.
func (jf *JournalFilterFlags) Register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&jf.Priority, "priority", "",
		"only entries at this syslog priority or more severe: a level from 0 (emerg) to 7 (debug), "+
			"or a LOW..HIGH range such as 0..3")
	f.StringVar(&jf.Service, "service", "",
		"only entries whose syslog identifier matches this glob, for example 'pve*' (not the systemd unit; see --unit)")
	f.StringVar(&jf.Unit, "unit", "", "only entries of this systemd unit (the .service suffix is implied)")
	f.BoolVar(&jf.Kernel, "kernel", false, "only kernel messages")
	f.BoolVar(&jf.Structured, "structured", false,
		"return one record per entry with separate timestamp, priority, pid, identifier, and message fields")
	f.BoolVar(&jf.Identifiers, "identifiers", false,
		"list the distinct syslog identifiers present instead of entries (requires --structured)")
	f.BoolVar(&jf.Units, "units", false,
		"list the distinct systemd units present instead of entries (requires --structured)")
}

// Validate rejects a malformed priority and the two listing flags that the
// server silently ignores without --structured. Only --priority is validated
// client-side because it is the one filter the server rejects with an opaque
// message; the service and unit patterns are left to the server, whose
// errors for them name the parameter.
func (jf *JournalFilterFlags) Validate(fl *pflag.FlagSet) error {
	if fl.Changed("priority") && !journalPriorityRE.MatchString(jf.Priority) {
		return fmt.Errorf("invalid --priority %q: want a level from 0 to 7 or a LOW..HIGH range such as 0..3",
			jf.Priority)
	}
	if jf.Identifiers && !jf.Structured {
		return fmt.Errorf("--identifiers requires --structured")
	}
	if jf.Units && !jf.Structured {
		return fmt.Errorf("--units requires --structured")
	}
	return nil
}

// JournalPath builds the journal path for node, escaping the segment the way
// the generated bindings do so a node name can never alter the endpoint.
func JournalPath(node string) string {
	return "/nodes/" + url.PathEscape(node) + "/journal"
}

// RawGetJSON performs GET path through the raw transport and returns the
// response's data element re-encoded as JSON. A response without data yields
// the literal null. Typed bindings fix the response shape at generation time;
// this is the escape hatch for a parameter (such as journal structured=1)
// that changes the shape of the same endpoint.
func RawGetJSON(ctx context.Context, c pve.Client, path string, params map[string]any) (json.RawMessage, error) {
	resp, err := c.GetRawCtx(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", path, err)
	}
	if resp == nil || resp.Data == nil {
		return json.RawMessage("null"), nil
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return raw, nil
}

// JournalHeaders are the curated columns of a structured journal table. The
// whole decoded array, cursor and host markers included, stays in Raw for
// -o json and -o yaml.
var JournalHeaders = []string{"TIMESTAMP", "PRIORITY", "PID", "IDENTIFIER", "MESSAGE"}

// StructuredJournalResult renders the array GET /nodes/{node}/journal returns
// with structured=1. Entry records carry id, msg, p, pid, and t (microseconds)
// and render one row each. Marker records carry ty ("cursor" with c, "host"
// with h) and are skipped as rows but kept in Raw, so -o json consumers see
// the start and end cursors. With identifiers=1 the array is instead
// [{"ids": [...]}], and with units=1 it is [{"names": [...]}]; those render
// one IDENTIFIER or UNIT row per element.
func StructuredJournalResult(raw json.RawMessage) (output.Result, error) {
	empty := output.Result{Headers: JournalHeaders, Rows: [][]string{}, Raw: []any{}}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return empty, nil
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return output.Result{}, fmt.Errorf("decode structured journal: %w", err)
	}

	records := make([]map[string]any, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return output.Result{}, fmt.Errorf("decode structured journal entry %d: want an object, got %s",
				i, journalKind(item))
		}
		records = append(records, obj)
	}

	if res, ok := journalListing(records); ok {
		res.Raw = items
		return res, nil
	}

	rows := make([][]string, 0, len(records))
	for _, obj := range records {
		if _, marker := obj["ty"]; marker {
			continue
		}
		rows = append(rows, []string{
			journalTimestamp(obj["t"]),
			output.Cell(obj["p"]),
			output.Cell(obj["pid"]),
			output.Cell(obj["id"]),
			output.Cell(obj["msg"]),
		})
	}
	return output.Result{Headers: JournalHeaders, Rows: rows, Raw: items}, nil
}

// journalListing renders the identifiers=1 and units=1 answers: records that
// carry an ids or names array. It reports false when no record carries
// either key, meaning the array holds journal entries instead. A record that
// carries both keys contributes rows to both columns.
func journalListing(records []map[string]any) (output.Result, bool) {
	var (
		ids, names       []any
		hasIDs, hasNames bool
	)
	for _, obj := range records {
		if arr, ok := obj["ids"].([]any); ok {
			hasIDs = true
			ids = append(ids, arr...)
		}
		if arr, ok := obj["names"].([]any); ok {
			hasNames = true
			names = append(names, arr...)
		}
	}
	if !hasIDs && !hasNames {
		return output.Result{}, false
	}

	headers := make([]string, 0, 2)
	if hasIDs {
		headers = append(headers, "IDENTIFIER")
	}
	if hasNames {
		headers = append(headers, "UNIT")
	}
	rows := make([][]string, 0, len(ids)+len(names))
	for _, v := range ids {
		row := []string{output.Cell(v)}
		if hasNames {
			row = append(row, "")
		}
		rows = append(rows, row)
	}
	for _, v := range names {
		row := make([]string, 0, 2)
		if hasIDs {
			row = append(row, "")
		}
		rows = append(rows, append(row, output.Cell(v)))
	}
	return output.Result{Headers: headers, Rows: rows}, true
}

// journalKind names a non-object array element for an error message.
func journalKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case float64, json.Number:
		return "a number"
	case bool:
		return "a bool"
	case []any:
		return "an array"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// journalTimestamp renders an epoch timestamp as RFC3339 UTC, matching the
// epoch cells elsewhere in the CLI. The server sends microseconds; the
// magnitude ladder also accepts nanoseconds, milliseconds, and seconds so a
// unit change on the server degrades to a wrong-looking date rather than a
// date in year 56633. Zero means absent and renders empty. Anything that is
// not a number is printed as it came.
func journalTimestamp(v any) string {
	var n int64
	switch t := v.(type) {
	case nil:
		return ""
	case float64:
		n = int64(t)
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return t.String()
		}
		n = i
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return t
		}
		n = i
	default:
		return output.Cell(v)
	}
	if n == 0 {
		return ""
	}
	var ts time.Time
	switch {
	case n >= 1e17:
		ts = time.Unix(0, n)
	case n >= 1e14:
		ts = time.UnixMicro(n)
	case n >= 1e11:
		ts = time.UnixMilli(n)
	default:
		ts = time.Unix(n, 0)
	}
	return ts.UTC().Format(time.RFC3339)
}
