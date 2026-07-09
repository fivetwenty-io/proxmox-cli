package pdm

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	pdmsubscriptions "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/subscriptions"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newSubscriptionCmd builds `pmx pdm subscription` — manage the subscription
// key pool this Proxmox Datacenter Manager instance maintains for its
// managed remotes, inspect per-node subscription status, and drive the
// adopt/auto-assign/apply-pending workflow that binds pool keys to remote
// nodes (/subscriptions).
func newSubscriptionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscription",
		Short: "Manage the subscription key pool and remote subscription status",
		Long: "Manage the subscription key pool this Proxmox Datacenter Manager instance " +
			"maintains for its managed remotes, inspect per-node subscription status, and " +
			"drive the adopt / auto-assign / apply-pending workflow that binds pool keys " +
			"to remote nodes.",
	}
	cmd.AddCommand(
		newSubscriptionKeyCmd(),
		newSubscriptionNodeStatusCmd(),
		newSubscriptionCheckCmd(),
		newSubscriptionAdoptKeyCmd(),
		newSubscriptionAdoptAllCmd(),
		newSubscriptionAutoAssignCmd(),
		newSubscriptionBulkAssignCmd(),
		newSubscriptionApplyPendingCmd(),
		newSubscriptionClearPendingCmd(),
		newSubscriptionQueueClearCmd(),
		newSubscriptionRevertPendingClearCmd(),
	)
	return cmd
}

// newSubscriptionKeyCmd builds `pmx pdm subscription key` — CRUD and
// remote-node binding for individual pool keys (/subscriptions/keys).
func newSubscriptionKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage individual subscription pool keys",
		Long:  "List, inspect, add, remove, and (un)bind individual subscription keys in the pool.",
	}
	cmd.AddCommand(
		newSubscriptionKeyLsCmd(),
		newSubscriptionKeyShowCmd(),
		newSubscriptionKeyAddCmd(),
		newSubscriptionKeyDeleteCmd(),
		newSubscriptionKeyAssignCmd(),
		newSubscriptionKeyUnassignCmd(),
	)
	return cmd
}

// subscriptionKeyEntry is the decoded shape of one element of
// GET /subscriptions/keys, and also the shape of GET /subscriptions/keys/{key}.
//
// Key is deliberately NOT treated as secret material to strip from output.
// Unlike remote.go's `token` (write-only credential material that never
// identifies a row — remotes are addressed by `id`), the subscription key IS
// the row's identity: `key ls` sorts by it and `key show <key>` /
// `key delete <key>` / the assignment endpoints all address a single pool
// entry by it (GET/DELETE /subscriptions/keys/{key}, per subscriptions_gen.go
// GetKeysResponse.Key and the Service method signatures, v3.6.0). Stripping it
// would make the list and show output useless for addressing a specific key.
type subscriptionKeyEntry struct {
	CheckTime    *int64  `json:"check-time,omitempty"`
	Key          string  `json:"key"`
	Level        *string `json:"level,omitempty"`
	NextDueDate  *string `json:"next-due-date,omitempty"`
	Node         *string `json:"node,omitempty"`
	PendingClear *bool   `json:"pending-clear,omitempty"`
	ProductName  *string `json:"product-name,omitempty"`
	ProductType  string  `json:"product-type"`
	Remote       *string `json:"remote,omitempty"`
	Source       *string `json:"source,omitempty"`
	Status       *string `json:"status,omitempty"`
}

