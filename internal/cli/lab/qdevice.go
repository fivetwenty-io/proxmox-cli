package lab

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
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

// qdeviceIPv6PersistPath is the ifupdown drop-in written on an
// ifupdown-managed QDevice VM so its management IPv6 address survives
// reboots (Debian's default /etc/network/interfaces sources
// interfaces.d/*).
const qdeviceIPv6PersistPath = "/etc/network/interfaces.d/lab-ipv6"

// qdeviceIPv6PersistDirs are every directory the convergence probe greps
// for a persisted copy of the planned address: the ifupdown drop-in's
// directory and netplan's. Which one actually holds it depends on the
// guest image's network stack, which is exactly what this pair exists to
// stop mattering — a QDevice image that renders its network with netplan
// (the lab's own tmpl-qdevice does) never reads interfaces.d at all, so an
// ifupdown-only persist looks applied while silently evaporating at the
// next reboot.
const qdeviceIPv6PersistDirs = "/etc/network/interfaces.d /etc/netplan"

// qdeviceNetplanOriginHint names the file `netplan set` is told to write
// (netplan appends the .yaml suffix), keeping every key this command writes
// in one file of its own.
//
// Without the hint, `netplan set` edits the file that already DEFINES the
// key — on a cloud image, that is 50-cloud-init.yaml, which cloud-init is
// free to regenerate from its datasource on the next boot, taking the
// address with it. The name sorts after the distro's own files, so on a
// netplan build that lets a later file override a key rather than merging
// into it, this file still wins.
const qdeviceNetplanOriginHint = "70-pmx-lab-ipv6"

// qdeviceNetplanFallbackPath is the netplan file written when `netplan set`
// is unavailable (netplan older than 0.98). It sorts after the distro's own
// 50-cloud-init.yaml, so its keys win the merge — which is why it re-states
// the interface's full address list and route list rather than only the
// IPv6 ones.
const qdeviceNetplanFallbackPath = "/etc/netplan/99-pmx-lab-ipv6.yaml"

// qdeviceNetplanMarker is echoed by the convergence probe on a guest whose
// network is netplan-rendered, selecting the persistence writer.
const qdeviceNetplanMarker = "PMX-NETPLAN-PRESENT"

// qdeviceStepResult records one step of `pmx lab qdevice add`'s execution
// for the final STEP/STATUS table, mirroring create.go's createStep render
// contract.
type qdeviceStepResult struct {
	desc string
	skip bool
	// note carries a caveat the operator must see even though the step
	// itself succeeded — e.g. a persistence path that could only be half
	// written. Rendered alongside the step's status.
	note string
}

// status renders the step's STATUS cell: its skip/done state, plus any note.
func (s qdeviceStepResult) status() string {
	base := "done"
	if s.skip {
		base = "skip (already satisfied)"
	}
	if s.note != "" {
		return base + ": " + s.note
	}
	return base
}

