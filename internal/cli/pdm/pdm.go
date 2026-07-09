package pdm

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// Group builds the `pmx pdm` command and all of its sub-commands.
// The supplied *cli.Deps is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained per-invocation via cli.GetDeps.
//
// The product annotation on this group makes the root PersistentPreRunE
// construct a PDM client (Deps.PDM) instead of a PVE client, and requires the
// selected context to have `product: pdm`.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pdm",
		Short: "Manage a Proxmox Datacenter Manager",
		Long: "Manage a Proxmox Datacenter Manager (PDM): remotes, aggregated " +
			"resources, access control, subscriptions, and per-remote PVE/PBS " +
			"operations. Requires a context with product: pdm " +
			"(create one with 'pmx context add <name> --product pdm ...').",
		Annotations: map[string]string{cli.ProductAnnotation: config.ProductPDM},
	}

	for _, f := range ChildFactories() {
		cmd.AddCommand(f(nil))
	}

	return cmd
}

// ChildFactories returns the PDM subtree's resource commands as GroupFactory
// values so they can be hoisted directly onto the root command when the binary
// is invoked as `pdm`. The shared version/api/ping commands are intentionally
// excluded — they live at the root as product:context commands.
func ChildFactories() []cli.GroupFactory {
	return []cli.GroupFactory{
		wrap(newRemoteCmd),
		wrap(newResourceCmd),
		wrap(newSdnCmd),
		wrap(newCephCmd),
		wrap(newSubscriptionCmd),
		wrap(newUserCmd),
		wrap(newTokenCmd),
		wrap(newACLCmd),
		wrap(newRoleCmd),
		wrap(newPermissionCmd),
		wrap(newTfaCmd),
	}
}

// wrap adapts a zero-arg command constructor to a cli.GroupFactory.
func wrap(ctor func() *cobra.Command) cli.GroupFactory {
	return func(_ *cli.Deps) *cobra.Command { return ctor() }
}

// finishAsync renders the outcome of an asynchronous PDM task. When deps.Async
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

	waitErr := apiclient.WaitPDMTask(cmd.Context(), deps.PDM, upid, nil)
	if waitErr != nil {
		return waitErr
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}
