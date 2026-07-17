package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// --- DeriveVnetID ------------------------------------------------------

// TestDeriveVnetID_MatchesPlanExamples pins every worked example from the
// multi-node lab plan's §14.1 rename map: hyphens stripped, then truncated
// to the first 8 characters.
func TestDeriveVnetID_MatchesPlanExamples(t *testing.T) {
	cases := map[string]string{
		"nabramovitz":  "nabramov",
		"itsouvalas":   "itsouval",
		"wayneeseguin": "wayneese",
		"pve-cpi":      "pvecpi",
		"krutten":      "krutten",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, config.DeriveVnetID(name))
		})
	}
}

// TestDeriveVnetID_ShortNamePassesThroughUnchanged covers a name already
// under the 8-character limit with no hyphens: it must pass through
// verbatim, not be padded or altered.
func TestDeriveVnetID_ShortNamePassesThroughUnchanged(t *testing.T) {
	assert.Equal(t, "wayne", config.DeriveVnetID("wayne"))
}

// TestDeriveVnetID_MultipleHyphensAllStripped covers a name with more than
// one hyphen: every hyphen is stripped before truncation, not just the
// first.
func TestDeriveVnetID_MultipleHyphensAllStripped(t *testing.T) {
	assert.Equal(t, "abcdefgh", config.DeriveVnetID("ab-cd-efgh"))
}

// --- ValidateTopology ----------------------------------------------------

func TestValidateTopology_ZeroValueIsValid(t *testing.T) {
	assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{}))
}

func TestValidateTopology_NodesInRangeAreValid(t *testing.T) {
	for n := config.MinTopologyNodes; n <= config.MaxTopologyNodes; n++ {
		t.Run(string(rune('0'+n)), func(t *testing.T) {
			assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{Nodes: n}))
		})
	}
}

func TestValidateTopology_NodesOutOfRange_Errors(t *testing.T) {
	for _, n := range []int{-1, 6, 100} {
		issues := config.ValidateTopology("wayne", config.LabTopology{Nodes: n})
		require.NotEmpty(t, issues, "nodes=%d must be rejected", n)
		assert.Contains(t, issues[0], "wayne")
		assert.Contains(t, issues[0], "out of range")
	}
}

func TestValidateTopology_QdeviceInvalidEnum_Errors(t *testing.T) {
	issues := config.ValidateTopology("wayne", config.LabTopology{Nodes: 2, Qdevice: "always"})
	require.NotEmpty(t, issues)
	assert.Contains(t, issues[0], `"always"`)
	assert.Contains(t, issues[0], `"auto"`)
	assert.Contains(t, issues[0], `"never"`)
}

// TestValidateTopology_ExplicitAutoOnOddNodes_Errors covers the specific
// contradiction the plan calls out: an operator who explicitly writes
// "qdevice: auto" is asking for a QDevice, which an odd node count can never
// satisfy (no QDevice ever on odd votes).
func TestValidateTopology_ExplicitAutoOnOddNodes_Errors(t *testing.T) {
	issues := config.ValidateTopology("wayne", config.LabTopology{Nodes: 3, Qdevice: config.QdeviceAuto})
	require.NotEmpty(t, issues)
	assert.Contains(t, issues[0], "auto")
	assert.Contains(t, issues[0], "even node count")
}

// TestValidateTopology_UnsetQdeviceOnOddNodes_IsValid covers the common
// case: a lab with an odd node count that never mentions topology.qdevice
// at all (e.g. the pve-cpi capstone lab) must pass cleanly — only an
// explicitly-written "auto" is rejected, not the empty default.
func TestValidateTopology_UnsetQdeviceOnOddNodes_IsValid(t *testing.T) {
	assert.Empty(t, config.ValidateTopology("pve-cpi", config.LabTopology{Nodes: 3}))
}

// TestValidateTopology_NeverOnOddNodes_IsValid covers "never" combined with
// an odd node count: redundant (odd counts never get a QDevice anyway) but
// not contradictory, so it must not error.
func TestValidateTopology_NeverOnOddNodes_IsValid(t *testing.T) {
	assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{Nodes: 3, Qdevice: config.QdeviceNever}))
}

func TestValidateTopology_AutoOnEvenNodes_IsValid(t *testing.T) {
	assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{Nodes: 2, Qdevice: config.QdeviceAuto}))
	assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{Nodes: 4, Qdevice: config.QdeviceAuto}))
}

func TestValidateTopology_NodeOverridesOutOfRange_Errors(t *testing.T) {
	issues := config.ValidateTopology("wayne", config.LabTopology{
		Nodes: 3,
		NodeOverrides: map[int]config.LabNodeOverride{
			0:  {VCPU: 4},
			5:  {VCPU: 4},
			-1: {VCPU: 4},
		},
	})
	require.Len(t, issues, 2, "only the two out-of-range indexes (5, -1) should be flagged")
}

