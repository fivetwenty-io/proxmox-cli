package cluster

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newMetricsCmd builds the `pmx cluster metrics` sub-tree for managing external
// metric servers (Graphite, InfluxDB, OpenTelemetry) that Proxmox VE pushes
// cluster and node statistics to, and for reading the raw metric export.
func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Manage external metric servers and read metric exports",
		Long: "List, inspect, create, update, and delete external metric servers " +
			"(Graphite, InfluxDB, OpenTelemetry), and export the cluster's current metrics.",
	}
	cmd.AddCommand(newMetricsServerCmd(), newMetricsExportCmd())
	return cmd
}

func newMetricsServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage external metric server targets",
	}
	cmd.AddCommand(
		newMetricsServerListCmd(),
		newMetricsServerGetCmd(),
		newMetricsServerCreateCmd(),
		newMetricsServerSetCmd(),
		newMetricsServerDeleteCmd(),
	)
	return cmd
}

// metricsServerListColumns are the stable columns rendered for the server list.
var metricsServerListColumns = []string{"id", "type", "server", "port", "disable", "comment"}

// metricsSecretKeys are the credential fields that must never be echoed back
// to the user. --token is forwarded to the API on create/set but the API's
// GET/list responses include the stored value, so it must be stripped from
// get/list output, mirroring the omitted TOKEN column in the list table.
var metricsSecretKeys = []string{"token"}

// stripMetricsSecrets deletes every key in metricsSecretKeys from each entry,
// in place.
func stripMetricsSecrets(entries []map[string]any) {
	for _, e := range entries {
		for _, k := range metricsSecretKeys {
			delete(e, k)
		}
	}
}

func newMetricsServerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured metric servers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListMetricsServer(cmd.Context())
			if err != nil {
				return fmt.Errorf("list metric servers: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			res, err := rawFixedColumnsResult(raws, metricsServerListColumns)
			if err != nil {
				return fmt.Errorf("list metric servers: %w", err)
			}
			// The table columns omit token, but Raw carries every decoded field
			// (rawFixedColumnsResult keeps it lossless for -o json/yaml), so it
			// must be stripped here too. The assertion is hard, not a silent
			// "if ok" no-op: if rawFixedColumnsResult's Raw type ever changes,
			// this must fail loudly rather than skip stripping the token.
			entries, ok := res.Raw.([]map[string]any)
			if !ok {
				return fmt.Errorf("list metric servers: unexpected raw result type %T, cannot strip secrets", res.Raw)
			}
			stripMetricsSecrets(entries)
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newMetricsServerGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show a single metric server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.GetMetricsServer(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get metric server %q: %w", id, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get metric server %q: %w", id, err)
			}
			for _, k := range metricsSecretKeys {
				delete(single, k)
			}
			// Same hard-assertion rationale as the list command above: a
			// future change to objectToSingle's Raw type must surface as an
			// error, not silently skip stripping the token.
			obj, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("get metric server %q: unexpected raw result type %T, cannot strip secrets", id, raw)
			}
			stripMetricsSecrets([]map[string]any{obj})
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// metricsServerFlags holds the create/update flag values for a metric server.
// Create and update share almost the entire field set; create additionally
// takes --type (the plugin type cannot change after creation), while update
// adds --delete and --digest. The PVE client models Server and Port as required
// (non-pointer) on both create and update, so both verbs require them.
type metricsServerFlags struct {
	typ               string
	server            string
	port              int64
	disable           bool
	proto             string
	path              string
	mtu               int64
	timeout           int64
	influxdbproto     string
	organization      string
	bucket            string
	token             string
	apiPathPrefix     string
	maxBodySize       int64
	verifyCertificate bool
	otelProtocol      string
	otelPath          string
	otelCompression   string
	otelHeaders       string
	otelResourceAttrs string
	otelMaxBodySize   int64
	otelTimeout       int64
	otelVerifySSL     bool
	del               string
	digest            string
}

