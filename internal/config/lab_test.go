package config_test

import (
	"testing"

	yaml "github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// --- backward compatibility: absent new keys ------------------------------

// todaysShapeLabYAML is a single-NIC, single-vnet lab file exactly as it
// exists in the fleet today: none of Vnets/HostNICs/NestedNetwork's keys are
// present. Every existing member lab, "pmx", and pve-cpi's on-disk file
// before this change all look like this.
const todaysShapeLabYAML = `
name: krutten
mode: nested
owner: krutten@pve
network:
  vnet_id: krutten
  vnet_alias: krutten-lab
  vxlan_tag: 5002
  cidr: 10.109.0.0/16
  mgmt:
    subnet: 10.109.0.0/24
    host_ip: 10.109.0.10
    gateway: 10.109.0.1
  bosh_bloc: 10.109.16.0/20
  mtu: 1450
compute: {vcpu: 16, cpu_type: host, numa: true, machine: q35, firmware: ovmf, memory: {min_gb: 32, max_gb: 96}}
storage: {pool: tank-lab-krutten, os_disk_gb: 64, data_disk_gb: 400, refquota_gb: 480, controller: virtio-scsi-single, iothread: true, discard: true, ssd: true}
dns: {zone: krutten.lab.fivetwenty.io}
access: {realm: pve, pool: lab-krutten, role: PMXAdmin}
`

// TestLabNetwork_AbsentNewKeys_StrictParseUnaffected pins the backward-
// compat rule the multi-node lab plan §1 promises: a lab file that predates
// Vnets/HostNICs/NestedNetwork parses byte-for-byte the same under the
// same yaml.Strict() decode loadLabFile uses, with every new field at its
// nil/zero value — not a hollow struct, not a decode error, not a spurious
// "unknown field" complaint about fields the new struct itself grew.
func TestLabNetwork_AbsentNewKeys_StrictParseUnaffected(t *testing.T) {
	var lab config.Lab
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(todaysShapeLabYAML), &lab, yaml.Strict()))

	// Existing fields: unaffected.
	assert.Equal(t, "krutten", lab.Name)
	assert.Equal(t, "krutten", lab.Network.VnetID)
	assert.Equal(t, 5002, lab.Network.VxlanTag)
	assert.Equal(t, "10.109.0.0/16", lab.Network.CIDR)

	// New fields: absent ⇒ zero value ⇒ today's single-NIC/single-vnet/
	// no-bond code path, per §1's explicit backward-compat rule.
	assert.Nil(t, lab.Network.Vnets)
	assert.Nil(t, lab.Network.HostNICs)
	assert.Equal(t, config.LabNestedNetwork{}, lab.Network.NestedNetwork)
	assert.Nil(t, lab.Network.NestedNetwork.Bonds)
	assert.Nil(t, lab.Network.NestedNetwork.VlanZone)

	// EffectiveHostNICs is a nil-safe passthrough, not a panic or a
	// synthesized default.
	assert.Nil(t, lab.Network.EffectiveHostNICs())

	// A zero-value NestedNetwork must validate cleanly — no bonding
	// configured is not itself an issue.
	assert.Empty(t, config.ValidateNestedNetwork(lab.Name, lab.Network.NestedNetwork))
}

// --- full schema round trip: pve-cpi (az1) example from the multi-node ---
// --- lab plan §1 -----------------------------------------------------------

