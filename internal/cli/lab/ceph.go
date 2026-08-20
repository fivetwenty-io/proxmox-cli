package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
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

// cephInnerAPI builds an API client bound to lab's own nested-cluster
// context (labInnerAPIClient) for the RunE shells below. Unlike
// hostnetReconcileNodes, a failed build here ABORTS the verb rather than
// degrading to a deferred row: init/mon/mgr have nothing else useful to do
// once the inner cluster is unreachable, so a row reporting anything but an
// error would claim success while doing nothing. The wording reuses
// hostnetReconcileNodes' own ("register it with `pmx lab context sync
// %s`"), just as an error instead of a row.
func cephInnerAPI(cmd *cobra.Command, deps *cli.Deps, lab *config.Lab) (*apiclient.APIClient, error) {
	api, err := labInnerAPIClient(cmd, deps, lab.Name)
	if err != nil {
		return nil, fmt.Errorf(
			"lab %q: cannot reach lab context %q: %w — register it with `pmx lab context sync %s`",
			lab.Name, labContextName(lab.Name), err, lab.Name)
	}
	return api, nil
}

// cephWaitTask blocks on the task a nested-cluster ceph POST returned, using
// the inner client (node/ceph.go's renderCephTask hardcodes the outer
// deps.API and cannot be reused here). Non-task responses (raw carries no
// UPID) are a no-op.
func cephWaitTask(ctx context.Context, api *apiclient.APIClient, raw json.RawMessage) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return nil
	}
	return apiclient.WaitTask(ctx, api, upid, nil)
}

// ensureCephInit runs `pveceph init` on node0, scoped to network (the lab's
// /16, not the mgmt /24 labMgmtCIDR derives node addresses from), unless
// node0 already carries an initialized ceph.conf.
//
// ListCephCfgRaw returns ceph.conf verbatim (ListCephCfgRawResponse =
// json.RawMessage, nodes_gen.go:1947); PVE answers 200 with an EMPTY body on
// a node that has never been initialized, so err == nil alone is not enough
// — the content must be non-empty (and carry a [global] section) too. On any
// probe failure we fall through to the POST: CreateCephInit is idempotent
// (an existing [global] section's fsid/auth/pool defaults are preserved,
// per the SDK doc), and the POST fails loudly where a swallowed probe error
// would not.
func ensureCephInit(ctx context.Context, api *apiclient.APIClient, node0, network string) (string, error) {
	if raw, err := api.Nodes.ListCephCfgRaw(ctx, node0); err == nil && raw != nil {
		var conf string
		if json.Unmarshal(*raw, &conf) == nil && strings.Contains(conf, "[global]") {
			return fmt.Sprintf("Ceph already initialized on %s; skipping init.", node0), nil
		}
	}
	// CreateCephInit returns error only — no response, no task UPID to wait
	// on (nodes_gen.go:2278); do NOT call cephWaitTask here.
	params := &nodes.CreateCephInitParams{Network: new(network)}
	if err := api.Nodes.CreateCephInit(ctx, node0, params); err != nil {
		return "", fmt.Errorf("pveceph init on %s: %w", node0, err)
	}
	return fmt.Sprintf("Ceph initialized on %s (network %s).", node0, network), nil
}

// cephCreateDaemon creates a mon or mgr named after the node it runs on. The
// two SDK calls differ in arity: CreateCephMon takes a monid plus params
// (POST /nodes/{node}/ceph/mon/{monid}, nodes_gen.go:2652), CreateCephMgr
// takes only an id (POST /nodes/{node}/ceph/mgr/{id}, nodes_gen.go:2547).
func cephCreateDaemon(ctx context.Context, api *apiclient.APIClient, node, kind string) (json.RawMessage, error) {
	switch kind {
	case "mon":
		resp, err := api.Nodes.CreateCephMon(ctx, node, node, &nodes.CreateCephMonParams{})
		if err != nil || resp == nil {
			return nil, err
		}
		return json.RawMessage(*resp), nil
	case "mgr":
		resp, err := api.Nodes.CreateCephMgr(ctx, node, node)
		if err != nil || resp == nil {
			return nil, err
		}
		return json.RawMessage(*resp), nil
	}
	return nil, fmt.Errorf("unknown ceph daemon kind %q", kind)
}

// cephListDaemonNames returns the set of daemon names (one per node they run
// on, by lab convention) already present on node0 for kind ("mon" or
// "mgr"). ListCephMon/ListCephMgr return []json.RawMessage (nodes_gen.go:
// 2577, :2478) — one element per daemon — so each element is unmarshalled
// individually into struct{ Name string }, not the whole response.
func cephListDaemonNames(ctx context.Context, api *apiclient.APIClient, node0, kind string) (map[string]bool, error) {
	var raws []json.RawMessage
	switch kind {
	case "mon":
		resp, err := api.Nodes.ListCephMon(ctx, node0)
		if err != nil {
			return nil, fmt.Errorf("list ceph mon on %s: %w", node0, err)
		}
		if resp != nil {
			raws = *resp
		}
	case "mgr":
		resp, err := api.Nodes.ListCephMgr(ctx, node0)
		if err != nil {
			return nil, fmt.Errorf("list ceph mgr on %s: %w", node0, err)
		}
		if resp != nil {
			raws = *resp
		}
	default:
		return nil, fmt.Errorf("unknown ceph daemon kind %q", kind)
	}

	names := make(map[string]bool, len(raws))
	for _, raw := range raws {
		var d struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("decode ceph %s entry: %w", kind, err)
		}
		names[d.Name] = true
	}
	return names, nil
}

