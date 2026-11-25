package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/accesslog"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newNodeAccessLogCmd builds `pmx pbs node access-log`, which reads the request
// log of the proxy (GET /nodes/{node}/access-log). Run without a sub-command it
// lists requests, and its stats sub-command summarizes them.
func newNodeAccessLogCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access-log",
		Short: "Read the API request log of the node",
		Long: "List the requests that the API proxy logged, newest first, with the time, client, user " +
			"or API token, method, path, status, size, and user agent of each. The filters narrow the " +
			"requests by client, user, user agent, method, path, status range, or time. The server " +
			"returns one page at a time, sized by --limit, and when older requests remain the table " +
			"output prints a --cursor value on the error stream that continues with the next page, " +
			"while json and yaml carry it as next-cursor. --after follows the log forward from the " +
			"head-cursor of an earlier page. The 'stats' sub-command counts the requests instead.",
		Example: `  pmx pbs node access-log --limit 20
  pmx pbs node access-log --user-agent proxmox-backup-client --min-status 400
  pmx pbs node access-log --cursor <next-cursor> --limit 20`,
		Args: cobra.NoArgs,
	}
	flags := accesslog.BindList(cmd, true)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		f, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.PBS.Nodes.ListAccessLog(cmd.Context(), nf.node, f.PBSList())
		if err != nil {
			return fmt.Errorf("read the access log of node %q: %w", nf.node, err)
		}
		if resp == nil {
			return fmt.Errorf("read the access log of node %q: empty response from server", nf.node)
		}
		res, err := accesslog.ListResult(accesslog.Page{Entries: resp.Entries, NextCursor: resp.NextCursor, Raw: resp})
		if err != nil {
			return err
		}
		if err := deps.Out.Render(cmd.OutOrStdout(), res, deps.Format); err != nil {
			return err
		}
		accesslog.WriteNextCursor(cmd.ErrOrStderr(), deps.Format, resp.NextCursor)
		return nil
	}
	cmd.AddCommand(newNodeAccessLogStatsCmd(nf))
	return cmd
}

// newNodeAccessLogStatsCmd builds `pmx pbs node access-log stats`
// (GET /nodes/{node}/access-log/stats).
func newNodeAccessLogStatsCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Summarize the API request log of the node",
		Long: "Count the requests in the API request log, with the totals, the client and server " +
			"errors, and the most active clients, users, user agents, status codes, and path groups. " +
			"The filters narrow the requests the same way they do for the list. --top sets how many " +
			"values each ranking holds and --bucket sets the histogram bucket in seconds. The table " +
			"shows the leading values of each ranking, and the json and yaml output carries the full " +
			"rankings and the histogram.",
		Example: `  pmx pbs node access-log stats
  pmx pbs node access-log stats --since 2026-11-01T00:00:00Z --top 10 -o json`,
		Args: cobra.NoArgs,
	}
	flags := accesslog.BindStats(cmd, true)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		deps := cli.GetDeps(cmd)
		f, err := flags.Collect(cmd)
		if err != nil {
			return err
		}
		resp, err := deps.PBS.Nodes.ListAccessLogStats(cmd.Context(), nf.node, f.PBSStats())
		if err != nil {
			return fmt.Errorf("read the access-log statistics of node %q: %w", nf.node, err)
		}
		if resp == nil {
			return fmt.Errorf("read the access-log statistics of node %q: empty response from server", nf.node)
		}
		res, err := accesslog.StatsResult(accesslog.StatsInput{
			Since: int64(resp.Since), Until: int64(resp.Until), InvalidLines: int64(resp.InvalidLines),
			Complete: bool(resp.Complete), Stopped: resp.Stopped, Total: resp.Total,
			Lists: []accesslog.NamedList{
				{Name: "auth-ids", Data: resp.AuthIds}, {Name: "clients", Data: resp.Clients},
				{Name: "user-agents", Data: resp.UserAgents},
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
