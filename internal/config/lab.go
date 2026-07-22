package config

import (
	"fmt"
	"strings"
)

// Lab describes one nested (or, in a future mode, hardware) lab environment:
// its network, compute, storage, DNS, provisioning, and access settings.
type Lab struct {
	// Name is the lab's display name. Defaults to its map key in Config.Labs.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Mode selects how the lab is realized: "nested" (VM-in-VM, v1 only) or
	// "hardware" (bare metal, reserved for future use).
	Mode string `yaml:"mode" json:"mode"`

	// Owner is the pve user this lab is assigned to ("user@realm"); "~" or ""
	// means no owner.
	Owner string `yaml:"owner" json:"owner"`

	// Network holds the SDN zone/vnet/subnet configuration for the lab.
	Network LabNetwork `yaml:"network" json:"network"`

	// Compute holds the CPU and memory configuration for the lab's VM (node 0
	// when Topology.Nodes > 1, and the base sizing every other node falls
	// back to before its own topology.node_overrides entry, if any, is
	// layered on top — see EffectiveNodeSizing).
	Compute LabCompute `yaml:"compute" json:"compute"`

	// Storage holds the ZFS pool and disk sizing for the lab. RefquotaGB is
	// the lab-wide dataset quota (shared by every node's disks); OSDiskGB/
	// DataDiskGB are the base per-node disk sizing EffectiveNodeSizing
	// starts from before per-node overrides.
	Storage LabStorage `yaml:"storage" json:"storage"`

	// DNS holds the DNS zone configuration for the lab.
	DNS LabDNS `yaml:"dns" json:"dns"`

	// Provisioning holds the guest provisioning configuration for the lab.
	Provisioning LabProvisioning `yaml:"provisioning" json:"provisioning"`

	// Access holds the pve realm/pool/role granted to the lab owner.
	Access LabAccess `yaml:"access" json:"access"`

	// Topology describes the lab's node count and QDevice policy. The zero
	// value means a single node with no QDevice, today's shape — see
	// EffectiveTopologyNodes and EffectiveTopologyQdevice for the defaulting
	// rules every caller must use instead of reading the fields directly.
	Topology LabTopology `yaml:"topology,omitempty" json:"topology,omitempty"`
}

// LabNetwork describes the SDN vnet, VXLAN tag, and subnet layout for a lab.
type LabNetwork struct {
	// VnetID is the SDN vnet identifier: at most 8 alphanumeric characters,
	// no hyphen (enforced by validation, not by this type).
	VnetID string `yaml:"vnet_id" json:"vnet_id"`

	// VnetAlias is a human-readable label for the vnet.
	VnetAlias string `yaml:"vnet_alias" json:"vnet_alias"`

	// VxlanTag is the VXLAN tag assigned to the vnet.
	VxlanTag int `yaml:"vxlan_tag" json:"vxlan_tag"`

	// CIDR is the overall subnet range allocated to the lab.
	CIDR string `yaml:"cidr" json:"cidr"`

	// Mgmt holds the management subnet, host IP, and gateway for the lab.
	Mgmt LabMgmt `yaml:"mgmt" json:"mgmt"`

	// BoshBloc is the subnet range reserved for BOSH-deployed VMs in the lab.
	BoshBloc string `yaml:"bosh_bloc" json:"bosh_bloc"`

	// MTU is the maximum transmission unit for the vnet.
	MTU int `yaml:"mtu" json:"mtu"`

	// ZoneName is the SDN zone this lab's vnet lives in. Empty defaults to
	// "labs" (EffectiveZoneName) — the deployed outer Simple zone (decision
	// D4 of the multi-node lab plan) — rather than the platform's historical
	// hardcoded "labsvxlan" VXLAN zone.
	ZoneName string `yaml:"zone_name,omitempty" json:"zone_name,omitempty"`

	// ZoneType is the SDN zone plugin type for ZoneName ("simple" or
	// "vxlan"). Empty defaults to "simple" (EffectiveZoneType), matching
	// ZoneName's "labs" default.
	ZoneType string `yaml:"zone_type,omitempty" json:"zone_type,omitempty"`

	// ZonePeers is the VXLAN zone's underlay peer list. Only meaningful when
	// ZoneType (effective) is "vxlan"; ignored for "simple", which has no
	// peers concept.
	ZonePeers string `yaml:"zone_peers,omitempty" json:"zone_peers,omitempty"`

	// Vnets holds additional outer SDN vnets beyond the primary VnetID/CIDR
	// pair, e.g. separate storage and workload L2 domains for a multi-bond
	// nested topology. Empty means today's single-vnet lab shape, unchanged.
	// The primary vnet is always net0 from HostNICs' point of view and is
	// never repeated here.
	Vnets []LabVnet `yaml:"vnets,omitempty" json:"vnets,omitempty"`

	// HostNICs describes additional outer qm NICs (net1..netN) beyond the
	// always-present net0, which always attaches to VnetID exactly as today
	// (createVM/createQdeviceVM). Empty means today's single-NIC shape.
	// Every entry's VnetID must resolve to either the primary VnetID or one
	// of Vnets[].ID — checked by labNetworkPlanIssues (internal/cli/lab).
	HostNICs []LabHostNIC `yaml:"host_nics,omitempty" json:"host_nics,omitempty"`

	// NestedNetwork describes in-guest bonding/bridging and an inner SDN
	// vlan-type zone for the lab's own nested PVE node OS — distinct from
	// the always-present inner VXLAN zone sdninner.go manages for cross-node
	// BOSH/CF L2 (`pmx lab sdn apply`, unchanged). Zero value means no
	// bonding: nested nodes use their NICs unbonded, today's shape.
	NestedNetwork LabNestedNetwork `yaml:"nested_network,omitempty" json:"nested_network,omitempty"`
}

