package lab

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// labInnerZoneName and labInnerZoneMTU are fixed by the multi-node lab plan
// §7: every multi-node lab's inner (nested-cluster) VXLAN zone is named
// "labvx" (≤8 characters, the same vnet-ID length limit as the outer zone)
// at MTU 1450 (the outer vnet runs at 1500; VXLAN encapsulation overhead
// forces this lower). Unlike the outer zone (net.go's config-driven
// EffectiveZoneName/Type, decision D4), the inner zone's name and type are
// never config-driven — every lab's inner zone is identically named and
// typed, only its peer list differs.
const (
	labInnerZoneName = "labvx"
	labInnerZoneType = "vxlan"
	labInnerZoneMTU  = 1450
)

// sdnInnerZone is the subset of a `pvesh get
// /cluster/sdn/zones/<zone> --output-format json` response this command
// needs to decide whether the inner zone's peer list has drifted.
type sdnInnerZone struct {
	Peers string `json:"peers"`
}

// peersSeparatorRE splits a peer-list string on any run of commas,
// semicolons, and/or whitespace: Proxmox VE's SDN zone `peers` property is
// comma-separated in zones.cfg and in a `pvesh get .../zones/<z>` response,
// so that is the format this package writes; splitting on the broader
// class defensively tolerates a space-separated or mixed-separator value
// too (e.g. a hand-edited zones.cfg, or a future PVE version) without
// misreading it as drift.
var peersSeparatorRE = regexp.MustCompile(`[,;\s]+`)

// normalizePeers splits raw via peersSeparatorRE, drops empty tokens (a
// leading/trailing/doubled separator must never produce a spurious empty
// "peer"), and returns the result sorted, so two peer lists naming the same
// address set compare equal regardless of separator or ordering.
func normalizePeers(raw string) []string {
	tokens := peersSeparatorRE.Split(raw, -1)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// peersEqual reports whether a and b name the same set of peer addresses,
// regardless of separator or ordering (normalizePeers). This is the
// comparison `sdn apply` uses to decide whether the inner zone's peers have
// actually drifted: comparing the raw strings directly (as an earlier
// version of this command did) false-positived on every already-converged
// run, since the value this command WRITES (comma-separated) never equals
// a value built with a different separator byte-for-byte, even when both
// name the identical peer set — spuriously re-issuing `pvesh set` and an
// SDN commit (and reload) on every single invocation.
func peersEqual(a, b string) bool {
	return slices.Equal(normalizePeers(a), normalizePeers(b))
}

// newSdnCmd builds `pmx lab sdn` and its subcommands.
func newSdnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdn",
		Short: "Manage a lab's inner (nested-cluster) VXLAN networking",
		Long: "Reconcile the VXLAN zone spanning every node of a multi-node lab's OWN nested " +
			"cluster (distinct from `pmx lab net`, which manages the outer per-lab Simple-zone " +
			"vnet on the physical host) — a plain flood-and-learn VXLAN zone so BOSH/Cloud " +
			"Foundry L2 guests can run and live-migrate to any node in the cluster. Applied over " +
			"ssh/pvesh against node 0; a single-node lab has nothing to reconcile.",
	}
	cmd.AddCommand(newSdnApplyCmd())
	cmd.AddCommand(newSdnVlanCmd())
	return cmd
}

