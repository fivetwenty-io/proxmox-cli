package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// labZoneName is the single Proxmox VE SDN zone shared by every nested lab: a VXLAN zone named
// "labsvxlan" that hosts one vnet per lab, each tagged with its own VXLAN
// VNI. It is not per-lab configuration; every `pmx lab net apply` invocation
// reconciles against this same zone name regardless of which lab is named.
const labZoneName = "labsvxlan"

// labZoneType is the Proxmox VE SDN zone plugin type used for labZoneName.
const labZoneType = "vxlan"

// labZonePeers is the VXLAN zone's peer list: sm-0's underlay address. The
// platform runs a single physical node today, so the zone's only peer is
// sm-0 itself.
const labZonePeers = "192.168.1.180"

// newNetCmd builds `pmx lab net` and its subcommands.
func newNetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "net",
		Short: "Manage a lab's SDN network",
		Long: "Reconcile a lab's SDN zone, vnet, and subnet against its config, " +
			"preview the resulting pending changeset, and apply it.",
	}
	cmd.AddCommand(newNetApplyCmd())
	return cmd
}

// newNetApplyCmd builds `pmx lab net apply <name>`.
//
// Every run resolves the lab (peppi-guarded via resolveLabForMutate), then,
// unless --dry-run is set, idempotently ensures the lab's SDN zone, vnet, and
// subnet exist and match its config — querying live state first and issuing
// a create or update only for what is missing or has drifted, keeping apply idempotent. It
// then ALWAYS calls ListSdnDryRun and renders the pending-changes preview,
// on every invocation, not only under --dry-run. --dry-run stops
// immediately after that preview: no zone/vnet/subnet create or update runs
// beforehand, and UpdateSdn never runs, so the preview in that mode reflects
// only whatever changeset was already staged before this command ran, not
// what this lab's reconciliation would stage. Without --dry-run, once the
// preview is shown, an empty changeset (no FRR or interfaces diff) skips
// UpdateSdn entirely as a no-op; a non-empty changeset is applied via
// UpdateSdn and awaited via WaitTask.
func newNetApplyCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "apply <name>",
		Short: "Reconcile and apply a lab's SDN configuration",
		Long: "Idempotently ensure the named lab's SDN zone, vnet, and subnet match " +
			"its config, then always preview the pending SDN changeset with " +
			"`ListSdnDryRun` before committing. --dry-run stops after that preview " +
			"without ensuring any resource or calling apply. Without --dry-run, an " +
			"empty pending changeset is reported and skipped; a non-empty one is " +
			"applied and awaited.",
		Example: `  pmx lab net apply wayne
  pmx lab net apply wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			lab, err := resolveLabForMutate(cmd, name)
			if err != nil {
				return err
			}

			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}

			ctx := cmd.Context()

			if !dryRun {
				if err := ensureLabSdnZone(ctx, deps.API, deps.Node, int64(lab.Network.MTU)); err != nil {
					return err
				}
				if err := ensureLabSdnVnet(ctx, deps.API, lab.Network); err != nil {
					return err
				}
				if err := ensureLabSdnSubnet(ctx, deps.API, lab.Network); err != nil {
					return err
				}
			}

			preview, err := deps.API.Cluster.ListSdnDryRun(ctx, &cluster.ListSdnDryRunParams{Node: deps.Node})
			if err != nil {
				return fmt.Errorf("preview SDN configuration on node %q: %w", deps.Node, err)
			}
			if err := renderSdnPreview(cmd, deps, preview); err != nil {
				return err
			}

			if dryRun {
				return nil
			}

			if !sdnPreviewHasChanges(preview) {
				res := output.Result{Message: "No pending SDN configuration changes; nothing to apply."}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			return applySdn(ctx, cmd, deps)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"preview the pending SDN changeset without ensuring resources or applying")
	return cmd
}

// applySdn commits the pending SDN configuration via UpdateSdn and waits for
// the reload task, mirroring `pmx sdn apply`'s immediate-vs-async response
// handling (a UPID is awaited; an older server's null/empty response is
// treated as an immediate success). net apply never runs in --async mode, so
// this always blocks until the reload task completes.
func applySdn(ctx context.Context, cmd *cobra.Command, deps *cli.Deps) error {
	resp, err := deps.API.Cluster.UpdateSdn(ctx, &cluster.UpdateSdnParams{})
	if err != nil {
		return fmt.Errorf("apply SDN configuration: %w", err)
	}

	var raw json.RawMessage
	if resp != nil {
		raw = *resp
	}
	upid, perr := apiclient.UPIDFromRaw(raw)
	if perr != nil || upid == "" {
		res := output.Result{Message: "SDN configuration applied."}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	if err := apiclient.WaitTask(ctx, deps.API, upid, nil); err != nil {
		return err
	}
	res := output.Result{Message: "SDN configuration applied."}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// renderSdnPreview renders a ListSdnDryRun response's FRR and
// /etc/network/interfaces.d/sdn diffs. Both fields are optional in the PVE
// response; a nil preview or nil field renders as an empty string rather
// than erroring, since an empty diff is itself meaningful (no pending
// change for that config surface).
func renderSdnPreview(cmd *cobra.Command, deps *cli.Deps, preview *cluster.ListSdnDryRunResponse) error {
	var frrDiff, ifacesDiff string
	if preview != nil {
		if preview.FrrDiff != nil {
			frrDiff = *preview.FrrDiff
		}
		if preview.InterfacesDiff != nil {
			ifacesDiff = *preview.InterfacesDiff
		}
	}
	res := output.Result{
		Single: map[string]string{
			"frr-diff":        frrDiff,
			"interfaces-diff": ifacesDiff,
		},
		Raw: preview,
	}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// sdnPreviewHasChanges reports whether a ListSdnDryRun response describes any
// pending change: either diff field is non-nil and non-blank once trimmed.
func sdnPreviewHasChanges(preview *cluster.ListSdnDryRunResponse) bool {
	if preview == nil {
		return false
	}
	if preview.FrrDiff != nil && strings.TrimSpace(*preview.FrrDiff) != "" {
		return true
	}
	if preview.InterfacesDiff != nil && strings.TrimSpace(*preview.InterfacesDiff) != "" {
		return true
	}
	return false
}

// sdnZoneState is the subset of a /cluster/sdn/zones element net apply reads
// to decide whether labZoneName exists and matches expectations.
type sdnZoneState struct {
	Zone  string `json:"zone"`
	Type  string `json:"type"`
	Mtu   int64  `json:"mtu"`
	Nodes string `json:"nodes"`
	Peers string `json:"peers"`
}

// sdnVnetState is the subset of a /cluster/sdn/vnets element net apply reads
// to decide whether a lab's vnet exists and matches expectations.
type sdnVnetState struct {
	Vnet  string `json:"vnet"`
	Zone  string `json:"zone"`
	Tag   int64  `json:"tag"`
	Alias string `json:"alias"`
}

// sdnSubnetState is the subset of a /cluster/sdn/vnets/{vnet}/subnets
// element net apply reads to decide whether a lab's mgmt subnet exists and
// matches expectations. Subnet is the PVE-assigned subnet identifier (e.g.
// "wayne-10.108.0.0-24"), distinct from Cidr, which is the plain CIDR string
// from the lab config; update/delete calls must address the subnet by
// Subnet, not by Cidr.
type sdnSubnetState struct {
	Subnet  string `json:"subnet"`
	Cidr    string `json:"cidr"`
	Gateway string `json:"gateway"`
	Zone    string `json:"zone"`
}

// findSdnZone decodes each entry of list and returns the one whose Zone
// field equals zone. found is false, with a nil error, if no entry matches.
func findSdnZone(list cluster.ListSdnZonesResponse, zone string) (sdnZoneState, bool, error) {
	for _, raw := range list {
		var z sdnZoneState
		if err := json.Unmarshal(raw, &z); err != nil {
			return sdnZoneState{}, false, fmt.Errorf("decode SDN zone entry: %w", err)
		}
		if z.Zone == zone {
			return z, true, nil
		}
	}
	return sdnZoneState{}, false, nil
}

// findSdnVnet decodes each entry of list and returns the one whose Vnet
// field equals vnet. found is false, with a nil error, if no entry matches.
func findSdnVnet(list cluster.ListSdnVnetsResponse, vnet string) (sdnVnetState, bool, error) {
	for _, raw := range list {
		var v sdnVnetState
		if err := json.Unmarshal(raw, &v); err != nil {
			return sdnVnetState{}, false, fmt.Errorf("decode SDN vnet entry: %w", err)
		}
		if v.Vnet == vnet {
			return v, true, nil
		}
	}
	return sdnVnetState{}, false, nil
}

// findSdnSubnet decodes each entry of list and returns the one whose Cidr
// field equals cidr. found is false, with a nil error, if no entry matches.
// Subnets are matched by CIDR rather than by the PVE-assigned subnet
// identifier, since the lab config only ever states the CIDR.
func findSdnSubnet(list cluster.ListSdnVnetsSubnetsResponse, cidr string) (sdnSubnetState, bool, error) {
	for _, raw := range list {
		var s sdnSubnetState
		if err := json.Unmarshal(raw, &s); err != nil {
			return sdnSubnetState{}, false, fmt.Errorf("decode SDN subnet entry: %w", err)
		}
		if s.Cidr == cidr {
			return s, true, nil
		}
	}
	return sdnSubnetState{}, false, nil
}

// labZoneCreateParams builds the CreateSdnZonesParams that provisions
// labZoneName as a VXLAN zone peered on labZonePeers and scoped to node, at
// the given MTU (0 leaves MTU unset). This is the single source of truth
// for the shared labsvxlan zone's create spec: both ensureLabSdnZone (`pmx
// lab net apply`) and `pmx lab create` build the zone's CreateSdnZones call
// through this function, so the two verbs can never provision the zone with
// diverging Peers, Nodes, or MTU values.
func labZoneCreateParams(node string, mtu int64) *cluster.CreateSdnZonesParams {
	params := &cluster.CreateSdnZonesParams{
		Zone:  labZoneName,
		Type:  labZoneType,
		Peers: netPtr(labZonePeers),
		Nodes: netPtr(node),
	}
	if mtu > 0 {
		params.Mtu = netPtr(mtu)
	}
	return params
}