// LabVnet describes one additional outer SDN vnet, beyond the primary
// LabNetwork.VnetID/CIDR pair, that a lab's nodes attach a NIC to.
type LabVnet struct {
	// ID is the SDN vnet identifier: at most MaxVnetIDLen alphanumeric
	// characters, no hyphen — same constraint as LabNetwork.VnetID
	// (enforced by validation, not by this type).
	ID string `yaml:"id" json:"id"`

	// Alias is a human-readable label for the vnet.
	Alias string `yaml:"alias,omitempty" json:"alias,omitempty"`

	// Tag is the vnet's VLAN/VXLAN tag within LabNetwork.EffectiveZoneName()'s
	// zone.
	Tag int `yaml:"tag" json:"tag"`

	// CIDR is the subnet ensured for this vnet. Empty means no subnet is
	// ensured — a pure L2 passthrough vnet.
	CIDR string `yaml:"cidr,omitempty" json:"cidr,omitempty"`

	// Gateway is the vnet's subnet gateway address, when CIDR is set.
	Gateway string `yaml:"gateway,omitempty" json:"gateway,omitempty"`

	// Purpose is a free-form label, e.g. "storage" or "workload". Never read
	// by reconciliation; documentation only.
	Purpose string `yaml:"purpose,omitempty" json:"purpose,omitempty"`
}

// LabHostNIC describes one additional outer qm NIC (netN, N>=1), beyond the
// always-present net0.
type LabHostNIC struct {
	// Index is the qm netN index, >=1 (net0 is reserved for the primary
	// vnet and must not be repeated here).
	Index int `yaml:"index" json:"index"`

	// VnetID is the target vnet: the lab's primary LabNetwork.VnetID, or one
	// of LabNetwork.Vnets[].ID.
	VnetID string `yaml:"vnet_id" json:"vnet_id"`

	// MTU is this NIC's maximum transmission unit. Zero means the same
	// default net0 already uses (LabNetwork.MTU).
	MTU int `yaml:"mtu,omitempty" json:"mtu,omitempty"`
}

// LabNestedNetwork describes a lab's nested PVE node's own in-guest
// bonding/bridging and inner SDN vlan zone. Zero value: no bonding — today's
// shape, unbonded NICs.
type LabNestedNetwork struct {
	// Bonds lists the guest-OS bonds (and the bridge on top of each) to
	// configure inside the lab's nested PVE nodes.
	Bonds []LabNestedBond `yaml:"bonds,omitempty" json:"bonds,omitempty"`

	// VlanZone describes an inner Proxmox SDN "vlan"-type zone layered on
	// one of the bonds' bridges above. Nil means no inner vlan zone.
	VlanZone *LabNestedVlanZone `yaml:"vlan_zone,omitempty" json:"vlan_zone,omitempty"`
}

// Valid LabNestedBond.Mode values. D-04: active-backup is the only mode
// that actually aggregates when nested — LACPDUs (01:80:C2:00:00:02) sit in
// the Linux bridge's reserved multicast range and are never forwarded, so
// nested 802.3ad can never negotiate. 802.3ad may still be written for
// syntax parity/future bare-metal reuse; ValidateNestedNetwork flags it as a
// warning, not a hard error.
const (
	NestedBondModeActiveBackup = "active-backup"
	NestedBondMode8023ad       = "802.3ad"
)

