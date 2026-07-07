package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newAPIRawCmd builds `pve pbs api` — issue raw HTTP requests directly
// against the Proxmox Backup Server REST API for endpoints this CLI does not
// (yet) wrap with a typed command. It is the PBS-side counterpart of the raw
// escape hatch other command groups reach for internally via deps.PBS.Raw
// (see e.g. internal/cli/pbs/datastore.go's rrd command), exposed here as a
// user-facing verb.
func newAPIRawCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Issue raw requests against the PBS API",
		Long: "Issue GET, POST, PUT, and DELETE requests directly against the Proxmox " +
			"Backup Server REST API by path, for endpoints this CLI does not (yet) " +
			"expose as a typed command. The response is rendered generically: as a " +
			"key/value table for a single JSON object, a table for an array of " +
			"objects, and a plain value otherwise; every format always preserves the " +
			"full response losslessly via --output json or --output yaml.",
	}
	cmd.AddCommand(newAPIRawGetCmd(), newAPIRawPostCmd(), newAPIRawPutCmd(), newAPIRawDeleteCmd())
	return cmd
}

// apiRawNormalizePath trims whitespace from path and ensures it starts with
// "/", the shape every pkg/client.Client method expects. It rejects an empty
// (or whitespace-only) path outright since there is no sensible request to
// issue against it.
func apiRawNormalizePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("path must not be empty")
	}

	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}

	return trimmed, nil
}

// apiRawParamsFromData parses repeated --data/-d KEY=VALUE flag values into
// the map[string]interface{} shape pkg/client.Client's Get/Post/Put/DeleteCtx
// methods accept. It returns a nil map (not an empty one) when data is empty
// so the underlying request carries no body/query parameters at all, matching
// what every generated binding does for a parameter-less call. Malformed
// entries (no "=", empty key, or a key repeated in the same invocation) are
// rejected by the shared cli.ParseKeyValues parser.
func apiRawParamsFromData(data []string) (map[string]interface{}, error) {
	kvs, err := cli.ParseKeyValues(data)
	if err != nil {
		return nil, err
	}

	if len(kvs) == 0 {
		return nil, nil
	}

	params := make(map[string]interface{}, len(kvs))
	for _, kv := range kvs {
		params[kv.Key] = kv.Value
	}

	return params, nil
}

// newAPIRawGetCmd builds `pve pbs api get <path>`.
func newAPIRawGetCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Issue a raw GET request",
		Long: "Issue a GET request to an arbitrary PBS API path (e.g. /admin/datastore), " +
			"passing any --data KEY=VALUE pairs as query parameters.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := apiRawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := apiRawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := deps.PBS.Raw.GetCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("GET %s: %w", path, err)
			}

			return apiRawRender(cmd, deps, "GET", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "query parameter as KEY=VALUE (repeatable)")
	return cmd
}

// newAPIRawPostCmd builds `pve pbs api post <path>`.
func newAPIRawPostCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "post <path>",
		Short: "Issue a raw POST request",
		Long: "Issue a POST request to an arbitrary PBS API path, sending any " +
			"--data KEY=VALUE pairs as the form-encoded request body.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := apiRawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := apiRawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := deps.PBS.Raw.PostCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("POST %s: %w", path, err)
			}

			return apiRawRender(cmd, deps, "POST", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "request parameter as KEY=VALUE (repeatable)")
	return cmd
}

// newAPIRawPutCmd builds `pve pbs api put <path>`.
func newAPIRawPutCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "put <path>",
		Short: "Issue a raw PUT request",
		Long: "Issue a PUT request to an arbitrary PBS API path, sending any " +
			"--data KEY=VALUE pairs as the form-encoded request body.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := apiRawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := apiRawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := deps.PBS.Raw.PutCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("PUT %s: %w", path, err)
			}

			return apiRawRender(cmd, deps, "PUT", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "request parameter as KEY=VALUE (repeatable)")
	return cmd
}

// newAPIRawDeleteCmd builds `pve pbs api delete <path>`.
func newAPIRawDeleteCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "delete <path>",
		Short: "Issue a raw DELETE request",
		Long: "Issue a DELETE request to an arbitrary PBS API path, passing any " +
			"--data KEY=VALUE pairs as query parameters.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := apiRawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := apiRawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := deps.PBS.Raw.DeleteCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("DELETE %s: %w", path, err)
			}

			return apiRawRender(cmd, deps, "DELETE", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "query parameter as KEY=VALUE (repeatable)")
	return cmd
}