// newSdnApplyCmd builds `pmx lab sdn apply <name>`.
func newSdnApplyCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "apply <name>",
		Short: "Reconcile a lab's inner VXLAN zone against its current node set",
		Long: "Ensure the nested cluster's \"labvx\" VXLAN zone exists (type vxlan, MTU 1450) " +
			"with a peer list equal to every currently-configured node's mgmt IP, comma-" +
			"separated (Proxmox VE's own SDN zone peers format), then commit the change via " +
			"`pvesh set /cluster/sdn`. Run over ssh " +
			"against node 0; must run after the nested cluster is formed (`pmx lab cluster " +
			"init`/`join`), since SDN changes propagate through pmxcfs, which requires healthy " +
			"inter-node communication. A single-node lab (topology.nodes=1) is a no-op with a " +
			"notice — it has no nested cluster for an inner zone to span. Re-running this after " +
			"a scale up/down reconciles the peer list to the lab's current node set (multi-node " +
			"lab plan §7/§9); it does not create or manage individual vnets/subnets inside the " +
			"zone — those are the operator's/BOSH's own responsibility once the zone exists.",
		Example: `  pmx lab sdn apply wayne
  pmx lab sdn apply wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSdnApply(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the pvesh commands that would run, without executing them")
	return cmd
}

func runSdnApply(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	numNodes := config.EffectiveTopologyNodes(lab.Topology)
	if numNodes < 2 {
		res := output.Result{Message: fmt.Sprintf(
			"lab %q is single-node (topology.nodes=%d); no inner cluster for a VXLAN zone to span, nothing to do.",
			name, numNodes)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	peerIPs := make([]string, 0, numNodes)
	for i := 0; i < numNodes; i++ {
		ip, ierr := labNodeMgmtIP(lab.Network, i)
		if ierr != nil {
			return fmt.Errorf("resolve node %d mgmt IP: %w", i, ierr)
		}
		peerIPs = append(peerIPs, ip)
	}
	// Comma-separated: Proxmox VE's own SDN zone `peers` property format
	// (zones.cfg and `pvesh get .../zones/<z>` both use commas), not the
	// space-separated form an earlier version of this command wrote — see
	// peersEqual's doc comment for why writing the wrong separator broke
	// idempotency against real PVE state.
	peers := strings.Join(peerIPs, ",")

	// dry-run never touches deps.Runner (see cluster.go's runClusterInit for
	// the same convention): it previews the zone this run would ensure,
	// without probing live remote state to decide create-vs-update.
	if dryRun {
		headers := []string{"STEP", "STATUS"}
		rows := [][]string{
			{fmt.Sprintf("ensure sdn zone %q (type %s, peers %q, mtu %d) on node 0",
				labInnerZoneName, labInnerZoneType, peers, labInnerZoneMTU), "would run"},
			{"commit pending sdn changes on node 0", "would run (if anything changed)"},
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
	}

	rows, err := sdnEnsureZoneApplied(deps, name, node0IP, peers)
	if err != nil {
		return err
	}

	headers := []string{"STEP", "STATUS"}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}

// sdnEnsureZoneApplied performs `sdn apply`'s actual work — probe, create
// or update the peers, commit if anything changed — without any cobra/
// rendering coupling, so `pmx lab scale`'s reconcile step can reuse the
// identical idempotent logic runSdnApply's RunE wraps. Returns the same
// two-row STEP/STATUS table runSdnApply's original inline version rendered
// (zone row, then commit row).
func sdnEnsureZoneApplied(deps *cli.Deps, name, node0IP, peers string) ([][]string, error) {
	createCmd := fmt.Sprintf(
		"pvesh create /cluster/sdn/zones --zone %s --type %s --peers %q --mtu %d",
		labInnerZoneName, labInnerZoneType, peers, labInnerZoneMTU)
	updateCmd := fmt.Sprintf("pvesh set /cluster/sdn/zones/%s --peers %q", labInnerZoneName, peers)
	commitCmd := "pvesh set /cluster/sdn"

	probe, perr := runGuestSSH(deps, node0IP, fmt.Sprintf(
		"pvesh get /cluster/sdn/zones/%s --output-format json", labInnerZoneName))

	var (
		changed  bool
		stepDesc string
	)

	switch {
	case perr == nil:
		var existing sdnInnerZone
		if uerr := json.Unmarshal([]byte(probe.Stdout), &existing); uerr != nil {
			return nil, fmt.Errorf("lab %q: decode existing inner sdn zone %q on node 0: %w", name, labInnerZoneName, uerr)
		}
		if !peersEqual(existing.Peers, peers) {
			if _, uerr := runGuestSSH(deps, node0IP, updateCmd); uerr != nil {
				return nil, fmt.Errorf("lab %q: update inner sdn zone %q peers on node 0: %w", name, labInnerZoneName, uerr)
			}
			changed = true
			stepDesc = fmt.Sprintf("sdn zone %q peers updated on node 0", labInnerZoneName)
		} else {
			stepDesc = fmt.Sprintf("sdn zone %q already matches on node 0", labInnerZoneName)
		}
	case guestCommandTransportFailed(perr):
		return nil, fmt.Errorf("lab %q: probe inner sdn zone %q on node 0 (%s): %w", name, labInnerZoneName, node0IP, perr)
	default:
		// Non-zero exit with a reachable node: pvesh reports the zone does
		// not exist yet.
		if _, cerr := runGuestSSH(deps, node0IP, createCmd); cerr != nil {
			return nil, fmt.Errorf("lab %q: create inner sdn zone %q on node 0: %w", name, labInnerZoneName, cerr)
		}
		changed = true
		stepDesc = fmt.Sprintf("sdn zone %q created on node 0", labInnerZoneName)
	}

	commitStatus := "skip (no pending changes)"
	if changed {
		if _, cerr := runGuestSSH(deps, node0IP, commitCmd); cerr != nil {
			return nil, fmt.Errorf("lab %q: commit inner sdn changes on node 0: %w", name, cerr)
		}
		commitStatus = "committed"
	}

	return [][]string{
		{fmt.Sprintf("sdn zone %q (peers %q)", labInnerZoneName, peers), stepDesc},
		{"commit pending sdn changes on node 0", commitStatus},
	}, nil
}

// labInnerVlanZoneType is the PVE SDN zone plugin type this command
// provisions for a lab's nested-node client-VLAN zone — distinct from
// labInnerZoneType (the always-present cross-node BOSH/CF VXLAN zone above),
// and from the outer host's own zone type (net.go's config-driven
// EffectiveZoneType). Neither the zone's name nor its member vnets are
// hardcoded here: both come from config.LabNestedVlanZone (multi-AZ
// topology plan §2).
const labInnerVlanZoneType = "vlan"

// newSdnVlanCmd builds `pmx lab sdn vlan` and its subcommands.
func newSdnVlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vlan",
		Short: "Manage a lab's nested-node client-VLAN SDN zone",
		Long: "Reconcile the inner Proxmox VE SDN \"vlan\"-type zone layered on one of a lab's " +
			"nested PVE node's own VLAN-aware bridges (network.nested_network.vlan_zone) — " +
			"distinct from `pmx lab sdn apply`'s always-present cross-node BOSH/CF VXLAN zone " +
			"above it. Applied over ssh/pvesh against node 0.",
	}
	cmd.AddCommand(newSdnVlanApplyCmd())
	return cmd
}

// newSdnVlanApplyCmd builds `pmx lab sdn vlan apply <name>`.
func newSdnVlanApplyCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "apply <name>",
		Short: "Reconcile a lab's nested-node client-VLAN SDN zone against its config",
		Long: "Ensure the lab's inner \"vlan\"-type SDN zone (network.nested_network.vlan_zone) " +
			"exists on the bridge it names, then ensure every one of its configured vnets and " +
			"subnets exist and match, each independently idempotent (probe-before-create/update, " +
			"mirroring `pmx lab sdn apply`'s zone-reconciliation pattern), then commit via " +
			"`pvesh set /cluster/sdn` iff anything changed. Run over ssh against node 0; must run " +
			"after the nested cluster's bonds/bridges exist (`pmx lab hostnet apply`), since the " +
			"zone's bridge must already exist for PVE to accept it. A lab with no " +
			"network.nested_network.vlan_zone configured is a no-op with a notice.",
		Example: `  pmx lab sdn vlan apply pve-cpi
  pmx lab sdn vlan apply pve-cpi --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSdnVlanApply(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the pvesh commands that would run, without executing them")
	return cmd
}

