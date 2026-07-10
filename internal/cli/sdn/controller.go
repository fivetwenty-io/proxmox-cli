package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
)

// controllerEntry is the subset of a /cluster/sdn/controllers element rendered
// in the list table; the full element is preserved in Raw.
type controllerEntry struct {
	Controller string `json:"controller"`
	Type       string `json:"type"`
}

// controllerFlags holds the editable controller attributes shared between
// create and set. Values are forwarded only when their flag is changed.
type controllerFlags struct {
	asn                     int64
	bgpMode                 string
	bgpMultipathAsPathRelax bool
	ebgp                    bool
	ebgpMultihop            int64
	fabric                  string
	isisDomain              string
	isisIfaces              string
	isisNet                 string
	loopback                string
	node                    string
	nodes                   string
	peerGroupName           string
	peers                   string
	routeMapIn              string
	routeMapOut             string
}

// controllerFlagNames lists every editable attribute flag, used by `set` to
// detect a no-op update.
var controllerFlagNames = []string{
	"asn", "bgp-mode", "bgp-multipath-as-path-relax", "ebgp", "ebgp-multihop",
	"fabric", "isis-domain", "isis-ifaces", "isis-net", "loopback", "node",
	"nodes", "peer-group-name", "peers", "route-map-in", "route-map-out",
}

// register binds the shared controller attribute flags onto cmd.
func (cf *controllerFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.Int64Var(&cf.asn, "asn", 0, "autonomous system number")
	f.StringVar(&cf.bgpMode, "bgp-mode", "", "BGP mode: ebgp or ibgp")
	f.BoolVar(&cf.bgpMultipathAsPathRelax, "bgp-multipath-as-path-relax", false,
		"consider different AS paths of equal length for multipath")
	f.BoolVar(&cf.ebgp, "ebgp", false, "enable eBGP (remote-as external)")
	f.Int64Var(&cf.ebgpMultihop, "ebgp-multihop", 0, "maximum hops for eBGP peers")
	f.StringVar(&cf.fabric, "fabric", "", "SDN fabric to use as underlay for this EVPN controller")
	f.StringVar(&cf.isisDomain, "isis-domain", "", "name of the IS-IS domain")
	f.StringVar(&cf.isisIfaces, "isis-ifaces", "", "comma-separated interfaces where IS-IS is active")
	f.StringVar(&cf.isisNet, "isis-net", "", "network entity title for this node in the IS-IS network")
	f.StringVar(&cf.loopback, "loopback", "", "loopback/dummy interface providing the router IP")
	f.StringVar(&cf.node, "node", "", "the cluster node name")
	f.StringVar(&cf.nodes, "nodes", "", "comma-separated list of cluster node names")
	f.StringVar(&cf.peerGroupName, "peer-group-name", "", "peer group name for this EVPN controller")
	f.StringVar(&cf.peers, "peers", "", "peers address list")
	f.StringVar(&cf.routeMapIn, "route-map-in", "", "route map applied to incoming routes")
	f.StringVar(&cf.routeMapOut, "route-map-out", "", "route map applied to outgoing routes")
}

// applyCreate forwards changed flags onto a create params struct.
func (cf *controllerFlags) applyCreate(fl interface{ Changed(string) bool }, p *cluster.CreateSdnControllersParams) {
	if fl.Changed("asn") {
		p.Asn = int64Ptr(cf.asn)
	}
	if fl.Changed("bgp-mode") {
		p.BgpMode = strPtr(cf.bgpMode)
	}
	if fl.Changed("bgp-multipath-as-path-relax") {
		p.BgpMultipathAsPathRelax = boolPtr(cf.bgpMultipathAsPathRelax)
	}
	if fl.Changed("ebgp") {
		p.Ebgp = boolPtr(cf.ebgp)
	}
	if fl.Changed("ebgp-multihop") {
		p.EbgpMultihop = int64Ptr(cf.ebgpMultihop)
	}
	if fl.Changed("fabric") {
		p.Fabric = strPtr(cf.fabric)
	}
	if fl.Changed("isis-domain") {
		p.IsisDomain = strPtr(cf.isisDomain)
	}
	if fl.Changed("isis-ifaces") {
		p.IsisIfaces = strPtr(cf.isisIfaces)
	}
	if fl.Changed("isis-net") {
		p.IsisNet = strPtr(cf.isisNet)
	}
	if fl.Changed("loopback") {
		p.Loopback = strPtr(cf.loopback)
	}
	if fl.Changed("node") {
		p.Node = strPtr(cf.node)
	}
	if fl.Changed("nodes") {
		p.Nodes = strPtr(cf.nodes)
	}
	if fl.Changed("peer-group-name") {
		p.PeerGroupName = strPtr(cf.peerGroupName)
	}
	if fl.Changed("peers") {
		p.Peers = strPtr(cf.peers)
	}
	if fl.Changed("route-map-in") {
		p.RouteMapIn = strPtr(cf.routeMapIn)
	}
	if fl.Changed("route-map-out") {
		p.RouteMapOut = strPtr(cf.routeMapOut)
	}
}

