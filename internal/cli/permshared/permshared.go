// Package permshared provides the ACL/permission decode, filter, and render
// helpers shared by the per-object-tree `permissions` sub-commands (qemu,
// lxc, storage, pool, node, sdn zone, sdn vnet). It deliberately duplicates a
// small amount of decode logic already present, unexported, in
// internal/cli/access (the PVE tolerant-boolean decoder and the ACL entry
// shape) rather than exporting it from that package: access is the escape
// hatch for raw ACL/permission paths and should not gain a dependency edge
// from every object tree just to satisfy this shared library.
//
// This package does not own any object tree's ACL path grammar (e.g. the
// singular "/pool/{poolid}" segment): each tree derives its own path and
// passes it in.
package permshared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// PVEBool is an optional boolean that tolerates the several JSON encodings
// the Proxmox VE API uses for boolean flags: a real JSON bool, the numbers
// 1/0, and the strings "1"/"0". A zero-value PVEBool (never unmarshalled)
// renders as "" via Cell and reports false via Bool.
type PVEBool struct {
	set bool
	val bool
}

// UnmarshalJSON decodes bool, numeric, and string encodings of a boolean.
func (b *PVEBool) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case 't', 'f':
		var v bool
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("decode bool: %w", err)
		}
		b.set, b.val = true, v
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("decode string bool: %w", err)
		}
		b.set, b.val = true, s == "1" || s == "true"
	default:
		var n float64
		if err := json.Unmarshal(data, &n); err != nil {
			return fmt.Errorf("decode numeric bool: %w", err)
		}
		b.set, b.val = true, n != 0
	}
	return nil
}

// MarshalJSON re-encodes the boolean the way PVE itself reports the ACL
// propagate flag (the numbers 1/0), and an unset value as null, so entries
// carried on output.Result.Raw round-trip cleanly to JSON instead of
// marshalling to an empty object (PVEBool's fields are unexported).
func (b PVEBool) MarshalJSON() ([]byte, error) {
	if !b.set {
		return []byte("null"), nil
	}
	if b.val {
		return []byte("1"), nil
	}
	return []byte("0"), nil
}

// MarshalYAML mirrors MarshalJSON for the YAML renderer: goccy/go-yaml does
// not consult json.Marshaler, so without this a PVEBool would marshal as an
// empty map ({}) under -o yaml.
func (b PVEBool) MarshalYAML() (any, error) {
	if !b.set {
		return nil, nil
	}
	if b.val {
		return 1, nil
	}
	return 0, nil
}

// Cell renders the boolean as "1"/"0" for table output; an unset value
// renders as "".
func (b PVEBool) Cell() string {
	if !b.set {
		return ""
	}
	if b.val {
		return "1"
	}
	return "0"
}

// Bool reports the decoded value; an unset PVEBool reports false.
func (b PVEBool) Bool() bool {
	return b.set && b.val
}

// AclEntry is a single row of the GET /access/acl response.
type AclEntry struct {
	Path      string  `json:"path"`
	Type      string  `json:"type"`
	Ugid      string  `json:"ugid"`
	Roleid    string  `json:"roleid"`
	Propagate PVEBool `json:"propagate"`
}