func runSdnVlanApply(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	vz := lab.Network.NestedNetwork.VlanZone
	if vz == nil {
		res := output.Result{Message: fmt.Sprintf(
			"lab %q has no network.nested_network.vlan_zone configured; nothing to do.", name)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	// dry-run never touches deps.Runner (see runSdnApply's identical
	// convention above): it previews the zone/vnets/subnets this run would
	// ensure, without probing live remote state to decide create-vs-update.
	if dryRun {
		headers := []string{"STEP", "STATUS"}
		rows := [][]string{
			{fmt.Sprintf("ensure sdn zone %q (type %s, bridge %s) on node 0",
				vz.ZoneName, labInnerVlanZoneType, vz.Bridge), "would run"},
		}
		for _, v := range vz.Vnets {
			rows = append(rows, []string{
				fmt.Sprintf("ensure sdn vnet %q (zone %s, tag %d) on node 0", v.ID, vz.ZoneName, v.Tag), "would run"})
			if v.CIDR != "" {
				rows = append(rows, []string{
					fmt.Sprintf("ensure sdn subnet %q on vnet %q on node 0", v.CIDR, v.ID), "would run"})
			}
		}
		rows = append(rows, []string{"commit pending sdn changes on node 0", "would run (if anything changed)"})
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
	}

	rows, err := sdnEnsureVlanZoneApplied(deps, name, node0IP, vz)
	if err != nil {
		return err
	}

	headers := []string{"STEP", "STATUS"}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}

// sdnInnerVlanZoneState is the subset of a `pvesh get /cluster/sdn/zones/<z>
// --output-format json` response this command needs to decide whether an
// inner "vlan"-type zone's bridge has drifted.
type sdnInnerVlanZoneState struct {
	Bridge string `json:"bridge"`
}

// sdnInnerVlanVnetState is the subset of a `pvesh get
// /cluster/sdn/vnets/<id> --output-format json` response this command needs
// to decide whether an inner vlan-zone vnet has drifted.
type sdnInnerVlanVnetState struct {
	Zone  string `json:"zone"`
	Tag   int    `json:"tag"`
	Alias string `json:"alias"`
}

// sdnInnerVlanSubnetState is the subset of one element of a `pvesh get
// /cluster/sdn/vnets/<vnet>/subnets --output-format json` response this
// command needs to decide whether an inner vlan-zone vnet's subnet has
// drifted. Subnet is the PVE-assigned subnet identifier (distinct from
// Cidr, the plain CIDR string from config) — an update must address the
// subnet by Subnet, not by Cidr, mirroring net.go's sdnSubnetState/
// findSdnSubnet convention for the outer (API-driven) subnet ensure path.
type sdnInnerVlanSubnetState struct {
	Subnet  string `json:"subnet"`
	Cidr    string `json:"cidr"`
	Gateway string `json:"gateway"`
}

// sdnEnsureVlanZoneApplied performs `sdn vlan apply`'s actual work — ensure
// the zone, then every configured vnet and its subnet, then commit iff
// anything changed — without any cobra/rendering coupling, mirroring
// sdnEnsureZoneApplied's shape above. vz must be non-nil; callers (only
// runSdnVlanApply today) must have already handled the nil/no-op case.
// Returns one STEP/STATUS row per zone/vnet/subnet ensured, plus a final
// commit row.
func sdnEnsureVlanZoneApplied(deps *cli.Deps, name, node0IP string, vz *config.LabNestedVlanZone) ([][]string, error) {
	var rows [][]string
	changed := false

	zoneChanged, zoneRow, err := sdnEnsureVlanZone(deps, name, node0IP, vz)
	if err != nil {
		return nil, err
	}
	rows = append(rows, zoneRow)
	changed = changed || zoneChanged

	for _, v := range vz.Vnets {
		vnetChanged, vnetRow, verr := sdnEnsureVlanVnet(deps, name, node0IP, vz.ZoneName, v)
		if verr != nil {
			return nil, verr
		}
		rows = append(rows, vnetRow)
		changed = changed || vnetChanged

		// LabNestedVlanVnet.CIDR is a required field (config/lab.go); the
		// empty-CIDR guard here is defensive only, against a hand-edited
		// config that bypassed schema validation, not an expected shape.
		if v.CIDR == "" {
			continue
		}
		subnetChanged, subnetRow, serr := sdnEnsureVlanSubnet(deps, name, node0IP, v)
		if serr != nil {
			return nil, serr
		}
		rows = append(rows, subnetRow)
		changed = changed || subnetChanged
	}

	commitCmd := "pvesh set /cluster/sdn"
	commitStatus := "skip (no pending changes)"
	if changed {
		if _, cerr := runGuestSSH(deps, node0IP, commitCmd); cerr != nil {
			return nil, fmt.Errorf("lab %q: commit inner sdn changes on node 0: %w", name, cerr)
		}
		commitStatus = "committed"
	}
	rows = append(rows, []string{"commit pending sdn changes on node 0", commitStatus})

	return rows, nil
}

// sdnEnsureVlanZone ensures vz's zone exists as a "vlan"-type zone on
// vz.Bridge, probing by zone name exactly as sdnEnsureZoneApplied's VXLAN
// zone does above: a non-zero, non-transport-failure exit from `pvesh get`
// means the zone does not exist yet (create); a zero exit decodes the
// existing bridge and updates only on drift. Returns whether anything
// changed and this step's STEP/STATUS row.
func sdnEnsureVlanZone(deps *cli.Deps, name, node0IP string, vz *config.LabNestedVlanZone) (bool, []string, error) {
	stepLabel := fmt.Sprintf("vlan sdn zone %q (bridge %s)", vz.ZoneName, vz.Bridge)

	probe, perr := runGuestSSH(deps, node0IP, fmt.Sprintf(
		"pvesh get /cluster/sdn/zones/%s --output-format json", vz.ZoneName))

	switch {
	case perr == nil:
		var existing sdnInnerVlanZoneState
		if uerr := json.Unmarshal([]byte(probe.Stdout), &existing); uerr != nil {
			return false, nil, fmt.Errorf("lab %q: decode existing inner vlan sdn zone %q on node 0: %w", name, vz.ZoneName, uerr)
		}
		if existing.Bridge == vz.Bridge {
			return false, []string{stepLabel, fmt.Sprintf("sdn zone %q already matches on node 0", vz.ZoneName)}, nil
		}
		updateCmd := fmt.Sprintf("pvesh set /cluster/sdn/zones/%s --bridge %s", vz.ZoneName, vz.Bridge)
		if _, uerr := runGuestSSH(deps, node0IP, updateCmd); uerr != nil {
			return false, nil, fmt.Errorf("lab %q: update inner vlan sdn zone %q bridge on node 0: %w", name, vz.ZoneName, uerr)
		}
		return true, []string{stepLabel, fmt.Sprintf("sdn zone %q bridge updated on node 0", vz.ZoneName)}, nil
	case guestCommandTransportFailed(perr):
		return false, nil, fmt.Errorf("lab %q: probe inner vlan sdn zone %q on node 0 (%s): %w", name, vz.ZoneName, node0IP, perr)
	default:
		createCmd := fmt.Sprintf("pvesh create /cluster/sdn/zones --zone %s --type %s --bridge %s",
			vz.ZoneName, labInnerVlanZoneType, vz.Bridge)
		if _, cerr := runGuestSSH(deps, node0IP, createCmd); cerr != nil {
			return false, nil, fmt.Errorf("lab %q: create inner vlan sdn zone %q on node 0: %w", name, vz.ZoneName, cerr)
		}
		return true, []string{stepLabel, fmt.Sprintf("sdn zone %q created on node 0", vz.ZoneName)}, nil
	}
}

// sdnEnsureVlanVnet ensures v exists as a vnet of zoneName, probing by vnet
// ID exactly as sdnEnsureVlanZone probes by zone name: a non-zero,
// non-transport-failure exit means the vnet does not exist yet (create); a
// zero exit decodes the existing zone/tag/alias and updates only on drift.
// An empty v.Alias never triggers a drift update on its own (mirrors
// ensureLabSdnVnet's own "only compare when the config sets a value"
// convention) — a vnet already carrying a server-assigned alias must not be
// blanked out by a config that simply never set one.
func sdnEnsureVlanVnet(deps *cli.Deps, name, node0IP, zoneName string, v config.LabNestedVlanVnet) (bool, []string, error) {
	stepLabel := fmt.Sprintf("vlan sdn vnet %q (zone %s, tag %d)", v.ID, zoneName, v.Tag)

	probe, perr := runGuestSSH(deps, node0IP, fmt.Sprintf(
		"pvesh get /cluster/sdn/vnets/%s --output-format json", v.ID))

	switch {
	case perr == nil:
		var existing sdnInnerVlanVnetState
		if uerr := json.Unmarshal([]byte(probe.Stdout), &existing); uerr != nil {
			return false, nil, fmt.Errorf("lab %q: decode existing inner vlan sdn vnet %q on node 0: %w", name, v.ID, uerr)
		}
		drift := existing.Zone != zoneName || existing.Tag != v.Tag || (v.Alias != "" && existing.Alias != v.Alias)
		if !drift {
			return false, []string{stepLabel, fmt.Sprintf("sdn vnet %q already matches on node 0", v.ID)}, nil
		}

		args := []string{fmt.Sprintf("--zone %s", zoneName), fmt.Sprintf("--tag %d", v.Tag)}
		if v.Alias != "" {
			args = append(args, fmt.Sprintf("--alias %s", v.Alias))
		}
		updateCmd := fmt.Sprintf("pvesh set /cluster/sdn/vnets/%s %s", v.ID, strings.Join(args, " "))
		if _, uerr := runGuestSSH(deps, node0IP, updateCmd); uerr != nil {
			return false, nil, fmt.Errorf("lab %q: update inner vlan sdn vnet %q on node 0: %w", name, v.ID, uerr)
		}
		return true, []string{stepLabel, fmt.Sprintf("sdn vnet %q updated on node 0", v.ID)}, nil
	case guestCommandTransportFailed(perr):
		return false, nil, fmt.Errorf("lab %q: probe inner vlan sdn vnet %q on node 0 (%s): %w", name, v.ID, node0IP, perr)
	default:
		createCmd := fmt.Sprintf("pvesh create /cluster/sdn/vnets --vnet %s --zone %s --tag %d", v.ID, zoneName, v.Tag)
		if v.Alias != "" {
			createCmd += fmt.Sprintf(" --alias %s", v.Alias)
		}
		if _, cerr := runGuestSSH(deps, node0IP, createCmd); cerr != nil {
			return false, nil, fmt.Errorf("lab %q: create inner vlan sdn vnet %q on node 0: %w", name, v.ID, cerr)
		}
		return true, []string{stepLabel, fmt.Sprintf("sdn vnet %q created on node 0", v.ID)}, nil
	}
}

// sdnEnsureVlanSubnet ensures v's CIDR exists as a subnet of vnet v.ID,
// matched by CIDR against the vnet's subnet list — mirroring net.go's
// findSdnSubnet convention (matched by CIDR, not a guessed subnet
// identifier, since the lab config only ever states the CIDR) — rather
// than probing a single guessed subnet-ID path the way the zone and vnet
// steps above probe by their own (config-known) IDs. A non-transport-
// failure error listing subnets is treated as "no subnets yet" (the vnet
// was just created moments earlier and may not yet have any), falling
// through to create.
func sdnEnsureVlanSubnet(deps *cli.Deps, name, node0IP string, v config.LabNestedVlanVnet) (bool, []string, error) {
	stepLabel := fmt.Sprintf("vlan sdn subnet %q on vnet %q", v.CIDR, v.ID)

	list, lerr := runGuestSSH(deps, node0IP, fmt.Sprintf(
		"pvesh get /cluster/sdn/vnets/%s/subnets --output-format json", v.ID))
	if lerr != nil && guestCommandTransportFailed(lerr) {
		return false, nil, fmt.Errorf("lab %q: list subnets on inner vlan sdn vnet %q on node 0 (%s): %w", name, v.ID, node0IP, lerr)
	}

	var existing []sdnInnerVlanSubnetState
	if lerr == nil && list.Stdout != "" {
		if uerr := json.Unmarshal([]byte(list.Stdout), &existing); uerr != nil {
			return false, nil, fmt.Errorf("lab %q: decode inner vlan sdn subnets on vnet %q on node 0: %w", name, v.ID, uerr)
		}
	}

	var found *sdnInnerVlanSubnetState
	for i := range existing {
		if existing[i].Cidr == v.CIDR {
			found = &existing[i]
			break
		}
	}

	if found == nil {
		createCmd := fmt.Sprintf("pvesh create /cluster/sdn/vnets/%s/subnets --subnet %s --type subnet", v.ID, v.CIDR)
		if v.Gateway != "" {
			createCmd += fmt.Sprintf(" --gateway %s", v.Gateway)
		}
		if _, cerr := runGuestSSH(deps, node0IP, createCmd); cerr != nil {
			return false, nil, fmt.Errorf("lab %q: create subnet %q on inner vlan sdn vnet %q on node 0: %w", name, v.CIDR, v.ID, cerr)
		}
		return true, []string{stepLabel, fmt.Sprintf("sdn subnet %q created on node 0", v.CIDR)}, nil
	}

	if v.Gateway == "" || found.Gateway == v.Gateway {
		return false, []string{stepLabel, fmt.Sprintf("sdn subnet %q already matches on node 0", v.CIDR)}, nil
	}

	updateCmd := fmt.Sprintf("pvesh set /cluster/sdn/vnets/%s/subnets/%s --gateway %s", v.ID, found.Subnet, v.Gateway)
	if _, uerr := runGuestSSH(deps, node0IP, updateCmd); uerr != nil {
		return false, nil, fmt.Errorf("lab %q: update subnet %q gateway on inner vlan sdn vnet %q on node 0: %w", name, v.CIDR, v.ID, uerr)
	}
	return true, []string{stepLabel, fmt.Sprintf("sdn subnet %q gateway updated on node 0", v.CIDR)}, nil
}
