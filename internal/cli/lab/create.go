package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/clusterstorage"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/pools"
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"
	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// createSubnetType is the fixed "type" value the PVE API requires when
// creating an SDN subnet.
const createSubnetType = "subnet"

// qdeviceVCPU, qdeviceMemoryGB, and qdeviceDiskGB are the fixed QDevice VM
// spec (multi-node lab plan §4.3, §6.4): a tiny witness VM, never resized by
// lab config, topology.node_overrides, or CLI sizing flags — those all
// apply to node VMs only.
const (
	qdeviceVCPU     = 1
	qdeviceMemoryGB = 1
	qdeviceDiskGB   = 8
)

// createCapacityWarnRatio and createCapacityRefuseRatio are the pool-fill
// thresholds the capacity gate (createCapacityGate) checks aggregate lab
// refquota reservation against, relative to the shared ZFS pool's live total
// size (multi-node lab plan §3.4): a warning above 75%, a hard refusal
// (absent --force) above 85%.
const (
	createCapacityWarnRatio   = 0.75
	createCapacityRefuseRatio = 0.85
)

// createStorageEntry is the subset of a /cluster/storage element this command
// needs to decide whether a storage definition already exists.
type createStorageEntry struct {
	Storage string `json:"storage"`
}

// createPoolEntry is the subset of a /pools element this command needs to
// decide whether a resource pool already exists.
type createPoolEntry struct {
	Poolid string `json:"poolid"`
}

// createQemuEntry is the subset of a /nodes/{node}/qemu element this command
// needs to find an already-existing lab VM by name.
type createQemuEntry struct {
	VMID int64  `json:"vmid"`
	Name string `json:"name"`
}

// createPoolMember is the decoded shape of one entry from
// pools.GetPoolsResponse.Members that buildCreatePlan's per-node/QDevice VM
// lookup needs: enough to classify which node/QDevice role (if any) each
// pool member already fills, by its live name (lifecycle.go's
// classifyLabVMName). destroy.go uses the cluster-wide listLiveVMs/
// findLabVMs pair instead of this pool-scoped GetPools lookup, since it
// needs each VM's live power state too (to decide whether to stop it
// before deleting); this type stays pool-scoped because buildCreatePlan
// only ever needs identity, not live state, for its existing-VM lookup.
type createPoolMember struct {
	ID   string     `json:"id"`
	Type string     `json:"type"`
	Node string     `json:"node"`
	Name string     `json:"name"`
	VMID pve.PVEInt `json:"vmid"`
}

// createOverrides holds the parsed --vcpu/--memory-*/--data-disk-gb/--os-disk-gb/
// --vxlan-tag/--cidr/--pool/--clone-from/--nodes/--qdevice/--qdevice-clone-from
// flag values for `pmx lab create`. Values are only applied to the effective
// lab copy when cmd.Flags().Changed reports the corresponding flag was
// actually passed (flag-over-config precedence).
type createOverrides struct {
	vcpu             int
	memMaxGB         int
	memMinGB         int
	dataDiskGB       int
	osDiskGB         int
	vxlanTag         int
	cidr             string
	pool             string
	cloneFrom        string
	nodes            int
	qdevice          string
	qdeviceCloneFrom string
}

// createStep is one entry in the ordered plan `pmx lab create` builds before
// mutating anything. desc is the human-readable description rendered in both
// --dry-run preview and normal output; skip/skipReason record an idempotency
// check already performed against live state; apply performs the mutation and
// is nil for a skipped step or when building a --dry-run-only plan.
type createStep struct {
	desc       string
	skip       bool
	skipReason string
	apply      func(ctx context.Context) error
}

// createNodePlan records one create target's (a node index or the QDevice)
// resolved naming/VMID state, accumulated by buildCreatePlan's per-target
// loop and read by renderCreatePlan to compose the final summary message.
type createNodePlan struct {
	label     string // "0".."4" or "q"
	vmName    string
	vmid      int64
	vmidKnown bool
	node      string // the PVE host node this VM lives, or will live, on
}

// createPlan is the fully-resolved, ordered set of operations `pmx lab create`
// will perform (or preview): the shared zone/vnet/subnet/storage/pool steps,
// plus one VM step (and, when --start is set, one start step) per configured
// node and, when the lab's topology calls for one, the QDevice.
type createPlan struct {
	steps           []createStep
	labName         string
	node            string
	storageID       string
	nodePlans       []createNodePlan
	agentNote       string
	capacityWarning string
	contextNote     string
}

// createPtr returns a pointer to v, for building the many optional pointer
// fields the generated API client params types expose.
func createPtr[T any](v T) *T { return &v }

// newCreateCmd builds `pmx lab create <name>`.
func newCreateCmd() *cobra.Command {
	var (
		dryRun    bool
		force     bool
		node      string
		start     bool
		noContext bool
		ov        createOverrides
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a lab's SDN network, storage, and node (and QDevice) VMs",
		Long: "Create a lab: idempotently ensures the lab's config-resolved SDN zone " +
			"(defaulting to zone \"labs\", type \"simple\" — decision D4), the lab's own vnet " +
			"and subnet, its derived lab storage (tank-lab-wayne for lab wayne), and its " +
			"resource pool all exist, then creates one VM per configured topology.nodes index " +
			"(or clones each from an existing VM given --clone-from) plus, when the lab's " +
			"topology calls for a QDevice tie-breaker, a QDevice VM (or clones it given " +
			"--qdevice-clone-from), applying each target's resolved compute spec. Every step " +
			"queries live state first and skips anything already satisfied, so re-running " +
			"create against a partially-built lab is safe. Before adding any node/QDevice VM " +
			"step, a capacity gate sums every configured lab's ZFS refquota reservation " +
			"against the shared pool's live size: it warns above 75% full and refuses above " +
			"85% full unless --force is passed.\n\n" +
			"This does not run `pmx lab net apply`: the vnet/subnet definitions are staged, not yet " +
			"live, until that command (or `pmx pve sdn apply`) commits them. --clone-from assumes " +
			"the source VM lives on the same node as the new lab VMs; the platform is single-node " +
			"today, so this always holds.\n\n" +
			"Every other lab verb (destroy, start, stop, list, status) locates the lab's VMs by " +
			"membership in the effective resource pool, not by name: a --pool override here must " +
			"match the lab's configured access.pool, or the config must be updated to match, or " +
			"those verbs will report no VM found even though create succeeded.",
		Example: `  pmx lab create wayne --node sm-0
  pmx lab create wayne --node sm-0 --start
  pmx lab create wayne --node sm-0 --dry-run
  pmx lab create wayne --node sm-0 --vcpu 24 --memory-max-gb 128
  pmx lab create pve-cpi --node sm-0 --nodes 3
  pmx lab create wayne --node sm-0 --nodes 2 --qdevice auto`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			deps := cli.GetDeps(cmd)

			lab, err := resolveLabForMutate(cmd, name)
			if err != nil {
				return err
			}

			fl := cmd.Flags()
			eff := applyCreateOverrides(fl, lab, ov)

			// A lab created from an incoherent address plan comes up broken —
			// guests and node unable to reach each other on their shared vnet —
			// so refuse before creating anything.
			if issues := labNetworkPlanIssues(eff.Network); len(issues) > 0 {
				return fmt.Errorf("lab %q network plan is incoherent:\n  %s",
					name, strings.Join(issues, "\n  "))
			}

			// --nodes/--qdevice can move the lab's topology to a state config-load
			// validation never saw; re-validate the effective topology the same
			// way, so a flag combination that would never be accepted in config
			// (e.g. --nodes 7, or --qdevice auto against an odd --nodes) is
			// refused before creating anything either.
			if issues := config.ValidateTopology(name, eff.Topology); len(issues) > 0 {
				return fmt.Errorf("lab %q topology is invalid:\n  %s", name, strings.Join(issues, "\n  "))
			}

			// Flag overrides (in particular --pool) can change an identifier
			// after the initial resolveLabForMutate guard already passed
			// against the pre-override config; guard the effective values too
			// so a flag cannot route the command at a protected peppi
			// resource that the base config did not name.
			effTarget := peppi.Target{
				Names: []string{eff.Network.VnetID, labPoolID(eff), storageID(eff), eff.DNS.Zone, eff.Name},
			}
			if err := peppi.Guard(effTarget); err != nil {
				return err
			}

			targetNode := deps.Node
			if fl.Changed("node") {
				targetNode = node
			}
			if targetNode == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}

			plan, err := buildCreatePlan(
				cmd.Context(), deps, eff, targetNode, ov.cloneFrom, ov.qdeviceCloneFrom, start, dryRun, force)
			if err != nil {
				return err
			}

			if dryRun {
				return renderCreatePlan(cmd, deps, plan, true)
			}

			for _, step := range plan.steps {
				if step.skip || step.apply == nil {
					continue
				}
				if err := step.apply(cmd.Context()); err != nil {
					return fmt.Errorf("%s: %w", step.desc, err)
				}
			}

			plan.contextNote = runCreateContextHook(cmd, deps, eff, name, start, noContext)
			return renderCreatePlan(cmd, deps, plan, false)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "preview the ordered plan without mutating anything")
	f.BoolVar(&force, "force", false, "override the capacity gate's 85% pool-fill refusal threshold")
	f.StringVar(&node, "node", "", "node to create the lab's VMs on (defaults to --node/PMX_NODE/config default)")
	f.BoolVar(&start, "start", false, "start every created VM after creation and verify the guest agent responds")
	f.BoolVar(&noContext, "no-context", false, "skip auto-registering the lab-<name> pmx context after --start")
	f.IntVar(&ov.vcpu, "vcpu", 0, "override compute.vcpu (base sizing for every node before per-node overrides)")
	f.IntVar(&ov.memMaxGB, "memory-max-gb", 0, "override compute.memory.max_gb")
	f.IntVar(&ov.memMinGB, "memory-min-gb", 0, "override compute.memory.min_gb")
	f.IntVar(&ov.dataDiskGB, "data-disk-gb", 0, "override storage.data_disk_gb")
	f.IntVar(&ov.osDiskGB, "os-disk-gb", 0, "override storage.os_disk_gb")
	f.IntVar(&ov.vxlanTag, "vxlan-tag", 0, "override network.vxlan_tag")
	f.StringVar(&ov.cidr, "cidr", "", "override network.cidr")
	f.StringVar(&ov.pool, "pool", "",
		"override access.pool (destroy/start/stop/list/status locate the VMs by resource-pool "+
			"membership; a --pool override must match the lab's configured pool, or those verbs "+
			"will not find them until the config is updated to match)")
	f.StringVar(&ov.cloneFrom, "clone-from", "",
		"VMID of an existing VM to clone every node's VM from, instead of creating blank disks")
	f.IntVar(&ov.nodes, "nodes", 0, "override topology.nodes (1-5)")
	f.StringVar(&ov.qdevice, "qdevice", "", "override topology.qdevice (\"auto\" or \"never\")")
	f.StringVar(&ov.qdeviceCloneFrom, "qdevice-clone-from", "",
		"VMID of an existing template (e.g. tmpl-qdevice) to clone the QDevice VM from")

	return cmd
}

