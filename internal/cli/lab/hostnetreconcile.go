package lab

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// Nested host-network reconciliation for the OUTER-context verbs.
//
// `pmx lab hostnet apply` reaches the nested cluster's own
// `/nodes/{node}/network` API through deps.API and therefore refuses to run
// under any context but the lab's own (hostnetRequireLabContext). `pmx lab
// scale` and `pmx lab cluster join` are the opposite: they run against the
// OUTER host — they create VM shells there — while still being the verbs
// that bring a node into existence. Before this file, that split left a
// grown or freshly joined node with no management IPv6 (and no nested
// bond/bridge config) until an operator remembered to run `hostnet apply`
// against the lab context by hand.
//
// The bridge is labInnerAPIClient: the same cli.BuildContextClient call
// labProbeContextVersion (labcontext.go) already uses to reach a lab's
// context from an outer-context command, pointed at labContextName(name).
// One client covers every node — the nested cluster's API proxies
// `/nodes/<other>/network` to its peers — so a single build serves the whole
// reconcile.
//
// Two deliberate limits:
//
//   - The ssh-transported NIC-naming phase (hostnetEnsureNICNaming) never
//     runs here. It can leave a node reboot-pending, and a reboot is not
//     something a topology transition may decide to need halfway through;
//     `hostnet apply` remains its only owner. Because of that, the
//     bond/bridge half is skipped on any node whose physical NICs are not
//     ALREADY named to the lab convention (hostnetNodeNICsNamed): a bond
//     staged against nic0-nic5 on a node still running ens18-ens23 would
//     be applied against interfaces that do not exist, taking the node's
//     networking down. Such a node still gets its IPv6.
//   - Nothing in here is fatal. A lab context that was never registered, an
//     inner API that is not up yet, a node that has not finished
//     provisioning — all of them are reported as a deferred row naming the
//     follow-up command, never as an error that aborts a transition whose
//     cluster/storage work already succeeded.

// labInnerAPIClient builds an API client bound to a lab's own nested-cluster
// context. It is a package var so tests can supply a fake client (and fake
// failures) without a live context registry, exactly as
// labProbeContextVersion is.
var labInnerAPIClient = func(cmd *cobra.Command, deps *cli.Deps, name string) (*apiclient.APIClient, error) {
	ac, _, err := cli.BuildContextClient(
		cmd, deps.Cfg, deps.ConfigPath, labContextName(name), deps.Insecure, func() bool { return false })
	return ac, err
}

// hostnetReconcileFollowup is the operator instruction every deferred row
// points at: the full `hostnet apply` verb, context flag included, which
// converges everything this reconcile could not (its NIC-naming phase
// included).
func hostnetReconcileFollowup(name string) string {
	return fmt.Sprintf("run `pmx -c %s lab hostnet apply %s` to converge it",
		labContextName(name), name)
}

// hostnetReconcileNeeded reports whether a lab has any nested host-network
// state worth reconciling at all: bonds/bridges to build, or IPv6 to put on
// the management bridge. A lab with neither (no nested_network, ipv6 off)
// gets no rows and no client build.
func hostnetReconcileNeeded(n config.LabNetwork) bool {
	return len(n.NestedNetwork.Bonds) > 0 || n.EffectiveIPv6()
}