// newQdeviceCmd builds `pmx lab qdevice` and its subcommands.
func newQdeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qdevice",
		Short: "Add or remove a lab's corosync QDevice tie-breaker",
		Long: "Wire up or tear down the corosync-level QDevice tie-breaker for a lab whose " +
			"topology calls for one: mandatory at exactly 2 nodes, `qdevice: auto`-recommended " +
			"at 4.\n\n" +
			"`pmx lab create` already provisions the QDevice VM itself when the topology calls " +
			"for one. This group handles what goes on top of that VM: the " +
			"corosync-qnetd/corosync-qdevice package installation and the `pvecm qdevice " +
			"setup`/`remove` steps, all over ssh into the lab guests.",
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
		Long: "Install corosync-qnetd on the lab's QDevice VM, confirm or install " +
			"corosync-qdevice on every cluster node, then run `pvecm qdevice setup " +
			"<qdevice-mgmt-ip>` on node 0.\n\n" +
			"Unless `network.ipv6: false`, the QDevice VM also gets its management IPv6 address " +
			"from the lab's IPv6 plan (the mgmt ::f, mirroring its IPv4 .15), live and persisted " +
			"across reboots. Corosync itself keeps talking over IPv4.\n\n" +
			"The QDevice VM must already exist and be running; `pmx lab create` provisions it " +
			"when the lab's topology calls for one. The nested cluster must already be formed " +
			"(`pmx lab cluster init`/`join`), and the lab's topology must actually call for a " +
			"QDevice.\n\n" +
			"Every step is idempotent: an already-installed package, or an already-configured " +
			"QDevice, is skipped rather than re-applied.",
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
		if lab.Network.EffectiveIPv6() {
			addr6, aerr := labQdeviceMgmtIP6(lab.Network)
			if aerr != nil {
				return fmt.Errorf("resolve QDevice mgmt IPv6: %w", aerr)
			}
			rows = append(rows, []string{
				fmt.Sprintf("ensure IPv6 %s/%d on QDevice VM (%s)", addr6, labV6InterfacePrefixBits, qdeviceIP),
				"would run"})
		}
		for i := range numNodes {
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

	steps, err := qdeviceEnsureWired(deps, lab, name, qdeviceIP, node0IP, numNodes, cst.HasQdevice)
	if err != nil {
		return err
	}

	headers := []string{"STEP", "STATUS"}
	rows := make([][]string, 0, len(steps)+1)
	for _, s := range steps {
		rows = append(rows, []string{s.desc, s.status()})
	}
	rows = append(rows, []string{"summary", fmt.Sprintf("lab %q: QDevice wired up against cluster %q.", name, lab.Name)})

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}

// qdeviceEnsureWired installs corosync-qnetd on the QDevice VM,
// corosync-qdevice on every node 0..numNodes-1, and runs `pvecm qdevice
// setup` on node 0 (skipped if hasQdevice is already true) — the actual
// wiring work `qdevice add`'s RunE performs after its preconditions pass,
// factored out with no cobra/rendering coupling so `pmx lab scale`'s
// QDevice-add step can reuse the identical idempotent logic. Every returned
// qdeviceStepResult and error is produced exactly as runQdeviceAdd's
// original inline version did.
func qdeviceEnsureWired(
	deps *cli.Deps, lab *config.Lab, name, qdeviceIP, node0IP string, numNodes int, hasQdevice bool,
) ([]qdeviceStepResult, error) {
	var steps []qdeviceStepResult

	alreadyInstalled, err := ensureGuestPackage(deps, qdeviceIP, qdeviceQnetdPackage)
	if err != nil {
		return nil, fmt.Errorf("lab %q: %w", name, err)
	}
	steps = append(steps, qdeviceStepResult{
		desc: fmt.Sprintf("install %s on QDevice VM (%s)", qdeviceQnetdPackage, qdeviceIP), skip: alreadyInstalled})

	if lab.Network.EffectiveIPv6() {
		v6Step, verr := qdeviceEnsureIPv6(deps, lab.Network, name, qdeviceIP)
		if verr != nil {
			return nil, verr
		}
		steps = append(steps, v6Step)
	}

	for i := range numNodes {
		nodeIP, ierr := labNodeMgmtIP(lab.Network, i)
		if ierr != nil {
			return nil, fmt.Errorf("resolve node %d mgmt IP: %w", i, ierr)
		}
		already, perr := ensureGuestPackage(deps, nodeIP, qdeviceClientPackage)
		if perr != nil {
			return nil, fmt.Errorf("lab %q: %w", name, perr)
		}
		steps = append(steps, qdeviceStepResult{
			desc: fmt.Sprintf("install %s on node %d (%s)", qdeviceClientPackage, i, nodeIP), skip: already})
	}

	if hasQdevice {
		steps = append(steps, qdeviceStepResult{
			desc: fmt.Sprintf("pvecm qdevice setup %s on node 0", qdeviceIP), skip: true})
	} else {
		setupCmd := fmt.Sprintf("pvecm qdevice setup %s", qdeviceIP)
		if _, serr := runGuestSSH(deps, node0IP, setupCmd); serr != nil {
			return nil, fmt.Errorf("lab %q: qdevice setup against node 0 (%s): %w", name, node0IP, serr)
		}
		steps = append(steps, qdeviceStepResult{desc: fmt.Sprintf("pvecm qdevice setup %s on node 0", qdeviceIP)})
	}

	return steps, nil
}