// ensureLabSdnZone ensures labZoneName exists as a VXLAN zone peered on
// labZonePeers, scoped to node, at the given MTU (0 leaves MTU unmanaged,
// e.g. when the lab config carries no explicit value). It creates the zone
// when absent; when present, it updates only the fields that have drifted
// (MTU, peers, or node membership), issuing no request at all when nothing
// has changed.
func ensureLabSdnZone(ctx context.Context, api *apiclient.APIClient, node string, mtu int64) error {
	list, err := api.Cluster.ListSdnZones(ctx, nil)
	if err != nil {
		return fmt.Errorf("list SDN zones: %w", err)
	}

	existing, found, err := findSdnZone(*list, labZoneName)
	if err != nil {
		return err
	}

	if !found {
		if err := api.Cluster.CreateSdnZones(ctx, labZoneCreateParams(node, mtu)); err != nil {
			return fmt.Errorf("create SDN zone %q: %w", labZoneName, err)
		}
		return nil
	}

	params := &cluster.UpdateSdnZonesParams{}
	changed := false

	if mtu > 0 && existing.Mtu != mtu {
		params.Mtu = netPtr(mtu)
		changed = true
	}
	if existing.Peers != labZonePeers {
		params.Peers = netPtr(labZonePeers)
		changed = true
	}
	if node != "" && !nodeListContains(existing.Nodes, node) {
		params.Nodes = netPtr(nodeListAdd(existing.Nodes, node))
		changed = true
	}
	if !changed {
		return nil
	}

	if err := api.Cluster.UpdateSdnZones(ctx, labZoneName, params); err != nil {
		return fmt.Errorf("update SDN zone %q: %w", labZoneName, err)
	}
	return nil
}

