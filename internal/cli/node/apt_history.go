package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/aptview"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newAptHistoryCmd builds `pmx pve node apt history` (GET /nodes/{node}/apt/history).
func newAptHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List the package transactions of a node",
		Long: "List the APT transactions that ran on the resolved node, newest first, with when each " +
			"started and ended, the command line that ran it, a count of what it did to packages, and " +
			"any error. --since and --until take a UNIX epoch or an RFC 3339 time. The START and INDEX " +
			"columns name a transaction for 'apt termlog' and 'apt transaction-changelogs', and the " +
			"package list of each transaction is in the json and yaml output. Reading the history " +
			"needs Sys.Audit on /nodes/{node}.",
		Example: `  pmx pve node apt history --node pve1 --limit 10
  pmx pve node apt history --node pve1 --package pve-manager --since 2026-10-01T00:00:00Z`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindHistory(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		if err := requireNode(deps); err != nil {
			return err
		}
		h, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.API.Nodes.ListAptHistory(cmd.Context(), deps.Node, h.PVE())
		if err != nil {
			return fmt.Errorf("read the package history of node %q: %w", deps.Node, err)
		}
		res, err := aptview.HistoryResult(derefRaws(resp), resp)
		if err != nil {
			return err
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}
	return cmd
}

// newAptTermlogCmd builds `pmx pve node apt termlog` (GET /nodes/{node}/apt/termlog).
func newAptTermlogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "termlog",
		Short: "Show the terminal output of a package transaction",
		Long: "Print the terminal output that one APT transaction produced on the resolved node. Name " +
			"the transaction with --start-time and, when several started in the same second, " +
			"--start-index, both as 'apt history' lists them. --format styled keeps bold, dim, and " +
			"basic colors as escape sequences. Reading the log needs Sys.Syslog on /nodes/{node}.",
		Example: `  pmx pve node apt termlog --node pve1 --start-time 2026-11-01T12:00:00Z
  pmx pve node apt termlog --node pve1 --start-time 1793534400 --start-index 1 --format styled`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindTransaction(cmd, true)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		if err := requireNode(deps); err != nil {
			return err
		}
		t, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.API.Nodes.ListAptTermlog(cmd.Context(), deps.Node, t.PVETermlog())
		if err != nil {
			return fmt.Errorf("read the terminal log of node %q: %w", deps.Node, err)
		}
		var raw json.RawMessage
		if resp != nil {
			raw = *resp
		}
		return deps.Out.Render(cmd.OutOrStdout(), aptview.TextResult(raw), deps.Format)
	}
	return cmd
}

// newAptInstalledChangelogCmd builds `pmx pve node apt installed-changelog`
// (GET /nodes/{node}/apt/installed-changelog).
func newAptInstalledChangelogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "installed-changelog",
		Short: "List the changelog entries of an installed package",
		Long: "List the changelog entries of a package that is installed on the resolved node, usually " +
			"newest first, with the version, date, urgency, distributions, and the first line of the " +
			"changes. The full change text is in the json and yaml output. --since-version keeps only " +
			"later versions, and --to-version stops at that version, or keeps only the newest entry " +
			"when the changelog does not list it. Use 'apt changelog' for the text of an available " +
			"update. Reading it needs Sys.Audit on /nodes/{node}.",
		Example: `  pmx pve node apt installed-changelog --node pve1 --name pve-manager
  pmx pve node apt installed-changelog --node pve1 --name pve-manager --since-version 9.0.0`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindInstalled(cmd)
	cli.MustMarkRequired(cmd, "name")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		if err := requireNode(deps); err != nil {
			return err
		}
		i := flags.Collect(cmd)
		resp, err := deps.API.Nodes.ListAptInstalledChangelog(cmd.Context(), deps.Node, i.PVE())
		if err != nil {
			return fmt.Errorf("read the installed changelog of package %q on node %q: %w", i.Name, deps.Node, err)
		}
		res, err := aptview.ChangelogResult(derefRaws(resp), resp)
		if err != nil {
			return err
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}
	return cmd
}

// newAptTransactionChangelogsCmd builds `pmx pve node apt transaction-changelogs`
// (GET /nodes/{node}/apt/transaction-changelogs).
func newAptTransactionChangelogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transaction-changelogs",
		Short: "List the changelogs of the packages a transaction changed",
		Long: "List the changelog entries of the packages that one APT transaction changed on the " +
			"resolved node, grouped by source package, with upgrades first, then downgrades, installs, " +
			"and other changes. Name the transaction with --start-time and, when several started in the " +
			"same second, --start-index, both as 'apt history' lists them. The entries of each group " +
			"are in the json and yaml output. Reading them needs Sys.Audit on /nodes/{node}.",
		Example: `  pmx pve node apt transaction-changelogs --node pve1 --start-time 2026-11-01T12:00:00Z
  pmx pve node apt transaction-changelogs --node pve1 --start-time 1793534400 -o json`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindTransaction(cmd, false)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		if err := requireNode(deps); err != nil {
			return err
		}
		t, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.API.Nodes.ListAptTransactionChangelogs(cmd.Context(), deps.Node, t.PVEChangelogs())
		if err != nil {
			return fmt.Errorf("read the transaction changelogs of node %q: %w", deps.Node, err)
		}
		res, err := aptview.GroupedChangelogResult(derefRaws(resp), resp)
		if err != nil {
			return err
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}
	return cmd
}

// newAptUpdateChangelogsCmd builds `pmx pve node apt update-changelogs`
// (GET /nodes/{node}/apt/update-changelogs).
func newAptUpdateChangelogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update-changelogs",
		Short: "List the changelogs of the pending package updates",
		Long: "List the changelog entries of every package that has a pending update on the resolved " +
			"node, grouped by source package. The entries of each group are in the json and yaml " +
			"output. It needs Sys.Modify on /nodes/{node}, like the update list itself.",
		Example: `  pmx pve node apt update-changelogs --node pve1
  pmx pve node apt update-changelogs --node pve1 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListAptUpdateChangelogs(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("read the update changelogs of node %q: %w", deps.Node, err)
			}
			res, err := aptview.GroupedChangelogResult(derefRaws(resp), resp)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