// pveCPIAz1LabYAML is the exact az1 example from the multi-node lab plan
// §1 (block-style vnets/host_nics/nested_network), the already-live
// pve-cpi cluster's target on-disk shape.
const pveCPIAz1LabYAML = `
name: pve-cpi
mode: nested
network:
  vnet_id: pvecpi
  vnet_alias: lab-pve-cpi
  vxlan_tag: 5009
  cidr: 10.254.0.0/16
  mgmt:
    subnet: 10.254.0.0/24
    host_ip: 10.254.0.10
    gateway: 10.254.0.1
  bosh_bloc: 10.254.16.0/20
  mtu: 1500
  vnets:
    - id: pvecpist
      alias: lab-pve-cpi-storage
      tag: 5011
      cidr: 10.254.32.0/24
      gateway: 10.254.32.1
      purpose: storage
    - id: pvecpiwk
      alias: lab-pve-cpi-workload
      tag: 5012
      purpose: workload
  host_nics:
    - {index: 1, vnet_id: pvecpi}
    - {index: 2, vnet_id: pvecpist}
    - {index: 3, vnet_id: pvecpist}
    - {index: 4, vnet_id: pvecpiwk}
    - {index: 5, vnet_id: pvecpiwk}
  nested_network:
    bonds:
      - {name: bond0, nics: [nic0, nic1], mode: active-backup, primary: nic0, bridge: vmbr0}
      - {name: bond1, nics: [nic2, nic3], mode: active-backup, primary: nic2, bridge: vmbr1}
      - {name: bond2, nics: [nic4, nic5], mode: active-backup, primary: nic4, bridge: vmbr2, vlan_aware: true}
    vlan_zone:
      bridge: vmbr2
      zone_name: clivlan
      vnets:
        - {id: cli40, tag: 40, cidr: 10.61.136.0/24, gateway: 10.61.136.1, alias: client-vlan40}
        - {id: cli38, tag: 38, cidr: 10.61.137.0/24, gateway: 10.61.137.1, alias: client-vlan38}
        - {id: cli59, tag: 59, cidr: 10.61.138.0/24, gateway: 10.61.138.1, alias: client-vlan59}
        - {id: cli60, tag: 60, cidr: 10.61.139.0/24, gateway: 10.61.139.1, alias: client-vlan60}
compute: {vcpu: 8, cpu_type: host, numa: true, machine: q35, firmware: ovmf, memory: {min_gb: 16, max_gb: 48}}
storage: {pool: tank, os_disk_gb: 64, data_disk_gb: 200, refquota_gb: 850, controller: virtio-scsi-single, iothread: true, discard: true, ssd: true}
dns: {zone: pve-cpi.lab.fivetwenty.io}
access: {realm: pve, pool: lab-pve-cpi, role: PVEVMUser}
topology: {nodes: 3, qdevice: never}
`

// TestLabNetwork_FullSchema_PVECPIAz1_RoundTrip parses the exact az1
// example from the multi-node lab plan §1 and asserts every new field
// decodes to the expected value, under the same strict decode loadLabFile
// uses.
func TestLabNetwork_FullSchema_PVECPIAz1_RoundTrip(t *testing.T) {
	var lab config.Lab
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(pveCPIAz1LabYAML), &lab, yaml.Strict()))

	require.Equal(t, "pve-cpi", lab.Name)
	require.Equal(t, "pvecpi", lab.Network.VnetID)
	require.Equal(t, 5009, lab.Network.VxlanTag)

	require.Len(t, lab.Network.Vnets, 2)
	assert.Equal(t, config.LabVnet{
		ID: "pvecpist", Alias: "lab-pve-cpi-storage", Tag: 5011,
		CIDR: "10.254.32.0/24", Gateway: "10.254.32.1", Purpose: "storage",
	}, lab.Network.Vnets[0])
	assert.Equal(t, config.LabVnet{
		ID: "pvecpiwk", Alias: "lab-pve-cpi-workload", Tag: 5012, Purpose: "workload",
	}, lab.Network.Vnets[1])
	// Workload vnet has no cidr: pure L2 trunk passthrough (R5 Q1).
	assert.Empty(t, lab.Network.Vnets[1].CIDR)

	require.Len(t, lab.Network.HostNICs, 5)
	assert.Equal(t, config.LabHostNIC{Index: 1, VnetID: "pvecpi"}, lab.Network.HostNICs[0])
	assert.Equal(t, config.LabHostNIC{Index: 2, VnetID: "pvecpist"}, lab.Network.HostNICs[1])
	assert.Equal(t, config.LabHostNIC{Index: 5, VnetID: "pvecpiwk"}, lab.Network.HostNICs[4])
	assert.Equal(t, lab.Network.HostNICs, lab.Network.EffectiveHostNICs())

	require.Len(t, lab.Network.NestedNetwork.Bonds, 3)
	assert.Equal(t, config.LabNestedBond{
		Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: "active-backup",
		Primary: "nic0", Bridge: "vmbr0",
	}, lab.Network.NestedNetwork.Bonds[0])
	bond2 := lab.Network.NestedNetwork.Bonds[2]
	assert.Equal(t, "bond2", bond2.Name)
	assert.Equal(t, "vmbr2", bond2.Bridge)
	assert.True(t, bond2.VlanAware)

	require.NotNil(t, lab.Network.NestedNetwork.VlanZone)
	vz := lab.Network.NestedNetwork.VlanZone
	assert.Equal(t, "vmbr2", vz.Bridge)
	assert.Equal(t, "clivlan", vz.ZoneName)
	require.Len(t, vz.Vnets, 4)
	assert.Equal(t, config.LabNestedVlanVnet{
		ID: "cli40", Tag: 40, CIDR: "10.61.136.0/24", Gateway: "10.61.136.1", Alias: "client-vlan40",
	}, vz.Vnets[0])
	assert.Equal(t, config.LabNestedVlanVnet{
		ID: "cli60", Tag: 60, CIDR: "10.61.139.0/24", Gateway: "10.61.139.1", Alias: "client-vlan60",
	}, vz.Vnets[3])

	// A fully-populated, spec-shaped nested network must validate clean.
	assert.Empty(t, config.ValidateNestedNetwork(lab.Name, lab.Network.NestedNetwork))

	// Existing fields untouched by any of the new parsing: topology,
	// compute, storage, dns, access all still resolve as before.
	assert.Equal(t, 3, lab.Topology.Nodes)
	assert.Equal(t, "never", lab.Topology.Qdevice)
	assert.Equal(t, "PVEVMUser", lab.Access.Role)
}