// apiRawRender renders the decoded body of a raw API response and writes it
// through deps.Out. See apiRawResult for the shape-dependent rendering rules.
func apiRawRender(cmd *cobra.Command, deps *cli.Deps, method, path string, data interface{}) error {
	res := apiRawResult(method, path, data)
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// apiRawResult builds an output.Result from a raw, dynamically-typed API
// response body. The body was decoded by encoding/json into one of Go's five
// possible JSON-derived shapes (nil, bool, float64, string, map[string]any,
// or []any); apiRawResult switches on which one it got so every response
// shape renders sensibly instead of only ever showing a JSON blob:
//
//   - nil (endpoints such as most POST/PUT/DELETE calls, which respond with
//     an empty body): a plain success message, no table.
//   - map[string]interface{} (a single object): a KEY/VALUE table.
//   - []interface{} of objects: a table with one column per key observed
//     across every element (the union, since PBS array elements do not all
//     guarantee the same optional keys).
//   - []interface{} of anything else, or a bare scalar (bool/float64/string):
//     rendered as a single VALUE column, or as the message text for a lone
//     scalar.
//
// Every branch also sets Raw to the original decoded value so --output
// json/yaml reproduces the server response losslessly regardless of which
// table shape was chosen for table/plain/ascii.
func apiRawResult(method, path string, data interface{}) output.Result {
	switch v := data.(type) {
	case nil:
		return output.Result{Message: fmt.Sprintf("%s %s: OK (no data returned).", method, path)}
	case map[string]interface{}:
		return output.Result{Single: apiRawObjectToSingle(v), Raw: v}
	case []interface{}:
		return apiRawArrayResult(v)
	default:
		return output.Result{Message: apiRawCell(v), Raw: v}
	}
}

// apiRawArrayResult renders a []interface{} response body: as a dynamic
// table when every element is a JSON object, or as a single VALUE column
// otherwise (an array of scalars, or a mix of objects and scalars).
func apiRawArrayResult(items []interface{}) output.Result {
	if len(items) == 0 {
		return output.Result{Message: "[] (empty array).", Raw: items}
	}

	maps := make([]map[string]interface{}, 0, len(items))
	allObjects := true

	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			allObjects = false
			break
		}

		maps = append(maps, m)
	}

	if allObjects {
		headers, rows := apiRawDynamicTable(maps)
		return output.Result{Headers: headers, Rows: rows, Raw: items}
	}

	headers := []string{"VALUE"}
	rows := make([][]string, 0, len(items))

	for _, it := range items {
		rows = append(rows, []string{apiRawCell(it)})
	}

	return output.Result{Headers: headers, Rows: rows, Raw: items}
}

// apiRawObjectToSingle flattens a decoded JSON object into a string map for
// table/plain/text rendering.
func apiRawObjectToSingle(obj map[string]interface{}) map[string]string {
	single := make(map[string]string, len(obj))
	for k, v := range obj {
		single[k] = apiRawCell(v)
	}

	return single
}

// apiRawDynamicTable derives a stable, sorted column set from the union of
// keys across entries and renders each entry as a row, for API responses
// whose element shape is not statically known to this CLI.
func apiRawDynamicTable(entries []map[string]interface{}) ([]string, [][]string) {
	keySet := make(map[string]struct{})
	for _, e := range entries {
		for k := range e {
			keySet[k] = struct{}{}
		}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	headers := make([]string, len(keys))
	for i, k := range keys {
		headers[i] = apiRawUpperHeader(k)
	}

	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		row := make([]string, len(keys))
		for i, k := range keys {
			row[i] = apiRawCell(e[k])
		}

		rows = append(rows, row)
	}

	return headers, rows
}

// apiRawUpperHeader renders a JSON key as an upper-case table header,
// e.g. "backend-type" -> "BACKEND-TYPE", "history_start" -> "HISTORY-START".
func apiRawUpperHeader(k string) string {
	return strings.ToUpper(strings.ReplaceAll(k, "_", "-"))
}

// apiRawCell renders a single decoded JSON value as a table/message cell.
// Integral float64 values print without a decimal point (JSON numbers
// decode to float64 regardless of whether the server sent an integer); any
// value this CLI does not otherwise recognize (nested objects/arrays) falls
// back to compact JSON so no information is silently dropped.
func apiRawCell(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}

		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}

		return string(b)
	}
}