// hostnetReconcileNodes reconciles the nested host networking of the given
// node indexes — the API-driven half of `pmx lab hostnet apply`: each
// configured bond and bridge, plus the management bridge's IPv6 addressing,
// staged and then applied per node (hostnetEnsureNode).
//
// It returns STEP/STATUS rows for the caller's own table (node name folded
// into the step, since scale and cluster join both render two columns): see
// this file's header for why every failure becomes a deferred row rather than
// failing the caller. An empty idxs, or a lab with nothing to reconcile,
// returns no rows at all.
//
// The second return value is non-nil only when the lab's own context could not
// be reached at all, which is the one failure here that --require-context
// promotes to a non-zero exit. A per-node reconcile that failed against a
// context that WAS reachable stays a deferred row and nothing more: it
// describes host networking, not the context registration the flag is about.
func hostnetReconcileNodes(
	ctx context.Context, cmd *cobra.Command, deps *cli.Deps, lab *config.Lab, idxs []int,
) ([][]string, error) {
	if len(idxs) == 0 || !hostnetReconcileNeeded(lab.Network) {
		return nil, nil
	}

	name := lab.Name
	nn := lab.Network.NestedNetwork

	api, err := labInnerAPIClient(cmd, deps, name)
	if err != nil {
		return [][]string{{"reconcile nested host network", fmt.Sprintf(
				"deferred: cannot reach lab context %q (%v); register it with `pmx lab context sync %s`, then %s",
				labContextName(name), err, name, hostnetReconcileFollowup(name))}},
			fmt.Errorf("reach lab context %s: %w", labContextName(name), err)
	}

	var rows [][]string

	// The NIC-naming phase is `hostnet apply`'s alone (see the header). Say
	// so once, rather than leaving an operator to conclude from a green
	// table that a bonded lab is fully converged.
	if len(nn.Bonds) > 0 {
		rows = append(rows, []string{"reconcile nested host network", fmt.Sprintf(
			"NIC naming (nic0-nic%d) not attempted here (it can leave a node reboot-pending); %s",
			hostnetRequiredNICCount-1, hostnetReconcileFollowup(name))})
	}

	for _, idx := range idxs {
		nodeName := hostnetNodeName(name, idx)
		step := fmt.Sprintf("nested host network on node %d (%s)", idx, nodeName)

		v6Want, werr := hostnetNodeV6Want(lab.Network, idx)
		if werr != nil {
			rows = append(rows, []string{step, fmt.Sprintf("deferred: resolve IPv6 addressing: %v", werr)})
			continue
		}

		nodeNN := nn
		if len(nn.Bonds) > 0 {
			named, nerr := hostnetNodeNICsNamed(ctx, api, nodeName)
			switch {
			case nerr != nil:
				rows = append(rows, []string{step,
					fmt.Sprintf("deferred: %v; %s", nerr, hostnetReconcileFollowup(name))})
				continue
			case !named:
				nodeNN = config.LabNestedNetwork{}
				rows = append(rows, []string{step, fmt.Sprintf(
					"bond/bridge phase skipped: node's NICs are not named nic0-nic%d yet, and staging a "+
						"bond against interfaces that do not exist would break its networking on apply; %s",
					hostnetRequiredNICCount-1, hostnetReconcileFollowup(name))})
			}
		}

		nodeRows, err := hostnetEnsureNode(ctx, api, name, nodeName, nodeNN, v6Want)
		if err != nil {
			rows = append(rows, []string{step, fmt.Sprintf("deferred: %v; %s", err, hostnetReconcileFollowup(name))})
			continue
		}
		for _, r := range nodeRows {
			// hostnetEnsureNode rows are NODE/STEP/STATUS; fold the node
			// into the step for the caller's two-column table.
			rows = append(rows, []string{fmt.Sprintf("%s: %s", r[0], r[1]), r[2]})
		}
	}

	return rows, nil
}

// hostnetNodeNICsNamed reports whether nodeName's interface list already
// carries every NIC name the lab's bond config references (nic0 through
// nic<hostnetRequiredNICCount-1>) — the state `hostnet apply`'s ssh
// NIC-naming phase produces, and which scripts/first-boot-network.sh.tmpl
// pins at install time for a freshly provisioned node. It is the
// precondition for staging any bond from here: PVE accepts a bond whose
// slaves do not exist, and the failure surfaces only when the staged
// changes are applied — on the node's live management path.
func hostnetNodeNICsNamed(ctx context.Context, api *apiclient.APIClient, nodeName string) (bool, error) {
	list, err := api.Nodes.ListNetwork(ctx, nodeName, nil)
	if err != nil {
		return false, fmt.Errorf("list network interfaces on node %q: %w", nodeName, err)
	}
	if list == nil {
		return false, nil
	}
	existing, err := hostnetDecodeInterfaces(*list)
	if err != nil {
		return false, fmt.Errorf("decode network interfaces on node %q: %w", nodeName, err)
	}
	for i := range hostnetRequiredNICCount {
		if _, ok := existing[hostnetNICName(i)]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// hostnetReconcilePreviewRows describes, without touching the network, what
// hostnetReconcileNodes would do for idxs — the dry-run counterpart, built
// purely from config the way `hostnet apply --dry-run` builds its own
// preview.
func hostnetReconcilePreviewRows(lab *config.Lab, idxs []int) [][]string {
	if len(idxs) == 0 || !hostnetReconcileNeeded(lab.Network) {
		return nil
	}

	nn := lab.Network.NestedNetwork
	var rows [][]string
	for _, idx := range idxs {
		nodeName := hostnetNodeName(lab.Name, idx)
		for _, b := range nn.Bonds {
			rows = append(rows, []string{
				fmt.Sprintf("%s: ensure bond %q + bridge %q", nodeName, b.Name, b.Bridge), "would run"})
		}
		if lab.Network.EffectiveIPv6() {
			status := "would run"
			if want, err := hostnetNodeV6Want(lab.Network, idx); err == nil && want != nil {
				status = fmt.Sprintf("would ensure %s (gateway %s)", want.Cidr6, want.Gateway6)
			}
			rows = append(rows, []string{
				fmt.Sprintf("%s: ensure IPv6 on %s", nodeName, hostnetMgmtBridge), status})
		}
	}
	return rows
}
