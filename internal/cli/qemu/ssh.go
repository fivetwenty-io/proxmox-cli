package qemu

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/sshcmd"
)

// newSSHCmd builds `pmx pve qemu ssh <vmid|name> [-- <cmd>...]`, which opens an SSH
// session straight into a guest VM. The target is resolved to its VMID and node
// via the cluster inventory; the guest's IP is then discovered through the QEMU
// guest agent unless --host is supplied to bypass it.
func newSSHCmd() *cobra.Command {
	var (
		f    sshcmd.Flags
		host string
	)
	cmd := &cobra.Command{
		Use:   "ssh <vmid|name> [-- <cmd>...]",
		Short: "Open an SSH session to a VM (optionally run a remote command)",
		Long: "Open an SSH session to a QEMU VM by VMID or name. The VM's address is\n" +
			"discovered via the QEMU guest agent (first non-loopback IPv4); pass\n" +
			"--host to connect to a specific address when the agent is unavailable.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			target := args[0]
			remote := args[1:]

			vmid, node, err := resolveGuest(cmd.Context(), deps, target)
			if err != nil {
				return err
			}

			if host == "" {
				host, err = guestIP(cmd.Context(), deps, node, vmid)
				if err != nil {
					return err
				}
			}

			argv := sshcmd.BaseArgs(&f, host)
			argv = append(argv, remote...)

			if err := deps.Runner.RunInteractive("ssh", argv, nil); err != nil {
				return fmt.Errorf("ssh to VM %s (%s): %w", vmid, target, err)
			}
			return nil
		},
	}
	sshcmd.RegisterFlags(cmd, &f)
	cmd.Flags().StringVar(&host, "host", "",
		"connect to this address/hostname instead of auto-detecting via the guest agent")
	return cmd
}
