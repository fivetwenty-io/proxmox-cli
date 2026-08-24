package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

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

// cephRenderPartial renders the steps an ensure-core completed before it
// failed. Every ensure-core below deliberately returns its rows alongside the
// error precisely so a mid-loop failure still shows which nodes and devices
// were already done; discarding them leaves the operator an opaque error and
// no idea how far the run got. The render error is dropped on purpose: the
// caller returns the failure that brought us here, which must not be masked
// by a secondary problem writing the table.
func cephRenderPartial(cmd *cobra.Command, deps *cli.Deps, rows []cephStepResult) {
	if len(rows) == 0 {
		return
	}
	_ = cephRenderSteps(cmd, deps, rows, "failed partway; the steps above completed before the error")
}

// cephRawResponse dereferences the *json.RawMessage every ceph and disks
// POST/PUT in the SDK answers with, yielding nil for an absent body so
// cephWaitTask's non-task no-op path handles it.
func cephRawResponse(resp *json.RawMessage) json.RawMessage {
	if resp == nil {
		return nil
	}
	return *resp
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

// cephInstallCommand is the single source of the remote pveceph install
// command line, so the dry-run preview cannot drift from what the real run
// executes (they previously repeated the same literal in two places).
//
// There is deliberately no -y: pveceph install takes only --allow-experimental,
// --repository, and --version (PVE 9.2.6). Anything else is rejected outright
// by the CLI option parser with "Unknown option: y / 400 unable to parse
// option", so the -y this used to pass meant the install aborted at argument
// parsing without touching a single package.
//
// Nothing replaces it, because pveceph needs no confirmation here. Each of its
// three prompts is guarded by `-t STDOUT`, and over ssh stdout is a pipe, so on
// a guest session they are skipped rather than asked. The prompt that actually
// hangs comes from the apt-get underneath, which guestPkgCommand answers.
func cephInstallCommand() string {
	return guestPkgCommand("pveceph install --repository no-subscription")
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
	if _, err := runGuestSSH(deps, nodeIP, cephInstallCommand()); err != nil {
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
		Long: "Run `pveceph install --repository no-subscription` over ssh on every node of " +
			"the lab's nested cluster, with every package-manager prompt a TTY-less ssh session " +
			"cannot answer suppressed. That includes the \"Do you want to continue? [Y/n]\" of " +
			"the apt-get pveceph runs underneath, which no frontend setting answers because " +
			"pveceph passes it no --assume-yes.\n\n" +
			"Idempotent: a node that already reports the ceph-osd package installed is skipped " +
			"rather than re-run.\n\n" +
			"Requires topology.nodes >= 3: Ceph needs at least a 3-node lab.",
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
			fmt.Fprintf(&b, "[dry-run] would run on node %d (%s): %s", i, nodeIP, cephInstallCommand())
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: b.String()}, deps.Format)
	}

	var rows []cephStepResult
	for i := range numNodes {
		nodeIP, ierr := labNodeMgmtIP(lab.Network, i)
		if ierr != nil {
			cephRenderPartial(cmd, deps, rows)
			return fmt.Errorf("resolve node %d mgmt IP: %w", i, ierr)
		}
		alreadyInstalled, ierr := ensureCephInstalled(deps, nodeIP)
		if ierr != nil {
			cephRenderPartial(cmd, deps, rows)
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
			"lab %q: cannot reach lab context %q: %w; register it with `pmx lab context sync %s`",
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
			"scoped to the lab's network /16 (network.cidr), never the mgmt /24 nodes are addressed on.\n\n" +
			"Idempotent: a node that already reports a [global] section in ceph.conf is skipped.\n\n" +
			"Requires topology.nodes >= 3: Ceph needs at least a 3-node lab.",
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
			"Requires topology.nodes >= 3: Ceph needs at least a 3-node lab.", kind, kind),
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
		cephRenderPartial(cmd, deps, rows)
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

// cephOSDSerial is the QEMU disk serial `pmx lab create`'s serial=osd<idx>
// disk option (createOSDDiskValue, create.go) pins on the idx-th OSD disk.
// The serial, not a kernel name, is what identifies an OSD disk: /dev/sdX
// ordering is not stable across boots, while the serial is.
func cephOSDSerial(idx int) string {
	return fmt.Sprintf("osd%d", idx)
}

// cephOSDLegacyByIDPath is the /dev/disk/by-id path this package used to
// DERIVE from a disk's serial and match against a node's disk listing.
// It is retained only as a fallback matcher, never as an OSD create target.
//
// The derivation is wrong on PVE 9.2 (live finding): the by-id symlink udev
// creates for a QEMU SCSI disk is keyed off the DRIVE NAME
// (scsi-0QEMU_QEMU_HARDDISK_drive-scsi1), not off the serial= option, which
// surfaces only as the disk's ID_SCSI_SERIAL udev property (what lsblk and
// PVE's own disks listing report as "serial"). Deriving the by-id path from
// the serial therefore produced a path that exists on no node, so every
// device failed to match and `pmx lab ceph osd` could not create a single
// OSD. Matching on the reported serial and taking the by-id path from the
// listing itself (cephResolveOSDDevices) is correct on every PVE build,
// because it reads the link rather than guessing its shape.
func cephOSDLegacyByIDPath(serial string) string {
	return "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_" + serial
}

// cephDiskEntry is the slice of one GET /nodes/{node}/disks/list element the
// OSD verb reads. It is deliberately API-only: an `lsblk FSTYPE` probe over
// guest ssh cannot tell a healthy OSD from a foreign LVM volume (both report
// LVM2_member), so a --wipe re-run driven by lsblk would zap live OSDs, and a
// GPT table with empty partitions reports an empty FSTYPE on the parent, so
// it would also green-light an in-use device. devpath/by_id_link/used/osdid
// answer all of it.
type cephDiskEntry struct {
	Devpath  string `json:"devpath"`
	ByIDLink string `json:"by_id_link"`
	// Serial is the disk's ID_SCSI_SERIAL udev property, which for a lab OSD
	// disk is the serial=osd<N> value `pmx lab create` pins on it. It is the
	// only field that survives a PVE build changing how it names by-id
	// symlinks, so it is what an OSD disk is matched on (cephMatchDisk).
	Serial string `json:"serial"`
	Used   string `json:"used"`
	// Osdid and OsdidList are pve.PVEInt, not int, because PVE renders them
	// through Perl scalars: the same disks/list array carries osdid as a JSON
	// number (-1) for a disk Ceph does not own and as a JSON string ("0") for
	// one it does, so a plain int fails to decode the listing at all.
	Osdid pve.PVEInt `json:"osdid"`
	// OsdidList names every OSD this device backs. A device whose Osdid is -1
	// but whose OsdidList is non-empty carries another OSD's DB or WAL: it is
	// not a free device, and wiping it destroys the OSDs it serves.
	OsdidList []pve.PVEInt `json:"osdid-list"`
}

// cephOSDDevice is one OSD device a lab's config calls for: node and serial
// come from storage.osd_disks, the rest from that node's live disk listing
// once cephResolveOSDDevices has matched the two. byID is therefore empty
// until resolution: it is READ from the listing, never derived, so it is
// whatever path that PVE build actually publishes for the disk.
type cephOSDDevice struct {
	node    string
	serial  string
	byID    string
	devpath string
	used    string
	osdid   int64
	// osdidList names every OSD this device backs, read from the listing's
	// osdid-list. A device with osdid -1 and a non-empty osdidList is a DB or
	// WAL carrier for those OSDs, not a free device.
	osdidList []int64
}

// cephExpectedOSDDevices derives every OSD device lab's config calls for,
// resolved per node through EffectiveNodeSizing so a per-node
// osd_disk_count override is honoured rather than the lab-wide value being
// applied to every node.
func cephExpectedOSDDevices(lab *config.Lab) ([]cephOSDDevice, error) {
	n := config.EffectiveTopologyNodes(lab.Topology)
	var devices []cephOSDDevice
	for i := range n {
		_, storage := config.EffectiveNodeSizing(lab, i)
		node := labNodeVMName(lab.Name, i)
		for j := range config.OSDDiskCount(storage) {
			devices = append(devices, cephOSDDevice{node: node, serial: cephOSDSerial(j), osdid: -1})
		}
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("lab %q has no storage.osd_disks configured: nothing to create OSDs on", lab.Name)
	}
	return devices, nil
}

// cephListDisks returns node's disk listing.
func cephListDisks(ctx context.Context, api *apiclient.APIClient, node string) ([]cephDiskEntry, error) {
	resp, err := api.Nodes.ListDisksList(ctx, node, &nodes.ListDisksListParams{})
	if err != nil {
		return nil, fmt.Errorf("list disks on %s: %w", node, err)
	}
	if resp == nil {
		return nil, nil
	}
	entries := make([]cephDiskEntry, 0, len(*resp))
	for _, raw := range *resp {
		// Osdid is seeded to -1 rather than left at Go's zero value: a
		// listing that omitted the field would otherwise read as "already
		// OSD 0" and silently skip a device that was never an OSD.
		e := cephDiskEntry{Osdid: -1}
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("decode disk entry on %s: %w", node, err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// cephMatchDisk finds the listing entry for the OSD disk carrying serial,
// matching on the reported serial first and only then falling back to the
// by-id path this package used to derive from that serial.
//
// Serial first is what makes this correct on PVE 9.2, where the by-id link is
// named after the drive rather than the serial (cephOSDLegacyByIDPath). The
// fallback is kept for a PVE build whose disk listing omits serial entirely,
// where the derived path is the only identifier available.
func cephMatchDisk(entries []cephDiskEntry, serial string) *cephDiskEntry {
	for i := range entries {
		if serial != "" && entries[i].Serial == serial {
			return &entries[i]
		}
	}
	legacy := cephOSDLegacyByIDPath(serial)
	for i := range entries {
		if entries[i].ByIDLink == legacy || entries[i].Devpath == legacy {
			return &entries[i]
		}
	}
	return nil
}

// cephResolveOSDDevices matches every expected device against its node's live
// disk listing, one listing per node however many devices that node carries.
// A configured device the node does not report aborts the whole run: it means
// the VM predates the lab's storage.osd_disks config, and creating OSDs on
// the remaining devices would leave the cluster silently short.
func cephResolveOSDDevices(ctx context.Context, api *apiclient.APIClient,
	expected []cephOSDDevice) ([]cephOSDDevice, error) {
	listings := make(map[string][]cephDiskEntry)
	out := make([]cephOSDDevice, 0, len(expected))
	for _, want := range expected {
		entries, ok := listings[want.node]
		if !ok {
			var err error
			if entries, err = cephListDisks(ctx, api, want.node); err != nil {
				return nil, err
			}
			listings[want.node] = entries
		}
		match := cephMatchDisk(entries, want.serial)
		if match == nil {
			return nil, fmt.Errorf(
				"node %s reports no disk with serial %q: the VM likely predates the lab's storage.osd_disks "+
					"config; attach the disk with `pmx pve qemu disk add`", want.node, want.serial)
		}
		// The OSD is created on the path the node itself publishes, never on
		// one derived from the serial: see cephOSDLegacyByIDPath for the PVE
		// 9.2 naming that makes deriving it wrong. by-id is still preferred
		// over devpath because /dev/sdX ordering is not stable across boots.
		want.byID = match.ByIDLink
		want.devpath = match.Devpath
		if want.byID == "" {
			want.byID = want.devpath
		}
		if want.devpath == "" {
			want.devpath = want.byID
		}
		if want.byID == "" {
			return nil, fmt.Errorf(
				"node %s reports the disk with serial %q but gives it neither a by-id link nor a device path",
				want.node, want.serial)
		}
		want.used = match.Used
		want.osdid = match.Osdid.Int()
		want.osdidList = want.osdidList[:0]
		for _, id := range match.OsdidList {
			want.osdidList = append(want.osdidList, id.Int())
		}
		out = append(out, want)
	}
	return out, nil
}

// cephOSDWipeCount counts the devices a --wipe run would actually destroy:
// in use by something other than Ceph. A device Ceph already owns is never
// counted, because it is never wiped, and neither is one that backs another
// OSD's DB or WAL (cephOSDServes).
func cephOSDWipeCount(devices []cephOSDDevice) int {
	n := 0
	for _, d := range devices {
		if d.osdid < 0 && d.used != "" && len(d.osdidList) == 0 {
			n++
		}
	}
	return n
}

// cephOSDServes reports the OSDs a device carries data for without itself
// being one of them: its osdid is -1, yet the node's disk listing names OSDs
// in its osdid-list, which is how PVE reports a shared DB or WAL device.
//
// Such a device is not free. Wiping it destroys every OSD in the list, and
// the osdid check alone cannot see that, because the device is not an OSD.
func cephOSDServes(d cephOSDDevice) []int64 {
	if d.osdid >= 0 {
		return nil
	}
	return d.osdidList
}

// cephOSDServesStatus renders the skip status for a DB or WAL carrier,
// naming the OSDs it serves so the operator can see why --wipe declined it.
func cephOSDServesStatus(ids []int64) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, strconv.FormatInt(id, 10))
	}
	return "backs OSD " + strings.Join(names, ", ") + " (skipped): DB or WAL device, wiping it destroys those OSDs"
}

// ensureCephOSDs creates an OSD on every resolved device that does not
// already carry one. A device Ceph owns (osdid >= 0) is left alone even under
// wipe, because refusing to touch a device Ceph owns is the whole point of
// reading osdid rather than a filesystem probe. So is a device that backs
// another OSD's DB or WAL (cephOSDServes), which osdid alone cannot identify.
// A device in use by anything else is skipped unless wipe is set, in which
// case it is wiped first.
func ensureCephOSDs(ctx context.Context, api *apiclient.APIClient,
	devices []cephOSDDevice, wipe bool) ([]cephStepResult, error) {
	rows := make([]cephStepResult, 0, len(devices))
	for _, d := range devices {
		step := fmt.Sprintf("osd %s %s", d.node, d.byID)
		if d.osdid >= 0 {
			rows = append(rows, cephStepResult{Step: step, Status: fmt.Sprintf("already OSD %d", d.osdid)})
			continue
		}
		if serves := cephOSDServes(d); len(serves) > 0 {
			rows = append(rows, cephStepResult{Step: step, Status: cephOSDServesStatus(serves)})
			continue
		}
		status := "created"
		if d.used != "" {
			if !wipe {
				rows = append(rows, cephStepResult{Step: step, Status: "in use (skipped): " + d.used})
				continue
			}
			// wipedisk takes the kernel device name, not the by-id path.
			resp, err := api.Nodes.UpdateDisksWipedisk(ctx, d.node, &nodes.UpdateDisksWipediskParams{Disk: d.devpath})
			if err != nil {
				return rows, fmt.Errorf("wipe %s on %s: %w", d.devpath, d.node, err)
			}
			if err := cephWaitTask(ctx, api, cephRawResponse(resp)); err != nil {
				return rows, fmt.Errorf("wait for wipe of %s on %s: %w", d.devpath, d.node, err)
			}
			status = "wiped, created"
		}
		resp, err := api.Nodes.CreateCephOsd(ctx, d.node, &nodes.CreateCephOsdParams{Dev: d.byID})
		if err != nil {
			return rows, fmt.Errorf("create OSD on %s at %s: %w", d.node, d.byID, err)
		}
		if err := cephWaitTask(ctx, api, cephRawResponse(resp)); err != nil {
			return rows, fmt.Errorf("wait for OSD create on %s at %s: %w", d.node, d.byID, err)
		}
		rows = append(rows, cephStepResult{Step: step, Status: status})
	}
	return rows, nil
}

// newCephOsdCmd builds `pmx lab ceph osd <name>`.
func newCephOsdCmd() *cobra.Command {
	var wipe, yes, dryRun bool

	cmd := &cobra.Command{
		Use:   "osd <name>",
		Short: "Create Ceph OSDs on a lab's nested cluster",
		Long: "Create a Ceph OSD on every disk storage.osd_disks gives a lab's nodes, through the " +
			"lab's own nested-cluster API context.\n\n" +
			"Devices are matched, never guessed: each one is found in the node's own disk listing " +
			"by the serial=osdN value `pmx lab create` pins on it, and its OSD is then created on " +
			"whatever device path that listing reports for it.\n\n" +
			"Idempotent: a device Ceph already owns is reported and left alone, even with --wipe. " +
			"A device in use by anything else is skipped unless --wipe is given, which destroys all " +
			"data on it and needs --yes or an interactive confirmation.\n\n" +
			"Requires topology.nodes >= 3: Ceph needs at least a 3-node lab.",
		Example: `  pmx lab ceph osd wayne
  pmx lab ceph osd wayne --dry-run
  pmx lab ceph osd wayne --wipe --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCephOsd(cmd, args[0], wipe, yes, dryRun)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&wipe, "wipe", false,
		"wipe a device in use by something other than Ceph before creating its OSD (destroys its data)")
	f.BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt --wipe otherwise requires")
	f.BoolVar(&dryRun, "dry-run", false, "print what would run, without touching the lab context")
	return cmd
}

func runCephOsd(cmd *cobra.Command, name string, wipe, yes, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	numNodes, err := cephResolveNodes(lab)
	if err != nil {
		return err
	}

	expected, err := cephExpectedOSDDevices(lab)
	if err != nil {
		return err
	}

	if dryRun {
		var b strings.Builder
		for i, d := range expected {
			if i > 0 {
				b.WriteString("\n")
			}
			// The device path is not known until the node's disk listing is
			// read, which dry-run deliberately never does, so this names the
			// disk the way the run itself will match it: by serial.
			fmt.Fprintf(&b, "[dry-run] would ensure a Ceph OSD on %s at the disk with serial %s", d.node, d.serial)
		}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: b.String()}, deps.Format)
	}

	api, err := cephInnerAPI(cmd, deps, lab)
	if err != nil {
		return err
	}

	devices, err := cephResolveOSDDevices(cmd.Context(), api, expected)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}

	// One prompt for the whole invocation, before ANY wipe runs.
	if toWipe := cephOSDWipeCount(devices); wipe && toWipe > 0 && !yes {
		ok, cerr := confirmYesNo(cmd, fmt.Sprintf(
			"Wipe %d in-use device(s) on lab %q and create Ceph OSDs on them (all data on them is destroyed)?",
			toWipe, name))
		if cerr != nil {
			return cerr
		}
		if !ok {
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: "Aborted."}, deps.Format)
		}
	}

	rows, err := ensureCephOSDs(cmd.Context(), api, devices, wipe)
	if err != nil {
		cephRenderPartial(cmd, deps, rows)
		return fmt.Errorf("lab %q: %w", name, err)
	}

	summary := fmt.Sprintf("lab %q: %d Ceph OSD device(s) ensured across %d nodes.", name, len(devices), numNodes)
	return cephRenderSteps(cmd, deps, rows, summary)
}

// cephPoolOptions carries `lab ceph pool`'s flags into ensureCephPool.
type cephPoolOptions struct {
	name          string
	size          int
	minSize       int
	autoscaleMode string
	addStorages   bool
}

// cephPoolExists reports whether node0 already lists a pool named name. PVE
// names the field pool_name; name is accepted too, so a PVE build that uses
// the shorter key still matches rather than provoking a duplicate create.
func cephPoolExists(ctx context.Context, api *apiclient.APIClient, node0, name string) (bool, error) {
	resp, err := api.Nodes.ListCephPool(ctx, node0)
	if err != nil {
		return false, fmt.Errorf("list ceph pools on %s: %w", node0, err)
	}
	if resp == nil {
		return false, nil
	}
	for _, raw := range *resp {
		var p struct {
			PoolName string `json:"pool_name"`
			Name     string `json:"name"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return false, fmt.Errorf("decode ceph pool entry on %s: %w", node0, err)
		}
		if p.PoolName == name || p.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// ensureCephPool creates opts' pool on node0 unless the nested cluster
// already carries one by that name.
func ensureCephPool(ctx context.Context, api *apiclient.APIClient, node0 string,
	opts cephPoolOptions) (cephStepResult, error) {
	step := "pool " + opts.name
	present, err := cephPoolExists(ctx, api, node0, opts.name)
	if err != nil {
		return cephStepResult{}, err
	}
	if present {
		return cephStepResult{Step: step, Status: "already present"}, nil
	}

	size, minSize := int64(opts.size), int64(opts.minSize)
	params := &nodes.CreateCephPoolParams{
		Name:            opts.name,
		Size:            &size,
		MinSize:         &minSize,
		PgAutoscaleMode: &opts.autoscaleMode,
		AddStorages:     &opts.addStorages,
	}
	resp, err := api.Nodes.CreateCephPool(ctx, node0, params)
	if err != nil {
		return cephStepResult{}, fmt.Errorf("create ceph pool %q on %s: %w", opts.name, node0, err)
	}
	if err := cephWaitTask(ctx, api, cephRawResponse(resp)); err != nil {
		return cephStepResult{}, fmt.Errorf("wait for ceph pool %q create on %s: %w", opts.name, node0, err)
	}
	return cephStepResult{Step: step, Status: "created"}, nil
}

// newCephPoolCmd builds `pmx lab ceph pool <name>`.
func newCephPoolCmd() *cobra.Command {
	var (
		opts   cephPoolOptions
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "pool <name>",
		Short: "Create the lab's Ceph RBD pool on its nested cluster",
		Long: "Create a replicated Ceph pool on a lab's node 0, through the lab's own " +
			"nested-cluster API context, and wire it up as VM/CT storage.\n\n" +
			"Idempotent: a pool the nested cluster already lists under the same name is skipped.\n\n" +
			"Requires topology.nodes >= 3: Ceph needs at least a 3-node lab.",
		Example: `  pmx lab ceph pool wayne
  pmx lab ceph pool wayne --name labrbd --size 3 --min-size 2
  pmx lab ceph pool wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCephPool(cmd, args[0], opts, dryRun)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.name, "name", "labrbd", "name of the pool to create")
	f.IntVar(&opts.size, "size", 3, "number of replicas per object")
	f.IntVar(&opts.minSize, "min-size", 2, "minimum number of replicas per object")
	f.StringVar(&opts.autoscaleMode, "pg-autoscale-mode", "on", "PG autoscale mode (on, off, or warn)")
	f.BoolVar(&opts.addStorages, "add-storages", true, "configure the new pool as VM and CT storage")
	f.BoolVar(&dryRun, "dry-run", false, "print what would run, without touching the lab context")
	return cmd
}

func runCephPool(cmd *cobra.Command, name string, opts cephPoolOptions, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	if _, err := cephResolveNodes(lab); err != nil {
		return err
	}

	// An empty --name would still probe cephPoolExists' name matching
	// (p.PoolName == "" || p.Name == "") and could match an unrelated
	// pool PVE reports with a blank name field, or otherwise reach
	// CreateCephPool with an empty pool name; refuse it up front instead,
	// before any API call at all.
	if strings.TrimSpace(opts.name) == "" {
		return fmt.Errorf("lab %q: --name is required and must not be empty", name)
	}

	node0 := labNodeVMName(lab.Name, 0)

	if dryRun {
		msg := fmt.Sprintf("[dry-run] would create Ceph pool %q on %s (size %d, min_size %d, "+
			"pg_autoscale_mode %s, add_storages %t)",
			opts.name, node0, opts.size, opts.minSize, opts.autoscaleMode, opts.addStorages)
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
	}

	api, err := cephInnerAPI(cmd, deps, lab)
	if err != nil {
		return err
	}

	row, err := ensureCephPool(cmd.Context(), api, node0, opts)
	if err != nil {
		return fmt.Errorf("lab %q: %w", name, err)
	}

	summary := fmt.Sprintf("lab %q: Ceph pool %q %s on %s.", name, opts.name, row.Status, node0)
	return cephRenderSteps(cmd, deps, []cephStepResult{row}, summary)
}

// cephStatusSummary is the slice of GET /nodes/{node}/ceph/status the
// FIELD/VALUE table reports. The rest of the payload still reaches the
// operator verbatim, through output.Result.Raw under --format json/yaml.
type cephStatusSummary struct {
	Health struct {
		Status string `json:"status"`
	} `json:"health"`
	Monmap struct {
		Mons []json.RawMessage `json:"mons"`
	} `json:"monmap"`
	Mgrmap struct {
		ActiveName string            `json:"active_name"`
		Standbys   []json.RawMessage `json:"standbys"`
	} `json:"mgrmap"`
	Osdmap struct {
		NumOsds   int `json:"num_osds"`
		NumUpOsds int `json:"num_up_osds"`
		NumInOsds int `json:"num_in_osds"`
	} `json:"osdmap"`
	Pgmap struct {
		NumPools int `json:"num_pools"`
	} `json:"pgmap"`
}

// mgrCount returns the number of manager daemons the status payload reports:
// the active one, when there is one, plus every standby.
func (s cephStatusSummary) mgrCount() int {
	n := len(s.Mgrmap.Standbys)
	if s.Mgrmap.ActiveName != "" {
		n++
	}
	return n
}

// newCephStatusCmd builds `pmx lab ceph status <name>`.
func newCephStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show a lab's nested Ceph cluster status",
		Long: "Read `ceph status` from a lab's node 0 through the lab's own nested-cluster API " +
			"context and report cluster health, daemon counts, OSD up/in totals, and pool count.\n\n" +
			"Read-only: never mutates anything. `--format json` carries the full status payload " +
			"verbatim, not just the summarized fields.",
		Example: `  pmx lab ceph status wayne
  pmx lab ceph status wayne --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCephStatus(cmd, args[0])
		},
	}
}

func runCephStatus(cmd *cobra.Command, name string) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLab(cmd, name)
	if err != nil {
		return err
	}

	node0 := labNodeVMName(lab.Name, 0)

	api, err := cephInnerAPI(cmd, deps, lab)
	if err != nil {
		return err
	}

	resp, err := api.Nodes.ListCephStatus(cmd.Context(), node0)
	if err != nil {
		return fmt.Errorf("lab %q: ceph status on %s: %w", name, node0, err)
	}
	raw := cephRawResponse(resp)

	var (
		st      cephStatusSummary
		payload any
	)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &st); err != nil {
			return fmt.Errorf("lab %q: decode ceph status from %s: %w", name, node0, err)
		}
		// The typed decode above already proved raw is well-formed JSON, so
		// this second pass (which feeds --format json/yaml the whole payload,
		// where a json.RawMessage would render as bytes) cannot fail.
		_ = json.Unmarshal(raw, &payload)
	}

	// An empty JSON object body decodes cleanly (every field stays its zero
	// value) but is not a genuinely healthy, empty cluster — it means the
	// nested cluster returned no usable health section at all. Report that
	// plainly instead of an empty health cell sitting beside all-zero
	// counts, which reads as a false "everything is fine, nothing exists"
	// signal.
	health := st.Health.Status
	if health == "" {
		health = "(no status reported)"
	}

	rows := [][]string{
		{"lab", name},
		{"queried node", node0},
		{"health", health},
		{"mons", strconv.Itoa(len(st.Monmap.Mons))},
		{"mgrs", strconv.Itoa(st.mgrCount())},
		{"osds", fmt.Sprintf("%d up / %d in / %d total",
			st.Osdmap.NumUpOsds, st.Osdmap.NumInOsds, st.Osdmap.NumOsds)},
		{"pools", strconv.Itoa(st.Pgmap.NumPools)},
	}

	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Headers: []string{"FIELD", "VALUE"}, Rows: rows, Raw: payload}, deps.Format)
}
