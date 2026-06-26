package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newCpuCmd builds the `pve qemu cpu` sub-group.
func newCpuCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cpu",
		Short: "Inspect QEMU CPU capabilities",
	}
	cmd.AddCommand(newCpuListCmd())
	return cmd
}

// newCpuListCmd builds `pve qemu cpu list`.
// Calls nodes.ListCapabilitiesQemuCpu on the resolved node.
func newCpuListCmd() *cobra.Command {
	var arch string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available QEMU CPU models for a node",
		Long: "List the CPU models available to QEMU on the target node. " +
			"Pass --arch to filter by virtual processor architecture.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}

			params := &nodes.ListCapabilitiesQemuCpuParams{}
			if cmd.Flags().Changed("arch") {
				params.Arch = strPtr(arch)
			}

			resp, err := deps.API.Nodes.ListCapabilitiesQemuCpu(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("list QEMU CPU models on node %q: %w", node, err)
			}

			var raw []json.RawMessage
			if resp != nil {
				raw = *resp
			}
			return renderRawList(cmd, deps, raw)
		},
	}
	cmd.Flags().StringVar(&arch, "arch", "", "filter by virtual processor architecture, e.g. x86_64 or aarch64")
	return cmd
}

// newMachineCmd builds the `pve qemu machine` sub-group.
func newMachineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "machine",
		Short: "Inspect QEMU machine type capabilities",
	}
	cmd.AddCommand(newMachineListCmd())
	return cmd
}

// newMachineListCmd builds `pve qemu machine list`.
// Calls nodes.ListCapabilitiesQemuMachines on the resolved node.
func newMachineListCmd() *cobra.Command {
	var arch string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available QEMU machine types for a node",
		Long: "List the machine types available to QEMU on the target node. " +
			"Pass --arch to filter by virtual processor architecture.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}

			params := &nodes.ListCapabilitiesQemuMachinesParams{}
			if cmd.Flags().Changed("arch") {
				params.Arch = strPtr(arch)
			}

			resp, err := deps.API.Nodes.ListCapabilitiesQemuMachines(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("list QEMU machine types on node %q: %w", node, err)
			}

			var raw []json.RawMessage
			if resp != nil {
				raw = *resp
			}
			return renderRawList(cmd, deps, raw)
		},
	}
	cmd.Flags().StringVar(&arch, "arch", "", "filter by virtual processor architecture, e.g. x86_64 or aarch64")
	return cmd
}

// newCpuFlagsCmd builds `pve qemu cpu-flags`.
// Calls nodes.ListCapabilitiesQemuCpuFlags on the resolved node.
func newCpuFlagsCmd() *cobra.Command {
	var (
		arch  string
		accel string
	)
	cmd := &cobra.Command{
		Use:   "cpu-flags",
		Short: "List available QEMU CPU flags for a node",
		Long: "List the CPU flags available to QEMU on the target node. " +
			"Pass --arch to filter by architecture and --accel to filter by acceleration type.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}

			params := &nodes.ListCapabilitiesQemuCpuFlagsParams{}
			if cmd.Flags().Changed("arch") {
				params.Arch = strPtr(arch)
			}
			if cmd.Flags().Changed("accel") {
				params.Accel = strPtr(accel)
			}

			resp, err := deps.API.Nodes.ListCapabilitiesQemuCpuFlags(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("list QEMU CPU flags on node %q: %w", node, err)
			}

			var raw []json.RawMessage
			if resp != nil {
				raw = *resp
			}
			return renderRawList(cmd, deps, raw)
		},
	}
	cmd.Flags().StringVar(&arch, "arch", "", "filter by virtual processor architecture, e.g. x86_64 or aarch64")
	cmd.Flags().StringVar(&accel, "accel", "", "filter by acceleration type to check node compatibility for")
	return cmd
}

// renderRawList renders a []json.RawMessage list. Each element is marshalled to
// its compact JSON representation in a single RAW column for table output, and
// the full slice is passed as Raw for JSON/YAML output.
func renderRawList(cmd *cobra.Command, deps *cli.Deps, items []json.RawMessage) error {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{string(item)})
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{
		Headers: []string{"DATA"},
		Rows:    rows,
		Raw:     items,
	}, deps.Format)
}