// ensureLabSdnVnet ensures net.VnetID exists as a vnet in labZoneName with
// the tag and alias from net. It creates the vnet when absent; when present,
// it updates only fields that have drifted, issuing no request when nothing
// has changed. An empty net.VnetID is a caller/config error, not silently
// skipped, since every lab must name a vnet.
func ensureLabSdnVnet(ctx context.Context, api *apiclient.APIClient, net config.LabNetwork) error {
	if net.VnetID == "" {
		return fmt.Errorf("lab network vnet_id is empty; cannot ensure an SDN vnet")
	}

	list, err := api.Cluster.ListSdnVnets(ctx, nil)
	if err != nil {
		return fmt.Errorf("list SDN vnets: %w", err)
	}

	existing, found, err := findSdnVnet(*list, net.VnetID)
	if err != nil {
		return err
	}

	tag := int64(net.VxlanTag)

	if !found {
		params := &cluster.CreateSdnVnetsParams{Vnet: net.VnetID, Zone: labZoneName}
		if tag != 0 {
			params.Tag = netPtr(tag)
		}
		if net.VnetAlias != "" {
			params.Alias = netPtr(net.VnetAlias)
		}
		if err := api.Cluster.CreateSdnVnets(ctx, params); err != nil {
			return fmt.Errorf("create SDN vnet %q: %w", net.VnetID, err)
		}
		return nil
	}

	params := &cluster.UpdateSdnVnetsParams{}
	changed := false

	if existing.Zone != labZoneName {
		params.Zone = netPtr(labZoneName)
		changed = true
	}
	if tag != 0 && existing.Tag != tag {
		params.Tag = netPtr(tag)
		changed = true
	}
	if net.VnetAlias != "" && existing.Alias != net.VnetAlias {
		params.Alias = netPtr(net.VnetAlias)
		changed = true
	}
	if !changed {
		return nil
	}

	if err := api.Cluster.UpdateSdnVnets(ctx, net.VnetID, params); err != nil {
		return fmt.Errorf("update SDN vnet %q: %w", net.VnetID, err)
	}
	return nil
}

