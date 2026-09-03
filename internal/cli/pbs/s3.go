package pbs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// s3ProviderQuirks are the values accepted by --provider-quirk, per the PBS
// API schema's provider-quirks enum.
var s3ProviderQuirks = []string{"skip-if-none-match-header", "delete-objects-via-delete-object"}

// s3DeletableProperties are the property names that PUT /config/s3/{id} accepts
// in its delete list, per the PBS API schema. The identity, endpoint, and
// credentials cannot be reset because an endpoint is meaningless without them.
var s3DeletableProperties = []string{
	"port", "region", "fingerprint", "path-style", "rate-in", "burst-in", "rate-out",
	"burst-out", "limit-active-requests", "limit-passive-requests", "provider-quirks",
	"use-node-proxy",
}

// newS3Cmd builds `pmx pbs s3` — manage the S3-compatible object-store
// endpoint configurations that S3-backed datastores reference
// (/config/s3 CRUD, bucket listing, and the /admin/s3 sanity checks).
func newS3Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "s3",
		Short: "Manage S3 endpoint configurations",
		Long: "List, inspect, create, update, and delete the S3-compatible object-store " +
			"endpoint configurations that S3-backed datastores reference, list the " +
			"buckets an endpoint can reach, and run the server-side sanity checks.",
	}
	cmd.AddCommand(
		newS3LsCmd(),
		newS3ShowCmd(),
		newS3AddCmd(),
		newS3UpdateCmd(),
		newS3DeleteCmd(),
		newS3BucketsCmd(),
	)
	return cmd
}

// s3ListEntry is the decoded shape of one element of GET /config/s3. The
// list omits the secret key by design. Port uses the SDK's tolerant integer,
// matching pbsconfig.GetS3Response, so a string-encoded number does not fail
// the whole listing.
type s3ListEntry struct {
	AccessKey string      `json:"access-key"`
	Endpoint  string      `json:"endpoint"`
	Id        string      `json:"id"`
	Port      *pve.PVEInt `json:"port,omitempty"`
	Region    *string     `json:"region,omitempty"`
}

// s3PortCell renders an optional tolerant integer for the table, "" when unset.
func s3PortCell(p *pve.PVEInt) string {
	if p == nil {
		return ""
	}
	v := p.Int()
	return pbsFormatOptionalInt64(&v)
}

// newS3LsCmd builds `pmx pbs s3 ls` — list configured S3 endpoints
// (GET /config/s3).
func newS3LsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List configured S3 endpoints",
		Long:    "List the S3-compatible endpoint configurations on this server. Secret keys are never returned.",
		Example: "  pmx pbs s3 ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListS3(cmd.Context())
			if err != nil {
				return fmt.Errorf("list s3 endpoints: %w", err)
			}

			table, err := cli.DecodePairedRows[s3ListEntry](rawItemsOf(resp), "s3 endpoint")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Id < table[j].Entry.Id })

			headers := []string{"ID", "ENDPOINT", "PORT", "REGION", "ACCESS-KEY"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))
			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Id, e.Endpoint, s3PortCell(e.Port), pbsFormatOptionalString(e.Region), e.AccessKey,
				})
				raws = append(raws, t.Raw)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: raws}, deps.Format)
		},
	}
}

