package pbs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeFlags carries the --node persistent flag shared by every sub-command
// under `pmx pbs node`. Proxmox Backup Server is single-node: every
// /nodes/{node}/... path segment is populated from this value, which
// defaults to "localhost" (the name a PBS host uses for its own API) and
// rarely needs to be overridden.
type nodeFlags struct {
	node string
}

// newNodeCmd builds `pmx pbs node` and its full sub-command tree: host
// status and power control, tasks, services, APT packages, disks, network,
// certificates, DNS, time, subscription, and node configuration.
func newNodeCmd() *cobra.Command {
	nf := &nodeFlags{}

	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage this Proxmox Backup Server host",
		Long: "Inspect and manage the Proxmox Backup Server host: status, power control, " +
			"tasks, services, APT packages, disks, network interfaces, TLS certificates, " +
			"DNS, time, subscription, and node configuration. PBS is single-node, so " +
			"--node defaults to \"localhost\" and rarely needs to be changed.",
	}
	cmd.PersistentFlags().StringVar(&nf.node, "node", "localhost", "PBS node name")

	cmd.AddCommand(
		newNodeLsCmd(),
		newNodeStatusCmd(nf),
		newNodePowerCmd(nf, "reboot", "Reboot the node",
			"Reboot the node immediately. This is disruptive: pass --yes/-y to confirm.",
			"  pmx pbs node reboot --yes"),
		newNodePowerCmd(nf, "shutdown", "Shut down the node",
			"Shut down the node immediately. This is disruptive: pass --yes/-y to confirm.",
			"  pmx pbs node shutdown --yes"),
		newNodeRrdCmd(nf),
		newNodeReportCmd(nf),
		newNodeSyslogCmd(nf),
		newNodeJournalCmd(nf),
		newNodeDNSCmd(nf),
		newNodeTimeCmd(nf),
		newNodeConfigCmd(nf),
		newNodeSubscriptionCmd(nf),
		newNodeIdentityCmd(nf),
		newNodeTasksCmd(nf),
		newNodeServicesCmd(nf),
		newNodeAptCmd(nf),
		newNodeDisksCmd(nf),
		newNodeNetworkCmd(nf),
		newNodeCertificatesCmd(nf),
	)

	return cmd
}

// newNodeLsCmd builds `pmx pbs node ls` — list the node entries visible at
// the compatibility cluster-node listing (GET /nodes).
//
// The generated Nodes.ListNodes binding discards its response body: the PBS
// API schema gives this "only for compatibility" endpoint no documented
// return type, so the generator produced a method returning only error. This
// bypasses it via the shared raw transport (the same *client.Client every
// generated binding is itself built on) to recover the actual entries.
func newNodeLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List node entries visible to this PBS host",
		Long: "List the node entries returned by the compatibility cluster-node listing. " +
			"PBS is single-node, so this always returns exactly one entry.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Raw.GetRawCtx(cmd.Context(), "/nodes", nil)
			if err != nil {
				return fmt.Errorf("list nodes: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("list nodes: empty response from server")
			}

			items, err := nodeRawArrayItems(resp.Data)
			if err != nil {
				return fmt.Errorf("list nodes: %w", err)
			}

			headers := []string{"NODE"}
			rows := make([][]string, 0, len(items))
			for _, raw := range items {
				var e struct {
					Node string `json:"node"`
				}
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode node entry: %w", err)
				}
				rows = append(rows, []string{e.Node})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeStatusCmd builds `pmx pbs node status` — read node memory, CPU, and
// root-disk usage (GET /nodes/{node}/status).
func newNodeStatusCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show node memory, CPU, and root-disk usage",
		Long:  "Show node memory, CPU, load average, kernel, and root-filesystem usage.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListStatus(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get status for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get status for node %q: empty response from server", nf.node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status for node %q: %w", nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodePowerCmd builds a node power-control command (reboot or shutdown)
// that wraps POST /nodes/{node}/status with the matching command. Both
// actions are disruptive, so each is gated behind --yes/-y.
func newNodePowerCmd(nf *nodeFlags, verb, short, long, example string) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     verb,
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to %s node %q without confirmation: pass --yes/-y", verb, nf.node)
			}

			params := &pbsnodes.CreateStatusParams{Command: verb}

			err := deps.PBS.Nodes.CreateStatus(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("%s node %q: %w", verb, nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Node %q %s initiated.", nf.node, verb)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the disruptive operation without prompting")

	return cmd
}