// applyCreateOverrides returns a copy of lab with every config-override flag
// that was actually passed on the command line applied on top of it. lab
// itself is never mutated: config.Lab's nested fields are all plain structs
// (not pointers), so the top-level copy also copies every nested section by
// value — except Topology.NodeOverrides, a map, which is copied explicitly
// below so mutating the copy's Topology never aliases lab's own map.
func applyCreateOverrides(fl interface{ Changed(string) bool }, lab *config.Lab, ov createOverrides) *config.Lab {
	eff := *lab

	if lab.Topology.NodeOverrides != nil {
		eff.Topology.NodeOverrides = make(map[int]config.LabNodeOverride, len(lab.Topology.NodeOverrides))
		for k, v := range lab.Topology.NodeOverrides {
			eff.Topology.NodeOverrides[k] = v
		}
	}

	if fl.Changed("vcpu") {
		eff.Compute.VCPU = ov.vcpu
	}
	if fl.Changed("memory-max-gb") {
		eff.Compute.Memory.MaxGB = ov.memMaxGB
	}
	if fl.Changed("memory-min-gb") {
		eff.Compute.Memory.MinGB = ov.memMinGB
	}
	if fl.Changed("data-disk-gb") {
		eff.Storage.DataDiskGB = ov.dataDiskGB
	}
	if fl.Changed("os-disk-gb") {
		eff.Storage.OSDiskGB = ov.osDiskGB
	}
	if fl.Changed("vxlan-tag") {
		eff.Network.VxlanTag = ov.vxlanTag
	}
	if fl.Changed("cidr") {
		eff.Network.CIDR = ov.cidr
	}
	if fl.Changed("pool") {
		eff.Access.Pool = ov.pool
	}
	if fl.Changed("nodes") {
		eff.Topology.Nodes = ov.nodes
	}
	if fl.Changed("qdevice") {
		eff.Topology.Qdevice = ov.qdevice
	}

	return &eff
}

// createDiskOptions renders the comma-prefixed discard/iothread/ssd option
// suffix for a scsi disk string from a lab's storage settings, e.g.
// ",discard=on,iothread=1,ssd=1". Returns "" when none of the three are set.
func createDiskOptions(st config.LabStorage) string {
	var opts []string
	if st.Discard {
		opts = append(opts, "discard=on")
	}
	if st.IOThread {
		opts = append(opts, "iothread=1")
	}
	if st.SSD {
		opts = append(opts, "ssd=1")
	}
	if len(opts) == 0 {
		return ""
	}
	return "," + strings.Join(opts, ",")
}

// createDecodeNextID decodes the raw response of GET /cluster/nextid, which
// PVE returns as a JSON string (e.g. "100") on real servers; a bare JSON
// number is also accepted defensively in case of an API version difference.
func createDecodeNextID(raw json.RawMessage) (int64, error) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		id, perr := strconv.ParseInt(asString, 10, 64)
		if perr != nil {
			return 0, fmt.Errorf("decode next VMID: %q is not a number: %w", asString, perr)
		}
		return id, nil
	}

	var asNumber int64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber, nil
	}

	return 0, fmt.Errorf("decode next VMID: unrecognized response %q", string(raw))
}

// createCapacityStorageEntry is the subset of a /cluster/storage element
// createResolveCapacityDenominator needs to decide whether an entry backs
// the capacity gate's base pool. Nodes is the storage's optional
// node-restriction list (comma-separated PVE node names; empty means "every
// node") — see createStorageAvailableOnNode.
type createCapacityStorageEntry struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Pool    string `json:"pool"`
	Nodes   string `json:"nodes"`
}

// createZfsPoolEntry is the subset of a GET /nodes/{node}/disks/zfs element
// (PVE's `zpool list` equivalent) createResolveCapacityDenominator's
// pool-level fallback needs: Size and Alloc are the zpool's true,
// dataset-quota-independent total and allocated byte counts.
type createZfsPoolEntry struct {
	Name  string     `json:"name"`
	Size  pve.PVEInt `json:"size"`
	Alloc pve.PVEInt `json:"alloc"`
}

const createZfspoolType = "zfspool"

// createStorageAvailableOnNode reports whether a /cluster/storage entry
// whose "nodes" attribute is nodesAttr (the raw, possibly empty,
// comma-separated PVE node-restriction string) is available on node: an
// empty/unset restriction means every node, per PVE's storage.cfg
// semantics.
func createStorageAvailableOnNode(nodesAttr, node string) bool {
	if nodesAttr == "" {
		return true
	}
	for _, n := range strings.Split(nodesAttr, ",") {
		if strings.TrimSpace(n) == node {
			return true
		}
	}
	return false
}

// createCapacityDenominator carries the live total/used bytes
// createCapacityGate compares its numerator against, plus a human-readable
// label identifying the source for the gate's skip/warn/refuse messages.
type createCapacityDenominator struct {
	totalBytes int64
	usedBytes  int64
	label      string
}

// createReadStorageStatus reads GET /nodes/{node}/storage/{storageID}/status
// and reports the denominator it carries. found is false (nil error) when
// the storage is reachable but reports no usable total (e.g. disabled), the
// same "nothing learned, but not itself an error" signal
// createResolveCapacityDenominator's other sources use to fall through.
func createReadStorageStatus(ctx context.Context, deps *cli.Deps, node, storageID, label string) (createCapacityDenominator, bool, error) {
	status, err := deps.API.Nodes.ListStorageStatus(ctx, node, storageID)
	if err != nil {
		return createCapacityDenominator{}, false, err
	}
	if status == nil || status.Total == nil || status.Total.Int() <= 0 {
		return createCapacityDenominator{}, false, nil
	}
	var usedBytes int64
	if status.Used != nil {
		usedBytes = status.Used.Int()
	}
	return createCapacityDenominator{totalBytes: status.Total.Int(), usedBytes: usedBytes, label: label}, true, nil
}

// createResolveCapacityDenominator finds the live total/used bytes
// createCapacityGate should compare its numerator against for basePool, in
// order:
//
//  1. deps.Cfg.Storage.CapacityStorageID, when the operator set it: read via
//     GET /nodes/{node}/storage/{storage}/status verbatim — the documented
//     escape hatch for hosts whose storage naming does not fit the
//     heuristics below.
//  2. A zfspool-type storage registered in /cluster/storage whose "pool"
//     attribute is basePool verbatim (e.g. "tank", never a nested dataset —
//     see the disks/zfs fallback below for why a nested match is never
//     used), available on node (a node-restricted candidate excluding node
//     is skipped — a node-scoped storage's status is only queryable on
//     nodes it is actually enabled on): read the same way.
//  3. GET /nodes/{node}/disks/zfs (PVE's `zpool list` equivalent): the entry
//     whose "name" is basePool, using its "size"/"alloc" fields directly.
//     This is the fallback for the real fleet shape the capacity gate's
//     storage-lookup fix targets — hosts with no storage.cfg entry rooted
//     at the bare pool name, only per-lab "tank-lab-<member>" entries
//     nested under it (e.g. pool "tank/labs/wayne"). A per-lab dataset's
//     own status is deliberately NEVER used as a stand-in for the pool
//     here: PVE's zfspool storage status reads that dataset's own zfs
//     avail/used, capped by whatever refquota `pmx lab quota set` applies
//     to it (quota.go), so its "total" reflects that ONE lab's refquota —
//     while createCapacityGate's numerator sums EVERY configured lab's
//     refquota. Measuring a fleet-wide numerator against a single lab's
//     quota inflates the ratio by roughly the lab count and refuses by
//     default on exactly the fleet shape this fallback exists to serve.
//     disks/zfs reports the zpool's actual total/allocated bytes,
//     independent of any dataset-level quota, so it is the only sound
//     source once no pool-rooted storage.cfg entry exists.
//
// err is non-nil only when an API call itself failed (network/decode
// error): the caller degrades to a skipped gate in that case, since a
// transient failure is not itself evidence the pool is full. When every
// source above found nothing for basePool, found is false with a nil
// error — the caller must treat that as a real gap (no live capacity
// signal exists for this pool at all) and refuse loudly rather than
// silently skip.
func createResolveCapacityDenominator(
	ctx context.Context, deps *cli.Deps, node, basePool string,
) (denom createCapacityDenominator, found bool, err error) {
	if deps.Cfg != nil && deps.Cfg.Storage.CapacityStorageID != "" {
		storageID := deps.Cfg.Storage.CapacityStorageID
		d, ok, statusErr := createReadStorageStatus(ctx, deps, node, storageID,
			fmt.Sprintf("storage %q (storage.capacity_storage_id override)", storageID))
		if statusErr != nil {
			return createCapacityDenominator{}, false, statusErr
		}
		if !ok {
			// An explicit override reporting no usable size degrades to a
			// skipped gate (via the non-nil error, caught by the caller),
			// not a hard refusal: unlike the "nothing matched at all" case
			// below, the operator did point at a real, reachable storage —
			// treating that as the "no live signal anywhere" refusal would
			// be misleading, and there is no further source to fall back
			// to once an override is set.
			return createCapacityDenominator{}, false, fmt.Errorf(
				"storage.capacity_storage_id %q reports no live size on node %q", storageID, node)
		}
		return d, true, nil
	}

	zfspoolType := createZfspoolType
	storages, err := deps.API.ClusterStorage.ListStorage(ctx, &clusterstorage.ListStorageParams{Type: &zfspoolType})
	if err != nil {
		return createCapacityDenominator{}, false, err
	}
	var rootedID string
	for _, raw := range *storages {
		var entry createCapacityStorageEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return createCapacityDenominator{}, false, fmt.Errorf("decode storage list: %w", err)
		}
		if entry.Type != createZfspoolType || entry.Storage == "" || entry.Pool != basePool {
			continue
		}
		if !createStorageAvailableOnNode(entry.Nodes, node) {
			continue
		}
		if rootedID == "" || entry.Storage < rootedID {
			rootedID = entry.Storage
		}
	}
	if rootedID != "" {
		d, ok, statusErr := createReadStorageStatus(ctx, deps, node, rootedID, fmt.Sprintf("storage %q (pool %q)", rootedID, basePool))
		if statusErr != nil {
			return createCapacityDenominator{}, false, statusErr
		}
		if ok {
			return d, true, nil
		}
		// Registered and pool-rooted, but its status reports no usable
		// total (e.g. disabled): fall through to disks/zfs rather than
		// treating this as "no source at all" outright.
	}

	zfsPools, err := deps.API.Nodes.ListDisksZfs(ctx, node)
	if err != nil {
		return createCapacityDenominator{}, false, err
	}
	if zfsPools != nil {
		for _, raw := range *zfsPools {
			var entry createZfsPoolEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
				return createCapacityDenominator{}, false, fmt.Errorf("decode zfs pool list: %w", err)
			}
			if entry.Name != basePool || entry.Size.Int() <= 0 {
				continue
			}
			return createCapacityDenominator{
				totalBytes: entry.Size.Int(),
				usedBytes:  entry.Alloc.Int(),
				label:      fmt.Sprintf("zfs pool %q", basePool),
			}, true, nil
		}
	}

	return createCapacityDenominator{}, false, nil
}