// LabNestedBond describes one guest-OS bond and the bridge on top of it,
// inside a lab's nested PVE node (D-04: active-backup is the only mode that
// actually aggregates nested; other modes may be written for syntax
// coverage but are non-functional — see ValidateNestedNetwork).
type LabNestedBond struct {
	// Name is the guest-OS bond interface name, e.g. "bond0".
	Name string `yaml:"name" json:"name"`

	// NICs lists the guest-visible interface names bonded together, e.g.
	// ["nic0","nic1"].
	NICs []string `yaml:"nics" json:"nics"`

	// Mode is the bond mode: NestedBondModeActiveBackup (functional) or
	// NestedBondMode8023ad (syntax-only, D-04). No other value is valid —
	// see ValidateNestedNetwork.
	Mode string `yaml:"mode" json:"mode"`

	// Primary is the bond's primary/preferred slave interface name, when
	// Mode is active-backup. Empty means no explicit primary.
	Primary string `yaml:"primary,omitempty" json:"primary,omitempty"`

	// Bridge is the guest-OS bridge built on top of this bond, e.g.
	// "vmbr0".
	Bridge string `yaml:"bridge" json:"bridge"`

	// VlanAware marks Bridge as VLAN-aware, allowing a LabNestedVlanZone to
	// reference it.
	VlanAware bool `yaml:"vlan_aware,omitempty" json:"vlan_aware,omitempty"`
}

// LabNestedVlanZone describes an inner Proxmox SDN "vlan"-type zone on one
// of a lab's nested node's own bridges (typically the workload bridge).
type LabNestedVlanZone struct {
	// Bridge must match a LabNestedBond.Bridge entry with VlanAware true.
	Bridge string `yaml:"bridge" json:"bridge"`

	// ZoneName is the inner SDN zone identifier: at most MaxVnetIDLen
	// alphanumeric characters, no hyphen.
	ZoneName string `yaml:"zone_name" json:"zone_name"`

	// Vnets lists the zone's client-VLAN vnets.
	Vnets []LabNestedVlanVnet `yaml:"vnets" json:"vnets"`
}

// LabNestedVlanVnet describes one vnet of a lab's inner SDN vlan zone: one
// 802.1Q tag, one subnet.
type LabNestedVlanVnet struct {
	// ID is the inner vnet identifier: at most MaxVnetIDLen alphanumeric
	// characters, no hyphen.
	ID string `yaml:"id" json:"id"`

	// Alias is a human-readable label for the vnet.
	Alias string `yaml:"alias,omitempty" json:"alias,omitempty"`

	// Tag is the 802.1Q VLAN ID carried on this vnet — a client-VLAN
	// number, an entirely separate numbering space from the outer SDN
	// zone's VxlanTag/LabVnet.Tag values.
	Tag int `yaml:"tag" json:"tag"`

	// CIDR is the subnet ensured for this vnet.
	CIDR string `yaml:"cidr" json:"cidr"`

	// Gateway is the vnet's subnet gateway address.
	Gateway string `yaml:"gateway,omitempty" json:"gateway,omitempty"`
}

// EffectiveHostNICs returns n.HostNICs verbatim (nil-safe passthrough; kept
// for symmetry with the other Effective* accessors and as the one place a
// future default could land without touching call sites).
func (n LabNetwork) EffectiveHostNICs() []LabHostNIC {
	return n.HostNICs
}

// nestedNetworkWarningPrefix marks a ValidateNestedNetwork issue string as
// warning-class rather than a hard error: an operator-visible caution
// (currently only D-04's 802.3ad non-functionality note) that a caller may
// choose to surface without refusing the config, unlike every other issue
// string this function returns. Mirrors the CLI's existing "warning: "
// convention (e.g. internal/cli/context/warnings.go).
const nestedNetworkWarningPrefix = "warning: "

