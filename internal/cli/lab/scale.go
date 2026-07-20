package lab

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
)

// newScaleCmd builds `pmx lab scale <name>`.
func newScaleCmd() *cobra.Command {
	var (
		nodesFlag   int
		qdeviceFlag string
		nodeFlag    string
		dryRun      bool
		force       bool
		yes         bool
	)

	cmd := &cobra.Command{
		Use:   "scale <name>",
		Short: "Migrate a lab's node count (and QDevice) to a new topology",
		Long: "Orchestrate a lab's transition to a new topology.nodes / topology.qdevice " +
			"target, per multi-node lab plan §9. Preflight (before any mutation): rename a " +
			"surviving legacy `lab-<member>` VM (no index suffix) to `lab-<member>-0` (decision " +
			"D3's safety net), verify the CURRENT cluster (if any) is quorate, run the capacity " +
			"gate if this run will create any VM shell — all before the QDevice-removal step, so " +
			"a capacity refusal never strands the cluster witness-less. Then: remove the QDevice " +
			"first if the target no longer needs one (never leave an odd+witness Last-Man-" +
			"Standing shape mid-transition) and destroy its now-orphaned VM; grow by creating VM " +
			"shells and joining every newly-reachable node in serial index order (deferring, not " +
			"failing, at the first node whose OS/ssh is not yet provisioned — re-run once it is); " +
			"shrink by evacuating guests to node 0, delnode, and destroying each departing node's " +
			"VM in reverse index order (node 0 is never removed); add the QDevice after every " +
			"join/delnode for this transition has completed; reconcile the inner SDN zone and NFS " +
			"storage attachment; and finally validate quorum, corosync links, and storage-active " +
			"state on EVERY node in the target topology. A transition that completed (was not " +
			"deferred waiting on manual OS provisioning) but ends non-quorate, link-degraded, or " +
			"storage-inactive on any node returns a non-zero exit after rendering the full report " +
			"— a deferred, still-in-progress transition does not. Every step besides the final " +
			"validation is individually idempotent, so a scale command that stopped partway can " +
			"simply be re-run to continue: the current node count and QDevice registration are " +
			"always re-derived from node 0's live corosync membership, never from VM-shell " +
			"existence alone, so a re-run correctly resumes joining/wiring already-created shells " +
			"rather than treating their mere existence as \"done.\" VM-shell provisioning (new " +
			"node/QDevice VMs) reuses `pmx lab create`'s own idempotent plan machinery and " +
			"capacity gate; this command creates VM shells only — it never installs an OS, " +
			"matching `pmx lab create`'s own scope. SNAT-rule, PBS-job, and DNS registration for " +
			"new/removed nodes remain lab-repository host-side script responsibilities, outside " +
			"this CLI's scope; a completed node-count change also prints a reminder that the " +
			"lab's PDM remote (single-host vs. cluster endpoint) needs a manual swap via the lab " +
			"repository's own PDM tooling — `pmx lab scale` has no PDM write API to call. The " +
			"live-migration acceptance test (a test VM on nfs-images migrated across every node) " +
			"is milestone QA, not part of this command's own automated validation.",
		Example: `  pmx lab scale wayne --nodes 3
  pmx lab scale wayne --nodes 2 --qdevice auto
  pmx lab scale wayne --nodes 5 --dry-run
  pmx lab scale wayne --nodes 1 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScale(cmd, args[0], nodesFlag, qdeviceFlag, nodeFlag, dryRun, force, yes)
		},
	}

	cmd.Flags().IntVar(&nodesFlag, "nodes", 0,
		fmt.Sprintf("target topology.nodes (%d-%d); required", config.MinTopologyNodes, config.MaxTopologyNodes))
	cmd.Flags().StringVar(&qdeviceFlag, "qdevice", "",
		"override topology.qdevice (\"auto\" or \"never\") for the target topology")
	cmd.Flags().StringVar(&nodeFlag, "node", "",
		"PVE host node to create new lab VMs on (defaults to --node/PMX_NODE/config default); "+
			"only needed when this scale actually creates a VM shell (growing, or adding a QDevice)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the full migration plan without mutating anything")
	cmd.Flags().BoolVar(&force, "force", false,
		"override the capacity gate's refusal threshold when this scale creates new VM shells")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func runScale(cmd *cobra.Command, name string, nodesFlag int, qdeviceFlag, nodeFlag string, dryRun, force, yes bool) error {
	deps := cli.GetDeps(cmd)
	ctx := cmd.Context()

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	if nodesFlag == 0 {
		return fmt.Errorf("--nodes is required (%d-%d)", config.MinTopologyNodes, config.MaxTopologyNodes)
	}
	if nodesFlag < config.MinTopologyNodes || nodesFlag > config.MaxTopologyNodes {
		return fmt.Errorf("--nodes %d is out of range [%d, %d]", nodesFlag, config.MinTopologyNodes, config.MaxTopologyNodes)
	}

	eff := scaleEffectiveLab(lab, nodesFlag, qdeviceFlag, cmd.Flags().Changed("qdevice"))
	if issues := config.ValidateTopology(name, eff.Topology); len(issues) > 0 {
		return fmt.Errorf("lab %q target topology is invalid:\n  %s", name, strings.Join(issues, "\n  "))
	}

	// Discovering the lab's live VM shells needs a deps.API call
	// (listLiveVMs) regardless of --dry-run, exactly like create.go's own
	// dry-run preview already does GETs — the "dry-run never touches
	// deps.Runner" convention (quota.go's precedent, reiterated for every
	// SSH-transported verb in this package) is about ssh calls specifically,
	// not every remote call.
	vms, err := listLiveVMs(ctx, deps)
	if err != nil {
		return err
	}
	poolID := labPoolID(lab)
	classified, err := findLabVMs(vms, poolID, lab.Name)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}

	shellN, err := scaleCurrentNodeCount(classified)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}
	if shellN == 0 {
		return fmt.Errorf("lab %q has no node 0 VM yet; run `pmx lab create %s` first", name, name)
	}

	targetN := nodesFlag
	targetQdeviceRequired := config.QdeviceRequired(eff.Topology)

	if dryRun {
		// The preview is a best-effort ESTIMATE computed from VM-shell
		// existence only (no ssh calls) — the same "generic would-run
		// preview, no live-state probing" contract every other ssh-based
		// verb's own --dry-run already has (cluster init/join, qdevice add,
		// sdn apply, nfs attach: none of them probe live state under
		// --dry-run either). The REAL run always re-derives the
		// authoritative plan from node 0's live corosync membership right
		// before acting (scaleCurrentMembership, below), so a dry-run
		// preview and the subsequent real run can legitimately describe a
		// different delta when VM shells exist but are not yet joined
		// (e.g. after a previous deferred run) — this is expected, not a
		// bug.
		_, shellQdevicePresent := qdeviceLabVM(classified)
		previewPlan := buildScalePlan(shellN, shellQdevicePresent, targetN, targetQdeviceRequired)
		return deps.Out.Render(cmd.OutOrStdout(), renderScalePlanPreview(name, previewPlan), deps.Format)
	}

	// --- Real run: preflight (plan §9 step 1), authoritative from here on ---

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	var preflightRows [][]string

	// Ground-truth current state: node 0's live corosync membership, not
	// VM-shell existence (see scaleCurrentMembership's doc comment for why
	// — this is the fix for the "deferred grow never resumes on re-run"
	// defect). Also serves as the preflight "verify the current cluster is
	// healthy and quorate" check (plan §9 step 1): a currently-clustered
	// lab that is not quorate refuses here, before any mutation — including
	// the legacy rename below, which, though idempotent metadata-only, is
	// still a mutation and must not precede this refusal.
	currentN, currentQdevicePresent, quorate, err := scaleCurrentMembership(deps, lab, node0IP)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}
	if !quorate {
		return fmt.Errorf(
			"lab %q: node 0 (%s) reports its CURRENT cluster is not quorate; resolve this before "+
				"scaling (multi-node lab plan §9 step 1: \"verify all current nodes healthy and quorate\")",
			name, node0IP)
	}

	// Legacy-rename safety net (decision D3): a bare `lab-<member>` VM (no
	// index suffix) surviving past the fleet rebuild is renamed to
	// `lab-<member>-0`. API-only (no ssh), idempotent.
	renameMsg, err := scaleRenameLegacyNodeZero(ctx, deps, lab, classified)
	if err != nil {
		return err
	}
	if renameMsg != "" {
		preflightRows = append(preflightRows, []string{"legacy rename (node 0)", renameMsg})
	}

	if targetN == currentN && targetQdeviceRequired == currentQdevicePresent {
		res := output.Result{Message: fmt.Sprintf(
			"lab %q is already at the target topology (%d node(s), qdevice=%v); nothing to do.",
			name, targetN, targetQdeviceRequired)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	plan := buildScalePlan(currentN, currentQdevicePresent, targetN, targetQdeviceRequired)

	targetNode := deps.Node
	if cmd.Flags().Changed("node") {
		targetNode = nodeFlag
	}

	// Capacity gate (plan §9 step 1, BEFORE step 2's QDevice removal): a
	// refusal here means zero mutations have happened yet — in particular
	// the QDevice has not been removed — so the cluster is never left
	// witness-less by a capacity refusal partway through. Only relevant
	// when this run will create a VM shell (buildCreatePlan's own internal
	// gate check, run again when it actually creates VMs below, is
	// redundant but harmless against the same live state).
	if len(plan.growIndices) > 0 || plan.qdeviceAddNeeded {
		if targetNode == "" {
			return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
		}
		capNote, capErr := createCapacityGate(ctx, deps, eff, targetNode, force)
		if capErr != nil {
			return capErr
		}
		if capNote != "" {
			preflightRows = append(preflightRows, []string{"capacity gate", capNote})
		}
	}

	if !yes {
		ok, cerr := confirmYesNo(cmd, fmt.Sprintf(
			"Scale lab %q: %d node(s) (qdevice=%v) -> %d node(s) (qdevice=%v)?",
			name, plan.currentN, plan.currentQdevicePresent, plan.targetN, plan.targetQdeviceRequired))
		if cerr != nil {
			return cerr
		}
		if !ok {
			res := output.Result{Message: "Aborted."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		}
	}

	rows, execErr := executeScalePlan(ctx, cmd, deps, lab, eff, plan, targetNode, force)
	rows = append(preflightRows, rows...)

	if len(rows) > 0 {
		headers := []string{"STEP", "STATUS"}
		if rerr := deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format); rerr != nil {
			return rerr
		}
	}
	return execErr
}

// scaleEffectiveLab returns a copy of lab with Topology.Nodes set to
// targetNodes and, when qdeviceChanged (cmd.Flags().Changed("qdevice")),
// Topology.Qdevice set to qdeviceFlag — the same flag-over-config
// copy-then-override pattern applyCreateOverrides uses for `pmx lab
// create`'s own --nodes/--qdevice flags. lab itself is never mutated.
func scaleEffectiveLab(lab *config.Lab, targetNodes int, qdeviceFlag string, qdeviceChanged bool) *config.Lab {
	eff := *lab
	if lab.Topology.NodeOverrides != nil {
		eff.Topology.NodeOverrides = make(map[int]config.LabNodeOverride, len(lab.Topology.NodeOverrides))
		for k, v := range lab.Topology.NodeOverrides {
			eff.Topology.NodeOverrides[k] = v
		}
	}
	eff.Topology.Nodes = targetNodes
	if qdeviceChanged {
		eff.Topology.Qdevice = qdeviceFlag
	}
	return &eff
}

// scaleCopyLabWithTopology returns a copy of lab with Topology.Nodes and
// Topology.Qdevice forced to the given values, for the two buildCreatePlan
// calls executeScalePlan makes: one with qdevice forced to
// config.QdeviceNever (create node VM shells only, deferring QDevice VM
// creation to its own later step per plan §9 step 5's ordering), one with
// it forced to config.QdeviceAuto (create the QDevice VM shell once every
// node join for this transition has completed).
func scaleCopyLabWithTopology(lab *config.Lab, nodes int, qdevice string) *config.Lab {
	lc := *lab
	if lab.Topology.NodeOverrides != nil {
		lc.Topology.NodeOverrides = make(map[int]config.LabNodeOverride, len(lab.Topology.NodeOverrides))
		for k, v := range lab.Topology.NodeOverrides {
			lc.Topology.NodeOverrides[k] = v
		}
	}
	lc.Topology.Nodes = nodes
	lc.Topology.Qdevice = qdevice
	return &lc
}

// scaleCurrentNodeCount returns the lab's live VM-SHELL count from
// classified (every classified.IsQdevice==false entry's Index), requiring
// the present indices to be contiguous from 0 — a gap (e.g. node 0 and node
// 2 exist but not node 1) means the live pool is in a state this command
// cannot safely reason about, and is reported as an error rather than
// guessed at. Returns 0 (no error) when no node VM exists at all — callers
// distinguish "no lab created yet" from "gap" by checking for that specific
// zero return.
//
// This is a VM-EXISTENCE signal only, used for the "has the lab been
// created at all" precondition and (for --dry-run only) the preview
// estimate. It is deliberately NOT used to drive the real run's delta
// computation or its idempotent re-run behavior — see
// scaleCurrentMembership, which reads live corosync state instead,
// specifically because VM-shell existence cannot distinguish "joined" from
// "shell created but never joined."
func scaleCurrentNodeCount(classified []classifiedLabVM) (int, error) {
	present := make(map[int]bool)
	maxIdx := -1
	for _, c := range classified {
		if c.IsQdevice {
			continue
		}
		present[c.Index] = true
		if c.Index > maxIdx {
			maxIdx = c.Index
		}
	}
	if maxIdx < 0 {
		return 0, nil
	}
	for i := 0; i <= maxIdx; i++ {
		if !present[i] {
			return 0, fmt.Errorf(
				"node VMs are not contiguous: node %d is missing while node %d exists; pmx lab scale "+
					"requires a contiguous 0..N-1 node set — resolve the gap manually first (e.g. `pmx lab "+
					"create` to fill it in)", i, maxIdx)
		}
	}
	return maxIdx + 1, nil
}

// scaleCurrentMembership probes node 0's live `pvecm status` to determine
// the lab's ACTUAL current node count and QDevice registration state — the
// ground truth the real run's delta computation, no-op decision, and
// idempotent re-run behavior all depend on (M4-02: VM-shell existence alone
// cannot distinguish "joined" from "shell created but never joined," the
// gap a deferred grow/QDevice-add leaves behind; deriving current state
// from live membership instead means a re-run always correctly resumes
// exactly where the previous run left off). quorate is vacuously true when
// the lab is not clustered at all yet (nothing to be non-quorate about); a
// currently-clustered-but-non-quorate lab is the plan §9 step 1 preflight
// signal callers use to refuse before mutating anything.
func scaleCurrentMembership(deps *cli.Deps, lab *config.Lab, node0IP string) (currentN int, qdevicePresent, quorate bool, err error) {
	probe, perr := runGuestSSH(deps, node0IP, "pvecm status")
	if perr != nil && guestCommandTransportFailed(perr) {
		return 0, false, false, fmt.Errorf("probe node 0 (%s) cluster membership: %w", node0IP, perr)
	}
	st := parsePvecmStatus(probe.Stdout)

	if !st.Clustered {
		return 1, false, true, nil
	}
	if st.ClusterName != lab.Name {
		return 0, false, false, fmt.Errorf(
			"node 0 (%s) is already part of a DIFFERENT cluster (%q); refusing to scale", node0IP, st.ClusterName)
	}

	n := st.NodeCount
	if n < 1 {
		n = 1
	}
	return n, st.HasQdevice, st.Quorate, nil
}

// scaleRenameLegacyNodeZero implements plan decision D3's safety net: if
// node 0's live VM still carries the pre-multi-node bare name
// (`lab-<member>`, no index suffix — resolve.go's legacyLabVMName), rename
// it to `lab-<member>-0` before anything else touches it. Every rebuilt lab
// is born already correctly named (D3 is "superseded by the fleet
// rebuild" for new labs), so this is expected to be a no-op (empty message,
// nil error) in the overwhelming majority of runs; it exists only to catch
// a legacy-named VM that somehow survived. API-only (no ssh).
func scaleRenameLegacyNodeZero(ctx context.Context, deps *cli.Deps, lab *config.Lab, classified []classifiedLabVM) (string, error) {
	vm, found := nodeLabVM(classified, 0)
	if !found {
		return "", nil
	}

	legacyName := legacyLabVMName(lab.Name)
	if vm.Name != legacyName {
		return "", nil
	}

	newName := labNodeVMName(lab.Name, 0)

	target := peppi.Target{
		VMID:  int(vm.VMID),
		Names: []string{lab.Network.VnetID, labPoolID(lab), storageID(lab), lab.DNS.Zone, lab.Name, vm.Name, newName},
	}
	if err := peppi.Guard(target); err != nil {
		return "", err
	}

	vmidStr := strconv.FormatInt(vm.VMID, 10)
	if err := deps.API.Nodes.UpdateQemuConfig(ctx, vm.Node, vmidStr, &nodes.UpdateQemuConfigParams{Name: createPtr(newName)}); err != nil {
		return "", fmt.Errorf("rename legacy VM %d (%q) to %q on node %q: %w", vm.VMID, legacyName, newName, vm.Node, err)
	}

	return fmt.Sprintf("VM %d renamed from %q to %q", vm.VMID, legacyName, newName), nil
}

// scalePlan is the fully-resolved delta between a lab's current live
// topology and its target, computed by buildScalePlan. It drives both the
// --dry-run preview (renderScalePlanPreview) and the real execution
// (executeScalePlan), so the two can never describe a different plan than
// the one that actually runs (against the SAME currentN/currentQdevicePresent
// inputs — see scaleCurrentMembership vs. scaleCurrentNodeCount for why the
// two callers deliberately source those inputs differently).
type scalePlan struct {
	currentN              int
	currentQdevicePresent bool
	targetN               int
	targetQdeviceRequired bool

	// qdeviceRemoveNeeded is true when the QDevice is currently present but
	// the target topology no longer needs it. Multi-node lab plan §9 step 2:
	// this must happen BEFORE any join or delnode in the same transition —
	// never leave the vote count in an odd+witness (Last-Man-Standing)
	// shape, which joining or removing a node while a stale QDevice remains
	// registered risks (e.g. 2+Q -> 3, or 2+Q -> 1).
	qdeviceRemoveNeeded bool

	// growIndices lists node indices to create-and-join, ascending
	// (currentN..targetN-1); empty when targetN <= currentN.
	growIndices []int

	// shrinkIndices lists node indices to evacuate/delnode/destroy,
	// descending — highest index first, per plan §9 step 4; empty when
	// targetN >= currentN. Node 0 is never included (targetN is always
	// >= config.MinTopologyNodes == 1).
	shrinkIndices []int

	// qdeviceAddNeeded is true when the target topology needs a QDevice
	// that is not currently present. Runs AFTER every join/delnode for this
	// transition has completed, per plan §9 step 5.
	qdeviceAddNeeded bool
}

// buildScalePlan computes the delta scalePlan between a lab's current live
// topology (currentN node VMs, currentQdevicePresent) and its target
// (targetN, targetQdeviceRequired) — a pure function, deliberately free of
// any deps/ssh/API dependency, so it can be unit-tested directly.
func buildScalePlan(currentN int, currentQdevicePresent bool, targetN int, targetQdeviceRequired bool) scalePlan {
	p := scalePlan{
		currentN:              currentN,
		currentQdevicePresent: currentQdevicePresent,
		targetN:               targetN,
		targetQdeviceRequired: targetQdeviceRequired,
	}

	p.qdeviceRemoveNeeded = currentQdevicePresent && !targetQdeviceRequired

	if targetN > currentN {
		p.growIndices = make([]int, 0, targetN-currentN)
		for i := currentN; i < targetN; i++ {
			p.growIndices = append(p.growIndices, i)
		}
	} else if targetN < currentN {
		p.shrinkIndices = make([]int, 0, currentN-targetN)
		for i := currentN - 1; i >= targetN; i-- {
			p.shrinkIndices = append(p.shrinkIndices, i)
		}
	}

	p.qdeviceAddNeeded = targetQdeviceRequired && !currentQdevicePresent

	return p
}

// renderScalePlanPreview renders p as a STEP/STATUS table describing every
// action a real (non-dry-run) invocation would take, in the exact order
// executeScalePlan performs them. It never touches deps.Runner or deps.API
// — everything it needs is already captured in p.
func renderScalePlanPreview(name string, p scalePlan) output.Result {
	headers := []string{"STEP", "STATUS"}
	var rows [][]string

	if p.qdeviceRemoveNeeded {
		rows = append(rows, []string{
			"remove QDevice (corosync-level) before any node membership change, then destroy its VM", "would run"})
	}

	if len(p.growIndices) > 0 {
		rows = append(rows, []string{"ensure node 0 is clustered (pvecm create if not already)", "would run"})
		for _, i := range p.growIndices {
			rows = append(rows, []string{fmt.Sprintf("create VM shell for node %d (if missing)", i), "would run"})
			rows = append(rows, []string{
				fmt.Sprintf("join node %d to cluster (once its OS/ssh is provisioned and reachable)", i), "would run"})
		}
	}

	for _, i := range p.shrinkIndices {
		rows = append(rows, []string{fmt.Sprintf("evacuate guests from node %d to node 0", i), "would run"})
		rows = append(rows, []string{fmt.Sprintf("remove node %d from cluster and destroy its VM", i), "would run"})
	}

	if p.qdeviceAddNeeded {
		rows = append(rows, []string{"create QDevice VM shell (if missing)", "would run"})
		rows = append(rows, []string{
			"wire up QDevice (install packages + pvecm qdevice setup, once reachable)", "would run"})
	}

	if p.targetN >= 2 {
		rows = append(rows, []string{"reconcile inner sdn zone peer list", "would run"})
	}
	rows = append(rows, []string{"reconcile NFS storage attachment", "would run"})
	rows = append(rows, []string{"final validation (every target node: quorum, links, storage)", "would run"})
	if len(p.growIndices) > 0 || len(p.shrinkIndices) > 0 {
		rows = append(rows, []string{"PDM remote", "reminder row (no PDM API call)"})
	}

	rows = append(rows, []string{"summary", fmt.Sprintf(
		"scale plan for lab %q: %d -> %d node(s), qdevice %v -> %v (estimate from VM-shell existence; "+
			"the real run re-derives this from live cluster membership)",
		name, p.currentN, p.targetN, p.currentQdevicePresent, p.targetQdeviceRequired)})

	return output.Result{Headers: headers, Rows: rows}
}

// executeScalePlan performs every action p describes, in order, against
// lab/eff (eff carries the --nodes/--qdevice-overridden target topology).
// Every sub-step reuses this package's existing idempotent core logic
// (ensureClusterInit/ensureClusterJoin, qdeviceEnsureWired/
// ensureQdeviceRemoved, sdnEnsureZoneApplied, nfsEnsureAttached,
// buildCreatePlan) rather than re-implementing any of it, so a change to
// one of those cores automatically applies here too.
//
// Returns the STEP/STATUS rows accumulated so far and an error. The rows
// are non-nil (and should be rendered by the caller) even when err is
// non-nil in exactly one case: the transition completed (was not deferred
// waiting on manual OS provisioning) but the final-validation step found a
// non-converged state on at least one target node (M4-05) — every other
// error path returns nil rows, matching this package's existing "an error
// mid-operation propagates immediately, without rendering a partial plan"
// convention.
func executeScalePlan(
	ctx context.Context, cmd *cobra.Command, deps *cli.Deps, lab, eff *config.Lab, p scalePlan,
	targetNode string, force bool,
) ([][]string, error) {
	name := lab.Name
	var rows [][]string
	deferred := false

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return nil, fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	// 1. QDevice-parity-first: remove before any join/delnode (plan §9
	// step 2), then destroy its now-orphaned VM (M4-04 — `pmx lab qdevice
	// remove` deliberately never destroys the VM, but `pmx lab scale` is
	// the topology orchestrator and must not leave a resource leak with no
	// operator signal).
	if p.qdeviceRemoveNeeded {
		msg, err := ensureQdeviceRemoved(deps, lab, name, node0IP)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []string{"qdevice remove", msg})

		destroyRows, err := scaleDestroyQdeviceVM(ctx, deps, lab)
		if err != nil {
			return nil, err
		}
		rows = append(rows, destroyRows...)
	}

	// 2. Grow: create VM shells for every new index, ensure node 0 is
	// clustered, then join newly-reachable nodes serially, deferring (not
	// failing) at the first not-yet-reachable node.
	if len(p.growIndices) > 0 {
		if targetNode == "" {
			return nil, fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
		}

		growEff := scaleCopyLabWithTopology(eff, eff.Topology.Nodes, config.QdeviceNever)
		createPlan, err := buildCreatePlan(ctx, deps, growEff, targetNode, "", "", false, false, force)
		if err != nil {
			return nil, fmt.Errorf("plan new node VM shell(s): %w", err)
		}
		for _, step := range createPlan.steps {
			if step.skip || step.apply == nil {
				continue
			}
			if err := step.apply(ctx); err != nil {
				return nil, fmt.Errorf("%s: %w", step.desc, err)
			}
		}
		rows = append(rows, []string{
			"create node VM shell(s)", fmt.Sprintf("ensured nodes %d-%d", p.currentN, p.targetN-1)})

		clusterMsg, err := ensureClusterInit(deps, lab, name, node0IP)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []string{"cluster init (node 0)", clusterMsg})

		if note := refreshLabContextAfterClusterInit(cmd, deps, lab); note != "" {
			rows = append(rows, []string{"context refresh", note})
		}

		for _, i := range p.growIndices {
			nodeIP, err := labNodeMgmtIP(lab.Network, i)
			if err != nil {
				return nil, fmt.Errorf("resolve node %d mgmt IP: %w", i, err)
			}

			if !scaleProbeReachable(deps, nodeIP) {
				rows = append(rows, []string{
					fmt.Sprintf("join node %d", i),
					fmt.Sprintf(
						"deferred: node %d (%s) is not yet ssh-reachable — provision its OS (lab "+
							"repository provisioning pipeline), confirm ssh works, then re-run `pmx lab "+
							"scale` to join it", i, nodeIP),
				})
				deferred = true
				break // serial order: never join node i+1 before node i.
			}

			joinMsg, err := ensureClusterJoin(deps, lab, name, i, node0IP, nodeIP)
			if err != nil {
				return nil, err
			}
			rows = append(rows, []string{fmt.Sprintf("join node %d", i), joinMsg})
		}
	}

	// 3. Shrink: evacuate, delnode, destroy — highest index first (plan §9
	// step 4). Node 0 is never among shrinkIndices.
	for _, i := range p.shrinkIndices {
		nodeRows, err := scaleEvacuateAndRemoveNode(ctx, deps, lab, i, node0IP)
		if err != nil {
			return nil, err
		}
		rows = append(rows, nodeRows...)
	}

	// 4. QDevice add — after every join/delnode for this transition has
	// completed (plan §9 step 5).
	if p.qdeviceAddNeeded {
		if targetNode == "" {
			return nil, fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
		}

		qdeviceEff := scaleCopyLabWithTopology(eff, eff.Topology.Nodes, config.QdeviceAuto)
		createPlan, err := buildCreatePlan(ctx, deps, qdeviceEff, targetNode, "", "", false, false, force)
		if err != nil {
			return nil, fmt.Errorf("plan QDevice VM shell: %w", err)
		}
		for _, step := range createPlan.steps {
			if step.skip || step.apply == nil {
				continue
			}
			if err := step.apply(ctx); err != nil {
				return nil, fmt.Errorf("%s: %w", step.desc, err)
			}
		}
		rows = append(rows, []string{"create QDevice VM shell", "ensured"})

		qdeviceIP, err := labQdeviceMgmtIP(lab.Network)
		if err != nil {
			return nil, fmt.Errorf("resolve QDevice mgmt IP: %w", err)
		}

		if !scaleProbeReachable(deps, qdeviceIP) {
			rows = append(rows, []string{
				"wire up QDevice",
				fmt.Sprintf(
					"deferred: QDevice VM (%s) is not yet ssh-reachable — provision its OS then re-run "+
						"`pmx lab scale` to wire it up", qdeviceIP),
			})
			deferred = true
		} else {
			clusterProbe, cerr := runGuestSSH(deps, node0IP, "pvecm status")
			if cerr != nil && guestCommandTransportFailed(cerr) {
				return nil, fmt.Errorf("probe node 0 (%s) cluster state: %w", node0IP, cerr)
			}
			cst := parsePvecmStatus(clusterProbe.Stdout)

			wireSteps, err := qdeviceEnsureWired(
				deps, lab, name, qdeviceIP, node0IP, config.EffectiveTopologyNodes(eff.Topology), cst.HasQdevice)
			if err != nil {
				return nil, err
			}
			for _, s := range wireSteps {
				status := "done"
				if s.skip {
					status = "skip (already satisfied)"
				}
				rows = append(rows, []string{s.desc, status})
			}
		}
	}

	// 5. Reconcile: inner SDN zone peer list (multi-node labs only), then
	// NFS storage attachment (every lab, regardless of node count).
	finalN := config.EffectiveTopologyNodes(eff.Topology)
	if finalN >= 2 {
		peerIPs := make([]string, 0, finalN)
		for i := 0; i < finalN; i++ {
			ip, err := labNodeMgmtIP(lab.Network, i)
			if err != nil {
				return nil, fmt.Errorf("resolve node %d mgmt IP: %w", i, err)
			}
			peerIPs = append(peerIPs, ip)
		}
		sdnRows, err := sdnEnsureZoneApplied(deps, name, node0IP, strings.Join(peerIPs, ","))
		if err != nil {
			return nil, err
		}
		rows = append(rows, sdnRows...)
	}

	server, err := nfsServerIP(lab.Network)
	if err != nil {
		return nil, fmt.Errorf("resolve NFS server IP: %w", err)
	}
	nfsRows, err := nfsEnsureAttached(deps, name, node0IP, server, nfsAttachTargets(lab.Name))
	if err != nil {
		return nil, err
	}
	rows = append(rows, nfsRows...)

	// 6. Final validation (plan §9 step 7, amended 2026-07-17): `pvecm
	// status` expected=total, `corosync-cfgtool -s` links, and `pvesm
	// status` storage-active, checked on EVERY node of the target topology
	// — not just node 0. The live-migration acceptance test the plan also
	// names is deliberately not automated here (milestone QA, first live
	// exercise: the M6 pve-cpi capstone — see this command's Long help).
	allHealthy := true
	for i := 0; i < finalN; i++ {
		nodeIP, err := labNodeMgmtIP(lab.Network, i)
		if err != nil {
			return nil, fmt.Errorf("resolve node %d mgmt IP: %w", i, err)
		}
		summary, healthy := scaleValidateNode(deps, nodeIP)
		rows = append(rows, []string{fmt.Sprintf("final validation node %d", i), summary})
		if !healthy {
			allHealthy = false
		}
	}

	// 7. PDM reminder (plan §9 step 6, ruled 2026-07-17 out of `pmx lab
	// scale`'s own scope — no PDM write API exists to call): surfaced only
	// when the node count actually changed this run.
	if len(p.growIndices) > 0 || len(p.shrinkIndices) > 0 {
		rows = append(rows, []string{"PDM remote", fmt.Sprintf(
			"reminder: node count changed (%d -> %d) — swap this lab's PDM remote (single-host vs. "+
				"cluster endpoint) via the lab repository's own PDM tooling (plan §5.5); `pmx lab scale` "+
				"does not do this", p.currentN, p.targetN)})
	}

	rows = append(rows, []string{"summary", fmt.Sprintf(
		"lab %q scale requested: %d -> %d node(s), qdevice %v -> %v.",
		name, p.currentN, p.targetN, p.currentQdevicePresent, p.targetQdeviceRequired)})

	if !deferred && !allHealthy {
		return rows, fmt.Errorf(
			"lab %q: scale actions completed but final validation found a non-converged state on at "+
				"least one target node (quorum, corosync links, or storage) — see the final-validation "+
				"rows above", name)
	}

	return rows, nil
}