// --- full schema round trip: pve-cpi-az2 (compact flow-style) example ----

// pveCPIAz2LabYAML is the exact az2 example from the multi-node lab plan
// §1: same schema, all-flow-mapping compact style, exercising a different
// YAML surface syntax against the same struct tags.
const pveCPIAz2LabYAML = `
name: pve-cpi-az2
mode: nested
network:
  vnet_id: pvecpi2
  vnet_alias: lab-pve-cpi-az2
  vxlan_tag: 5010
  cidr: 10.255.0.0/16
  mgmt: {subnet: 10.255.0.0/24, host_ip: 10.255.0.10, gateway: 10.255.0.1}
  bosh_bloc: 10.255.16.0/20
  mtu: 1500
  vnets:
    - {id: pvecp2st, alias: lab-pve-cpi-az2-storage, tag: 5013, cidr: 10.255.32.0/24, gateway: 10.255.32.1, purpose: storage}
    - {id: pvecp2wk, alias: lab-pve-cpi-az2-workload, tag: 5014, purpose: workload}
  host_nics:
    - {index: 1, vnet_id: pvecpi2}
    - {index: 2, vnet_id: pvecp2st}
    - {index: 3, vnet_id: pvecp2st}
    - {index: 4, vnet_id: pvecp2wk}
    - {index: 5, vnet_id: pvecp2wk}
  nested_network:
    bonds:
      - {name: bond0, nics: [nic0, nic1], mode: active-backup, primary: nic0, bridge: vmbr0}
      - {name: bond1, nics: [nic2, nic3], mode: active-backup, primary: nic2, bridge: vmbr1}
      - {name: bond2, nics: [nic4, nic5], mode: active-backup, primary: nic4, bridge: vmbr2, vlan_aware: true}
    vlan_zone:
      bridge: vmbr2
      zone_name: clivlan
      vnets:
        - {id: cli40, tag: 40, cidr: 10.61.136.0/24, gateway: 10.61.136.1, alias: client-vlan40}
        - {id: cli38, tag: 38, cidr: 10.61.137.0/24, gateway: 10.61.137.1, alias: client-vlan38}
        - {id: cli59, tag: 59, cidr: 10.61.138.0/24, gateway: 10.61.138.1, alias: client-vlan59}
        - {id: cli60, tag: 60, cidr: 10.61.139.0/24, gateway: 10.61.139.1, alias: client-vlan60}
compute: {vcpu: 8, cpu_type: host, numa: true, machine: q35, firmware: ovmf, memory: {min_gb: 16, max_gb: 48}}
storage: {pool: tank, os_disk_gb: 64, data_disk_gb: 200, refquota_gb: 850, controller: virtio-scsi-single, iothread: true, discard: true, ssd: true}
dns: {zone: pve-cpi-az2.lab.fivetwenty.io}
access: {realm: pve, pool: lab-pve-cpi-az2, role: PVEVMUser}
topology: {nodes: 3, qdevice: never}
`

