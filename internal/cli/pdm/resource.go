package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	pdmresources "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/resources"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// validResourceTypes are the resource-type enum values accepted by
// --resource-type on `resource ls` (GET /resources/list), per the PDM API schema.
var validResourceTypes = []string{"storage", "qemu", "lxc", "network", "datastore", "node"}

// newResourceCmd builds `pmx pdm resource` — list and inspect the aggregated
// PVE/PBS resources this Proxmox Datacenter Manager instance collects from
// its managed remotes (/resources).
func newResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource",
		Short: "List and inspect aggregated resources across managed remotes",
		Long: "List PVE/PBS resources (guests, storages, nodes, datastores, networks) collected " +
			"from every managed remote, and inspect resource counts, subscription status, and " +
			"top entities by CPU/memory usage.",
	}
	cmd.AddCommand(
		newResourceLsCmd(),
		newResourceLocationInfoCmd(),
		newResourceStatusCmd(),
		newResourceSubscriptionCmd(),
		newResourceTopEntitiesCmd(),
	)
	return cmd
}

// resourceListWrapper is the decoded shape of one element of GET
// /resources/list: a per-remote envelope carrying either an error or that
// remote's resources.
type resourceListWrapper struct {
	Error     *string           `json:"error,omitempty"`
	Remote    string            `json:"remote"`
	Resources []json.RawMessage `json:"resources"`
}

// newResourceLsCmd builds `pmx pdm resource ls` — list every resource
// collected from every managed remote (GET /resources/list).
//
// The API groups resources by remote, and each individual resource entry is
// one of several kinds (PVE guest, node, storage, network; PBS datastore,
// node) with no fields shared across every kind except "id". This command
// flattens the per-remote groups into one row per resource, inferring TYPE
// from the "<type>/<name>" convention Proxmox resource ids follow. A remote
// that failed to respond renders as a single "error" row instead of being
// silently dropped.
func newResourceLsCmd() *cobra.Command {
	var (
		maxAge       int64
		resourceType string
		search       string
		view         string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List resources from every managed remote",
		Long: "List PVE/PBS resources collected from every managed remote (GET /resources/list). " +
			"Each row is one resource; a remote that failed to respond renders as a single " +
			"\"error\" row instead of being silently dropped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if resourceType != "" && !stringInSlice(resourceType, validResourceTypes) {
				return fmt.Errorf("list resources: --resource-type must be one of %s (got %q)",
					strings.Join(validResourceTypes, ", "), resourceType)
			}

			params := &pdmresources.ListListParams{}
			if fl.Changed("max-age") {
				params.MaxAge = int64Ptr(maxAge)
			}
			if fl.Changed("resource-type") {
				params.ResourceType = strPtr(resourceType)
			}
			if fl.Changed("search") {
				params.Search = strPtr(search)
			}
			if fl.Changed("view") {
				params.View = strPtr(view)
			}

			resp, err := deps.PDM.Resources.ListList(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list resources: %w", err)
			}

			type resourceRow struct {
				remote, typ, id, name, node, status string
				raw                                 map[string]any
			}
			var rows []resourceRow

			for _, wrapperRaw := range rawItemsOf(resp) {
				var w resourceListWrapper

				err := json.Unmarshal(wrapperRaw, &w)
				if err != nil {
					return fmt.Errorf("decode resource wrapper: %w", err)
				}

				if w.Error != nil {
					rows = append(rows, resourceRow{
						remote: w.Remote,
						typ:    "error",
						status: *w.Error,
						raw:    map[string]any{"remote": w.Remote, "error": *w.Error},
					})
					continue
				}

				for _, resRaw := range w.Resources {
					var m map[string]any

					err := json.Unmarshal(resRaw, &m)
					if err != nil {
						return fmt.Errorf("decode resource entry: %w", err)
					}
					m["remote"] = w.Remote

					id, _ := m["id"].(string)
					typ := ""
					if idx := strings.IndexByte(id, '/'); idx >= 0 {
						typ = id[:idx]
					}
					name, _ := m["name"].(string)
					node, _ := m["node"].(string)
					status, _ := m["status"].(string)

					rows = append(rows, resourceRow{
						remote: w.Remote, typ: typ, id: id, name: name, node: node, status: status, raw: m,
					})
				}
			}

			sort.Slice(rows, func(i, j int) bool {
				if rows[i].remote != rows[j].remote {
					return rows[i].remote < rows[j].remote
				}
				return rows[i].id < rows[j].id
			})

			headers := []string{"REMOTE", "TYPE", "ID", "NAME", "NODE", "STATUS"}
			tableRows := make([][]string, 0, len(rows))
			raws := make([]map[string]any, 0, len(rows))

			for _, r := range rows {
				tableRows = append(tableRows, []string{r.remote, r.typ, r.id, r.name, r.node, r.status})
				raws = append(raws, r.raw)
			}

			res := output.Result{Headers: headers, Rows: tableRows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&maxAge, "max-age", 0, "maximum age (seconds) of cached remote resources")
	f.StringVar(&resourceType, "resource-type", "",
		"only list this resource type: storage|qemu|lxc|network|datastore|node")
	f.StringVar(&search, "search", "", "search term to filter resources")
	f.StringVar(&view, "view", "", "view name")
	return cmd
}