// newS3ShowCmd builds `pmx pbs s3 show <id>` — show one endpoint's
// configuration (GET /config/s3/{id}).
func newS3ShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a single S3 endpoint configuration",
		Long: "Show every populated field of one S3 endpoint configuration (GET " +
			"/config/s3/{id}). The secret key is never returned. The PBS API omits " +
			"options left at their built-in defaults; pass --defaults to also list " +
			"those, with the value they effectively have.",
		Example: "  pmx pbs s3 show minio-lab",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			resp, err := deps.PBS.Config.GetS3(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get s3 endpoint %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode s3 endpoint %q: %w", id, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(s3OptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// s3Flags collects the endpoint attribute flags shared by `add` and
// `update`. Every field maps onto a CreateS3Params / UpdateS3Params field
// of the same name.
type s3Flags struct {
	endpoint             string
	accessKey            string
	secretKey            string
	region               string
	fingerprint          string
	port                 int64
	pathStyle            bool
	useNodeProxy         bool
	rateIn               string
	rateOut              string
	burstIn              string
	burstOut             string
	limitActiveRequests  int64
	limitPassiveRequests int64
	putRateLimit         int64
	providerQuirks       []string

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `add` and `update`.
func (sf *s3Flags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&sf.endpoint, "endpoint", "", "endpoint host name or address of the S3 object store")
	f.StringVar(&sf.accessKey, "access-key", "", "access key for the S3 object store")
	f.StringVar(&sf.secretKey, "secret-key", "", "secret key for the S3 object store (write-only)")
	f.StringVar(&sf.region, "region", "", "region of the S3 object store")
	f.StringVar(&sf.fingerprint, "fingerprint", "", "X509 certificate fingerprint (sha256) of the endpoint")
	f.Int64Var(&sf.port, "port", 0, "port of the S3 object store (default: 443)")
	f.BoolVar(&sf.pathStyle, "path-style", false, "use path-style bucket addressing instead of virtual-host style")
	f.BoolVar(&sf.useNodeProxy, "use-node-proxy", false, "use the node's HTTP proxy configuration for this endpoint")
	f.StringVar(&sf.rateIn, "rate-in", "", "inbound rate limit as a byte size with optional unit (e.g. 100MiB)")
	f.StringVar(&sf.rateOut, "rate-out", "", "outbound rate limit as a byte size with optional unit (e.g. 100MiB)")
	f.StringVar(&sf.burstIn, "burst-in", "", "inbound burst size as a byte size with optional unit")
	f.StringVar(&sf.burstOut, "burst-out", "", "outbound burst size as a byte size with optional unit")
	f.Int64Var(&sf.limitActiveRequests, "limit-active-requests", 0,
		"combined rate limit for PUT, POST, and DELETE requests, in requests per second")
	f.Int64Var(&sf.limitPassiveRequests, "limit-passive-requests", 0,
		"combined rate limit for GET and HEAD requests, in requests per second")
	// Deliberately not MarkDeprecated: pflag hides deprecated flags, and this
	// one must stay visible in help, man pages, and completions.
	f.Int64Var(&sf.putRateLimit, "put-rate-limit", 0,
		"rate limit for PUT requests in requests per second (deprecated upstream; prefer --limit-active-requests)")
	f.StringArrayVar(&sf.providerQuirks, "provider-quirk", nil,
		"provider-specific quirk to enable (repeatable): "+strings.Join(s3ProviderQuirks, ", "))
}

// registerUpdate binds every flag `update` accepts, including the
// update-only delete/digest fields.
func (sf *s3Flags) registerUpdate(cmd *cobra.Command) {
	sf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&sf.del, "delete", nil,
		"property name to reset to its default (repeatable): "+strings.Join(s3DeletableProperties, ", "))
	f.StringVar(&sf.digest, "digest", "", "only update if the current config digest matches")
}

// validateS3ProviderQuirks rejects any quirk outside the API's enum, naming
// the offending value so a typo is caught before the request is sent.
func validateS3ProviderQuirks(quirks []string) error {
	for _, q := range quirks {
		if !slices.Contains(s3ProviderQuirks, q) {
			return fmt.Errorf("--provider-quirk: unknown quirk %q: want one of %s", q, strings.Join(s3ProviderQuirks, ", "))
		}
	}
	return nil
}

// applyCreate copies the optional flags that were explicitly set onto p.
func (sf *s3Flags) applyCreate(cmd *cobra.Command, p *pbsconfig.CreateS3Params) {
	fl := cmd.Flags()
	if fl.Changed("region") {
		p.Region = &sf.region
	}
	if fl.Changed("fingerprint") {
		p.Fingerprint = &sf.fingerprint
	}
	if fl.Changed("port") {
		p.Port = &sf.port
	}
	if fl.Changed("path-style") {
		p.PathStyle = &sf.pathStyle
	}
	if fl.Changed("use-node-proxy") {
		p.UseNodeProxy = &sf.useNodeProxy
	}
	if fl.Changed("rate-in") {
		p.RateIn = &sf.rateIn
	}
	if fl.Changed("rate-out") {
		p.RateOut = &sf.rateOut
	}
	if fl.Changed("burst-in") {
		p.BurstIn = &sf.burstIn
	}
	if fl.Changed("burst-out") {
		p.BurstOut = &sf.burstOut
	}
	if fl.Changed("limit-active-requests") {
		p.LimitActiveRequests = &sf.limitActiveRequests
	}
	if fl.Changed("limit-passive-requests") {
		p.LimitPassiveRequests = &sf.limitPassiveRequests
	}
	if fl.Changed("put-rate-limit") {
		p.PutRateLimit = &sf.putRateLimit
	}
	if fl.Changed("provider-quirk") {
		p.ProviderQuirks = sf.providerQuirks
	}
}