// ValidateNestedNetwork checks nn for internal coherence and returns one
// message per problem found, or nil when nn is valid. name is the lab's
// name, used to prefix every message, mirroring ValidateTopology's per-lab
// issue-list convention. A zero-value nn (no Bonds, no VlanZone) always
// returns nil — that is today's unbonded-NIC shape, so labs written before
// this field existed keep validating cleanly.
//
// Four checks, matching the multi-node lab plan §1:
//  1. A bond's Mode outside {NestedBondModeActiveBackup, NestedBondMode8023ad}
//     is a hard error.
//  2. A NestedBondMode8023ad bond is flagged with a nestedNetworkWarningPrefix
//     issue string noting D-04 non-functionality — not a hard error, since an
//     operator may legitimately author it for later real-hardware parity.
//  3. A NestedNetwork.VlanZone.Bridge with no matching Bonds[].Bridge entry
//     that also has VlanAware true is a hard error (the vlan zone would have
//     nothing to attach to).
//  4. A bond whose NICs has fewer than 2 entries is a hard error (nothing to
//     bond).
//
// Broader cross-referencing against the lab's outer Network.Vnets/HostNICs
// (e.g. HostNICs[].VnetID resolution, Vnets[].ID charset/uniqueness) is out
// of this function's scope by design — it lives in
// internal/cli/lab/netplan.go's labNestedNetworkPlanIssues wrapper, which
// has access to the full LabNetwork, not just NestedNetwork.
func ValidateNestedNetwork(name string, nn LabNestedNetwork) []string {
	var issues []string

	// bridgesVlanAware tracks every VlanAware bridge declared by a bond, so
	// VlanZone's cross-reference check below can look it up in one pass.
	bridgesVlanAware := make(map[string]bool, len(nn.Bonds))

	for i, b := range nn.Bonds {
		if b.VlanAware && b.Bridge != "" {
			bridgesVlanAware[b.Bridge] = true
		}

		switch b.Mode {
		case NestedBondModeActiveBackup:
			// functional when nested — no issue.
		case NestedBondMode8023ad:
			issues = append(issues, fmt.Sprintf(
				"%slab %q: nested_network.bonds[%d] (%q) mode %q is non-functional when nested (D-04): "+
					"LACPDUs (01:80:C2:00:00:02) sit in the outer Linux bridge's reserved multicast range "+
					"and are never forwarded, so this bond can never aggregate; kept for syntax parity/"+
					"future bare-metal reuse only, not an error",
				nestedNetworkWarningPrefix, name, i, b.Name, b.Mode))
		default:
			issues = append(issues, fmt.Sprintf(
				"lab %q: nested_network.bonds[%d] (%q) mode %q must be %q or %q",
				name, i, b.Name, b.Mode, NestedBondModeActiveBackup, NestedBondMode8023ad))
		}

		if len(b.NICs) < 2 {
			issues = append(issues, fmt.Sprintf(
				"lab %q: nested_network.bonds[%d] (%q) has %d nics, need at least 2 to bond",
				name, i, b.Name, len(b.NICs)))
		}
	}

	if nn.VlanZone != nil && !bridgesVlanAware[nn.VlanZone.Bridge] {
		issues = append(issues, fmt.Sprintf(
			"lab %q: nested_network.vlan_zone.bridge %q has no matching nested_network.bonds[] entry "+
				"with that bridge and vlan_aware: true",
			name, nn.VlanZone.Bridge))
	}

	return issues
}

// Defaults for LabNetwork's SDN zone fields, applied whenever the
// corresponding config field is empty: the deployed outer Simple zone
// "labs" (decision D4 of the multi-node lab plan), superseding the
// platform's historical hardcoded "labsvxlan" VXLAN zone constants.
const (
	DefaultZoneName = "labs"
	DefaultZoneType = "simple"
)

// EffectiveZoneName returns n.ZoneName, defaulting to DefaultZoneName when
// unset.
func (n LabNetwork) EffectiveZoneName() string {
	if n.ZoneName != "" {
		return n.ZoneName
	}
	return DefaultZoneName
}

// EffectiveZoneType returns n.ZoneType, defaulting to DefaultZoneType when
// unset.
func (n LabNetwork) EffectiveZoneType() string {
	if n.ZoneType != "" {
		return n.ZoneType
	}
	return DefaultZoneType
}

// EffectiveZonePeers returns n.ZonePeers when the effective zone type is
// "vxlan" (the only zone type with a peers concept), else "": a "simple"
// zone's Peers field must never be sent or compared, since PVE's Simple zone
// plugin has no such field.
func (n LabNetwork) EffectiveZonePeers() string {
	if n.EffectiveZoneType() != "vxlan" {
		return ""
	}
	return n.ZonePeers
}

// LabMgmt describes the lab's management subnet.
type LabMgmt struct {
	// Subnet is the management subnet CIDR: an address-plan reservation
	// within LabNetwork.CIDR marking which slice is set aside for
	// management-plane hosts. It is NOT an interface prefix: the lab host's
	// interface must be addressed with LabNetwork.CIDR's own prefix length
	// (e.g. host_ip/16 for a /16 lab, even when Subnet is a /24). A narrower
	// interface prefix makes the host route replies to on-link guests in the
	// wider CIDR via the gateway, which drops them as out-of-state.
	Subnet string `yaml:"subnet" json:"subnet"`

	// HostIP is the management-plane IP address of the lab host.
	HostIP string `yaml:"host_ip" json:"host_ip"`

	// Gateway is the management subnet's gateway address.
	Gateway string `yaml:"gateway" json:"gateway"`
}

// LabCompute describes the CPU and memory configuration for a lab's VM.
type LabCompute struct {
	// VCPU is the number of virtual CPUs assigned to the VM.
	VCPU int `yaml:"vcpu" json:"vcpu"`

	// CPUType is the QEMU CPU model presented to the guest.
	CPUType string `yaml:"cpu_type" json:"cpu_type"`

	// NUMA enables NUMA topology awareness for the VM.
	NUMA bool `yaml:"numa" json:"numa"`

	// Machine is the QEMU machine type.
	Machine string `yaml:"machine" json:"machine"`

	// Firmware is the VM firmware type (e.g. "ovmf" for UEFI, "seabios" for legacy BIOS).
	Firmware string `yaml:"firmware" json:"firmware"`

	// Memory holds the VM's minimum and maximum memory sizing.
	Memory LabMemory `yaml:"memory" json:"memory"`
}

