// Package accesslog holds what the PVE and PBS "node access-log" commands share.
// Both products serve the same API for reading the proxy's request log, with PBS
// adding a user-agent filter, so one set of flags, one parameter mapping, and one
// renderer serve both personas.
package accesslog

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvenodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/epoch"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// Filters is the set of request filters of the access-log list and stats
// calls. A pointer field is set only when its flag was given, so the request
// carries nothing the operator did not ask for.
type Filters struct {
	After       *string
	AuthID      *string
	AuthIDExact *bool
	Bucket      *int64
	Client      *string
	Cursor      *string
	Limit       *int64
	MaxStatus   *int64
	Method      *string
	MinStatus   *int64
	Path        *string
	PathGroup   *string
	Search      *string
	Since       *int64
	Top         *int64
	Until       *int64
	UserAgent   *string
}

// Flags binds the access-log flags to a command and collects them after parsing.
type Flags struct {
	after, authID, client, cursor, method, path, pathGroup, search, since, until, userAgent string
	authIDExact                                                                             bool
	bucket, limit, maxStatus, minStatus, top                                                int64
	wantUserAgent                                                                           bool
	stats                                                                                   bool
}

// BindList adds the flags of the list call. userAgent adds --user-agent, which
// only PBS serves.
func BindList(cmd *cobra.Command, userAgent bool) *Flags {
	f := &Flags{wantUserAgent: userAgent}
	f.bindCommon(cmd)
	fl := cmd.Flags()
	fl.StringVar(&f.after, "after", "", "only requests logged after this position, the head-cursor of an earlier page")
	fl.StringVar(&f.cursor, "cursor", "", "continue with the requests before this position, the next-cursor of the previous page")
	fl.Int64Var(&f.limit, "limit", 0, "the maximum number of requests to return")
	return f
}

// BindStats adds the flags of the stats call. userAgent adds --user-agent.
func BindStats(cmd *cobra.Command, userAgent bool) *Flags {
	f := &Flags{wantUserAgent: userAgent, stats: true}
	f.bindCommon(cmd)
	fl := cmd.Flags()
	fl.Int64Var(&f.bucket, "bucket", 0, "the length of a histogram bucket in seconds")
	fl.Int64Var(&f.top, "top", 0, "the number of values with the most requests to list")
	return f
}

func (f *Flags) bindCommon(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.authID, "auth-id", "", "only requests whose user or API token contains this text, ignoring case")
	fl.BoolVar(&f.authIDExact, "auth-id-exact", false, "match --auth-id as a whole, so a user does not match its tokens")
	fl.StringVar(&f.client, "client", "", "only requests from this client address")
	fl.Int64Var(&f.maxStatus, "max-status", 0, "only responses with an HTTP status of at most this value")
	fl.StringVar(&f.method, "method", "", "only requests with this HTTP method")
	fl.Int64Var(&f.minStatus, "min-status", 0, "only responses with an HTTP status of at least this value")
	fl.StringVar(&f.path, "path", "", "only requests whose path contains this text")
	fl.StringVar(&f.pathGroup, "path-group", "", "only requests whose path falls into this group, as the statistics list them")
	fl.StringVar(&f.search, "search", "", "only requests whose client, user, or path contains this text, ignoring case")
	fl.StringVar(&f.since, "since", "", "only requests at or after this time, as a UNIX epoch or an RFC 3339 time")
	fl.StringVar(&f.until, "until", "", "only requests at or before this time, as a UNIX epoch or an RFC 3339 time")
	if f.wantUserAgent {
		fl.StringVar(&f.userAgent, "user-agent", "", "only requests with this user agent, as logged")
	}
}

// ParseTime reads a UNIX epoch or an RFC 3339 time and returns epoch seconds.
func ParseTime(flag, value string) (int64, error) {
	return epoch.Parse(flag, value)
}

