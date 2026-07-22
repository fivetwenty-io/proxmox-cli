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

// labZoneName returns the SDN zone a lab's vnet lives in: net.EffectiveZoneName(),
// which defaults to "labs" (the deployed outer Simple zone, decision D4 of
// the multi-node lab plan) when the lab's config leaves network.zone_name
// unset. Every `pmx lab net apply`/`pmx lab create` invocation reconciles
// against this per-lab-resolved zone name, superseding the platform's
// historical hardcoded "labsvxlan" VXLAN zone constant.
func labZoneName(net config.LabNetwork) string {
	return net.EffectiveZoneName()
}

// labZoneType returns the SDN zone plugin type for labZoneName(net):
// net.EffectiveZoneType(), which defaults to "simple".
func labZoneType(net config.LabNetwork) string {
	return net.EffectiveZoneType()
}

// labZonePeers returns the VXLAN zone's underlay peer list: net.EffectiveZonePeers(),
// which is always "" for a "simple"-type zone (Simple zones have no peers
// concept) and net.ZonePeers verbatim for a "vxlan"-type zone.
func labZonePeers(net config.LabNetwork) string {
	return net.EffectiveZonePeers()
}

// newNetCmd builds `pmx lab net` and its subcommands.
func newNetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "net",
		Short: "Manage a lab's SDN network",
		Long: "Reconcile a lab's SDN zone, every configured vnet (the primary " +
			"vnet plus any network.vnets entries) and their subnets against " +
			"its config, preview the resulting pending changeset, and apply it.",
	}
	cmd.AddCommand(newNetApplyCmd())
	return cmd
}