// TestValidateTopology_NodeOverrideBeyondEffectiveNodeCount_Errors covers
// M2-08: an override index within the global [0, MaxTopologyNodes-1] range
// but at or beyond THIS lab's own effective node count must still be
// rejected — index 3 addresses a node that will never exist on a 2-node
// lab, and EffectiveNodeSizing would otherwise silently never apply it (no
// node loop ever reaches index 3), leaving dead config with no warning.
func TestValidateTopology_NodeOverrideBeyondEffectiveNodeCount_Errors(t *testing.T) {
	issues := config.ValidateTopology("wayne", config.LabTopology{
		Nodes: 2,
		NodeOverrides: map[int]config.LabNodeOverride{
			0: {VCPU: 4},
			1: {VCPU: 4},
			3: {VCPU: 4}, // in [0,4] globally, but out of range for 2 nodes
		},
	})
	require.Len(t, issues, 1, "only index 3 is out of range for this 2-node lab")
	assert.Contains(t, issues[0], "3")
	assert.Contains(t, issues[0], "2-node lab")
}

// TestValidateTopology_TwoNodesQdeviceNever_Errors covers M2-06: §3.1 makes
// a QDevice mandatory at exactly 2 nodes (a bare 2/2 cluster loses quorum
// on any single node outage); an explicit "qdevice: never" at 2 nodes must
// be rejected rather than silently accepted into that fragile shape.
func TestValidateTopology_TwoNodesQdeviceNever_Errors(t *testing.T) {
	issues := config.ValidateTopology("wayne", config.LabTopology{Nodes: 2, Qdevice: config.QdeviceNever})
	require.NotEmpty(t, issues)
	assert.Contains(t, issues[0], "2 nodes")
	assert.Contains(t, issues[0], "mandatory")
}

// TestValidateTopology_TwoNodesQdeviceUnsetOrAuto_IsValid covers the
// non-error paths at 2 nodes: leaving qdevice unset (defaults to "auto",
// which QdeviceRequired already treats as mandatory) or writing "auto"
// explicitly must both pass cleanly — only an explicit "never" is rejected.
func TestValidateTopology_TwoNodesQdeviceUnsetOrAuto_IsValid(t *testing.T) {
	assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{Nodes: 2}))
	assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{Nodes: 2, Qdevice: config.QdeviceAuto}))
}

// TestValidateTopology_FourNodesQdeviceNever_IsValid covers the contrast
// with M2-06: at 4 nodes (unlike 2), "qdevice: never" is a legitimate
// opt-out (§3.1: the cluster still tolerates one node down without a
// QDevice) and must not be rejected.
func TestValidateTopology_FourNodesQdeviceNever_IsValid(t *testing.T) {
	assert.Empty(t, config.ValidateTopology("wayne", config.LabTopology{Nodes: 4, Qdevice: config.QdeviceNever}))
}

// --- EffectiveTopologyNodes / EffectiveTopologyQdevice / QdeviceRequired --

func TestEffectiveTopologyNodes_DefaultsToOne(t *testing.T) {
	assert.Equal(t, 1, config.EffectiveTopologyNodes(config.LabTopology{}))
	assert.Equal(t, 1, config.EffectiveTopologyNodes(config.LabTopology{Nodes: 0}))
	assert.Equal(t, 3, config.EffectiveTopologyNodes(config.LabTopology{Nodes: 3}))
}

func TestEffectiveTopologyQdevice_DefaultsToAuto(t *testing.T) {
	assert.Equal(t, config.QdeviceAuto, config.EffectiveTopologyQdevice(config.LabTopology{}))
	assert.Equal(t, config.QdeviceNever, config.EffectiveTopologyQdevice(config.LabTopology{Qdevice: config.QdeviceNever}))
}

// TestQdeviceRequired_Table covers every node count 1-5 crossed with every
// qdevice policy, pinning the multi-node lab plan §3.1 rule: a QDevice is
// added iff the node count is even AND policy is not "never" — never on an
// odd count, regardless of policy.
func TestQdeviceRequired_Table(t *testing.T) {
	cases := []struct {
		nodes   int
		qdevice string
		want    bool
	}{
		{1, "", false},
		{1, config.QdeviceNever, false},
		{2, "", true}, // mandatory: auto is the default
		{2, config.QdeviceAuto, true},
		{2, config.QdeviceNever, false},
		{3, "", false},
		{3, config.QdeviceNever, false},
		{4, "", true}, // recommended, on by default
		{4, config.QdeviceAuto, true},
		{4, config.QdeviceNever, false},
		{5, "", false},
		{5, config.QdeviceNever, false},
	}
	for _, tc := range cases {
		got := config.QdeviceRequired(config.LabTopology{Nodes: tc.nodes, Qdevice: tc.qdevice})
		assert.Equal(t, tc.want, got, "nodes=%d qdevice=%q", tc.nodes, tc.qdevice)
	}
}

