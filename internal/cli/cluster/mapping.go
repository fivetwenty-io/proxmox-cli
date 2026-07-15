package cluster

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newMappingCmd builds the `pmx pve cluster mapping` sub-tree for managing
// hardware-mapping definitions. A mapping gives a logical name to one or more
// physical devices (PCI, USB) or host directories so guests can reference the
// name and Proxmox resolves it to the right device on whichever node runs the
// guest.
func newMappingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mapping",
		Short: "Manage cluster hardware and directory mappings",
		Long: "Manage logical mappings for PCI devices, USB devices, and host " +
			"directories. A mapping names a set of per-node device or path entries " +
			"so guests can reference the name instead of node-specific hardware.",
	}
	cmd.AddCommand(
		newMappingPciCmd(),
		newMappingUsbCmd(),
		newMappingDirCmd(),
	)
	return cmd
}

// mappingListColumns are the focused columns rendered for every mapping list.
// The per-node entries live under "map" (an array); Raw preserves everything.
var mappingListColumns = []string{"id", "description", "map"}

// --- shared list/get/delete builders ---------------------------------------

// newMappingListCmd builds a `list` command for a mapping type. call performs
// the type-specific client request and returns the raw element list.
func newMappingListCmd(call func(*cli.Deps, context.Context, *string) ([]json.RawMessage, error)) *cobra.Command {
	var checkNode string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List mappings",
		Long: "List the configured mappings of this type with their ID, description, and per-node " +
			"entries. Pass --check-node to validate each mapping against a node and include diagnostics.",
		Example: `  pmx pve cluster mapping pci list
  pmx pve cluster mapping usb list
  pmx pve cluster mapping dir list`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			var cn *string
			if cmd.Flags().Changed("check-node") {
				cn = &checkNode
			}
			raws, err := call(deps, cmd.Context(), cn)
			if err != nil {
				return fmt.Errorf("list mappings: %w", err)
			}
			res, err := rawFixedColumnsResult(raws, mappingListColumns)
			if err != nil {
				return fmt.Errorf("list mappings: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&checkNode, "check-node", "",
		"validate the mapping against this node and include diagnostics")
	return cmd
}

// newMappingGetCmd builds a `get <id>` command for a mapping type.
func newMappingGetCmd(kind string, call func(*cli.Deps, context.Context, string) (any, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show a single mapping",
		Long: "Show one mapping's full configuration by ID, including every per-node device or " +
			"path entry.",
		Example: `  pmx pve cluster mapping pci get gpu0
  pmx pve cluster mapping usb get scanner
  pmx pve cluster mapping dir get shared-data`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := call(deps, cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get %s mapping %q: %w", kind, id, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get %s mapping %q: %w", kind, id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// newMappingDeleteCmd builds a `delete <id>` command for a mapping type.
func newMappingDeleteCmd(kind string, call func(*cli.Deps, context.Context, string) error) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a mapping",
		Long:  "Delete a mapping by ID. Refuses to run without --yes.",
		Example: `  pmx pve cluster mapping pci delete gpu0 --yes
  pmx pve cluster mapping usb delete scanner --yes
  pmx pve cluster mapping dir delete shared-data --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete %s mapping %q without confirmation: pass --yes/-y", kind, id)
			}
			if err := call(deps, cmd.Context(), id); err != nil {
				return fmt.Errorf("delete %s mapping %q: %w", kind, id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s mapping %s deleted.", kind, id)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// --- PCI --------------------------------------------------------------------

func newMappingPciCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pci",
		Short: "Manage PCI device mappings",
		Long: "List, inspect, create, update, and delete PCI device mappings. Each mapping names " +
			"a set of per-node PCI devices so guests can reference the name instead of node-specific hardware.",
	}
	cmd.AddCommand(
		newMappingListCmd(func(d *cli.Deps, ctx context.Context, cn *string) ([]json.RawMessage, error) {
			resp, err := d.API.Cluster.ListMappingPci(ctx, &pvecluster.ListMappingPciParams{CheckNode: cn})
			return derefRawList(resp), err
		}),
		newMappingGetCmd("pci", func(d *cli.Deps, ctx context.Context, id string) (any, error) {
			return d.API.Cluster.GetMappingPci(ctx, id)
		}),
		newMappingPciCreateCmd(),
		newMappingPciSetCmd(),
		newMappingDeleteCmd("pci", func(d *cli.Deps, ctx context.Context, id string) error {
			return d.API.Cluster.DeleteMappingPci(ctx, id)
		}),
	)
	return cmd
}

func newMappingPciCreateCmd() *cobra.Command {
	var (
		entries     []string
		description string
		liveMig     bool
		mdev        bool
	)
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a PCI device mapping",
		Long: "Create a logical PCI mapping. Each --map entry is a per-node device " +
			"property string, for example 'node=pve,path=0000:01:00.0,id=10de:1b80'.",
		Example: `  pmx pve cluster mapping pci create gpu0 --map node=pve1,path=0000:01:00.0,id=10de:1b80`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := &pvecluster.CreateMappingPciParams{Id: id, Map: entries}
			fl := cmd.Flags()
			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("live-migration-capable") {
				params.LiveMigrationCapable = &liveMig
			}
			if fl.Changed("mdev") {
				params.Mdev = &mdev
			}
			if err := deps.API.Cluster.CreateMappingPci(cmd.Context(), params); err != nil {
				return fmt.Errorf("create pci mapping %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("pci mapping %s created.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "map", nil, "per-node device mapping entry (repeatable, required)")
	f.StringVar(&description, "description", "", "description of the mapping")
	f.BoolVar(&liveMig, "live-migration-capable", false, "mark the device(s) as live-migration capable (experimental)")
	f.BoolVar(&mdev, "mdev", false, "mark the device(s) as able to provide mediated devices")
	cli.MustMarkRequired(cmd, "map")
	return cmd
}

func newMappingPciSetCmd() *cobra.Command {
	var (
		entries     []string
		description string
		liveMig     bool
		mdev        bool
		digest      string
		del         string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update a PCI device mapping",
		Long: "Update a PCI mapping. --map re-sends the full per-node entry list " +
			"(the API rewrites it on every update); other flags are changed only when passed.",
		Example: `  pmx pve cluster mapping pci set gpu0 --map node=pve1,path=0000:01:00.0,id=10de:1b80`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			params := &pvecluster.UpdateMappingPciParams{Map: entries}
			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("live-migration-capable") {
				params.LiveMigrationCapable = &liveMig
			}
			if fl.Changed("mdev") {
				params.Mdev = &mdev
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if err := deps.API.Cluster.UpdateMappingPci(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update pci mapping %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("pci mapping %s updated.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "map", nil, "per-node device mapping entry (repeatable, required — re-sends the full list)")
	f.StringVar(&description, "description", "", "description of the mapping")
	f.BoolVar(&liveMig, "live-migration-capable", false, "mark the device(s) as live-migration capable (experimental)")
	f.BoolVar(&mdev, "mdev", false, "mark the device(s) as able to provide mediated devices")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to reset to default")
	cli.MustMarkRequired(cmd, "map")
	return cmd
}

// --- USB --------------------------------------------------------------------

func newMappingUsbCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usb",
		Short: "Manage USB device mappings",
		Long: "List, inspect, create, update, and delete USB device mappings. Each mapping names " +
			"a set of per-node USB devices so guests can reference the name instead of node-specific hardware.",
	}
	cmd.AddCommand(
		newMappingListCmd(func(d *cli.Deps, ctx context.Context, cn *string) ([]json.RawMessage, error) {
			resp, err := d.API.Cluster.ListMappingUsb(ctx, &pvecluster.ListMappingUsbParams{CheckNode: cn})
			return derefRawList(resp), err
		}),
		newMappingGetCmd("usb", func(d *cli.Deps, ctx context.Context, id string) (any, error) {
			return d.API.Cluster.GetMappingUsb(ctx, id)
		}),
		newMappingUsbCreateCmd(),
		newMappingUsbSetCmd(),
		newMappingDeleteCmd("usb", func(d *cli.Deps, ctx context.Context, id string) error {
			return d.API.Cluster.DeleteMappingUsb(ctx, id)
		}),
	)
	return cmd
}

func newMappingUsbCreateCmd() *cobra.Command {
	var (
		entries     []string
		description string
	)
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a USB device mapping",
		Long: "Create a logical USB mapping. Each --map entry is a per-node device " +
			"property string, for example 'node=pve,path=1-2,id=046d:c52b'.",
		Example: `  pmx pve cluster mapping usb create scanner --map node=pve1,path=1-2,id=046d:c52b`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := &pvecluster.CreateMappingUsbParams{Id: id, Map: entries}
			if cmd.Flags().Changed("description") {
				params.Description = &description
			}
			if err := deps.API.Cluster.CreateMappingUsb(cmd.Context(), params); err != nil {
				return fmt.Errorf("create usb mapping %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("usb mapping %s created.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "map", nil, "per-node device mapping entry (repeatable, required)")
	f.StringVar(&description, "description", "", "description of the mapping")
	cli.MustMarkRequired(cmd, "map")
	return cmd
}

func newMappingUsbSetCmd() *cobra.Command {
	var (
		entries     []string
		description string
		digest      string
		del         string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update a USB device mapping",
		Long: "Update a USB mapping. --map re-sends the full per-node entry list " +
			"(the API rewrites it on every update); other flags are changed only when passed.",
		Example: `  pmx pve cluster mapping usb set scanner --map node=pve1,path=1-2,id=046d:c52b`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			params := &pvecluster.UpdateMappingUsbParams{Map: entries}
			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if err := deps.API.Cluster.UpdateMappingUsb(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update usb mapping %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("usb mapping %s updated.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "map", nil, "per-node device mapping entry (repeatable, required — re-sends the full list)")
	f.StringVar(&description, "description", "", "description of the mapping")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to reset to default")
	cli.MustMarkRequired(cmd, "map")
	return cmd
}

// --- Directory --------------------------------------------------------------

func newMappingDirCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dir",
		Short: "Manage host directory mappings",
		Long: "List, inspect, create, update, and delete host directory mappings. Each mapping names " +
			"a set of per-node host paths so guests can reference the name instead of a node-specific directory.",
	}
	cmd.AddCommand(
		newMappingListCmd(func(d *cli.Deps, ctx context.Context, cn *string) ([]json.RawMessage, error) {
			resp, err := d.API.Cluster.ListMappingDir(ctx, &pvecluster.ListMappingDirParams{CheckNode: cn})
			return derefRawList(resp), err
		}),
		newMappingGetCmd("dir", func(d *cli.Deps, ctx context.Context, id string) (any, error) {
			return d.API.Cluster.GetMappingDir(ctx, id)
		}),
		newMappingDirCreateCmd(),
		newMappingDirSetCmd(),
		newMappingDeleteCmd("dir", func(d *cli.Deps, ctx context.Context, id string) error {
			return d.API.Cluster.DeleteMappingDir(ctx, id)
		}),
	)
	return cmd
}

func newMappingDirCreateCmd() *cobra.Command {
	var (
		entries     []string
		description string
	)
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a host directory mapping",
		Long: "Create a logical directory mapping. Each --map entry is a per-node " +
			"property string, for example 'node=pve,path=/mnt/data'.",
		Example: `  pmx pve cluster mapping dir create shared-data --map node=pve1,path=/mnt/data`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := &pvecluster.CreateMappingDirParams{Id: id, Map: entries}
			if cmd.Flags().Changed("description") {
				params.Description = &description
			}
			if err := deps.API.Cluster.CreateMappingDir(cmd.Context(), params); err != nil {
				return fmt.Errorf("create dir mapping %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("dir mapping %s created.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "map", nil, "per-node directory mapping entry (repeatable, required)")
	f.StringVar(&description, "description", "", "description of the mapping")
	cli.MustMarkRequired(cmd, "map")
	return cmd
}

func newMappingDirSetCmd() *cobra.Command {
	var (
		entries     []string
		description string
		digest      string
		del         string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update a host directory mapping",
		Long: "Update a directory mapping. --map re-sends the full per-node entry list " +
			"(the API rewrites it on every update); other flags are changed only when passed.",
		Example: `  pmx pve cluster mapping dir set shared-data --map node=pve1,path=/mnt/data`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			params := &pvecluster.UpdateMappingDirParams{Map: entries}
			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if err := deps.API.Cluster.UpdateMappingDir(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update dir mapping %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("dir mapping %s updated.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&entries, "map", nil, "per-node directory mapping entry (repeatable, required — re-sends the full list)")
	f.StringVar(&description, "description", "", "description of the mapping")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to reset to default")
	cli.MustMarkRequired(cmd, "map")
	return cmd
}