// newNetApplyCmd builds `pmx lab net apply <name>`.
//
// Every run resolves the lab (peppi-guarded via resolveLabForMutate), then,
// unless --dry-run is set, idempotently ensures the lab's SDN zone (shared,
// singular) and every configured vnet — the primary VnetID/CIDR pair, then
// each network.vnets[] entry in order (ensureLabSdnVnets) — plus each vnet's
// subnet exist and match its config; a vnet entry with an empty CIDR (a pure
// L2 passthrough vnet, e.g. a workload vnet) skips its subnet-ensure
// sub-step, matching only-create-what's-configured. Every ensure call
// queries live state first and issues a create or update only for what is
// missing or has drifted, keeping apply idempotent. It then ALWAYS calls
// ListSdnDryRun and renders the pending-changes preview, on every
// invocation, not only under --dry-run. --dry-run stops immediately after
// that preview: no zone/vnet/subnet create or update runs beforehand, and
// UpdateSdn never runs, so the preview in that mode reflects only whatever
// changeset was already staged before this command ran, not what this lab's
// reconciliation would stage. Without --dry-run, once the preview is shown,
// an empty changeset (no FRR or interfaces diff) skips UpdateSdn entirely as
// a no-op; a non-empty changeset is applied via UpdateSdn and awaited via
// WaitTask.
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
				zoneType, err := ensureLabSdnZone(ctx, deps.API, lab.Network, deps.Node, int64(lab.Network.MTU))
				if err != nil {
					return err
				}
				if err := ensureLabSdnVnets(ctx, deps.API, lab.Network, zoneType); err != nil {
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
// the reload task, mirroring `pmx pve sdn apply`'s immediate-vs-async response
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

// labZoneCreateParams builds the CreateSdnZonesParams that provisions the
// lab's config-resolved zone (labZoneName(net)/labZoneType(net)), peered on
// labZonePeers(net) when it is a vxlan-type zone, scoped to node, at the
// given MTU (0 leaves MTU unset). This is the single source of truth for the
// zone's create spec: both ensureLabSdnZone (`pmx lab net apply`) and `pmx
// lab create` build the zone's CreateSdnZones call through this function, so
// the two verbs can never provision the zone with diverging Type, Peers,
// Nodes, or MTU values.
func labZoneCreateParams(net config.LabNetwork, node string, mtu int64) *cluster.CreateSdnZonesParams {
	params := &cluster.CreateSdnZonesParams{
		Zone:  labZoneName(net),
		Type:  labZoneType(net),
		Nodes: netPtr(node),
	}
	if peers := labZonePeers(net); peers != "" {
		params.Peers = netPtr(peers)
	}
	if mtu > 0 {
		params.Mtu = netPtr(mtu)
	}
	return params
}

// ensureLabSdnZone ensures the lab's config-resolved zone (labZoneName(net))
// exists as labZoneType(net), peered on labZonePeers(net) when it is a
// vxlan-type zone, scoped to node, at the given MTU (0 leaves MTU unmanaged,
// e.g. when the lab config carries no explicit value). It creates the zone
// when absent; when present, it updates only the fields that have drifted
// (MTU, peers — only compared for a vxlan-type zone, since a simple zone has
// no peers field — or node membership), issuing no request at all when
// nothing has changed. On success it returns the zone's resolved plugin type
// — the live type when the zone already existed, else labZoneType(net) (the
// type it was just created as) — so a caller (ensureLabSdnVnets) can decide
// per-zone vnet-create behavior (e.g. omitting the tag param a "simple" zone
// rejects) without a second ListSdnZones round trip.
func ensureLabSdnZone(ctx context.Context, api *apiclient.APIClient, net config.LabNetwork, node string, mtu int64) (string, error) {
	zoneName := labZoneName(net)

	list, err := api.Cluster.ListSdnZones(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list SDN zones: %w", err)
	}

	existing, found, err := findSdnZone(*list, zoneName)
	if err != nil {
		return "", err
	}

	if !found {
		if err := api.Cluster.CreateSdnZones(ctx, labZoneCreateParams(net, node, mtu)); err != nil {
			return "", fmt.Errorf("create SDN zone %q: %w", zoneName, err)
		}
		return labZoneType(net), nil
	}

	params := &cluster.UpdateSdnZonesParams{}
	changed := false

	if mtu > 0 && existing.Mtu != mtu {
		params.Mtu = netPtr(mtu)
		changed = true
	}
	if peers := labZonePeers(net); peers != "" && existing.Peers != peers {
		params.Peers = netPtr(peers)
		changed = true
	}
	if node != "" && !nodeListContains(existing.Nodes, node) {
		params.Nodes = netPtr(nodeListAdd(existing.Nodes, node))
		changed = true
	}
	if !changed {
		return existing.Type, nil
	}

	if err := api.Cluster.UpdateSdnZones(ctx, zoneName, params); err != nil {
		return "", fmt.Errorf("update SDN zone %q: %w", zoneName, err)
	}
	return existing.Type, nil
}

// sdnZoneAllowsVnetTag reports whether an SDN zone of the given plugin type
// accepts the "tag" parameter on its vnets' create/update calls. PVE's
// "simple" zone plugin has no VLAN/VXLAN tag concept at the vnet level:
// `create sdn vnet` on a simple-zone vnet rejects a tag with "400 Parameter
// verification failed. tag: vlan tag is not allowed on simple zone". Every
// other zone plugin PVE ships (vlan, vxlan, qinq, evpn) does accept one, so
// this only special-cases "simple" (also the config default,
// config.DefaultZoneType) rather than enumerating an allow-list, keeping an
// unrecognized-but-tag-capable future zone type working unmodified. An empty
// zoneType (e.g. a not-yet-created zone whose type could not be resolved) is
// treated as tag-capable, matching every zone type except "simple".
func sdnZoneAllowsVnetTag(zoneType string) bool {
	return zoneType != "simple"
}

// ensureLabSdnVnetSubnet ensures one outer SDN vnet — identified by vnetID,
// in zoneName, with the given tag and alias — exists and matches those
// values: it creates the vnet when absent; when present, it updates only the
// fields that have drifted, issuing no request when nothing has changed.
// tagAllowed gates every use of tag: when false (the zone is a "simple"-type
// zone — see sdnZoneAllowsVnetTag), tag is omitted from the create call
// entirely and never considered for drift on an existing vnet, since PVE
// rejects the parameter outright for that zone type. When cidr is
// non-empty, it then ensures that vnet's subnet exists with the given
// gateway the same way (ensureLabSdnSubnetOn); an empty cidr skips the
// subnet-ensure sub-step entirely — a pure L2 passthrough vnet, e.g. a
// workload vnet with no subnet (multi-AZ topology plan §1/§2). An empty
// vnetID is a caller/config error, not silently skipped, since every vnet a
// lab declares — primary or extra — must have an id.
//
// This is the vnet-agnostic body shared by every vnet a lab's network
// declares: ensureLabSdnVnets calls it once for the primary VnetID/CIDR pair
// and once per Network.Vnets[] entry, so there is exactly one code path that
// can create or update an outer vnet+subnet pair.
func ensureLabSdnVnetSubnet(ctx context.Context, api *apiclient.APIClient, zoneName, vnetID, alias string, tag int, cidr, gateway string, tagAllowed bool) error {
	if vnetID == "" {
		return fmt.Errorf("vnet id is empty; cannot ensure an SDN vnet")
	}

	list, err := api.Cluster.ListSdnVnets(ctx, nil)
	if err != nil {
		return fmt.Errorf("list SDN vnets: %w", err)
	}

	existing, found, err := findSdnVnet(*list, vnetID)
	if err != nil {
		return err
	}

	tag64 := int64(tag)

	if !found {
		params := &cluster.CreateSdnVnetsParams{Vnet: vnetID, Zone: zoneName}
		if tagAllowed && tag64 != 0 {
			params.Tag = netPtr(tag64)
		}
		if alias != "" {
			params.Alias = netPtr(alias)
		}
		if err := api.Cluster.CreateSdnVnets(ctx, params); err != nil {
			return fmt.Errorf("create SDN vnet %q: %w", vnetID, err)
		}
	} else {
		params := &cluster.UpdateSdnVnetsParams{}
		changed := false

		if existing.Zone != zoneName {
			params.Zone = netPtr(zoneName)
			changed = true
		}
		if tagAllowed && tag64 != 0 && existing.Tag != tag64 {
			params.Tag = netPtr(tag64)
			changed = true
		}
		if alias != "" && existing.Alias != alias {
			params.Alias = netPtr(alias)
			changed = true
		}
		if changed {
			if err := api.Cluster.UpdateSdnVnets(ctx, vnetID, params); err != nil {
				return fmt.Errorf("update SDN vnet %q: %w", vnetID, err)
			}
		}
	}

	if cidr == "" {
		return nil
	}
	return ensureLabSdnSubnetOn(ctx, api, vnetID, cidr, gateway)
}

// ensureLabSdnSubnetOn ensures cidr exists as a subnet on vnetID with the
// given gateway. It creates the subnet when absent; when present, it
// updates the gateway only if it has drifted, addressing the update by the
// PVE-assigned subnet identifier (not the CIDR — see sdnSubnetState). This
// is the vnet-agnostic subnet body ensureLabSdnVnetSubnet calls for
// whichever vnet (primary or a Network.Vnets[] entry) it is reconciling; a
// caller must never invoke this with an empty cidr — ensureLabSdnVnetSubnet
// already skips the call in that case rather than passing one through.
func ensureLabSdnSubnetOn(ctx context.Context, api *apiclient.APIClient, vnetID, cidr, gateway string) error {
	list, err := api.Cluster.ListSdnVnetsSubnets(ctx, vnetID, nil)
	if err != nil {
		return fmt.Errorf("list subnets on vnet %q: %w", vnetID, err)
	}

	existing, found, err := findSdnSubnet(*list, cidr)
	if err != nil {
		return err
	}

	if !found {
		params := &cluster.CreateSdnVnetsSubnetsParams{Subnet: cidr, Type: "subnet"}
		if gateway != "" {
			params.Gateway = netPtr(gateway)
		}
		if err := api.Cluster.CreateSdnVnetsSubnets(ctx, vnetID, params); err != nil {
			return fmt.Errorf("create subnet %q on vnet %q: %w", cidr, vnetID, err)
		}
		return nil
	}

	if gateway == "" || existing.Gateway == gateway {
		return nil
	}

	params := &cluster.UpdateSdnVnetsSubnetsParams{Gateway: netPtr(gateway)}
	if err := api.Cluster.UpdateSdnVnetsSubnets(ctx, vnetID, existing.Subnet, params); err != nil {
		return fmt.Errorf("update subnet %q on vnet %q: %w", existing.Subnet, vnetID, err)
	}
	return nil
}

// ensureLabSdnVnets ensures every outer SDN vnet a lab's network declares:
// first the primary VnetID/CIDR pair (n.VnetID/.VnetAlias/.VxlanTag/.CIDR/
// .Mgmt.Gateway — today's single-vnet shape, unchanged), then each
// Network.Vnets[] entry in declaration order, each via
// ensureLabSdnVnetSubnet. zoneType is the lab's zone's resolved plugin type
// (from ensureLabSdnZone's return value — no independent lookup here, so the
// zone is probed at most once per apply regardless of how many vnets the lab
// declares); it is converted once, via sdnZoneAllowsVnetTag, into the
// tagAllowed flag passed to every ensureLabSdnVnetSubnet call below, so a
// "simple"-type zone (which rejects the tag parameter) never has one sent
// for any of its vnets. An empty n.VnetID is a caller/config error, not
// silently skipped, since every lab must name a primary vnet; an empty
// Vnets[] entry id is likewise refused, so a malformed extra-vnet entry
// cannot silently no-op instead of failing loud.
func ensureLabSdnVnets(ctx context.Context, api *apiclient.APIClient, n config.LabNetwork, zoneType string) error {
	if n.VnetID == "" {
		return fmt.Errorf("lab network vnet_id is empty; cannot ensure an SDN vnet")
	}

	zoneName := labZoneName(n)
	tagAllowed := sdnZoneAllowsVnetTag(zoneType)

	if err := ensureLabSdnVnetSubnet(ctx, api, zoneName, n.VnetID, n.VnetAlias, n.VxlanTag, n.CIDR, n.Mgmt.Gateway, tagAllowed); err != nil {
		return err
	}

	for i, v := range n.Vnets {
		if v.ID == "" {
			return fmt.Errorf("network.vnets[%d] has an empty id; cannot ensure an SDN vnet", i)
		}
		if err := ensureLabSdnVnetSubnet(ctx, api, zoneName, v.ID, v.Alias, v.Tag, v.CIDR, v.Gateway, tagAllowed); err != nil {
			return err
		}
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
