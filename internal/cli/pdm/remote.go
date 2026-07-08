package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pdmremotes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/remotes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// validRemoteTypes are the remote-entry type enum values accepted by
// POST /remotes/remote, per the PDM API schema.
var validRemoteTypes = []string{"pve", "pbs"}

// validRemoteRrdTimeframes are the RRD time-frame enum values accepted by
// GET /remotes/remote/{id}/rrddata, per the PDM API schema.
var validRemoteRrdTimeframes = []string{"hour", "day", "week", "month", "year", "decade"}

// validRemoteRrdConsolidations are the RRD consolidation-function enum
// values accepted by GET /remotes/remote/{id}/rrddata, per the PDM API schema.
var validRemoteRrdConsolidations = []string{"MAX", "AVERAGE"}

// remoteSecretKeys are the credential fields that must never be echoed back
// to the user. They are forwarded to the API on add/update but the API's GET
// responses include the stored value, so it must be stripped from ls/show output.
var remoteSecretKeys = []string{"token"}

// stripRemoteSecrets deletes every key in remoteSecretKeys from fields, in place.
func stripRemoteSecrets(fields map[string]any) {
	for _, k := range remoteSecretKeys {
		delete(fields, k)
	}
}

// newRemoteCmd builds `pmx pdm remote` — manage the PVE/PBS remotes this
// Proxmox Datacenter Manager instance manages (/remotes/remote CRUD), and
// inspect a remote's version, TLS certificate, RRD metrics, cached tasks,
// update summary, and metric-collection status.
func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage remotes registered with this Proxmox Datacenter Manager",
		Long: "List, inspect, add, update, and remove the PVE/PBS remotes this " +
			"Proxmox Datacenter Manager instance manages, and inspect a remote's " +
			"version, TLS certificate, RRD metrics, cached tasks, update summary, " +
			"and metric-collection status.",
	}
	cmd.AddCommand(
		newRemoteLsCmd(),
		newRemoteShowCmd(),
		newRemoteAddCmd(),
		newRemoteUpdateCmd(),
		newRemoteDeleteCmd(),
		newRemoteVersionCmd(),
		newRemoteProbeCertificateCmd(),
		newRemoteRrddataCmd(),
		newRemoteTaskCmd(),
		newRemoteUpdatesCmd(),
		newRemoteMetricCollectionCmd(),
	)
	return cmd
}

// remoteListEntry is the decoded shape of one element of GET /remotes/remote.
// Token is deliberately omitted from this struct: it is write-only
// credential material and must never be rendered (the Raw output goes
// through stripRemoteSecrets for the same reason).
type remoteListEntry struct {
	Id     string   `json:"id"`
	Type   string   `json:"type"`
	Authid string   `json:"authid"`
	Nodes  []string `json:"nodes"`
	WebUrl *string  `json:"web-url,omitempty"`
}

