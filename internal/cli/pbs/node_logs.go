package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeLogLine is one entry of a syslog-shaped response: a line number and its
// text, the shape shared by GET /nodes/{node}/syslog across Proxmox products.
type nodeLogLine struct {
	N *int64  `json:"n,omitempty"`
	T *string `json:"t,omitempty"`
}

// nodeLogLineText renders one nodeLogLine's text field, or "" when absent.
func nodeLogLineText(l nodeLogLine) string {
	if l.T == nil {
		return ""
	}
	return *l.T
}

// newNodeSyslogCmd builds `pmx pbs node syslog` — read syslog entries
// (GET /nodes/{node}/syslog).
func newNodeSyslogCmd(nf *nodeFlags) *cobra.Command {
	var (
		service      string
		since, until string
		limit, start int64
	)

	cmd := &cobra.Command{
		Use:   "syslog",
		Short: "Read the node's system log",
		Long:  "Read entries from the node's system log, optionally filtered by service or time range.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pbsnodes.ListSyslogParams{}
			if fl.Changed("service") {
				params.Service = &service
			}
			if fl.Changed("since") {
				params.Since = &since
			}
			if fl.Changed("until") {
				params.Until = &until
			}
			if fl.Changed("limit") {
				params.Limit = &limit
			}
			if fl.Changed("start") {
				params.Start = &start
			}

			resp, err := deps.PBS.Nodes.ListSyslog(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("read syslog on node %q: %w", nf.node, err)
			}

			lines, err := nodeDecodeArray[nodeLogLine](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode syslog on node %q: %w", nf.node, err)
			}

			headers := []string{"LINE"}
			rows := make([][]string, 0, len(lines))
			for _, l := range lines {
				rows = append(rows, []string{nodeLogLineText(l)})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: lines}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&service, "service", "", "limit to a single service (systemd unit)")
	f.StringVar(&since, "since", "", "show entries since this date-time string")
	f.StringVar(&until, "until", "", "show entries until this date-time string")
	f.Int64Var(&limit, "limit", 0, "maximum number of entries to return")
	f.Int64Var(&start, "start", 0, "offset of the first entry to return")

	return cmd
}

// newNodeJournalCmd builds `pmx pbs node journal` — read raw systemd journal
// lines (GET /nodes/{node}/journal).
func newNodeJournalCmd(nf *nodeFlags) *cobra.Command {
	var (
		lastentries  int64
		since, until int64
		startcursor  string
		endcursor    string
	)

	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Read the node's systemd journal",
		Long: "Read raw lines from the node's systemd journal, optionally limited to the " +
			"last N entries or bounded by a Unix-epoch time range or cursor.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pbsnodes.ListJournalParams{}
			if fl.Changed("lastentries") {
				params.Lastentries = &lastentries
			}
			if fl.Changed("since") {
				params.Since = &since
			}
			if fl.Changed("until") {
				params.Until = &until
			}
			if fl.Changed("startcursor") {
				params.Startcursor = &startcursor
			}
			if fl.Changed("endcursor") {
				params.Endcursor = &endcursor
			}

			resp, err := deps.PBS.Nodes.ListJournal(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("read journal on node %q: %w", nf.node, err)
			}

			lines := make([]string, 0)
			if resp != nil {
				lines = []string(*resp)
			}

			headers := []string{"LINE"}
			rows := make([][]string, 0, len(lines))
			for _, l := range lines {
				rows = append(rows, []string{l})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: lines}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&lastentries, "lastentries", 0, "limit to the last N entries (conflicts with a range)")
	f.Int64Var(&since, "since", 0, "show entries since this Unix epoch (conflicts with --startcursor)")
	f.Int64Var(&until, "until", 0, "show entries until this Unix epoch (conflicts with --endcursor)")
	f.StringVar(&startcursor, "startcursor", "", "start after this cursor (conflicts with --since)")
	f.StringVar(&endcursor, "endcursor", "", "end before this cursor (conflicts with --until)")

	return cmd
}
