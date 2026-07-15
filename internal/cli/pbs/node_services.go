package pbs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeServiceEntry mirrors one element of the JSON array PBS returns from
// GET /nodes/{node}/services.
type nodeServiceEntry struct {
	Service     string `json:"service"`
	Name        string `json:"name"`
	Desc        string `json:"desc"`
	State       string `json:"state"`
	ActiveState string `json:"active-state"`
}

// nodeServiceName returns e.Service, falling back to e.Name when Service is
// empty (PBS documents both keys across API versions for this field).
func nodeServiceName(e nodeServiceEntry) string {
	if e.Service != "" {
		return e.Service
	}
	return e.Name
}

// nodeServiceStateEntry is the decoded shape of GET
// /nodes/{node}/services/{service}/state, per the PBS API's documented
// ServiceState schema.
type nodeServiceStateEntry struct {
	Service     string `json:"service"`
	Name        string `json:"name"`
	Desc        string `json:"desc"`
	State       string `json:"state"`
	ActiveState string `json:"active-state"`
	UnitState   string `json:"unit-state"`
}

// newNodeServicesCmd builds `pmx pbs node services` and its
// ls/show/state/start/stop/restart/reload verbs (/nodes/{node}/services...).
func newNodeServicesCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Inspect and control node system services",
		Long: "Inspect and control the systemd services backing the node: list every service, " +
			"show a summary or the raw state of one, and start, stop, restart, or reload it.",
	}
	cmd.AddCommand(
		newNodeServicesLsCmd(nf),
		newNodeServicesShowCmd(nf),
		newNodeServicesStateCmd(nf),
		newNodeServiceActionCmd(nf, "start", "started", "Start a service on the node",
			"Start a stopped systemd service on the node."),
		newNodeServiceActionCmd(nf, "stop", "stopped", "Stop a service on the node",
			"Stop a running systemd service on the node."),
		newNodeServiceActionCmd(nf, "restart", "restarted", "Restart a service on the node",
			"Stop and start a systemd service on the node, dropping its current state."),
		newNodeServiceActionCmd(nf, "reload", "reloaded", "Reload a service on the node",
			"Ask a systemd service on the node to reload its configuration without stopping it."),
	)
	return cmd
}

// newNodeServicesLsCmd builds `pmx pbs node services ls` — list system
// services (GET /nodes/{node}/services).
func newNodeServicesLsCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List system services on the node",
		Long: "List the node's systemd services with description, state, and active-state, as " +
			"reported by `proxmox-backup-manager` service management.",
		Example: "  pmx pbs node services ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListServices(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("list services on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeServiceEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode services on node %q: %w", nf.node, err)
			}

			headers := []string{"SERVICE", "STATE", "DESC", "ACTIVE-STATE"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{nodeServiceName(e), e.State, e.Desc, e.ActiveState})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// fetchNodeServiceState retrieves a service's state via the raw transport.
//
// The generated Nodes.ListServicesState binding discards its response body
// (the PBS API schema gives this endpoint no documented return type), so
// this bypasses it via the shared raw transport to recover the actual
// service state fields.
func fetchNodeServiceState(cmd *cobra.Command, deps *cli.Deps, node, svc string) (nodeServiceStateEntry, error) {
	path := fmt.Sprintf("/nodes/%s/services/%s/state", url.PathEscape(node), url.PathEscape(svc))

	raw, err := nodeRawCall(cmd.Context(), deps, http.MethodGet, path, nil)
	if err != nil {
		return nodeServiceStateEntry{}, fmt.Errorf("get state of service %q on node %q: %w", svc, node, err)
	}

	var entry nodeServiceStateEntry

	err = json.Unmarshal(raw, &entry)
	if err != nil {
		return nodeServiceStateEntry{}, fmt.Errorf("decode state of service %q on node %q: %w", svc, node, err)
	}

	return entry, nil
}

// newNodeServicesShowCmd builds `pmx pbs node services show <svc>` — a
// friendly key/value rendering of a service's state.
func newNodeServicesShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show <svc>",
		Short: "Show a summary of a single service's state",
		Long: "Show a friendly key/value summary of a single systemd service's name, state, " +
			"description, and active-state.",
		Example: "  pmx pbs node services show proxmox-backup",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			svc := args[0]

			entry, err := fetchNodeServiceState(cmd, deps, nf.node, svc)
			if err != nil {
				return err
			}

			single := map[string]string{"service": svc}
			if name := nodeServiceStateName(entry); name != "" {
				single["service"] = name
			}
			if entry.State != "" {
				single["state"] = entry.State
			}
			if entry.Desc != "" {
				single["desc"] = entry.Desc
			}
			if entry.ActiveState != "" {
				single["active-state"] = entry.ActiveState
			}
			if entry.UnitState != "" {
				single["unit-state"] = entry.UnitState
			}

			res := output.Result{Single: single, Raw: entry}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// nodeServiceStateName returns entry.Service, falling back to entry.Name.
func nodeServiceStateName(entry nodeServiceStateEntry) string {
	if entry.Service != "" {
		return entry.Service
	}
	return entry.Name
}

// newNodeServicesStateCmd builds `pmx pbs node services state <svc>` — the
// full raw systemd state details for a single service.
func newNodeServicesStateCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "state <svc>",
		Short: "Show the raw systemd state for a single service",
		Long: "Show every field systemd reports for a single service: name, state, " +
			"description, active-state, and unit-state.",
		Example: "  pmx pbs node services state proxmox-backup",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			svc := args[0]

			entry, err := fetchNodeServiceState(cmd, deps, nf.node, svc)
			if err != nil {
				return err
			}

			fields, err := flattenToMap(entry)
			if err != nil {
				return fmt.Errorf("decode state of service %q on node %q: %w", svc, nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeServiceActionCmd builds a `pmx pbs node services <verb> <svc>`
// command (POST /nodes/{node}/services/{service}/<verb>).
//
// The generated Nodes.CreateServices{Start,Stop,Restart,Reload} bindings
// discard their response body (the PBS API schema gives these endpoints no
// documented return type), even though the equivalent PVE-side endpoints —
// confirmed async, UPID-bearing — share the same lifecycle semantics. This
// bypasses the discarding binding via the shared raw transport to recover
// the task UPID and support --async like every other PBS lifecycle command.
func newNodeServiceActionCmd(nf *nodeFlags, verb, pastTense, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:     fmt.Sprintf("%s <svc>", verb),
		Short:   short,
		Long:    long,
		Example: fmt.Sprintf("  pmx pbs node services %s proxmox-backup", verb),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			svc := args[0]

			path := fmt.Sprintf("/nodes/%s/services/%s/%s", url.PathEscape(nf.node), url.PathEscape(svc), verb)
			msg := fmt.Sprintf("Service %q on node %q %s.", svc, nf.node, pastTense)

			err := nodeFinishAsync(cmd, deps, http.MethodPost, path, nil, msg)
			if err != nil {
				return fmt.Errorf("%s service %q on node %q: %w", verb, svc, nf.node, err)
			}

			return nil
		},
	}
}