// createCapacityGate is the sum-of-refquotas-vs-pool-size check `pmx lab
// create` runs before adding one more node/QDevice VM step to the plan
// (multi-node lab plan §3.4): it sums config.EffectiveRefquotaGB across
// every lab config.ResolveLabs resolves, substituting eff for the on-disk
// entry of the same name (see below), compares that sum against the live
// pool's total capacity — read via createResolveCapacityDenominator for the
// lab's base pool (zfsBasePool(eff), e.g. "tank") — and returns a non-empty
// warning string when the ratio exceeds createCapacityWarnRatio, or an
// error when it exceeds createCapacityRefuseRatio and force is false. When
// no live capacity source exists for the base pool at all, the gate refuses
// loudly (an operator-actionable error naming the base pool) rather than
// silently skipping, since a bare capacity gate with no live signal at all
// provides no protection. When resolving a source or reading its live size
// fails for another reason (a transient API/network error, a storage
// disabled/unavailable on this node, or a resolved storage reporting no
// usable size), the gate is skipped with an explanatory warning instead of
// blocking create outright, since that failure is not itself evidence the
// pool is full.
func createCapacityGate(ctx context.Context, deps *cli.Deps, eff *config.Lab, node string, force bool) (string, error) {
	labs, err := config.ResolveLabs(deps.Cfg, deps.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("capacity gate: resolve labs: %w", err)
	}

	// eff carries this invocation's flag overrides (--nodes,
	// --data-disk-gb, --os-disk-gb, etc.) on top of the on-disk config; the
	// entry ResolveLabs returned for the same name is still the pre-override
	// config. Substituting eff here means the gate reflects the lab actually
	// about to be created — a `--nodes 5` override against an on-disk
	// `nodes: 1` config must count at the 5-node reservation, not the
	// stale 1-node figure.
	labs[eff.Name] = eff

	var reservedGB int64
	for _, l := range labs {
		reservedGB += int64(config.EffectiveRefquotaGB(l))
	}

	nfsReservedGB := int64(config.EffectiveNFSReservedGB(deps.Cfg))

	basePool := zfsBasePool(eff)
	denom, found, resolveErr := createResolveCapacityDenominator(ctx, deps, node, basePool)
	if resolveErr != nil {
		return fmt.Sprintf(
			"capacity gate: could not read pool %q's live size on node %q (%v); skipping the pre-create capacity check",
			basePool, node, resolveErr), nil
	}
	if !found {
		return "", fmt.Errorf(
			"capacity gate: no live capacity signal found for base pool %q on node %q (no /cluster/storage "+
				"entry whose \"pool\" attribute is %q, and no %q entry in `zpool list`); register a storage "+
				"rooted at the pool (e.g. `pvesm add zfspool %s --pool %s`), or set storage.capacity_storage_id "+
				"in config to the storage ID the gate should read pool size from",
			basePool, node, basePool, basePool, basePool, basePool)
	}

	totalBytes := denom.totalBytes
	const bytesPerGB = int64(1024 * 1024 * 1024)

	// "Peppi actuals" (multi-node lab plan §3.4: "sum of all lab refquotas +
	// peppi actuals vs. pool size"): denom.usedBytes — the pool-wide
	// allocated/used figure createResolveCapacityDenominator's resolved
	// source reports (a pool-rooted storage's live "used", or a zpool's
	// "alloc") — is the only actual-usage signal available without an
	// SSH-based `zfs list` (out of scope for this API-only gate), so
	// peppi's actual usage cannot be cleanly isolated from every lab's own
	// actual usage, which it necessarily also includes. Rather than risk
	// under-counting (and letting the gate pass a pool that is actually
	// close to full), this deliberately takes the conservative, higher
	// estimate: the whole pool's live used bytes are added on top of
	// reservedGB's per-lab refquota sum, even though that double-counts a
	// lab whose actual usage is smaller than its refquota (once via its own
	// quota headroom, once via the pool-wide used figure). Over-estimating
	// the reservation trips the gate sooner rather than later — the safe
	// direction to err in a "before the pool actually fills" check. A
	// resolved source reporting no used figure at all degrades this term to
	// 0 rather than skipping the whole gate: unlike a missing/zero total,
	// which leaves no denominator to compute a ratio at all, a missing used
	// figure still leaves the refquota-sum and NFS-reserve terms meaningful
	// on their own.
	usedGB := denom.usedBytes / bytesPerGB

	reservedBytes := (reservedGB + usedGB + nfsReservedGB) * bytesPerGB
	ratio := float64(reservedBytes) / float64(totalBytes)

	switch {
	case ratio > createCapacityRefuseRatio && !force:
		return "", fmt.Errorf(
			"capacity gate: configured labs reserve %dG + pool actual usage %dG + NFS reserve %dG "+
				"against pool %q's %dG capacity (%.0f%%), over the %.0f%% refuse threshold; "+
				"pass --force to override",
			reservedGB, usedGB, nfsReservedGB, basePool, totalBytes/bytesPerGB, ratio*100, createCapacityRefuseRatio*100)
	case ratio > createCapacityWarnRatio:
		return fmt.Sprintf(
			"capacity gate WARNING: configured labs reserve %dG + pool actual usage %dG + NFS reserve %dG "+
				"against pool %q's %dG capacity (%.0f%%), over the %.0f%% warning threshold.",
			reservedGB, usedGB, nfsReservedGB, basePool, totalBytes/bytesPerGB, ratio*100, createCapacityWarnRatio*100), nil
	default:
		return "", nil
	}
}

// resourceNotFoundPattern matches PVE's "<kind> '<id>' does not exist" error
// text for one specific resource ID. Several PVE lookups (pool GET/DELETE in
// PVE::API2::Pool, storage DELETE via PVE::Storage::storage_config) signal a
// missing resource with a bare Perl `die "<kind> '$id' does not exist\n"`
// rather than raising a proper PVE::Exception with an HTTP code; the REST
// framework then surfaces that as a generic HTTP 500 whose body is just the
// die string, not the HTTP 404 pveerrors.ErrNotFound matches. Anchoring on
// both the resource kind and its exact ID (never a bare "does not exist"
// substring test) keeps an unrelated 500 — a permission denial, a locked
// config, a connection failure — from being misclassified as not-found.
func resourceNotFoundPattern(kind, id string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(kind) + ` ['"]?` + regexp.QuoteMeta(id) + `['"]? does not exist\b`)
}

// isResourceNotFound reports whether err represents kind/id not existing,
// whether PVE surfaced it as a genuine 404 (pveerrors.ErrNotFound) or — the
// shape actually observed against live PVE 9 for pool and storage lookups —
// an HTTP 500 APIError whose message or errors map names this exact
// resource as missing. See resourceNotFoundPattern for why the match is
// anchored to kind+id rather than a bare substring test.
func isResourceNotFound(err error, kind, id string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pveerrors.ErrNotFound) {
		return true
	}

	var apiErr *pveerrors.APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	pattern := resourceNotFoundPattern(kind, id)
	if pattern.MatchString(apiErr.Message) {
		return true
	}
	for _, msg := range apiErr.Errors {
		if pattern.MatchString(msg) {
			return true
		}
	}
	return false
}

// isPoolNotFound reports whether err represents poolID not existing yet, per
// isResourceNotFound.
func isPoolNotFound(err error, poolID string) bool {
	return isResourceNotFound(err, "pool", poolID)
}

// isStorageNotFound reports whether err represents storageID not existing
// yet, per isResourceNotFound.
func isStorageNotFound(err error, storageID string) bool {
	return isResourceNotFound(err, "storage", storageID)
}

// createPoolMembers returns every qemu member of poolID via GetPools
// filtered to type=qemu, tolerating more than one member (every multi-node
// lab's pool has one). found is an empty, nil-error slice both when the
// pool does not exist yet (PVE reports this as an HTTP 500 "pool '<id>' does
// not exist" body for GET /pools/{poolid}, not a 404 — see isPoolNotFound)
// and when it exists but has no qemu member.
func createPoolMembers(ctx context.Context, api *apiclient.APIClient, poolID string) ([]createPoolMember, error) {
	qemuType := "qemu"
	resp, err := api.Pools.GetPools(ctx, poolID, &pools.GetPoolsParams{Type: &qemuType})
	if err != nil {
		if isPoolNotFound(err, poolID) {
			return nil, nil
		}
		return nil, fmt.Errorf("look up VMs for pool %q: %w", poolID, err)
	}
	if resp == nil {
		return nil, nil
	}

	members := make([]createPoolMember, 0, len(resp.Members))
	for _, raw := range resp.Members {
		var m createPoolMember
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("decode member of pool %q: %w", poolID, err)
		}
		if m.Type != "qemu" || m.VMID.Int() == 0 {
			continue
		}
		members = append(members, m)
	}
	return members, nil
}

// createNextVMIDAllocator hands out a distinct VMID per call within one
// buildCreatePlan invocation (fixes M2-01: every node/QDevice target
// allocated the SAME VMID). GET /cluster/nextid is a pure function of live
// server state — the lowest currently-free VMID — and does not reserve the
// ID it returns. buildCreatePlan resolves every target's VMID during
// planning (before any target's VM actually exists, since planning never
// mutates anything), so calling ListNextid once per target, as the earlier
// implementation did, queried the SAME "lowest free VMID" answer every
// time and handed every target the identical ID; node 1's create would
// then fail "VM already exists" against node 0's already-created VM.
//
// This allocator instead queries ListNextid exactly once per buildCreatePlan
// run, then hands out baseline, baseline+1, baseline+2, ... for every
// subsequent call, so every target that needs a fresh VMID in one command
// invocation gets a distinct one even though none of them exist yet at
// planning time. This does not eliminate the inherent cross-process TOCTOU
// race every nextid-based allocation carries (PVE does not lock the ID it
// returns) — a concurrent `pmx lab create`/`qm create` elsewhere could still
// collide with one of these IDs before this command's execute phase
// reaches it, exactly as a single bare nextid call always could — it only
// fixes the guaranteed self-collision across this command's own targets.
type createNextVMIDAllocator struct {
	ac     *apiclient.APIClient
	next   int64
	primed bool
}

// allocate returns the next distinct VMID from a, querying
// GET /cluster/nextid on the first call and incrementing locally on every
// call after that.
func (a *createNextVMIDAllocator) allocate(ctx context.Context) (int64, error) {
	if !a.primed {
		nextRaw, nerr := a.ac.Cluster.ListNextid(ctx, &cluster.ListNextidParams{})
		if nerr != nil {
			return 0, fmt.Errorf("allocate next VMID: %w", nerr)
		}
		if nextRaw == nil {
			return 0, fmt.Errorf("allocate next VMID: empty response")
		}
		id, derr := createDecodeNextID(*nextRaw)
		if derr != nil {
			return 0, derr
		}
		a.next = id
		a.primed = true
	}

	id := a.next
	a.next++
	return id, nil
}

