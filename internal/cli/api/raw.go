package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// rawClient returns the pve.Client the active context selected: deps.PBS's
// underlying transport when set (a PBS context), deps.PDM's when set (a PDM
// context), otherwise deps.API's. APIClient, PBSClient, and PDMClient all
// wrap the same pve.Client interface, so a single raw command tree can issue
// GET/POST/PUT/DELETE against whichever product the active context targets
// without knowing which one it is.
func rawClient(deps *cli.Deps) pve.Client {
	if deps.PBS != nil {
		return deps.PBS.Raw
	}
	if deps.PDM != nil {
		return deps.PDM.Raw
	}
	return deps.API.Raw
}

// rawNormalizePath trims whitespace from path and ensures it starts with
// "/", the shape every pve.Client method expects. It rejects an empty (or
// whitespace-only) path outright since there is no sensible request to issue
// against it.
func rawNormalizePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("path must not be empty")
	}

	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}

	return trimmed, nil
}

// rawParamsFromData parses repeated --data/-d KEY=VALUE flag values into the
// map[string]interface{} shape pve.Client's GetCtx/PostCtx/PutCtx/DeleteCtx
// methods accept. It returns a nil map (not an empty one) when data is empty
// so the underlying request carries no body/query parameters at all, matching
// what every generated binding does for a parameter-less call. Malformed
// entries (no "=", empty key, or a key repeated in the same invocation) are
// rejected by the shared cli.ParseKeyValues parser.
func rawParamsFromData(data []string) (map[string]interface{}, error) {
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

// newRawGetCmd builds `pmx api get <path>`.
func newRawGetCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Issue a raw GET request",
		// The verb is the HTTP method, not a CRUD synonym; a "show" alias
		// would misdescribe the command.
		Annotations: map[string]string{cli.AnnotationNoVerbAlias: "true"},
		Long: "Issue a GET request to an arbitrary API path (e.g. /nodes or /admin/datastore) " +
			"against the active context's Proxmox VE or Proxmox Backup Server API, passing any " +
			"--data KEY=VALUE pairs as query parameters.",
		Example: `  pmx api get /nodes
  pmx api get /nodes/pve1/status
  pmx api get /cluster/resources -d type=vm`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := rawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := rawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := rawClient(deps).GetCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("GET %s: %w", path, err)
			}

			return rawRender(cmd, deps, "GET", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "query parameter as KEY=VALUE (repeatable)")
	return cmd
}

// newRawPostCmd builds `pmx api post <path>`.
func newRawPostCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "post <path>",
		Short: "Issue a raw POST request",
		Long: "Issue a POST request to an arbitrary API path against the active context's " +
			"Proxmox VE or Proxmox Backup Server API, sending any --data KEY=VALUE pairs as the " +
			"form-encoded request body.",
		Example: `  pmx api post /nodes/pve1/lxc/200/status/start
  pmx api post /access/users -d userid=alice@pve -d password='${PMX_PASSWORD}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := rawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := rawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := rawClient(deps).PostCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("POST %s: %w", path, err)
			}

			return rawRender(cmd, deps, "POST", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "request parameter as KEY=VALUE (repeatable)")
	return cmd
}

// newRawPutCmd builds `pmx api put <path>`.
func newRawPutCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "put <path>",
		Short: "Issue a raw PUT request",
		Long: "Issue a PUT request to an arbitrary API path against the active context's " +
			"Proxmox VE or Proxmox Backup Server API, sending any --data KEY=VALUE pairs as the " +
			"form-encoded request body.",
		Example: `  pmx api put /nodes/pve1/lxc/200/config -d cores=4
  pmx api put /access/domains/pve -d comment='local realm'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := rawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := rawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := rawClient(deps).PutCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("PUT %s: %w", path, err)
			}

			return rawRender(cmd, deps, "PUT", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "request parameter as KEY=VALUE (repeatable)")
	return cmd
}

