package lab

import (
	"context"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// reconcileTestCmd builds a `pmx lab hostnet`-shaped command whose deps
// point at f, with labInnerAPIClient stubbed to hand back a client for that
// same fake — the nested cluster's API, from an outer-context caller's point
// of view. Returns the command for hostnetReconcileNodes to run against.
func reconcileTestCmd(t *testing.T, f *testhelper.FakePVE, lab *config.Lab) *cobra.Command {
	t.Helper()

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{lab.Name: lab}})
	cmd := buildHostnetCmd(t, path, f, "outer", exec.Fake())

	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		return apiclient.NewAPIClient(f.Options)
	}
	return cmd
}

// TestHostnetReconcileNodes_StagesV6OnEveryNode covers the gap this file
// exists to close: `pmx lab scale` and `pmx lab cluster join` run against
// the OUTER host, so before this, a node they brought up never received the
// management IPv6 the lab's address plan gives it. The reconcile must stage
// that address on each named node's vmbr0 through the lab's own nested
// context and apply it, without the operator running `hostnet apply` by
// hand.
func TestHostnetReconcileNodes_StagesV6OnEveryNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")
	lab.Name = "wayne"

	var updateRec, applyRec []hostnetRecordedRequest
	for _, node := range []string{"lab-wayne-0", "lab-wayne-1"} {
		hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/"+node+"/network",
			[]any{hostnetVmbr0Row(nil)}, 200)
		hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/"+node+"/network/vmbr0", nil, 200)
		hostnetRecord(f, &applyRec, nil, "", "PUT /api2/json/nodes/"+node+"/network", nil, 200)
	}

	cmd := reconcileTestCmd(t, f, lab)
	rows, _ := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0, 1})

	require.Len(t, updateRec, 2, "one vmbr0 IPv6 stage per node")
	require.Len(t, applyRec, 2, "each node's staged changes applied exactly once")

	for i, rec := range updateRec {
		addr6, err := labNodeMgmtIP6(lab.Network, i)
		require.NoError(t, err)
		gw6, err := labMgmtGateway6(lab.Network)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("%s/%d", addr6, labV6InterfacePrefixBits), rec.body["cidr6"],
			"node %d is addressed at its own plan offset, with the /48 interface prefix", i)
		assert.Equal(t, gw6, rec.body["gateway6"])
		assert.Equal(t, "10.10.1.10/24", rec.body["cidr"], "the node's existing IPv4 addressing rides along")
	}

	joined := fmt.Sprintf("%v", rows)
	assert.Contains(t, joined, "lab-wayne-0")
	assert.Contains(t, joined, "lab-wayne-1")
	assert.NotContains(t, joined, "deferred")
}

// TestHostnetReconcileNodes_UnreachableContextDefersWithFollowup pins the
// non-fatal contract: a lab whose nested context was never registered must
// cost one explanatory row naming the follow-up command — never an error,
// which in `pmx lab scale` would turn a completed cluster transition into a
// failed one.
func TestHostnetReconcileNodes_UnreachableContextDefersWithFollowup(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")
	lab.Name = "wayne"
	cmd := reconcileTestCmd(t, f, lab)

	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		return nil, fmt.Errorf("context %q not found", "lab-wayne")
	}

	rows, _ := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0})
	require.Len(t, rows, 1)
	assert.Contains(t, rows[0][1], "deferred")
	assert.Contains(t, rows[0][1], "pmx lab context sync wayne")
	assert.Contains(t, rows[0][1], "pmx -c lab-wayne lab hostnet apply wayne")
}

// TestHostnetReconcileNodes_NodeFailureDefersAndContinues pins per-node
// isolation: node 0 erroring (not provisioned yet, API refusing) must not
// stop node 1 from converging — a transition that grew two nodes must not
// leave the second unaddressed because the first was not ready.
func TestHostnetReconcileNodes_NodeFailureDefersAndContinues(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")
	lab.Name = "wayne"

	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network", nil, 500)
	var updateRec []hostnetRecordedRequest
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-1/network",
		[]any{hostnetVmbr0Row(nil)}, 200)
	hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/lab-wayne-1/network/vmbr0", nil, 200)
	hostnetRecord(f, nil, nil, "", "PUT /api2/json/nodes/lab-wayne-1/network", nil, 200)

	cmd := reconcileTestCmd(t, f, lab)
	rows, _ := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0, 1})

	joined := fmt.Sprintf("%v", rows)
	assert.Contains(t, joined, "deferred", "node 0's failure is reported")
	require.Len(t, updateRec, 1, "node 1 still converges after node 0 failed")
}

// TestHostnetReconcileNodes_NothingConfiguredIsSilent pins the quiet path:
// an IPv4-only lab with no nested bonds has no nested host-network state at
// all, so the phase must not even build a client, let alone emit rows.
func TestHostnetReconcileNodes_NothingConfiguredIsSilent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")
	lab.Name = "wayne"
	off := false
	lab.Network.IPv6 = &off

	cmd := reconcileTestCmd(t, f, lab)
	called := false
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		called = true
		return apiclient.NewAPIClient(f.Options)
	}

	noopRows, noopErr := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0})
	assert.Empty(t, noopRows)
	assert.NoError(t, noopErr)
	assert.False(t, called, "a lab with nothing to reconcile must not build a nested-context client")
}

// TestHostnetReconcileNodes_BondsCarryNICNamingCaveat pins the one thing
// this phase deliberately does not do: the ssh NIC-naming pass, which can
// leave a node reboot-pending. A bonded lab must be told so rather than
// reading a green table as full convergence.
func TestHostnetReconcileNodes_BondsCarryNICNamingCaveat(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	lab.Name = "wayne"

	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network",
		[]any{hostnetVmbr0Row(nil)}, 200)
	hostnetRecord(f, nil, nil, "", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, nil, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, nil, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	cmd := reconcileTestCmd(t, f, lab)
	rows, _ := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0})

	joined := fmt.Sprintf("%v", rows)
	assert.Contains(t, joined, "NIC naming")
	assert.Contains(t, joined, "hostnet apply")
}