// register wires the metric-server flags onto cmd. When forUpdate is set the
// create-only --type flag is omitted and the update-only --delete/--digest
// flags are added.
func (m *metricsServerFlags) register(cmd *cobra.Command, forUpdate bool) {
	f := cmd.Flags()
	if !forUpdate {
		f.StringVar(&m.typ, "type", "", "plugin type: graphite, influxdb, or opentelemetry (required)")
	}
	f.StringVar(&m.server, "server", "", "server DNS name or IP address (required)")
	f.Int64Var(&m.port, "port", 0, "server network port (required)")
	f.BoolVar(&m.disable, "disable", false, "disable the plugin")
	f.StringVar(&m.proto, "proto", "", "graphite transport protocol: tcp or udp")
	f.StringVar(&m.path, "path", "", "graphite root path, for example proxmox.mycluster.mykey")
	f.Int64Var(&m.mtu, "mtu", 0, "MTU for metric transmission over UDP")
	f.Int64Var(&m.timeout, "timeout", 0, "socket timeout in seconds")
	f.StringVar(&m.influxdbproto, "influxdbproto", "", "InfluxDB protocol: udp, http, or https")
	f.StringVar(&m.organization, "organization", "", "InfluxDB organization (http v2 API)")
	f.StringVar(&m.bucket, "bucket", "", "InfluxDB bucket or database (http v2 API)")
	f.StringVar(&m.token, "token", "", "InfluxDB access token (http v2 API)")
	f.StringVar(&m.apiPathPrefix, "api-path-prefix", "", "API path prefix inserted before /api2/ (reverse proxy)")
	f.Int64Var(&m.maxBodySize, "max-body-size", 0, "InfluxDB max body size in bytes")
	f.BoolVar(&m.verifyCertificate, "verify-certificate", true, "verify SSL certificates for https endpoints")
	f.StringVar(&m.otelProtocol, "otel-protocol", "", "OpenTelemetry HTTP protocol")
	f.StringVar(&m.otelPath, "otel-path", "", "OpenTelemetry OTLP endpoint path")
	f.StringVar(&m.otelCompression, "otel-compression", "", "OpenTelemetry request compression algorithm")
	f.StringVar(&m.otelHeaders, "otel-headers", "", "OpenTelemetry custom HTTP headers (JSON, base64 encoded)")
	f.StringVar(&m.otelResourceAttrs, "otel-resource-attributes", "", "OpenTelemetry resource attributes (JSON, base64 encoded)")
	f.Int64Var(&m.otelMaxBodySize, "otel-max-body-size", 0, "OpenTelemetry maximum request body size in bytes")
	f.Int64Var(&m.otelTimeout, "otel-timeout", 0, "OpenTelemetry HTTP request timeout in seconds")
	f.BoolVar(&m.otelVerifySSL, "otel-verify-ssl", true, "verify SSL certificates for the OpenTelemetry endpoint")
	if forUpdate {
		f.StringVar(&m.del, "delete", "", "comma-separated list of settings to reset to default")
		f.StringVar(&m.digest, "digest", "", "prevent changes if the config digest differs")
	}
}