// Collect reads the flags that were given into a Filters value.
func (f *Flags) Collect(cmd *cobra.Command) (*Filters, error) {
	fl := cmd.Flags()
	out := &Filters{}
	str := func(name string, src *string, dst **string) {
		if fl.Changed(name) {
			v := *src
			*dst = &v
		}
	}
	num := func(name string, src *int64, dst **int64) {
		if fl.Changed(name) {
			v := *src
			*dst = &v
		}
	}
	str("auth-id", &f.authID, &out.AuthID)
	str("client", &f.client, &out.Client)
	str("method", &f.method, &out.Method)
	str("path", &f.path, &out.Path)
	str("path-group", &f.pathGroup, &out.PathGroup)
	str("search", &f.search, &out.Search)
	num("max-status", &f.maxStatus, &out.MaxStatus)
	num("min-status", &f.minStatus, &out.MinStatus)
	if fl.Changed("auth-id-exact") {
		v := f.authIDExact
		out.AuthIDExact = &v
	}
	if f.wantUserAgent {
		str("user-agent", &f.userAgent, &out.UserAgent)
	}
	if f.stats {
		num("bucket", &f.bucket, &out.Bucket)
		num("top", &f.top, &out.Top)
	} else {
		str("after", &f.after, &out.After)
		str("cursor", &f.cursor, &out.Cursor)
		num("limit", &f.limit, &out.Limit)
	}
	for name, pair := range map[string]struct {
		src string
		dst **int64
	}{"since": {f.since, &out.Since}, "until": {f.until, &out.Until}} {
		if fl.Changed(name) {
			n, err := ParseTime(name, pair.src)
			if err != nil {
				return nil, err
			}
			*pair.dst = &n
		}
	}
	if out.After != nil && out.Cursor != nil {
		return nil, fmt.Errorf("--after and --cursor cannot be combined")
	}
	return out, nil
}

// PVEList maps the filters onto the PVE list parameters.
func (f *Filters) PVEList() *pvenodes.ListAccessLogParams {
	return &pvenodes.ListAccessLogParams{
		After: f.After, AuthId: f.AuthID, AuthIdExact: f.AuthIDExact, Client: f.Client, Cursor: f.Cursor,
		Limit: f.Limit, MaxStatus: f.MaxStatus, Method: f.Method, MinStatus: f.MinStatus, Path: f.Path,
		PathGroup: f.PathGroup, Search: f.Search, Since: f.Since, Until: f.Until,
	}
}

// PVEStats maps the filters onto the PVE stats parameters.
func (f *Filters) PVEStats() *pvenodes.ListAccessLogStatsParams {
	return &pvenodes.ListAccessLogStatsParams{
		AuthId: f.AuthID, AuthIdExact: f.AuthIDExact, Bucket: f.Bucket, Client: f.Client, MaxStatus: f.MaxStatus,
		Method: f.Method, MinStatus: f.MinStatus, Path: f.Path, PathGroup: f.PathGroup, Search: f.Search,
		Since: f.Since, Top: f.Top, Until: f.Until,
	}
}

// PBSList maps the filters onto the PBS list parameters.
func (f *Filters) PBSList() *pbsnodes.ListAccessLogParams {
	return &pbsnodes.ListAccessLogParams{
		After: f.After, AuthId: f.AuthID, AuthIdExact: f.AuthIDExact, Client: f.Client, Cursor: f.Cursor,
		Limit: f.Limit, MaxStatus: f.MaxStatus, Method: f.Method, MinStatus: f.MinStatus, Path: f.Path,
		PathGroup: f.PathGroup, Search: f.Search, Since: f.Since, Until: f.Until, UserAgent: f.UserAgent,
	}
}

// PBSStats maps the filters onto the PBS stats parameters.
func (f *Filters) PBSStats() *pbsnodes.ListAccessLogStatsParams {
	return &pbsnodes.ListAccessLogStatsParams{
		AuthId: f.AuthID, AuthIdExact: f.AuthIDExact, Bucket: f.Bucket, Client: f.Client, MaxStatus: f.MaxStatus,
		Method: f.Method, MinStatus: f.MinStatus, Path: f.Path, PathGroup: f.PathGroup, Search: f.Search,
		Since: f.Since, Top: f.Top, Until: f.Until, UserAgent: f.UserAgent,
	}
}

// Page is the product-neutral view of one page of the access log.
type Page struct {
	Entries    []json.RawMessage
	NextCursor *string
	HeadCursor *string
	Complete   bool
	Gaps       int
	Raw        any
}

