package pbs

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/aptview"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newNodeAptHistoryCmd builds `pmx pbs node apt history` (GET /nodes/{node}/apt/history).
func newNodeAptHistoryCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List the package transactions of the node",
		Long: "List the APT transactions that ran on the node, newest first, with when each started " +
			"and ended, the command line that ran it, a count of what it did to packages, and any " +
			"error. --since and --until take a UNIX epoch or an RFC 3339 time. The START and INDEX " +
			"columns name a transaction for 'apt termlog' and 'apt transaction-changelogs', and the " +
			"package list of each transaction is in the json and yaml output.",
		Example: `  pmx pbs node apt history --limit 10
  pmx pbs node apt history --package proxmox-backup-server --since 2026-10-01T00:00:00Z`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindHistory(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		h, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.PBS.Nodes.ListAptHistory(cmd.Context(), nf.node, h.PBS())
		if err != nil {
			return fmt.Errorf("read the package history of node %q: %w", nf.node, err)
		}
		res, err := aptview.HistoryResult(rawItemsOf(resp), resp)
		if err != nil {
			return err
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}
	return cmd
}

// newNodeAptTermlogCmd builds `pmx pbs node apt termlog` (GET /nodes/{node}/apt/termlog).
func newNodeAptTermlogCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "termlog",
		Short: "Show the terminal output of a package transaction",
		Long: "Print the terminal output that one APT transaction produced on the node. Name the " +
			"transaction with --start-time and, when several started in the same second, " +
			"--start-index, both as 'apt history' lists them. --format styled keeps bold, dim, and " +
			"basic colors as escape sequences.",
		Example: `  pmx pbs node apt termlog --start-time 2026-11-01T12:00:00Z
  pmx pbs node apt termlog --start-time 1793534400 --start-index 1 --format styled`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindTransaction(cmd, true)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		t, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.PBS.Nodes.ListAptTermlog(cmd.Context(), nf.node, t.PBSTermlog())
		if err != nil {
			return fmt.Errorf("read the terminal log of node %q: %w", nf.node, err)
		}
		var raw json.RawMessage
		if resp != nil {
			raw = *resp
		}
		return deps.Out.Render(cmd.OutOrStdout(), aptview.TextResult(raw), deps.Format)
	}
	return cmd
}

// newNodeAptInstalledChangelogCmd builds `pmx pbs node apt installed-changelog`
// (GET /nodes/{node}/apt/installed-changelog).
func newNodeAptInstalledChangelogCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "installed-changelog",
		Short: "List the changelog entries of an installed package",
		Long: "List the changelog entries of a package that is installed on the node, usually newest " +
			"first, with the version, date, urgency, distributions, and the first line of the " +
			"changes. The full change text is in the json and yaml output. --since-version keeps only " +
			"later versions, and --to-version stops at that version, or keeps only the newest entry " +
			"when the changelog does not list it. Use 'apt changelog' for the text of an available update.",
		Example: `  pmx pbs node apt installed-changelog --name proxmox-backup-server
  pmx pbs node apt installed-changelog --name proxmox-backup-server --since-version 4.0.0`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindInstalled(cmd)
	cli.MustMarkRequired(cmd, "name")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		i := flags.Collect(cmd)
		resp, err := deps.PBS.Nodes.ListAptInstalledChangelog(cmd.Context(), nf.node, i.PBS())
		if err != nil {
			return fmt.Errorf("read the installed changelog of package %q on node %q: %w", i.Name, nf.node, err)
		}
		res, err := aptview.ChangelogResult(rawItemsOf(resp), resp)
		if err != nil {
			return err
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}
	return cmd
}

// newNodeAptTransactionChangelogsCmd builds `pmx pbs node apt transaction-changelogs`
// (GET /nodes/{node}/apt/transaction-changelogs).
func newNodeAptTransactionChangelogsCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transaction-changelogs",
		Short: "List the changelogs of the packages a transaction changed",
		Long: "List the changelog entries of the packages that one APT transaction changed on the " +
			"node, grouped by source package, with upgrades first, then downgrades, installs, and " +
			"other changes. Name the transaction with --start-time and, when several started in the " +
			"same second, --start-index, both as 'apt history' lists them. The entries of each group " +
			"are in the json and yaml output.",
		Example: `  pmx pbs node apt transaction-changelogs --start-time 2026-11-01T12:00:00Z
  pmx pbs node apt transaction-changelogs --start-time 1793534400 -o json`,
		Args: cobra.NoArgs,
	}
	flags := aptview.BindTransaction(cmd, false)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		t, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.PBS.Nodes.ListAptTransactionChangelogs(cmd.Context(), nf.node, t.PBSChangelogs())
		if err != nil {
			return fmt.Errorf("read the transaction changelogs of node %q: %w", nf.node, err)
		}
		res, err := aptview.GroupedChangelogResult(rawItemsOf(resp), resp)
		if err != nil {
			return err
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}
	return cmd
}

// newNodeAptUpdateChangelogsCmd builds `pmx pbs node apt update-changelogs`
// (GET /nodes/{node}/apt/update-changelogs).
func newNodeAptUpdateChangelogsCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "update-changelogs",
		Short: "List the changelogs of the pending package updates",
		Long: "List the changelog entries of every package that has a pending update on the node, " +
			"grouped by source package. The entries of each group are in the json and yaml output.",
		Example: `  pmx pbs node apt update-changelogs
  pmx pbs node apt update-changelogs -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.PBS.Nodes.ListAptUpdateChangelogs(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("read the update changelogs of node %q: %w", nf.node, err)
			}
			res, err := aptview.GroupedChangelogResult(rawItemsOf(resp), resp)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