// TestLabNetwork_FullSchema_PVECPIAz2_RoundTrip covers the second lab
// entry's shape (D-02: "2 clusters" = 2 Lab entries, not a new schema
// list) — same struct tags, independent vnet/tag/CIDR identifiers, and the
// identical 4 client-VLAN vnets (R7 client parity: az1/az2 share subnets).
func TestLabNetwork_FullSchema_PVECPIAz2_RoundTrip(t *testing.T) {
	var lab config.Lab
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(pveCPIAz2LabYAML), &lab, yaml.Strict()))

	require.Equal(t, "pve-cpi-az2", lab.Name)
	require.Equal(t, "pvecpi2", lab.Network.VnetID)
	require.Equal(t, 5010, lab.Network.VxlanTag)

	require.Len(t, lab.Network.Vnets, 2)
	assert.Equal(t, "pvecp2st", lab.Network.Vnets[0].ID)
	assert.Equal(t, 5013, lab.Network.Vnets[0].Tag)
	assert.Equal(t, "pvecp2wk", lab.Network.Vnets[1].ID)
	assert.Empty(t, lab.Network.Vnets[1].CIDR)

	require.Len(t, lab.Network.HostNICs, 5)
	require.Len(t, lab.Network.NestedNetwork.Bonds, 3)
	require.NotNil(t, lab.Network.NestedNetwork.VlanZone)

	// az1/az2 client-VLAN vnets: identical IDs/tags/subnets (R7 parity),
	// distinct outer vnet IDs/tags (5013/5014 vs 5011/5012, R5 Q3 table).
	az2VZ := lab.Network.NestedNetwork.VlanZone
	require.Len(t, az2VZ.Vnets, 4)
	assert.Equal(t, "cli40", az2VZ.Vnets[0].ID)
	assert.Equal(t, "10.61.136.0/24", az2VZ.Vnets[0].CIDR)

	assert.Empty(t, config.ValidateNestedNetwork(lab.Name, lab.Network.NestedNetwork))
}

// --- ValidateNestedNetwork ---------------------------------------------

// validBonds returns the spec's 3-bond, D-04-compliant nested topology,
// reused as a base by several ValidateNestedNetwork test cases below.
func validBonds() []config.LabNestedBond {
	return []config.LabNestedBond{
		{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: config.NestedBondModeActiveBackup, Primary: "nic0", Bridge: "vmbr0"},
		{Name: "bond1", NICs: []string{"nic2", "nic3"}, Mode: config.NestedBondModeActiveBackup, Primary: "nic2", Bridge: "vmbr1"},
		{Name: "bond2", NICs: []string{"nic4", "nic5"}, Mode: config.NestedBondModeActiveBackup, Primary: "nic4", Bridge: "vmbr2", VlanAware: true},
	}
}

func TestValidateNestedNetwork_ZeroValueIsValid(t *testing.T) {
	assert.Empty(t, config.ValidateNestedNetwork("wayne", config.LabNestedNetwork{}))
}

func TestValidateNestedNetwork_FullyValidTopology_NoIssues(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: validBonds(),
		VlanZone: &config.LabNestedVlanZone{
			Bridge:   "vmbr2",
			ZoneName: "clivlan",
			Vnets: []config.LabNestedVlanVnet{
				{ID: "cli40", Tag: 40, CIDR: "10.61.136.0/24", Gateway: "10.61.136.1"},
			},
		},
	}
	assert.Empty(t, config.ValidateNestedNetwork("pve-cpi", nn))
}