// scaleValidateNode probes nodeIP for `pvecm status`, `corosync-cfgtool -s`
// (only when clustered), and `pvesm status`, returning a one-line summary
// and whether everything it could check reported healthy. A node that is
// unreachable (transport failure on any of the three probes) is reported
// as such and treated as unhealthy — never as a fatal error: the final-
// validation loop must be able to report EVERY target node's state even
// when one of them is legitimately not provisioned yet (a deferred grow/
// QDevice-add), leaving the deferred-vs-completed distinction to
// executeScalePlan's own `deferred` tracking rather than to this function.
func scaleValidateNode(deps *cli.Deps, nodeIP string) (summary string, healthy bool) {
	// Every probe below treats ANY error (not just guestCommandTransportFailed's
	// narrower "non-ExitError" class) as "not reachable": unlike this
	// package's idempotency probes, where a plain non-zero exit meaningfully
	// signals "resource not found yet, safe to proceed," there is no such
	// interpretation for a final-validation health check — an unreachable
	// ssh connection to a real host also exits via a normal (non-zero)
	// process exit code (openssh's own connection-failure exit status),
	// which guestCommandTransportFailed would NOT classify as a transport
	// failure, so using it here would silently misread "genuinely
	// unreachable" as "reachable but reports nothing" and keep probing with
	// stale/empty output instead of stopping.
	statusRes, serr := runGuestSSH(deps, nodeIP, "pvecm status")
	if serr != nil {
		return "not reachable", false
	}
	st := parsePvecmStatus(statusRes.Stdout)

	linkSummary := "n/a (not clustered)"
	linksOK := true
	if st.Clustered {
		linksRes, lerr := runGuestSSH(deps, nodeIP, "corosync-cfgtool -s")
		if lerr != nil {
			linkSummary = "not reachable"
			linksOK = false
		} else {
			allUp, statuses := parseCorosyncLinks(linksRes.Stdout)
			linksOK = allUp
			switch {
			case allUp:
				linkSummary = "all links connected"
			case len(statuses) == 0:
				linkSummary = "no link status parsed"
			default:
				linkSummary = fmt.Sprintf("degraded: %s", strings.Join(statuses, ", "))
			}
		}
	}

	// Every branch below assigns storageSummary, so it carries no default:
	// a placeholder here would only ever mask a path that forgot to set it.
	var storageSummary string

	storageOK := true
	pvesmRes, perr := runGuestSSH(deps, nodeIP, "pvesm status")
	if perr != nil {
		storageSummary = "not reachable"
		storageOK = false
	} else {
		storages := parsePvesmStatus(pvesmRes.Stdout)
		var inactive []string
		for id, status := range storages {
			if status != "active" {
				inactive = append(inactive, fmt.Sprintf("%s=%s", id, status))
			}
		}
		sort.Strings(inactive)
		if len(inactive) == 0 {
			storageSummary = "all storage active"
		} else {
			storageOK = false
			storageSummary = "inactive: " + strings.Join(inactive, ", ")
		}
	}

	votesOK := !st.Clustered || (st.Quorate && st.ExpectedVotes == st.TotalVotes)
	healthy = votesOK && linksOK && storageOK

	summary = fmt.Sprintf("quorate=%v expected=%d total=%d qdevice=%v links=%s storage=%s",
		st.Quorate, st.ExpectedVotes, st.TotalVotes, st.HasQdevice, linkSummary, storageSummary)
	return summary, healthy
}