// newRemoteLsCmd builds `pmx pdm remote ls` — list every managed remote
// (GET /remotes/remote).
func newRemoteLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List managed remotes",
		Long:  "List every PVE/PBS remote this Proxmox Datacenter Manager instance is managing.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Remotes.ListRemote(cmd.Context())
			if err != nil {
				return fmt.Errorf("list remotes: %w", err)
			}

			items := rawItemsOf(resp)
			type remoteListRow struct {
				entry remoteListEntry
				raw   map[string]any
			}
			table := make([]remoteListRow, 0, len(items))

			for _, raw := range items {
				var e remoteListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode remote entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode remote entry: %w", err)
				}
				stripRemoteSecrets(m)

				table = append(table, remoteListRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Id < table[j].entry.Id })

			headers := []string{"ID", "TYPE", "AUTHID", "NODES", "WEB-URL"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Id, e.Type, e.Authid, strings.Join(e.Nodes, ","), strPtrString(e.WebUrl),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRemoteShowCmd builds `pmx pdm remote show <id>` — show a single
// remote's configuration (GET /remotes/remote/{id}/config).
func newRemoteShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a single remote's configuration",
		Long: "Show every populated field of a single managed remote's configuration " +
			"(GET /remotes/remote/{id}/config). The access token is write-only and " +
			"never rendered. The API also omits options left at their built-in " +
			"defaults; pass --defaults to also list those, with the value they " +
			"effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			resp, err := deps.PDM.Remotes.ListRemoteConfig(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get remote %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode remote %q: %w", id, err)
			}
			stripRemoteSecrets(fields)

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(remoteOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// remoteFlags collects the remote attribute flags shared by `add` and
// `update`. Every field maps directly onto a CreateRemoteParams /
// UpdateRemoteParams field of the same name.
type remoteFlags struct {
	authid string
	token  string
	nodes  []string
	webUrl string

	// add-only
	createToken string

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `add` and `update`.
func (rf *remoteFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&rf.authid, "authid", "", "authentication ID on the remote (e.g. 'root@pam' or an API token)")
	f.StringVar(&rf.token, "token", "", "the access token's secret")
	f.StringArrayVar(&rf.nodes, "node", nil, "cluster node address of the remote (repeatable)")
	f.StringVar(&rf.webUrl, "web-url", "", "configuration for the web UI URL link generation")
}

// registerAdd binds every flag `add` accepts, including the create-only
// create-token field.
func (rf *remoteFlags) registerAdd(cmd *cobra.Command) {
	rf.registerCommon(cmd)
	cmd.Flags().StringVar(&rf.createToken, "create-token", "",
		"create this API token on the remote and use it instead of the existing authentication details")
}

// registerUpdate binds every flag `update` accepts, including the
// update-only delete/digest fields.
func (rf *remoteFlags) registerUpdate(cmd *cobra.Command) {
	rf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&rf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&rf.digest, "digest", "", "only update if the current config digest matches")
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (rf *remoteFlags) applyUpdate(cmd *cobra.Command, p *pdmremotes.UpdateRemoteParams) {
	fl := cmd.Flags()
	if fl.Changed("authid") {
		p.Authid = &rf.authid
	}
	if fl.Changed("token") {
		p.Token = &rf.token
	}
	if fl.Changed("node") {
		p.Nodes = rf.nodes
	}
	if fl.Changed("web-url") {
		p.WebUrl = &rf.webUrl
	}
	if fl.Changed("delete") {
		p.Delete = rf.del
	}
	if fl.Changed("digest") {
		p.Digest = &rf.digest
	}
}

// newRemoteAddCmd builds `pmx pdm remote add <id>` — register a new remote
// (POST /remotes/remote). --type, --authid, --token, and at least one
// --node are required.
func newRemoteAddCmd() *cobra.Command {
	var (
		rf       remoteFlags
		typeFlag string
	)
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Register a new remote",
		Long: "Register a new PVE or PBS remote for this Proxmox Datacenter Manager " +
			"instance to manage (POST /remotes/remote). --type, --authid, --token, and " +
			"at least one --node are required. If --create-token is set, a new API " +
			"token is minted on the remote with that name and used instead of the " +
			"authentication details given here.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()

			if !stringInSlice(typeFlag, validRemoteTypes) {
				return fmt.Errorf("add remote %q: --type must be one of %s (got %q)",
					id, strings.Join(validRemoteTypes, ", "), typeFlag)
			}

			params := &pdmremotes.CreateRemoteParams{
				Id:     id,
				Type:   typeFlag,
				Authid: rf.authid,
				Token:  rf.token,
				Nodes:  rf.nodes,
			}
			if fl.Changed("create-token") {
				params.CreateToken = &rf.createToken
			}
			if fl.Changed("web-url") {
				params.WebUrl = &rf.webUrl
			}

			err := deps.PDM.Remotes.CreateRemote(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("add remote %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Remote %q added.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&typeFlag, "type", "", "remote type: pve|pbs (required)")
	rf.registerAdd(cmd)
	cli.MustMarkRequired(cmd, "type")
	cli.MustMarkRequired(cmd, "authid")
	cli.MustMarkRequired(cmd, "token")
	cli.MustMarkRequired(cmd, "node")
	return cmd
}

// newRemoteUpdateCmd builds `pmx pdm remote update <id>` — update an
// existing managed remote (PUT /remotes/remote/{id}). Only flags explicitly
// set are sent; use --delete to reset properties to their default instead.
// The remote's type cannot be changed after creation.
func newRemoteUpdateCmd() *cobra.Command {
	var rf remoteFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a managed remote",
		Long: "Update an existing managed remote (PUT /remotes/remote/{id}). Only " +
			"flags explicitly set are sent; use --delete to reset properties to their " +
			"default instead. The remote's type cannot be changed after creation.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update remote %q: no changes given: pass at least one flag", id)
			}

			params := &pdmremotes.UpdateRemoteParams{}
			rf.applyUpdate(cmd, params)

			err := deps.PDM.Remotes.UpdateRemote(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update remote %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Remote %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	rf.registerUpdate(cmd)
	return cmd
}

// newRemoteDeleteCmd builds `pmx pdm remote delete <id>` — remove a managed
// remote (DELETE /remotes/remote/{id}).
func newRemoteDeleteCmd() *cobra.Command {
	var (
		deleteToken bool
		yes         bool
	)
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Remove a managed remote",
		Long: "Remove a remote this instance is managing (DELETE /remotes/remote/{id}). " +
			"Pass --delete-token to also delete the API token on the remote. This is " +
			"destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete remote %q without confirmation: pass --yes/-y", id)
			}

			params := &pdmremotes.DeleteRemoteParams{}
			if cmd.Flags().Changed("delete-token") {
				params.DeleteToken = &deleteToken
			}

			err := deps.PDM.Remotes.DeleteRemote(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete remote %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Remote %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&deleteToken, "delete-token", false, "also delete the API token on the remote")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newRemoteVersionCmd builds `pmx pdm remote version <id>` — query a
// remote's Proxmox version (GET /remotes/remote/{id}/version).
func newRemoteVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version <id>",
		Short: "Query a remote's version",
		Long:  "Query the Proxmox version running on a managed remote (GET /remotes/remote/{id}/version).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			resp, err := deps.PDM.Remotes.ListRemoteVersion(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get version of remote %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode version of remote %q: %w", id, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRemoteProbeCertificateCmd builds `pmx pdm remote probe-certificate
// <id>` — re-probe a configured node's TLS certificate without using the
// pinned fingerprint, to detect rotation (POST
// /remotes/remote/{id}/probe-certificate). The endpoint carries no response
// data of its own (its API schema declares a "null" return type); it only
// reports success or the connection error encountered while probing.
func newRemoteProbeCertificateCmd() *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "probe-certificate <id>",
		Short: "Re-probe a remote node's TLS certificate",
		Long: "Re-probe a configured node's TLS certificate without using the pinned " +
			"fingerprint, to detect a rotated certificate (POST " +
			"/remotes/remote/{id}/probe-certificate). This never modifies the remote's " +
			"stored configuration; it only reports whether the probe succeeded.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			params := &pdmremotes.CreateRemoteProbeCertificateParams{Node: node}

			err := deps.PDM.Remotes.CreateRemoteProbeCertificate(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("probe certificate for remote %q node %q: %w", id, node, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Probed TLS certificate for remote %q node %q.", id, node),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "hostname of the configured node to probe (required)")
	cli.MustMarkRequired(cmd, "node")
	return cmd
}

// remoteRrdEntry is the decoded shape of one element of
// GET /remotes/remote/{id}/rrddata.
type remoteRrdEntry struct {
	Time                         int64    `json:"time"`
	MetricCollectionResponseTime *float64 `json:"metric-collection-response-time,omitempty"`
}

// newRemoteRrddataCmd builds `pmx pdm remote rrddata <id>` — read RRD
// metric-collection data points for a remote (GET
// /remotes/remote/{id}/rrddata).
func newRemoteRrddataCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrddata <id>",
		Short: "Read a remote's metric-collection RRD data",
		Long: "Read RRD (round-robin database) metric-collection data points for a " +
			"remote over the given time frame and consolidation function (GET " +
			"/remotes/remote/{id}/rrddata).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !stringInSlice(timeframe, validRemoteRrdTimeframes) {
				return fmt.Errorf("get rrddata for remote %q: --timeframe must be one of %s (got %q)",
					id, strings.Join(validRemoteRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRemoteRrdConsolidations) {
				return fmt.Errorf("get rrddata for remote %q: --cf must be one of %s (got %q)",
					id, strings.Join(validRemoteRrdConsolidations, ", "), cf)
			}

			params := &pdmremotes.ListRemoteRrddataParams{Cf: cf, Timeframe: timeframe}

			resp, err := deps.PDM.Remotes.ListRemoteRrddata(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("get rrddata for remote %q: %w", id, err)
			}

			items := rawItemsOf(resp)
			entries := make([]remoteRrdEntry, 0, len(items))

			for _, raw := range items {
				var e remoteRrdEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode rrd datapoint for remote %q: %w", id, err)
				}

				entries = append(entries, e)
			}

			headers := []string{"TIME", "METRIC-COLLECTION-RESPONSE-TIME"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					strconv.FormatInt(e.Time, 10), float64PtrString(e.MetricCollectionResponseTime),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade (required)")
	f.StringVar(&cf, "cf", "AVERAGE", "RRD consolidation function: MAX|AVERAGE")
	cli.MustMarkRequired(cmd, "timeframe")
	return cmd
}
