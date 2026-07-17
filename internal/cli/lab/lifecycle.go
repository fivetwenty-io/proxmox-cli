package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
)

// labVM is the decoded shape of one /cluster/resources entry of type "vm"
// that this file cares about: enough to join a configured lab against its
// live QEMU guest(s) by resource-pool membership, and to classify which
// node/QDevice role each one plays by its live name. The endpoint also
// returns lxc guests under type=vm; Type is kept so callers can filter those
// out.
type labVM struct {
	VMID   int64  `json:"vmid"`
	Node   string `json:"node"`
	Pool   string `json:"pool"`
	Status string `json:"status"`
	Type   string `json:"type"`
	Name   string `json:"name"`
}

// listLiveVMs queries GET /cluster/resources for every QEMU guest in the
// cluster, across every node, so list/status/start/stop can each join a
// configured lab against its live VM(s) by resource-pool membership rather
// than by a stored VMID (labs carry no VMID field in config). The
// node-scoped GET /nodes/{node}/qemu endpoint is not used here because it
// would require already knowing which node a lab's VM lives on.
func listLiveVMs(ctx context.Context, deps *cli.Deps) ([]labVM, error) {
	typeVM := "vm"
	resp, err := deps.API.Cluster.ListResources(ctx, &pvecluster.ListResourcesParams{Type: &typeVM})
	if err != nil {
		return nil, fmt.Errorf("list cluster VMs: %w", err)
	}
	if resp == nil {
		return nil, nil
	}

	vms := make([]labVM, 0, len(*resp))
	for _, raw := range *resp {
		var vm labVM
		if err := json.Unmarshal(raw, &vm); err != nil {
			return nil, fmt.Errorf("decode cluster resource entry: %w", err)
		}
		// cluster/resources type=vm returns both qemu and lxc guests; a lab's
		// VMs are always qemu, never lxc.
		if vm.Type != "qemu" {
			continue
		}
		vms = append(vms, vm)
	}
	return vms, nil
}

// classifyLabVMName matches vmName against labName's node/QDevice naming
// convention (resolve.go): "lab-<labName>-<i>" for i in
// [0, maxLabNodeIndex] classifies as node i; "lab-<labName>-q" classifies as
// the QDevice; and the legacy bare "lab-<labName>" (no index suffix, the
// pre-multi-node convention) classifies as node 0, for back-compat with a
// lab created before topology.nodes existed (multi-node lab plan §3.2,
// decision D3's safety-net case). ok is false when vmName matches none of
// these.
func classifyLabVMName(vmName, labName string) (isQdevice bool, index int, ok bool) {
	if vmName == legacyLabVMName(labName) {
		return false, 0, true
	}
	if vmName == labQdeviceVMName(labName) {
		return true, 0, true
	}
	for i := 0; i <= maxLabNodeIndex; i++ {
		if vmName == labNodeVMName(labName, i) {
			return false, i, true
		}
	}
	return false, 0, false
}

// classifiedLabVM pairs one live pool-member VM with the topology role its
// name identifies: a specific node index, or the QDevice.
type classifiedLabVM struct {
	VM        labVM
	IsQdevice bool
	Index     int // valid only when !IsQdevice
}

// findLabVMs returns every qemu guest in vms that belongs to pool,
// classified by its live name against labName's node/QDevice naming
// convention (classifyLabVMName). It errors when any pool member's name
// matches none of the convention's patterns — an unclassifiable VM sharing
// the lab's pool is a data problem the operator must resolve, not a case
// lifecycle verbs can safely guess past — or when two members classify to
// the same role (e.g. a legacy bare-named VM and an explicit "-0"-named VM
// present in the pool at once), which is equally ambiguous.
func findLabVMs(vms []labVM, pool, labName string) ([]classifiedLabVM, error) {
	var out []classifiedLabVM
	seen := make(map[string]labVM)

	for _, vm := range vms {
		if vm.Pool != pool {
			continue
		}

		isQdevice, index, ok := classifyLabVMName(vm.Name, labName)
		if !ok {
			return nil, fmt.Errorf(
				"lab pool %q member VM %d (name %q) does not match the node/QDevice naming convention "+
					"for lab %q (expected lab-%s-<0..%d> or lab-%s-q)",
				pool, vm.VMID, vm.Name, labName, labName, maxLabNodeIndex, labName)
		}

		key := "q"
		if !isQdevice {
			key = strconv.Itoa(index)
		}
		if dup, exists := seen[key]; exists {
			return nil, fmt.Errorf(
				"lab pool %q has two VMs claiming the same role %q: VM %d (%q) and VM %d (%q)",
				pool, key, dup.VMID, dup.Name, vm.VMID, vm.Name)
		}
		seen[key] = vm

		out = append(out, classifiedLabVM{VM: vm, IsQdevice: isQdevice, Index: index})
	}

	return out, nil
}

