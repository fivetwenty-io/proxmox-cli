// Package aptview holds what the PVE and PBS "node apt" history and changelog
// commands share. Both products serve the same five endpoints with the same
// parameters and response shapes, so one set of flags, one parameter mapping,
// and one set of renderers serve both personas.
package aptview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvenodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/epoch"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TermlogFormats are the formats the terminal log can come in.
var TermlogFormats = []string{"plain", "styled"}

// ---- history ----------------------------------------------------------------

// HistoryFlags binds and collects the flags of `apt history`.
type HistoryFlags struct {
	limit        int64
	pkg          string
	since, until string
}

// BindHistory adds the history flags to cmd.
func BindHistory(cmd *cobra.Command) *HistoryFlags {
	f := &HistoryFlags{}
	fl := cmd.Flags()
	fl.Int64Var(&f.limit, "limit", 0, "list at most this many transactions")
	fl.StringVar(&f.pkg, "package", "", "only list transactions that changed this package")
	fl.StringVar(&f.since, "since", "", "only transactions that started at or after this time, as a UNIX epoch or an RFC 3339 time")
	fl.StringVar(&f.until, "until", "", "only transactions that started at or before this time, as a UNIX epoch or an RFC 3339 time")
	return f
}

// History is the collected history filter.
type History struct {
	Limit, Since, Until *int64
	Package             *string
}

// Collect reads the flags that were given.
func (f *HistoryFlags) Collect(cmd *cobra.Command) (*History, error) {
	fl := cmd.Flags()
	h := &History{}
	if fl.Changed("limit") {
		h.Limit = &f.limit
	}
	if fl.Changed("package") {
		h.Package = &f.pkg
	}
	for _, p := range []struct {
		name string
		src  string
		dst  **int64
	}{{"since", f.since, &h.Since}, {"until", f.until, &h.Until}} {
		if fl.Changed(p.name) {
			n, err := epoch.Parse(p.name, p.src)
			if err != nil {
				return nil, err
			}
			*p.dst = &n
		}
	}
	return h, nil
}

// PVE maps the filter onto the PVE parameters.
func (h *History) PVE() *pvenodes.ListAptHistoryParams {
	return &pvenodes.ListAptHistoryParams{Limit: h.Limit, Package: h.Package, Since: h.Since, Until: h.Until}
}

// PBS maps the filter onto the PBS parameters.
func (h *History) PBS() *pbsnodes.ListAptHistoryParams {
	return &pbsnodes.ListAptHistoryParams{Limit: h.Limit, Package: h.Package, Since: h.Since, Until: h.Until}
}

// ---- installed changelog ----------------------------------------------------

// InstalledFlags binds and collects the flags of `apt installed-changelog`.
type InstalledFlags struct {
	name, sinceVersion, toVersion string
}

// BindInstalled adds the installed-changelog flags to cmd.
func BindInstalled(cmd *cobra.Command) *InstalledFlags {
	f := &InstalledFlags{}
	fl := cmd.Flags()
	fl.StringVar(&f.name, "name", "", "name of the installed package (required)")
	fl.StringVar(&f.sinceVersion, "since-version", "", "only the entries of versions later than this one")
	fl.StringVar(&f.toVersion, "to-version", "", "only the entries up to and including this version")
	return f
}

// Installed is the collected installed-changelog filter.
type Installed struct {
	Name                    string
	SinceVersion, ToVersion *string
}

// Collect reads the flags that were given.
func (f *InstalledFlags) Collect(cmd *cobra.Command) *Installed {
	fl := cmd.Flags()
	i := &Installed{Name: f.name}
	if fl.Changed("since-version") {
		i.SinceVersion = &f.sinceVersion
	}
	if fl.Changed("to-version") {
		i.ToVersion = &f.toVersion
	}
	return i
}

// PVE maps the filter onto the PVE parameters.
func (i *Installed) PVE() *pvenodes.ListAptInstalledChangelogParams {
	return &pvenodes.ListAptInstalledChangelogParams{Name: i.Name, SinceVersion: i.SinceVersion, ToVersion: i.ToVersion}
}