// LabMemory describes the memory ballooning range for a lab's VM.
type LabMemory struct {
	// MinGB is the VM's minimum (guaranteed) memory in gigabytes.
	MinGB int `yaml:"min_gb" json:"min_gb"`

	// MaxGB is the VM's maximum (ballooned) memory in gigabytes. Schema
	// default is 96; a given lab may deploy up to 128 per its own needs.
	MaxGB int `yaml:"max_gb" json:"max_gb"`
}

// LabStorage describes the ZFS pool and disk sizing for a lab's VM.
type LabStorage struct {
	// Pool is the ZFS storage pool the lab's disks live on.
	Pool string `yaml:"pool" json:"pool"`

	// OSDiskGB is the size of the OS disk in gigabytes.
	OSDiskGB int `yaml:"os_disk_gb" json:"os_disk_gb"`

	// DataDiskGB is the size of the data disk in gigabytes.
	DataDiskGB int `yaml:"data_disk_gb" json:"data_disk_gb"`

	// RefquotaGB is the ZFS refquota enforced on the lab's dataset, in gigabytes.
	RefquotaGB int `yaml:"refquota_gb" json:"refquota_gb"`

	// NFSQuotaGB is the ZFS `quota` (never `refquota` — `quota` constrains a
	// dataset's descendants, which is what caps this lab's images+backup
	// exports combined) `pmx lab nfs attach`'s server-side ensure phase sets
	// on the lab's NFS parent dataset (tank/nfs/labs/<lab>, distinct from
	// this lab's own compute dataset RefquotaGB targets). Zero means unset:
	// EffectiveNFSQuotaGB then applies DefaultNFSQuotaGB (200, matching the
	// historical scripts/60-nfs-service --lab-quota default) instead.
	NFSQuotaGB int `yaml:"nfs_quota_gb,omitempty" json:"nfs_quota_gb,omitempty"`

	// Controller is the disk controller type (e.g. "virtio-scsi-single").
	Controller string `yaml:"controller" json:"controller"`

	// IOThread enables a dedicated I/O thread for the disk.
	IOThread bool `yaml:"iothread" json:"iothread"`

	// Discard enables discard/TRIM passthrough for the disk.
	Discard bool `yaml:"discard" json:"discard"`

	// SSD marks the disk as SSD-backed to the guest.
	SSD bool `yaml:"ssd" json:"ssd"`
}

// LabDNS describes the DNS zone associated with a lab.
type LabDNS struct {
	// Zone is the DNS zone name for the lab.
	Zone string `yaml:"zone" json:"zone"`
}

// LabProvisioning describes how a lab's guest is provisioned.
type LabProvisioning struct {
	// Mode selects the provisioning method (e.g. "cloud-init").
	Mode string `yaml:"mode" json:"mode"`

	// AnswerTemplate is the path to the answer-file template used to
	// provision the guest.
	AnswerTemplate string `yaml:"answer_template" json:"answer_template"`

	// SSHKeys lists the SSH public keys injected into the guest.
	SSHKeys []string `yaml:"ssh_keys" json:"ssh_keys"`
}

// LabAccess describes the pve realm, pool, and role granted to a lab's owner.
type LabAccess struct {
	// Realm is the pve authentication realm the owner is granted access under.
	Realm string `yaml:"realm" json:"realm"`

	// Pool is the pve resource pool the lab's role grant is scoped to.
	Pool string `yaml:"pool" json:"pool"`

	// Role is the pve role granted to the owner on Pool.
	Role string `yaml:"role" json:"role"`
}