// createFindExistingTarget locates the already-existing VM (if any) for one
// create target — a specific node index or the QDevice — first by
// classifying every pool member's live name against the target's role
// (multi-VM-aware), then, when the pool itself does not exist yet or has no
// matching member (e.g. this lab's first-ever create run, before its pool
// has any member), falling back to a name match against qemus on node.
// Node index 0 additionally matches the legacy bare "lab-<labName>" name in
// the fallback, back-compat for a lab created before topology.nodes
// existed.
func createFindExistingTarget(
	members []createPoolMember, qemus *nodes.ListQemuResponse, node, labName, vmName string, isQdevice bool, index int,
) (vmid int64, vmNode string, found bool, err error) {
	for _, m := range members {
		mIsQ, mIdx, ok := classifyLabVMName(m.Name, labName)
		if !ok || mIsQ != isQdevice {
			continue
		}
		if !isQdevice && mIdx != index {
			continue
		}
		return m.VMID.Int(), m.Node, true, nil
	}

	if id, ok, ferr := createFindQemuByName(qemus, vmName); ferr != nil {
		return 0, "", false, ferr
	} else if ok {
		return id, node, true, nil
	}

	if !isQdevice && index == 0 {
		if id, ok, ferr := createFindQemuByName(qemus, legacyLabVMName(labName)); ferr != nil {
			return 0, "", false, ferr
		} else if ok {
			return id, node, true, nil
		}
	}

	return 0, "", false, nil
}

// createTargetSpec describes one `pmx lab create` target: a specific node
// index, or the QDevice.
type createTargetSpec struct {
	label     string // "0".."4" or "q"
	vmName    string
	isQdevice bool
	index     int // valid only when !isQdevice
	cloneFrom string
	compute   config.LabCompute
	storage   config.LabStorage
}

// createZfsDatasetProbeArgs builds the remote argv (after the ssh
// destination) that checks whether dataset already exists: "zfs list" of
// exactly that name, "-H" for script-friendly (unheadered, tab-separated)
// output. A nonzero exit means PVE will find no such dataset either.
func createZfsDatasetProbeArgs(dataset string) []string {
	return []string{"zfs", "list", "-H", "-o", "name", dataset}
}

// createZfsDatasetCreateArgs builds the remote argv (after the ssh
// destination) that creates dataset: "-p" creates any missing parent
// datasets (e.g. "tank/labs" ahead of "tank/labs/wayne"), matching the
// historical lab-provisioning script this step supersedes. refquotaGB is
// included as "-o refquota=<N>G" only when positive; a lab that leaves
// storage.refquota_gb unset gets a dataset with no refquota enforced at
// creation time (an operator can still set one later via `pmx lab quota
// set`).
func createZfsDatasetCreateArgs(dataset string, refquotaGB int) []string {
	args := []string{"zfs", "create", "-p"}
	if refquotaGB > 0 {
		args = append(args, "-o", fmt.Sprintf("refquota=%dG", refquotaGB))
	}
	return append(args, dataset)
}

// createDatasetSSHFlags resolves the ssh connection flags (user/port/
// identity) used to reach the dataset-ensure step's ssh target: the same
// root/22-default-with-context-override precedence runQuotaSet (quota.go)
// uses for its own "zfs set refquota" call — the operator's ssh key/user
// material lives on the pmx context (deps.Ctx.SSH), same as every other lab
// verb that shells out. Callers must check deps.Ctx != nil first; this
// function dereferences it unconditionally.
func createDatasetSSHFlags(deps *cli.Deps) sshcmd.Flags {
	f := sshcmd.Flags{User: "root", Port: 22}
	if deps.Ctx.SSH.User != "" {
		f.User = deps.Ctx.SSH.User
	}
	if deps.Ctx.SSH.Port != 0 {
		f.Port = deps.Ctx.SSH.Port
	}
	if deps.Ctx.SSH.Identity != "" {
		f.Identity = deps.Ctx.SSH.Identity
	}
	return f
}

// createDatasetSSHHost resolves the ssh destination HOST for the dataset-
// ensure step: the target PVE node's own address (via nodeaddr.Resolve
// against GET /cluster/status — the exact mechanism `pmx pve node ssh`
// uses, internal/cli/node/ssh.go's resolveHost), NOT deps.Ctx.Host.
//
// deps.Ctx.Host is the pmx CONTEXT's own configured host (quota.go's ssh
// target), which on a multi-node outer fleet cluster is only the cluster's
// API entrypoint — a different machine from the node a lab's VMs (and its
// ZFS pool) actually live on. A live run confirmed this the hard way:
// deps.Ctx.Host ("pve-0.taile80fe.ts.net") was not even ssh-reachable from
// the operator's machine, while the target node's own resolved address
// worked and returned a clean "dataset does not exist" (exit 1). node is
// buildCreatePlan's own node parameter — the same node every VM/QDevice
// target in this plan is created on — so the dataset is always probed and
// created on the machine that will actually host it.
func createDatasetSSHHost(ctx context.Context, ac *apiclient.APIClient, node string) (string, error) {
	host, err := nodeaddr.Resolve(ctx, ac.Cluster, node)
	if err != nil {
		return "", fmt.Errorf("resolve ssh address for node %q: %w", node, err)
	}
	return host, nil
}

// createDatasetSSHArgs builds the full ssh argv (connection flags +
// "user@host" destination + remoteCmd) for reaching host using f
// (createDatasetSSHFlags's result). The remote command, if any, is appended
// by the caller via remoteCmd.
func createDatasetSSHArgs(f sshcmd.Flags, host string, remoteCmd []string) []string {
	argv := sshcmd.BaseArgs(&f, host)
	return append(argv, remoteCmd...)
}

// sshTransportExitCode is the exit status ssh(1) itself uses when it could
// not establish or maintain the connection (refused, host unreachable, auth
// failure, ...) — distinct from a REMOTE command's own nonzero exit, which
// ssh passes through verbatim. "zfs list" against a missing dataset exits 1
// (a remote-command result); ssh exits 255 when it never got far enough to
// run any remote command at all. See ssh_config(5)/ssh(1): "ssh exits with
// the exit status of the remote command or with 255 if an error occurred."
const sshTransportExitCode = 255

// createZfsDatasetExists probes whether dataset already exists on host by
// running "zfs list -H -o name <dataset>" over ssh via deps.Runner — the
// same exec seam runQuotaSet uses, so tests substitute exec.Fake() the same
// way. The three outcomes are NOT collapsed into a single "nonzero exit ->
// absent" reading (the earlier revision of this step did that, and a live
// run showed why it is wrong: it silently misread an unreachable ssh target
// as "dataset absent" and proceeded to a "zfs create" that failed the exact
// same way, with no visible error):
//
//   - exit 0: dataset exists -> (true, nil).
//   - ssh's own transport exit code (255, sshTransportExitCode): ssh never
//     reached the remote shell at all (refused, unreachable, auth failure,
//     ...) -> (false, error) carrying dataset, ssh destination, exit code,
//     and stderr, so the caller aborts the whole plan instead of silently
//     treating a broken connection as "go ahead and create it".
//   - any other nonzero exit (e.g. zfs list's own exit 1 for "dataset does
//     not exist", or any other code returned by a remote command that DID
//     run): the remote host was reached and zfs itself reported absence ->
//     (false, nil).
//
// A process that could not even be started (no *exec.ExitError at all,
// e.g. the local ssh binary missing) is treated the same as a transport
// failure, not "absent" — exec.ExitCodeOf returns -1 for that case, folded
// into the same branch as the explicit 255 check below.
func createZfsDatasetExists(deps *cli.Deps, host, dataset string) (bool, error) {
	f := createDatasetSSHFlags(deps)
	argv := createDatasetSSHArgs(f, host, createZfsDatasetProbeArgs(dataset))

	var stdout, stderr bytes.Buffer
	err := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr)
	if err == nil {
		return true, nil
	}

	code := exec.ExitCodeOf(err)
	if code != sshTransportExitCode && code > 0 {
		// The remote host was reached and ran "zfs list"; its own nonzero
		// exit (1 for "no such dataset", possibly another code for a
		// different zfs-level failure) means "does not exist".
		return false, nil
	}

	wrapped := fmt.Errorf(
		"probe zfs dataset %q via ssh %s@%s: transport failure (exit %d): %w (stderr: %s)",
		dataset, f.User, host, code, err, strings.TrimSpace(stderr.String()))
	return false, exec.NewCapturedError(wrapped)
}

// createZfsDatasetEnsure runs "zfs create -p [-o refquota=<N>G] <dataset>"
// over ssh against host via deps.Runner. Called only when
// createZfsDatasetExists has already reported dataset absent (buildCreatePlan
// wires this as a createStep's apply, itself only reachable when the step
// was not skipped), so this never races an already-existing dataset under
// normal operation; "zfs create" without "-f" still fails loudly against an
// already-existing leaf dataset if that assumption is ever wrong (e.g. a
// concurrent creation from elsewhere), rather than silently doing nothing.
// On failure, the returned error carries the dataset path, ssh destination,
// exit code, and captured stderr — a live run against an unreachable host
// otherwise printed nothing at all, leaving the operator with no signal.
func createZfsDatasetEnsure(deps *cli.Deps, host, dataset string, refquotaGB int) error {
	f := createDatasetSSHFlags(deps)
	argv := createDatasetSSHArgs(f, host, createZfsDatasetCreateArgs(dataset, refquotaGB))

	var stdout, stderr bytes.Buffer
	if err := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr); err != nil {
		wrapped := fmt.Errorf(
			"create zfs dataset %q via ssh %s@%s (exit %d): %w (stderr: %s)",
			dataset, f.User, host, exec.ExitCodeOf(err), err, strings.TrimSpace(stderr.String()))
		return exec.NewCapturedError(wrapped)
	}
	return nil
}

