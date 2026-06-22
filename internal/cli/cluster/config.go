package cluster

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// parseLinkFlags converts repeated --link "N=spec" values into the indexed
// corosync link map the API expects (link0..link7). The spec is the corosync
// link address, optionally followed by ",priority=N".
func parseLinkFlags(vals []string) (map[int]string, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	links := make(map[int]string, len(vals))
	for _, v := range vals {
		idx, spec, ok := strings.Cut(v, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --link %q: want INDEX=ADDRESS, for example 0=10.0.0.1", v)
		}
		n, err := strconv.Atoi(strings.TrimSpace(idx))
		if err != nil || n < 0 || n > 7 {
			return nil, fmt.Errorf("invalid --link index in %q: want an integer 0-7", v)
		}
		if _, dup := links[n]; dup {
			return nil, fmt.Errorf("duplicate --link index %d", n)
		}
		links[n] = spec
	}
	return links, nil
}

// newConfigCmd builds the `pve cluster config` sub-tree for corosync cluster
// membership: reading the join information a new node needs, and adding or
// removing nodes from the cluster configuration.
//
// The write operations (join, nodes add, nodes delete) change cluster
// membership and corosync quorum, so they are gated behind --yes; they are not
// exercised by the automated lab tests, which would risk breaking the cluster.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage cluster membership configuration",
		Long: "Inspect the cluster join information and manage corosync membership. " +
			"Membership changes affect quorum and are gated behind --yes.",
	}
	cmd.AddCommand(
		newConfigCreateCmd(),
		newConfigJoinCmd(),
		newConfigNodesCmd(),
		newConfigApiversionCmd(),
		newConfigQdeviceCmd(),
		newConfigTotemCmd(),
	)
	return cmd
}

// newConfigCreateCmd builds `pve cluster config create` — initial cluster
// formation on the local node (POST /cluster/config). This is a one-time,
// disruptive operation, so it is gated behind --yes.
func newConfigCreateCmd() *cobra.Command {
	var (
		yes              bool
		clustername      string
		nodeid           int64
		votes            int64
		tokenCoefficient int64
		links            []string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new cluster on this node",
		Long: "Initialize a new corosync cluster on the local node. This is a one-time " +
			"cluster-formation step; afterwards other nodes join with `cluster config join add`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to create a cluster without confirmation: pass --yes/-y")
			}
			params := &pvecluster.CreateConfigParams{Clustername: clustername}
			fl := cmd.Flags()
			if fl.Changed("nodeid") {
				params.Nodeid = &nodeid
			}
			if fl.Changed("votes") {
				params.Votes = &votes
			}
			if fl.Changed("token-coefficient") {
				params.TokenCoefficient = &tokenCoefficient
			}
			linkMap, err := parseLinkFlags(links)
			if err != nil {
				return err
			}
			params.Link = linkMap
			resp, err := deps.API.Cluster.CreateConfig(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create cluster %q: %w", clustername, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Cluster %q created.", clustername), Raw: resp}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&yes, "yes", "y", false, "confirm cluster creation without prompting")
	f.StringVar(&clustername, "clustername", "", "name of the cluster (required)")
	f.Int64Var(&nodeid, "nodeid", 0, "node ID for this node")
	f.Int64Var(&votes, "votes", 0, "number of corosync votes for this node")
	f.Int64Var(&tokenCoefficient, "token-coefficient", 0,
		"coefficient used to determine the corosync token timeout")
	f.StringArrayVar(&links, "link", nil,
		"corosync link as INDEX=ADDRESS (repeatable, up to 8: link0..link7), for example 0=10.0.0.1")
	cli.MustMarkRequired(cmd, "clustername")
	return cmd
}

// ---- join ------------------------------------------------------------------

func newConfigJoinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join",
		Short: "Inspect join information or join this node to a cluster",
	}
	cmd.AddCommand(
		newConfigJoinListCmd(),
		newConfigJoinAddCmd(),
	)
	return cmd
}

func newConfigJoinListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the information needed to join this cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListConfigJoin(cmd.Context(), &pvecluster.ListConfigJoinParams{})
			if err != nil {
				return fmt.Errorf("get cluster join information: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get cluster join information: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newConfigJoinAddCmd() *cobra.Command {
	var (
		yes         bool
		hostname    string
		fingerprint string
		password    string
		force       bool
		nodeid      int64
		votes       int64
		links       []string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Join this node to an existing cluster",
		Long: "Join the local node to an existing cluster reachable at --hostname, " +
			"authenticating with the peer's root password and verifying its certificate " +
			"fingerprint. This changes cluster membership and quorum.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to change cluster membership without confirmation: pass --yes/-y")
			}
			params := &pvecluster.CreateConfigJoinParams{
				Hostname:    hostname,
				Fingerprint: fingerprint,
				Password:    password,
			}
			fl := cmd.Flags()
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("nodeid") {
				params.Nodeid = &nodeid
			}
			if fl.Changed("votes") {
				params.Votes = &votes
			}
			linkMap, err := parseLinkFlags(links)
			if err != nil {
				return err
			}
			params.Link = linkMap
			resp, err := deps.API.Cluster.CreateConfigJoin(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("join cluster at %q: %w", hostname, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Join to %s initiated.", hostname), Raw: resp}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&yes, "yes", "y", false, "confirm the membership change without prompting")
	f.StringVar(&hostname, "hostname", "", "hostname or IP of an existing cluster member (required)")
	f.StringVar(&fingerprint, "fingerprint", "", "certificate SHA-256 fingerprint of the peer (required)")
	f.StringVar(&password, "password", "", "root password of the peer node (required)")
	f.BoolVar(&force, "force", false, "do not error if this node already exists in the cluster")
	f.Int64Var(&nodeid, "nodeid", 0, "node ID to assign to this node")
	f.Int64Var(&votes, "votes", 0, "number of corosync votes for this node")
	f.StringArrayVar(&links, "link", nil,
		"corosync link as INDEX=ADDRESS (repeatable, up to 8: link0..link7), for example 0=10.0.0.1")
	cli.MustMarkRequired(cmd, "hostname")
	cli.MustMarkRequired(cmd, "fingerprint")
	cli.MustMarkRequired(cmd, "password")
	return cmd
}

// ---- nodes -----------------------------------------------------------------

func newConfigNodesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List, add, or remove cluster members",
	}
	cmd.AddCommand(
		newConfigNodesListCmd(),
		newConfigNodesAddCmd(),
		newConfigNodesDeleteCmd(),
	)
	return cmd
}

func newConfigNodesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the corosync cluster members",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListConfigNodes(cmd.Context())
			if err != nil {
				return fmt.Errorf("list cluster nodes: %w", err)
			}
			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range *resp {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("decode cluster node: %w", err)
					}
					entries = append(entries, m)
				}
			}
			headers, rows := dynamicTable(entries)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newConfigNodesAddCmd() *cobra.Command {
	var (
		yes        bool
		newNodeIP  string
		force      bool
		nodeid     int64
		votes      int64
		apiversion int64
		links      []string
	)
	cmd := &cobra.Command{
		Use:   "add <node>",
		Short: "Add a node to the cluster configuration",
		Long: "Register a new node in the local cluster configuration and return the " +
			"corosync configuration and authkey the joining node needs. This changes " +
			"cluster membership and quorum.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			if !yes {
				return fmt.Errorf("refusing to add node %q to the cluster without confirmation: pass --yes/-y", node)
			}
			params := &pvecluster.CreateConfigNodesParams{}
			fl := cmd.Flags()
			if fl.Changed("new-node-ip") {
				params.NewNodeIp = &newNodeIP
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("nodeid") {
				params.Nodeid = &nodeid
			}
			if fl.Changed("votes") {
				params.Votes = &votes
			}
			if fl.Changed("apiversion") {
				params.Apiversion = &apiversion
			}
			linkMap, err := parseLinkFlags(links)
			if err != nil {
				return err
			}
			params.Link = linkMap
			resp, err := deps.API.Cluster.CreateConfigNodes(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("add cluster node %q: %w", node, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("add cluster node %q: %w", node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&yes, "yes", "y", false, "confirm the membership change without prompting")
	f.StringVar(&newNodeIP, "new-node-ip", "", "IP address of the node to add (fallback if no links given)")
	f.BoolVar(&force, "force", false, "do not error if the node already exists")
	f.Int64Var(&nodeid, "nodeid", 0, "node ID to assign")
	f.Int64Var(&votes, "votes", 0, "number of corosync votes for the node")
	f.Int64Var(&apiversion, "apiversion", 0, "JOIN_API_VERSION of the new node")
	f.StringArrayVar(&links, "link", nil,
		"corosync link as INDEX=ADDRESS (repeatable, up to 8: link0..link7), for example 0=10.0.0.1")
	return cmd
}

func newConfigNodesDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <node>",
		Short: "Remove a node from the cluster configuration",
		Long: "Remove a node from the corosync cluster configuration. This changes " +
			"cluster membership and quorum.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			if !yes {
				return fmt.Errorf("refusing to remove node %q from the cluster without confirmation: pass --yes/-y", node)
			}
			if err := deps.API.Cluster.DeleteConfigNodes(cmd.Context(), node); err != nil {
				return fmt.Errorf("remove cluster node %q: %w", node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Node %s removed from the cluster.", node)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the membership change without prompting")
	return cmd
}

// ---- cluster configuration introspection -----------------------------------

// newConfigApiversionCmd builds `pve cluster config apiversion`.
// It returns the cluster JOIN_API_VERSION, primarily useful for tooling that
// needs to negotiate join protocol compatibility. The API returns a scalar
// integer, so the response is rendered as a message rather than a table.
func newConfigApiversionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apiversion",
		Short: "Show the cluster join API version",
		Long: "Return the cluster JOIN_API_VERSION. Primarily useful for tooling " +
			"that needs to verify join protocol compatibility between nodes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListConfigApiversion(cmd.Context())
			if err != nil {
				return fmt.Errorf("get cluster config apiversion: %w", err)
			}
			msg := ""
			if resp != nil {
				msg = string(*resp)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg, Raw: resp}, deps.Format)
		},
	}
}

// newConfigQdeviceCmd builds `pve cluster config qdevice`.
// It returns the QDevice quorum status for the corosync cluster.
// On a cluster without a QDevice configured the API returns an error,
// which is surfaced verbatim — the command itself is correct.
func newConfigQdeviceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "qdevice",
		Short: "Show QDevice quorum status",
		Long: "Show the QDevice quorum device status for the corosync cluster. " +
			"Returns an error on clusters without a configured QDevice.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListConfigQdevice(cmd.Context())
			if err != nil {
				return fmt.Errorf("get cluster config qdevice: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode cluster config qdevice: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// newConfigTotemCmd builds `pve cluster config totem`.
// It returns the corosync totem settings, which govern ring transport, token
// timeouts, and consensus parameters — useful for cluster health diagnosis.
func newConfigTotemCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "totem",
		Short: "Show corosync totem settings",
		Long: "Show the corosync totem configuration: ring transport, token timeouts, " +
			"and consensus parameters. Useful for cluster health diagnosis.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListConfigTotem(cmd.Context())
			if err != nil {
				return fmt.Errorf("get cluster config totem: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode cluster config totem: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}
