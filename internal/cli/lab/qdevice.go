package lab

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// qdeviceQnetdPackage and qdeviceClientPackage are the Debian packages the
// QDevice VM and every cluster node (respectively) need installed before
// `pvecm qdevice setup` can succeed (multi-node lab plan §6.3).
const (
	qdeviceQnetdPackage  = "corosync-qnetd"
	qdeviceClientPackage = "corosync-qdevice"
)

// qdeviceStepResult records one step of `pmx lab qdevice add`'s execution
// for the final STEP/STATUS table, mirroring create.go's createStep render
// contract.
type qdeviceStepResult struct {
	desc string
	skip bool
}

// newQdeviceCmd builds `pmx lab qdevice` and its subcommands.
func newQdeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qdevice",
		Short: "Add or remove a lab's corosync QDevice tie-breaker",
		Long: "Wire up (or tear down) the corosync-level QDevice tie-breaker for a lab whose " +
			"topology calls for one (mandatory at exactly 2 nodes, `qdevice: auto`-recommended " +
			"at 4). `pmx lab create` already provisions the QDevice VM itself when the topology " +
			"calls for one; this command group handles the corosync-qnetd/corosync-qdevice " +
			"package installation and `pvecm qdevice setup`/`remove` steps on top of that VM, all " +
			"over ssh into the lab guests.",
	}
	cmd.AddCommand(newQdeviceAddCmd(), newQdeviceRemoveCmd())
	return cmd
}

