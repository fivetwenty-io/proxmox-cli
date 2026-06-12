package qemu

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newSendkeyCmd builds `pve qemu sendkey <vmid> --key KEY`.
// Injects a keypress into the VM console using QEMU monitor encoding.
// Typical use: sending Ctrl-Alt-Del or navigating BIOS/boot menus.
func newSendkeyCmd() *cobra.Command {
	var (
		key      string
		skiplock bool
	)
	cmd := &cobra.Command{
		Use:   "sendkey <vmid>",
		Short: "Send a keypress to a VM's console",
		Long: "Inject a key event into the VM console using QEMU monitor key encoding " +
			"(e.g. ctrl-alt-delete, ret, esc). Useful for navigating BIOS menus or " +
			"sending special key sequences without a graphical console.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
				return err
			}

			params := &nodes.UpdateQemuSendkeyParams{Key: key}
			if cmd.Flags().Changed("skiplock") {
				params.Skiplock = boolPtr(skiplock)
			}

			if err := deps.API.Nodes.UpdateQemuSendkey(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("sendkey %q to VM %s on node %q: %w", key, vmid, node, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Key %q sent to VM %s.", key, vmid)}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&key, "key", "", "key to send in QEMU monitor encoding (required)")
	cmd.Flags().BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
	if err := cmd.MarkFlagRequired("key"); err != nil {
		panic(fmt.Sprintf("sendkey: mark --key required: %v", err))
	}
	return cmd
}