// nodeLabVM returns the classified VM for node index idx among classified,
// if present.
func nodeLabVM(classified []classifiedLabVM, idx int) (labVM, bool) {
	for _, c := range classified {
		if !c.IsQdevice && c.Index == idx {
			return c.VM, true
		}
	}
	return labVM{}, false
}

// qdeviceLabVM returns the classified QDevice VM among classified, if
// present.
func qdeviceLabVM(classified []classifiedLabVM) (labVM, bool) {
	for _, c := range classified {
		if c.IsQdevice {
			return c.VM, true
		}
	}
	return labVM{}, false
}

// lifecycleTarget is one node index or the QDevice that a lifecycle verb
// (start/stop/status) can act against.
type lifecycleTarget struct {
	// label identifies the target in output and in --node matching: "0"
	// through "4" for a node, "q" for the QDevice.
	label  string
	isNode bool
	index  int // valid only when isNode
}

// lookup returns the live VM classified for t among classified, if any.
func (t lifecycleTarget) lookup(classified []classifiedLabVM) (labVM, bool) {
	if t.isNode {
		return nodeLabVM(classified, t.index)
	}
	return qdeviceLabVM(classified)
}

// lifecycleTargetsForLab returns every target a lifecycle verb should act
// against for lab, in start order: node 0, 1, …, N-1 (N =
// config.EffectiveTopologyNodes(lab.Topology)), then the QDevice when
// config.QdeviceRequired(lab.Topology) is true (multi-node lab plan §4.4).
// stop callers reverse this slice (reverseLifecycleTargets) so the QDevice
// (if present) stops first and node 0 stops last.
func lifecycleTargetsForLab(l *config.Lab) []lifecycleTarget {
	n := config.EffectiveTopologyNodes(l.Topology)
	targets := make([]lifecycleTarget, 0, n+1)
	for i := 0; i < n; i++ {
		targets = append(targets, lifecycleTarget{label: strconv.Itoa(i), isNode: true, index: i})
	}
	if config.QdeviceRequired(l.Topology) {
		targets = append(targets, lifecycleTarget{label: "q", isNode: false})
	}
	return targets
}

// reverseLifecycleTargets returns a new slice holding targets in reverse
// order, for `stop`'s "QDevice (if any), then N-1 down to 0" sequencing.
func reverseLifecycleTargets(targets []lifecycleTarget) []lifecycleTarget {
	out := make([]lifecycleTarget, len(targets))
	for i, t := range targets {
		out[len(targets)-1-i] = t
	}
	return out
}

// lifecycleTargetLabels returns the comma-joined labels of targets, for a
// --node error message listing the valid choices.
func lifecycleTargetLabels(targets []lifecycleTarget) string {
	labels := make([]string, len(targets))
	for i, t := range targets {
		labels[i] = t.label
	}
	return strings.Join(labels, ", ")
}

// filterLifecycleTargets returns all when nodeFlag is empty, else the single
// target among all whose label matches nodeFlag exactly. An unmatched,
// non-empty nodeFlag is a caller error naming the valid choices.
func filterLifecycleTargets(all []lifecycleTarget, nodeFlag string) ([]lifecycleTarget, error) {
	if nodeFlag == "" {
		return all, nil
	}
	for _, t := range all {
		if t.label == nodeFlag {
			return []lifecycleTarget{t}, nil
		}
	}
	return nil, fmt.Errorf("--node %q does not name a configured node/QDevice for this lab (valid: %s)",
		nodeFlag, lifecycleTargetLabels(all))
}