// applyCreate builds the create payload, forwarding optional flags only when set.
func (m *metricsServerFlags) applyCreate(fl flagChecker) *pvecluster.CreateMetricsServerParams {
	p := &pvecluster.CreateMetricsServerParams{Type: m.typ, Server: m.server, Port: m.port}
	if fl.Changed("disable") {
		p.Disable = &m.disable
	}
	if fl.Changed("proto") {
		p.Proto = &m.proto
	}
	if fl.Changed("path") {
		p.Path = &m.path
	}
	if fl.Changed("mtu") {
		p.Mtu = &m.mtu
	}
	if fl.Changed("timeout") {
		p.Timeout = &m.timeout
	}
	if fl.Changed("influxdbproto") {
		p.Influxdbproto = &m.influxdbproto
	}
	if fl.Changed("organization") {
		p.Organization = &m.organization
	}
	if fl.Changed("bucket") {
		p.Bucket = &m.bucket
	}
	if fl.Changed("token") {
		p.Token = &m.token
	}
	if fl.Changed("api-path-prefix") {
		p.ApiPathPrefix = &m.apiPathPrefix
	}
	if fl.Changed("max-body-size") {
		p.MaxBodySize = &m.maxBodySize
	}
	if fl.Changed("verify-certificate") {
		p.VerifyCertificate = &m.verifyCertificate
	}
	if fl.Changed("otel-protocol") {
		p.OtelProtocol = &m.otelProtocol
	}
	if fl.Changed("otel-path") {
		p.OtelPath = &m.otelPath
	}
	if fl.Changed("otel-compression") {
		p.OtelCompression = &m.otelCompression
	}
	if fl.Changed("otel-headers") {
		p.OtelHeaders = &m.otelHeaders
	}
	if fl.Changed("otel-resource-attributes") {
		p.OtelResourceAttributes = &m.otelResourceAttrs
	}
	if fl.Changed("otel-max-body-size") {
		p.OtelMaxBodySize = &m.otelMaxBodySize
	}
	if fl.Changed("otel-timeout") {
		p.OtelTimeout = &m.otelTimeout
	}
	if fl.Changed("otel-verify-ssl") {
		p.OtelVerifySsl = &m.otelVerifySSL
	}
	return p
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (m *metricsServerFlags) applyUpdate(fl flagChecker) *pvecluster.UpdateMetricsServerParams {
	p := &pvecluster.UpdateMetricsServerParams{Server: m.server, Port: m.port}
	if fl.Changed("disable") {
		p.Disable = &m.disable
	}
	if fl.Changed("proto") {
		p.Proto = &m.proto
	}
	if fl.Changed("path") {
		p.Path = &m.path
	}
	if fl.Changed("mtu") {
		p.Mtu = &m.mtu
	}
	if fl.Changed("timeout") {
		p.Timeout = &m.timeout
	}
	if fl.Changed("influxdbproto") {
		p.Influxdbproto = &m.influxdbproto
	}
	if fl.Changed("organization") {
		p.Organization = &m.organization
	}
	if fl.Changed("bucket") {
		p.Bucket = &m.bucket
	}
	if fl.Changed("token") {
		p.Token = &m.token
	}
	if fl.Changed("api-path-prefix") {
		p.ApiPathPrefix = &m.apiPathPrefix
	}
	if fl.Changed("max-body-size") {
		p.MaxBodySize = &m.maxBodySize
	}
	if fl.Changed("verify-certificate") {
		p.VerifyCertificate = &m.verifyCertificate
	}
	if fl.Changed("otel-protocol") {
		p.OtelProtocol = &m.otelProtocol
	}
	if fl.Changed("otel-path") {
		p.OtelPath = &m.otelPath
	}
	if fl.Changed("otel-compression") {
		p.OtelCompression = &m.otelCompression
	}
	if fl.Changed("otel-headers") {
		p.OtelHeaders = &m.otelHeaders
	}
	if fl.Changed("otel-resource-attributes") {
		p.OtelResourceAttributes = &m.otelResourceAttrs
	}
	if fl.Changed("otel-max-body-size") {
		p.OtelMaxBodySize = &m.otelMaxBodySize
	}
	if fl.Changed("otel-timeout") {
		p.OtelTimeout = &m.otelTimeout
	}
	if fl.Changed("otel-verify-ssl") {
		p.OtelVerifySsl = &m.otelVerifySSL
	}
	if fl.Changed("delete") {
		p.Delete = &m.del
	}
	if fl.Changed("digest") {
		p.Digest = &m.digest
	}
	return p
}

func newMetricsServerCreateCmd() *cobra.Command {
	m := &metricsServerFlags{}
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a metric server",
		Long: "Create an external metric server. --type selects the plugin (graphite, " +
			"influxdb, or opentelemetry); --server and --port address the target.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := m.applyCreate(cmd.Flags())
			if err := deps.API.Cluster.CreateMetricsServer(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("create metric server %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Metric server %q created.", id)}, deps.Format)
		},
	}
	m.register(cmd, false)
	cli.MustMarkRequired(cmd, "type")
	cli.MustMarkRequired(cmd, "server")
	cli.MustMarkRequired(cmd, "port")
	return cmd
}