// ListResult builds the table for a page of requests. A column shows only when
// some entry carries the field, which keeps the PVE table free of an empty
// USER-AGENT column.
func ListResult(p Page) (output.Result, error) {
	entries := make([]map[string]any, 0, len(p.Entries))
	for _, raw := range p.Entries {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return output.Result{}, fmt.Errorf("decode access-log entry: %w", err)
		}
		entries = append(entries, m)
	}
	type column struct{ header, key string }
	columns := []column{
		{"TIME", "time"}, {"CLIENT", "client"}, {"AUTH-ID", "auth-id"}, {"METHOD", "method"},
		{"URI", "uri"}, {"STATUS", "status"}, {"SIZE", "size"}, {"USER-AGENT", "user-agent"},
	}
	var used []column
	for _, c := range columns {
		for _, e := range entries {
			if _, ok := e[c.key]; ok {
				used = append(used, c)
				break
			}
		}
	}
	if len(used) == 0 {
		used = columns[:7]
	}
	headers := make([]string, len(used))
	for i, c := range used {
		headers[i] = c.header
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		row := make([]string, len(used))
		for i, c := range used {
			row[i] = cell(c.key, e[c.key])
		}
		rows = append(rows, row)
	}
	return output.Result{Headers: headers, Rows: rows, Raw: p.Raw}, nil
}

// cell renders one entry field, showing the time as an RFC 3339 UTC stamp.
func cell(key string, v any) string {
	if key == "time" {
		if n, ok := v.(float64); ok {
			return epoch.Format(int64(n))
		}
	}
	return output.Cell(v)
}

// WriteNotes tells the operator what the table and plain output of a page
// leaves out. It writes to the error stream, so a pipe of the table output stays
// clean, and only for the human formats because the json and yaml documents
// carry every one of these fields already. It names the cursor that reads the
// next page of older requests, the head cursor that --after takes to read what
// was logged since, and a warning when the page is incomplete or skipped parts
// of the log.
func WriteNotes(w io.Writer, format output.Format, p Page) {
	if format != output.FormatTable && format != output.FormatPlain {
		return
	}
	if p.NextCursor != nil && *p.NextCursor != "" {
		_, _ = fmt.Fprintf(w, "More requests are available. Continue with --cursor %s\n", *p.NextCursor)
	}
	if p.HeadCursor != nil && *p.HeadCursor != "" {
		_, _ = fmt.Fprintf(w, "To read the requests logged after this page, use --after %s\n", *p.HeadCursor)
	}
	if p.Gaps > 0 {
		_, _ = fmt.Fprintf(w, "Warning: the server skipped %d unreadable part(s) of the log, so some requests are missing.\n", p.Gaps)
	}
	if !p.Complete {
		_, _ = fmt.Fprintln(w, "Warning: this page and the ones before it do not cover the whole log; use -o json for the details.")
	}
}

// topLimit bounds how many ranked values a summary line lists.
const topLimit = 5

// StatsInput is the product-neutral view of an access-log stats response.
type StatsInput struct {
	Since, Until, InvalidLines int64
	Complete                   bool
	Stopped                    *string
	Total                      json.RawMessage
	Lists                      []NamedList
	Raw                        any
}

// NamedList is one ranked list of the statistics, such as the clients.
type NamedList struct {
	Name string
	Data json.RawMessage
}

// StatsResult builds the key/value summary of the statistics. The ranked lists
// show their leading values with the request counts, and the complete lists
// stay in the json and yaml output.
func StatsResult(in StatsInput) (output.Result, error) {
	single := map[string]string{
		"since":         epoch.Format(in.Since),
		"until":         epoch.Format(in.Until),
		"complete":      strconv.FormatBool(in.Complete),
		"invalid-lines": strconv.FormatInt(in.InvalidLines, 10),
	}
	if in.Stopped != nil && *in.Stopped != "" {
		single["stopped"] = *in.Stopped
	}
	if len(in.Total) > 0 {
		var total map[string]any
		if err := json.Unmarshal(in.Total, &total); err != nil {
			return output.Result{}, fmt.Errorf("decode access-log totals: %w", err)
		}
		keys := make([]string, 0, len(total))
		for k := range total {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			single["total-"+k] = output.Cell(total[k])
		}
	}
	for _, l := range in.Lists {
		if len(l.Data) == 0 || string(l.Data) == "null" {
			continue
		}
		var list struct {
			Rows []map[string]any `json:"rows"`
		}
		if err := json.Unmarshal(l.Data, &list); err != nil {
			return output.Result{}, fmt.Errorf("decode access-log %s: %w", l.Name, err)
		}
		var parts []string
		for i, r := range list.Rows {
			if i == topLimit {
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", output.Cell(r["key"]), output.Cell(r["requests"])))
		}
		single[l.Name] = strings.Join(parts, ", ")
	}
	return output.Result{Single: single, Raw: in.Raw}, nil
}