// PBS maps the filter onto the PBS parameters.
func (i *Installed) PBS() *pbsnodes.ListAptInstalledChangelogParams {
	return &pbsnodes.ListAptInstalledChangelogParams{Name: i.Name, SinceVersion: i.SinceVersion, ToVersion: i.ToVersion}
}

// ---- transactions (termlog and transaction changelogs) ----------------------

// TransactionFlags binds and collects the flags that name one transaction of the
// history, by its start time and index.
type TransactionFlags struct {
	startTime, format string
	startIndex        int64
	withFormat        bool
}

// BindTransaction adds --start-time and --start-index to cmd, and --format when
// withFormat is set. --start-time is required.
func BindTransaction(cmd *cobra.Command, withFormat bool) *TransactionFlags {
	f := &TransactionFlags{withFormat: withFormat}
	fl := cmd.Flags()
	fl.StringVar(&f.startTime, "start-time", "", "start time of the transaction as listed in 'apt history', as a UNIX epoch or an RFC 3339 time (required)")
	fl.Int64Var(&f.startIndex, "start-index", 0, "start index of the transaction as listed in 'apt history'")
	if withFormat {
		fl.StringVar(&f.format, "format", "", "output format, plain or styled (styled keeps bold, dim, and basic colors as escape sequences)")
		_ = cmd.RegisterFlagCompletionFunc("format", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return TermlogFormats, cobra.ShellCompDirectiveNoFileComp
		})
	}
	cli.MustMarkRequired(cmd, "start-time")
	return f
}

// Transaction is the collected transaction selector.
type Transaction struct {
	StartTime  int64
	StartIndex *int64
	Format     *string
}

// Collect reads the flags and validates them.
func (f *TransactionFlags) Collect(cmd *cobra.Command) (*Transaction, error) {
	fl := cmd.Flags()
	start, err := epoch.Parse("start-time", f.startTime)
	if err != nil {
		return nil, err
	}
	t := &Transaction{StartTime: start}
	if fl.Changed("start-index") {
		t.StartIndex = &f.startIndex
	}
	if f.withFormat && fl.Changed("format") {
		ok := false
		for _, v := range TermlogFormats {
			ok = ok || v == f.format
		}
		if !ok {
			return nil, fmt.Errorf("invalid --format %q: want plain or styled", f.format)
		}
		t.Format = &f.format
	}
	return t, nil
}

// PVETermlog maps the selector onto the PVE termlog parameters.
func (t *Transaction) PVETermlog() *pvenodes.ListAptTermlogParams {
	return &pvenodes.ListAptTermlogParams{StartTime: t.StartTime, StartIndex: t.StartIndex, Format: t.Format}
}

// PBSTermlog maps the selector onto the PBS termlog parameters.
func (t *Transaction) PBSTermlog() *pbsnodes.ListAptTermlogParams {
	return &pbsnodes.ListAptTermlogParams{StartTime: t.StartTime, StartIndex: t.StartIndex, Format: t.Format}
}

// PVEChangelogs maps the selector onto the PVE transaction-changelogs parameters.
func (t *Transaction) PVEChangelogs() *pvenodes.ListAptTransactionChangelogsParams {
	return &pvenodes.ListAptTransactionChangelogsParams{StartTime: t.StartTime, StartIndex: t.StartIndex}
}

// PBSChangelogs maps the selector onto the PBS transaction-changelogs parameters.
func (t *Transaction) PBSChangelogs() *pbsnodes.ListAptTransactionChangelogsParams {
	return &pbsnodes.ListAptTransactionChangelogsParams{StartTime: t.StartTime, StartIndex: t.StartIndex}
}

// ---- rendering --------------------------------------------------------------