func newMetricsServerSetCmd() *cobra.Command {
	m := &metricsServerFlags{}
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update a metric server",
		Long: "Update a metric server. --server and --port are required because the API " +
			"rewrites the full target address on every update.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := m.applyUpdate(cmd.Flags())
			if err := deps.API.Cluster.UpdateMetricsServer(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update metric server %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Metric server %q updated.", id)}, deps.Format)
		},
	}
	m.register(cmd, true)
	cli.MustMarkRequired(cmd, "server")
	cli.MustMarkRequired(cmd, "port")
	return cmd
}

func newMetricsServerDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a metric server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete metric server %q without confirmation: pass --yes/-y", id)
			}
			if err := deps.API.Cluster.DeleteMetricsServer(cmd.Context(), id); err != nil {
				return fmt.Errorf("delete metric server %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Metric server %q deleted.", id)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

func newMetricsExportCmd() *cobra.Command {
	var (
		history   bool
		localOnly bool
		nodeList  string
		startTime int64
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the cluster's current metrics",
		Long:  "Return the metrics Proxmox VE would push to its configured servers.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.ListMetricsExportParams{}
			if fl.Changed("history") {
				params.History = &history
			}
			if fl.Changed("local-only") {
				params.LocalOnly = &localOnly
			}
			if fl.Changed("node-list") {
				params.NodeList = &nodeList
			}
			if fl.Changed("start-time") {
				params.StartTime = &startTime
			}
			resp, err := deps.API.Cluster.ListMetricsExport(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("export metrics: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = resp.Data
			}
			res, err := rawUnionResult(raws)
			if err != nil {
				return fmt.Errorf("export metrics: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&history, "history", false, "return historic values")
	f.BoolVar(&localOnly, "local-only", false, "return metrics for the current node only")
	f.StringVar(&nodeList, "node-list", "", "comma-separated list of nodes to return metrics from")
	f.Int64Var(&startTime, "start-time", 0, "only include metrics with a timestamp greater than this unix time")
	return cmd
}

// flagChecker is the subset of *pflag.FlagSet used to forward only changed flags.
type flagChecker interface{ Changed(string) bool }

// rawFixedColumnsResult decodes a raw object list into a table with the given
// fixed columns. Unknown keys are dropped from the table but preserved in Raw.
func rawFixedColumnsResult(raws []json.RawMessage, columns []string) (output.Result, error) {
	entries := make([]map[string]any, 0, len(raws))
	for _, raw := range raws {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return output.Result{}, fmt.Errorf("decode object: %w", err)
		}
		entries = append(entries, m)
	}
	headers := make([]string, len(columns))
	for i, k := range columns {
		headers[i] = upperHeader(k)
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		row := make([]string, len(columns))
		for i, k := range columns {
			row[i] = anyCell(e[k])
		}
		rows = append(rows, row)
	}
	return output.Result{Headers: headers, Rows: rows, Raw: entries}, nil
}

// rawUnionResult decodes a raw object list into a table whose columns are the
// sorted union of every object's keys. Raw preserves the lossless decode.
func rawUnionResult(raws []json.RawMessage) (output.Result, error) {
	entries := make([]map[string]any, 0, len(raws))
	keySet := map[string]struct{}{}
	for _, raw := range raws {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return output.Result{}, fmt.Errorf("decode object: %w", err)
		}
		entries = append(entries, m)
		for k := range m {
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
		headers[i] = upperHeader(k)
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		row := make([]string, len(keys))
		for i, k := range keys {
			row[i] = anyCell(e[k])
		}
		rows = append(rows, row)
	}
	return output.Result{Headers: headers, Rows: rows, Raw: entries}, nil
}
