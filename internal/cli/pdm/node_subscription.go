package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newNodeSubscriptionCmd builds `pmx pdm node subscription` and its
// show/update verbs (/nodes/{node}/subscription). Unlike PBS's node
// subscription group (set/update/delete), PDM's /nodes/{node}/subscription
// only exposes GET and POST — the Service interface has no
// UpdateSubscription (set-key) or DeleteSubscription method at all
// (nodes_gen.go:140-145, v3.6.0; the PDM API schema declares only GET and
// POST children for this path, verified 2026-07-08) — so there is no `set`
// or `delete` sub-command.
func newNodeSubscriptionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscription",
		Short: "Show or refresh the node's subscription status",
	}
	cmd.AddCommand(newNodeSubscriptionShowCmd(), newNodeSubscriptionUpdateCmd())
	return cmd
}

// newNodeSubscriptionShowCmd builds `pmx pdm node subscription show
// <node>` — show the node's subscription info (GET
// /nodes/{node}/subscription).
func newNodeSubscriptionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <node>",
		Short: "Show the node's subscription info",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListSubscription(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get subscription for node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get subscription for node %q: empty response from server", node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode subscription for node %q: %w", node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeSubscriptionUpdateCmd builds `pmx pdm node subscription update
// <node>` — check and refresh the node's subscription status against the
// server (POST /nodes/{node}/subscription).
//
// CreateSubscription runs synchronously: its returns.type in the PDM API
// schema is "null" (pdm-apidoc.json, verified 2026-07-08) and nodes_gen.go
// emits `CreateSubscription(ctx, node string) error` — no params and no
// response type at all (nodes_gen.go:143-145,1800-1816, v3.6.0), unlike the
// PBS analog which accepts a --force flag
// (internal/cli/pbs/node_settings.go:421-448); PDM's endpoint takes no
// parameters, so there is no --force here.
func newNodeSubscriptionUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update <node>",
		Short: "Check and refresh the node's subscription status against the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			err := deps.PDM.Nodes.CreateSubscription(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("refresh subscription on node %q: %w", node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Subscription status for node %q refreshed.", node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
