package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// ---- mon -------------------------------------------------------------------

func newCephMonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mon",
		Short: "Inspect and manage Ceph monitors",
		Long:  "List Ceph monitors and create or destroy monitor daemons on the resolved node.",
	}
	cmd.AddCommand(newCephMonListCmd(), newCephMonCreateCmd(), newCephMonDeleteCmd())
	return cmd
}

func newCephMonListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Ceph monitors",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephMon(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list Ceph monitors on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newCephMonCreateCmd() *cobra.Command {
	var (
		monAddress string
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "create <monid>",
		Short: "Create a Ceph monitor (destructive)",
		Long:  "Create a Ceph monitor daemon with the given id on the resolved node.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("create Ceph monitor %q", args[0])); err != nil {
				return err
			}
			params := &nodes.CreateCephMonParams{}
			if cmd.Flags().Changed("mon-address") {
				params.MonAddress = &monAddress
			}
			resp, err := deps.API.Nodes.CreateCephMon(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("create Ceph monitor %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph monitor %q created on node %q.", args[0], deps.Node))
		},
	}
	f := cmd.Flags()
	f.StringVar(&monAddress, "mon-address", "", "override the autodetected monitor IP address(es)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newCephMonDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <monid>",
		Short: "Destroy a Ceph monitor (destructive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("destroy Ceph monitor %q", args[0])); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.DeleteCephMon(cmd.Context(), deps.Node, args[0])
			if err != nil {
				return fmt.Errorf("destroy Ceph monitor %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph monitor %q destroyed on node %q.", args[0], deps.Node))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ---- mds -------------------------------------------------------------------

func newCephMdsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mds",
		Short: "Inspect and manage Ceph metadata servers",
		Long:  "List Ceph metadata servers (MDS) and create or destroy MDS daemons on the resolved node.",
	}
	cmd.AddCommand(newCephMdsListCmd(), newCephMdsCreateCmd(), newCephMdsDeleteCmd())
	return cmd
}

func newCephMdsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Ceph metadata servers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephMds(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list Ceph metadata servers on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newCephMdsCreateCmd() *cobra.Command {
	var (
		hotstandby bool
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a Ceph metadata server (destructive)",
		Long:  "Create a Ceph metadata server (MDS) daemon with the given name on the resolved node.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("create Ceph metadata server %q", args[0])); err != nil {
				return err
			}
			params := &nodes.CreateCephMdsParams{}
			if cmd.Flags().Changed("hotstandby") {
				params.Hotstandby = &hotstandby
			}
			resp, err := deps.API.Nodes.CreateCephMds(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("create Ceph metadata server %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph metadata server %q created on node %q.", args[0], deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&hotstandby, "hotstandby", false, "poll and replay the active MDS log for faster failover")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newCephMdsDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Destroy a Ceph metadata server (destructive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("destroy Ceph metadata server %q", args[0])); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.DeleteCephMds(cmd.Context(), deps.Node, args[0])
			if err != nil {
				return fmt.Errorf("destroy Ceph metadata server %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph metadata server %q destroyed on node %q.", args[0], deps.Node))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ---- mgr -------------------------------------------------------------------

func newCephMgrCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mgr",
		Short: "Inspect and manage Ceph managers",
		Long:  "List Ceph managers (MGR) and create or destroy manager daemons on the resolved node.",
	}
	cmd.AddCommand(newCephMgrListCmd(), newCephMgrCreateCmd(), newCephMgrDeleteCmd())
	return cmd
}

func newCephMgrListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Ceph managers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephMgr(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list Ceph managers on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newCephMgrCreateCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a Ceph manager (destructive)",
		Long:  "Create a Ceph manager (MGR) daemon with the given id on the resolved node.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("create Ceph manager %q", args[0])); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.CreateCephMgr(cmd.Context(), deps.Node, args[0])
			if err != nil {
				return fmt.Errorf("create Ceph manager %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph manager %q created on node %q.", args[0], deps.Node))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newCephMgrDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Destroy a Ceph manager (destructive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("destroy Ceph manager %q", args[0])); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.DeleteCephMgr(cmd.Context(), deps.Node, args[0])
			if err != nil {
				return fmt.Errorf("destroy Ceph manager %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Ceph manager %q destroyed on node %q.", args[0], deps.Node))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// ---- fs --------------------------------------------------------------------

func newCephFsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fs",
		Short: "Inspect and manage CephFS filesystems",
		Long:  "List CephFS filesystems and create or destroy filesystems on the resolved node.",
	}
	cmd.AddCommand(newCephFsListCmd(), newCephFsCreateCmd(), newCephFsDeleteCmd())
	return cmd
}

func newCephFsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List CephFS filesystems",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCephFs(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list CephFS filesystems on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

func newCephFsCreateCmd() *cobra.Command {
	var (
		addStorage bool
		pgNum      int64
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a CephFS filesystem (destructive)",
		Long:  "Create a CephFS filesystem with the given name, provisioning its backing data and metadata pools.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("create CephFS filesystem %q", args[0])); err != nil {
				return err
			}
			params := &nodes.CreateCephFsParams{}
			fl := cmd.Flags()
			if fl.Changed("add-storage") {
				params.AddStorage = &addStorage
			}
			if fl.Changed("pg-num") {
				params.PgNum = &pgNum
			}
			resp, err := deps.API.Nodes.CreateCephFs(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("create CephFS filesystem %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("CephFS filesystem %q created on node %q.", args[0], deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&addStorage, "add-storage", false, "configure the created CephFS as cluster storage")
	f.Int64Var(&pgNum, "pg-num", 0, "placement groups for the backing data pool")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

func newCephFsDeleteCmd() *cobra.Command {
	var (
		removePools    bool
		removeStorages bool
		yes            bool
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Destroy a CephFS filesystem (destructive)",
		Long:  "Destroy the given CephFS filesystem, optionally removing its pools and managed storages.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, fmt.Sprintf("destroy CephFS filesystem %q", args[0])); err != nil {
				return err
			}
			params := &nodes.DeleteCephFsParams{}
			fl := cmd.Flags()
			if fl.Changed("remove-pools") {
				params.RemovePools = &removePools
			}
			if fl.Changed("remove-storages") {
				params.RemoveStorages = &removeStorages
			}
			resp, err := deps.API.Nodes.DeleteCephFs(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("destroy CephFS filesystem %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderCephTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("CephFS filesystem %q destroyed on node %q.", args[0], deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&removePools, "remove-pools", false, "also remove the metadata and data pools")
	f.BoolVar(&removeStorages, "remove-storages", false, "remove pveceph-managed storages for this filesystem")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
