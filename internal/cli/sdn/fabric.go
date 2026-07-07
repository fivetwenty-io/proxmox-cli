package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// rawStringList converts plain string values into a list of JSON-encoded raw
// messages, the shape the fabric `delete` parameter expects (a list of property
// names to clear).
func rawStringList(vals []string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(vals))
	for _, v := range vals {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out = append(out, json.RawMessage(b))
	}
	return out
}

// fabricFlags holds the editable fabric attributes shared between create and
// set. Values are forwarded only when their flag is changed.
type fabricFlags struct {
	area                string
	ipPrefix            string
	ip6Prefix           string
	helloInterval       float64
	csnpInterval        float64
	persistentKeepalive float64
	routeFilter         string
	lockToken           string
	redistVals          []string
}

func (ff *fabricFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&ff.area, "area", "", "OSPF area: an IPv4 address or a 32-bit number")
	f.StringVar(&ff.ipPrefix, "ip-prefix", "", "IPv4 prefix for node IPs")
	f.StringVar(&ff.ip6Prefix, "ip6-prefix", "", "IPv6 prefix for node IPs")
	f.Float64Var(&ff.helloInterval, "hello-interval", 0, "OpenFabric hello interval (seconds)")
	f.Float64Var(&ff.csnpInterval, "csnp-interval", 0, "OpenFabric CSNP interval (seconds)")
	f.Float64Var(&ff.persistentKeepalive, "persistent-keepalive", 0,
		"WireGuard persistent keepalive interval (1-65535 seconds, 0 disables)")
	f.StringVar(&ff.routeFilter, "route-filter", "", "prefix list used to filter routes installed into the kernel")
	f.StringVar(&ff.lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	f.StringArrayVar(&ff.redistVals, "redistribute", nil,
		"routing protocol to redistribute into this fabric (repeatable, e.g. --redistribute connected --redistribute static)")
}

func (ff *fabricFlags) applyCreate(fl *cobra.Command, p *cluster.CreateSdnFabricsFabricParams) {
	f := fl.Flags()
	if f.Changed("area") {
		p.Area = strPtr(ff.area)
	}
	if f.Changed("ip-prefix") {
		p.IpPrefix = strPtr(ff.ipPrefix)
	}
	if f.Changed("ip6-prefix") {
		p.Ip6Prefix = strPtr(ff.ip6Prefix)
	}
	if f.Changed("hello-interval") {
		p.HelloInterval = &ff.helloInterval
	}
	if f.Changed("csnp-interval") {
		p.CsnpInterval = &ff.csnpInterval
	}
	if f.Changed("persistent-keepalive") {
		p.PersistentKeepalive = &ff.persistentKeepalive
	}
	if f.Changed("route-filter") {
		p.RouteFilter = strPtr(ff.routeFilter)
	}
	if f.Changed("lock-token") {
		p.LockToken = strPtr(ff.lockToken)
	}
	if f.Changed("redistribute") {
		p.Redistribute = rawStringList(ff.redistVals)
	}
}

func (ff *fabricFlags) applyUpdate(fl *cobra.Command, p *cluster.UpdateSdnFabricsFabricParams) {
	f := fl.Flags()
	if f.Changed("area") {
		p.Area = strPtr(ff.area)
	}
	if f.Changed("ip-prefix") {
		p.IpPrefix = strPtr(ff.ipPrefix)
	}
	if f.Changed("ip6-prefix") {
		p.Ip6Prefix = strPtr(ff.ip6Prefix)
	}
	if f.Changed("hello-interval") {
		p.HelloInterval = &ff.helloInterval
	}
	if f.Changed("csnp-interval") {
		p.CsnpInterval = &ff.csnpInterval
	}
	if f.Changed("persistent-keepalive") {
		p.PersistentKeepalive = &ff.persistentKeepalive
	}
	if f.Changed("route-filter") {
		p.RouteFilter = strPtr(ff.routeFilter)
	}
	if f.Changed("lock-token") {
		p.LockToken = strPtr(ff.lockToken)
	}
	if f.Changed("redistribute") {
		p.Redistribute = rawStringList(ff.redistVals)
	}
}