// newSubscriptionKeyLsCmd builds `pmx pdm subscription key ls` — list every
// subscription key in the pool the caller has audit access to (GET
// /subscriptions/keys).
func newSubscriptionKeyLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List subscription pool keys",
		Long: "List every subscription key in the pool the caller has audit access to " +
			"(GET /subscriptions/keys).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Subscriptions.ListKeys(cmd.Context())
			if err != nil {
				return fmt.Errorf("list subscription keys: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[subscriptionKeyEntry](items, "subscription key")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Key < table[j].Entry.Key })

			headers := []string{
				"KEY", "PRODUCT-TYPE", "PRODUCT-NAME", "STATUS", "LEVEL", "REMOTE",
				"NODE", "SOURCE", "PENDING-CLEAR", "NEXT-DUE-DATE", "CHECK-TIME",
			}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Key, e.ProductType, strPtrString(e.ProductName), strPtrString(e.Status),
					strPtrString(e.Level), strPtrString(e.Remote), strPtrString(e.Node),
					strPtrString(e.Source), boolPtrString(e.PendingClear), strPtrString(e.NextDueDate),
					int64PtrString(e.CheckTime),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newSubscriptionKeyShowCmd builds `pmx pdm subscription key show <key>` —
// show a single pool key's details (GET /subscriptions/keys/{key}).
func newSubscriptionKeyShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <key>",
		Short: "Show a single subscription pool key",
		Long:  "Show details for a single subscription key in the pool (GET /subscriptions/keys/{key}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			key := args[0]

			resp, err := deps.PDM.Subscriptions.GetKeys(cmd.Context(), key)
			if err != nil {
				return fmt.Errorf("get subscription key %q: %w", key, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode subscription key %q: %w", key, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newSubscriptionKeyAddCmd builds `pmx pdm subscription key add` — add one
// or more subscription keys to the pool (POST /subscriptions/keys).
func newSubscriptionKeyAddCmd() *cobra.Command {
	var (
		keys   []string
		digest string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add subscription keys to the pool",
		Long: "Add one or more subscription keys to the pool (POST /subscriptions/keys). " +
			"Duplicate keys within the input are silently collapsed; a key already present " +
			"in the pool fails the whole call and leaves the pool untouched.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmsubscriptions.CreateKeysParams{Keys: keys}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			resp, err := deps.PDM.Subscriptions.CreateKeys(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("add subscription keys: %w", err)
			}

			res := output.Result{
				Single: map[string]string{
					"added":        strconv.FormatInt(resp.Added.Int(), 10),
					"deduplicated": strconv.FormatInt(resp.Deduplicated.Int(), 10),
				},
				Raw: resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&keys, "key", nil, "subscription key to add (repeatable, required)")
	f.StringVar(&digest, "digest", "", "only add if the current config digest matches")
	cli.MustMarkRequired(cmd, "key")
	return cmd
}

// newSubscriptionKeyDeleteCmd builds `pmx pdm subscription key delete <key>`
// — remove a key from the pool (DELETE /subscriptions/keys/{key}).
func newSubscriptionKeyDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Remove a subscription key from the pool",
		Long: "Remove a subscription key from the pool (DELETE /subscriptions/keys/{key}). " +
			"Refused if the key is currently the live active key on its bound node; run " +
			"'subscription queue-clear' first. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			key := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete subscription key %q without confirmation: pass --yes/-y", key)
			}

			params := &pdmsubscriptions.DeleteKeysParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Subscriptions.DeleteKeys(cmd.Context(), key, params)
			if err != nil {
				return fmt.Errorf("delete subscription key %q: %w", key, err)
			}

			res := output.Result{Message: fmt.Sprintf("Subscription key %q deleted.", key)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newSubscriptionKeyAssignCmd builds `pmx pdm subscription key assign <key>`
// — bind a pool key to a remote node (POST /subscriptions/keys/{key}/assignment).
func newSubscriptionKeyAssignCmd() *cobra.Command {
	var (
		remote string
		node   string
		digest string
	)
	cmd := &cobra.Command{
		Use:   "assign <key>",
		Short: "Bind a pool key to a remote node",
		Long: "Bind a subscription pool key to a remote node (POST " +
			"/subscriptions/keys/{key}/assignment).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			key := args[0]

			params := &pdmsubscriptions.CreateKeysAssignmentParams{Remote: remote, Node: node}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Subscriptions.CreateKeysAssignment(cmd.Context(), key, params)
			if err != nil {
				return fmt.Errorf("assign subscription key %q: %w", key, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Subscription key %q assigned to remote %q node %q.", key, remote, node),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&remote, "remote", "", "remote ID to bind the key to (required)")
	f.StringVar(&node, "node", "", "node within the remote to bind the key to (required)")
	f.StringVar(&digest, "digest", "", "only assign if the current config digest matches")
	cli.MustMarkRequired(cmd, "remote")
	cli.MustMarkRequired(cmd, "node")
	return cmd
}

// newSubscriptionKeyUnassignCmd builds `pmx pdm subscription key unassign
// <key>` — drop a pool key's remote-node binding (DELETE
// /subscriptions/keys/{key}/assignment).
func newSubscriptionKeyUnassignCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "unassign <key>",
		Short: "Drop the remote-node binding for a pool key",
		Long: "Drop the remote-node binding for a subscription pool key (DELETE " +
			"/subscriptions/keys/{key}/assignment). Refused when the binding is currently " +
			"synced (the assigned key is the live active key on its remote); run " +
			"'subscription queue-clear' first. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			key := args[0]

			if !yes {
				return fmt.Errorf("refusing to unassign subscription key %q without confirmation: pass --yes/-y", key)
			}

			params := &pdmsubscriptions.DeleteKeysAssignmentParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Subscriptions.DeleteKeysAssignment(cmd.Context(), key, params)
			if err != nil {
				return fmt.Errorf("unassign subscription key %q: %w", key, err)
			}

			res := output.Result{Message: fmt.Sprintf("Subscription key %q unassigned.", key)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only unassign if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// subscriptionNodeStatusEntry is the decoded shape of one element of
// GET /subscriptions/node-status.
type subscriptionNodeStatusEntry struct {
	AssignedKey  *string `json:"assigned-key,omitempty"`
	CheckTime    *int64  `json:"check-time,omitempty"`
	CurrentKey   *string `json:"current-key,omitempty"`
	Level        string  `json:"level"`
	NextDueDate  *string `json:"next-due-date,omitempty"`
	Node         string  `json:"node"`
	PendingClear bool    `json:"pending-clear"`
	Remote       string  `json:"remote"`
	Sockets      *int64  `json:"sockets,omitempty"`
	Status       string  `json:"status"`
	Type         string  `json:"type"`
}

// newSubscriptionNodeStatusCmd builds `pmx pdm subscription node-status` —
// show the subscription status of every remote node the caller can audit,
// combined with key pool assignment information (GET /subscriptions/node-status).
func newSubscriptionNodeStatusCmd() *cobra.Command {
	var maxAge int64
	cmd := &cobra.Command{
		Use:   "node-status",
		Short: "Show subscription status for every auditable remote node",
		Long: "Show the subscription status of every remote node the caller can audit, " +
			"combined with key pool assignment information (GET /subscriptions/node-status).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmsubscriptions.ListNodeStatusParams{}
			if cmd.Flags().Changed("max-age") {
				params.MaxAge = int64Ptr(maxAge)
			}

			resp, err := deps.PDM.Subscriptions.ListNodeStatus(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list subscription node status: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[subscriptionNodeStatusEntry](items, "subscription node status")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].Entry.Remote != table[j].Entry.Remote {
					return table[i].Entry.Remote < table[j].Entry.Remote
				}
				return table[i].Entry.Node < table[j].Entry.Node
			})

			headers := []string{
				"REMOTE", "NODE", "TYPE", "STATUS", "LEVEL", "ASSIGNED-KEY",
				"CURRENT-KEY", "PENDING-CLEAR", "SOCKETS", "NEXT-DUE-DATE", "CHECK-TIME",
			}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Remote, e.Node, e.Type, e.Status, e.Level,
					strPtrString(e.AssignedKey), strPtrString(e.CurrentKey),
					strconv.FormatBool(e.PendingClear), int64PtrString(e.Sockets),
					strPtrString(e.NextDueDate), int64PtrString(e.CheckTime),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().Int64Var(&maxAge, "max-age", 0,
		"override the cache freshness window in seconds (0 forces a fresh query)")
	return cmd
}

// newSubscriptionCheckCmd builds `pmx pdm subscription check` — trigger a
// fresh shop-side subscription check on a remote node (POST /subscriptions/check).
func newSubscriptionCheckCmd() *cobra.Command {
	var remote, node string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Trigger a fresh subscription check on a remote node",
		Long: "Trigger a fresh shop-side subscription check on a remote node (POST " +
			"/subscriptions/check). Mirrors the per-product \"Check\" button on PVE/PBS.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmsubscriptions.CreateCheckParams{Remote: remote, Node: node}

			err := deps.PDM.Subscriptions.CreateCheck(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("check subscription for remote %q node %q: %w", remote, node, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Subscription check triggered for remote %q node %q.", remote, node),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&remote, "remote", "", "remote ID to check (required)")
	f.StringVar(&node, "node", "", "node within the remote to check (required)")
	cli.MustMarkRequired(cmd, "remote")
	cli.MustMarkRequired(cmd, "node")
	return cmd
}

// newSubscriptionAdoptKeyCmd builds `pmx pdm subscription adopt-key` —
// adopt the live subscription on a remote node into the pool without
// touching the remote (POST /subscriptions/adopt-key).
func newSubscriptionAdoptKeyCmd() *cobra.Command {
	var remote, node, digest string
	cmd := &cobra.Command{
		Use:   "adopt-key",
		Short: "Adopt the live subscription on a remote node into the pool",
		Long: "Adopt the live subscription on a remote node into the pool without " +
			"touching the remote (no DELETE / push) (POST /subscriptions/adopt-key). " +
			"Refused if a pool entry is already bound to the remote/node.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmsubscriptions.CreateAdoptKeyParams{Remote: remote, Node: node}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Subscriptions.CreateAdoptKey(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("adopt subscription key for remote %q node %q: %w", remote, node, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Subscription key on remote %q node %q adopted into the pool.", remote, node),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&remote, "remote", "", "remote ID to adopt from (required)")
	f.StringVar(&node, "node", "", "node within the remote to adopt from (required)")
	f.StringVar(&digest, "digest", "", "only adopt if the current config digest matches")
	cli.MustMarkRequired(cmd, "remote")
	cli.MustMarkRequired(cmd, "node")
	return cmd
}

// subscriptionAdoptedEntry is the decoded shape of one element of
// POST /subscriptions/adopt-all's response: a (remote, node, key) tuple that
// was imported into the pool by the bulk adoption.
type subscriptionAdoptedEntry struct {
	Key    string `json:"key"`
	Node   string `json:"node"`
	Remote string `json:"remote"`
}

// newSubscriptionAdoptAllCmd builds `pmx pdm subscription adopt-all` —
// adopt every foreign live subscription in one transaction (POST /subscriptions/adopt-all).
func newSubscriptionAdoptAllCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "adopt-all",
		Short: "Adopt every foreign live subscription into the pool",
		Long: "Adopt every foreign live subscription in one bulk transaction (POST " +
			"/subscriptions/adopt-all), importing every unbound live key on a remote the " +
			"caller may manage. This is a bulk transaction: pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !yes {
				return fmt.Errorf("refusing to adopt every foreign live subscription without confirmation: pass --yes/-y")
			}

			params := &pdmsubscriptions.CreateAdoptAllParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			resp, err := deps.PDM.Subscriptions.CreateAdoptAll(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("adopt all subscriptions: %w", err)
			}

			items := rawItemsOf(resp)
			type adoptedRow struct {
				entry subscriptionAdoptedEntry
				raw   json.RawMessage
			}
			table := make([]adoptedRow, 0, len(items))

			for _, raw := range items {
				var e subscriptionAdoptedEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode adopted subscription entry: %w", err)
				}

				table = append(table, adoptedRow{entry: e, raw: raw})
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].entry.Remote != table[j].entry.Remote {
					return table[i].entry.Remote < table[j].entry.Remote
				}
				return table[i].entry.Node < table[j].entry.Node
			})

			headers := []string{"REMOTE", "NODE", "KEY"}
			rows := make([][]string, 0, len(table))
			sortedRaw := make([]json.RawMessage, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.entry.Remote, t.entry.Node, t.entry.Key})
				sortedRaw = append(sortedRaw, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(sortedRaw)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only adopt if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the bulk operation without prompting")
	return cmd
}

// subscriptionAssignmentEntry is the decoded shape of one proposed or
// persisted key-to-node assignment, shared by the auto-assign response's
// "assignments" field and the bulk-assign response's item list — both
// declare the same object schema per pdm-apidoc.json (v3.6.0).
type subscriptionAssignmentEntry struct {
	Key         string `json:"key"`
	KeySockets  *int64 `json:"key-sockets,omitempty"`
	Node        string `json:"node"`
	NodeSockets *int64 `json:"node-sockets,omitempty"`
	Remote      string `json:"remote"`
}

// newSubscriptionAutoAssignCmd builds `pmx pdm subscription auto-assign` —
// compute a proposed mapping of unused pool keys to nodes without an active
// subscription (POST /subscriptions/auto-assign). This is a read-only
// preview; the Raw/JSON/YAML output carries the full proposal (including its
// keys-digest and node-status-digest snapshots) to feed into 'bulk-assign'.
func newSubscriptionAutoAssignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto-assign",
		Short: "Compute a proposed key-to-node assignment plan",
		Long: "Compute a proposed mapping of unused pool keys to nodes without an active " +
			"subscription (POST /subscriptions/auto-assign). This is a read-only preview: " +
			"apply the plan with 'subscription bulk-assign --proposal <json>', using the " +
			"full JSON/YAML output of this command as the proposal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Subscriptions.CreateAutoAssign(cmd.Context())
			if err != nil {
				return fmt.Errorf("compute subscription auto-assign proposal: %w", err)
			}

			entries := make([]subscriptionAssignmentEntry, 0, len(resp.Assignments))
			for _, raw := range resp.Assignments {
				var e subscriptionAssignmentEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode auto-assign proposal entry: %w", err)
				}

				entries = append(entries, e)
			}

			headers := []string{"KEY", "REMOTE", "NODE", "KEY-SOCKETS", "NODE-SOCKETS"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Key, e.Remote, e.Node, int64PtrString(e.KeySockets), int64PtrString(e.NodeSockets),
				})
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("encode subscription auto-assign proposal: %w", err)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newSubscriptionBulkAssignCmd builds `pmx pdm subscription bulk-assign` —
// apply a proposal previously returned by 'subscription auto-assign' (POST
// /subscriptions/bulk-assign).
func newSubscriptionBulkAssignCmd() *cobra.Command {
	var (
		proposal string
		yes      bool
	)
	cmd := &cobra.Command{
		Use:   "bulk-assign",
		Short: "Apply a proposal previously returned by auto-assign",
		Long: "Apply a key-to-node assignment proposal previously returned by " +
			"'subscription auto-assign' (POST /subscriptions/bulk-assign). Pass the full " +
			"proposal JSON via --proposal, or '-' to read it from stdin. The server rejects " +
			"the call if the pool or node-status digests embedded in the proposal no " +
			"longer match the live state. This applies the assignments: pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !yes {
				return fmt.Errorf("refusing to bulk-assign subscriptions without confirmation: pass --yes/-y")
			}

			proposalJSON := proposal
			if proposalJSON == "-" {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("bulk-assign subscriptions: read proposal from stdin: %w", err)
				}
				proposalJSON = string(data)
			}

			if !json.Valid([]byte(proposalJSON)) {
				return fmt.Errorf("bulk-assign subscriptions: --proposal is not valid JSON")
			}

			params := &pdmsubscriptions.CreateBulkAssignParams{Proposal: json.RawMessage(proposalJSON)}

			resp, err := deps.PDM.Subscriptions.CreateBulkAssign(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("bulk-assign subscriptions: %w", err)
			}

			items := rawItemsOf(resp)
			type assignmentRow struct {
				entry subscriptionAssignmentEntry
				raw   json.RawMessage
			}
			table := make([]assignmentRow, 0, len(items))

			for _, item := range items {
				var e subscriptionAssignmentEntry

				err := json.Unmarshal(item, &e)
				if err != nil {
					return fmt.Errorf("decode bulk-assign result entry: %w", err)
				}

				table = append(table, assignmentRow{entry: e, raw: item})
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].entry.Remote != table[j].entry.Remote {
					return table[i].entry.Remote < table[j].entry.Remote
				}
				return table[i].entry.Node < table[j].entry.Node
			})

			headers := []string{"KEY", "REMOTE", "NODE", "KEY-SOCKETS", "NODE-SOCKETS"}
			rows := make([][]string, 0, len(table))
			sortedRaw := make([]json.RawMessage, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Key, e.Remote, e.Node, int64PtrString(e.KeySockets), int64PtrString(e.NodeSockets),
				})
				sortedRaw = append(sortedRaw, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(sortedRaw)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&proposal, "proposal", "", "proposal JSON from 'subscription auto-assign', or '-' for stdin (required)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the bulk operation without prompting")
	cli.MustMarkRequired(cmd, "proposal")
	return cmd
}