// DecodeAclList decodes each raw ACL entry in resp into an AclEntry. A nil
// resp yields an empty, non-nil slice.
func DecodeAclList(resp *access.ListAclResponse) ([]AclEntry, error) {
	if resp == nil {
		return []AclEntry{}, nil
	}
	entries := make([]AclEntry, 0, len(*resp))
	for _, raw := range *resp {
		var e AclEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("decode acl entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// FilterByPath returns the subset of entries matching path. An empty path
// matches every entry. When exact is true the entry's path must equal path;
// otherwise a prefix match is used (a plain byte-wise prefix, not
// path-boundary aware, mirroring the existing `pmx access acl list
// --path/--exact` matcher in internal/cli/access/acl.go so behaviour stays
// consistent across both surfaces).
func FilterByPath(entries []AclEntry, path string, exact bool) []AclEntry {
	if path == "" {
		return entries
	}
	filtered := make([]AclEntry, 0, len(entries))
	for _, e := range entries {
		if aclPathMatch(e.Path, path, exact) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// aclPathMatch reports whether entryPath passes the path filter.
func aclPathMatch(entryPath, filter string, exact bool) bool {
	if exact {
		return entryPath == filter
	}
	return len(entryPath) >= len(filter) && entryPath[:len(filter)] == filter
}

// ParentChain returns the client-side ancestor chain of path, root first:
// ParentChain("/") is ["/"]; ParentChain("/vms/100") is
// ["/", "/vms", "/vms/100"]. An empty path and a trailing slash are both
// treated as if absent/trimmed, so ParentChain("") and ParentChain("/foo/")
// behave the same as ParentChain("/") and ParentChain("/foo") respectively.
func ParentChain(path string) []string {
	trimmed := strings.TrimSuffix(path, "/")
	if trimmed == "" {
		return []string{"/"}
	}
	segments := strings.Split(strings.TrimPrefix(trimmed, "/"), "/")
	chain := make([]string, 0, len(segments)+1)
	chain = append(chain, "/")
	var cur strings.Builder
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		cur.WriteByte('/')
		cur.WriteString(seg)
		chain = append(chain, cur.String())
	}
	return chain
}

// pathDepth returns the number of path segments below root: pathDepth("/")
// is 0, pathDepth("/vms") is 1, pathDepth("/vms/100") is 2.
func pathDepth(path string) int {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "/") + 1
}

// DecodePermissions decodes the GET /access/permissions response, a map of
// path to a map of privilege to a tolerantly-encoded propagate flag, into a
// plain map[string]map[string]bool. A nil or empty resp yields an empty,
// non-nil map.
func DecodePermissions(resp *access.ListPermissionsResponse) (map[string]map[string]bool, error) {
	result := map[string]map[string]bool{}
	if resp == nil || len(*resp) == 0 {
		return result, nil
	}
	var raw map[string]map[string]PVEBool
	if err := json.Unmarshal(*resp, &raw); err != nil {
		return nil, fmt.Errorf("decode permissions: %w", err)
	}
	for path, privs := range raw {
		m := make(map[string]bool, len(privs))
		for priv, v := range privs {
			m[priv] = v.Bool()
		}
		result[path] = m
	}
	return result, nil
}

// GrantRevokeParams builds the UpdateAclParams for a `permissions grant` or
// `permissions revoke` invocation. revoke sets Delete to true; propagate nil
// leaves the Propagate field nil so the server applies its own default
// (propagate=1) rather than the CLI forcing a value.
func GrantRevokeParams(
	path, roles string,
	users, groups, tokens *string,
	propagate *bool,
	revoke bool,
) *access.UpdateAclParams {
	params := &access.UpdateAclParams{
		Path:   path,
		Roles:  roles,
		Users:  users,
		Groups: groups,
		Tokens: tokens,
	}
	if revoke {
		del := true
		params.Delete = &del
	}
	if propagate != nil {
		p := *propagate
		params.Propagate = &p
	}
	return params
}

// RenderAclList builds an output.Result for a set of ACL entries. Non-
// inherited output has headers TYPE, UGID, ROLEID, PROPAGATE, sorted by
// (TYPE, UGID, ROLEID). Inherited output gains a leading INHERITED-FROM
// column (the entry's own path, so every row is self-explanatory whether it
// came from the object's own path or an ancestor) and sorts by chain depth
// root first, then (TYPE, UGID, ROLEID). Raw is set to the sorted entries so
// JSON/YAML output reflects the same filtered subset as the table.
func RenderAclList(entries []AclEntry, inherited bool) output.Result {
	sorted := make([]AclEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if inherited {
			di, dj := pathDepth(sorted[i].Path), pathDepth(sorted[j].Path)
			if di != dj {
				return di < dj
			}
		}
		if sorted[i].Type != sorted[j].Type {
			return sorted[i].Type < sorted[j].Type
		}
		if sorted[i].Ugid != sorted[j].Ugid {
			return sorted[i].Ugid < sorted[j].Ugid
		}
		return sorted[i].Roleid < sorted[j].Roleid
	})

	headers := []string{"TYPE", "UGID", "ROLEID", "PROPAGATE"}
	if inherited {
		headers = append([]string{"INHERITED-FROM"}, headers...)
	}

	rows := make([][]string, 0, len(sorted))
	for _, e := range sorted {
		row := []string{e.Type, e.Ugid, e.Roleid, e.Propagate.Cell()}
		if inherited {
			row = append([]string{e.Path}, row...)
		}
		rows = append(rows, row)
	}

	return output.Result{Headers: headers, Rows: rows, Raw: sorted}
}

// RenderEffective builds an output.Result for a decoded effective-permissions
// tree, mirroring the PATH/PRIVS table shape of `pmx access permissions`
// (internal/cli/access/misc.go): one row per path, privileges comma-joined
// and sorted. Like that command, the per-privilege propagate flag is decoded
// (via DecodePermissions) but not shown in this table; every privilege
// present for a path is listed regardless of its propagate value.
func RenderEffective(tree map[string]map[string]bool) output.Result {
	paths := make([]string, 0, len(tree))
	for p := range tree {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	rows := make([][]string, 0, len(paths))
	for _, p := range paths {
		privs := make([]string, 0, len(tree[p]))
		for priv := range tree[p] {
			privs = append(privs, priv)
		}
		sort.Strings(privs)
		rows = append(rows, []string{p, strings.Join(privs, ",")})
	}

	return output.Result{Headers: []string{"PATH", "PRIVS"}, Rows: rows, Raw: tree}
}