// qdeviceIfaceNamePattern is the shape a remote-derived interface name must
// have before it may be interpolated into a shell command line: a plain
// Linux interface name, nothing a shell could interpret. The value comes
// from parsing `ip` output on the guest — root-owned, but a parse gone wrong
// (or an exotic link kind) must fail loudly here, not run as shell.
var qdeviceIfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// qdeviceIPv6Converged reports whether one combined probe output shows all
// three convergence markers at once: the live address with its EXACT
// planned prefix (`ip -6 addr`'s "inet6 <addr>/48" — a stale <addr>/64
// from an older run must not read as converged, since `ip -6 addr replace`
// cannot remove it), the DEFAULT route via the mgmt gateway (`ip -6 route
// show default`'s "default via <gw>"), and a persisted copy of the address
// in a config file THE GUEST'S OWN RENDERER READS (qdeviceIPv6Persisted).
// Anything less is a half-applied state the apply must repair — matching on
// the live address alone would read "addr landed, route/persist chain died"
// as converged forever.
func qdeviceIPv6Converged(probeOut, addr6, gw6 string) bool {
	return strings.Contains(probeOut, fmt.Sprintf("inet6 %s/%d", addr6, labV6InterfacePrefixBits)) &&
		strings.Contains(probeOut, "default via "+gw6) &&
		qdeviceIPv6Persisted(probeOut, qdeviceUsesNetplan(probeOut))
}

// qdeviceIPv6Persisted reports whether the probe's `grep -rl` phase named a
// config file that the guest's OWN renderer reads: a netplan document on a
// netplan guest, an ifupdown drop-in otherwise. Only paths under the two
// directories the probe searched are honored, so an unrelated line of `ip`
// output can never read as persistence.
//
// The stack has to gate this, not merely select the writer. A netplan guest
// carrying only /etc/network/interfaces.d/lab-ipv6 — exactly what an
// upgraded lab has, since the older ifupdown-only writer put it there — has
// its address in a file nothing reads, so it is NOT persisted, and a
// stack-agnostic check would report the pre-fix bug as converged forever.
//
// A netplan guest whose management interface netplan does not manage keeps
// its ifupdown drop-in (qdevicePersistIPv6Netplan falls back to writing
// one), and that case simply never reports converged: the apply path is
// idempotent and re-derives the same answer, which is a few ssh round trips
// against being wrong.
func qdeviceIPv6Persisted(probeOut string, netplan bool) bool {
	dir := "/etc/network/interfaces.d/"
	if netplan {
		dir = "/etc/netplan/"
	}
	for line := range strings.SplitSeq(probeOut, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), dir) {
			return true
		}
	}
	return false
}

// qdeviceUsesNetplan reports whether the probed guest renders its network
// with netplan, selecting which persistence writer qdeviceEnsureIPv6 uses.
func qdeviceUsesNetplan(probeOut string) bool {
	return strings.Contains(probeOut, qdeviceNetplanMarker)
}

// qdeviceParseNetplanList decodes the YAML `netplan get` prints for a list
// value: a block sequence ("- 10.0.1.15/24" per line), an inline flow
// sequence ("[10.0.1.15/24]"), or the literal "null" for an unset key
// (which decodes to no entries). Quotes around entries are stripped, so a
// value round-trips through a `netplan set` unchanged.
func qdeviceParseNetplanList(out string) []string {
	var items []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		l := strings.TrimSpace(line)
		switch {
		case l == "" || l == "null":
			continue
		case strings.HasPrefix(l, "- "):
			items = append(items, strings.Trim(strings.TrimSpace(l[2:]), `"'`))
		case strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]"):
			for part := range strings.SplitSeq(l[1:len(l)-1], ",") {
				if p := strings.Trim(strings.TrimSpace(part), `"'`); p != "" {
					items = append(items, p)
				}
			}
		}
	}
	return items
}