// guardVMID re-runs peppi.Guard now that a concrete VMID has been resolved
// for lab, per resolve.go's resolveLabForMutate contract: every mutating
// verb that subsequently learns a lab's VMID (as start/stop do, by locating
// the live VM in the lab's pool) must guard again with that VMID before
// issuing any mutating call against it.
func guardVMID(lab *config.Lab, vmid int64) error {
	return peppi.Guard(peppi.Target{
		VMID: int(vmid),
		Names: []string{
			lab.Network.VnetID,
			labPoolID(lab),
			storageID(lab),
			lab.DNS.Zone,
			lab.Name,
		},
	})
}

// newListCmd builds `pmx lab list`.
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every configured lab and its live VM state",
		Long: "List every lab defined in config (inline `labs:` plus `labs_dir`/`include` " +
			"includes), joined with the live state of its node-0 VM in the configured PVE " +
			"cluster: present or absent, running or stopped, VMID, and node. A lab whose " +
			"node-0 VM has not been created yet, or was destroyed, shows an absent state " +
			"rather than an error. Use `pmx lab status <name>` for a full per-node/QDevice " +
			"breakdown of a multi-node lab.",
		Example: `  pmx lab list
  pmx lab list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			labs, err := config.ResolveLabs(deps.Cfg, deps.ConfigPath)
			if err != nil {
				return fmt.Errorf("resolve labs: %w", err)
			}

			vms, err := listLiveVMs(cmd.Context(), deps)
			if err != nil {
				return err
			}

			names := make([]string, 0, len(labs))
			for name := range labs {
				names = append(names, name)
			}
			sort.Strings(names)

			headers := []string{"NAME", "POOL", "NODES", "VMID", "NODE", "STATUS"}
			rows := make([][]string, 0, len(names))

			for _, name := range names {
				l := labs[name]
				pool := labPoolID(l)
				nodeCount := strconv.Itoa(config.EffectiveTopologyNodes(l.Topology))

				classified, cerr := findLabVMs(vms, pool, l.Name)
				if cerr != nil {
					return fmt.Errorf("lab %q: %w", name, cerr)
				}

				vmidStr, node, status := "", "", "absent"
				if vm, found := nodeLabVM(classified, 0); found {
					vmidStr = strconv.FormatInt(vm.VMID, 10)
					node = vm.Node
					status = vm.Status
				}

				rows = append(rows, []string{name, pool, nodeCount, vmidStr, node, status})
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
		},
	}
}

// agentNetworkInterfaces is the decoded shape of the QEMU guest agent's
// "network-get-interfaces" QMP command result. Prefix is a pointer because
// older agents may omit it; consumers must treat a nil prefix as unknown.
type agentNetworkInterfaces struct {
	Result []struct {
		Name        string `json:"name"`
		IPAddresses []struct {
			IPAddress     string `json:"ip-address"`
			IPAddressType string `json:"ip-address-type"`
			Prefix        *int   `json:"prefix"`
		} `json:"ip-addresses"`
	} `json:"result"`
}

// guestAgentInterfaces asks the QEMU guest agent for the VM's network
// interfaces and returns the decoded result. It returns (nil, false) on any
// failure — agent not installed, not running, or the call erroring — rather
// than propagating an error, since a missing guest agent is an expected,
// non-fatal state that status falls back from to the lab's configured
// management IP.
func guestAgentInterfaces(ctx context.Context, deps *cli.Deps, node, vmid string) (*agentNetworkInterfaces, bool) {
	resp, err := deps.API.Nodes.ListQemuAgentNetworkGetInterfaces(ctx, node, vmid)
	if err != nil || resp == nil {
		return nil, false
	}

	var parsed agentNetworkInterfaces
	if err := json.Unmarshal(*resp, &parsed); err != nil {
		return nil, false
	}
	return &parsed, true
}

// firstIPv4 returns the first non-loopback IPv4 address in the agent result.
func (a *agentNetworkInterfaces) firstIPv4() (string, bool) {
	for _, iface := range a.Result {
		if iface.Name == "lo" {
			continue
		}
		for _, addr := range iface.IPAddresses {
			if addr.IPAddressType == "ipv4" && addr.IPAddress != "" {
				return addr.IPAddress, true
			}
		}
	}
	return "", false
}

// statusRow renders one row of `pmx lab status`'s per-node/QDevice table for
// tgt: VM state, node, agent-reported (falling back to configured) IP, the
// target's configured sizing, and a network-prefix hazard warning (see
// netplan.go's guestPrefixWarning) when the guest agent reports an in-cidr
// interface whose prefix is narrower than network.cidr. An absent target (no
// live VM classified for it yet) still reports its configured sizing,
// matching status's existing "absent VM shows config-derived sizing"
// contract.
func statusRow(ctx context.Context, deps *cli.Deps, l *config.Lab, tgt lifecycleTarget, classified []classifiedLabVM) ([]string, error) {
	vcpu, memMaxGB := "-", "-"
	if tgt.isNode {
		compute, _ := config.EffectiveNodeSizing(l, tgt.index)
		vcpu, memMaxGB = strconv.Itoa(compute.VCPU), strconv.Itoa(compute.Memory.MaxGB)
	}

	configuredIP := "-"
	if tgt.isNode {
		if ip, err := labNodeMgmtIP(l.Network, tgt.index); err == nil {
			configuredIP = ip
		}
	} else if ip, err := labQdeviceMgmtIP(l.Network); err == nil {
		configuredIP = ip
	}

	vm, found := tgt.lookup(classified)
	if !found {
		return []string{tgt.label, "", "", "absent", configuredIP, "n/a", vcpu, memMaxGB, ""}, nil
	}

	vmid := strconv.FormatInt(vm.VMID, 10)

	current, err := deps.API.Nodes.ListQemuStatusCurrent(ctx, vm.Node, vmid)
	if err != nil {
		return nil, fmt.Errorf("get status for lab %q target %s VM %s on node %q: %w", l.Name, tgt.label, vmid, vm.Node, err)
	}
	if current == nil {
		return nil, fmt.Errorf("get status for lab %q target %s VM %s on node %q: empty response", l.Name, tgt.label, vmid, vm.Node)
	}

	ip := configuredIP
	agent := "n/a"
	warning := ""
	if current.Status == "running" {
		if ifaces, ok := guestAgentInterfaces(ctx, deps, vm.Node, vmid); ok {
			agent = "ok"
			if liveIP, ok2 := ifaces.firstIPv4(); ok2 {
				ip = liveIP
			}
			if w, ok2 := guestPrefixWarning(ifaces, l.Network.CIDR); ok2 {
				warning = w
			}
		} else {
			agent = "no response"
		}
	}

	return []string{tgt.label, vmid, vm.Node, current.Status, ip, agent, vcpu, memMaxGB, warning}, nil
}

// newStatusCmd builds `pmx lab status <name>`.
func newStatusCmd() *cobra.Command {
	var nodeFlag string

	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show a lab's per-node power state, IP, and sizing",
		Long: "Show one row per configured node (and, when the lab's topology calls for one, " +
			"the QDevice tie-breaker VM): live power state, PVE host node, IP address " +
			"(guest-agent-reported when running and the agent responds, else the target's " +
			"configured management IP), guest-agent responsiveness, and configured vCPU/" +
			"memory sizing. A target whose VM has not been created yet is reported as absent, " +
			"with its configured sizing shown in place of live values, and does not cause the " +
			"command to fail. Pass --node to show only one node index (0-4) or \"q\" for the " +
			"QDevice.",
		Example: `  pmx lab status wayne
  pmx lab status wayne --node 0
  pmx lab status wayne --node q`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			l, err := resolveLab(cmd, name)
			if err != nil {
				return err
			}

			vms, err := listLiveVMs(cmd.Context(), deps)
			if err != nil {
				return err
			}

			pool := labPoolID(l)
			classified, err := findLabVMs(vms, pool, l.Name)
			if err != nil {
				return fmt.Errorf("lab %q: %w", name, err)
			}

			targets, err := filterLifecycleTargets(lifecycleTargetsForLab(l), nodeFlag)
			if err != nil {
				return err
			}

			headers := []string{"NODE", "VMID", "PVE_NODE", "STATUS", "IP", "AGENT", "VCPU", "MEMORY_MAX_GB", "WARNING"}
			rows := make([][]string, 0, len(targets)+1)
			for _, tgt := range targets {
				row, err := statusRow(cmd.Context(), deps, l, tgt, classified)
				if err != nil {
					return err
				}
				rows = append(rows, row)
			}

			// Rendered as a trailing row, not via output.Result.Message: every
			// renderer in internal/output (table, plain, JSON, YAML) drops
			// Message whenever Headers/Rows are also set, which this table
			// always is, so a Message-only summary here would never reach the
			// operator in any output format (the same defect create.go's
			// renderCreatePlan hit and fixed the same way).
			summaryRow := make([]string, len(headers))
			summaryRow[0] = "summary"
			summaryRow[len(summaryRow)-1] = fmt.Sprintf(
				"Lab %q: %d node(s) configured, pool %q.", name, config.EffectiveTopologyNodes(l.Topology), pool)
			rows = append(rows, summaryRow)

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&nodeFlag, "node", "", "scope to one node index (0-4) or \"q\" for the QDevice")
	return cmd
}

// lifecycleAction describes one of the start/stop verbs: the state that
// makes the action a no-op, its human-readable verb forms, and the API call
// that performs it.
type lifecycleAction struct {
	// verb is the present-tense verb used in dry-run and error messages
	// ("start", "stop").
	verb string
	// pastVerb is the past-tense verb used in the success message
	// ("started", "stopped").
	pastVerb string
	// noopState is the PVE power state ("running", "stopped") in which this
	// action is already satisfied and must not be repeated.
	noopState string
	// reverse selects stop's node-order reversal (QDevice first if present,
	// then N-1 down to 0) versus start's natural order (0, 1, …, N-1, then
	// QDevice).
	reverse bool
	// invoke issues the actual mutating API call and returns its raw task
	// response — normally a JSON-encoded UPID string, but possibly null or
	// empty on older servers.
	invoke func(ctx context.Context, deps *cli.Deps, node, vmid string) (json.RawMessage, error)
}

// runLifecycleMutate implements the shared start/stop control flow:
// resolve-and-guard the lab, classify every live VM in its pool by
// node/QDevice role, walk the lab's targets in the action's order (or the
// single target --node selects), guarding each concrete VMID again before
// acting, skipping idempotently where the action is already satisfied,
// previewing under --dry-run, or else invoking the action and blocking on
// its task. A target with no VM found is skipped silently when acting
// against the whole topology (a partially-created lab is not itself an
// error for start/stop), but errors immediately when --node named that
// specific, missing target. The overall command errors only when --node
// named a target with no VM, or when none of the lab's targets have any VM
// at all.
func runLifecycleMutate(cmd *cobra.Command, name string, dryRun bool, nodeFlag string, action lifecycleAction) error {
	deps := cli.GetDeps(cmd)

	l, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	vms, err := listLiveVMs(cmd.Context(), deps)
	if err != nil {
		return err
	}

	pool := labPoolID(l)
	classified, err := findLabVMs(vms, pool, l.Name)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}

	targets := lifecycleTargetsForLab(l)
	if action.reverse {
		targets = reverseLifecycleTargets(targets)
	}
	targets, err = filterLifecycleTargets(targets, nodeFlag)
	if err != nil {
		return err
	}

	var lines []string
	var anyFound bool

	for _, tgt := range targets {
		vm, found := tgt.lookup(classified)
		if !found {
			if nodeFlag != "" {
				return fmt.Errorf("lab %q: no VM found for node %q in pool %q; run `pmx lab create %s` first",
					name, tgt.label, pool, name)
			}
			continue
		}
		anyFound = true

		if err := guardVMID(l, vm.VMID); err != nil {
			return err
		}

		vmid := strconv.FormatInt(vm.VMID, 10)

		current, err := deps.API.Nodes.ListQemuStatusCurrent(cmd.Context(), vm.Node, vmid)
		if err != nil {
			return fmt.Errorf("get status for lab %q node %q VM %s on node %q: %w", name, tgt.label, vmid, vm.Node, err)
		}
		if current == nil {
			return fmt.Errorf("get status for lab %q node %q VM %s on node %q: empty response", name, tgt.label, vmid, vm.Node)
		}

		if current.Status == action.noopState {
			lines = append(lines, fmt.Sprintf("node %s VM %s on node %q is already %s",
				tgt.label, vmid, vm.Node, action.noopState))
			continue
		}

		if dryRun {
			lines = append(lines, fmt.Sprintf("[dry-run] would %s node %s VM %s on node %q (currently %s)",
				action.verb, tgt.label, vmid, vm.Node, current.Status))
			continue
		}

		raw, err := action.invoke(cmd.Context(), deps, vm.Node, vmid)
		if err != nil {
			return err
		}

		// Mirror applySdn's immediate-vs-async response handling: a UPID is
		// awaited; a null/empty/non-UPID body from an older server is treated as
		// an immediate success rather than a failure — the mutating call itself
		// already succeeded by this point.
		upid, perr := apiclient.UPIDFromRaw(raw)
		if perr == nil && upid != "" {
			if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
				return err
			}
		}

		lines = append(lines, fmt.Sprintf("node %s VM %s %s", tgt.label, vmid, action.pastVerb))
	}

	if !anyFound {
		return fmt.Errorf("lab %q: no VM found in pool %q; run `pmx lab create %s` first", name, pool, name)
	}

	msg := fmt.Sprintf("Lab %q: %s.", name, strings.Join(lines, "; "))
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// newStartCmd builds `pmx lab start <name>`.
func newStartCmd() *cobra.Command {
	var (
		dryRun   bool
		nodeFlag string
	)
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a lab's node VMs (and QDevice, if any)",
		Long: "Start every configured node VM in order (node 0 first, so a fresh cluster " +
			"regains quorum predictably), then the QDevice VM if the lab's topology calls " +
			"for one. Idempotent per target: a VM that is already running is reported as a " +
			"no-op rather than re-issuing the start call. A target with no VM created yet is " +
			"skipped silently unless --node names it explicitly. Refuses to act when the " +
			"lab's identifiers, or any target's live VM ID, overlap a peppi-protected " +
			"production resource. Blocks until each PVE task completes before starting the " +
			"next target. Pass --node to scope the action to one node index (0-4) or \"q\" for " +
			"the QDevice.",
		Example: `  pmx lab start wayne
  pmx lab start wayne --node 1
  pmx lab start wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLifecycleMutate(cmd, args[0], dryRun, nodeFlag, lifecycleAction{
				verb:      "start",
				pastVerb:  "started",
				noopState: "running",
				reverse:   false,
				invoke: func(ctx context.Context, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
					resp, err := deps.API.Nodes.CreateQemuStatusStart(ctx, node, vmid, &nodes.CreateQemuStatusStartParams{})
					if err != nil {
						return nil, fmt.Errorf("start VM %s on node %q: %w", vmid, node, err)
					}
					if resp == nil {
						return nil, nil
					}
					return json.RawMessage(*resp), nil
				},
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the intended actions without starting any VM")
	cmd.Flags().StringVar(&nodeFlag, "node", "", "scope to one node index (0-4) or \"q\" for the QDevice")
	return cmd
}

// newStopCmd builds `pmx lab stop <name>`.
func newStopCmd() *cobra.Command {
	var (
		dryRun   bool
		nodeFlag string
	)
	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a lab's node VMs (and QDevice, if any) (hard power off)",
		Long: "Immediately power off every configured node VM without asking the guest OS to " +
			"shut down cleanly, similar to pulling the power — in reverse start order (the " +
			"QDevice VM, if the lab's topology calls for one, then node N-1 down to node 0). " +
			"Idempotent per target: a VM that is already stopped is reported as a no-op " +
			"rather than re-issuing the stop call. A target with no VM created yet is skipped " +
			"silently unless --node names it explicitly. Refuses to act when the lab's " +
			"identifiers, or any target's live VM ID, overlap a peppi-protected production " +
			"resource. Blocks until each PVE task completes before stopping the next target. " +
			"Pass --node to scope the action to one node index (0-4) or \"q\" for the QDevice.",
		Example: `  pmx lab stop wayne
  pmx lab stop wayne --node q
  pmx lab stop wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLifecycleMutate(cmd, args[0], dryRun, nodeFlag, lifecycleAction{
				verb:      "stop",
				pastVerb:  "stopped",
				noopState: "stopped",
				reverse:   true,
				invoke: func(ctx context.Context, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
					resp, err := deps.API.Nodes.CreateQemuStatusStop(ctx, node, vmid, &nodes.CreateQemuStatusStopParams{})
					if err != nil {
						return nil, fmt.Errorf("stop VM %s on node %q: %w", vmid, node, err)
					}
					if resp == nil {
						return nil, nil
					}
					return json.RawMessage(*resp), nil
				},
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the intended actions without stopping any VM")
	cmd.Flags().StringVar(&nodeFlag, "node", "", "scope to one node index (0-4) or \"q\" for the QDevice")
	return cmd
}