// LabTopology describes the node count and QDevice tie-breaker policy of a
// lab's nested PVE cluster (multi-node lab plan §3.1). The zero value means
// a single node with no QDevice — today's shape — so labs written before
// this field existed keep working unchanged.
type LabTopology struct {
	// Nodes is how many PVE node VMs make up the lab, 1 through 5 (indexes
	// 0..Nodes-1). Zero means "not set"; callers must read
	// EffectiveTopologyNodes(t) rather than this field directly, since zero
	// defaults to 1.
	Nodes int `yaml:"nodes,omitempty" json:"nodes,omitempty"`

	// Qdevice selects the QDevice tie-breaker policy: "auto" (the default
	// when empty) adds a QDevice VM iff the effective node count is even;
	// "never" never adds one regardless of node count. No other value is
	// valid — see ValidateTopology. Callers must read
	// EffectiveTopologyQdevice(t) / QdeviceRequired(t) rather than this
	// field directly.
	Qdevice string `yaml:"qdevice,omitempty" json:"qdevice,omitempty"`

	// NodeOverrides holds optional per-node sizing overrides keyed by 0-based
	// node index (0..4). A node index absent from this map uses the lab's
	// sizing profile default for every field — see EffectiveNodeSizing. Only
	// the fields an entry actually sets (non-zero) override the profile;
	// zero-valued fields in an override fall through to the profile default,
	// exactly like the top-level Compute/Storage-over-profile precedence.
	NodeOverrides map[int]LabNodeOverride `yaml:"node_overrides,omitempty" json:"node_overrides,omitempty"`
}

// LabNodeOverride holds per-node compute/storage sizing overrides layered on
// top of a lab's sizing profile (and its lab-level Compute/Storage values)
// for one node index. A zero field means "use the profile/lab-level value
// for this field", not "set this field to zero" — there is no sizing
// dimension for which zero is a meaningful VM spec.
type LabNodeOverride struct {
	VCPU        int `yaml:"vcpu,omitempty" json:"vcpu,omitempty"`
	MemoryMinGB int `yaml:"memory_min_gb,omitempty" json:"memory_min_gb,omitempty"`
	MemoryMaxGB int `yaml:"memory_max_gb,omitempty" json:"memory_max_gb,omitempty"`
	OSDiskGB    int `yaml:"os_disk_gb,omitempty" json:"os_disk_gb,omitempty"`
	DataDiskGB  int `yaml:"data_disk_gb,omitempty" json:"data_disk_gb,omitempty"`
}

// MinTopologyNodes and MaxTopologyNodes bound LabTopology.Nodes' valid
// range: 1 (today's single-node shape) through 5 (multi-node lab plan §3.1).
const (
	MinTopologyNodes = 1
	MaxTopologyNodes = 5
)

// Valid LabTopology.Qdevice values.
const (
	QdeviceAuto  = "auto"
	QdeviceNever = "never"
)

// EffectiveTopologyNodes returns t.Nodes, defaulting to 1 (today's
// single-node shape) when unset (zero or negative).
func EffectiveTopologyNodes(t LabTopology) int {
	if t.Nodes <= 0 {
		return 1
	}
	return t.Nodes
}

// EffectiveTopologyQdevice returns t.Qdevice, defaulting to QdeviceAuto when
// empty.
func EffectiveTopologyQdevice(t LabTopology) string {
	if t.Qdevice == "" {
		return QdeviceAuto
	}
	return t.Qdevice
}

// QdeviceRequired reports whether a lab's topology calls for a QDevice
// tie-breaker VM: true iff the effective node count is even AND the
// effective policy is not QdeviceNever. An odd node count never gets a
// QDevice regardless of policy — forcing one onto odd votes flips the
// cluster to Last-Man-Standing semantics and worsens availability (multi-
// node lab plan §3.1) — so QdeviceAuto on an odd count is simply a no-op
// here, not an error; ValidateTopology separately rejects the more specific
// case of an explicitly-written "auto" combined with an odd node count, to
// surface that contradiction to the operator instead of silently ignoring
// it.
func QdeviceRequired(t LabTopology) bool {
	if EffectiveTopologyQdevice(t) == QdeviceNever {
		return false
	}
	return EffectiveTopologyNodes(t)%2 == 0
}