// newResourceLocationInfoCmd builds `pmx pdm resource location-info` —
// refresh the location info cache of the selected view, or every remote
// (GET /resources/location-info). The endpoint's API schema declares a
// "null" return type; despite its description, the generated binding
// carries no response data, so this command only reports success.
func newResourceLocationInfoCmd() *cobra.Command {
	var (
		maxAge int64
		view   string
	)
	cmd := &cobra.Command{
		Use:   "location-info",
		Short: "Refresh the location info cache for a view",
		Long: "Refresh the cached location info of the selected view, or every remote when " +
			"--view is omitted (GET /resources/location-info). The endpoint carries no response " +
			"data of its own (its API schema declares a \"null\" return type); it only reports " +
			"whether the request succeeded.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pdmresources.ListLocationInfoParams{}
			if fl.Changed("max-age") {
				params.MaxAge = int64Ptr(maxAge)
			}
			if fl.Changed("view") {
				params.View = strPtr(view)
			}

			err := deps.PDM.Resources.ListLocationInfo(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get resource location info: %w", err)
			}

			msg := "Location info retrieved for every remote."
			if view != "" {
				msg = fmt.Sprintf("Location info retrieved for view %q.", view)
			}

			res := output.Result{Message: msg}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&maxAge, "max-age", 0, "maximum age (seconds) of cached location info")
	f.StringVar(&view, "view", "", "view name")
	return cmd
}

// newResourceStatusCmd builds `pmx pdm resource status` — show the amount of
// configured/seen resources by type (GET /resources/status).
func newResourceStatusCmd() *cobra.Command {
	var (
		maxAge int64
		view   string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the amount of configured/seen resources by type",
		Long:  "Show the amount of configured/seen resources by type (GET /resources/status).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pdmresources.ListStatusParams{}
			if fl.Changed("max-age") {
				params.MaxAge = int64Ptr(maxAge)
			}
			if fl.Changed("view") {
				params.View = strPtr(view)
			}

			resp, err := deps.PDM.Resources.ListStatus(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get resource status: %w", err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode resource status: %w", err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&maxAge, "max-age", 0, "maximum age (seconds) of cached remote resources")
	f.StringVar(&view, "view", "", "view name")
	return cmd
}

// resourceSubscriptionEntry is the decoded shape of one element of
// GET /resources/subscription.
type resourceSubscriptionEntry struct {
	Error  *string `json:"error,omitempty"`
	Remote string  `json:"remote"`
	State  string  `json:"state"`
}

// newResourceSubscriptionCmd builds `pmx pdm resource subscription` — show
// the subscription status of every managed remote (GET /resources/subscription).
func newResourceSubscriptionCmd() *cobra.Command {
	var (
		maxAge  int64
		verbose bool
		view    string
	)
	cmd := &cobra.Command{
		Use:   "subscription",
		Short: "Show the subscription status of every managed remote",
		Long:  "Show the subscription status of every managed remote (GET /resources/subscription).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pdmresources.ListSubscriptionParams{}
			if fl.Changed("max-age") {
				params.MaxAge = int64Ptr(maxAge)
			}
			if fl.Changed("verbose") {
				params.Verbose = boolPtr(verbose)
			}
			if fl.Changed("view") {
				params.View = strPtr(view)
			}

			resp, err := deps.PDM.Resources.ListSubscription(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list resource subscriptions: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[resourceSubscriptionEntry](items, "resource subscription")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Remote < table[j].Entry.Remote })

			headers := []string{"REMOTE", "STATE", "ERROR"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.Entry.Remote, t.Entry.State, strPtrString(t.Entry.Error)})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&maxAge, "max-age", 0, "maximum age (seconds) of cached subscription state")
	f.BoolVar(&verbose, "verbose", false, "include per-node subscription information")
	f.StringVar(&view, "view", "", "view name")
	return cmd
}

// newResourceTopEntitiesCmd builds `pmx pdm resource top-entities` — show
// the top guest-CPU, node-CPU, and node-memory entities across every managed
// remote (GET /resources/top-entities). Each of the three lists is already
// ranked by the server, so rows preserve API order and are not re-sorted.
func newResourceTopEntitiesCmd() *cobra.Command {
	var (
		timeframe string
		view      string
	)
	cmd := &cobra.Command{
		Use:   "top-entities",
		Short: "Show the top entities by CPU/memory usage",
		Long: "Show the top guest-CPU, node-CPU, and node-memory entities across every managed " +
			"remote (GET /resources/top-entities). Each list is already ranked by the server; " +
			"rows preserve that order and are not re-sorted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if timeframe != "" && !stringInSlice(timeframe, validRemoteRrdTimeframes) {
				return fmt.Errorf("get top entities: --timeframe must be one of %s (got %q)",
					strings.Join(validRemoteRrdTimeframes, ", "), timeframe)
			}

			params := &pdmresources.ListTopEntitiesParams{}
			if fl.Changed("timeframe") {
				params.Timeframe = strPtr(timeframe)
			}
			if fl.Changed("view") {
				params.View = strPtr(view)
			}

			resp, err := deps.PDM.Resources.ListTopEntities(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get top entities: %w", err)
			}

			headers := []string{"METRIC", "REMOTE", "RESOURCE-ID"}
			var rows [][]string
			var raws []map[string]any

			groups := []struct {
				metric string
				items  []json.RawMessage
			}{
				{"guest-cpu", resp.GuestCpu},
				{"node-cpu", resp.NodeCpu},
				{"node-memory", resp.NodeMemory},
			}
			for _, g := range groups {
				for _, raw := range g.items {
					var m map[string]any

					err := json.Unmarshal(raw, &m)
					if err != nil {
						return fmt.Errorf("decode top-entities %s entry: %w", g.metric, err)
					}

					remote, _ := m["remote"].(string)
					id := ""
					if resMap, ok := m["resource"].(map[string]any); ok {
						id, _ = resMap["id"].(string)
					}
					m["metric"] = g.metric

					rows = append(rows, []string{g.metric, remote, id})
					raws = append(raws, m)
				}
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade")
	f.StringVar(&view, "view", "", "view name")
	return cmd
}
