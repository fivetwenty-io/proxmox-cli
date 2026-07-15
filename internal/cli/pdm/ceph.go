package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	pdmceph "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/ceph"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newCephCmd builds `pmx pdm ceph` — inspect the Ceph clusters registered
// with this Proxmox Datacenter Manager's managed remotes (/ceph).
func newCephCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ceph",
		Short: "Inspect Ceph clusters registered with managed remotes",
		Long: "List the Ceph clusters this Proxmox Datacenter Manager instance can audit, and " +
			"inspect a cluster's status, summary, flags, file systems, daemons, OSD tree, and pools.",
	}
	cmd.AddCommand(
		newCephLsCmd(),
		newCephStatusCmd(),
		newCephSummaryCmd(),
		newCephFlagsCmd(),
		newCephFsCmd(),
		newCephMdsCmd(),
		newCephMgrCmd(),
		newCephMonCmd(),
		newCephOsdTreeCmd(),
		newCephPoolsCmd(),
	)
	return cmd
}

// cephClusterEntry is the subset of one element of GET /ceph/clusters
// decoded into a typed struct. The remaining, mostly-optional numeric/bool
// fields (health, member-count, osds-*, mons-*, remote, node, ...) are read
// straight off the raw map via scalarString, since every one of them is
// optional and a hand-maintained pointer field per column adds no safety
// scalarString doesn't already provide.
type cephClusterEntry struct {
	Cluster     string `json:"cluster"`
	DisplayName string `json:"display-name"`
	State       string `json:"state"`
}