// ensureLabSdnSubnet ensures net.CIDR exists as a subnet on net.VnetID with
// the gateway from net.Mgmt.Gateway. It creates the subnet when absent; when
// present, it updates the gateway only if it has drifted, addressing the
// update by the PVE-assigned subnet identifier (not the CIDR — see
// sdnSubnetState). An empty CIDR is a caller/config error, not silently
// skipped.
func ensureLabSdnSubnet(ctx context.Context, api *apiclient.APIClient, net config.LabNetwork) error {
	if net.CIDR == "" {
		return fmt.Errorf("lab network cidr is empty; cannot ensure an SDN subnet")
	}

	list, err := api.Cluster.ListSdnVnetsSubnets(ctx, net.VnetID, nil)
	if err != nil {
		return fmt.Errorf("list subnets on vnet %q: %w", net.VnetID, err)
	}

	existing, found, err := findSdnSubnet(*list, net.CIDR)
	if err != nil {
		return err
	}

	gateway := net.Mgmt.Gateway

	if !found {
		params := &cluster.CreateSdnVnetsSubnetsParams{Subnet: net.CIDR, Type: "subnet"}
		if gateway != "" {
			params.Gateway = netPtr(gateway)
		}
		if err := api.Cluster.CreateSdnVnetsSubnets(ctx, net.VnetID, params); err != nil {
			return fmt.Errorf("create subnet %q on vnet %q: %w", net.CIDR, net.VnetID, err)
		}
		return nil
	}

	if gateway == "" || existing.Gateway == gateway {
		return nil
	}

	params := &cluster.UpdateSdnVnetsSubnetsParams{Gateway: netPtr(gateway)}
	if err := api.Cluster.UpdateSdnVnetsSubnets(ctx, net.VnetID, existing.Subnet, params); err != nil {
		return fmt.Errorf("update subnet %q on vnet %q: %w", existing.Subnet, net.VnetID, err)
	}
	return nil
}

// nodeListContains reports whether node appears verbatim in a
// comma-separated PVE node-name list (e.g. a zone's "nodes" field).
func nodeListContains(nodes, node string) bool {
	for _, n := range strings.Split(nodes, ",") {
		if strings.TrimSpace(n) == node {
			return true
		}
	}
	return false
}

// nodeListAdd returns nodes with node appended, comma-separated, unless node
// is already present, in which case nodes is returned unchanged.
func nodeListAdd(nodes, node string) string {
	if nodes == "" {
		return node
	}
	if nodeListContains(nodes, node) {
		return nodes
	}
	return nodes + "," + node
}

// netPtr returns a pointer to a copy of v, for populating the optional
// *T-typed fields of the generated SDN request param structs from a plain
// value.
func netPtr[T any](v T) *T {
	return &v
}