// newQdeviceAddCmd builds `pmx lab qdevice add <name>`.
func newQdeviceAddCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Wire up a lab's QDevice tie-breaker",
		Long: "Install corosync-qnetd on the lab's QDevice VM (which must already exist and be " +
			"running — `pmx lab create` provisions it when the lab's topology calls for one), " +
			"confirm/install corosync-qdevice on every cluster node, then run `pvecm qdevice " +
			"setup <qdevice-mgmt-ip>` on node 0. Requires the nested cluster to already be " +
			"formed (`pmx lab cluster init`/`join`) and the lab's topology to actually call for " +
			"a QDevice. Every step is idempotent: an already-installed package or an already-" +
			"configured QDevice is skipped, not re-applied.",
		Example: `  pmx lab qdevice add wayne
  pmx lab qdevice add wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQdeviceAdd(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the steps that would run, without executing them")
	return cmd
}

func runQdeviceAdd(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	if !config.QdeviceRequired(lab.Topology) {
		return fmt.Errorf(
			"lab %q's topology does not call for a QDevice (nodes=%d, qdevice=%q); "+
				"nothing to add", name, config.EffectiveTopologyNodes(lab.Topology),
			config.EffectiveTopologyQdevice(lab.Topology))
	}

	qdeviceIP, err := labQdeviceMgmtIP(lab.Network)
	if err != nil {
		return fmt.Errorf("resolve QDevice mgmt IP: %w", err)
	}
	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}
	numNodes := config.EffectiveTopologyNodes(lab.Topology)

	// dry-run never touches deps.Runner (see cluster.go's runClusterInit for
	// the same convention and rationale): it renders the literal step list
	// this run would perform, without probing live remote state.
	if dryRun {
		headers := []string{"STEP", "STATUS"}
		rows := [][]string{
			{fmt.Sprintf("install %s on QDevice VM (%s)", qdeviceQnetdPackage, qdeviceIP), "would run"},
		}
		for i := 0; i < numNodes; i++ {
			nodeIP, ierr := labNodeMgmtIP(lab.Network, i)
			if ierr != nil {
				return fmt.Errorf("resolve node %d mgmt IP: %w", i, ierr)
			}
			rows = append(rows, []string{fmt.Sprintf("install %s on node %d (%s)", qdeviceClientPackage, i, nodeIP), "would run"})
		}
		rows = append(rows, []string{fmt.Sprintf("pvecm qdevice setup %s on node 0", qdeviceIP), "would run"})
		rows = append(rows, []string{"summary", fmt.Sprintf("qdevice add plan for lab %q", name)})
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
	}

	// Precondition: the QDevice VM must already exist and be running —
	// `pmx lab create` provisions it (multi-node lab plan §4.3), this
	// command never creates VMs.
	vms, err := listLiveVMs(cmd.Context(), deps)
	if err != nil {
		return err
	}
	classified, err := findLabVMs(vms, labPoolID(lab), lab.Name)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}
	qvm, found := qdeviceLabVM(classified)
	if !found {
		return fmt.Errorf(
			"lab %q: no QDevice VM found in pool %q; run `pmx lab create %s` first "+
				"(its topology already calls for a QDevice)", name, labPoolID(lab), name)
	}
	if qvm.Status != "running" {
		return fmt.Errorf(
			"lab %q: QDevice VM %d is not running (status %q); run `pmx lab start %s --node q` first",
			name, qvm.VMID, qvm.Status, name)
	}

	// Precondition: the nested cluster must already be formed.
	clusterProbe, cerr := runGuestSSH(deps, node0IP, "pvecm status")
	if cerr != nil && guestCommandTransportFailed(cerr) {
		return fmt.Errorf("probe node 0 (%s) cluster state: %w", node0IP, cerr)
	}
	cst := parsePvecmStatus(clusterProbe.Stdout)
	if !cst.Clustered || cst.ClusterName != lab.Name {
		return fmt.Errorf(
			"lab %q: node 0 (%s) is not yet clustered as %q; run `pmx lab cluster init`/`join` first",
			name, node0IP, lab.Name)
	}

	var steps []qdeviceStepResult

	alreadyInstalled, err := ensureGuestPackage(deps, qdeviceIP, qdeviceQnetdPackage)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}
	steps = append(steps, qdeviceStepResult{
		desc: fmt.Sprintf("install %s on QDevice VM (%s)", qdeviceQnetdPackage, qdeviceIP), skip: alreadyInstalled})

	for i := 0; i < numNodes; i++ {
		nodeIP, ierr := labNodeMgmtIP(lab.Network, i)
		if ierr != nil {
			return fmt.Errorf("resolve node %d mgmt IP: %w", i, ierr)
		}
		already, perr := ensureGuestPackage(deps, nodeIP, qdeviceClientPackage)
		if perr != nil {
			return fmt.Errorf("lab %q: %w", name, perr)
		}
		steps = append(steps, qdeviceStepResult{
			desc: fmt.Sprintf("install %s on node %d (%s)", qdeviceClientPackage, i, nodeIP), skip: already})
	}

	if cst.HasQdevice {
		steps = append(steps, qdeviceStepResult{
			desc: fmt.Sprintf("pvecm qdevice setup %s on node 0", qdeviceIP), skip: true})
	} else {
		setupCmd := fmt.Sprintf("pvecm qdevice setup %s", qdeviceIP)
		if _, serr := runGuestSSH(deps, node0IP, setupCmd); serr != nil {
			return fmt.Errorf("lab %q: qdevice setup against node 0 (%s): %w", name, node0IP, serr)
		}
		steps = append(steps, qdeviceStepResult{desc: fmt.Sprintf("pvecm qdevice setup %s on node 0", qdeviceIP)})
	}

	headers := []string{"STEP", "STATUS"}
	rows := make([][]string, 0, len(steps)+1)
	for _, s := range steps {
		status := "done"
		if s.skip {
			status = "skip (already satisfied)"
		}
		rows = append(rows, []string{s.desc, status})
	}
	rows = append(rows, []string{"summary", fmt.Sprintf("lab %q: QDevice wired up against cluster %q.", name, lab.Name)})

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}

// ensureGuestPackage probes host for pkg via `dpkg -s`, installing it via
// `apt-get update && apt-get install -y <pkg>` when the probe reports it is
// not present. alreadyInstalled is true when the probe succeeded (pkg was
// already there) and no install command was run. A probe failure that is
// not a plain non-zero exit (guestCommandTransportFailed) is treated as a
// real error rather than "not installed", since it means ssh itself could
// not reach host at all.
func ensureGuestPackage(deps *cli.Deps, host, pkg string) (alreadyInstalled bool, err error) {
	_, perr := runGuestSSH(deps, host, fmt.Sprintf("dpkg -s %s", pkg))
	if perr == nil {
		return true, nil
	}
	if guestCommandTransportFailed(perr) {
		return false, fmt.Errorf("probe package %q on %s: %w", pkg, host, perr)
	}

	installCmd := fmt.Sprintf("apt-get update && apt-get install -y %s", pkg)
	if _, ierr := runGuestSSH(deps, host, installCmd); ierr != nil {
		return false, fmt.Errorf("install package %q on %s: %w", pkg, host, ierr)
	}
	return false, nil
}

// newQdeviceRemoveCmd builds `pmx lab qdevice remove <name>`.
func newQdeviceRemoveCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a lab's corosync QDevice tie-breaker",
		Long: "Run `pvecm qdevice remove` over ssh on node 0, unregistering the QDevice from " +
			"the nested cluster's corosync quorum config. Idempotent: if node 0 does not report " +
			"a registered QDevice, the command reports it as already absent and does nothing " +
			"further. This does NOT destroy the QDevice VM itself — use `pmx lab destroy " +
			"--node q` (or a full `pmx lab destroy`) for that once no cluster references it. " +
			"Per multi-node lab plan §9, this must run BEFORE any node join that would otherwise " +
			"leave the vote count in an odd+witness (Last-Man-Standing) shape — never " +
			"simultaneously with a join.",
		Example: `  pmx lab qdevice remove wayne
  pmx lab qdevice remove wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQdeviceRemove(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the ssh command that would run, without executing it")
	return cmd
}

func runQdeviceRemove(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	node0IP, err := labNodeMgmtIP(lab.Network, 0)
	if err != nil {
		return fmt.Errorf("resolve node 0 mgmt IP: %w", err)
	}

	removeCmd := "pvecm qdevice remove"

	if dryRun {
		res := output.Result{Message: fmt.Sprintf("[dry-run] would run on node 0 (%s): %s", node0IP, removeCmd)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	probe, perr := runGuestSSH(deps, node0IP, "pvecm status")
	if perr != nil && guestCommandTransportFailed(perr) {
		return fmt.Errorf("probe node 0 (%s) cluster state: %w", node0IP, perr)
	}
	st := parsePvecmStatus(probe.Stdout)
	if !st.HasQdevice {
		res := output.Result{Message: fmt.Sprintf(
			"lab %q: node 0 (%s) reports no registered QDevice; nothing to do.", name, node0IP)}
		return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
	}

	if _, err := runGuestSSH(deps, node0IP, removeCmd); err != nil {
		return fmt.Errorf("lab %q: remove qdevice from node 0 (%s): %w", name, node0IP, err)
	}

	res := output.Result{Message: fmt.Sprintf("lab %q: QDevice removed from cluster %q.", name, lab.Name)}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}