// newSubscriptionApplyPendingCmd builds `pmx pdm subscription apply-pending`
// — apply every pending pool change to its remote node (POST
// /subscriptions/apply-pending).
func newSubscriptionApplyPendingCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "apply-pending",
		Short: "Apply every pending pool change to its remote node",
		Long: "Apply every pending subscription pool change to its remote node (POST " +
			"/subscriptions/apply-pending). This starts an asynchronous worker task when " +
			"there is something to apply: by default the command blocks until it " +
			"completes; pass --async (persistent flag) to return the UPID immediately " +
			"instead. This pushes changes to remotes: pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !yes {
				return fmt.Errorf("refusing to apply pending subscription changes without confirmation: pass --yes/-y")
			}

			params := &pdmsubscriptions.CreateApplyPendingParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			resp, err := deps.PDM.Subscriptions.CreateApplyPending(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("apply pending subscription changes: %w", err)
			}

			// The endpoint returns a UPID when a worker task was started, or null
			// when nothing was pending (subscriptions_gen.go CreateApplyPendingResponse
			// is an optional UPID string per pdm-apidoc.json's "returns" schema).
			if resp == nil || len(*resp) == 0 || string(*resp) == "null" {
				res := output.Result{Message: "No pending subscription changes to apply."}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			return finishAsync(cmd, deps, json.RawMessage(*resp), "Pending subscription changes applied.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only apply if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the operation without prompting")
	return cmd
}