// newCephLsCmd builds `pmx pdm ceph ls` — list the Ceph clusters this
// instance can audit (GET /ceph/clusters).
func newCephLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List registered Ceph clusters",
		Long:    "List the Ceph clusters this instance can audit (GET /ceph/clusters).",
		Example: `  pmx pdm ceph ls`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Ceph.ListClusters(cmd.Context())
			if err != nil {
				return fmt.Errorf("list ceph clusters: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[cephClusterEntry](items, "ceph cluster")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Cluster < table[j].Entry.Cluster })

			headers := []string{
				"CLUSTER", "DISPLAY-NAME", "STATE", "HEALTH", "MEMBER-COUNT",
				"OSDS-UP", "OSDS-TOTAL", "MONS-IN-QUORUM", "MONS-TOTAL", "REMOTE", "NODE",
			}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e, m := t.Entry, t.Raw
				rows = append(rows, []string{
					e.Cluster, e.DisplayName, e.State,
					scalarString(m["health"]), scalarString(m["member-count"]),
					scalarString(m["osds-up"]), scalarString(m["osds-total"]),
					scalarString(m["mons-in-quorum"]), scalarString(m["mons-total"]),
					scalarString(m["remote"]), scalarString(m["node"]),
				})
				raws = append(raws, m)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newCephStatusCmd builds `pmx pdm ceph status <cluster>` — show a cluster's
// raw `ceph status` object (GET /ceph/clusters/{cluster}/status).
func newCephStatusCmd() *cobra.Command {
	var maxAge int64
	cmd := &cobra.Command{
		Use:   "status <cluster>",
		Short: "Show a Ceph cluster's raw status",
		Long: "Show a cluster-wide Ceph status (the raw `ceph status` object), served from the " +
			"cache within --max-age seconds or fetched fresh and cached (GET " +
			"/ceph/clusters/{cluster}/status). The response shape is dynamic, so it is rendered " +
			"as raw JSON.",
		Example: `  pmx pdm ceph status fsid1
  pmx pdm ceph status fsid1 --max-age 30`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			params := &pdmceph.ListClustersStatusParams{}
			if cmd.Flags().Changed("max-age") {
				params.MaxAge = int64Ptr(maxAge)
			}

			resp, err := deps.PDM.Ceph.ListClustersStatus(cmd.Context(), cluster, params)
			if err != nil {
				return fmt.Errorf("get ceph cluster status %q: %w", cluster, err)
			}
			if resp == nil {
				return fmt.Errorf("get ceph cluster status %q: empty response from server", cluster)
			}

			var raw any

			err = json.Unmarshal(*resp, &raw)
			if err != nil {
				return fmt.Errorf("decode ceph cluster status %q: %w", cluster, err)
			}

			res := output.Result{Message: fmt.Sprintf("Ceph status for cluster %q.", cluster), Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().Int64Var(&maxAge, "max-age", 0, "serve a cached status if younger than this many seconds")
	return cmd
}

// newCephSummaryCmd builds `pmx pdm ceph summary <cluster>` — show a typed,
// summarized cluster status (GET /ceph/clusters/{cluster}/summary).
func newCephSummaryCmd() *cobra.Command {
	var maxAge int64
	cmd := &cobra.Command{
		Use:   "summary <cluster>",
		Short: "Show a Ceph cluster's typed status summary",
		Long: "Show a typed, summarized Ceph cluster status (health, capacity, OSD/MON/MGR/PG " +
			"counts) for the dashboard (GET /ceph/clusters/{cluster}/summary).",
		Example: `  pmx pdm ceph summary fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			params := &pdmceph.ListClustersSummaryParams{}
			if cmd.Flags().Changed("max-age") {
				params.MaxAge = int64Ptr(maxAge)
			}

			resp, err := deps.PDM.Ceph.ListClustersSummary(cmd.Context(), cluster, params)
			if err != nil {
				return fmt.Errorf("get ceph cluster summary %q: %w", cluster, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode ceph cluster summary %q: %w", cluster, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().Int64Var(&maxAge, "max-age", 0, "serve a cached status if younger than this many seconds")
	return cmd
}

// cephFlagEntry is the decoded shape of one element of GET /ceph/clusters/{cluster}/flags.
type cephFlagEntry struct {
	Name        string `json:"name"`
	Value       bool   `json:"value"`
	Description string `json:"description"`
}

// newCephFlagsCmd builds `pmx pdm ceph flags <cluster>` — show cluster-wide
// Ceph flags (GET /ceph/clusters/{cluster}/flags).
func newCephFlagsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "flags <cluster>",
		Short:   "Show a Ceph cluster's cluster-wide flags",
		Long:    "Show cluster-wide Ceph flags and their state (GET /ceph/clusters/{cluster}/flags).",
		Example: `  pmx pdm ceph flags fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			resp, err := deps.PDM.Ceph.ListClustersFlags(cmd.Context(), cluster)
			if err != nil {
				return fmt.Errorf("get ceph cluster flags %q: %w", cluster, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[cephFlagEntry](items, "ceph flag")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"NAME", "VALUE", "DESCRIPTION"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{e.Name, strconv.FormatBool(e.Value), e.Description})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// cephFsEntry is the decoded shape of one element of GET /ceph/clusters/{cluster}/fs.
type cephFsEntry struct {
	Name           string `json:"name"`
	DataPool       string `json:"data_pool"`
	MetadataPool   string `json:"metadata_pool"`
	MetadataPoolId *int64 `json:"metadata_pool_id,omitempty"`
}

// newCephFsCmd builds `pmx pdm ceph fs <cluster>` — list a cluster's CephFS
// file systems (GET /ceph/clusters/{cluster}/fs).
func newCephFsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "fs <cluster>",
		Short:   "List a Ceph cluster's CephFS file systems",
		Long:    "List the cluster's CephFS file systems (GET /ceph/clusters/{cluster}/fs).",
		Example: `  pmx pdm ceph fs fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			resp, err := deps.PDM.Ceph.ListClustersFs(cmd.Context(), cluster)
			if err != nil {
				return fmt.Errorf("list ceph cluster file systems %q: %w", cluster, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[cephFsEntry](items, "ceph fs")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"NAME", "DATA-POOL", "METADATA-POOL", "METADATA-POOL-ID"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{e.Name, e.DataPool, e.MetadataPool, int64PtrString(e.MetadataPoolId)})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// cephMdsEntry is the decoded shape of one element of GET /ceph/clusters/{cluster}/mds.
type cephMdsEntry struct {
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Host   *string `json:"host,omitempty"`
	Rank   *int64  `json:"rank,omitempty"`
	FsName *string `json:"fs_name,omitempty"`
}

// newCephMdsCmd builds `pmx pdm ceph mds <cluster>` — list a cluster's Ceph
// metadata servers (GET /ceph/clusters/{cluster}/mds).
func newCephMdsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mds <cluster>",
		Short:   "List a Ceph cluster's metadata servers",
		Long:    "List the cluster's Ceph metadata servers (MDS) (GET /ceph/clusters/{cluster}/mds).",
		Example: `  pmx pdm ceph mds fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			resp, err := deps.PDM.Ceph.ListClustersMds(cmd.Context(), cluster)
			if err != nil {
				return fmt.Errorf("list ceph cluster mds %q: %w", cluster, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[cephMdsEntry](items, "ceph mds")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"NAME", "STATE", "HOST", "RANK", "FS-NAME"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Name, e.State, strPtrString(e.Host), int64PtrString(e.Rank), strPtrString(e.FsName),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// cephMgrEntry is the decoded shape of one element of GET /ceph/clusters/{cluster}/mgr.
type cephMgrEntry struct {
	Name  string  `json:"name"`
	State string  `json:"state"`
	Host  *string `json:"host,omitempty"`
}

// newCephMgrCmd builds `pmx pdm ceph mgr <cluster>` — list a cluster's Ceph
// managers (GET /ceph/clusters/{cluster}/mgr).
func newCephMgrCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mgr <cluster>",
		Short:   "List a Ceph cluster's managers",
		Long:    "List the cluster's Ceph managers (GET /ceph/clusters/{cluster}/mgr).",
		Example: `  pmx pdm ceph mgr fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			resp, err := deps.PDM.Ceph.ListClustersMgr(cmd.Context(), cluster)
			if err != nil {
				return fmt.Errorf("list ceph cluster mgr %q: %w", cluster, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[cephMgrEntry](items, "ceph mgr")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"NAME", "STATE", "HOST"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{e.Name, e.State, strPtrString(e.Host)})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// cephMonEntry is the decoded shape of one element of GET /ceph/clusters/{cluster}/mon.
type cephMonEntry struct {
	Name   string  `json:"name"`
	State  *string `json:"state,omitempty"`
	Host   *string `json:"host,omitempty"`
	Quorum *bool   `json:"quorum,omitempty"`
	Rank   *int64  `json:"rank,omitempty"`
}

// newCephMonCmd builds `pmx pdm ceph mon <cluster>` — list a cluster's Ceph
// monitors (GET /ceph/clusters/{cluster}/mon).
func newCephMonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mon <cluster>",
		Short:   "List a Ceph cluster's monitors",
		Long:    "List the cluster's Ceph monitors (GET /ceph/clusters/{cluster}/mon).",
		Example: `  pmx pdm ceph mon fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			resp, err := deps.PDM.Ceph.ListClustersMon(cmd.Context(), cluster)
			if err != nil {
				return fmt.Errorf("list ceph cluster mon %q: %w", cluster, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[cephMonEntry](items, "ceph mon")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"NAME", "STATE", "HOST", "QUORUM", "RANK"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Name, strPtrString(e.State), strPtrString(e.Host), boolPtrString(e.Quorum), int64PtrString(e.Rank),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newCephOsdTreeCmd builds `pmx pdm ceph osd-tree <cluster>` — show a
// cluster's OSD (CRUSH) tree (GET /ceph/clusters/{cluster}/osd-tree).
func newCephOsdTreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "osd-tree <cluster>",
		Short: "Show a Ceph cluster's OSD (CRUSH) tree",
		Long: "Show the cluster's OSD (CRUSH) tree (GET /ceph/clusters/{cluster}/osd-tree). The " +
			"response shape is dynamic, so it is rendered as raw JSON.",
		Example: `  pmx pdm ceph osd-tree fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			resp, err := deps.PDM.Ceph.ListClustersOsdTree(cmd.Context(), cluster)
			if err != nil {
				return fmt.Errorf("get ceph cluster osd tree %q: %w", cluster, err)
			}
			if resp == nil {
				return fmt.Errorf("get ceph cluster osd tree %q: empty response from server", cluster)
			}

			var raw any

			err = json.Unmarshal(*resp, &raw)
			if err != nil {
				return fmt.Errorf("decode ceph cluster osd tree %q: %w", cluster, err)
			}

			res := output.Result{Message: fmt.Sprintf("OSD tree for cluster %q.", cluster), Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// cephPoolEntry is the decoded shape of one element of GET /ceph/clusters/{cluster}/pools.
type cephPoolEntry struct {
	PoolName    string   `json:"pool_name"`
	Pool        int64    `json:"pool"`
	Type        string   `json:"type"`
	Size        int64    `json:"size"`
	MinSize     int64    `json:"min_size"`
	PgNum       int64    `json:"pg_num"`
	PercentUsed *float64 `json:"percent_used,omitempty"`
	BytesUsed   *int64   `json:"bytes_used,omitempty"`
}

// newCephPoolsCmd builds `pmx pdm ceph pools <cluster>` — list a cluster's
// Ceph pools (GET /ceph/clusters/{cluster}/pools).
func newCephPoolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pools <cluster>",
		Short:   "List a Ceph cluster's pools",
		Long:    "List the cluster's Ceph pools (GET /ceph/clusters/{cluster}/pools).",
		Example: `  pmx pdm ceph pools fsid1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cluster := args[0]

			resp, err := deps.PDM.Ceph.ListClustersPools(cmd.Context(), cluster)
			if err != nil {
				return fmt.Errorf("list ceph cluster pools %q: %w", cluster, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[cephPoolEntry](items, "ceph pool")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.PoolName < table[j].Entry.PoolName })

			headers := []string{"POOL-NAME", "POOL", "TYPE", "SIZE", "MIN-SIZE", "PG-NUM", "PERCENT-USED", "BYTES-USED"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.PoolName, strconv.FormatInt(e.Pool, 10), e.Type,
					strconv.FormatInt(e.Size, 10), strconv.FormatInt(e.MinSize, 10), strconv.FormatInt(e.PgNum, 10),
					float64PtrString(e.PercentUsed), int64PtrString(e.BytesUsed),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