// ensureCephDaemons creates a mon or mgr on every node of lab that does not
// already have one, named after the node it runs on. kind is "mon" or
// "mgr". Existing daemons are probed once, from node0, since the nested
// cluster's ceph/mon and ceph/mgr listings are cluster-wide regardless of
// which node answers.
func ensureCephDaemons(ctx context.Context, api *apiclient.APIClient, lab *config.Lab, kind string) ([]cephStepResult, error) {
	n := config.EffectiveTopologyNodes(lab.Topology)
	node0 := labNodeVMName(lab.Name, 0)
	existing, err := cephListDaemonNames(ctx, api, node0, kind)
	if err != nil {
		return nil, err
	}
	rows := make([]cephStepResult, 0, n)
	for i := range n {
		name := labNodeVMName(lab.Name, i)
		if existing[name] {
			rows = append(rows, cephStepResult{Step: kind + " " + name, Status: "already present"})
			continue
		}
		raw, err := cephCreateDaemon(ctx, api, name, kind)
		if err != nil {
			return rows, fmt.Errorf("create %s on %s: %w", kind, name, err)
		}
		if err := cephWaitTask(ctx, api, raw); err != nil {
			return rows, fmt.Errorf("wait for %s create on %s: %w", kind, name, err)
		}
		rows = append(rows, cephStepResult{Step: kind + " " + name, Status: "created"})
	}
	return rows, nil
}

// newCephInitCmd builds `pmx lab ceph init <name>`.
func newCephInitCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Initialize the Ceph cluster configuration on a lab's nested cluster",
		Long: "Run `pveceph init` against the lab's own nested-cluster API context, on node 0, " +
			"scoped to the lab's network /16 (network.cidr) — never the mgmt /24 nodes are addressed on.\n\n" +
			"Idempotent: a node that already reports a [global] section in ceph.conf is skipped.\n\n" +
			"Requires topology.nodes >= 3 — Ceph needs at least a 3-node lab.",
		Example: `  pmx lab ceph init wayne
  pmx lab ceph init wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCephInit(cmd, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would run, without touching the lab context")
	return cmd
}

func runCephInit(cmd *cobra.Command, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	if _, err := cephResolveNodes(lab); err != nil {
		return err
	}

	node0 := labNodeVMName(lab.Name, 0)
	network := lab.Network.CIDR

	if dryRun {
		msg := fmt.Sprintf("[dry-run] would run pveceph init on %s (network %s)", node0, network)
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
	}

	api, err := cephInnerAPI(cmd, deps, lab)
	if err != nil {
		return err
	}

	status, err := ensureCephInit(cmd.Context(), api, node0, network)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}

	return cephRenderSteps(cmd, deps, []cephStepResult{{Step: "init " + node0, Status: status}}, status)
}

// newCephDaemonCmd builds the shared shell for `pmx lab ceph mon` and
// `pmx lab ceph mgr <name>`: same resolve/gate/dry-run/ensure/render flow,
// differing only in kind ("mon" or "mgr").
func newCephDaemonCmd(kind, short string) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   kind + " <name>",
		Short: short,
		Long: fmt.Sprintf("Create a Ceph %s daemon on every node of a lab's nested cluster that does not "+
			"already have one, named after the node it runs on.\n\n"+
			"Idempotent: a node already listed as running a %s is skipped.\n\n"+
			"Requires topology.nodes >= 3 — Ceph needs at least a 3-node lab.", kind, kind),
		Example: fmt.Sprintf("  pmx lab ceph %s wayne\n  pmx lab ceph %s wayne --dry-run", kind, kind),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCephDaemon(cmd, kind, args[0], dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would run, without touching the lab context")
	return cmd
}

func runCephDaemon(cmd *cobra.Command, kind, name string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	numNodes, err := cephResolveNodes(lab)
	if err != nil {
		return err
	}

	if dryRun {
		var b strings.Builder
		for i := range numNodes {
			node := labNodeVMName(lab.Name, i)
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "[dry-run] would ensure ceph %s on %s", kind, node)
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: b.String()}, deps.Format)
	}

	api, err := cephInnerAPI(cmd, deps, lab)
	if err != nil {
		return err
	}

	rows, err := ensureCephDaemons(cmd.Context(), api, lab, kind)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}

	summary := fmt.Sprintf("lab %q: Ceph %s daemons ensured on all %d nodes.", name, kind, numNodes)
	return cephRenderSteps(cmd, deps, rows, summary)
}

// newCephMonCmd builds `pmx lab ceph mon <name>`.
func newCephMonCmd() *cobra.Command {
	return newCephDaemonCmd("mon", "Create Ceph monitor daemons on a lab's nested cluster")
}

// newCephMgrCmd builds `pmx lab ceph mgr <name>`.
func newCephMgrCmd() *cobra.Command {
	return newCephDaemonCmd("mgr", "Create Ceph manager daemons on a lab's nested cluster")
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
