package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// agentCall invokes one no-parameter QEMU guest-agent endpoint. All such
// endpoints return a json.RawMessage alias, so a single signature covers the
// whole set and the methods can be used directly as map values.
type agentCall func(ctx context.Context, node, vmid string) (*json.RawMessage, error)

// agentQueryCommands are the read-only guest-agent verbs; agentMutateCommands
// change guest state. Both are kept sorted for stable help and error text. The
// parameterised endpoints (exec, exec-status, file-read, file-write,
// set-user-password) are intentionally omitted: they require structured
// arguments and, in the password case, a secret value the CLI must never echo
// or log. They can be added later as dedicated sub-commands.
var (
	agentQueryCommands = []string{
		"fsfreeze-status", "get-fsinfo", "get-host-name", "get-memory-block-info",
		"get-memory-blocks", "get-osinfo", "get-time", "get-timezone", "get-users",
		"get-vcpus", "info", "network-get-interfaces", "ping",
	}
	agentMutateCommands = []string{
		"fsfreeze-freeze", "fsfreeze-thaw", "fstrim", "shutdown",
		"suspend-disk", "suspend-hybrid", "suspend-ram",
	}
)

// buildAgentCalls maps each guest-agent command name to its API method. It is
// built per invocation because the method values are bound to a live nodes
// service (deps.API.Nodes); calling it with a nil service would panic.
func buildAgentCalls(n agentNodes) map[string]agentCall {
	return map[string]agentCall{
		"get-fsinfo":             n.ListQemuAgentGetFsinfo,
		"get-host-name":          n.ListQemuAgentGetHostName,
		"get-memory-block-info":  n.ListQemuAgentGetMemoryBlockInfo,
		"get-memory-blocks":      n.ListQemuAgentGetMemoryBlocks,
		"get-osinfo":             n.ListQemuAgentGetOsinfo,
		"get-time":               n.ListQemuAgentGetTime,
		"get-timezone":           n.ListQemuAgentGetTimezone,
		"get-users":              n.ListQemuAgentGetUsers,
		"get-vcpus":              n.ListQemuAgentGetVcpus,
		"info":                   n.ListQemuAgentInfo,
		"network-get-interfaces": n.ListQemuAgentNetworkGetInterfaces,
		"ping":                   n.CreateQemuAgentPing,
		"fsfreeze-status":        n.CreateQemuAgentFsfreezeStatus,
		"fsfreeze-freeze":        n.CreateQemuAgentFsfreezeFreeze,
		"fsfreeze-thaw":          n.CreateQemuAgentFsfreezeThaw,
		"fstrim":                 n.CreateQemuAgentFstrim,
		"shutdown":               n.CreateQemuAgentShutdown,
		"suspend-disk":           n.CreateQemuAgentSuspendDisk,
		"suspend-hybrid":         n.CreateQemuAgentSuspendHybrid,
		"suspend-ram":            n.CreateQemuAgentSuspendRam,
	}
}

// agentNodes is the subset of the nodes service the agent command depends on. It
// is satisfied by the live deps.API.Nodes service.
type agentNodes interface {
	ListQemuAgentGetFsinfo(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetHostName(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetMemoryBlockInfo(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetMemoryBlocks(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetOsinfo(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetTime(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetTimezone(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetUsers(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentGetVcpus(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentInfo(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	ListQemuAgentNetworkGetInterfaces(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentPing(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentFsfreezeFreeze(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentFsfreezeStatus(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentFsfreezeThaw(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentFstrim(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentShutdown(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentSuspendDisk(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentSuspendHybrid(ctx context.Context, node, vmid string) (*json.RawMessage, error)
	CreateQemuAgentSuspendRam(ctx context.Context, node, vmid string) (*json.RawMessage, error)
}

// newAgentCmd builds `pmx pve qemu agent <vmid> <command>`, a dispatcher over the
// QEMU guest-agent endpoints. The command is named positionally (mirroring
// `qm agent`) so that the full set of guest-agent verbs is reachable without a
// sub-command per verb. All verbs require the guest agent to be installed and
// running inside the VM.
//
// Parameterised endpoints (exec, exec-status, file-read, file-write,
// set-user-password) are wired as dedicated sub-commands so their structured
// arguments and, in the password case, the secret value are handled cleanly.
func newAgentCmd() *cobra.Command {
	long := "Run a QEMU guest-agent command against a VM. The guest agent must be\n" +
		"installed and running inside the VM.\n\n" +
		"Read-only queries:\n  " + strings.Join(agentQueryCommands, ", ") + "\n\n" +
		"Operations that change guest state:\n  " + strings.Join(agentMutateCommands, ", ") + "\n\n" +
		"Parameterised sub-commands (require additional flags):\n" +
		"  exec, exec-status, file-read, file-write, set-user-password"

	cmd := &cobra.Command{
		Use:       "agent <vmid|name> <command>",
		Short:     "Run a QEMU guest-agent command against a VM",
		Long:      long,
		Args:      cobra.ExactArgs(2),
		ValidArgs: append(append([]string{}, agentQueryCommands...), agentMutateCommands...),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			command := args[1]

			calls := buildAgentCalls(deps.API.Nodes)
			call, ok := calls[command]
			if !ok {
				names := make([]string, 0, len(calls))
				for k := range calls {
					names = append(names, k)
				}
				sort.Strings(names)
				return fmt.Errorf("unknown agent command %q: must be one of %s",
					command, strings.Join(names, ", "))
			}

			resp, err := call(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("agent %s for VM %s on node %q: %w", command, vmid, node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = *resp
			}
			return renderAgentResult(cmd, deps, raw,
				fmt.Sprintf("Guest agent on VM %s acknowledged %q.", vmid, command))
		},
	}

	// Parameterised agent endpoints wired as dedicated sub-commands.
	cmd.AddCommand(
		newAgentExecCmd(),
		newAgentExecStatusCmd(),
		newAgentFileReadCmd(),
		newAgentFileWriteCmd(),
		newAgentSetUserPasswordCmd(),
	)
	return cmd
}

// renderAgentResult renders a guest-agent response. Agent payloads are
// open-ended JSON: most queries return an object (often wrapping the useful
// data under a "result" key), some return arrays, and operations like ping
// return an empty body. An empty or null body renders emptyMsg; an object is
// flattened to a single record; anything else (array or scalar) is rendered
// under a synthetic "result" field with the raw value preserved.
func renderAgentResult(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, emptyMsg string) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: emptyMsg}, deps.Format)
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("decode agent response: %w", err)
	}

	if m, ok := v.(map[string]any); ok {
		single := make(map[string]string, len(m))
		for k, val := range m {
			single[k] = stringifyValue(val)
		}
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{Single: single, Raw: v}, deps.Format)
	}

	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Single: map[string]string{"result": stringifyValue(v)}, Raw: v}, deps.Format)
}
