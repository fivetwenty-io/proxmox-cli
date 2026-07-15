package pdm

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeLogLine is one entry of a syslog-shaped response: a line number and its
// text, the shape GET /nodes/{node}/syslog and (per its item schema) GET
// /nodes/{node}/tasks/{upid}/log share across Proxmox products.
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

// validNodeRrdTimeframes are the RRD time-frame enum values accepted by GET
// /nodes/{node}/rrddata, per the PDM API schema.
var validNodeRrdTimeframes = []string{"hour", "day", "week", "month", "year", "decade"}

// validNodeRrdConsolidations are the RRD consolidation-function enum values
// accepted by GET /nodes/{node}/rrddata, per the PDM API schema.
var validNodeRrdConsolidations = []string{"MAX", "AVERAGE"}

// newNodeJournalCmd builds `pmx pdm node journal <node>` — read raw systemd
// journal lines (GET /nodes/{node}/journal), matching the PBS analog
// (internal/cli/pbs/node_logs.go's newNodeJournalCmd).
func newNodeJournalCmd() *cobra.Command {
	var (
		lastentries  int64
		since, until int64
		startcursor  string
		endcursor    string
	)

	cmd := &cobra.Command{
		Use:   "journal <node>",
		Short: "Read the node's systemd journal",
		Long: "Read raw lines from the node's systemd journal, optionally limited to the " +
			"last N entries or bounded by a Unix-epoch time range or cursor.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			fl := cmd.Flags()

			params := &pdmnodes.ListJournalParams{}
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

			resp, err := deps.PDM.Nodes.ListJournal(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("read journal on node %q: %w", node, err)
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

// newNodeSyslogCmd builds `pmx pdm node syslog <node>` — read syslog
// entries (GET /nodes/{node}/syslog).
func newNodeSyslogCmd() *cobra.Command {
	var (
		service      string
		since, until string
		limit, start int64
	)

	cmd := &cobra.Command{
		Use:   "syslog <node>",
		Short: "Read the node's system log",
		Long:  "Read entries from the node's system log, optionally filtered by service or time range.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			fl := cmd.Flags()

			params := &pdmnodes.ListSyslogParams{}
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

			resp, err := deps.PDM.Nodes.ListSyslog(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("read syslog on node %q: %w", node, err)
			}

			lines, err := nodeDecodeArray[nodeLogLine](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode syslog on node %q: %w", node, err)
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

// newNodeReportCmd builds `pmx pdm node report <node>` — generate a full
// system report for the node (GET /nodes/{node}/report), rendered as plain
// text.
func newNodeReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report <node>",
		Short: "Generate a full system report for the node",
		Long: "Generate a diagnostic report covering system, network, and storage " +
			"configuration for the node.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListReport(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("generate report for node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("generate report for node %q: empty response from server", node)
			}

			text, err := nodeDecodeText(*resp)
			if err != nil {
				return fmt.Errorf("decode report for node %q: %w", node, err)
			}

			res := output.Result{Message: text, Raw: text}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// nodeRrdEntry is a table-relevant subset of the JSON object PDM returns for
// each element of GET /nodes/{node}/rrddata; every field the schema declares
// (24 total) is still preserved losslessly in Raw via decodeRawList, this
// struct only backs the table columns.
type nodeRrdEntry struct {
	Time       int64    `json:"time"`
	CpuCurrent *float64 `json:"cpu-current,omitempty"`
	MemUsed    *float64 `json:"mem-used,omitempty"`
	MemTotal   *float64 `json:"mem-total,omitempty"`
	DiskUsed   *float64 `json:"disk-used,omitempty"`
	NetIn      *float64 `json:"net-in,omitempty"`
	NetOut     *float64 `json:"net-out,omitempty"`
}

// newNodeRrddataCmd builds `pmx pdm node rrddata <node>` — read RRD data
// points for the node (GET /nodes/{node}/rrddata).
func newNodeRrddataCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)

	cmd := &cobra.Command{
		Use:   "rrddata <node>",
		Short: "Read RRD usage statistics for the node",
		Long: "Read RRD (round-robin database) usage statistics for the node over the " +
			"given time frame and consolidation function.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			if !stringInSlice(timeframe, validNodeRrdTimeframes) {
				return fmt.Errorf("get rrddata for node %q: --timeframe must be one of %s (got %q)",
					node, strings.Join(validNodeRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validNodeRrdConsolidations) {
				return fmt.Errorf("get rrddata for node %q: --cf must be one of %s (got %q)",
					node, strings.Join(validNodeRrdConsolidations, ", "), cf)
			}

			params := &pdmnodes.ListRrddataParams{Cf: cf, Timeframe: timeframe}

			resp, err := deps.PDM.Nodes.ListRrddata(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("get rrddata for node %q: %w", node, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[nodeRrdEntry](items)
			if err != nil {
				return fmt.Errorf("decode rrd datapoint for node %q: %w", node, err)
			}

			headers := []string{"TIME", "CPU-CURRENT", "MEM-USED", "MEM-TOTAL", "DISK-USED", "NET-IN", "NET-OUT"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					int64PtrString(&e.Time), float64PtrString(e.CpuCurrent), float64PtrString(e.MemUsed),
					float64PtrString(e.MemTotal), float64PtrString(e.DiskUsed), float64PtrString(e.NetIn),
					float64PtrString(e.NetOut),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade (required)")
	fl.StringVar(&cf, "cf", "AVERAGE", "RRD consolidation function: MAX|AVERAGE")
	cli.MustMarkRequired(cmd, "timeframe")

	return cmd
}
