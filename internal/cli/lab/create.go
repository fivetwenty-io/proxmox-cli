package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
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
}

// createPtr returns a pointer to v, for building the many optional pointer
// fields the generated API client params types expose.
func createPtr[T any](v T) *T { return &v }

// newCreateCmd builds `pmx lab create <name>`.
func newCreateCmd() *cobra.Command {
	var (
		dryRun bool
		force  bool
		node   string
		start  bool
		ov     createOverrides
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

			return renderCreatePlan(cmd, deps, plan, false)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "preview the ordered plan without mutating anything")
	f.BoolVar(&force, "force", false, "override the capacity gate's 85% pool-fill refusal threshold")
	f.StringVar(&node, "node", "", "node to create the lab's VMs on (defaults to --node/PMX_NODE/config default)")
	f.BoolVar(&start, "start", false, "start every created VM after creation and verify the guest agent responds")
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
	_, zoneExists, err := findSdnZone(*zones, zoneName)
	if err != nil {
		return nil, fmt.Errorf("decode SDN zone list: %w", err)
	}
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
				Tag:  createPtr(int64(eff.Network.VxlanTag)),
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

	// 4. Storage (per-lab zfspool, shared by every node's disks).
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

	// 5. Resource pool. poolID falls back to "lab-<name>" when access.pool is
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

	// 6. Capacity gate: before reserving any VMID for any node/QDevice
	// target, check the aggregate lab refquota reservation against the
	// shared pool's live size (multi-node lab plan §3.4). A refusal aborts
	// here; a warning is carried through to the rendered summary.
	capNote, capErr := createCapacityGate(ctx, deps, eff, node, force)
	if capErr != nil {
		return nil, capErr
	}
	plan.capacityWarning = capNote

	// 7. One VM per configured node, plus the QDevice when the lab's
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
			Net: map[int]string{0: net0},
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
	updateParams.Net = map[int]string{0: net0}

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
			Net: map[int]string{0: net0},
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
		Net:     map[int]string{0: net0},
	}
	if err := ac.Nodes.UpdateQemuConfig(ctx, node, vmidStr, updateParams); err != nil {
		return fmt.Errorf("update cloned QDevice VM %d config on node %q: %w", vmid, node, err)
	}
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

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
}
