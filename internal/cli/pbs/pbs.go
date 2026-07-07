package pbs

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// Group builds the `pmx pbs` command and all of its sub-commands.
// The supplied *cli.Deps is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained per-invocation via cli.GetDeps.
//
// The product annotation on this group makes the root PersistentPreRunE
// construct a PBS client (Deps.PBS) instead of a PVE client, and requires the
// selected context to have `product: pbs`.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pbs",
		Short: "Manage a Proxmox Backup Server",
		Long: "Manage a Proxmox Backup Server (PBS): datastores, backup snapshots and " +
			"groups, and prune, garbage-collection, and verification jobs. " +
			"These commands require a context with product: pbs " +
			"(create one with 'pmx context add <name> --product pbs ...').",
		Annotations: map[string]string{cli.ProductAnnotation: config.ProductPBS},
	}

	cmd.AddCommand(
		newDatastoreCmd(),
		newSnapshotCmd(),
		newGroupCmd(),
		newPruneCmd(),
		newGcCmd(),
		newVerifyCmd(),
		newSyncCmd(),
		newRemoteCmd(),
		newTrafficCmd(),
		newNodeCmd(),
		newUserCmd(),
		newACLCmd(),
		newRoleCmd(),
		newPermissionCmd(),
		newRealmCmd(),
		newMetricsCmd(),
		newNotificationCmd(),
		newAcmeCmd(),
		newTapeCmd(),
		newEncryptionKeyCmd(),
		newStatusCmd(),
		newVersionCmd(),
		newPingCmd(),
		newAPIRawCmd(),
	)

	return cmd
}

// finishAsync renders the outcome of an asynchronous PBS task. When deps.Async
// is set it prints the UPID immediately; otherwise it blocks until the task
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

	waitErr := apiclient.WaitPBSTask(cmd.Context(), deps.PBS, upid, nil)
	if waitErr != nil {
		return waitErr
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool { return &v }

// strPtr returns a pointer to v.
func strPtr(v string) *string { return &v }

// int64Ptr returns a pointer to v.
func int64Ptr(v int64) *int64 { return &v }
