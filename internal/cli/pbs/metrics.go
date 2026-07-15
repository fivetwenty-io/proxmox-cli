package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newMetricsCmd builds `pmx pbs metrics` — manage external metric-server
// targets (InfluxDB over HTTP(s) and InfluxDB over UDP) that Proxmox Backup
// Server pushes host and datastore statistics to (/config/metrics CRUD), and
// read the raw metric data points the server currently holds
// (GET /status/metrics).
func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Manage external metric servers and read metric data",
		Long: "List, inspect, create, update, and delete external metric-server targets " +
			"(InfluxDB over HTTP(s) and InfluxDB over UDP) that Proxmox Backup Server " +
			"pushes host and datastore statistics to, and read the raw metric data " +
			"points currently held by the server.",
	}
	cmd.AddCommand(newMetricsInfluxdbHTTPCmd(), newMetricsInfluxdbUDPCmd(), newMetricsDataCmd())
	return cmd
}

// --- shared helpers ----------------------------------------------------------

// metricsFormatOptionalBool dereferences a *bool for table rendering,
// returning "" for nil (PBS applies its own server-side default when the
// field is unset, which the CLI does not attempt to guess).
func metricsFormatOptionalBool(b *bool) string {
	if b == nil {
		return ""
	}

	return strconv.FormatBool(*b)
}

// metricsSecretKeys are the credential fields that must never be echoed back
// to the user. They are forwarded to the API on add/update but the API's GET
// responses include the stored value, so it must be stripped from ls/show output.
var metricsSecretKeys = []string{"token"}

// stripMetricsSecrets deletes every key in metricsSecretKeys from fields, in
// place, mirroring storage.go's handling of the storage password/keyring/
// encryption-key/master-pubkey fields.
func stripMetricsSecrets(fields map[string]any) {
	for _, k := range metricsSecretKeys {
		delete(fields, k)
	}
}

// ===========================================================================
// influxdb-http
// ===========================================================================

// newMetricsInfluxdbHTTPCmd builds `pmx pbs metrics influxdb-http` — manage
// InfluxDB HTTP(s) metric-server targets (/config/metrics/influxdb-http CRUD).
func newMetricsInfluxdbHTTPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "influxdb-http",
		Short: "Manage InfluxDB HTTP(s) metric servers",
		Long: "List, inspect, create, update, and delete InfluxDB HTTP(s) metric-server " +
			"targets (/config/metrics/influxdb-http).",
	}
	cmd.AddCommand(
		newMetricsInfluxdbHTTPLsCmd(),
		newMetricsInfluxdbHTTPShowCmd(),
		newMetricsInfluxdbHTTPAddCmd(),
		newMetricsInfluxdbHTTPUpdateCmd(),
		newMetricsInfluxdbHTTPDeleteCmd(),
	)
	return cmd
}

// metricsInfluxdbHTTPEntry is the decoded shape of one element returned by
// `metrics influxdb-http ls` (GET /config/metrics/influxdb-http).
type metricsInfluxdbHTTPEntry struct {
	Bucket       *string `json:"bucket,omitempty"`
	Comment      *string `json:"comment,omitempty"`
	Enable       *bool   `json:"enable,omitempty"`
	MaxBodySize  *int64  `json:"max-body-size,omitempty"`
	Name         string  `json:"name"`
	Organization *string `json:"organization,omitempty"`
	Token        *string `json:"token,omitempty"`
	Url          string  `json:"url"`
	VerifyTls    *bool   `json:"verify-tls,omitempty"`
}