// scaleDestroyQdeviceVM destroys the lab's QDevice VM if one still exists
// (M4-04): `pmx lab qdevice remove` deliberately never destroys the VM
// (M3 design), but `pmx lab scale` is the topology orchestrator — leaving
// an orphaned QDevice VM (still consuming its full reservation) with no
// operator signal after a QDevice-removing transition is a resource leak
// this command must close. Idempotent: reports "already gone" rather than
// erroring when no QDevice VM is found. Reuses destroy.go's stop-then-
// delete machinery, peppi-guarded by VMID once it is known.
func scaleDestroyQdeviceVM(ctx context.Context, deps *cli.Deps, lab *config.Lab) ([][]string, error) {
	vms, err := listLiveVMs(ctx, deps)
	if err != nil {
		return nil, err
	}
	classified, err := findLabVMs(vms, labPoolID(lab), lab.Name)
	if err != nil {
		return nil, fmt.Errorf("lab %q: %w", lab.Name, err)
	}
	vm, found := qdeviceLabVM(classified)
	if !found {
		return [][]string{{"destroy QDevice VM", "already gone"}}, nil
	}

	target := peppi.Target{
		VMID:  int(vm.VMID),
		Names: []string{lab.Network.VnetID, labPoolID(lab), storageID(lab), lab.DNS.Zone, lab.Name},
	}
	if err := peppi.Guard(target); err != nil {
		return nil, err
	}

	if err := destroyStopIfRunning(ctx, deps.API, vm.Node, int(vm.VMID)); err != nil {
		return nil, fmt.Errorf("stop QDevice VM %d before destroy: %w", vm.VMID, err)
	}
	if err := destroyDeleteVM(ctx, deps.API, vm.Node, int(vm.VMID)); err != nil {
		return nil, fmt.Errorf("destroy QDevice VM %d: %w", vm.VMID, err)
	}
	return [][]string{{"destroy QDevice VM", fmt.Sprintf("VM %d destroyed", vm.VMID)}}, nil
}

