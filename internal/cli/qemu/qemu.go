package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
}

// resolveDeps returns the live dependencies for a command. It defaults to
// cli.GetDeps and is overridable in tests so a command tree can run against a
// fake server without driving the root PersistentPreRunE.
var resolveDeps = cli.GetDeps

// newGroupCmd builds the `pve qemu` command and all of its sub-commands.
// The *cli.Deps argument is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained inside each RunE via resolveDeps.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qemu",
		Short: "Manage QEMU virtual machines",
		Long: "List, inspect, configure, and control the lifecycle of QEMU virtual " +
			"machines on a node, including snapshots.",
	}

	cmd.AddCommand(
		newListCmd(),
		newStatusCmd(),
		newConfigCmd(),
		newCreateCmd(),
		newCloneCmd(),
		newMigrateCmd(),
		newDiskCmd(),
		newFirewallCmd(),
		newStartCmd(),
		newStopCmd(),
		newRebootCmd(),
		newShutdownCmd(),
		newResetCmd(),
		newSuspendCmd(),
		newResumeCmd(),
		newDeleteCmd(),
		newSnapshotCmd(),
	)
	return cmd
}

// resolveNode returns the node from deps (flag > env > config), erroring when no
// node could be determined for an operation that targets a specific node.
func resolveNode(deps *cli.Deps) (string, error) {
	if deps.Node == "" {
		return "", fmt.Errorf("no node specified: use --node, set PVE_NODE, or configure a default node")
	}
	return deps.Node, nil
}

// finishAsync renders the outcome of an asynchronous task. When deps.Async is
// set it prints the UPID immediately; otherwise it blocks until the task
// completes and prints msg. The raw response carries the UPID JSON string.
func finishAsync(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, msg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return err
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
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool { return &v }

// strPtr returns a pointer to v.
func strPtr(v string) *string { return &v }

// int64Ptr returns a pointer to v.
func int64Ptr(v int64) *int64 { return &v }