// qdeviceEnsureIPv6 ensures the QDevice VM (reached over ssh at its IPv4
// management address) carries its planned management IPv6 address —
// labQdeviceMgmtIP6's ::f, addressed with the /48 interface prefix per
// labV6InterfacePrefixBits — with a v6 default route via the management
// gateway, both live (ip) and persisted for reboots.
//
// Persistence is written for the stack the guest actually renders its
// network with, probed rather than assumed: an ifupdown drop-in at
// qdeviceIPv6PersistPath, or netplan (qdevicePersistIPv6Netplan) on an
// image whose network is netplan-rendered — the lab's own tmpl-qdevice
// image among them, which never reads interfaces.d, so an ifupdown-only
// persist there looks applied and then evaporates at the next reboot.
//
// The management interface is resolved from the VM's own IPv4 address
// rather than assumed, so a template with a different NIC name converges
// the same way. Idempotent two ways: one probe checks address, route, AND
// persistence together and skips only on all three, and the apply itself
// uses replace-style commands so re-running over a half-applied state
// repairs it rather than failing on what already exists. The nested PVE
// nodes get their IPv6 through `pmx lab hostnet apply`'s API-staged vmbr0
// path instead; the QDevice VM is a plain Debian guest with no PVE network
// API, hence ssh.
func qdeviceEnsureIPv6(
	deps *cli.Deps, n config.LabNetwork, name, qdeviceIP string,
) (qdeviceStepResult, error) {
	addr6, err := labQdeviceMgmtIP6(n)
	if err != nil {
		return qdeviceStepResult{}, fmt.Errorf("resolve QDevice mgmt IPv6: %w", err)
	}
	gw6, err := labMgmtGateway6(n)
	if err != nil {
		return qdeviceStepResult{}, fmt.Errorf("resolve mgmt IPv6 gateway: %w", err)
	}
	desc := fmt.Sprintf("ensure IPv6 %s/%d on QDevice VM (%s)", addr6, labV6InterfacePrefixBits, qdeviceIP)

	// One compound probe for all three convergence markers plus the guest's
	// network stack; the trailing `true` pins exit 0 (grep exits 1 when it
	// matches nothing), so a non-nil error here is a transport-level
	// failure, never "not converged".
	probeCmd := fmt.Sprintf(
		"ip -6 addr show to %[1]s/128; ip -6 route show default; "+
			"grep -rlsF -- '%[1]s/%[2]d' %[3]s; "+
			"command -v netplan >/dev/null 2>&1 && echo %[4]s; true",
		addr6, labV6InterfacePrefixBits, qdeviceIPv6PersistDirs, qdeviceNetplanMarker)
	probe, perr := runGuestSSH(deps, qdeviceIP, probeCmd)
	if perr != nil && guestCommandTransportFailed(perr) {
		return qdeviceStepResult{}, fmt.Errorf("lab %q: probe IPv6 on QDevice VM (%s): %w", name, qdeviceIP, perr)
	}
	if perr == nil && qdeviceIPv6Converged(probe.Stdout, addr6, gw6) {
		return qdeviceStepResult{desc: desc, skip: true}, nil
	}

	ifaceProbe, ferr := runGuestSSH(deps, qdeviceIP, fmt.Sprintf("ip -o -4 addr show to %s/32", qdeviceIP))
	if ferr != nil {
		return qdeviceStepResult{}, fmt.Errorf(
			"lab %q: resolve QDevice VM management interface (%s): %w", name, qdeviceIP, ferr)
	}
	// `ip -o -4 addr show` one-line format: "2: ens18    inet 10.0.1.15/24 ...".
	fields := strings.Fields(ifaceProbe.Stdout)
	if len(fields) < 2 {
		return qdeviceStepResult{}, fmt.Errorf(
			"lab %q: QDevice VM (%s) reports no interface holding its management IPv4 address; "+
				"cannot pick an interface for IPv6", name, qdeviceIP)
	}
	// Some link kinds print as "eth0@if5"; only the part before '@' is the
	// interface name.
	iface, _, _ := strings.Cut(fields[1], "@")
	if !qdeviceIfaceNamePattern.MatchString(iface) {
		return qdeviceStepResult{}, fmt.Errorf(
			"lab %q: QDevice VM (%s) reports management interface name %q, which is not a plain "+
				"interface name; refusing to use it in a shell command", name, qdeviceIP, fields[1])
	}

	// The live half is identical for either stack: the address and the
	// default route the cluster needs right now.
	liveCmd := fmt.Sprintf(
		"ip -6 addr replace %[1]s/%[2]d dev %[3]s && ip -6 route replace default via %[4]s dev %[3]s",
		addr6, labV6InterfacePrefixBits, iface, gw6)

	// An ifupdown guest persists in the same breath — one command, so a
	// half-applied state cannot be created by the transport dying between
	// two of them. A netplan guest needs its config read before it can be
	// written (qdevicePersistIPv6Netplan), which no single command can do.
	if !qdeviceUsesNetplan(probe.Stdout) {
		applyCmd := liveCmd + fmt.Sprintf(
			" && printf '%%s\\n' 'iface %[3]s inet6 static' '\taddress %[1]s/%[2]d' '\tgateway %[4]s' > %[5]s",
			addr6, labV6InterfacePrefixBits, iface, gw6, qdeviceIPv6PersistPath)
		if _, aerr := runGuestSSH(deps, qdeviceIP, applyCmd); aerr != nil {
			return qdeviceStepResult{}, fmt.Errorf("lab %q: add IPv6 to QDevice VM (%s): %w", name, qdeviceIP, aerr)
		}
		return qdeviceStepResult{desc: desc}, nil
	}

	if _, aerr := runGuestSSH(deps, qdeviceIP, liveCmd); aerr != nil {
		return qdeviceStepResult{}, fmt.Errorf("lab %q: add IPv6 to QDevice VM (%s): %w", name, qdeviceIP, aerr)
	}
	note, perr := qdevicePersistIPv6Netplan(deps, name, qdeviceIP, iface, addr6, gw6)
	if perr != nil {
		return qdeviceStepResult{}, perr
	}
	return qdeviceStepResult{desc: desc, note: note}, nil
}

