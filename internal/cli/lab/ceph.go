package lab

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// cephStepResult is one row of a ceph verb's STEP/STATUS table. Unlike
// qdeviceStepResult (qdevice.go:53), Status is free-form text: the ceph
// verbs report more states than done/skip.
type cephStepResult struct {
	Step, Status string
}

// cephRenderSteps renders rows as the STEP/STATUS table every mutating
// lab ceph verb ends with, plus the trailing summary row runQdeviceAdd
// uses (qdevice.go:215-222).
func cephRenderSteps(cmd *cobra.Command, deps *cli.Deps, rows []cephStepResult, summary string) error {
	out := make([][]string, 0, len(rows)+1)
	for _, r := range rows {
		out = append(out, []string{r.Step, r.Status})
	}
	out = append(out, []string{"summary", summary})
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Headers: []string{"STEP", "STATUS"}, Rows: out}, deps.Format)
}

// newCephCmd groups the Ceph orchestration verbs for a multi-node lab: they
// drive `pveceph` inside the nested cluster, over guest SSH where PVE has no
// REST endpoint (package install) and through the lab's own API context
// (labInnerAPIClient) everywhere else. The verbs are idempotent and are
// designed to be run in order: install, init, mon, mgr, osd, pool.
func newCephCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ceph",
		Short: "Orchestrate Ceph inside a multi-node lab cluster",
	}
	cmd.AddCommand(newCephInstallCmd(), newCephInitCmd(), newCephMonCmd(),
		newCephMgrCmd(), newCephOsdCmd(), newCephPoolCmd(), newCephStatusCmd())
	return cmd
}

// cephResolveNodes returns lab's effective node count, erroring when it is
// fewer than the 3 nodes Ceph requires for a meaningful quorum of monitors.
func cephResolveNodes(lab *config.Lab) (int, error) {
	n := config.EffectiveTopologyNodes(lab.Topology)
	if n < 3 {
		return 0, fmt.Errorf("lab %q has %d node(s): Ceph needs at least a 3-node lab (set topology.nodes: 3)", lab.Name, n)
	}
	return n, nil
}

// ensureCephInstalled probes for the ceph packages and runs pveceph install
// when absent. pveceph install has no REST endpoint anywhere in PVE, so this
// is guest SSH by necessity, not convenience.
func ensureCephInstalled(deps *cli.Deps, nodeIP string) (bool, error) {
	probe, perr := runGuestSSH(deps, nodeIP,
		"dpkg -s ceph-osd >/dev/null 2>&1 && echo installed || echo absent")
	if perr != nil && guestCommandTransportFailed(perr) {
		return false, fmt.Errorf("probe ceph install state on %s: %w", nodeIP, perr)
	}
	if strings.TrimSpace(probe.Stdout) == "installed" {
		return true, nil
	}
	// -y is required: DEBIAN_FRONTEND covers apt, not pveceph's own
	// confirmation prompt, which aborts on EOF over non-TTY SSH.
	if _, err := runGuestSSH(deps, nodeIP,
		"DEBIAN_FRONTEND=noninteractive pveceph install --repository no-subscription -y"); err != nil {
		return false, fmt.Errorf("pveceph install on %s: %w", nodeIP, err)
	}
	return false, nil
}

// newCephInstallCmd builds `pmx lab ceph install <name>`.
func newCephInstallCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install <name>",
		Short: "Install the Ceph packages on every node of a lab's nested cluster",
		Long: "Run `pveceph install --repository no-subscription -y` over ssh on every node of " +
			"the lab's nested cluster.\n\n" +
			"Idempotent: a node that already reports the ceph-osd package installed is skipped " +
			"rather than re-run.\n\n" +
			"Requires topology.nodes >= 3 — Ceph needs at least a 3-node lab.",
		Example: `  pmx lab ceph install wayne
  pmx lab ceph install wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCephInstall(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the ssh commands that would run, without executing them")
	return cmd
}

func runCephInstall(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	numNodes, err := cephResolveNodes(lab)
	if err != nil {
		return err
	}

	// dry-run never touches deps.Runner: see runClusterInit's matching
	// comment (cluster.go) for why this mirrors quota.go's established
	// precedent instead of probing live remote state.
	if dryRun {
		var b strings.Builder
		for i := range numNodes {
			nodeIP, ierr := labNodeMgmtIP(lab.Network, i)
			if ierr != nil {
				return fmt.Errorf("resolve node %d mgmt IP: %w", i, ierr)
			}
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "[dry-run] would run on node %d (%s): "+
				"DEBIAN_FRONTEND=noninteractive pveceph install --repository no-subscription -y", i, nodeIP)
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: b.String()}, deps.Format)
	}

	var rows []cephStepResult
	for i := range numNodes {
		nodeIP, ierr := labNodeMgmtIP(lab.Network, i)
		if ierr != nil {
			return fmt.Errorf("resolve node %d mgmt IP: %w", i, ierr)
		}
		alreadyInstalled, ierr := ensureCephInstalled(deps, nodeIP)
		if ierr != nil {
			return fmt.Errorf("lab %q: %w", name, ierr)
		}
		status := "installed"
		if alreadyInstalled {
			status = "already installed"
		}
		rows = append(rows, cephStepResult{Step: fmt.Sprintf("install node %d", i), Status: status})
	}

	summary := fmt.Sprintf("lab %q: Ceph packages installed on all %d nodes.", name, numNodes)
	return cephRenderSteps(cmd, deps, rows, summary)
}

// newCephInitCmd builds `pmx lab ceph init <name>` (scaffold only; filled in
// by a later task).
func newCephInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <name>",
		Short: "Initialize the Ceph cluster configuration on a lab's nested cluster",
	}
}

// newCephMonCmd builds `pmx lab ceph mon <name>` (scaffold only; filled in by
// a later task).
func newCephMonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mon <name>",
		Short: "Create Ceph monitor daemons on a lab's nested cluster",
	}
}

// newCephMgrCmd builds `pmx lab ceph mgr <name>` (scaffold only; filled in by
// a later task).
func newCephMgrCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mgr <name>",
		Short: "Create Ceph manager daemons on a lab's nested cluster",
	}
}

// newCephOsdCmd builds `pmx lab ceph osd <name>` (scaffold only; filled in by
// a later task).
func newCephOsdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "osd <name>",
		Short: "Create Ceph OSDs on a lab's nested cluster",
	}
}

// newCephPoolCmd builds `pmx lab ceph pool <name>` (scaffold only; filled in
// by a later task).
func newCephPoolCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pool <name>",
		Short: "Manage Ceph pools on a lab's nested cluster",
	}
}

// newCephStatusCmd builds `pmx lab ceph status <name>` (scaffold only; filled
// in by a later task).
func newCephStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show a lab's nested Ceph cluster status",
	}
}
