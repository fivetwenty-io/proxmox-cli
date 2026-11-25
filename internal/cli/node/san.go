package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newSanCmd builds the `pmx pve node san` sub-tree, which reads the storage
// area network state of a node: its iSCSI initiator and targets, its LUNs, and
// its NVMe over Fabrics host identity and controllers.
func newSanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "san",
		Short: "Inspect the SAN state of a node",
		Long: "Read the storage area network state of the resolved node, which covers the iSCSI " +
			"initiator and its targets, the LUNs the node sees, and the NVMe over Fabrics host " +
			"identity and controllers. Every read needs Sys.Audit on the node or Datastore.Allocate " +
			"on /storage. Disconnecting a stale NVMe controller is the one change this group makes.",
		Example: `  pmx pve node san list --node pve1
  pmx pve node san luns --node pve1`,
	}
	cmd.AddCommand(
		newSanListCmd(),
		newSanLunsCmd(),
		newSanIscsiCmd(),
		newSanNvmeofCmd(),
	)
	return cmd
}

// sanListVerb builds a read verb that lists JSON objects as a union-key table.
func sanListVerb(use, short, long, example, what string,
	fetch func(ctx context.Context, deps *cli.Deps) ([]json.RawMessage, any, error),
) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			raws, raw, err := fetch(cmd.Context(), deps)
			if err != nil {
				return fmt.Errorf("%s on node %q: %w", what, deps.Node, err)
			}
			res, err := rawObjectListResult(raws)
			if err != nil {
				return fmt.Errorf("decode %s: %w", what, err)
			}
			res.Raw = raw
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newSanListCmd() *cobra.Command {
	return sanListVerb("list", "List the SAN areas of a node",
		"List the SAN areas that the resolved node reports.",
		`  pmx pve node san list --node pve1`, "list SAN areas",
		func(ctx context.Context, deps *cli.Deps) ([]json.RawMessage, any, error) {
			resp, err := deps.API.Nodes.ListSan(ctx, deps.Node)
			return derefRaws(resp), resp, err
		})
}

func newSanLunsCmd() *cobra.Command {
	return sanListVerb("luns", "List the LUNs a node sees",
		"List the LUNs and NVMe namespaces that the resolved node sees, with their WWIDs and paths.",
		`  pmx pve node san luns --node pve1`, "list SAN LUNs",
		func(ctx context.Context, deps *cli.Deps) ([]json.RawMessage, any, error) {
			resp, err := deps.API.Nodes.ListSanLuns(ctx, deps.Node)
			return derefRaws(resp), resp, err
		})
}

func newSanIscsiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "iscsi",
		Short: "Inspect the iSCSI initiator of a node",
		Long: "Read the iSCSI state of the resolved node, which is the initiator identity and the " +
			"targets the node has logged in to.",
		Example: `  pmx pve node san iscsi status --node pve1
  pmx pve node san iscsi targets --node pve1`,
	}
	cmd.AddCommand(
		sanListVerb("list", "List the iSCSI areas of a node",
			"List the iSCSI areas that the resolved node reports.",
			`  pmx pve node san iscsi list --node pve1`, "list iSCSI areas",
			func(ctx context.Context, deps *cli.Deps) ([]json.RawMessage, any, error) {
				resp, err := deps.API.Nodes.ListSanIscsi(ctx, deps.Node)
				return derefRaws(resp), resp, err
			}),
		sanListVerb("targets", "List the iSCSI targets of a node",
			"List the iSCSI targets that the resolved node has sessions with.",
			`  pmx pve node san iscsi targets --node pve1`, "list iSCSI targets",
			func(ctx context.Context, deps *cli.Deps) ([]json.RawMessage, any, error) {
				resp, err := deps.API.Nodes.ListSanIscsiTarget(ctx, deps.Node)
				return derefRaws(resp), resp, err
			}),
		newSanIscsiStatusCmd(),
	)
	return cmd
}

func newSanIscsiStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the iSCSI initiator identity of a node",
		Long: "Show whether open-iscsi is installed on the resolved node, together with the " +
			"initiator name and alias that targets need for access control.",
		Example: `  pmx pve node san iscsi status --node pve1`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSanIscsiStatus(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("read iSCSI status on node %q: %w", deps.Node, err)
			}
			single, _, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode iSCSI status: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}

func newSanNvmeofCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nvmeof",
		Short: "Inspect NVMe over Fabrics on a node",
		Long: "Read the NVMe over Fabrics state of the resolved node, which is its host identity " +
			"and the controllers it is connected to, and disconnect a controller that is no longer wanted.",
		Example: `  pmx pve node san nvmeof status --node pve1
  pmx pve node san nvmeof controllers --node pve1`,
	}
	cmd.AddCommand(
		sanListVerb("list", "List the NVMe over Fabrics areas of a node",
			"List the NVMe over Fabrics areas that the resolved node reports.",
			`  pmx pve node san nvmeof list --node pve1`, "list NVMe-oF areas",
			func(ctx context.Context, deps *cli.Deps) ([]json.RawMessage, any, error) {
				resp, err := deps.API.Nodes.ListSanNvmeof(ctx, deps.Node)
				return derefRaws(resp), resp, err
			}),
		sanListVerb("controllers", "List the NVMe over Fabrics controllers of a node",
			"List the NVMe over Fabrics controllers that the resolved node is connected to.",
			`  pmx pve node san nvmeof controllers --node pve1`, "list NVMe-oF controllers",
			func(ctx context.Context, deps *cli.Deps) ([]json.RawMessage, any, error) {
				resp, err := deps.API.Nodes.ListSanNvmeofController(ctx, deps.Node)
				return derefRaws(resp), resp, err
			}),
		newSanNvmeofStatusCmd(),
		newSanNvmeofControllerCmd(),
	)
	return cmd
}

func newSanNvmeofStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the NVMe over Fabrics host identity of a node",
		Long: "Show whether nvme-cli is installed on the resolved node, together with the host NQN " +
			"and host ID that targets need for access control. When the identity is unavailable, " +
			"the reason is shown as well.",
		Example: `  pmx pve node san nvmeof status --node pve1`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSanNvmeofStatus(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("read NVMe-oF status on node %q: %w", deps.Node, err)
			}
			single, _, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode NVMe-oF status: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}

func newSanNvmeofControllerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "controller",
		Short:   "Disconnect an NVMe over Fabrics controller",
		Long:    "Act on one NVMe over Fabrics controller of the resolved node. Use 'controllers' to list them.",
		Example: `  pmx pve node san nvmeof controller delete nvme1 --node pve1 --yes`,
	}
	cmd.AddCommand(newSanNvmeofControllerDeleteCmd())
	return cmd
}

func newSanNvmeofControllerDeleteCmd() *cobra.Command {
	var (
		yes       bool
		portal    string
		subsysnqn string
	)
	cmd := &cobra.Command{
		Use:     "delete <controller>",
		Aliases: []string{"rm"},
		Short:   "Disconnect an NVMe over Fabrics controller",
		Long: "Disconnect one NVMe over Fabrics controller, such as nvme1, from the resolved node. " +
			"Storage that is using the controller loses its paths, so the command asks for " +
			"confirmation. On a terminal it prompts, and anywhere else it refuses unless --yes/-y is " +
			"passed. --portal and --subsysnqn make the server refuse unless the controller still has " +
			"that portal or subsystem NQN, which guards against a controller number that was reused. " +
			"The server runs the disconnect as a task, and the command waits for it unless --async " +
			"is set. It needs Sys.Modify on /. " + cli.WaitBoundHelp,
		Example: `  pmx pve node san nvmeof controller delete nvme1 --node pve1 --yes
  pmx pve node san nvmeof controller delete nvme1 --node pve1 --subsysnqn nqn.2014-08.org.example:sub1 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			controller := args[0]
			ok, err := cli.ConfirmDestructive(cmd, yes,
				fmt.Sprintf("disconnect NVMe-oF controller %q on node %q", controller, deps.Node),
				fmt.Sprintf("Disconnect NVMe-oF controller %q on node %q? Storage using it loses its paths.",
					controller, deps.Node))
			if err != nil {
				return err
			}
			if !ok {
				return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: "Aborted."}, deps.Format)
			}
			params := &nodes.DeleteSanNvmeofControllerParams{}
			if cmd.Flags().Changed("portal") {
				params.Portal = &portal
			}
			if cmd.Flags().Changed("subsysnqn") {
				params.Subsysnqn = &subsysnqn
			}
			resp, err := deps.API.Nodes.DeleteSanNvmeofController(cmd.Context(), deps.Node, controller, params)
			if err != nil {
				return fmt.Errorf("disconnect NVMe-oF controller %q on node %q: %w", controller, deps.Node, err)
			}
			done := fmt.Sprintf("Controller %q disconnected on node %q.", controller, deps.Node)
			var raw json.RawMessage
			if resp != nil {
				raw = *resp
			}
			upid, upidErr := apiclient.UPIDFromRaw(raw)
			if upidErr != nil {
				return fmt.Errorf("disconnect NVMe-oF controller %q on node %q: the server did not return a task ID: %w",
					controller, deps.Node, upidErr)
			}
			if err := cli.RenderTaskWait(cmd, deps, upid, done, nil); err != nil {
				return fmt.Errorf("disconnect NVMe-oF controller %q on node %q: %w", controller, deps.Node, err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the disconnect without prompting")
	cmd.Flags().StringVar(&portal, "portal", "", "refuse unless the controller still has this portal")
	cmd.Flags().StringVar(&subsysnqn, "subsysnqn", "", "refuse unless the controller still has this subsystem NQN")
	return cmd
}

func newScanNvmeofCmd() *cobra.Command {
	var portal string
	cmd := &cobra.Command{
		Use:   "nvmeof",
		Short: "List NVMe over Fabrics subsystems on a portal",
		Long: "Query --portal, from the resolved node, for the NVMe over Fabrics subsystems it offers. " +
			"The portal is an IPv4 address or a bracketed IPv6 address with an optional port. Without " +
			"a port the server tries 8009 and then 4420. It needs Datastore.Allocate on /storage.",
		Example: `  pmx pve node scan nvmeof --portal 10.0.0.5
  pmx pve node scan nvmeof --portal '[fd00::5]:4420'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListScanNvmeof(cmd.Context(), deps.Node,
				&nodes.ListScanNvmeofParams{Portal: portal})
			if err != nil {
				return fmt.Errorf("scan NVMe-oF portal %q from node %q: %w", portal, deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().StringVar(&portal, "portal", "", "NVMe-oF discovery portal address (required)")
	cli.MustMarkRequired(cmd, "portal")
	return cmd
}