// newRawDeleteCmd builds `pmx api delete <path>`.
func newRawDeleteCmd() *cobra.Command {
	var data []string
	cmd := &cobra.Command{
		Use:   "delete <path>",
		Short: "Issue a raw DELETE request",
		// The verb is the HTTP method, not a CRUD synonym; an "rm" alias
		// would misdescribe the command.
		Annotations: map[string]string{cli.AnnotationNoVerbAlias: "true"},
		Long: "Issue a DELETE request to an arbitrary API path against the active context's " +
			"Proxmox VE or Proxmox Backup Server API, passing any --data KEY=VALUE pairs as query " +
			"parameters.",
		Example: `  pmx api delete /nodes/pve1/lxc/200/snapshot/pre-upgrade
  pmx api delete /access/users/alice@pve`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			path, err := rawNormalizePath(args[0])
			if err != nil {
				return err
			}

			params, err := rawParamsFromData(data)
			if err != nil {
				return err
			}

			resp, err := rawClient(deps).DeleteCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("DELETE %s: %w", path, err)
			}

			return rawRender(cmd, deps, "DELETE", path, resp)
		},
	}
	cmd.Flags().StringArrayVarP(&data, "data", "d", nil, "query parameter as KEY=VALUE (repeatable)")
	return cmd
}

// rawRender renders the decoded body of a raw API response and writes it
// through deps.Out. See rawResult for the shape-dependent rendering rules.
func rawRender(cmd *cobra.Command, deps *cli.Deps, method, path string, data interface{}) error {
	res := rawResult(method, path, data)
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// rawResult builds an output.Result from a raw, dynamically-typed API
// response body. The body was decoded by encoding/json into one of Go's five
// possible JSON-derived shapes (nil, bool, float64, string, map[string]any,
// or []any); rawResult switches on which one it got so every response shape
// renders sensibly instead of only ever showing a JSON blob:
//
//   - nil (endpoints such as most POST/PUT/DELETE calls, which respond with
//     an empty body): a plain success message, no table.
//   - map[string]interface{} (a single object): a KEY/VALUE table.
//   - []interface{} of objects: a table with one column per key observed
//     across every element (the union, since API array elements do not all
//     guarantee the same optional keys).
//   - []interface{} of anything else, or a bare scalar (bool/float64/string):
//     rendered as a single VALUE column, or as the message text for a lone
//     scalar.
//
// Every branch also sets Raw to the original decoded value so --output
// json/yaml reproduces the server response losslessly regardless of which
// table shape was chosen for table/plain/ascii.
func rawResult(method, path string, data interface{}) output.Result {
	switch v := data.(type) {
	case nil:
		return output.Result{Message: fmt.Sprintf("%s %s: OK (no data returned).", method, path)}
	case map[string]interface{}:
		return output.Result{Single: rawObjectToSingle(v), Raw: v}
	case []interface{}:
		return rawArrayResult(v)
	default:
		return output.Result{Message: rawCell(v), Raw: v}
	}
}

// rawArrayResult renders a []interface{} response body: as a dynamic table
// when every element is a JSON object, or as a single VALUE column otherwise
// (an array of scalars, or a mix of objects and scalars).
func rawArrayResult(items []interface{}) output.Result {
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
		headers, rows := rawDynamicTable(maps)
		return output.Result{Headers: headers, Rows: rows, Raw: items}
	}

	headers := []string{"VALUE"}
	rows := make([][]string, 0, len(items))

	for _, it := range items {
		rows = append(rows, []string{rawCell(it)})
	}

	return output.Result{Headers: headers, Rows: rows, Raw: items}
}

// rawObjectToSingle flattens a decoded JSON object into a string map for
// table/plain/text rendering.
func rawObjectToSingle(obj map[string]interface{}) map[string]string {
	single := make(map[string]string, len(obj))
	for k, v := range obj {
		single[k] = rawCell(v)
	}

	return single
}

// rawDynamicTable derives a stable, sorted column set from the union of keys
// across entries and renders each entry as a row, for API responses whose
// element shape is not statically known to this CLI.
func rawDynamicTable(entries []map[string]interface{}) ([]string, [][]string) {
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
		headers[i] = rawUpperHeader(k)
	}

	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		row := make([]string, len(keys))
		for i, k := range keys {
			row[i] = rawCell(e[k])
		}

		rows = append(rows, row)
	}

	return headers, rows
}

// rawUpperHeader renders a JSON key as an upper-case table header, e.g.
// "backend-type" -> "BACKEND-TYPE", "history_start" -> "HISTORY-START".
func rawUpperHeader(k string) string {
	return strings.ToUpper(strings.ReplaceAll(k, "_", "-"))
}

// rawCell renders a single decoded JSON value as a table/message cell.
// Integral float64 values print without a decimal point (JSON numbers decode
// to float64 regardless of whether the server sent an integer); any value
// this CLI does not otherwise recognize (nested objects/arrays) falls back to
// compact JSON so no information is silently dropped.
func rawCell(v interface{}) string {
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