// TestHostnetReconcilePreviewRows_DerivesFromConfigAlone pins the dry-run
// counterpart: the preview names each node's real planned address without
// touching the network, so a `--dry-run` scale shows the addressing it would
// apply.
func TestHostnetReconcilePreviewRows_DerivesFromConfigAlone(t *testing.T) {
	lab := cleanLab("wayne")
	lab.Name = "wayne"
	rows := hostnetReconcilePreviewRows(lab, []int{0, 1})
	require.Len(t, rows, 2)

	for i, row := range rows {
		addr6, err := labNodeMgmtIP6(lab.Network, i)
		require.NoError(t, err)
		assert.Contains(t, row[0], hostnetNodeName("wayne", i))
		assert.Contains(t, row[1], addr6)
	}

	off := false
	lab.Network.IPv6 = &off
	assert.Empty(t, hostnetReconcilePreviewRows(lab, []int{0, 1}),
		"an IPv4-only lab with no bonds previews nothing")
}

// hostnetNICRows returns list entries for the six renamed physical NICs a
// converged lab node carries (nic0-nic5), the precondition for staging any
// bond from an outer-context verb.
func hostnetNICRows() []any {
	rows := make([]any, 0, hostnetRequiredNICCount)
	for i := range hostnetRequiredNICCount {
		rows = append(rows, map[string]any{"iface": hostnetNICName(i), "type": "eth"})
	}
	return rows
}

// TestHostnetReconcileNodes_UnnamedNICsSkipBondsButStillAddressIPv6 pins the
// safety rule that makes folding the bond phase into a topology transition
// survivable: PVE accepts a bond whose slave interfaces do not exist and
// only fails when the staged batch is APPLIED — on the node's own management
// path. A node whose NICs are still ens18-style therefore gets no bond
// staged here (that is `hostnet apply`'s job, since renaming needs ssh and a
// reboot), but must still receive its management IPv6.
func TestHostnetReconcileNodes_UnnamedNICsSkipBondsButStillAddressIPv6(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	lab.Name = "wayne"

	var bondRec, updateRec []hostnetRecordedRequest
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network",
		[]any{hostnetVmbr0Row(nil)}, 200)
	hostnetRecord(f, &bondRec, nil, "", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, nil, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	cmd := reconcileTestCmd(t, f, lab)
	rows, _ := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0})

	assert.Empty(t, bondRec, "no bond may be staged against NICs that do not exist yet")
	require.Len(t, updateRec, 1, "the node still gets its management IPv6")
	assert.Contains(t, fmt.Sprintf("%v", rows), "bond/bridge phase skipped")
}

// TestHostnetReconcileNodes_NamedNICsStageBonds is the other half: a node
// already carrying nic0-nic5 (first-boot-provisioned, or converged by an
// earlier `hostnet apply`) gets its configured bond staged from the
// transition itself.
func TestHostnetReconcileNodes_NamedNICsStageBonds(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	lab.Name = "wayne"

	var bondRec []hostnetRecordedRequest
	// vmbr0 still holds nic0 directly (the pre-bond installer shape), which
	// hostnetRestageBridgeIfSlaveConflict frees before the bond is created.
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network",
		append([]any{hostnetVmbr0Row(map[string]any{"bridge_ports": "nic0"})}, hostnetNICRows()...), 200)
	hostnetRecord(f, &bondRec, nil, "", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, nil, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, nil, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	cmd := reconcileTestCmd(t, f, lab)
	rows, _ := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0})

	require.Len(t, bondRec, 1, "the configured bond is created")
	assert.Equal(t, "bond0", bondRec[0].body["iface"])
	assert.NotContains(t, fmt.Sprintf("%v", rows), "bond/bridge phase skipped")
}

// TestHostnetReconcileNodes_UnreachableContextIsReportedAndReturned pins the
// distinction --require-context rests on. A lab context that cannot be built
// at all is a context failure: the rows still say "deferred" (the join or
// scale that called this has already succeeded, and nothing here may
// retroactively fail it), but the error return carries the failure so a
// caller that asked to be told can exit non-zero on it.
func TestHostnetReconcileNodes_UnreachableContextIsReportedAndReturned(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")
	lab.Name = "wayne"

	cmd := reconcileTestCmd(t, f, lab)
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		return nil, fmt.Errorf("no context lab-wayne")
	}

	rows, err := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0})

	require.Error(t, err, "an unreachable lab context must be returned, not only printed")
	assert.Contains(t, err.Error(), "lab-wayne")
	assert.Contains(t, fmt.Sprintf("%v", rows), "deferred",
		"the row stays a warning: the caller's own work already succeeded")
}

// TestHostnetReconcileNodes_NodeFailureIsNotAContextFailure is the other side
// of the same line. Node 0's 500 here comes back through a context that WAS
// reachable, so it describes host networking, not context registration, and
// --require-context must not promote it: a lab whose bond staging failed on
// one node still has a usable context.
func TestHostnetReconcileNodes_NodeFailureIsNotAContextFailure(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")
	lab.Name = "wayne"

	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network", nil, 500)

	cmd := reconcileTestCmd(t, f, lab)
	rows, err := hostnetReconcileNodes(context.Background(), cmd, cli.GetDeps(cmd), lab, []int{0})

	require.NoError(t, err)
	assert.Contains(t, fmt.Sprintf("%v", rows), "deferred")
}