// --- Sizing profiles / EffectiveNodeSizing / EffectiveRefquotaGB ---------

func TestProfileForTopology_SingleVsCluster(t *testing.T) {
	single := config.ProfileForTopology(config.LabTopology{Nodes: 1})
	assert.Equal(t, config.SingleNodeProfile(), single)

	cluster := config.ProfileForTopology(config.LabTopology{Nodes: 3})
	assert.Equal(t, config.ClusterNodeProfile(), cluster)
}

// TestEffectiveNodeSizing_FallsBackToProfileWhenLabUnset covers a lab with
// zero-valued Compute/Storage and no node override: every node must receive
// the profile's default sizing.
func TestEffectiveNodeSizing_FallsBackToProfileWhenLabUnset(t *testing.T) {
	lab := &config.Lab{Name: "wayne", Topology: config.LabTopology{Nodes: 3}}

	compute, storage := config.EffectiveNodeSizing(lab, 0)
	profile := config.ClusterNodeProfile()
	assert.Equal(t, profile.VCPU, compute.VCPU)
	assert.Equal(t, profile.MemoryMinGB, compute.Memory.MinGB)
	assert.Equal(t, profile.MemoryMaxGB, compute.Memory.MaxGB)
	assert.Equal(t, profile.OSDiskGB, storage.OSDiskGB)
	assert.Equal(t, profile.DataDiskGB, storage.DataDiskGB)
}

// TestEffectiveNodeSizing_LabLevelOverridesProfile covers a lab whose own
// Compute/Storage fields are set: those act as the base for every node,
// ahead of the profile default.
func TestEffectiveNodeSizing_LabLevelOverridesProfile(t *testing.T) {
	lab := &config.Lab{
		Name:     "wayne",
		Topology: config.LabTopology{Nodes: 3},
		Compute:  config.LabCompute{VCPU: 12, Memory: config.LabMemory{MinGB: 24, MaxGB: 64}},
		Storage:  config.LabStorage{OSDiskGB: 80, DataDiskGB: 300},
	}

	compute, storage := config.EffectiveNodeSizing(lab, 1)
	assert.Equal(t, 12, compute.VCPU)
	assert.Equal(t, 24, compute.Memory.MinGB)
	assert.Equal(t, 64, compute.Memory.MaxGB)
	assert.Equal(t, 80, storage.OSDiskGB)
	assert.Equal(t, 300, storage.DataDiskGB)
}

// TestEffectiveNodeSizing_PerNodeOverrideWinsOverLabAndProfile covers the
// full precedence chain: profile default < lab-level Compute/Storage <
// topology.node_overrides[idx], and only for the overridden node index —
// every other node keeps the lab-level/profile value.
func TestEffectiveNodeSizing_PerNodeOverrideWinsOverLabAndProfile(t *testing.T) {
	lab := &config.Lab{
		Name: "wayne",
		Topology: config.LabTopology{
			Nodes: 3,
			NodeOverrides: map[int]config.LabNodeOverride{
				1: {VCPU: 32, DataDiskGB: 999},
			},
		},
		Compute: config.LabCompute{VCPU: 12},
	}

	node0Compute, _ := config.EffectiveNodeSizing(lab, 0)
	assert.Equal(t, 12, node0Compute.VCPU, "node 0 keeps the lab-level value, unaffected by node 1's override")

	node1Compute, node1Storage := config.EffectiveNodeSizing(lab, 1)
	assert.Equal(t, 32, node1Compute.VCPU, "node 1's override wins over the lab-level value")
	assert.Equal(t, 999, node1Storage.DataDiskGB, "node 1's override wins over the profile default")
	assert.Equal(t, config.ClusterNodeProfile().OSDiskGB, node1Storage.OSDiskGB,
		"a field the override left zero still falls through to the profile default")
}

func TestEffectiveRefquotaGB_ExplicitWinsOverProfile(t *testing.T) {
	lab := &config.Lab{Name: "wayne", Storage: config.LabStorage{RefquotaGB: 900}}
	assert.Equal(t, 900, config.EffectiveRefquotaGB(lab))
}

func TestEffectiveRefquotaGB_SingleNodeDefault(t *testing.T) {
	lab := &config.Lab{Name: "wayne", Topology: config.LabTopology{Nodes: 1}}
	assert.Equal(t, config.DefaultSingleRefquotaGB, config.EffectiveRefquotaGB(lab))
}

func TestEffectiveRefquotaGB_ClusterDefaultScalesWithNodes(t *testing.T) {
	lab := &config.Lab{Name: "wayne", Topology: config.LabTopology{Nodes: 3}}
	assert.Equal(t, 3*config.DefaultClusterRefquotaPerNodeGB, config.EffectiveRefquotaGB(lab))
}