// buildCreatePlan queries live PVE state for every resource `pmx lab create`
// composes (SDN zone/vnet/subnet, storage, pool, and one VM per configured
// node plus, when applicable, the QDevice) and returns the full ordered plan
// of steps needed to reach the desired state, marking any already-satisfied
// step as skipped, keeping create idempotent. No mutating API call is made
// by this function; it only performs GETs plus, for each not-yet-existing
// target, a GET /cluster/nextid VMID allocation when dryRun is false (also
// non-mutating, but still skipped in dry-run: a not-yet-created target's
// preview shows the "<vmid>" placeholder rather than reserving a real one it
// may never use).
//
// As soon as a target's VMID becomes known, whether from an already-existing
// VM found on node or from a freshly allocated next-id, it is peppi-guarded
// immediately, before any step in the returned plan is executed, so a
// protected VMID always aborts the whole command rather than only that
// target's step.
//
// Before any node/QDevice VM step is added, createCapacityGate runs; a
// refusal aborts here (returning an error) before any VMID is allocated for
// any target, so a lab over the pool-fill refuse threshold never even
// reserves VMIDs it will not use.
func buildCreatePlan(
	ctx context.Context, deps *cli.Deps, eff *config.Lab, node, cloneFrom, qdeviceCloneFrom string, start, dryRun, force bool,
) (*createPlan, error) {
	ac := deps.API
	plan := &createPlan{
		node: node, labName: eff.Name, storageID: storageID(eff),
	}

	zoneName := labZoneName(eff.Network)

	// 1. SDN zone (config-resolved, defaulting to "labs"/"simple" — decision
	// D4). The create spec (Peers/Nodes/MTU) is built by labZoneCreateParams
	// (net.go), the same helper `pmx lab net apply`'s ensureLabSdnZone uses,
	// so the two verbs can never provision the zone with diverging
	// parameters.
	zones, err := ac.Cluster.ListSdnZones(ctx, &cluster.ListSdnZonesParams{})
	if err != nil {
		return nil, fmt.Errorf("list SDN zones: %w", err)
	}
	existingZone, zoneExists, err := findSdnZone(*zones, zoneName)
	if err != nil {
		return nil, fmt.Errorf("decode SDN zone list: %w", err)
	}
	// zoneType is the zone's resolved plugin type: the live type when it
	// already exists, else labZoneType(eff.Network) — the type step 1 below
	// creates it as. tagAllowed (sdnZoneAllowsVnetTag, net.go) gates every
	// vnet-create/update Tag param this plan builds (steps 2 and 3b) the same
	// way ensureLabSdnVnets gates `pmx lab net apply`'s: a "simple"-type zone
	// rejects the tag parameter outright, so it must never be sent for any
	// vnet in that zone, primary or extra.
	zoneType := labZoneType(eff.Network)
	if zoneExists {
		zoneType = existingZone.Type
	}
	tagAllowed := sdnZoneAllowsVnetTag(zoneType)
	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("sdn zone %q (%s)", zoneName, labZoneType(eff.Network)),
		skip:       zoneExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			return ac.Cluster.CreateSdnZones(ctx, labZoneCreateParams(eff.Network, node, int64(eff.Network.MTU)))
		},
	})

	// 2. SDN vnet (per-lab). Existence is decided via findSdnVnet (net.go),
	// the same helper `pmx lab net apply` uses to reconcile the same vnet,
	// so the two verbs always agree on which live vnet corresponds to this
	// lab's vnet ID.
	vnets, err := ac.Cluster.ListSdnVnets(ctx, &cluster.ListSdnVnetsParams{})
	if err != nil {
		return nil, fmt.Errorf("list SDN vnets: %w", err)
	}
	_, vnetExists, err := findSdnVnet(*vnets, eff.Network.VnetID)
	if err != nil {
		return nil, fmt.Errorf("decode SDN vnet list: %w", err)
	}
	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("sdn vnet %q (zone %q, tag %d)", eff.Network.VnetID, zoneName, eff.Network.VxlanTag),
		skip:       vnetExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			params := &cluster.CreateSdnVnetsParams{
				Vnet: eff.Network.VnetID,
				Zone: zoneName,
			}
			if tagAllowed && eff.Network.VxlanTag != 0 {
				params.Tag = createPtr(int64(eff.Network.VxlanTag))
			}
			if eff.Network.VnetAlias != "" {
				params.Alias = createPtr(eff.Network.VnetAlias)
			}
			return ac.Cluster.CreateSdnVnets(ctx, params)
		},
	})

	// 3. SDN subnet (per-lab vnet's CIDR + mgmt gateway). Existence is
	// decided via findSdnSubnet (net.go), the same helper `pmx lab net
	// apply` uses to reconcile the same subnet: it matches on the Cidr
	// field, not the PVE-assigned Subnet identifier those two fields carry
	// separately (see sdnSubnetState), so create and net apply always agree
	// on which live subnet corresponds to this lab's CIDR.
	subnets, err := ac.Cluster.ListSdnVnetsSubnets(ctx, eff.Network.VnetID, &cluster.ListSdnVnetsSubnetsParams{})
	if err != nil {
		return nil, fmt.Errorf("list subnets of vnet %q: %w", eff.Network.VnetID, err)
	}
	_, subnetExists, err := findSdnSubnet(*subnets, eff.Network.CIDR)
	if err != nil {
		return nil, fmt.Errorf("decode subnet list for vnet %q: %w", eff.Network.VnetID, err)
	}
	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("sdn subnet %q on vnet %q", eff.Network.CIDR, eff.Network.VnetID),
		skip:       subnetExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			params := &cluster.CreateSdnVnetsSubnetsParams{Subnet: eff.Network.CIDR, Type: createSubnetType}
			if eff.Network.Mgmt.Gateway != "" {
				params.Gateway = createPtr(eff.Network.Mgmt.Gateway)
			}
			return ac.Cluster.CreateSdnVnetsSubnets(ctx, eff.Network.VnetID, params)
		},
	})

	// 3b. Additional outer SDN vnets/subnets (network.vnets[], multi-AZ
	// topology plan §1/§2): one step per entry, ensured via net.go's
	// vnet-agnostic ensureLabSdnVnetSubnet — the same helper `pmx lab net
	// apply` uses to reconcile the primary vnet, so the two verbs can never
	// provision an extra vnet with diverging Zone/Tag/Alias/CIDR/Gateway
	// values. A vnet with an empty CIDR (a pure L2 passthrough vnet, e.g. a
	// workload vnet) has no subnet sub-step: its step is skipped whenever
	// the vnet alone already exists, matching ensureLabSdnVnetSubnet's own
	// skip-if-cidr-empty rule. The vnets list already fetched for the
	// primary vnet's existence check (above) is reused here rather than
	// re-listing per entry; only the subnet lookup needs its own call, one
	// per vnet that declares a CIDR.
	for _, v := range eff.Network.Vnets {
		v := v
		_, extraVnetExists, verr := findSdnVnet(*vnets, v.ID)
		if verr != nil {
			return nil, fmt.Errorf("decode SDN vnet list: %w", verr)
		}
		extraSubnetExists := v.CIDR == ""
		if v.CIDR != "" {
			extraSubnets, serr := ac.Cluster.ListSdnVnetsSubnets(ctx, v.ID, &cluster.ListSdnVnetsSubnetsParams{})
			if serr != nil {
				return nil, fmt.Errorf("list subnets of vnet %q: %w", v.ID, serr)
			}
			_, extraSubnetExists, serr = findSdnSubnet(*extraSubnets, v.CIDR)
			if serr != nil {
				return nil, fmt.Errorf("decode subnet list for vnet %q: %w", v.ID, serr)
			}
		}

		desc := fmt.Sprintf("sdn vnet %q (zone %q, tag %d)", v.ID, zoneName, v.Tag)
		if v.CIDR != "" {
			desc = fmt.Sprintf("sdn vnet %q (zone %q, tag %d) + subnet %q", v.ID, zoneName, v.Tag, v.CIDR)
		}
		plan.steps = append(plan.steps, createStep{
			desc:       desc,
			skip:       extraVnetExists && extraSubnetExists,
			skipReason: "already exists",
			apply: func(ctx context.Context) error {
				return ensureLabSdnVnetSubnet(ctx, ac, zoneName, v.ID, v.Alias, v.Tag, v.CIDR, v.Gateway, tagAllowed)
			},
		})
	}

	// 4. ZFS dataset backing the storage step below. PVE's storage-create API
	// (step 5) accepts a "pool" dataset path that does not exist yet with no
	// complaint of its own — there is no PVE API for ZFS dataset operations at
	// all (quota.go's doc comment) — so without this step, qmcreate later
	// fails deep inside disk allocation with a raw "zfs error: cannot open
	// '<dataset>': dataset does not exist" the first time it tries to
	// allocate a disk on that storage. Every lab's storage is a zfspool
	// rooted at zfsBasePool(eff) (config.LabStorage has no other storage kind
	// today), so this step always applies. --dry-run never reaches ssh at
	// all here (unlike the PVE-API-backed existence checks above, which
	// --dry-run already depends on to render an accurate preview): a preview
	// should not carry a live ssh round trip as a hard dependency, so its
	// existence is reported as "verified at apply time" instead of probed.
	//
	// The ssh target is node's OWN resolved address (createDatasetSSHHost,
	// via nodeaddr.Resolve against GET /cluster/status — the exact mechanism
	// `pmx pve node ssh` uses), never deps.Ctx.Host: on a multi-node outer
	// fleet cluster, deps.Ctx.Host is only the cluster's API entrypoint,
	// which is not necessarily reachable at all and is not necessarily the
	// machine hosting this lab's ZFS pool — node is. The connection
	// credentials (user/port/identity) still come from deps.Ctx.SSH, same as
	// runQuotaSet (quota.go). Both the probe and the create call go through
	// deps.Runner — the same exec seam runQuotaSet uses, so tests substitute
	// exec.Fake() the same way. deps.Ctx or deps.Runner is nil only when a
	// caller builds *cli.Deps directly, bypassing PersistentPreRunE (e.g.
	// this package's own unit tests that never exercise ssh);
	// persistentPreRunE (root.go) always resolves deps.Ctx and sets
	// deps.Runner to exec.Real() before any lab verb's RunE runs, so that
	// branch is never taken on a real `pmx lab create`/`pmx lab scale`
	// invocation.
	//
	// A transport-level ssh failure (exit 255, or no process launched at
	// all) reaching the probe aborts the whole plan build with a loud error
	// naming the dataset, ssh destination, exit code, and stderr — it is
	// NEVER read as "dataset absent" (a live run against an unreachable ctx
	// host once did exactly that, silently proceeding to a "zfs create" that
	// failed the same way with no visible error at all). Only a REMOTE
	// command's own nonzero exit (the host was reached; zfs itself reported
	// no such dataset) is read as absent; see createZfsDatasetExists.
	dataset := zfsDatasetPath(eff)
	refquotaGB := eff.Storage.RefquotaGB
	datasetDesc := fmt.Sprintf("zfs dataset %q", dataset)
	var datasetStep createStep
	switch {
	case deps.Ctx == nil || deps.Runner == nil:
		datasetStep = createStep{
			desc: datasetDesc, skip: true,
			skipReason: "no active ssh context/runner to verify",
		}
	case dryRun:
		datasetStep = createStep{desc: datasetDesc + " (verified at apply time, not previewed)"}
	default:
		datasetHost, herr := createDatasetSSHHost(ctx, ac, node)
		if herr != nil {
			return nil, herr
		}
		exists, perr := createZfsDatasetExists(deps, datasetHost, dataset)
		if perr != nil {
			return nil, perr
		}
		if exists {
			datasetStep = createStep{desc: datasetDesc, skip: true, skipReason: "already exists"}
		} else {
			datasetStep = createStep{
				desc: datasetDesc,
				apply: func(context.Context) error {
					return createZfsDatasetEnsure(deps, datasetHost, dataset, refquotaGB)
				},
			}
		}
	}
	plan.steps = append(plan.steps, datasetStep)

	// 5. Storage (per-lab zfspool, shared by every node's disks).
	storages, err := ac.ClusterStorage.ListStorage(ctx, &clusterstorage.ListStorageParams{})
	if err != nil {
		return nil, fmt.Errorf("list storage: %w", err)
	}
	storageExists, err := createFindEntry(storages, func(e createStorageEntry) bool { return e.Storage == plan.storageID })
	if err != nil {
		return nil, fmt.Errorf("decode storage list: %w", err)
	}
	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("storage %q (zfspool %s)", plan.storageID, zfsDatasetPath(eff)),
		skip:       storageExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			params := &clusterstorage.CreateStorageParams{
				Storage: plan.storageID,
				Type:    "zfspool",
				Pool:    createPtr(zfsDatasetPath(eff)),
				Content: createPtr("images,rootdir"),
			}
			_, err := ac.ClusterStorage.CreateStorage(ctx, params)
			return err
		},
	})

	// 6. Resource pool. poolID falls back to "lab-<name>" when access.pool is
	// unset, the same labPoolID convention destroy/lifecycle/access grant use,
	// so a lab that omits access.pool resolves to the identical pool
	// everywhere rather than an empty pool here alone.
	poolID := labPoolID(eff)
	poolsResp, err := ac.Pools.ListPools(ctx, &pools.ListPoolsParams{})
	if err != nil {
		return nil, fmt.Errorf("list pools: %w", err)
	}
	poolExists, err := createFindEntry(poolsResp, func(e createPoolEntry) bool { return e.Poolid == poolID })
	if err != nil {
		return nil, fmt.Errorf("decode pool list: %w", err)
	}
	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("resource pool %q", poolID),
		skip:       poolExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			params := &pools.CreatePoolsParams{
				Poolid:  poolID,
				Comment: createPtr(fmt.Sprintf("Lab %q resource pool", eff.Name)),
			}
			return ac.Pools.CreatePools(ctx, params)
		},
	})

	// 7. Capacity gate: before reserving any VMID for any node/QDevice
	// target, check the aggregate lab refquota reservation against the
	// shared pool's live size (multi-node lab plan §3.4). A refusal aborts
	// here; a warning is carried through to the rendered summary.
	capNote, capErr := createCapacityGate(ctx, deps, eff, node, force)
	if capErr != nil {
		return nil, capErr
	}
	plan.capacityWarning = capNote

	// 8. One VM per configured node, plus the QDevice when the lab's
	// topology calls for one. Existing VMs are located once via pool
	// membership (multi-VM-aware) and once via a name-match fallback on
	// node, shared across every target's lookup.
	members, err := createPoolMembers(ctx, ac, poolID)
	if err != nil {
		return nil, err
	}
	qemus, err := ac.Nodes.ListQemu(ctx, node, &nodes.ListQemuParams{})
	if err != nil {
		return nil, fmt.Errorf("list VMs on node %q: %w", node, err)
	}

	numNodes := config.EffectiveTopologyNodes(eff.Topology)
	specs := make([]createTargetSpec, 0, numNodes+1)
	for i := 0; i < numNodes; i++ {
		compute, storage := config.EffectiveNodeSizing(eff, i)
		specs = append(specs, createTargetSpec{
			label:     strconv.Itoa(i),
			vmName:    labNodeVMName(eff.Name, i),
			isQdevice: false,
			index:     i,
			cloneFrom: cloneFrom,
			compute:   compute,
			storage:   storage,
		})
	}
	if config.QdeviceRequired(eff.Topology) {
		specs = append(specs, createTargetSpec{
			label:     "q",
			vmName:    labQdeviceVMName(eff.Name),
			isQdevice: true,
			cloneFrom: qdeviceCloneFrom,
		})
	}

	// One allocator per buildCreatePlan run, shared across every target: see
	// createNextVMIDAllocator's doc comment (M2-01) for why a fresh
	// ListNextid call per target would hand every target the same VMID.
	vmidAllocator := &createNextVMIDAllocator{ac: ac}

	for _, spec := range specs {
		np, err := planCreateTarget(ctx, deps, plan, eff, poolID, node, spec, members, qemus, vmidAllocator, start, dryRun)
		if err != nil {
			return nil, err
		}
		plan.nodePlans = append(plan.nodePlans, np)
	}

	return plan, nil
}