// scaleProbeReachable reports whether host answers ssh at all (a trivial
// `true` remote command). Any error — transport failure or a remote
// command failure alike — means "not reachable yet"; unlike this package's
// other idempotency probes, there is no "absent means proceed" distinction
// to make here: an unreachable node is never safe to join/wire, regardless
// of why it is unreachable.
func scaleProbeReachable(deps *cli.Deps, host string) bool {
	_, err := runGuestSSH(deps, host, "true")
	return err == nil
}

// qmListVMIDRE matches the VMID (first column) of one `qm list` data row.
var qmListVMIDRE = regexp.MustCompile(`^\s*(\d+)\s+`)

// parseQmListVMIDs returns every guest VMID listed in `qm list`'s plain-text
// output (skipping the header row).
func parseQmListVMIDs(output string) []string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var ids []string
	for i, line := range lines {
		if i == 0 {
			continue // header row
		}
		if m := qmListVMIDRE.FindStringSubmatch(line); m != nil {
			ids = append(ids, m[1])
		}
	}
	return ids
}

// scaleNodeStillMember reports whether nodeIP still appears in node 0's
// `pvecm status` output — corosync's Membership information section lists
// each member by its link0 IP address, not hostname, so nodeIP (not the
// node's PVE hostname) is what to search for. Used only to disambiguate a
// failed `pvecm delnode` call: plan §9 step 4 documents "'Could not kill
// node' on re-run means it's already gone — treat as success (idempotency)",
// but pvecm's exact error text is not a stable, documented interface to
// pattern-match against, so this re-probes live membership state directly
// instead.
func scaleNodeStillMember(deps *cli.Deps, node0IP, nodeIP string) (bool, error) {
	res, err := runGuestSSH(deps, node0IP, "pvecm status")
	if err != nil && guestCommandTransportFailed(err) {
		return false, err
	}
	return strings.Contains(res.Stdout, nodeIP), nil
}