// TestValidateNestedNetwork_8023ad_IsWarningNotError is the acceptance case
// team-lead called out explicitly: D-04 says active-backup is the only mode
// that actually aggregates when nested, but an operator authoring 802.3ad
// for syntax parity must get a warning-class issue string, not a hard
// validation failure that blocks the config outright.
func TestValidateNestedNetwork_8023ad_IsWarningNotError(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: config.NestedBondMode8023ad, Bridge: "vmbr0"},
		},
	}
	issues := config.ValidateNestedNetwork("wayne", nn)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "warning: ")
	assert.Contains(t, issues[0], `mode "802.3ad"`)
	assert.Contains(t, issues[0], "D-04")
}

func TestValidateNestedNetwork_InvalidMode_IsHardError(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: "round-robin", Bridge: "vmbr0"},
		},
	}
	issues := config.ValidateNestedNetwork("wayne", nn)
	require.Len(t, issues, 1)
	assert.NotContains(t, issues[0], "warning: ")
	assert.Contains(t, issues[0], `"round-robin"`)
	assert.Contains(t, issues[0], "must be")
}

func TestValidateNestedNetwork_TooFewNICs_IsHardError(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0"}, Mode: config.NestedBondModeActiveBackup, Bridge: "vmbr0"},
		},
	}
	issues := config.ValidateNestedNetwork("wayne", nn)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "need at least 2")
}

func TestValidateNestedNetwork_ZeroNICs_IsHardError(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", Mode: config.NestedBondModeActiveBackup, Bridge: "vmbr0"},
		},
	}
	issues := config.ValidateNestedNetwork("wayne", nn)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "need at least 2")
}

func TestValidateNestedNetwork_VlanZoneBridge_NoMatchingBond_IsHardError(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: validBonds(),
		VlanZone: &config.LabNestedVlanZone{
			Bridge:   "vmbr9", // no bond declares this bridge at all
			ZoneName: "clivlan",
			Vnets:    []config.LabNestedVlanVnet{{ID: "cli40", Tag: 40, CIDR: "10.61.136.0/24"}},
		},
	}
	issues := config.ValidateNestedNetwork("wayne", nn)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], `"vmbr9"`)
	assert.Contains(t, issues[0], "no matching")
}

func TestValidateNestedNetwork_VlanZoneBridge_NotVlanAware_IsHardError(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			// vmbr0 exists as a bond bridge, but is not vlan_aware: true.
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: config.NestedBondModeActiveBackup, Bridge: "vmbr0"},
		},
		VlanZone: &config.LabNestedVlanZone{
			Bridge:   "vmbr0",
			ZoneName: "clivlan",
			Vnets:    []config.LabNestedVlanVnet{{ID: "cli40", Tag: 40, CIDR: "10.61.136.0/24"}},
		},
	}
	issues := config.ValidateNestedNetwork("wayne", nn)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], `"vmbr0"`)
}

func TestValidateNestedNetwork_MultipleIssues_AllReported(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0"}, Mode: "bogus", Bridge: "vmbr0"},
			{Name: "bond1", NICs: []string{"nic2", "nic3"}, Mode: config.NestedBondMode8023ad, Bridge: "vmbr1"},
		},
		VlanZone: &config.LabNestedVlanZone{Bridge: "vmbr9", ZoneName: "clivlan"},
	}
	issues := config.ValidateNestedNetwork("wayne", nn)
	// bond0: invalid mode + too-few nics (2); bond1: 802.3ad warning (1);
	// vlan_zone.bridge no match (1) = 4 total, every problem surfaced, not
	// just the first one found.
	require.Len(t, issues, 4)
}

// --- LabVnet / LabHostNIC: standalone zero-value shape --------------------

func TestLabNetwork_EffectiveHostNICs_NilSafePassthrough(t *testing.T) {
	var n config.LabNetwork
	assert.Nil(t, n.EffectiveHostNICs())

	n.HostNICs = []config.LabHostNIC{{Index: 1, VnetID: "pvecpi"}}
	assert.Equal(t, n.HostNICs, n.EffectiveHostNICs())
}
