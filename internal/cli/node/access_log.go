package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/accesslog"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newAccessLogCmd builds `pmx pve node access-log`, which reads the request log
// of the node's API proxy (GET /nodes/{node}/access-log). Run without a
// sub-command it lists requests, and its stats sub-command summarizes them.
func newAccessLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access-log",
		Short: "Read the API request log of a node",
		Long: "List the requests that the API proxy of the resolved node logged, newest first, with " +
			"the time, client, user or API token, method, path, status, and size of each. The filters " +
			"narrow the requests by client, user, method, path, status range, or time. The server " +
			"returns one page at a time, sized by --limit, and when older requests remain the table " +
			"output prints a --cursor value on the error stream that continues with the next page, " +
			"while json and yaml carry it as next-cursor. The table output also prints the head " +
			"cursor of the page on the error stream, and --after follows the log forward from it. " +
			"A warning on the error stream says when the page is incomplete or skipped parts of the log. The 'stats' sub-command counts the requests instead. " +
			"Reading the log needs Sys.Syslog on /nodes/{node}.",
		Example: `  pmx pve node access-log --node pve1 --limit 20
  pmx pve node access-log --node pve1 --client 10.0.0.5 --min-status 400
  pmx pve node access-log --node pve1 --cursor <next-cursor> --limit 20`,
		Args: cobra.NoArgs,
	}
	flags := accesslog.BindList(cmd, false)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		if err := requireNode(deps); err != nil {
			return err
		}
		f, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.API.Nodes.ListAccessLog(cmd.Context(), deps.Node, f.PVEList())
		if err != nil {
			return fmt.Errorf("read the access log of node %q: %w", deps.Node, err)
		}
		if resp == nil {
			return fmt.Errorf("read the access log of node %q: empty response from server", deps.Node)
		}
		page := accesslog.Page{
			Entries: resp.Entries, NextCursor: resp.NextCursor, HeadCursor: resp.HeadCursor,
			Complete: bool(resp.Complete), Gaps: len(resp.Gaps), Raw: resp,
		}
		res, err := accesslog.ListResult(page)
		if err != nil {
			return err
		}
		if err := deps.Out.Render(cmd.OutOrStdout(), res, deps.Format); err != nil {
			return err
		}
		accesslog.WriteNotes(cmd.ErrOrStderr(), deps.Format, page)
		return nil
	}
	cmd.AddCommand(newAccessLogStatsCmd())
	return cmd
}

// newAccessLogStatsCmd builds `pmx pve node access-log stats`
// (GET /nodes/{node}/access-log/stats).
func newAccessLogStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Summarize the API request log of a node",
		Long: "Count the requests in the API request log of the resolved node, with the totals, the " +
			"client and server errors, and the most active clients, users, status codes, and path " +
			"groups. The filters narrow the requests the same way they do for the list. --top sets how " +
			"many values each ranking holds and --bucket sets the histogram bucket in seconds. The " +
			"table shows the leading values of each ranking, and the json and yaml output carries the " +
			"full rankings and the histogram. Reading the log needs Sys.Syslog on /nodes/{node}.",
		Example: `  pmx pve node access-log stats --node pve1
  pmx pve node access-log stats --node pve1 --since 2026-11-01T00:00:00Z --top 10 -o json`,
		Args: cobra.NoArgs,
	}
	flags := accesslog.BindStats(cmd, false)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		if err := requireNode(deps); err != nil {
			return err
		}
		f, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.API.Nodes.ListAccessLogStats(cmd.Context(), deps.Node, f.PVEStats())
		if err != nil {
			return fmt.Errorf("read the access-log statistics of node %q: %w", deps.Node, err)
		}
		if resp == nil {
			return fmt.Errorf("read the access-log statistics of node %q: empty response from server", deps.Node)
		}
		res, err := accesslog.StatsResult(accesslog.StatsInput{
			Since: int64(resp.Since), Until: int64(resp.Until), InvalidLines: int64(resp.InvalidLines),
			Complete: bool(resp.Complete), Stopped: resp.Stopped, Total: resp.Total,
			Lists: []accesslog.NamedList{
				{Name: "auth-ids", Data: resp.AuthIds}, {Name: "clients", Data: resp.Clients},
				{Name: "statuses", Data: resp.Statuses}, {Name: "path-groups", Data: resp.PathGroups},
			},
			Raw: resp,
		})
		if err != nil {
			return err
		}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}
	return cmd
}