// ValidateTopology checks t for internal coherence and returns one message
// per problem found, or nil when t is valid. name is the lab's name, used to
// prefix every message so a multi-lab validation error (see
// validateAllTopologies) can name which lab each issue belongs to.
func ValidateTopology(name string, t LabTopology) []string {
	var issues []string

	if t.Nodes != 0 && (t.Nodes < MinTopologyNodes || t.Nodes > MaxTopologyNodes) {
		issues = append(issues, fmt.Sprintf(
			"lab %q: topology.nodes %d is out of range [%d, %d]", name, t.Nodes, MinTopologyNodes, MaxTopologyNodes))
	}

	switch t.Qdevice {
	case "", QdeviceAuto, QdeviceNever:
		// valid
	default:
		issues = append(issues, fmt.Sprintf(
			"lab %q: topology.qdevice %q must be %q or %q", name, t.Qdevice, QdeviceAuto, QdeviceNever))
	}

	// An operator who explicitly writes "qdevice: auto" is asking for a
	// QDevice; on an odd node count that request can never be satisfied
	// (§3.1: no QDevice ever on odd votes), so treat the combination as a
	// config error rather than silently no-op'ing it. Leaving qdevice unset
	// (empty string) is not "explicit" and never errors here: the common
	// case of an odd-node lab that never mentions qdevice at all (e.g. the
	// pve-cpi capstone lab, §12) must pass cleanly.
	if t.Qdevice == QdeviceAuto && EffectiveTopologyNodes(t)%2 != 0 {
		issues = append(issues, fmt.Sprintf(
			"lab %q: topology.qdevice: \"auto\" requires an even node count, got %d nodes "+
				"(odd node counts never use a QDevice; omit topology.qdevice entirely instead of writing \"auto\")",
			name, EffectiveTopologyNodes(t)))
	}

	// §3.1: a QDevice is MANDATORY at exactly 2 nodes — a bare 2-node
	// cluster is 2/2 votes, and losing either node drops the survivor below
	// quorum (/etc/pve goes read-only, no VM starts). Unlike 4 nodes, where
	// "qdevice: never" is a legitimate opt-out (the cluster still tolerates
	// one node down without a QDevice), 2 nodes has no safe "never" state,
	// so an explicit "never" here is rejected rather than silently accepted
	// into a fragile bare 2/2 cluster. Only the explicit string is checked
	// (not EffectiveTopologyQdevice): the unset default is "auto", which
	// QdeviceRequired already treats as mandatory at 2 nodes on its own.
	if EffectiveTopologyNodes(t) == 2 && t.Qdevice == QdeviceNever {
		issues = append(issues, fmt.Sprintf(
			"lab %q: topology.qdevice: \"never\" is invalid at 2 nodes — a QDevice is mandatory for a "+
				"2-node cluster (without a tie-breaker, any single node outage loses quorum); "+
				"use 3 or more nodes if you want no QDevice at all",
			name))
	}

	// Validated against the lab's own effective node count, not the global
	// [0, MaxTopologyNodes-1] range: an override index at or beyond the
	// lab's actual node count addresses a node that will never exist, and
	// EffectiveNodeSizing silently never applies it (no node loop ever
	// reaches that index), so it must be a config error rather than a
	// silently dead entry.
	effNodes := EffectiveTopologyNodes(t)
	for idx := range t.NodeOverrides {
		if idx < 0 || idx >= effNodes {
			issues = append(issues, fmt.Sprintf(
				"lab %q: topology.node_overrides key %d is out of range for a %d-node lab [0, %d]",
				name, idx, effNodes, effNodes-1))
		}
	}

	return issues
}

// SizingProfile is a set of default per-node compute/storage values a lab's
// nodes draw from when neither the lab's own Compute/Storage fields nor a
// per-node override set a given value explicitly (multi-node lab plan §3.4).
type SizingProfile struct {
	VCPU        int
	MemoryMinGB int
	MemoryMaxGB int
	OSDiskGB    int
	DataDiskGB  int
}

// SingleNodeProfile returns the default sizing for a single-node lab
// (topology.nodes == 1): today's per-lab shape, unchanged by the multi-node
// work.
func SingleNodeProfile() SizingProfile {
	return SizingProfile{VCPU: 16, MemoryMinGB: 32, MemoryMaxGB: 128, OSDiskGB: 64, DataDiskGB: 400}
}

// ClusterNodeProfile returns the default per-node sizing for a multi-node
// lab (topology.nodes > 1): a smaller footprint than the single-node
// profile, sized so several labs' clusters fit the shared tank pool at once
// (multi-node lab plan §3.4, decision D2).
func ClusterNodeProfile() SizingProfile {
	return SizingProfile{VCPU: 8, MemoryMinGB: 16, MemoryMaxGB: 48, OSDiskGB: 64, DataDiskGB: 200}
}

// ProfileForTopology returns SingleNodeProfile() when t's effective node
// count is 1, else ClusterNodeProfile().
func ProfileForTopology(t LabTopology) SizingProfile {
	if EffectiveTopologyNodes(t) == 1 {
		return SingleNodeProfile()
	}
	return ClusterNodeProfile()
}

