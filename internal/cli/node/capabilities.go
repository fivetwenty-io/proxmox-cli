package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newCapabilitiesCmd builds the `pmx pve node capabilities` sub-tree: read-only
// queries for the virtualization capabilities the resolved node can offer to
// guests.
func newCapabilitiesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Query node virtualization capabilities",
		Long: "Inspect the QEMU/KVM capabilities the resolved node can offer to guests: " +
			"the supported CPU models, machine types, and live-migration features. " +
			"All queries are read-only.",
	}
	cmd.AddCommand(newCapabilitiesQemuCmd())
	return cmd
}

func newCapabilitiesQemuCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qemu",
		Short: "Query QEMU/KVM capabilities",
		Long:  "Query the QEMU/KVM CPU models, machine types, CPU flags, and live-migration features available on the resolved node.",
	}
	cmd.AddCommand(
		newCapabilitiesQemuCpuCmd(),
		newCapabilitiesQemuCpuFlagsCmd(),
		newCapabilitiesQemuMachinesCmd(),
		newCapabilitiesQemuMigrationCmd(),
	)
	return cmd
}

func newCapabilitiesQemuCpuCmd() *cobra.Command {
	var arch string
	cmd := &cobra.Command{
		Use:   "cpu",
		Short: "List supported QEMU CPU models",
		Long: "List the QEMU CPU models the resolved node can offer to VMs. --arch " +
			"defaults to the host architecture.",
		Example: `  pmx pve node capabilities qemu cpu`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListCapabilitiesQemuCpuParams{}
			if cmd.Flags().Changed("arch") {
				params.Arch = &arch
			}
			resp, err := deps.API.Nodes.ListCapabilitiesQemuCpu(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list QEMU CPU capabilities on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&arch, "arch", "", "virtual processor architecture (defaults to the host architecture)")
	return cmd
}

func newCapabilitiesQemuCpuFlagsCmd() *cobra.Command {
	var (
		accel string
		arch  string
	)
	cmd := &cobra.Command{
		Use:   "cpu-flags",
		Short: "List supported QEMU CPU flags",
		Long: "List the QEMU CPU flags supported on the resolved node for the given " +
			"--accel and --arch (both default to the host's).",
		Example: `  pmx pve node capabilities qemu cpu-flags`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListCapabilitiesQemuCpuFlagsParams{}
			fl := cmd.Flags()
			if fl.Changed("accel") {
				params.Accel = &accel
			}
			if fl.Changed("arch") {
				params.Arch = &arch
			}
			resp, err := deps.API.Nodes.ListCapabilitiesQemuCpuFlags(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list QEMU CPU flags on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&accel, "accel", "", "acceleration type to check node compatibility for")
	cmd.Flags().StringVar(&arch, "arch", "", "virtual processor architecture (defaults to the host architecture)")
	return cmd
}

func newCapabilitiesQemuMachinesCmd() *cobra.Command {
	var arch string
	cmd := &cobra.Command{
		Use:   "machines",
		Short: "List supported QEMU machine types",
		Long: "List the QEMU machine types the resolved node can offer to VMs. --arch " +
			"defaults to the host architecture.",
		Example: `  pmx pve node capabilities qemu machines`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListCapabilitiesQemuMachinesParams{}
			if cmd.Flags().Changed("arch") {
				params.Arch = &arch
			}
			resp, err := deps.API.Nodes.ListCapabilitiesQemuMachines(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list QEMU machine capabilities on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&arch, "arch", "", "virtual processor architecture (defaults to the host architecture)")
	return cmd
}

func newCapabilitiesQemuMigrationCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "migration",
		Short:   "Show node live-migration capabilities",
		Long:    "Show the live-migration capabilities (supported transports and options) of the resolved node.",
		Example: `  pmx pve node capabilities qemu migration`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCapabilitiesQemuMigration(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get QEMU migration capabilities on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}
