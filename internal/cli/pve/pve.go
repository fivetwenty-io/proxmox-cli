package pve

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/access"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/cluster"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/lxc"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/node"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/pool"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/qemu"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/sdn"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/storage"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/task"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// ChildFactories returns the Proxmox VE resource command groups. They are used
// both to populate the `pve` command (pmx binary) and to be hoisted directly
// onto the root command when the binary is invoked as `pve`.
func ChildFactories() []cli.GroupFactory {
	return []cli.GroupFactory{
		cluster.Group, qemu.Group, lxc.Group, node.Group,
		storage.Group, sdn.Group, pool.Group, access.Group, task.Group,
	}
}

// Group builds the `pve` command wrapping every PVE resource group. The
// product:pve annotation makes the root PersistentPreRunE build a PVE client
// and reject a non-PVE context for any command in this subtree.
func Group(deps *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "pve",
		Short:       "Manage a Proxmox VE cluster",
		Long:        "Manage a Proxmox VE cluster: nodes, guests (QEMU/LXC), storage, SDN, pools, access control, and tasks. Invoke the binary as `pve` (symlink) to use these commands without the `pve` prefix.",
		Annotations: map[string]string{cli.ProductAnnotation: config.ProductPVE},
	}
	for _, f := range ChildFactories() {
		cmd.AddCommand(f(deps))
	}
	return cmd
}