// applyUpdate forwards changed flags onto an update params struct.
func (cf *controllerFlags) applyUpdate(fl interface{ Changed(string) bool }, p *cluster.UpdateSdnControllersParams) {
	if fl.Changed("asn") {
		p.Asn = int64Ptr(cf.asn)
	}
	if fl.Changed("bgp-mode") {
		p.BgpMode = strPtr(cf.bgpMode)
	}
	if fl.Changed("bgp-multipath-as-path-relax") {
		p.BgpMultipathAsPathRelax = boolPtr(cf.bgpMultipathAsPathRelax)
	}
	if fl.Changed("ebgp") {
		p.Ebgp = boolPtr(cf.ebgp)
	}
	if fl.Changed("ebgp-multihop") {
		p.EbgpMultihop = int64Ptr(cf.ebgpMultihop)
	}
	if fl.Changed("fabric") {
		p.Fabric = strPtr(cf.fabric)
	}
	if fl.Changed("isis-domain") {
		p.IsisDomain = strPtr(cf.isisDomain)
	}
	if fl.Changed("isis-ifaces") {
		p.IsisIfaces = strPtr(cf.isisIfaces)
	}
	if fl.Changed("isis-net") {
		p.IsisNet = strPtr(cf.isisNet)
	}
	if fl.Changed("loopback") {
		p.Loopback = strPtr(cf.loopback)
	}
	if fl.Changed("node") {
		p.Node = strPtr(cf.node)
	}
	if fl.Changed("nodes") {
		p.Nodes = strPtr(cf.nodes)
	}
	if fl.Changed("peer-group-name") {
		p.PeerGroupName = strPtr(cf.peerGroupName)
	}
	if fl.Changed("peers") {
		p.Peers = strPtr(cf.peers)
	}
	if fl.Changed("route-map-in") {
		p.RouteMapIn = strPtr(cf.routeMapIn)
	}
	if fl.Changed("route-map-out") {
		p.RouteMapOut = strPtr(cf.routeMapOut)
	}
}

// newControllerCmd builds `pmx sdn controller` and its sub-commands.
func newControllerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Manage SDN routing controllers (BGP, EVPN, IS-IS)",
		Long: "List, create, inspect, update, and delete SDN controllers. Changes are " +
			"staged until committed with `pmx pve sdn apply`.",
	}
	cmd.AddCommand(
		newControllerListCmd(),
		newControllerCreateCmd(),
		newControllerGetCmd(),
		newControllerSetCmd(),
		newControllerDeleteCmd(),
	)
	return cmd
}

func newControllerListCmd() *cobra.Command {
	var (
		typ     string
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SDN controllers",
		Long: "List SDN controllers (BGP, EVPN, or IS-IS) with their type. Pass --type to " +
			"filter by controller type, or --pending/--running to view the staged or active " +
			"configuration instead of the merged default view.",
		Example: `  pmx pve sdn controller list
  pmx pve sdn controller list --type bgp
  pmx pve sdn controller list --pending`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnControllersParams{}
			fl := cmd.Flags()
			if fl.Changed("type") {
				params.Type = strPtr(typ)
			}
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.ListSdnControllers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN controllers: %w", err)
			}
			raws := []json.RawMessage(*resp)
			res := output.Result{Headers: []string{"CONTROLLER", "TYPE"}, Raw: raws}
			for _, raw := range raws {
				var e controllerEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode controller entry: %w", err)
				}
				res.Rows = append(res.Rows, []string{e.Controller, e.Type})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, "type", "", "only list controllers of this type")
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

func newControllerCreateCmd() *cobra.Command {
	var (
		typ       string
		cf        controllerFlags
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "create <controller> --type <type>",
		Short: "Create an SDN controller",
		Long:  "Create an SDN controller. The change is staged until `pmx pve sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			controller := args[0]
			params := &cluster.CreateSdnControllersParams{Controller: controller, Type: typ}
			fl := cmd.Flags()
			cf.applyCreate(fl, params)
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.CreateSdnControllers(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN controller %q: %w", controller, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN controller %q created (run `pmx sdn apply` to commit).", controller)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "controller type: bgp, evpn, or isis (required)")
	cli.MustMarkRequired(cmd, "type")
	cf.register(cmd)
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newControllerGetCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "get <controller>",
		Short: "Show an SDN controller's configuration",
		Long: "Show the full configuration of one SDN controller. Pass --pending or --running " +
			"to view the staged or active configuration instead of the merged default view.",
		Example: `  pmx pve sdn controller get evpn-ctl1
  pmx pve sdn controller get evpn-ctl1 --pending`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			controller := args[0]
			params := &cluster.GetSdnControllersParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.GetSdnControllers(cmd.Context(), controller, params)
			if err != nil {
				return fmt.Errorf("get SDN controller %q: %w", controller, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

func newControllerSetCmd() *cobra.Command {
	var (
		cf        controllerFlags
		del       string
		digest    string
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "set <controller>",
		Short: "Update an SDN controller",
		Long:  "Update an SDN controller. The change is staged until `pmx pve sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			controller := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, append(controllerFlagNames, "delete")...) {
				return fmt.Errorf("no changes requested: pass at least one field flag")
			}
			params := &cluster.UpdateSdnControllersParams{}
			cf.applyUpdate(fl, params)
			if fl.Changed("delete") {
				params.Delete = strPtr(del)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.UpdateSdnControllers(cmd.Context(), controller, params); err != nil {
				return fmt.Errorf("update SDN controller %q: %w", controller, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN controller %q updated (run `pmx sdn apply` to commit).", controller)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cf.register(cmd)
	cmd.Flags().StringVar(&del, "delete", "", "comma-separated list of settings to delete")
	cmd.Flags().StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newControllerDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "delete <controller>",
		Short: "Delete an SDN controller",
		Long: "Delete an SDN controller. Refuses to run without --yes. The change is staged " +
			"until `pmx pve sdn apply` commits it.",
		Example: `  pmx pve sdn controller delete evpn-ctl1 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			controller := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete SDN controller %q without confirmation: pass --yes", controller)
			}
			params := &cluster.DeleteSdnControllersParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			err := deps.API.Cluster.DeleteSdnControllers(cmd.Context(), controller, params)
			if err != nil {
				return fmt.Errorf("delete SDN controller %q: %w", controller, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN controller %q deleted (run `pmx sdn apply` to commit).", controller)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}
