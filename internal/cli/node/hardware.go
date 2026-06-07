package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newHardwareCmd builds the `pve node hardware` sub-tree: PCI(e) and USB device
// inventory for the resolved node, used when planning device passthrough.
func newHardwareCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hardware",
		Short: "Inspect PCI and USB hardware on a node",
		Long:  "List the PCI(e) and USB devices on the resolved node. All queries are read-only.",
	}
	cmd.AddCommand(
		newHardwarePciCmd(),
		newHardwarePciMdevCmd(),
		newHardwareUsbCmd(),
	)
	return cmd
}

// ---- pci -------------------------------------------------------------------

func newHardwarePciCmd() *cobra.Command {
	var (
		verbose        bool
		classBlacklist string
	)
	cmd := &cobra.Command{
		Use:   "pci [id-or-mapping]",
		Short: "List PCI devices, or show one device's capabilities",
		Long: "Without an argument, list the PCI(e) devices on the resolved node. With a " +
			"PCI ID or mapping name, show that device's detailed capabilities.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if len(args) == 1 {
				resp, err := deps.API.Nodes.GetHardwarePci(cmd.Context(), deps.Node, args[0])
				if err != nil {
					return fmt.Errorf("show PCI device %q on node %q: %w", args[0], deps.Node, err)
				}
				return renderScan(cmd, deps, derefRaws(resp), resp)
			}
			params := &nodes.ListHardwarePciParams{}
			fl := cmd.Flags()
			if fl.Changed("verbose") {
				params.Verbose = &verbose
			}
			if fl.Changed("class-blacklist") {
				params.PciClassBlacklist = &classBlacklist
			}
			resp, err := deps.API.Nodes.ListHardwarePci(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list PCI devices on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&verbose, "verbose", false, "include vendor and device names, not just IDs")
	f.StringVar(&classBlacklist, "class-blacklist", "",
		"comma-separated PCI classes to exclude (memory, bridge, and processor are excluded by default)")
	return cmd
}

// ---- pci mdev --------------------------------------------------------------

// newHardwarePciMdevCmd builds `pve node hardware pci mdev <id>` — lists the
// available mediated device types for a given PCI device ID or mapping name.
func newHardwarePciMdevCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mdev <pci-id-or-mapping>",
		Short: "List mediated device types for a PCI device",
		Long: "List the mediated device (mdev) types supported by the specified PCI device " +
			"on the resolved node. Used when planning vGPU or other mdev passthrough.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			id := args[0]
			resp, err := deps.API.Nodes.ListHardwarePciMdev(cmd.Context(), deps.Node, id)
			if err != nil {
				return fmt.Errorf("list mdev types for PCI device %q on node %q: %w", id, deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

// ---- usb -------------------------------------------------------------------

func newHardwareUsbCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usb",
		Short: "List USB devices on the node",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListHardwareUsb(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list USB devices on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}