// EffectiveNodeSizing returns the fully-resolved compute and storage sizing
// for node index idx of lab: starting from ProfileForTopology(lab.Topology),
// layering lab.Compute/lab.Storage's own non-zero fields on top (the
// lab-wide base every node without a more specific override uses, matching
// today's single-VM behavior when Compute/Storage are set), then layering
// lab.Topology.NodeOverrides[idx]'s own non-zero fields on top of that. Only
// the sizing fields are read from Storage (Controller/IOThread/Discard/SSD
// and RefquotaGB/Pool are lab-wide and copied through unchanged from
// lab.Storage). idx is not range-checked here — out-of-range indexes simply
// find no NodeOverrides entry and use the lab-level/profile values, since
// range-checking is ValidateTopology's job at config-load time, not this
// read path's.
func EffectiveNodeSizing(lab *Lab, idx int) (LabCompute, LabStorage) {
	profile := ProfileForTopology(lab.Topology)

	compute := lab.Compute
	if compute.VCPU == 0 {
		compute.VCPU = profile.VCPU
	}
	if compute.Memory.MinGB == 0 {
		compute.Memory.MinGB = profile.MemoryMinGB
	}
	if compute.Memory.MaxGB == 0 {
		compute.Memory.MaxGB = profile.MemoryMaxGB
	}

	storage := lab.Storage
	if storage.OSDiskGB == 0 {
		storage.OSDiskGB = profile.OSDiskGB
	}
	if storage.DataDiskGB == 0 {
		storage.DataDiskGB = profile.DataDiskGB
	}

	if ov, ok := lab.Topology.NodeOverrides[idx]; ok {
		if ov.VCPU != 0 {
			compute.VCPU = ov.VCPU
		}
		if ov.MemoryMinGB != 0 {
			compute.Memory.MinGB = ov.MemoryMinGB
		}
		if ov.MemoryMaxGB != 0 {
			compute.Memory.MaxGB = ov.MemoryMaxGB
		}
		if ov.OSDiskGB != 0 {
			storage.OSDiskGB = ov.OSDiskGB
		}
		if ov.DataDiskGB != 0 {
			storage.DataDiskGB = ov.DataDiskGB
		}
	}

	return compute, storage
}

// DefaultSingleRefquotaGB and DefaultClusterRefquotaPerNodeGB are the
// fallback ZFS dataset refquota values EffectiveRefquotaGB uses when a lab
// does not set storage.refquota_gb explicitly (multi-node lab plan §3.4):
// 480G for a single-node lab, or node-count × 264G (plus no extra slack
// beyond the per-node figure already carrying it) for a multi-node lab.
const (
	DefaultSingleRefquotaGB         = 480
	DefaultClusterRefquotaPerNodeGB = 264
)

// EffectiveRefquotaGB returns lab.Storage.RefquotaGB when set, else the
// profile-appropriate default: DefaultSingleRefquotaGB for a single-node
// lab, or EffectiveTopologyNodes(lab.Topology) × DefaultClusterRefquotaPerNodeGB
// for a multi-node lab. Used by the capacity gate (`pmx lab create`) to
// estimate a lab's pool reservation when the operator has not pinned an
// explicit refquota.
func EffectiveRefquotaGB(lab *Lab) int {
	if lab.Storage.RefquotaGB > 0 {
		return lab.Storage.RefquotaGB
	}
	if EffectiveTopologyNodes(lab.Topology) == 1 {
		return DefaultSingleRefquotaGB
	}
	return EffectiveTopologyNodes(lab.Topology) * DefaultClusterRefquotaPerNodeGB
}

// DefaultNFSQuotaGB is the fallback ZFS `quota` EffectiveNFSQuotaGB applies
// when a lab leaves storage.nfs_quota_gb unset (0). Matches the historical
// scripts/60-nfs-service (lab repo) script's own --lab-quota default
// exactly, so a lab attached via `pmx lab nfs attach` with no explicit
// override gets the same quota that script's fleet-wide default run applied.
const DefaultNFSQuotaGB = 200

// EffectiveNFSQuotaGB returns lab.Storage.NFSQuotaGB when the operator set
// it (a positive value), else DefaultNFSQuotaGB. This is the ZFS `quota`
// `pmx lab nfs attach`'s server-side ensure phase sets on the lab's
// tank/nfs/labs/<lab> parent dataset — distinct from EffectiveRefquotaGB,
// which sizes this lab's own compute dataset instead.
func EffectiveNFSQuotaGB(lab *Lab) int {
	if lab.Storage.NFSQuotaGB > 0 {
		return lab.Storage.NFSQuotaGB
	}
	return DefaultNFSQuotaGB
}

// MaxVnetIDLen is the Proxmox VE SDN vnet ID length limit: at most 8
// alphanumeric characters, no hyphen.
const MaxVnetIDLen = 8

// DeriveVnetID returns the deterministic SDN vnet ID for a lab named name:
// every hyphen stripped, then truncated to the first MaxVnetIDLen
// characters. This is PVE's own vnet ID constraint (≤8 alphanumeric
// characters, no hyphen), applied to the lab's name so a lab that does not
// set network.vnet_id explicitly still gets a valid, deterministic vnet ID
// (multi-node lab plan §14.1). Examples: "wayneeseguin" -> "wayneese",
// "pve-cpi" -> "pvecpi", "krutten" -> "krutten" (already ≤8 chars).
func DeriveVnetID(name string) string {
	stripped := strings.ReplaceAll(name, "-", "")
	if len(stripped) > MaxVnetIDLen {
		return stripped[:MaxVnetIDLen]
	}
	return stripped
}