// newFabricCmd builds `pve sdn fabric` and its sub-commands.
func newFabricCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fabric",
		Short: "Manage SDN fabrics (BGP/OpenFabric/OSPF routing underlays)",
		Long: "List, create, inspect, update, and delete SDN fabrics and their member " +
			"nodes. Changes are staged until committed with `pve sdn apply`.",
	}
	cmd.AddCommand(
		newFabricListCmd(),
		newFabricListAllCmd(),
		newFabricCreateCmd(),
		newFabricGetCmd(),
		newFabricSetCmd(),
		newFabricDeleteCmd(),
		newFabricNodeCmd(),
	)
	return cmd
}

// newFabricListAllCmd builds `pve sdn fabric list-all`.
func newFabricListAllCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "list-all",
		Short: "List all SDN fabrics across nodes",
		Long: "List all SDN fabrics across all cluster nodes. Returns both fabric " +
			"definitions and their member nodes in a single request.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnFabricsAllParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.ListSdnFabricsAll(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list all SDN fabrics: %w", err)
			}
			// Combine fabrics + nodes into a single flat list for the table view.
			all := make([]json.RawMessage, 0, len(resp.Fabrics)+len(resp.Nodes))
			all = append(all, resp.Fabrics...)
			all = append(all, resp.Nodes...)
			return renderRawList(cmd, deps, all)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

func newFabricListCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SDN fabrics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			// GET /cluster/sdn/fabrics is only a directory index (fabric, node,
			// all); the fabric definitions live at /cluster/sdn/fabrics/fabric.
			params := &cluster.ListSdnFabricsFabricParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.ListSdnFabricsFabric(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN fabrics: %w", err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

func newFabricCreateCmd() *cobra.Command {
	var (
		protocol string
		ff       fabricFlags
	)
	cmd := &cobra.Command{
		Use:   "create <id> --protocol <protocol>",
		Short: "Create an SDN fabric",
		Long:  "Create an SDN fabric. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := &cluster.CreateSdnFabricsFabricParams{Id: id, Protocol: protocol}
			ff.applyCreate(cmd, params)
			if err := deps.API.Cluster.CreateSdnFabricsFabric(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN fabric %q: %w", id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN fabric %q created (run `pve sdn apply` to commit).", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&protocol, "protocol", "", "fabric protocol: openfabric, ospf, or evpn (required)")
	cli.MustMarkRequired(cmd, "protocol")
	ff.register(cmd)
	return cmd
}

func newFabricGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show an SDN fabric's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.GetSdnFabricsFabric(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get SDN fabric %q: %w", id, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	return cmd
}

func newFabricSetCmd() *cobra.Command {
	var (
		protocol string
		ff       fabricFlags
		del      []string
		digest   string
	)
	cmd := &cobra.Command{
		Use:   "set <id> --protocol <protocol>",
		Short: "Update an SDN fabric",
		Long:  "Update an SDN fabric. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			params := &cluster.UpdateSdnFabricsFabricParams{Protocol: protocol}
			ff.applyUpdate(cmd, params)
			if fl.Changed("delete") {
				params.Delete = rawStringList(del)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if err := deps.API.Cluster.UpdateSdnFabricsFabric(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update SDN fabric %q: %w", id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN fabric %q updated (run `pve sdn apply` to commit).", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&protocol, "protocol", "", "fabric protocol: openfabric, ospf, or evpn (required)")
	cli.MustMarkRequired(cmd, "protocol")
	ff.register(cmd)
	cmd.Flags().StringArrayVar(&del, "delete", nil, "property to clear (repeatable)")
	cmd.Flags().StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	return cmd
}

func newFabricDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an SDN fabric",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete SDN fabric %q without confirmation: pass --yes", id)
			}
			if err := deps.API.Cluster.DeleteSdnFabricsFabric(cmd.Context(), id); err != nil {
				return fmt.Errorf("delete SDN fabric %q: %w", id, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN fabric %q deleted (run `pve sdn apply` to commit).", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// nodeFlags holds the editable fabric-node attributes shared between create and
// set. Values are forwarded only when their flag is changed.
type nodeFlags struct {
	ip         string
	ip6        string
	endpoint   string
	publicKey  string
	role       string
	allowedIps []string
	peers      []string
	lockToken  string
	ifaceVals  []string
}

func (nf *nodeFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&nf.ip, "ip", "", "IPv4 address for this node")
	f.StringVar(&nf.ip6, "ip6", "", "IPv6 address for this node")
	f.StringVar(&nf.endpoint, "endpoint", "", "endpoint used for connecting to this node")
	f.StringVar(&nf.publicKey, "public-key", "", "public key for an external node")
	f.StringVar(&nf.role, "role", "", "role of this node in the WireGuard fabric")
	f.StringArrayVar(&nf.allowedIps, "allowed-ip", nil, "IP routable via this node (repeatable)")
	f.StringArrayVar(&nf.peers, "peer", nil, "peer of this node (repeatable)")
	f.StringVar(&nf.lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	f.StringArrayVar(&nf.ifaceVals, "interface", nil,
		"interface property-string for this node (repeatable, maps to API `interfaces`)")
}

func (nf *nodeFlags) applyCreate(fl *cobra.Command, p *cluster.CreateSdnFabricsNodeParams) {
	f := fl.Flags()
	if f.Changed("ip") {
		p.Ip = strPtr(nf.ip)
	}
	if f.Changed("ip6") {
		p.Ip6 = strPtr(nf.ip6)
	}
	if f.Changed("endpoint") {
		p.Endpoint = strPtr(nf.endpoint)
	}
	if f.Changed("public-key") {
		p.PublicKey = strPtr(nf.publicKey)
	}
	if f.Changed("role") {
		p.Role = strPtr(nf.role)
	}
	if f.Changed("allowed-ip") {
		p.AllowedIps = nf.allowedIps
	}
	if f.Changed("peer") {
		p.Peers = nf.peers
	}
	if f.Changed("lock-token") {
		p.LockToken = strPtr(nf.lockToken)
	}
	if f.Changed("interface") {
		p.Interfaces = rawStringList(nf.ifaceVals)
	}
}

func (nf *nodeFlags) applyUpdate(fl *cobra.Command, p *cluster.UpdateSdnFabricsNodeParams) {
	f := fl.Flags()
	if f.Changed("ip") {
		p.Ip = strPtr(nf.ip)
	}
	if f.Changed("ip6") {
		p.Ip6 = strPtr(nf.ip6)
	}
	if f.Changed("endpoint") {
		p.Endpoint = strPtr(nf.endpoint)
	}
	if f.Changed("public-key") {
		p.PublicKey = strPtr(nf.publicKey)
	}
	if f.Changed("role") {
		p.Role = strPtr(nf.role)
	}
	if f.Changed("allowed-ip") {
		p.AllowedIps = nf.allowedIps
	}
	if f.Changed("peer") {
		p.Peers = nf.peers
	}
	if f.Changed("lock-token") {
		p.LockToken = strPtr(nf.lockToken)
	}
	if f.Changed("interface") {
		p.Interfaces = rawStringList(nf.ifaceVals)
	}
}

// newFabricNodeCmd builds `pve sdn fabric node` and its sub-commands.
func newFabricNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage member nodes of SDN fabrics",
		Long: "List, create, inspect, update, and delete the nodes that participate in " +
			"SDN fabrics. Changes are staged until committed with `pve sdn apply`.",
	}
	cmd.AddCommand(
		newFabricNodeListCmd(),
		newFabricNodeGetCmd(),
		newFabricNodeCreateCmd(),
		newFabricNodeSetCmd(),
		newFabricNodeDeleteCmd(),
	)
	return cmd
}

func newFabricNodeListCmd() *cobra.Command {
	var (
		fabric  string
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "list [--fabric <id>]",
		Short: "List fabric nodes (across all fabrics, or within one fabric)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			var raws []json.RawMessage
			if fl.Changed("fabric") {
				params := &cluster.GetSdnFabricsNodeParams{}
				if fl.Changed("pending") {
					params.Pending = boolPtr(pending)
				}
				if fl.Changed("running") {
					params.Running = boolPtr(running)
				}
				resp, err := deps.API.Cluster.GetSdnFabricsNode(cmd.Context(), fabric, params)
				if err != nil {
					return fmt.Errorf("list nodes of SDN fabric %q: %w", fabric, err)
				}
				raws = []json.RawMessage(*resp)
			} else {
				params := &cluster.ListSdnFabricsNodeParams{}
				if fl.Changed("pending") {
					params.Pending = boolPtr(pending)
				}
				if fl.Changed("running") {
					params.Running = boolPtr(running)
				}
				resp, err := deps.API.Cluster.ListSdnFabricsNode(cmd.Context(), params)
				if err != nil {
					return fmt.Errorf("list SDN fabric nodes: %w", err)
				}
				raws = []json.RawMessage(*resp)
			}
			return renderRawList(cmd, deps, raws)
		},
	}
	f := cmd.Flags()
	f.StringVar(&fabric, "fabric", "", "only list nodes belonging to this fabric")
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

func newFabricNodeGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <fabric> <node>",
		Short: "Show a fabric node's configuration",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fabric, node := args[0], args[1]
			resp, err := deps.API.Cluster.GetSdnFabricsNode2(cmd.Context(), fabric, node)
			if err != nil {
				return fmt.Errorf("get node %q of SDN fabric %q: %w", node, fabric, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	return cmd
}

func newFabricNodeCreateCmd() *cobra.Command {
	var (
		protocol string
		nf       nodeFlags
	)
	cmd := &cobra.Command{
		Use:   "create <fabric> <node> --protocol <protocol>",
		Short: "Add a node to an SDN fabric",
		Long:  "Add a node to an SDN fabric. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fabric, node := args[0], args[1]
			params := &cluster.CreateSdnFabricsNodeParams{NodeId: node, Protocol: protocol}
			nf.applyCreate(cmd, params)
			if err := deps.API.Cluster.CreateSdnFabricsNode(cmd.Context(), fabric, params); err != nil {
				return fmt.Errorf("add node %q to SDN fabric %q: %w", node, fabric, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Node %q added to SDN fabric %q (run `pve sdn apply` to commit).", node, fabric)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&protocol, "protocol", "", "fabric protocol: openfabric, ospf, or evpn (required)")
	cli.MustMarkRequired(cmd, "protocol")
	nf.register(cmd)
	return cmd
}

func newFabricNodeSetCmd() *cobra.Command {
	var (
		protocol string
		nf       nodeFlags
		del      []string
		digest   string
	)
	cmd := &cobra.Command{
		Use:   "set <fabric> <node> --protocol <protocol>",
		Short: "Update a node in an SDN fabric",
		Long:  "Update a node in an SDN fabric. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fabric, node := args[0], args[1]
			fl := cmd.Flags()
			params := &cluster.UpdateSdnFabricsNodeParams{Protocol: protocol}
			nf.applyUpdate(cmd, params)
			if fl.Changed("delete") {
				params.Delete = rawStringList(del)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if err := deps.API.Cluster.UpdateSdnFabricsNode(cmd.Context(), fabric, node, params); err != nil {
				return fmt.Errorf("update node %q of SDN fabric %q: %w", node, fabric, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Node %q of SDN fabric %q updated (run `pve sdn apply` to commit).", node, fabric)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&protocol, "protocol", "", "fabric protocol: openfabric, ospf, or evpn (required)")
	cli.MustMarkRequired(cmd, "protocol")
	nf.register(cmd)
	cmd.Flags().StringArrayVar(&del, "delete", nil, "property to clear (repeatable)")
	cmd.Flags().StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	return cmd
}

func newFabricNodeDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <fabric> <node>",
		Short: "Remove a node from an SDN fabric",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fabric, node := args[0], args[1]
			if !yes {
				return fmt.Errorf(
					"refusing to remove node %q from SDN fabric %q without confirmation: pass --yes", node, fabric)
			}
			if err := deps.API.Cluster.DeleteSdnFabricsNode(cmd.Context(), fabric, node); err != nil {
				return fmt.Errorf("remove node %q from SDN fabric %q: %w", node, fabric, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Node %q removed from SDN fabric %q (run `pve sdn apply` to commit).", node, fabric)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm removal without prompting")
	return cmd
}