// applyUpdate builds the update payload, forwarding flags only when set.
func (sf *s3Flags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateS3Params) {
	fl := cmd.Flags()
	if fl.Changed("endpoint") {
		p.Endpoint = &sf.endpoint
	}
	if fl.Changed("access-key") {
		p.AccessKey = &sf.accessKey
	}
	if fl.Changed("secret-key") {
		p.SecretKey = &sf.secretKey
	}
	if fl.Changed("region") {
		p.Region = &sf.region
	}
	if fl.Changed("fingerprint") {
		p.Fingerprint = &sf.fingerprint
	}
	if fl.Changed("port") {
		p.Port = &sf.port
	}
	if fl.Changed("path-style") {
		p.PathStyle = &sf.pathStyle
	}
	if fl.Changed("use-node-proxy") {
		p.UseNodeProxy = &sf.useNodeProxy
	}
	if fl.Changed("rate-in") {
		p.RateIn = &sf.rateIn
	}
	if fl.Changed("rate-out") {
		p.RateOut = &sf.rateOut
	}
	if fl.Changed("burst-in") {
		p.BurstIn = &sf.burstIn
	}
	if fl.Changed("burst-out") {
		p.BurstOut = &sf.burstOut
	}
	if fl.Changed("limit-active-requests") {
		p.LimitActiveRequests = &sf.limitActiveRequests
	}
	if fl.Changed("limit-passive-requests") {
		p.LimitPassiveRequests = &sf.limitPassiveRequests
	}
	if fl.Changed("put-rate-limit") {
		p.PutRateLimit = &sf.putRateLimit
	}
	if fl.Changed("provider-quirk") {
		p.ProviderQuirks = sf.providerQuirks
	}
	if fl.Changed("delete") {
		p.Delete = sf.del
	}
	if fl.Changed("digest") {
		p.Digest = &sf.digest
	}
}