// qdevicePersistIPv6Netplan persists the QDevice VM's management IPv6 on a
// netplan-rendered guest, returning any caveat the operator must still see.
//
// Three netplan facts shape this. First, `netplan set` writes into the file
// that already defines the key unless it is told otherwise, which on a cloud
// image means editing 50-cloud-init.yaml — a file cloud-init may regenerate
// on the next boot; every write here therefore carries --origin-hint. Second,
// whether a list in a later-sorting file merges into the earlier one or
// replaces it varies by netplan build, so the address and route lists are
// read back and re-stated in FULL: writing the IPv6 entries alone would drop
// the guest's IPv4 address and default route on a build that replaces.
// Third, an interface netplan does not know about must not be given a stanza
// here at all: doing so hands it to networkd on the next boot, taking it away
// from whatever does manage it today. That case falls back to the ifupdown
// drop-in, which is what such a guest reads anyway.
//
// Nothing is applied: `netplan apply` would re-render every interface,
// including the management one this command is reached over, to no benefit —
// the live address is already in place. `netplan generate` validates the
// written config instead, so a file that would fail at boot fails here.
func qdevicePersistIPv6Netplan(
	deps *cli.Deps, name, qdeviceIP, iface, addr6, gw6 string,
) (note string, err error) {
	want := fmt.Sprintf("%s/%d", addr6, labV6InterfacePrefixBits)

	ifaceCfg, err := runGuestSSH(deps, qdeviceIP, fmt.Sprintf("netplan get ethernets.%s", iface))
	if err != nil {
		return "", fmt.Errorf(
			"lab %q: read netplan config for %q on QDevice VM (%s): %w", name, iface, qdeviceIP, err)
	}
	if strings.TrimSpace(ifaceCfg.Stdout) == "null" || strings.TrimSpace(ifaceCfg.Stdout) == "" {
		persistCmd := fmt.Sprintf(
			"printf '%%s\\n' 'iface %[3]s inet6 static' '\taddress %[1]s/%[2]d' '\tgateway %[4]s' > %[5]s",
			addr6, labV6InterfacePrefixBits, iface, gw6, qdeviceIPv6PersistPath)
		if _, aerr := runGuestSSH(deps, qdeviceIP, persistCmd); aerr != nil {
			return "", fmt.Errorf(
				"lab %q: persist IPv6 on QDevice VM (%s): %w", name, qdeviceIP, aerr)
		}
		return fmt.Sprintf(
			"netplan is installed but does not manage %s; persisted to %s instead",
			iface, qdeviceIPv6PersistPath), nil
	}

	addrsOut, err := runGuestSSH(deps, qdeviceIP, fmt.Sprintf("netplan get ethernets.%s.addresses", iface))
	if err != nil {
		return "", fmt.Errorf(
			"lab %q: read netplan addresses for %q on QDevice VM (%s): %w", name, iface, qdeviceIP, err)
	}
	addrs := qdeviceParseNetplanList(addrsOut.Stdout)
	if !slices.Contains(addrs, want) {
		addrs = append(addrs, want)
	}

	// The v6 default route is merged into whatever the interface already
	// declares, and both lists are then re-stated in full. Netplan builds
	// differ in whether a list in a later-sorting file is appended to the
	// earlier one or replaces it outright (this lab's Debian 13 guests
	// append); re-stating is correct under either reading, while writing
	// only the IPv6 route would drop the guest's IPv4 default route on a
	// build that replaces.
	routesOut, err := runGuestSSH(deps, qdeviceIP, fmt.Sprintf("netplan get ethernets.%s.routes", iface))
	if err != nil {
		return "", fmt.Errorf(
			"lab %q: read netplan routes for %q on QDevice VM (%s): %w", name, iface, qdeviceIP, err)
	}
	routesVal, routeNote := qdeviceNetplanRoutesValue(routesOut.Stdout, gw6, iface)

	// Every entry is double-quoted inside the single-quoted shell argument:
	// an unquoted IPv6 address is a plain scalar whose colons a YAML flow
	// sequence is entitled to read as a mapping separator, and which parser
	// netplan happens to be built against is not this command's business.
	quoted := make([]string, 0, len(addrs))
	for _, a := range addrs {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	set := fmt.Sprintf("netplan set --origin-hint %s ", qdeviceNetplanOriginHint)
	setCmd := set + fmt.Sprintf("'ethernets.%s.addresses=[%s]'", iface, strings.Join(quoted, ","))
	if routesVal != "" {
		setCmd += " && " + set + fmt.Sprintf("'ethernets.%s.routes=%s'", iface, routesVal)
	}
	setCmd += " && netplan generate"

	if _, serr := runGuestSSH(deps, qdeviceIP, setCmd); serr != nil {
		// `netplan set` has shipped since netplan 0.98 (both Debian 12 and
		// Ubuntu 22.04 carry it), but an older image would fail the whole
		// chain above; fall back to a last-sorting file carrying the same
		// merged address and route lists.
		fallback, ferr := qdeviceNetplanFallbackFile(iface, addrs, routesVal)
		if ferr != nil {
			return "", fmt.Errorf(
				"lab %q: persist IPv6 via netplan on QDevice VM (%s): %w (fallback render also failed: %v)",
				name, qdeviceIP, serr, ferr)
		}
		writeCmd := fmt.Sprintf("umask 077 && cat > %s <<'PMXEOF'\n%sPMXEOF\nnetplan generate",
			qdeviceNetplanFallbackPath, fallback)
		if _, werr := runGuestSSH(deps, qdeviceIP, writeCmd); werr != nil {
			return "", fmt.Errorf(
				"lab %q: persist IPv6 via netplan on QDevice VM (%s): %w (fallback write also failed: %v)",
				name, qdeviceIP, serr, werr)
		}
		note = fmt.Sprintf("`netplan set` unavailable; wrote %s", qdeviceNetplanFallbackPath)
	}

	if routeNote != "" {
		if note != "" {
			return note + "; " + routeNote, nil
		}
		return routeNote, nil
	}
	return note, nil
}

// qdeviceNetplanRoutesValue renders the routes value to write for iface: the
// routes `netplan get` reported, with the lab's IPv6 default route appended
// when it is not already among them, as the JSON `netplan set` takes for a
// list value. It returns "" when the routes must be left alone, in which
// case note says why.
//
// A route list this code cannot parse, or one carrying a single quote (which
// would end the shell argument the value is passed inside), is left
// untouched on purpose: re-stating a structure it did not fully understand
// is how a working IPv4 default route gets silently rewritten.
func qdeviceNetplanRoutesValue(routesOut, gw6, iface string) (value, note string) {
	trimmed := strings.TrimSpace(routesOut)

	var routes []any
	if trimmed != "" && trimmed != "null" {
		if err := yaml.Unmarshal([]byte(routesOut), &routes); err != nil {
			return "", fmt.Sprintf(
				"could not parse the netplan routes already on %s (%v); add the ::/0 route via %s by hand",
				iface, err, gw6)
		}
	}

	declared := false
	for _, r := range routes {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if to, _ := m["to"].(string); to == "::/0" {
			declared = true
			break
		}
	}
	if !declared {
		routes = append(routes, map[string]any{"to": "::/0", "via": gw6})
	}

	raw, err := json.Marshal(routes)
	if err != nil {
		return "", fmt.Sprintf(
			"could not render the netplan routes for %s (%v); add the ::/0 route via %s by hand",
			iface, err, gw6)
	}
	if strings.ContainsAny(string(raw), "'\n") {
		return "", fmt.Sprintf(
			"the netplan routes already on %s carry a quote or newline this command will not pass through "+
				"a shell; add the ::/0 route via %s by hand", iface, gw6)
	}
	return string(raw), ""
}

// qdeviceNetplanFallbackFile renders the netplan document written when
// `netplan set` is unavailable: the interface's full address list and its
// full route list (both merged from what the guest reported, this command's
// IPv6 entries included), since this file's keys win the merge.
func qdeviceNetplanFallbackFile(iface string, addrs []string, routesValue string) (string, error) {
	ifaceCfg := map[string]any{"addresses": addrs}
	if routesValue != "" {
		var routes []any
		if err := json.Unmarshal([]byte(routesValue), &routes); err != nil {
			return "", fmt.Errorf("decode merged netplan routes: %w", err)
		}
		ifaceCfg["routes"] = routes
	}
	doc, err := yaml.Marshal(map[string]any{
		"network": map[string]any{
			"version":   2,
			"ethernets": map[string]any{iface: ifaceCfg},
		},
	})
	if err != nil {
		return "", fmt.Errorf("render netplan document: %w", err)
	}
	return string(doc), nil
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
			"the nested cluster's corosync quorum config.\n\n" +
			"Idempotent: if node 0 does not report a registered QDevice, the command reports " +
			"it as already absent and does nothing further.\n\n" +
			"This does NOT destroy the QDevice VM itself. Use `pmx pve qemu delete <vmid>` to " +
			"delete the QDevice VM directly, or a full `pmx lab destroy`, for that once no " +
			"cluster references it.\n\n" +
			"Per multi-node lab plan §9, this must run BEFORE any node join that would " +
			"otherwise leave the vote count in an odd+witness (Last-Man-Standing) shape, " +
			"never simultaneously with a join.",
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

	message, err := ensureQdeviceRemoved(deps, lab, name, node0IP)
	if err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: message}, deps.Format)
}

