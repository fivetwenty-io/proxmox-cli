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