// newS3AddCmd builds `pmx pbs s3 add <id>` — create an endpoint
// configuration (POST /config/s3). --endpoint, --access-key, and
// --secret-key are required; every other option is optional.
func newS3AddCmd() *cobra.Command {
	var sf s3Flags
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create an S3 endpoint configuration",
		Long: "Create a new S3-compatible endpoint configuration (POST /config/s3). " +
			"--endpoint, --access-key, and --secret-key are required; every other " +
			"option is optional and only forwarded when explicitly set.",
		Example: `  pmx pbs s3 add minio-lab --endpoint minio.lab.internal --port 9000 --path-style \
  --access-key "$S3_ACCESS_KEY" --secret-key "$S3_SECRET_KEY"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if sf.endpoint == "" {
				return fmt.Errorf("--endpoint is required")
			}
			if sf.accessKey == "" {
				return fmt.Errorf("--access-key is required")
			}
			if sf.secretKey == "" {
				return fmt.Errorf("--secret-key is required")
			}
			if err := validateS3ProviderQuirks(sf.providerQuirks); err != nil {
				return err
			}

			params := &pbsconfig.CreateS3Params{
				Id:        id,
				Endpoint:  sf.endpoint,
				AccessKey: sf.accessKey,
				SecretKey: sf.secretKey,
			}
			sf.applyCreate(cmd, params)

			if err := deps.PBS.Config.CreateS3(cmd.Context(), params); err != nil {
				return fmt.Errorf("create s3 endpoint %q: %w", id, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("S3 endpoint %q created.", id)}, deps.Format)
		},
	}
	sf.registerCommon(cmd)
	cli.MustMarkRequired(cmd, "endpoint")
	cli.MustMarkRequired(cmd, "access-key")
	cli.MustMarkRequired(cmd, "secret-key")
	return cmd
}

// newS3UpdateCmd builds `pmx pbs s3 update <id>` — update an endpoint
// configuration (PUT /config/s3/{id}). Only flags explicitly set are sent;
// use --delete to reset properties to their default.
func newS3UpdateCmd() *cobra.Command {
	var sf s3Flags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an S3 endpoint configuration",
		Long: "Update an existing S3-compatible endpoint configuration (PUT /config/s3/{id}). " +
			"Only flags explicitly set are sent; pass --secret-key to rotate the " +
			"credential, and --delete to reset properties to their default instead.",
		Example: `  pmx pbs s3 update minio-lab --secret-key "$S3_SECRET_KEY"
  pmx pbs s3 update minio-lab --delete region --delete port`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update s3 endpoint %q: no changes requested: pass at least one flag", id)
			}
			if err := validateS3ProviderQuirks(sf.providerQuirks); err != nil {
				return err
			}
			if cmd.Flags().Changed("delete") {
				for _, prop := range sf.del {
					if !slices.Contains(s3DeletableProperties, prop) {
						return fmt.Errorf("--delete: property %q cannot be reset: want one of %s",
							prop, strings.Join(s3DeletableProperties, ", "))
					}
				}
			}

			params := &pbsconfig.UpdateS3Params{}
			sf.applyUpdate(cmd, params)

			if err := deps.PBS.Config.UpdateS3(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update s3 endpoint %q: %w", id, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("S3 endpoint %q updated.", id)}, deps.Format)
		},
	}
	sf.registerUpdate(cmd)
	return cmd
}

// newS3DeleteCmd builds `pmx pbs s3 delete <id>` — remove an endpoint
// configuration (DELETE /config/s3/{id}).
func newS3DeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an S3 endpoint configuration",
		Long: "Remove an S3-compatible endpoint configuration (DELETE /config/s3/{id}). " +
			"Datastores that reference the endpoint stop working. This is " +
			"destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs s3 delete minio-lab --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete s3 endpoint %q without confirmation: pass --yes/-y", id)
			}

			params := &pbsconfig.DeleteS3Params{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			if err := deps.PBS.Config.DeleteS3(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("delete s3 endpoint %q: %w", id, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("S3 endpoint %q deleted.", id)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// s3BucketsHeaders is the single-column table `s3 buckets` renders.
var s3BucketsHeaders = []string{"BUCKET"}

// s3Bucket pairs a bucket's display name with the raw element it came from,
// so sorting once keeps the table and the JSON view in the same order.
type s3Bucket struct {
	name string
	raw  any
}

// s3BucketsResult renders the body of GET /config/s3/{id}/list-buckets. The
// API schema declares the return type as null, so the SDK discards the body
// and we read it through the raw client instead. The server actually returns
// the bucket names; both a plain string array and an array of objects with a
// "name" field are accepted so a future schema change does not break the
// verb. A literal null or empty array renders exactly like any empty list:
// the header row in table view and [] in JSON. The table renderer prints
// Headers before it ever looks at Message, and the JSON renderer would emit
// an object for a header-only result, so no Message is set on this path.
func s3BucketsResult(raw json.RawMessage) (output.Result, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return output.Result{Headers: s3BucketsHeaders, Rows: [][]string{}, Raw: []any{}}, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return output.Result{}, fmt.Errorf("decode bucket list: %w", err)
	}

	buckets := make([]s3Bucket, 0, len(items))
	for _, item := range items {
		var name string
		if err := json.Unmarshal(item, &name); err == nil {
			buckets = append(buckets, s3Bucket{name: name, raw: name})
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(item, &obj); err != nil {
			return output.Result{}, fmt.Errorf("decode bucket list: unexpected element %s", string(item))
		}
		buckets = append(buckets, s3Bucket{name: output.Cell(obj["name"]), raw: obj})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].name < buckets[j].name })

	rows := make([][]string, 0, len(buckets))
	raws := make([]any, 0, len(buckets))
	for _, b := range buckets {
		rows = append(rows, []string{b.name})
		raws = append(raws, b.raw)
	}

	return output.Result{Headers: s3BucketsHeaders, Rows: rows, Raw: raws}, nil
}

// newS3BucketsCmd builds `pmx pbs s3 buckets <id>` — list the buckets an
// endpoint's credentials can see (GET /config/s3/{id}/list-buckets).
func newS3BucketsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "buckets <id>",
		Short: "List the buckets an S3 endpoint can reach",
		Long: "List the buckets visible to an S3 endpoint's credentials (GET " +
			"/config/s3/{id}/list-buckets). The server contacts the object store, so " +
			"this doubles as a connectivity check.",
		Example: "  pmx pbs s3 buckets minio-lab",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			raw, err := cli.RawGetJSON(cmd.Context(), deps.PBS.Raw, "/config/s3/"+url.PathEscape(id)+"/list-buckets", nil)
			if err != nil {
				return fmt.Errorf("list buckets on s3 endpoint %q: %w", id, err)
			}

			res, err := s3BucketsResult(raw)
			if err != nil {
				return fmt.Errorf("list buckets on s3 endpoint %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
