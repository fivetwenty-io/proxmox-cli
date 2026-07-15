package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newServicesCmd builds the `pmx node services` sub-group.
func newServicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Inspect and control node system services",
		Long: "List and inspect a node's systemd services, and start, stop, restart, or " +
			"reload a specific service.",
	}
	cmd.AddCommand(
		newServicesListCmd(),
		newServicesGetCmd(),
		newServicesStateCmd(),
		newServiceActionCmd("start", "started", "Start a service on a node",
			"Start a systemd service on the given node. Submits a task and blocks until it "+
				"completes; pass the global --async flag to print the task UPID immediately "+
				"instead of waiting.",
			`  pmx pve node services start pve1 pveproxy`, serviceStart),
		newServiceActionCmd("stop", "stopped", "Stop a service on a node",
			"Stop a systemd service on the given node. Submits a task and blocks until it "+
				"completes; pass the global --async flag to print the task UPID immediately "+
				"instead of waiting.",
			`  pmx pve node services stop pve1 pveproxy`, serviceStop),
		newServiceActionCmd("restart", "restarted", "Restart a service on a node",
			"Restart a systemd service on the given node. Submits a task and blocks until "+
				"it completes; pass the global --async flag to print the task UPID "+
				"immediately instead of waiting.",
			`  pmx pve node services restart pve1 pveproxy`, serviceRestart),
		newServiceActionCmd("reload", "reloaded", "Reload a service on a node",
			"Reload a systemd service's configuration on the given node without a full "+
				"restart. Submits a task and blocks until it completes; pass the global "+
				"--async flag to print the task UPID immediately instead of waiting.",
			`  pmx pve node services reload pve1 pveproxy`, serviceReload),
	)
	return cmd
}

// serviceEntry is the minimal decoded shape of a node services list entry.
type serviceEntry struct {
	Service     string `json:"service"`
	Name        string `json:"name"`
	Desc        string `json:"desc"`
	State       string `json:"state"`
	ActiveState string `json:"active-state"`
}

// newServicesListCmd builds `pmx node services list <node>`.
func newServicesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list <node>",
		Short:   "List system services on a node",
		Long:    "List the systemd services known to the given node, with their state and description.",
		Example: `  pmx pve node services list pve1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.API.Nodes.ListServices(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("list services on node %q: %w", node, err)
			}

			headers := []string{"SERVICE", "STATE", "DESC", "ACTIVE-STATE"}
			entries := make([]serviceEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e serviceEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode service entry: %w", err)
					}
					entries = append(entries, e)
					name := e.Service
					if name == "" {
						name = e.Name
					}
					rows = append(rows, []string{name, e.State, e.Desc, e.ActiveState})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

// newServicesGetCmd builds `pmx node services get <node> <svc>`.
//
// GET /nodes/{node}/services/{service} is only a directory index (state,
// start, stop, ...); the service detail lives at the state child endpoint.
func newServicesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <node> <svc>",
		Short:   "Show details for a single service",
		Long:    "Show a single systemd service's name, state, description, and active-state on the given node.",
		Example: `  pmx pve node services get pve1 pveproxy`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			svc := args[1]

			resp, err := deps.API.Nodes.ListServicesState(cmd.Context(), node, svc)
			if err != nil {
				return fmt.Errorf("get service %q on node %q: %w", svc, node, err)
			}

			single := map[string]string{"SERVICE": svc}
			if resp != nil {
				name := resp.Service
				if name == "" {
					name = resp.Name
				}
				if name != "" {
					single["SERVICE"] = name
				}
				if resp.State != "" {
					single["STATE"] = resp.State
				}
				if resp.Desc != "" {
					single["DESC"] = resp.Desc
				}
				if resp.ActiveState != "" {
					single["ACTIVE-STATE"] = resp.ActiveState
				}
				if resp.UnitState != "" {
					single["UNIT-STATE"] = resp.UnitState
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}

// newServicesStateCmd builds `pmx node services state <node> <svc>` — returns
// the raw systemd state details for a single service.
func newServicesStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "state <node> <svc>",
		Short:   "Show the raw systemd state for a single service",
		Long:    "Show the raw systemd unit-state fields for a single service on the given node.",
		Example: `  pmx pve node services state pve1 pveproxy`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			svc := args[1]

			resp, err := deps.API.Nodes.ListServicesState(cmd.Context(), node, svc)
			if err != nil {
				return fmt.Errorf("get state of service %q on node %q: %w", svc, node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

// serviceAction is a function that triggers a service lifecycle operation and
// returns the raw UPID-bearing response.
type serviceAction func(ctx context.Context, ac *apiclient.APIClient, node, svc string) (json.RawMessage, error)

func serviceStart(ctx context.Context, ac *apiclient.APIClient, node, svc string) (json.RawMessage, error) {
	resp, err := ac.Nodes.CreateServicesStart(ctx, node, svc)
	if err != nil {
		return nil, err
	}
	return rawOrNil(resp), nil
}

func serviceStop(ctx context.Context, ac *apiclient.APIClient, node, svc string) (json.RawMessage, error) {
	resp, err := ac.Nodes.CreateServicesStop(ctx, node, svc)
	if err != nil {
		return nil, err
	}
	return rawOrNil(resp), nil
}

func serviceRestart(ctx context.Context, ac *apiclient.APIClient, node, svc string) (json.RawMessage, error) {
	resp, err := ac.Nodes.CreateServicesRestart(ctx, node, svc)
	if err != nil {
		return nil, err
	}
	return rawOrNil(resp), nil
}

func serviceReload(ctx context.Context, ac *apiclient.APIClient, node, svc string) (json.RawMessage, error) {
	resp, err := ac.Nodes.CreateServicesReload(ctx, node, svc)
	if err != nil {
		return nil, err
	}
	return rawOrNil(resp), nil
}

// rawOrNil normalises a *json.RawMessage response to a json.RawMessage value.
func rawOrNil(resp *json.RawMessage) json.RawMessage {
	if resp == nil {
		return nil
	}
	return *resp
}

// newServiceActionCmd builds a `pmx node services <verb> <node> <svc>` command.
// verb is the cobra verb (start/stop/restart/reload), pastTense is the message
// participle, and action performs the API call. long and example are threaded
// per-verb by the caller since the four verbs share this builder but need
// distinct docs.
func newServiceActionCmd(verb, pastTense, short, long, example string, action serviceAction) *cobra.Command {
	cmd := &cobra.Command{
		Use:     fmt.Sprintf("%s <node> <svc>", verb),
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			svc := args[1]

			raw, err := action(cmd.Context(), deps.API, node, svc)
			if err != nil {
				return fmt.Errorf("%s service %q on node %q: %w", verb, svc, node, err)
			}

			upid, err := apiclient.UPIDFromRaw(raw)
			if err != nil {
				return fmt.Errorf("%s service %q on node %q: %w", verb, svc, node, err)
			}

			if deps.Async {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{
						Single:  map[string]string{"upid": upid},
						Raw:     map[string]string{"upid": upid},
						Message: upid,
					}, deps.Format)
			}

			if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
				return fmt.Errorf("%s service %q on node %q: %w", verb, svc, node, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Service %q %s.", svc, pastTense)}, deps.Format)
		},
	}

	return cmd
}
