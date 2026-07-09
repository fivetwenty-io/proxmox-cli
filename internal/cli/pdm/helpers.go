package pdm

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// --- helpers -----------------------------------------------------------------

// anyFlagChanged reports whether at least one flag on fl was explicitly set.
func anyFlagChanged(fl *pflag.FlagSet) bool {
	changed := false
	fl.Visit(func(*pflag.Flag) { changed = true })
	return changed
}

// stringInSlice reports whether v equals one of allowed.
func stringInSlice(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// rawItemsOf dereferences a *[]json.RawMessage-shaped response type, returning
// an empty (nil) slice for a nil response instead of panicking.
func rawItemsOf[T ~[]json.RawMessage](resp *T) []json.RawMessage {
	if resp == nil {
		return nil
	}
	return []json.RawMessage(*resp)
}

// decodeRawList decodes each element of items into a generic map, preserving
// every field the API returned (unlike a typed struct, which only captures
// fields it declares). Elements that fail to decode as an object are skipped
// rather than aborting the whole list.
func decodeRawList(items []json.RawMessage) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err == nil {
			out = append(out, obj)
		}
	}
	return out
}

// decodeRawString unmarshals a json.RawMessage that carries a plain JSON
// string, the shape PDM uses for free-text endpoints such as
// GET /config/acme/tos and GET /config/notes.
func decodeRawString(raw json.RawMessage) (string, error) {
	var text string

	err := json.Unmarshal(raw, &text)
	if err != nil {
		return "", fmt.Errorf("unexpected non-string response: %w", err)
	}

	return text, nil
}

// flattenToMap re-marshals v (a typed API response struct) and unmarshals the
// result into a generic map, so every populated field — including nested
// json.RawMessage sub-objects — is available for Single/Raw rendering without
// hand-maintaining a field-by-field projection.
func flattenToMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return fields, nil
}

// stringMap renders every value in fields via scalarString, for output.Result.Single.
func stringMap(fields map[string]any) map[string]string {
	single := make(map[string]string, len(fields))
	for k, v := range fields {
		single[k] = scalarString(v)
	}
	return single
}

// scalarString renders an arbitrary JSON scalar as a display string. Numbers
// decoded as float64 with no fractional part render without a trailing ".0".
// Non-scalar values (nested objects/arrays, e.g. gc-status) render as compact
// JSON text.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return strings.TrimSpace(fmt.Sprintf("%v", t))
		}
		return string(b)
	}
}

// int64PtrString renders a possibly-nil *int64 for a table cell.
func int64PtrString(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

// strPtrString renders a possibly-nil *string for a table cell.
func strPtrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// float64PtrString renders a possibly-nil *float64 for a table cell.
func float64PtrString(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool { return &v }

// boolPtrString renders a possibly-nil *bool for a table cell.
func boolPtrString(p *bool) string {
	if p == nil {
		return ""
	}
	return strconv.FormatBool(*p)
}

// strPtr returns a pointer to v.
func strPtr(v string) *string { return &v }

// int64Ptr returns a pointer to v.
func int64Ptr(v int64) *int64 { return &v }

// pveBoolPtrString renders a possibly-nil *pveclient.PVEBool for a table
// cell. PVEBool tolerantly decodes both native JSON booleans and the PVE
// 0/1 encoding; realm-config responses (GetAccessAd/Ldap/OpenidResponse,
// ListDomains items) declare their optional boolean fields with this type
// rather than *bool. Nil renders as "", matching every other *T-pointer
// formatter in this file (int64PtrString, strPtrString, etc.).
func pveBoolPtrString(p *pveclient.PVEBool) string {
	if p == nil {
		return ""
	}
	return strconv.FormatBool(p.Bool())
}

// pveIntPtrString renders a possibly-nil *pveclient.PVEInt for a table
// cell. PVEInt tolerantly decodes integers the PVE/PDM API may encode as a
// JSON string; GetAccessAdResponse.Port and GetAccessLdapResponse.Port
// declare this type rather than *int64.
func pveIntPtrString(p *pveclient.PVEInt) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(p.Int(), 10)
}
