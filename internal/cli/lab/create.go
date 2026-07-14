package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/peppi"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/clusterstorage"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/pools"
)

// createSubnetType is the fixed "type" value the PVE API requires when
// creating an SDN subnet.
const createSubnetType = "subnet"

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

// createOverrides holds the parsed --vcpu/--memory-*/--data-disk-gb/--os-disk-gb/
// --vxlan-tag/--cidr/--pool/--clone-from flag values for `pmx lab create`. Values
// are only applied to the effective lab copy when cmd.Flags().Changed reports
// the corresponding flag was actually passed (flag-over-config precedence).
type createOverrides struct {
	vcpu       int
	memMaxGB   int
	memMinGB   int
	dataDiskGB int
	osDiskGB   int
	vxlanTag   int
	cidr       string
	pool       string
	cloneFrom  string
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

// createPlan is the fully-resolved, ordered set of operations `pmx lab create`
// will perform (or preview). vmid is 0 only when the lab's VM does not yet
// exist and creation was not actually planned (--dry-run against a lab with no
// prior next-id allocation), in which case the preview renders the "<vmid>"
// placeholder.
type createPlan struct {
	steps     []createStep
	labName   string
	vmid      int64
	vmidKnown bool
	node      string
	vmName    string
	storageID string
	agentNote string
}

// createPtr returns a pointer to v, for building the many optional pointer
// fields the generated API client params types expose.
func createPtr[T any](v T) *T { return &v }

// newCreateCmd builds `pmx lab create <name>`.
func newCreateCmd() *cobra.Command {
	var (
		dryRun bool
		node   string
		start  bool
		ov     createOverrides
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a lab's SDN network, storage, and VM",
		Long: "Create a lab: idempotently ensures the shared labsvxlan SDN zone, the lab's own " +
			"vnet and subnet, its derived lab storage (tank-lab-wayne for lab wayne), and its resource pool all exist, then " +
			"creates the lab's VM (or clones it from an existing VM given --clone-from) and applies " +
			"the resolved compute spec. Every step queries live state first and skips anything " +
			"already satisfied, so re-running create against a partially-built lab is safe.\n\n" +
			"This does not run `pmx lab net apply`: the vnet/subnet definitions are staged, not yet " +
			"live, until that command (or `pmx pve sdn apply`) commits them. --clone-from assumes " +
			"the source VM lives on the same node as the new lab VM; the platform is single-node " +
			"today, so this always holds.\n\n" +
			"Every other lab verb (destroy, start, stop, list, status) locates the lab's VM by its " +
			"membership in the effective resource pool, not by name: a --pool override here must " +
			"match the lab's configured access.pool, or the config must be updated to match, or " +
			"those verbs will report no VM found even though create succeeded.",
		Example: `  pmx lab create wayne --node sm-0
  pmx lab create wayne --node sm-0 --start
  pmx lab create wayne --node sm-0 --dry-run
  pmx lab create wayne --node sm-0 --vcpu 24 --memory-max-gb 128`,
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

			plan, err := buildCreatePlan(cmd.Context(), deps, eff, targetNode, ov.cloneFrom, start, dryRun)
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
	f.StringVar(&node, "node", "", "node to create the lab's VM on (defaults to --node/PMX_NODE/config default)")
	f.BoolVar(&start, "start", false, "start the VM after creation and verify the guest agent responds")
	f.IntVar(&ov.vcpu, "vcpu", 0, "override compute.vcpu")
	f.IntVar(&ov.memMaxGB, "memory-max-gb", 0, "override compute.memory.max_gb")
	f.IntVar(&ov.memMinGB, "memory-min-gb", 0, "override compute.memory.min_gb")
	f.IntVar(&ov.dataDiskGB, "data-disk-gb", 0, "override storage.data_disk_gb")
	f.IntVar(&ov.osDiskGB, "os-disk-gb", 0, "override storage.os_disk_gb")
	f.IntVar(&ov.vxlanTag, "vxlan-tag", 0, "override network.vxlan_tag")
	f.StringVar(&ov.cidr, "cidr", "", "override network.cidr")
	f.StringVar(&ov.pool, "pool", "",
		"override access.pool (destroy/start/stop/list/status locate the VM by resource-pool "+
			"membership; a --pool override must match the lab's configured pool, or those verbs "+
			"will not find the VM until the config is updated to match)")
	f.StringVar(&ov.cloneFrom, "clone-from", "", "VMID of an existing VM to clone the lab's VM from, instead of creating blank disks")

	return cmd
}

// applyCreateOverrides returns a copy of lab with every config-override flag
// that was actually passed on the command line applied on top of it. lab
// itself is never mutated: config.Lab's nested fields are all plain structs
// (not pointers), so the top-level copy also copies every nested section by
// value.
func applyCreateOverrides(fl interface{ Changed(string) bool }, lab *config.Lab, ov createOverrides) *config.Lab {
	eff := *lab

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

// buildCreatePlan queries live PVE state for every resource `pmx lab create`
// composes (SDN zone/vnet/subnet, storage, pool, VM) and returns the full
// ordered plan of steps needed to reach the desired state, marking any
// already-satisfied step as skipped, keeping create idempotent. No mutating API call is made by
// this function; it only performs GETs plus, if the lab's VM does not yet
// exist AND dryRun is false, a GET /cluster/nextid VMID allocation (also
// non-mutating, but still skipped in dry-run: a not-yet-existing
// lab's preview shows the "<vmid>" placeholder rather than reserving a real
// one it may never use).
//
// As soon as the lab's VMID becomes known, whether from an already-existing
// VM found on node or from a freshly allocated next-id, it is peppi-guarded
// immediately, before any step in the returned plan is executed, so a
// protected VMID always aborts the whole command rather than only the VM
// step.
func buildCreatePlan(
	ctx context.Context, deps *cli.Deps, eff *config.Lab, node, cloneFrom string, start, dryRun bool,
) (*createPlan, error) {
	ac := deps.API
	plan := &createPlan{
		node: node, labName: eff.Name, vmName: fmt.Sprintf("lab-%s", eff.Name), storageID: storageID(eff),
	}

	// 1. SDN zone (shared, "labsvxlan"). The create spec (Peers/Nodes/MTU) is
	// built by labZoneCreateParams (net.go), the same helper `pmx lab net
	// apply`'s ensureLabSdnZone uses, so the two verbs can never provision the
	// zone with diverging parameters.
	zones, err := ac.Cluster.ListSdnZones(ctx, &cluster.ListSdnZonesParams{})
	if err != nil {
		return nil, fmt.Errorf("list SDN zones: %w", err)
	}
	_, zoneExists, err := findSdnZone(*zones, labZoneName)
	if err != nil {
		return nil, fmt.Errorf("decode SDN zone list: %w", err)
	}
	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("sdn zone %q (%s)", labZoneName, labZoneType),
		skip:       zoneExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			return ac.Cluster.CreateSdnZones(ctx, labZoneCreateParams(node, int64(eff.Network.MTU)))
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
		desc:       fmt.Sprintf("sdn vnet %q (zone %q, tag %d)", eff.Network.VnetID, labZoneName, eff.Network.VxlanTag),
		skip:       vnetExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			params := &cluster.CreateSdnVnetsParams{
				Vnet: eff.Network.VnetID,
				Zone: labZoneName,
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

	// 4. Storage (per-lab zfspool).
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

	// 6. VM: find an existing lab VM primarily by membership in poolID, the
	// same resource-pool join key destroy/start/stop/list/status use (see
	// destroyLocateVM), falling back to a name+node match when the pool
	// does not exist yet or has no qemu member — e.g. this lab's first-ever
	// create run, before its pool has any member. Either way, guard the
	// concrete VMID before returning the plan, since every step above is
	// still unexecuted.
	qemus, err := ac.Nodes.ListQemu(ctx, node, &nodes.ListQemuParams{})
	if err != nil {
		return nil, fmt.Errorf("list VMs on node %q: %w", node, err)
	}

	poolVMID, poolVMNode, poolVMFound, err := destroyLocateVM(ctx, ac, poolID)
	if err != nil {
		return nil, fmt.Errorf("locate existing VM in pool %q: %w", poolID, err)
	}

	var existingVMID int64
	vmExists := poolVMFound
	vmNode := node
	switch {
	case poolVMFound:
		existingVMID = int64(poolVMID)
		vmNode = poolVMNode
	default:
		existingVMID, vmExists, err = createFindQemuByName(qemus, plan.vmName)
		if err != nil {
			return nil, fmt.Errorf("decode VM list on node %q: %w", node, err)
		}
	}

	var vmid int64
	vmidKnown := vmExists
	switch {
	case vmExists:
		vmid = existingVMID
	case dryRun:
		// Not yet created and previewing only: do not reserve a real VMID
		// via GET /cluster/nextid; the plan renders the "<vmid>" placeholder
		// for this step instead.
	default:
		nextRaw, nerr := ac.Cluster.ListNextid(ctx, &cluster.ListNextidParams{})
		if nerr != nil {
			return nil, fmt.Errorf("allocate next VMID: %w", nerr)
		}
		if nextRaw == nil {
			return nil, fmt.Errorf("allocate next VMID: empty response")
		}
		vmid, err = createDecodeNextID(*nextRaw)
		if err != nil {
			return nil, err
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
		return nil, err
	}

	// --clone-from reads (never mutates) its source VM, but a protected
	// production VMID must never seed a lab clone even so; guard the source
	// VMID here, before any step's apply runs, exactly like the target VMID
	// above. The source's own name (from the same qemus list, since
	// --clone-from's source is documented to live on the same node) is
	// guarded alongside its VMID, so a production VM whose name matches a
	// protected pattern is refused even when its VMID alone is not one of
	// the protected IDs.
	if cloneFrom != "" {
		sourceVMID, cerr := strconv.ParseInt(cloneFrom, 10, 64)
		if cerr != nil {
			return nil, fmt.Errorf("invalid --clone-from %q: expected a numeric VMID: %w", cloneFrom, cerr)
		}
		sourceTarget := peppi.Target{VMID: int(sourceVMID)}
		sourceName, sourceFound, nerr := createFindQemuNameByVMID(qemus, sourceVMID)
		if nerr != nil {
			return nil, fmt.Errorf("decode VM list on node %q: %w", node, nerr)
		}
		if sourceFound {
			sourceTarget.Names = []string{sourceName}
		}
		if err := peppi.Guard(sourceTarget); err != nil {
			return nil, fmt.Errorf("clone source: %w", err)
		}
	}

	plan.vmid = vmid
	plan.vmidKnown = vmidKnown
	vmidStr := strconv.FormatInt(vmid, 10)
	vmidLabel := "<vmid>"
	if vmidKnown {
		vmidLabel = vmidStr
	}

	plan.steps = append(plan.steps, createStep{
		desc:       fmt.Sprintf("qemu VM %s (%s) on node %q", vmidLabel, plan.vmName, vmNode),
		skip:       vmExists,
		skipReason: "already exists",
		apply: func(ctx context.Context) error {
			return createVM(ctx, deps, eff, node, vmid, vmidStr, plan.storageID, cloneFrom)
		},
	})

	// 7. Optional start + guest-agent verification. The start targets vmNode,
	// not the --node flag value: an already-existing VM found via pool
	// membership may live on a different node than the one create was told
	// to provision on, and node-scoped qemu calls 404 against the wrong node.
	// For a VM this command creates, vmNode is node.
	if start {
		plan.steps = append(plan.steps, createStep{
			desc: fmt.Sprintf("start VM %s on node %q", vmidLabel, vmNode),
			apply: func(ctx context.Context) error {
				resp, serr := ac.Nodes.CreateQemuStatusStart(ctx, vmNode, vmidStr, &nodes.CreateQemuStatusStartParams{})
				if serr != nil {
					return fmt.Errorf("start VM %d: %w", vmid, serr)
				}
				if resp != nil {
					if upid, uerr := apiclient.UPIDFromRaw(*resp); uerr == nil && upid != "" {
						if werr := apiclient.WaitTask(ctx, ac, upid, nil); werr != nil {
							return werr
						}
					}
				}
				if _, aerr := ac.Nodes.CreateQemuAgentPing(ctx, vmNode, vmidStr); aerr != nil {
					plan.agentNote = fmt.Sprintf(
						"guest agent did not respond after start (expected if the OS has not been installed yet): %v", aerr)
				}
				return nil
			},
		})
	}

	return plan, nil
}

// createVM creates or clones a lab's VM once its VMID has been resolved and
// peppi-guarded. When cloneFrom is empty a blank VM is created with the
// lab's full compute/storage/network spec (matching Lab Host VM Spec); when
// cloneFrom names a source VMID, the VM is created via clone and the
// compute/network spec is then applied with a follow-up config update, since
// CreateQemuClone only carries identity/placement parameters.
func createVM(
	ctx context.Context, deps *cli.Deps, eff *config.Lab, node string, vmid int64, vmidStr, stID, cloneFrom string,
) error {
	ac := deps.API
	net0 := fmt.Sprintf("virtio,bridge=%s", eff.Network.VnetID)
	if eff.Network.MTU > 0 {
		net0 = fmt.Sprintf("%s,mtu=%d", net0, eff.Network.MTU)
	}

	if cloneFrom == "" {
		params := &nodes.CreateQemuParams{
			Vmid:     vmid,
			Name:     createPtr(fmt.Sprintf("lab-%s", eff.Name)),
			Pool:     createPtr(labPoolID(eff)),
			Cores:    createPtr(int64(eff.Compute.VCPU)),
			Sockets:  createPtr(int64(1)),
			Numa:     createPtr(eff.Compute.NUMA),
			Memory:   createPtr(strconv.Itoa(eff.Compute.Memory.MaxGB * 1024)),
			Balloon:  createPtr(int64(eff.Compute.Memory.MinGB * 1024)),
			Agent:    createPtr("enabled=1"),
			Ostype:   createPtr("l26"),
			Efidisk0: createPtr(fmt.Sprintf("%s:1,efitype=4m,pre-enrolled-keys=1", stID)),
			Scsi: map[int]string{
				0: fmt.Sprintf("%s:%d%s", stID, eff.Storage.OSDiskGB, createDiskOptions(eff.Storage)),
				1: fmt.Sprintf("%s:%d%s", stID, eff.Storage.DataDiskGB, createDiskOptions(eff.Storage)),
			},
			Net: map[int]string{0: net0},
		}
		if eff.Compute.CPUType != "" {
			params.Cpu = createPtr(eff.Compute.CPUType)
		}
		if eff.Compute.Machine != "" {
			params.Machine = createPtr(eff.Compute.Machine)
		}
		if eff.Compute.Firmware != "" {
			params.Bios = createPtr(eff.Compute.Firmware)
		}
		if eff.Storage.Controller != "" {
			params.Scsihw = createPtr(eff.Storage.Controller)
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
		return fmt.Errorf("invalid --clone-from %q: expected a numeric VMID: %w", cloneFrom, err)
	}

	cloneParams := &nodes.CreateQemuCloneParams{
		Newid:   vmid,
		Name:    createPtr(fmt.Sprintf("lab-%s", eff.Name)),
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
		Cores:   createPtr(int64(eff.Compute.VCPU)),
		Numa:    createPtr(eff.Compute.NUMA),
		Memory:  createPtr(strconv.Itoa(eff.Compute.Memory.MaxGB * 1024)),
		Balloon: createPtr(int64(eff.Compute.Memory.MinGB * 1024)),
	}
	if eff.Compute.CPUType != "" {
		updateParams.Cpu = createPtr(eff.Compute.CPUType)
	}
	if eff.Compute.Machine != "" {
		updateParams.Machine = createPtr(eff.Compute.Machine)
	}
	if eff.Compute.Firmware != "" {
		updateParams.Bios = createPtr(eff.Compute.Firmware)
	}
	if eff.Storage.Controller != "" {
		updateParams.Scsihw = createPtr(eff.Storage.Controller)
	}
	updateParams.Net = map[int]string{0: net0}

	if err := ac.Nodes.UpdateQemuConfig(ctx, node, vmidStr, updateParams); err != nil {
		return fmt.Errorf("update cloned VM %d config on node %q: %w", vmid, node, err)
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
// --clone-from source by name as well as VMID: a production VM's name might
// match a protected pattern even when its VMID does not.
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
// desc by buildCreatePlan when the lab's VM does not yet exist. In
// non-dry-run mode every non-skipped step has already been applied by the
// caller and rows show "created"/"skip (already exists)".
func renderCreatePlan(cmd *cobra.Command, deps *cli.Deps, plan *createPlan, dryRun bool) error {
	headers := []string{"STEP", "STATUS"}
	rows := make([][]string, 0, len(plan.steps))
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

	var msg string
	switch {
	case dryRun:
		msg = fmt.Sprintf("Lab %q create plan on node %q.", plan.labName, plan.node)
	case plan.vmidKnown:
		msg = fmt.Sprintf("Lab %q created on node %q (VM %d).", plan.labName, plan.node, plan.vmid)
	default:
		msg = fmt.Sprintf("Lab %q created on node %q.", plan.labName, plan.node)
	}
	if plan.agentNote != "" {
		msg = msg + " " + plan.agentNote
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows, Message: msg}, deps.Format)
}