// newNodeRrdCmd builds `pmx pbs node rrd` — read node RRD usage statistics
// (GET /nodes/{node}/rrd).
//
// The generated Nodes.ListRrd binding discards its response body for the
// same reason as Admin.ListDatastoreRrd (see newDatastoreRrdCmd's comment):
// the response shape varies with the requested time frame, so the generator
// could not model it and emits a method returning only error. This bypasses
// it via the shared raw transport to recover the actual statistics.
func newNodeRrdCmd(nf *nodeFlags) *cobra.Command {
	var (
		timeframe string
		cf        string
	)

	cmd := &cobra.Command{
		Use:   "rrd",
		Short: "Show RRD usage statistics for the node",
		Long: "Read RRD (round-robin database) usage statistics for the node over the " +
			"given time frame and consolidation function. The response shape is dynamic " +
			"(PBS does not publish a fixed schema for it), so it is rendered as raw JSON.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !stringInSlice(timeframe, validRrdTimeframes) {
				return fmt.Errorf("--timeframe must be one of %s (got %q)",
					strings.Join(validRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRrdConsolidations) {
				return fmt.Errorf("--cf must be one of %s (got %q)",
					strings.Join(validRrdConsolidations, ", "), cf)
			}

			path := fmt.Sprintf("/nodes/%s/rrd", url.PathEscape(nf.node))
			params := map[string]interface{}{"cf": cf, "timeframe": timeframe}

			resp, err := deps.PBS.Raw.GetRawCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("get rrd for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get rrd for node %q: empty response from server", nf.node)
			}

			res := output.Result{
				Message: fmt.Sprintf("RRD stats for node %q (timeframe=%s, cf=%s).", nf.node, timeframe, cf),
				Raw:     resp.Data,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade (required)")
	fl.StringVar(&cf, "cf", "AVERAGE", "RRD consolidation function: MAX|AVERAGE")
	cli.MustMarkRequired(cmd, "timeframe")

	return cmd
}

// newNodeReportCmd builds `pmx pbs node report` — generate a full system
// report for the node (GET /nodes/{node}/report), rendered as plain text.
func newNodeReportCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Generate a full system report for the node",
		Long: "Generate a diagnostic report covering system, network, and storage " +
			"configuration for the node — the same report `proxmox-backup-manager report` " +
			"produces on the host itself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListReport(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("generate report for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("generate report for node %q: empty response from server", nf.node)
			}

			text, err := nodeDecodeText(*resp)
			if err != nil {
				return fmt.Errorf("decode report for node %q: %w", nf.node, err)
			}

			res := output.Result{Message: text, Raw: text}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// --- shared node helpers -----------------------------------------------------

// nodeRawCall issues method (an http.Method* constant) against deps.PBS.Raw
// for a /nodes/{node}/... endpoint whose generated binding discards its
// response body entirely (an interface with no response type at all, or one
// typed as bare error), and returns the response payload re-marshalled as
// json.RawMessage. Every generated nodes.Service method is itself built on
// this same raw transport, so the request shape (path, auth, encoding, error
// handling) is identical either way; only the discarded-body limitation is
// bypassed. A nil resp.Data (a JSON null body) decodes to the JSON literal
// "null" rather than an error, since some endpoints legitimately return no
// data on success.
func nodeRawCall(
	ctx context.Context, deps *cli.Deps, method, path string, body map[string]interface{},
) (json.RawMessage, error) {
	var (
		resp *pve.Response
		err  error
	)

	switch method {
	case http.MethodGet:
		resp, err = deps.PBS.Raw.GetRawCtx(ctx, path, body)
	case http.MethodPost:
		resp, err = deps.PBS.Raw.PostRawCtx(ctx, path, body)
	case http.MethodPut:
		resp, err = deps.PBS.Raw.PutRawCtx(ctx, path, body)
	case http.MethodDelete:
		resp, err = deps.PBS.Raw.DeleteRawCtx(ctx, path, body)
	default:
		return nil, fmt.Errorf("nodeRawCall: unsupported method %q", method)
	}
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty response from server")
	}
	if resp.Data == nil {
		return json.RawMessage("null"), nil
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("re-marshal response data: %w", err)
	}

	return raw, nil
}

// nodeRawArrayItems converts an arbitrary decoded JSON value (typically
// *client.Response.Data from a raw-transport call) into a slice of
// json.RawMessage elements. It errors if data is non-nil and not a JSON
// array, rather than silently returning an empty list.
func nodeRawArrayItems(data any) ([]json.RawMessage, error) {
	if data == nil {
		return []json.RawMessage{}, nil
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal response data: %w", err)
	}

	var items []json.RawMessage

	err = json.Unmarshal(raw, &items)
	if err != nil {
		return nil, fmt.Errorf("unmarshal response data as array: %w", err)
	}

	return items, nil
}

// nodeDecodeArray unmarshals each element of items into T, returning a hard
// error on the first malformed element rather than silently dropping it —
// a partially-decoded list must never be mistaken for a complete one.
func nodeDecodeArray[T any](items []json.RawMessage) ([]T, error) {
	out := make([]T, 0, len(items))

	for i, raw := range items {
		var v T

		err := json.Unmarshal(raw, &v)
		if err != nil {
			return nil, fmt.Errorf("decode entry %d: %w", i, err)
		}

		out = append(out, v)
	}

	return out, nil
}

// nodeDecodeText unmarshals a json.RawMessage that carries a plain JSON
// string (the shape PBS uses for free-text endpoints such as report and apt
// changelog).
func nodeDecodeText(raw json.RawMessage) (string, error) {
	var text string

	err := json.Unmarshal(raw, &text)
	if err != nil {
		return "", fmt.Errorf("unexpected non-string response: %w", err)
	}

	return text, nil
}

// nodeFinishAsync issues method against path/body via the raw transport (for
// endpoints whose generated binding discards the response body) and renders
// the outcome through finishAsync, honouring --async.
func nodeFinishAsync(
	cmd *cobra.Command, deps *cli.Deps, method, path string, body map[string]interface{}, msg string,
) error {
	raw, err := nodeRawCall(cmd.Context(), deps, method, path, body)
	if err != nil {
		return err
	}

	return finishAsync(cmd, deps, raw, msg)
}
