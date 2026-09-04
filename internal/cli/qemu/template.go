package qemu

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newTemplateCmd builds `pmx pve qemu template <vmid>`, converting a VM into a
// template (POST /nodes/{node}/qemu/{vmid}/template). This is irreversible: a
// template cannot be started or converted back into a regular VM, so the
// command refuses to run without --yes. With --disk only the named disk is
// converted to a base image.
//
// It also answers to `to-template`, which is what the same operation is called
// for containers (`pmx pve lxc to-template`, so named because `lxc template`
// was already the appliance-download group). One word therefore means a
// destructive verb here and a noun group there; the alias gives the conversion
// a single name across both guest types without moving anyone's cheese.
func newTemplateCmd() *cobra.Command {
	var (
		yes   bool
		async bool
		disk  string
	)
	cmd := &cobra.Command{
		Use:     "template <vmid|name>",
		Aliases: []string{"to-template"},
		Short:   "Convert a VM into a template (irreversible)",
		Long: "Convert a VM into a template.\n\n" +
			"This cannot be undone. A template can neither be started nor converted back into " +
			"a regular VM, so the command refuses to run without --yes/-y.\n\n" +
			"With --disk, only the named disk becomes a base image.\n\n" +
			"Depending on the PVE version, conversion either completes synchronously or runs " +
			"as a background task. In the latter case the command blocks until it finishes, " +
			"unless --async is passed. " + cli.WaitBoundHelp,
		Example: `  pmx pve qemu template 100 --yes
  pmx pve qemu template golden-image --yes --disk scsi0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if !yes {
				return fmt.Errorf(
					"refusing to convert VM %s into a template without confirmation: pass --yes/-y "+
						"(this is irreversible)", vmid)
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateQemuTemplateParams{}
			if cmd.Flags().Changed("disk") {
				params.Disk = new(disk)
			}

			resp, err := deps.API.Nodes.CreateQemuTemplate(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("convert VM %s on node %q into a template: %w", vmid, node, err)
			}

			// Template conversion may run as a worker task (returning a UPID) or
			// complete synchronously with an empty/null body. Block on the task
			// when a UPID is present; otherwise report success directly.
			var raw json.RawMessage
			if resp != nil {
				raw = *resp
			}
			doneMsg := fmt.Sprintf("VM %s converted into a template.", vmid)
			if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed == "null" {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: doneMsg}, deps.Format)
			}
			return finishAsync(cmd, deps, raw, doneMsg)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the irreversible conversion without prompting")
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&disk, "disk", "", "convert only this disk to a base image, e.g. scsi0")
	return cmd
}