// newSubscriptionClearPendingCmd builds `pmx pdm subscription clear-pending`
// — drop every queued pending change in one bulk transaction (POST
// /subscriptions/clear-pending).
func newSubscriptionClearPendingCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "clear-pending",
		Short: "Drop every queued pending subscription change",
		Long: "Drop every queued pending subscription pool change in one bulk " +
			"transaction (POST /subscriptions/clear-pending), without touching the " +
			"remotes. This drops queued changes: pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !yes {
				return fmt.Errorf("refusing to clear pending subscription changes without confirmation: pass --yes/-y")
			}

			params := &pdmsubscriptions.CreateClearPendingParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			resp, err := deps.PDM.Subscriptions.CreateClearPending(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("clear pending subscription changes: %w", err)
			}

			res := output.Result{
				Single: map[string]string{"cleared": strconv.FormatInt(resp.Cleared.Int(), 10)},
				Raw:    resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only clear if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the operation without prompting")
	return cmd
}

// newSubscriptionQueueClearCmd builds `pmx pdm subscription queue-clear` —
// mark a remote node's subscription for removal so its pool key can be
// reassigned elsewhere (POST /subscriptions/queue-clear).
func newSubscriptionQueueClearCmd() *cobra.Command {
	var (
		remote, node, digest string
		yes                  bool
	)
	cmd := &cobra.Command{
		Use:   "queue-clear",
		Short: "Queue a clear on a remote node",
		Long: "Queue a clear on a remote node, marking its subscription for removal so " +
			"the pool key bound to it can be reassigned elsewhere (POST " +
			"/subscriptions/queue-clear). Refused if no pool entry is bound to the " +
			"remote/node. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !yes {
				return fmt.Errorf(
					"refusing to queue a clear for remote %q node %q without confirmation: pass --yes/-y", remote, node)
			}

			params := &pdmsubscriptions.CreateQueueClearParams{Remote: remote, Node: node}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Subscriptions.CreateQueueClear(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("queue clear for remote %q node %q: %w", remote, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Clear queued for remote %q node %q.", remote, node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&remote, "remote", "", "remote ID (required)")
	f.StringVar(&node, "node", "", "node within the remote (required)")
	f.StringVar(&digest, "digest", "", "only queue if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	cli.MustMarkRequired(cmd, "remote")
	cli.MustMarkRequired(cmd, "node")
	return cmd
}

// newSubscriptionRevertPendingClearCmd builds `pmx pdm subscription
// revert-pending-clear` — drop a queued Clear Key on a remote node while
// keeping the binding intact (POST /subscriptions/revert-pending-clear).
func newSubscriptionRevertPendingClearCmd() *cobra.Command {
	var remote, node, digest string
	cmd := &cobra.Command{
		Use:   "revert-pending-clear",
		Short: "Drop a queued clear on a remote node",
		Long: "Drop a queued Clear Key on a remote node while keeping the pool binding " +
			"intact (POST /subscriptions/revert-pending-clear). Backs out a single " +
			"'subscription queue-clear' without discarding every other pending change.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmsubscriptions.CreateRevertPendingClearParams{Remote: remote, Node: node}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Subscriptions.CreateRevertPendingClear(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("revert pending clear for remote %q node %q: %w", remote, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Pending clear reverted for remote %q node %q.", remote, node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&remote, "remote", "", "remote ID (required)")
	f.StringVar(&node, "node", "", "node within the remote (required)")
	f.StringVar(&digest, "digest", "", "only revert if the current config digest matches")
	cli.MustMarkRequired(cmd, "remote")
	cli.MustMarkRequired(cmd, "node")
	return cmd
}