// scaleEvacuateAndRemoveNode evacuates every L2 guest running on node idx's
// own nested PVE instance (migrating each to node 0 — the survivor every
// scale-down keeps, per plan §9's worked transitions), removes idx from
// cluster membership via `pvecm delnode` (idempotent — see
// scaleNodeStillMember), then destroys its VM (peppi-guarded a second time
// now that its VMID is known, per resolve.go's resolveLabForMutate
// contract). Dropping the departing node's tailnet SNAT rule
// (scripts/50-tailnet-snat) is a lab-repository host-side script
// responsibility, outside this Go CLI's scope.
func scaleEvacuateAndRemoveNode(ctx context.Context, deps *cli.Deps, lab *config.Lab, idx int, node0IP string) ([][]string, error) {
	var rows [][]string

	nodeIP, err := labNodeMgmtIP(lab.Network, idx)
	if err != nil {
		return nil, fmt.Errorf("resolve node %d mgmt IP: %w", idx, err)
	}
	// node0Hostname (the MIGRATE TARGET — every guest evacuates TO node 0,
	// M4-01) is deliberately a different value from nodeHostname (the
	// DEPARTING node's own hostname, used only for delnode): migrating a
	// guest to the node it already runs on fails in real PVE.
	node0Hostname := labNodeVMName(lab.Name, 0)
	nodeHostname := labNodeVMName(lab.Name, idx)

	// --with-local-disks additionally migrates any LVM-thin-backed disk; it
	// is a no-op for a disk already on shared (NFS) storage, so issuing it
	// unconditionally for every guest — rather than inspecting each disk's
	// storage backend first to pick between the two command shapes plan §9
	// step 4 describes — is safe and simpler, and achieves the identical
	// outcome (live migration, no unnecessary disk copy for NFS-backed
	// guests, a full disk copy only for guests that actually need one).
	qmRes, err := runGuestSSH(deps, nodeIP, "qm list")
	if err != nil {
		return nil, fmt.Errorf("list guests on node %d (%s) before removal: %w", idx, nodeIP, err)
	}
	vmids := parseQmListVMIDs(qmRes.Stdout)
	for _, vmid := range vmids {
		migrateCmd := fmt.Sprintf("qm migrate %s %s --online --with-local-disks", vmid, node0Hostname)
		if _, err := runGuestSSH(deps, nodeIP, migrateCmd); err != nil {
			return nil, fmt.Errorf("evacuate guest %s from node %d (%s) to node 0 (%s): %w",
				vmid, idx, nodeIP, node0Hostname, err)
		}
	}
	if len(vmids) > 0 {
		rows = append(rows, []string{
			fmt.Sprintf("evacuate node %d guests", idx), fmt.Sprintf("migrated %d guest(s) to node 0", len(vmids))})
	} else {
		rows = append(rows, []string{fmt.Sprintf("evacuate node %d guests", idx), "none present"})
	}

	delnodeCmd := fmt.Sprintf("pvecm delnode %s", nodeHostname)
	if _, derr := runGuestSSH(deps, node0IP, delnodeCmd); derr != nil {
		stillMember, perr := scaleNodeStillMember(deps, node0IP, nodeIP)
		if perr != nil {
			return nil, fmt.Errorf("delnode %s failed and could not confirm membership afterward: %w", nodeHostname, derr)
		}
		if stillMember {
			return nil, fmt.Errorf("delnode %s from node 0 (%s): %w", nodeHostname, node0IP, derr)
		}
		// Already gone — idempotent success (plan §9 step 4).
	}
	rows = append(rows, []string{fmt.Sprintf("delnode %d (%s)", idx, nodeHostname), "removed from cluster membership"})

	vms, err := listLiveVMs(ctx, deps)
	if err != nil {
		return nil, err
	}
	classified, err := findLabVMs(vms, labPoolID(lab), lab.Name)
	if err != nil {
		return nil, fmt.Errorf("lab %q: %w", lab.Name, err)
	}
	vm, found := nodeLabVM(classified, idx)
	if !found {
		rows = append(rows, []string{fmt.Sprintf("destroy node %d VM", idx), "already gone"})
		return rows, nil
	}

	target := peppi.Target{
		VMID:  int(vm.VMID),
		Names: []string{lab.Network.VnetID, labPoolID(lab), storageID(lab), lab.DNS.Zone, lab.Name},
	}
	if err := peppi.Guard(target); err != nil {
		return nil, err
	}

	if err := destroyStopIfRunning(ctx, deps.API, vm.Node, int(vm.VMID)); err != nil {
		return nil, fmt.Errorf("stop node %d VM %d before destroy: %w", idx, vm.VMID, err)
	}
	if err := destroyDeleteVM(ctx, deps.API, vm.Node, int(vm.VMID)); err != nil {
		return nil, fmt.Errorf("destroy node %d VM %d: %w", idx, vm.VMID, err)
	}
	rows = append(rows, []string{fmt.Sprintf("destroy node %d VM", idx), fmt.Sprintf("VM %d destroyed", vm.VMID)})

	return rows, nil
}