// newMetricsInfluxdbHTTPLsCmd builds `pmx pbs metrics influxdb-http ls` —
// list every configured InfluxDB HTTP(s) metric server
// (GET /config/metrics/influxdb-http).
func newMetricsInfluxdbHTTPLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List InfluxDB HTTP(s) metric servers",
		Long:    "List every configured InfluxDB HTTP(s) metric server (GET /config/metrics/influxdb-http).",
		Example: "  pmx pbs metrics influxdb-http ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListMetricsInfluxdbHttp(cmd.Context())
			if err != nil {
				return fmt.Errorf("list influxdb-http metric servers: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]metricsInfluxdbHTTPEntry, 0, len(items))

			for _, raw := range items {
				var e metricsInfluxdbHTTPEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode influxdb-http metric server entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{
				"NAME", "URL", "ENABLE", "BUCKET", "ORGANIZATION", "MAX-BODY-SIZE", "VERIFY-TLS", "COMMENT",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name,
					e.Url,
					metricsFormatOptionalBool(e.Enable),
					pbsFormatOptionalString(e.Bucket),
					pbsFormatOptionalString(e.Organization),
					pbsFormatOptionalInt64(e.MaxBodySize),
					metricsFormatOptionalBool(e.VerifyTls),
					pbsFormatOptionalString(e.Comment),
				})
			}

			raws := decodeRawList(items)
			for _, m := range raws {
				stripMetricsSecrets(m)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newMetricsInfluxdbHTTPShowCmd builds `pmx pbs metrics influxdb-http show
// <name>` — show one InfluxDB HTTP(s) metric server's full configuration
// (GET /config/metrics/influxdb-http/{name}).
func newMetricsInfluxdbHTTPShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one InfluxDB HTTP(s) metric server's configuration",
		Long: "Show the full configuration of one InfluxDB HTTP(s) metric server " +
			"(GET /config/metrics/influxdb-http/{name}). The token is write-only and " +
			"never returned by the API. The API also omits options left at their " +
			"built-in defaults; pass --defaults to also list those, with the value " +
			"they effectively have.",
		Example: "  pmx pbs metrics influxdb-http show graf01",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}

			resp, err := deps.PBS.Config.GetMetricsInfluxdbHttp(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show influxdb-http metric server %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode influxdb-http metric server %q: %w", name, err)
			}
			stripMetricsSecrets(fields)

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(
					metricsInfluxdbHTTPOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// metricsInfluxdbHTTPArgs holds the flag values shared by `influxdb-http add`
// and `influxdb-http update`, covering every field of
// CreateMetricsInfluxdbHttpParams and UpdateMetricsInfluxdbHttpParams.
type metricsInfluxdbHTTPArgs struct {
	url          string
	bucket       string
	organization string
	token        string
	maxBodySize  int64
	verifyTls    bool
	enable       bool
	comment      string
}

// registerMetricsInfluxdbHTTPArgs binds the influxdb-http server flags shared
// by add and update onto cmd.
func registerMetricsInfluxdbHTTPArgs(cmd *cobra.Command, a *metricsInfluxdbHTTPArgs) {
	f := cmd.Flags()
	f.StringVar(&a.url, "url", "", "HTTP(s) URL of the InfluxDB server, with optional port")
	f.StringVar(&a.bucket, "bucket", "", "InfluxDB bucket (default: proxmox)")
	f.StringVar(&a.organization, "organization", "", "InfluxDB organization (default: proxmox)")
	f.StringVar(&a.token, "token", "", "InfluxDB API token")
	f.Int64Var(&a.maxBodySize, "max-body-size", 0, "maximum request body size in bytes (default: 25000000)")
	f.BoolVar(&a.verifyTls, "verify-tls", false, "validate the server's TLS certificate (default: true)")
	f.BoolVar(&a.enable, "enable", false, "enable the metric server (default: true)")
	f.StringVar(&a.comment, "comment", "", "comment")
}

// newMetricsInfluxdbHTTPAddCmd builds `pmx pbs metrics influxdb-http add
// <name>` — create an InfluxDB HTTP(s) metric server
// (POST /config/metrics/influxdb-http).
func newMetricsInfluxdbHTTPAddCmd() *cobra.Command {
	var a metricsInfluxdbHTTPArgs
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create an InfluxDB HTTP(s) metric server",
		Long: "Create a new InfluxDB HTTP(s) metric server (POST /config/metrics/influxdb-http). " +
			"--url is required; every other flag is optional and only forwarded when " +
			"explicitly set.",
		Example: "  pmx pbs metrics influxdb-http add graf01 --url http://influx.example.com:8086",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}

			if a.url == "" {
				return fmt.Errorf("--url is required")
			}

			params := &pbsconfig.CreateMetricsInfluxdbHttpParams{
				Name: name,
				Url:  a.url,
			}

			fl := cmd.Flags()
			if fl.Changed("bucket") {
				params.Bucket = strPtr(a.bucket)
			}

			if fl.Changed("organization") {
				params.Organization = strPtr(a.organization)
			}

			if fl.Changed("token") {
				params.Token = strPtr(a.token)
			}

			if fl.Changed("max-body-size") {
				params.MaxBodySize = int64Ptr(a.maxBodySize)
			}

			if fl.Changed("verify-tls") {
				params.VerifyTls = boolPtr(a.verifyTls)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(a.enable)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(a.comment)
			}

			err := deps.PBS.Config.CreateMetricsInfluxdbHttp(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create influxdb-http metric server %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("InfluxDB HTTP(s) metric server %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	registerMetricsInfluxdbHTTPArgs(cmd, &a)
	cli.MustMarkRequired(cmd, "url")
	return cmd
}

// newMetricsInfluxdbHTTPUpdateCmd builds `pmx pbs metrics influxdb-http
// update <name>` — update an InfluxDB HTTP(s) metric server
// (PUT /config/metrics/influxdb-http/{name}).
func newMetricsInfluxdbHTTPUpdateCmd() *cobra.Command {
	var (
		a      metricsInfluxdbHTTPArgs
		digest string
		del    []string
	)
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an InfluxDB HTTP(s) metric server",
		Long: "Update an existing InfluxDB HTTP(s) metric server (PUT " +
			"/config/metrics/influxdb-http/{name}). Only flags explicitly set are " +
			"sent; use --delete to reset properties to their default instead.",
		Example: "  pmx pbs metrics influxdb-http update graf01 --bucket proxmox",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update influxdb-http metric server %q: no changes requested: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, propName := range del {
					if propName == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateMetricsInfluxdbHttpParams{}

			if fl.Changed("url") {
				params.Url = strPtr(a.url)
			}

			if fl.Changed("bucket") {
				params.Bucket = strPtr(a.bucket)
			}

			if fl.Changed("organization") {
				params.Organization = strPtr(a.organization)
			}

			if fl.Changed("token") {
				params.Token = strPtr(a.token)
			}

			if fl.Changed("max-body-size") {
				params.MaxBodySize = int64Ptr(a.maxBodySize)
			}

			if fl.Changed("verify-tls") {
				params.VerifyTls = boolPtr(a.verifyTls)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(a.enable)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(a.comment)
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("delete") {
				params.Delete = del
			}

			err := deps.PBS.Config.UpdateMetricsInfluxdbHttp(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update influxdb-http metric server %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("InfluxDB HTTP(s) metric server %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	registerMetricsInfluxdbHTTPArgs(cmd, &a)
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	return cmd
}

// newMetricsInfluxdbHTTPDeleteCmd builds `pmx pbs metrics influxdb-http
// delete <name>` — remove an InfluxDB HTTP(s) metric server
// (DELETE /config/metrics/influxdb-http/{name}).
func newMetricsInfluxdbHTTPDeleteCmd() *cobra.Command {
	var digest string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an InfluxDB HTTP(s) metric server",
		Long: "Remove an InfluxDB HTTP(s) metric server (DELETE /config/metrics/influxdb-http/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs metrics influxdb-http delete graf01 --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete influxdb-http metric server %q without confirmation: pass --yes/-y",
					name)
			}

			params := &pbsconfig.DeleteMetricsInfluxdbHttpParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteMetricsInfluxdbHttp(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("delete influxdb-http metric server %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("InfluxDB HTTP(s) metric server %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ===========================================================================
// influxdb-udp
// ===========================================================================

// newMetricsInfluxdbUDPCmd builds `pmx pbs metrics influxdb-udp` — manage
// InfluxDB UDP metric-server targets (/config/metrics/influxdb-udp CRUD).
func newMetricsInfluxdbUDPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "influxdb-udp",
		Short: "Manage InfluxDB UDP metric servers",
		Long: "List, inspect, create, update, and delete InfluxDB UDP metric-server " +
			"targets (/config/metrics/influxdb-udp).",
	}
	cmd.AddCommand(
		newMetricsInfluxdbUDPLsCmd(),
		newMetricsInfluxdbUDPShowCmd(),
		newMetricsInfluxdbUDPAddCmd(),
		newMetricsInfluxdbUDPUpdateCmd(),
		newMetricsInfluxdbUDPDeleteCmd(),
	)
	return cmd
}

// metricsInfluxdbUDPEntry is the decoded shape of one element returned by
// `metrics influxdb-udp ls` (GET /config/metrics/influxdb-udp).
type metricsInfluxdbUDPEntry struct {
	Comment *string `json:"comment,omitempty"`
	Enable  *bool   `json:"enable,omitempty"`
	Host    string  `json:"host"`
	Mtu     *int64  `json:"mtu,omitempty"`
	Name    string  `json:"name"`
}

// newMetricsInfluxdbUDPLsCmd builds `pmx pbs metrics influxdb-udp ls` —
// list every configured InfluxDB UDP metric server
// (GET /config/metrics/influxdb-udp).
func newMetricsInfluxdbUDPLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List InfluxDB UDP metric servers",
		Long:    "List every configured InfluxDB UDP metric server (GET /config/metrics/influxdb-udp).",
		Example: "  pmx pbs metrics influxdb-udp ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListMetricsInfluxdbUdp(cmd.Context())
			if err != nil {
				return fmt.Errorf("list influxdb-udp metric servers: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]metricsInfluxdbUDPEntry, 0, len(items))

			for _, raw := range items {
				var e metricsInfluxdbUDPEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode influxdb-udp metric server entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "HOST", "ENABLE", "MTU", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name,
					e.Host,
					metricsFormatOptionalBool(e.Enable),
					pbsFormatOptionalInt64(e.Mtu),
					pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newMetricsInfluxdbUDPShowCmd builds `pmx pbs metrics influxdb-udp show
// <name>` — show one InfluxDB UDP metric server's full configuration
// (GET /config/metrics/influxdb-udp/{name}).
func newMetricsInfluxdbUDPShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one InfluxDB UDP metric server's configuration",
		Long: "Show the full configuration of one InfluxDB UDP metric server " +
			"(GET /config/metrics/influxdb-udp/{name}). The API omits options left " +
			"at their built-in defaults; pass --defaults to also list those, with " +
			"the value they effectively have.",
		Example: "  pmx pbs metrics influxdb-udp show graf01",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}

			resp, err := deps.PBS.Config.GetMetricsInfluxdbUdp(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show influxdb-udp metric server %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode influxdb-udp metric server %q: %w", name, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(
					metricsInfluxdbUDPOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// metricsInfluxdbUDPArgs holds the flag values shared by `influxdb-udp add`
// and `influxdb-udp update`, covering every field of
// CreateMetricsInfluxdbUdpParams and UpdateMetricsInfluxdbUdpParams.
type metricsInfluxdbUDPArgs struct {
	host    string
	mtu     int64
	enable  bool
	comment string
}

// registerMetricsInfluxdbUDPArgs binds the influxdb-udp server flags shared
// by add and update onto cmd.
func registerMetricsInfluxdbUDPArgs(cmd *cobra.Command, a *metricsInfluxdbUDPArgs) {
	f := cmd.Flags()
	f.StringVar(&a.host, "host", "", "host:port combination (host can be a DNS name or IP address)")
	f.Int64Var(&a.mtu, "mtu", 0, "MTU for metric transmission over UDP (default: 1500)")
	f.BoolVar(&a.enable, "enable", false, "enable the metric server (default: true)")
	f.StringVar(&a.comment, "comment", "", "comment")
}

// newMetricsInfluxdbUDPAddCmd builds `pmx pbs metrics influxdb-udp add
// <name>` — create an InfluxDB UDP metric server
// (POST /config/metrics/influxdb-udp).
func newMetricsInfluxdbUDPAddCmd() *cobra.Command {
	var a metricsInfluxdbUDPArgs
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create an InfluxDB UDP metric server",
		Long: "Create a new InfluxDB UDP metric server (POST /config/metrics/influxdb-udp). " +
			"--host is required; every other flag is optional and only forwarded when " +
			"explicitly set.",
		Example: "  pmx pbs metrics influxdb-udp add graf01 --host metrics.example.com:8089",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}

			if a.host == "" {
				return fmt.Errorf("--host is required")
			}

			params := &pbsconfig.CreateMetricsInfluxdbUdpParams{
				Name: name,
				Host: a.host,
			}

			fl := cmd.Flags()
			if fl.Changed("mtu") {
				params.Mtu = int64Ptr(a.mtu)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(a.enable)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(a.comment)
			}

			err := deps.PBS.Config.CreateMetricsInfluxdbUdp(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create influxdb-udp metric server %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("InfluxDB UDP metric server %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	registerMetricsInfluxdbUDPArgs(cmd, &a)
	cli.MustMarkRequired(cmd, "host")
	return cmd
}

// newMetricsInfluxdbUDPUpdateCmd builds `pmx pbs metrics influxdb-udp
// update <name>` — update an InfluxDB UDP metric server
// (PUT /config/metrics/influxdb-udp/{name}).
func newMetricsInfluxdbUDPUpdateCmd() *cobra.Command {
	var (
		a      metricsInfluxdbUDPArgs
		digest string
		del    []string
	)
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an InfluxDB UDP metric server",
		Long: "Update an existing InfluxDB UDP metric server (PUT " +
			"/config/metrics/influxdb-udp/{name}). Only flags explicitly set are " +
			"sent; use --delete to reset properties to their default instead.",
		Example: "  pmx pbs metrics influxdb-udp update graf01 --mtu 1450",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update influxdb-udp metric server %q: no changes requested: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, propName := range del {
					if propName == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateMetricsInfluxdbUdpParams{}

			if fl.Changed("host") {
				params.Host = strPtr(a.host)
			}

			if fl.Changed("mtu") {
				params.Mtu = int64Ptr(a.mtu)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(a.enable)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(a.comment)
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("delete") {
				params.Delete = del
			}

			err := deps.PBS.Config.UpdateMetricsInfluxdbUdp(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update influxdb-udp metric server %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("InfluxDB UDP metric server %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	registerMetricsInfluxdbUDPArgs(cmd, &a)
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	return cmd
}

// newMetricsInfluxdbUDPDeleteCmd builds `pmx pbs metrics influxdb-udp
// delete <name>` — remove an InfluxDB UDP metric server
// (DELETE /config/metrics/influxdb-udp/{name}).
func newMetricsInfluxdbUDPDeleteCmd() *cobra.Command {
	var digest string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an InfluxDB UDP metric server",
		Long: "Remove an InfluxDB UDP metric server (DELETE /config/metrics/influxdb-udp/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs metrics influxdb-udp delete graf01 --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("metric server name must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete influxdb-udp metric server %q without confirmation: pass --yes/-y",
					name)
			}

			params := &pbsconfig.DeleteMetricsInfluxdbUdpParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteMetricsInfluxdbUdp(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("delete influxdb-udp metric server %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("InfluxDB UDP metric server %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ===========================================================================
// data
// ===========================================================================

// metricsDataPoint is one element of the array GET /status/metrics returns:
// a single metric observation, matching the MetricDataPoint shape Proxmox VE
// documents for the analogous GET /cluster/metrics/export endpoint (id,
// metric, timestamp, type, value). Proxmox Backup Server's own API schema
// does not document a return type for /status/metrics (its apidoc entry
// declares "returns": {"type": "null"}), so this shape is inferred from the
// shared metric-export feature rather than a published PBS schema.
type metricsDataPoint struct {
	ID        string  `json:"id"`
	Metric    string  `json:"metric"`
	Timestamp int64   `json:"timestamp"`
	Type      string  `json:"type"`
	Value     float64 `json:"value"`
}

// newMetricsDataCmd builds `pmx pbs metrics data` — read the raw metric data
// points Proxmox Backup Server currently holds for itself and its datastores
// (GET /status/metrics).
//
// The generated Status.ListMetrics binding discards its response body: PBS's
// API schema gives this endpoint no documented return type, so the generator
// produced a method returning only error (the same situation as Nodes.ListRrd
// and Admin.ListDatastoreRrd; see their command comments). This bypasses it
// via the shared raw transport to recover the actual data points.
func newMetricsDataCmd() *cobra.Command {
	var (
		history   bool
		startTime int64
	)
	cmd := &cobra.Command{
		Use:   "data",
		Short: "Show backup server metric data points",
		Long: "Read the raw metric data points Proxmox Backup Server currently holds " +
			"for host and datastore statistics (GET /status/metrics). Use --history to " +
			"also return the last 30 minutes of historic values, optionally bounded by " +
			"--start-time.",
		Example: `  pmx pbs metrics data
  pmx pbs metrics data --history`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := map[string]interface{}{}
			if fl.Changed("history") {
				params["history"] = history
			}

			if fl.Changed("start-time") {
				params["start-time"] = startTime
			}

			resp, err := deps.PBS.Raw.GetRawCtx(cmd.Context(), "/status/metrics", params)
			if err != nil {
				return fmt.Errorf("get backup server metrics: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("get backup server metrics: empty response from server")
			}

			points, raws, err := metricsDecodeDataPoints(resp.Data)
			if err != nil {
				return fmt.Errorf("decode backup server metrics: %w", err)
			}
			sort.Slice(points, func(i, j int) bool {
				if points[i].ID != points[j].ID {
					return points[i].ID < points[j].ID
				}
				if points[i].Metric != points[j].Metric {
					return points[i].Metric < points[j].Metric
				}
				return points[i].Timestamp < points[j].Timestamp
			})

			headers := []string{"ID", "METRIC", "TYPE", "TIMESTAMP", "VALUE"}
			rows := make([][]string, 0, len(points))

			for _, p := range points {
				rows = append(rows, []string{
					p.ID,
					p.Metric,
					p.Type,
					strconv.FormatInt(p.Timestamp, 10),
					metricsFormatValue(p.Value),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&history, "history", false, "include historic values (last 30 minutes)")
	f.Int64Var(&startTime, "start-time", 0,
		"only return values with a timestamp greater than this unix time (effective only with --history)")
	return cmd
}

// metricsDecodeDataPoints decodes the raw response body of GET
// /status/metrics into a slice of typed data points and a slice of lossless
// generic maps (for JSON/YAML rendering). Since PBS does not publish a
// schema for this endpoint, two wire shapes are tolerated: a bare JSON array
// of data points, or a JSON object of the form {"data": [...]} — the shape
// Proxmox VE's documented, schema-identical GET /cluster/metrics/export
// endpoint uses for the same feature. Any other shape, or an element that
// fails to decode as a data point, is a hard error rather than a silently
// truncated result.
func metricsDecodeDataPoints(data any) ([]metricsDataPoint, []map[string]any, error) {
	if data == nil {
		return []metricsDataPoint{}, []map[string]any{}, nil
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal response data: %w", err)
	}

	var items []json.RawMessage

	err = json.Unmarshal(raw, &items)
	if err != nil {
		var wrapper struct {
			Data []json.RawMessage `json:"data"`
		}

		wrapErr := json.Unmarshal(raw, &wrapper)
		if wrapErr != nil {
			return nil, nil, fmt.Errorf(
				"unexpected response shape (neither a JSON array nor a {\"data\": [...]} object): %w", err)
		}

		items = wrapper.Data
	}

	points := make([]metricsDataPoint, 0, len(items))
	raws := make([]map[string]any, 0, len(items))

	for i, item := range items {
		var p metricsDataPoint

		err := json.Unmarshal(item, &p)
		if err != nil {
			return nil, nil, fmt.Errorf("decode metric data point %d: %w", i, err)
		}

		points = append(points, p)

		var m map[string]any

		err = json.Unmarshal(item, &m)
		if err != nil {
			return nil, nil, fmt.Errorf("decode metric data point %d: %w", i, err)
		}

		raws = append(raws, m)
	}

	return points, raws, nil
}

// metricsFormatValue renders a metric value for a table cell. Whole numbers
// render without a trailing ".0", matching the convention datastore.go's
// scalarString uses for other float64-typed API fields.
func metricsFormatValue(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}

	return strconv.FormatFloat(v, 'f', -1, 64)
}