// ensureQdeviceRemoved performs `qdevice remove`'s actual work — probe,
// then `pvecm qdevice remove` if a QDevice is registered — without any
// cobra/rendering coupling, so `pmx lab scale`'s QDevice-parity-first step
// can reuse the identical idempotent logic runQdeviceRemove's RunE wraps.
// The returned message distinguishes "removed" from "already absent" (its
// text differs, matching runQdeviceRemove's original two outcomes exactly)
// for callers that want to report which one happened.
func ensureQdeviceRemoved(deps *cli.Deps, lab *config.Lab, name, node0IP string) (message string, err error) {
	removeCmd := "pvecm qdevice remove"

	probe, perr := runGuestSSH(deps, node0IP, "pvecm status")
	if perr != nil && guestCommandTransportFailed(perr) {
		return "", fmt.Errorf("probe node 0 (%s) cluster state: %w", node0IP, perr)
	}
	st := parsePvecmStatus(probe.Stdout)
	if !st.HasQdevice {
		return fmt.Sprintf(
			"lab %q: node 0 (%s) reports no registered QDevice; nothing to do.", name, node0IP), nil
	}

	if _, err := runGuestSSH(deps, node0IP, removeCmd); err != nil {
		return "", fmt.Errorf("lab %q: remove qdevice from node 0 (%s): %w", name, node0IP, err)
	}

	return fmt.Sprintf("lab %q: QDevice removed from cluster %q.", name, lab.Name), nil
}
