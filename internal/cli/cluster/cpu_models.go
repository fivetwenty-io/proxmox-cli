package cluster

import (
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newCpuModelCmd builds the `pve cluster cpu-model` sub-tree for managing
// datacenter-wide custom QEMU CPU models. A custom model lets guests use a
// tailored CPU definition: a reported model plus extra CPU flags and CPUID
// tuning.
func newCpuModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cpu-model",
		Aliases: []string{"cpu-models"},
		Short:   "Manage custom QEMU CPU models",
		Long: "List, create, inspect, update, and delete datacenter-wide custom QEMU CPU " +
			"models. A custom model pairs a reported QEMU/KVM CPU model with additional " +
			"CPU flags and CPUID tuning that guests can then select.",
	}
	cmd.AddCommand(
		newCpuModelListCmd(),
		newCpuModelGetCmd(),
		newCpuModelCreateCmd(),
		newCpuModelSetCmd(),
		newCpuModelDeleteCmd(),
	)
	return cmd
}

func newCpuModelListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List custom CPU models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListQemuCustomCpuModels(cmd.Context())
			if err != nil {
				return fmt.Errorf("list custom CPU models: %w", err)
			}
			res, err := rawUnionResult(derefRawList(resp))
			if err != nil {
				return fmt.Errorf("list custom CPU models: %w", err)
			}
			res.Raw = resp
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newCpuModelGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <cputype>",
		Short: "Show a single custom CPU model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cputype := args[0]
			resp, err := deps.API.Cluster.GetQemuCustomCpuModels(cmd.Context(), cputype)
			if err != nil {
				return fmt.Errorf("get custom CPU model %q: %w", cputype, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get custom CPU model %q: %w", cputype, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newCpuModelCreateCmd() *cobra.Command {
	var (
		reportedModel string
		flags         string
		guestPhysBits int64
		hidden        bool
		hvVendorID    string
		level         int64
		physBits      string
	)
	cmd := &cobra.Command{
		Use:   "create <cputype>",
		Short: "Create a custom CPU model",
		Long: "Create a custom CPU model in the datacenter configuration. The 'custom-' " +
			"prefix on <cputype> is optional. --reported-model is the QEMU/KVM model " +
			"reported to guests; additional CPU flags and CPUID tuning are optional.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cputype := args[0]
			params := &pvecluster.CreateQemuCustomCpuModelsParams{
				Cputype:       cputype,
				ReportedModel: reportedModel,
			}
			fl := cmd.Flags()
			if fl.Changed("flags") {
				params.Flags = &flags
			}
			if fl.Changed("guest-phys-bits") {
				params.GuestPhysBits = &guestPhysBits
			}
			if fl.Changed("hidden") {
				params.Hidden = &hidden
			}
			if fl.Changed("hv-vendor-id") {
				params.HvVendorId = &hvVendorID
			}
			if fl.Changed("level") {
				params.Level = &level
			}
			if fl.Changed("phys-bits") {
				params.PhysBits = &physBits
			}
			if err := deps.API.Cluster.CreateQemuCustomCpuModels(cmd.Context(), params); err != nil {
				return fmt.Errorf("create custom CPU model %q: %w", cputype, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Custom CPU model %s created.", cputype)}, deps.Format)
		},
	}
	registerCpuModelFlags(cmd, &reportedModel, &flags, &guestPhysBits, &hidden, &hvVendorID, &level, &physBits)
	_ = cmd.MarkFlagRequired("reported-model")
	return cmd
}

func newCpuModelSetCmd() *cobra.Command {
	var (
		reportedModel string
		flags         string
		guestPhysBits int64
		hidden        bool
		hvVendorID    string
		level         int64
		physBits      string
		del           string
		digest        string
	)
	cmd := &cobra.Command{
		Use:   "set <cputype>",
		Short: "Update a custom CPU model",
		Long: "Update a custom CPU model. Only the flags you pass are changed; use " +
			"--delete to reset specific properties to their defaults.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cputype := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "reported-model", "flags", "guest-phys-bits",
				"hidden", "hv-vendor-id", "level", "phys-bits", "delete") {
				return fmt.Errorf("no changes to set: pass at least one field flag")
			}
			params := &pvecluster.UpdateQemuCustomCpuModelsParams{}
			if fl.Changed("reported-model") {
				params.ReportedModel = &reportedModel
			}
			if fl.Changed("flags") {
				params.Flags = &flags
			}
			if fl.Changed("guest-phys-bits") {
				params.GuestPhysBits = &guestPhysBits
			}
			if fl.Changed("hidden") {
				params.Hidden = &hidden
			}
			if fl.Changed("hv-vendor-id") {
				params.HvVendorId = &hvVendorID
			}
			if fl.Changed("level") {
				params.Level = &level
			}
			if fl.Changed("phys-bits") {
				params.PhysBits = &physBits
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateQemuCustomCpuModels(cmd.Context(), cputype, params); err != nil {
				return fmt.Errorf("update custom CPU model %q: %w", cputype, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Custom CPU model %s updated.", cputype)}, deps.Format)
		},
	}
	registerCpuModelFlags(cmd, &reportedModel, &flags, &guestPhysBits, &hidden, &hvVendorID, &level, &physBits)
	f := cmd.Flags()
	f.StringVar(&del, "delete", "", "comma-separated list of properties to reset to default")
	f.StringVar(&digest, "digest", "", "expected configuration digest; rejects the update if it has changed")
	return cmd
}

func newCpuModelDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <cputype>",
		Short: "Delete a custom CPU model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cputype := args[0]
			if err := requireDeleteYes(yes, "custom CPU model", cputype); err != nil {
				return err
			}
			if err := deps.API.Cluster.DeleteQemuCustomCpuModels(cmd.Context(), cputype); err != nil {
				return fmt.Errorf("delete custom CPU model %q: %w", cputype, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Custom CPU model %s deleted.", cputype)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// registerCpuModelFlags registers the CPU-model field flags shared by create
// and set.
func registerCpuModelFlags(cmd *cobra.Command, reportedModel, flags *string,
	guestPhysBits *int64, hidden *bool, hvVendorID *string, level *int64, physBits *string) {
	f := cmd.Flags()
	f.StringVar(reportedModel, "reported-model", "", "QEMU/KVM CPU model reported to the guest")
	f.StringVar(flags, "flags", "", "additional CPU flags separated by ';', e.g. '+aes;-spec-ctrl'")
	f.Int64Var(guestPhysBits, "guest-phys-bits", 0, "number of physical address bits available to the guest")
	f.BoolVar(hidden, "hidden", false, "do not identify as a KVM virtual machine")
	f.StringVar(hvVendorID, "hv-vendor-id", "", "Hyper-V vendor ID reported to Windows guests")
	f.Int64Var(level, "level", 0, "maximum input value for the basic CPUID leaves the guest can query")
	f.StringVar(physBits, "phys-bits", "", "physical memory address bits reported to the guest (or 'host')")
}