// planCreateTarget composes the plan step(s) for one create target — a
// specific node index or the QDevice — appending them to plan.steps in
// order: locate its existing VM (createFindExistingTarget), allocate a
// fresh, distinct VMID from vmidAllocator when none exists (skipped under
// dryRun, in which case the step renders the "<vmid>" placeholder;
// vmidAllocator — shared across every target in one buildCreatePlan run —
// is what keeps this VMID distinct from every other target's, see
// createNextVMIDAllocator), peppi-guard the resolved VMID immediately
// (before any step in the plan executes, matching buildCreatePlan's
// existing contract), guard a clone source VMID the same way, and append
// the VM-create step plus, when start is set, a start+agent-ping step
// targeting the VM's own live node (which may differ from node when an
// already-existing VM was found elsewhere via pool membership).
func planCreateTarget(
	ctx context.Context, deps *cli.Deps, plan *createPlan, eff *config.Lab, poolID, node string,
	spec createTargetSpec, members []createPoolMember, qemus *nodes.ListQemuResponse,
	vmidAllocator *createNextVMIDAllocator, start, dryRun bool,
) (createNodePlan, error) {
	ac := deps.API

	existingVMID, existingNode, vmExists, err := createFindExistingTarget(
		members, qemus, node, eff.Name, spec.vmName, spec.isQdevice, spec.index)
	if err != nil {
		return createNodePlan{}, fmt.Errorf("locate existing VM for %s: %w", spec.label, err)
	}

	vmNode := node
	if vmExists {
		vmNode = existingNode
	}

	var vmid int64
	vmidKnown := vmExists
	switch {
	case vmExists:
		vmid = existingVMID
	case dryRun:
		// Not yet created and previewing only: do not reserve a real VMID.
	default:
		vmid, err = vmidAllocator.allocate(ctx)
		if err != nil {
			return createNodePlan{}, fmt.Errorf("allocate next VMID for %s: %w", spec.label, err)
		}
		vmidKnown = true
	}

	// VMID 0 (unresolved, dry-run-only preview) never matches a protected
	// peppi VMID, so guarding unconditionally here is always safe.
	vmTarget := peppi.Target{
		VMID:  int(vmid),
		Names: []string{eff.Network.VnetID, poolID, plan.storageID, eff.DNS.Zone, eff.Name},
	}
	if err := peppi.Guard(vmTarget); err != nil {
		return createNodePlan{}, err
	}

	if spec.cloneFrom != "" {
		sourceVMID, cerr := strconv.ParseInt(spec.cloneFrom, 10, 64)
		if cerr != nil {
			return createNodePlan{}, fmt.Errorf(
				"invalid clone source %q for %s: expected a numeric VMID: %w", spec.cloneFrom, spec.label, cerr)
		}
		sourceTarget := peppi.Target{VMID: int(sourceVMID)}
		sourceName, sourceFound, nerr := createFindQemuNameByVMID(qemus, sourceVMID)
		if nerr != nil {
			return createNodePlan{}, fmt.Errorf("decode VM list on node %q: %w", node, nerr)
		}
		if sourceFound {
			sourceTarget.Names = []string{sourceName}
		}
		if err := peppi.Guard(sourceTarget); err != nil {
			return createNodePlan{}, fmt.Errorf("%s clone source: %w", spec.label, err)
		}
	}

	vmidStr := strconv.FormatInt(vmid, 10)
	vmidLabel := "<vmid>"
	if vmidKnown {
		vmidLabel = vmidStr
	}

	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("qemu VM %s (%s) on node %q", vmidLabel, spec.vmName, vmNode),
		skip:       vmExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			if spec.isQdevice {
				return createQdeviceVM(ctx, deps, eff, node, vmid, vmidStr, plan.storageID, spec.cloneFrom)
			}
			return createVM(ctx, deps, eff, spec.vmName, spec.compute, spec.storage, node, vmid, vmidStr, plan.storageID, spec.cloneFrom)
		},
	})

	// Idempotent NIC-reconciliation step: only meaningful for a target that
	// already exists (a not-yet-created VM's Net map, above, already carries
	// every configured HostNICs entry from the start) and only when the lab
	// actually configures HostNICs at all — the mechanism an
	// already-installed lab (e.g. pve-cpi's 3 az1 nodes) needs to pick up
	// newly-added net1..netN without a destroy/recreate (multi-AZ topology
	// plan §2/§3). Runs during planning (a non-mutating live-config read),
	// even under --dry-run, so the preview reflects real drift; only the
	// resulting UpdateQemuConfig call itself is deferred to apply time.
	if vmExists && len(eff.Network.HostNICs) > 0 {
		if err := planHostNICReconcileStep(ctx, deps, plan, eff, vmNode, vmid, vmidStr); err != nil {
			return createNodePlan{}, err
		}
	}

	// Optional start + guest-agent verification. The start targets vmNode,
	// not the --node flag value: an already-existing VM found via pool
	// membership may live on a different node than the one create was told
	// to provision on, and node-scoped qemu calls 404 against the wrong node.
	// For a VM this command creates, vmNode is node.
	if start {
		label := spec.label
		plan.steps = append(plan.steps, createStep{
			desc: fmt.Sprintf("start VM %s (%s) on node %q", vmidLabel, label, vmNode),
			apply: func(ctx context.Context) error {
				resp, serr := ac.Nodes.CreateQemuStatusStart(ctx, vmNode, vmidStr, &nodes.CreateQemuStatusStartParams{})
				if serr != nil {
					return fmt.Errorf("start VM %d (%s): %w", vmid, label, serr)
				}
				if resp != nil {
					if upid, uerr := apiclient.UPIDFromRaw(*resp); uerr == nil && upid != "" {
						if werr := apiclient.WaitTask(ctx, ac, upid, nil); werr != nil {
							return werr
						}
					}
				}
				if _, aerr := ac.Nodes.CreateQemuAgentPing(ctx, vmNode, vmidStr); aerr != nil {
					note := fmt.Sprintf(
						"guest agent did not respond after starting %s (expected if the OS has not been installed yet): %v",
						label, aerr)
					if plan.agentNote == "" {
						plan.agentNote = note
					} else {
						plan.agentNote = plan.agentNote + "; " + note
					}
				}
				return nil
			},
		})
	}

	return createNodePlan{label: spec.label, vmName: spec.vmName, vmid: vmid, vmidKnown: vmidKnown, node: vmNode}, nil
}

// createHostNICNetString renders one configured HostNICs entry as a PVE net
// device string, e.g. "virtio,bridge=pvecpist,mtu=1500": bridge is the
// entry's VnetID verbatim — an SDN vnet ID *is* its PVE bridge name, so no
// separate bridge-name resolution table is needed — and mtu falls back to
// defaultMTU (the lab's net0 MTU) when the entry itself leaves MTU unset,
// mirroring net0's own MTU rule.
func createHostNICNetString(nic config.LabHostNIC, defaultMTU int) string {
	s := fmt.Sprintf("virtio,bridge=%s", nic.VnetID)
	mtu := nic.MTU
	if mtu == 0 {
		mtu = defaultMTU
	}
	if mtu > 0 {
		s = fmt.Sprintf("%s,mtu=%d", s, mtu)
	}
	return s
}

