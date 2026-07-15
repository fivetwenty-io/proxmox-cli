package lxc

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newToTemplateCmd builds `pmx pve lxc to-template <vmid|name>`.
//
// Converts an existing, stopped container into a template via
// POST /nodes/{node}/lxc/{vmid}/template. The operation is synchronous —
// the API returns no UPID and no response body on success.
//
// The name `to-template` is used because `template` is already taken by
// the aplinfo-download sub-group.
func newToTemplateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "to-template <vmid|name>",
		Short: "Convert a container into a template",
		Long: "Convert an existing, stopped LXC container into a template. " +
			"Templates cannot be started; clone them to create new containers. " +
			"The container must be stopped before conversion.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if err := deps.API.Nodes.CreateLxcTemplate(cmd.Context(), node, vmid); err != nil {
				return fmt.Errorf("convert container %s on node %q to template: %w", vmid, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Container %s converted to template.", vmid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