func decodeObjects(raws []json.RawMessage, what string) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(raws))
	for _, raw := range raws {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("decode %s: %w", what, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// stamp renders a numeric epoch field as an RFC 3339 UTC time, and anything else as is.
func stamp(v any) string {
	if n, ok := v.(float64); ok {
		return epoch.Format(int64(n))
	}
	return output.Cell(v)
}

// HistoryResult builds the table of package transactions. The packages column
// counts what each transaction did, such as "2 upgrade, 1 install".
func HistoryResult(raws []json.RawMessage, raw any) (output.Result, error) {
	entries, err := decodeObjects(raws, "apt history")
	if err != nil {
		return output.Result{}, err
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			stamp(e["start-time"]), output.Cell(e["start-index"]), stamp(e["end-time"]),
			output.Cell(e["command-line"]), actionSummary(e["packages"]), output.Cell(e["error"]),
		})
	}
	return output.Result{
		Headers: []string{"START", "INDEX", "END", "COMMAND", "PACKAGES", "ERROR"}, Rows: rows, Raw: raw,
	}, nil
}

// actionSummary counts the package actions of a transaction.
func actionSummary(v any) string {
	pkgs, _ := v.([]any)
	counts := map[string]int{}
	for _, p := range pkgs {
		if m, ok := p.(map[string]any); ok {
			counts[output.Cell(m["action"])]++
		}
	}
	names := make([]string, 0, len(counts))
	for a := range counts {
		names = append(names, a)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, a := range names {
		parts = append(parts, strconv.Itoa(counts[a])+" "+a)
	}
	return strings.Join(parts, ", ")
}

// firstLine is the first non-empty line of a changelog text.
func firstLine(v any) string {
	text, _ := v.(string)
	for line := range strings.SplitSeq(text, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// ChangelogResult builds the table of one package's changelog entries. The
// full change text is in the json and yaml output.
func ChangelogResult(raws []json.RawMessage, raw any) (output.Result, error) {
	entries, err := decodeObjects(raws, "changelog entries")
	if err != nil {
		return output.Result{}, err
	}
	return output.Result{
		Headers: []string{"PACKAGE", "VERSION", "DATE", "URGENCY", "DISTRIBUTIONS", "CHANGES"},
		Rows:    entryRows(entries), Raw: raw,
	}, nil
}

func entryRows(entries []map[string]any) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			output.Cell(e["package"]), output.Cell(e["version"]), output.Cell(e["date"]),
			output.Cell(e["urgency"]), output.Cell(e["distributions"]), firstLine(e["changes"]),
		})
	}
	return rows
}

// GroupedChangelogResult builds the table of changelogs grouped by source
// package: one row for each group, with the binary packages it covers, how many
// entries it holds, the newest entry, and any error the server hit.
func GroupedChangelogResult(raws []json.RawMessage, raw any) (output.Result, error) {
	groups, err := decodeObjects(raws, "changelog groups")
	if err != nil {
		return output.Result{}, err
	}
	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		entries, _ := g["entries"].([]any)
		newest := ""
		if len(entries) > 0 {
			if m, ok := entries[0].(map[string]any); ok {
				newest = output.Cell(m["package"]) + " " + output.Cell(m["version"])
				newest = strings.TrimSpace(newest)
			}
		}
		rows = append(rows, []string{
			packageNames(g["packages"]), strconv.Itoa(len(entries)), newest, output.Cell(g["error"]),
		})
	}
	return output.Result{
		Headers: []string{"PACKAGES", "ENTRIES", "NEWEST", "ERROR"}, Rows: rows, Raw: raw,
	}, nil
}

// packageNames lists the package names of a group, whether the server names
// them as plain strings or as objects.
func packageNames(v any) string {
	list, _ := v.([]any)
	names := make([]string, 0, len(list))
	for _, p := range list {
		switch t := p.(type) {
		case string:
			names = append(names, t)
		case map[string]any:
			for _, k := range []string{"package", "name"} {
				if s, ok := t[k].(string); ok {
					names = append(names, s)
					break
				}
			}
		}
	}
	return strings.Join(names, ",")
}

// TextResult decodes a response that the server sends as one JSON string, which
// is how the terminal log arrives, and renders it as plain text. A body that is
// not a string falls back to its raw bytes, the way `apt changelog` does.
func TextResult(raw json.RawMessage) output.Result {
	var text string
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &text); err != nil {
			text = string(raw)
		}
	}
	return output.Result{Message: text, Raw: text}
}