// createExtraNetMap builds the net1..netN map[int]string entries a lab's
// network.host_nics declares, each rendered via createHostNICNetString.
// Returns nil (not an empty non-nil map) when net.HostNICs is empty, so a
// caller merging this into an existing {0: net0} map with a simple loop
// never iterates when there is nothing to add — today's single-NIC shape,
// unchanged.
func createExtraNetMap(net config.LabNetwork) map[int]string {
	if len(net.HostNICs) == 0 {
		return nil
	}
	m := make(map[int]string, len(net.HostNICs))
	for _, nic := range net.HostNICs {
		m[nic.Index] = createHostNICNetString(nic, net.MTU)
	}
	return m
}

// createVM creates or clones a node's VM once its VMID has been resolved and
// peppi-guarded, using the given vmName/compute/storage (config.EffectiveNodeSizing's
// per-node result — not necessarily eff.Compute/eff.Storage verbatim). When
// cloneFrom is empty a blank VM is created with the target's full compute/
// storage/network spec (matching Lab Host VM Spec); when cloneFrom names a
// source VMID, the VM is created via clone and the compute/network spec is
// then applied with a follow-up config update, since CreateQemuClone only
// carries identity/placement parameters.
func createVM(
	ctx context.Context, deps *cli.Deps, eff *config.Lab, vmName string, compute config.LabCompute, storage config.LabStorage,
	node string, vmid int64, vmidStr, stID, cloneFrom string,
) error {
	ac := deps.API
	net0 := fmt.Sprintf("virtio,bridge=%s", eff.Network.VnetID)
	if eff.Network.MTU > 0 {
		net0 = fmt.Sprintf("%s,mtu=%d", net0, eff.Network.MTU)
	}
	netMap := map[int]string{0: net0}
	for idx, s := range createExtraNetMap(eff.Network) {
		netMap[idx] = s
	}

	if cloneFrom == "" {
		params := &nodes.CreateQemuParams{
			Vmid:     vmid,
			Name:     createPtr(vmName),
			Pool:     createPtr(labPoolID(eff)),
			Cores:    createPtr(int64(compute.VCPU)),
			Sockets:  createPtr(int64(1)),
			Numa:     createPtr(compute.NUMA),
			Memory:   createPtr(strconv.Itoa(compute.Memory.MaxGB * 1024)),
			Balloon:  createPtr(int64(compute.Memory.MinGB * 1024)),
			Agent:    createPtr("enabled=1"),
			Ostype:   createPtr("l26"),
			Efidisk0: createPtr(fmt.Sprintf("%s:1,efitype=4m,pre-enrolled-keys=1", stID)),
			Scsi: map[int]string{
				0: fmt.Sprintf("%s:%d%s", stID, storage.OSDiskGB, createDiskOptions(storage)),
				1: fmt.Sprintf("%s:%d%s", stID, storage.DataDiskGB, createDiskOptions(storage)),
			},
			Net: netMap,
		}
		if compute.CPUType != "" {
			params.Cpu = createPtr(compute.CPUType)
		}
		if compute.Machine != "" {
			params.Machine = createPtr(compute.Machine)
		}
		if compute.Firmware != "" {
			params.Bios = createPtr(compute.Firmware)
		}
		if storage.Controller != "" {
			params.Scsihw = createPtr(storage.Controller)
		}

		resp, err := ac.Nodes.CreateQemu(ctx, node, params)
		if err != nil {
			return fmt.Errorf("create VM %d on node %q: %w", vmid, node, err)
		}
		if resp == nil {
			return fmt.Errorf("create VM %d on node %q: empty response", vmid, node)
		}
		upid, err := apiclient.UPIDFromRaw(*resp)
		if err != nil {
			return fmt.Errorf("create VM %d on node %q: %w", vmid, node, err)
		}
		return apiclient.WaitTask(ctx, ac, upid, nil)
	}

	sourceVMID, err := strconv.ParseInt(cloneFrom, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid clone source %q: expected a numeric VMID: %w", cloneFrom, err)
	}

	cloneParams := &nodes.CreateQemuCloneParams{
		Newid:   vmid,
		Name:    createPtr(vmName),
		Pool:    createPtr(labPoolID(eff)),
		Storage: createPtr(stID),
		Full:    createPtr(true),
	}
	resp, err := ac.Nodes.CreateQemuClone(ctx, node, strconv.FormatInt(sourceVMID, 10), cloneParams)
	if err != nil {
		return fmt.Errorf("clone VM %d from %d on node %q: %w", vmid, sourceVMID, node, err)
	}
	if resp != nil {
		if upid, uerr := apiclient.UPIDFromRaw(*resp); uerr == nil && upid != "" {
			if werr := apiclient.WaitTask(ctx, ac, upid, nil); werr != nil {
				return werr
			}
		}
	}

	updateParams := &nodes.UpdateQemuConfigParams{
		Cores:   createPtr(int64(compute.VCPU)),
		Numa:    createPtr(compute.NUMA),
		Memory:  createPtr(strconv.Itoa(compute.Memory.MaxGB * 1024)),
		Balloon: createPtr(int64(compute.Memory.MinGB * 1024)),
	}
	if compute.CPUType != "" {
		updateParams.Cpu = createPtr(compute.CPUType)
	}
	if compute.Machine != "" {
		updateParams.Machine = createPtr(compute.Machine)
	}
	if compute.Firmware != "" {
		updateParams.Bios = createPtr(compute.Firmware)
	}
	if storage.Controller != "" {
		updateParams.Scsihw = createPtr(storage.Controller)
	}
	updateParams.Net = netMap

	if err := ac.Nodes.UpdateQemuConfig(ctx, node, vmidStr, updateParams); err != nil {
		return fmt.Errorf("update cloned VM %d config on node %q: %w", vmid, node, err)
	}
	return nil
}

// createQdeviceVM creates or clones a lab's QDevice tie-breaker VM once its
// VMID has been resolved and peppi-guarded, at the fixed tiny spec
// (qdeviceVCPU/qdeviceMemoryGB/qdeviceDiskGB — multi-node lab plan §4.3),
// never the lab's own node compute/storage sizing. Mirrors createVM's
// create-vs-clone branching, but the QDevice is deliberately never resized
// by lab config or CLI flags: it exists only to cast one corosync
// tie-breaker vote and needs no meaningful compute. A blank (non-clone)
// QDevice VM has no OS installed — §6.4 documents provisioning it from the
// shared tmpl-qdevice template via --qdevice-clone-from as the intended
// path; blank creation is supported for completeness (e.g. a caller that
// installs the OS out-of-band) but is not the documented production path.
func createQdeviceVM(
	ctx context.Context, deps *cli.Deps, eff *config.Lab, node string, vmid int64, vmidStr, stID, cloneFrom string,
) error {
	ac := deps.API
	net0 := fmt.Sprintf("virtio,bridge=%s", eff.Network.VnetID)
	if eff.Network.MTU > 0 {
		net0 = fmt.Sprintf("%s,mtu=%d", net0, eff.Network.MTU)
	}
	netMap := map[int]string{0: net0}
	for idx, s := range createExtraNetMap(eff.Network) {
		netMap[idx] = s
	}
	memKiB := strconv.Itoa(qdeviceMemoryGB * 1024)
	vmName := labQdeviceVMName(eff.Name)

	if cloneFrom == "" {
		params := &nodes.CreateQemuParams{
			Vmid:    vmid,
			Name:    createPtr(vmName),
			Pool:    createPtr(labPoolID(eff)),
			Cores:   createPtr(int64(qdeviceVCPU)),
			Sockets: createPtr(int64(1)),
			Memory:  createPtr(memKiB),
			Balloon: createPtr(int64(qdeviceMemoryGB * 1024)),
			Agent:   createPtr("enabled=1"),
			Ostype:  createPtr("l26"),
			Scsi: map[int]string{
				0: fmt.Sprintf("%s:%d", stID, qdeviceDiskGB),
			},
			Net: netMap,
		}

		resp, err := ac.Nodes.CreateQemu(ctx, node, params)
		if err != nil {
			return fmt.Errorf("create QDevice VM %d on node %q: %w", vmid, node, err)
		}
		if resp == nil {
			return fmt.Errorf("create QDevice VM %d on node %q: empty response", vmid, node)
		}
		upid, err := apiclient.UPIDFromRaw(*resp)
		if err != nil {
			return fmt.Errorf("create QDevice VM %d on node %q: %w", vmid, node, err)
		}
		return apiclient.WaitTask(ctx, ac, upid, nil)
	}

	sourceVMID, err := strconv.ParseInt(cloneFrom, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid --qdevice-clone-from %q: expected a numeric VMID: %w", cloneFrom, err)
	}

	cloneParams := &nodes.CreateQemuCloneParams{
		Newid:   vmid,
		Name:    createPtr(vmName),
		Pool:    createPtr(labPoolID(eff)),
		Storage: createPtr(stID),
		Full:    createPtr(true),
	}
	resp, err := ac.Nodes.CreateQemuClone(ctx, node, strconv.FormatInt(sourceVMID, 10), cloneParams)
	if err != nil {
		return fmt.Errorf("clone QDevice VM %d from %d on node %q: %w", vmid, sourceVMID, node, err)
	}
	if resp != nil {
		if upid, uerr := apiclient.UPIDFromRaw(*resp); uerr == nil && upid != "" {
			if werr := apiclient.WaitTask(ctx, ac, upid, nil); werr != nil {
				return werr
			}
		}
	}

	updateParams := &nodes.UpdateQemuConfigParams{
		Cores:   createPtr(int64(qdeviceVCPU)),
		Memory:  createPtr(memKiB),
		Balloon: createPtr(int64(qdeviceMemoryGB * 1024)),
		Net:     netMap,
	}
	if err := ac.Nodes.UpdateQemuConfig(ctx, node, vmidStr, updateParams); err != nil {
		return fmt.Errorf("update cloned QDevice VM %d config on node %q: %w", vmid, node, err)
	}
	return nil
}

// createLiveNetConfig fetches vmidStr's current config on node and returns
// the live "netN" strings for exactly the given indices. The raw endpoint is
// read directly (deps.API.Raw.GetCtx) instead of through
// nodes.ListQemuConfig/ListQemuConfigResponse because that generated struct
// cannot represent PVE's dynamically-indexed net0/net1/... keys at all — its
// field for the whole family is a literal placeholder (Netn, tagged
// json:"net[n]") that never matches a real response key such as "net1"; see
// internal/cli/qemu/config.go's newConfigGetCmd doc comment for the same
// documented caveat. An index with no corresponding live key (never
// configured) is simply absent from the returned map, not an error — that
// is exactly the "missing, needs to be added" case
// planHostNICReconcileStep's caller must detect.
func createLiveNetConfig(ctx context.Context, deps *cli.Deps, node, vmidStr string, indices []int) (map[int]string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%s/config", url.PathEscape(node), url.PathEscape(vmidStr))
	data, err := deps.API.Raw.GetCtx(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get live config for VM %s on node %q: %w", vmidStr, node, err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("get live config for VM %s on node %q: unexpected response shape %T", vmidStr, node, data)
	}

	live := make(map[int]string, len(indices))
	for _, idx := range indices {
		key := fmt.Sprintf("net%d", idx)
		raw, present := m[key]
		if !present {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("get live config for VM %s on node %q: %s is not a string (%T)", vmidStr, node, key, raw)
		}
		live[idx] = s
	}
	return live, nil
}

// createNetConfigMatches reports whether a live PVE net device string (e.g.
// "virtio=BC:24:11:AA:BB:CC,bridge=vmbr1,mtu=1500") already matches the
// bridge and MTU this CLI would configure for a HostNICs entry. The
// comparison is deliberately narrower than a full string equality check:
// PVE always injects and persists a MAC address into the model=MAC
// component this CLI never sends on create (net0's own params never set a
// MAC either), so a live value can never equal the CLI's own
// "virtio,bridge=...[,mtu=...]" string byte-for-byte even once fully
// converged — comparing only the two fields this CLI actually manages is
// the only way to detect true convergence rather than perpetual "drift".
// mtu of 0 means "no MTU asserted" on both sides (net.MTU unset and no
// mtu= component in live), matching createHostNICNetString's own
// zero-means-omit convention.
func createNetConfigMatches(live, bridge string, mtu int) bool {
	var gotBridge string
	var gotMTU int
	for _, part := range strings.Split(live, ",") {
		switch {
		case strings.HasPrefix(part, "bridge="):
			gotBridge = strings.TrimPrefix(part, "bridge=")
		case strings.HasPrefix(part, "mtu="):
			if v, err := strconv.Atoi(strings.TrimPrefix(part, "mtu=")); err == nil {
				gotMTU = v
			}
		}
	}
	return gotBridge == bridge && gotMTU == mtu
}

// createHostNICDrift returns the map[int]string of net device strings (one
// per createHostNICNetString-rendered value) for exactly the
// net.HostNICs entries that are missing from live (never configured) or
// present but not matching the configured bridge/MTU (createNetConfigMatches
// returns false). An empty (nil) result means every configured HostNICs
// entry already matches the VM's live config: fully converged, nothing to
// reconcile.
func createHostNICDrift(net config.LabNetwork, live map[int]string) map[int]string {
	var diff map[int]string
	for _, nic := range net.HostNICs {
		desiredMTU := nic.MTU
		if desiredMTU == 0 {
			desiredMTU = net.MTU
		}
		liveVal, ok := live[nic.Index]
		if ok && createNetConfigMatches(liveVal, nic.VnetID, desiredMTU) {
			continue
		}
		if diff == nil {
			diff = make(map[int]string, len(net.HostNICs))
		}
		diff[nic.Index] = createHostNICNetString(nic, net.MTU)
	}
	return diff
}

// planHostNICReconcileStep appends, for an already-existing target whose lab
// configures Network.HostNICs, an idempotent NIC-reconciliation createStep:
// it reads the VM's live net1..netN config (createLiveNetConfig, at planning
// time — a non-mutating read, run even under --dry-run so the preview
// reflects real drift), diffs it against the configured HostNICs
// (createHostNICDrift), and — only when at least one entry is missing or
// drifted — appends exactly one step whose apply issues exactly one
// UpdateQemuConfig call carrying every drifted/missing index at once. This
// is the mechanism an already-installed lab (e.g. pve-cpi's 3 az1 nodes)
// needs to pick up newly-added HostNICs entries without a destroy/recreate
// (multi-AZ topology plan §2/§3). A fully-converged VM (every configured
// index already matches) appends no step at all, keeping create's STEP
// table free of no-op rows for the common re-run case. Callers must only
// invoke this when the target's VM already exists (vmExists) and
// eff.Network.HostNICs is non-empty — a not-yet-created target's Net map,
// built by createVM/createQdeviceVM, already carries every configured
// HostNICs entry from the start.
func planHostNICReconcileStep(
	ctx context.Context, deps *cli.Deps, plan *createPlan, eff *config.Lab, vmNode string, vmid int64, vmidStr string,
) error {
	indices := make([]int, 0, len(eff.Network.HostNICs))
	for _, nic := range eff.Network.HostNICs {
		indices = append(indices, nic.Index)
	}

	live, err := createLiveNetConfig(ctx, deps, vmNode, vmidStr, indices)
	if err != nil {
		return fmt.Errorf("read live NIC config for VM %d on node %q: %w", vmid, vmNode, err)
	}

	diff := createHostNICDrift(eff.Network, live)
	if len(diff) == 0 {
		return nil
	}

	diffIdx := make([]int, 0, len(diff))
	for idx := range diff {
		diffIdx = append(diffIdx, idx)
	}
	sort.Ints(diffIdx)
	diffLabels := make([]string, 0, len(diffIdx))
	for _, idx := range diffIdx {
		diffLabels = append(diffLabels, fmt.Sprintf("net%d", idx))
	}

	ac := deps.API
	plan.steps = append(plan.steps, createStep{
		desc: fmt.Sprintf("reconcile host NICs %s on VM %d (node %q)", strings.Join(diffLabels, ","), vmid, vmNode),
		apply: func(ctx context.Context) error {
			if err := ac.Nodes.UpdateQemuConfig(ctx, vmNode, vmidStr, &nodes.UpdateQemuConfigParams{Net: diff}); err != nil {
				return fmt.Errorf("reconcile host NICs on VM %d (node %q): %w", vmid, vmNode, err)
			}
			return nil
		},
	})
	return nil
}

// createFindEntry decodes each element of a []json.RawMessage-shaped list
// response into T and reports whether any decoded element satisfies match.
// resp may be nil (an empty PVE list), in which case it reports false.
func createFindEntry[T any](resp any, match func(T) bool) (bool, error) {
	raws, err := createRawList(resp)
	if err != nil {
		return false, err
	}
	for _, raw := range raws {
		var e T
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, err
		}
		if match(e) {
			return true, nil
		}
	}
	return false, nil
}

// createFindQemuByName decodes a nodes.ListQemuResponse and reports the VMID
// of the first entry whose name equals name, if any.
func createFindQemuByName(resp *nodes.ListQemuResponse, name string) (int64, bool, error) {
	if resp == nil {
		return 0, false, nil
	}
	for _, raw := range *resp {
		var e createQemuEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return 0, false, err
		}
		if e.Name == name {
			return e.VMID, true, nil
		}
	}
	return 0, false, nil
}

// createFindQemuNameByVMID decodes a nodes.ListQemuResponse and reports the
// name of the first entry whose VMID equals vmid, if any. Used to guard a
// --clone-from/--qdevice-clone-from source by name as well as VMID: a
// production VM's name might match a protected pattern even when its VMID
// alone does not.
func createFindQemuNameByVMID(resp *nodes.ListQemuResponse, vmid int64) (string, bool, error) {
	if resp == nil {
		return "", false, nil
	}
	for _, raw := range *resp {
		var e createQemuEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return "", false, err
		}
		if e.VMID == vmid {
			return e.Name, true, nil
		}
	}
	return "", false, nil
}

// createRawList normalizes the several list response pointer-to-named-slice
// types (e.g. *cluster.ListSdnZonesResponse, *clusterstorage.ListStorageResponse)
// used across this file into a plain []json.RawMessage, or nil for a nil
// response. It uses a JSON round-trip so it works uniformly across every
// distinct named slice type without one accessor function per type.
func createRawList(resp any) ([]json.RawMessage, error) {
	if resp == nil {
		return nil, nil
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var out []json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// runCreateContextHook registers or refreshes the lab-<name> pmx context after
// a successful create, and returns the STEP-table note describing the outcome.
// It is strictly best-effort: any failure yields a warning note pointing at
// `pmx lab context sync` and never affects create's exit code. When --start
// was not given the VMs are powered off, so it emits guidance instead of
// polling a dead node; --no-context suppresses it entirely.
func runCreateContextHook(
	cmd *cobra.Command, deps *cli.Deps, lab *config.Lab, name string, start, noContext bool,
) string {
	if noContext {
		return ""
	}
	ctxName := labContextName(name)
	if !start {
		return fmt.Sprintf("skipped (no --start); run 'pmx lab context sync %s' after 'pmx lab start %s'", name, name)
	}

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: true})
	if err != nil {
		return fmt.Sprintf("⚠ context %s: %v; run 'pmx lab context sync %s' to retry", ctxName, err, name)
	}
	verb := "reused existing token"
	if res.Rotated {
		verb = "rotated token"
	}
	return fmt.Sprintf("context %s ready (%s)", ctxName, verb)
}

// renderCreatePlan renders plan as a STEP/STATUS table. In dry-run mode every
// step shows either "would create" or "skip (already exists)", and the VM
// (and start) steps carry the literal "<vmid>" placeholder baked into their
// desc by buildCreatePlan when a target's VM does not yet exist. In
// non-dry-run mode every non-skipped step has already been applied by the
// caller and rows show "created"/"skip (already exists)".
//
// A trailing "summary" row lists every target's resolved VMID (label=VM
// id), and a "capacity gate"/"start" row is appended when the capacity
// gate's warning or a post-start agent note, respectively, is non-empty.
// These are rendered as table rows rather than via output.Result.Message:
// every renderer in internal/output (table, plain, JSON, YAML) drops
// Message whenever Headers/Rows are also set, which create's STEP table
// always is, so a Message-only warning here would never reach the operator
// in any output format.
func renderCreatePlan(cmd *cobra.Command, deps *cli.Deps, plan *createPlan, dryRun bool) error {
	headers := []string{"STEP", "STATUS"}
	rows := make([][]string, 0, len(plan.steps)+3)
	for _, step := range plan.steps {
		status := "created"
		if dryRun {
			status = "would create"
		}
		if step.skip {
			status = fmt.Sprintf("skip (%s)", step.skipReason)
		}
		rows = append(rows, []string{step.desc, status})
	}

	var summary string
	switch {
	case dryRun:
		summary = fmt.Sprintf("create plan for lab %q on node %q", plan.labName, plan.node)
	default:
		parts := make([]string, 0, len(plan.nodePlans))
		for _, np := range plan.nodePlans {
			if np.vmidKnown {
				parts = append(parts, fmt.Sprintf("%s=VM %d", np.label, np.vmid))
			}
		}
		if len(parts) > 0 {
			summary = fmt.Sprintf("lab %q created on node %q: %s", plan.labName, plan.node, strings.Join(parts, ", "))
		} else {
			summary = fmt.Sprintf("lab %q created on node %q", plan.labName, plan.node)
		}
	}
	rows = append(rows, []string{"summary", summary})

	if plan.capacityWarning != "" {
		rows = append(rows, []string{"capacity gate", plan.capacityWarning})
	}
	if plan.agentNote != "" {
		rows = append(rows, []string{"start", plan.agentNote})
	}
	if plan.contextNote != "" {
		rows = append(rows, []string{"context", plan.contextNote})
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}
