package lab

import (
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

// stubInnerAPIClient points labInnerAPIClient at f for the duration of the
// test: the outer-context verbs reach the nested cluster through it, and a
// test's fake PVE stands in for both.
func stubInnerAPIClient(t *testing.T, f *testhelper.FakePVE) {
	t.Helper()
	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		return apiclient.NewAPIClient(f.Options)
	}
}

// TestScale_ReconcilesNestedHostNetworkIPv6 closes the gap this round exists
// for: `pmx lab scale` used to finish a transition leaving every node's
// management IPv6 unset, because the address is written through the NESTED
// cluster's API while scale talks to the outer host. The transition must now
// stage and apply it for each member node itself.
//
// The fixture is TestScale_QdeviceAdd_ReRunWiresAlreadyExistingShell's (a
// 2-node lab whose QDevice shell exists but was never registered), so the
// only new thing under test is the nested host-network phase.
func TestScale_ReconcilesNestedHostNetworkIPv6(t *testing.T) {
	lab := scaleTestLab("wayne", 2, "auto")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9299, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	scaleGrowFixture(t, f, lab,
		[]map[string]any{
			{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200, "name": "lab-wayne-0"},
			{"id": "qemu/9201", "node": "node1", "type": "qemu", "vmid": 9201, "name": "lab-wayne-1"},
			{"id": "qemu/9299", "node": "node1", "type": "qemu", "vmid": 9299, "name": "lab-wayne-q"},
		},
		[]map[string]any{
			{"vmid": 9200, "name": "lab-wayne-0"},
			{"vmid": 9201, "name": "lab-wayne-1"},
			{"vmid": 9299, "name": "lab-wayne-q"},
		},
	)

	var updateRec, applyRec []hostnetRecordedRequest
	for _, node := range []string{"lab-wayne-0", "lab-wayne-1"} {
		hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/"+node+"/network",
			[]any{hostnetVmbr0Row(nil)}, 200)
		hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/"+node+"/network/vmbr0", nil, 200)
		hostnetRecord(f, &applyRec, nil, "", "PUT /api2/json/nodes/"+node+"/network", nil, 200)
	}
	stubInnerAPIClient(t, f)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2}, // 0: preflight membership
		exec.FakeResponse{}, // 1: zfs dataset probe
		exec.FakeResponse{}, // 2: qdevice reachable probe
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2}, // 3: qdevice add's cluster probe
		exec.FakeResponse{},           // 4: qnetd package probe
		exec.FakeResponse{Stdout: ""}, // 5: QDevice IPv6 probe
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"}, // 6: iface resolve
		exec.FakeResponse{},                                                      // 7: QDevice IPv6 apply
		exec.FakeResponse{},                                                      // 8: node 0 package probe
		exec.FakeResponse{},                                                      // 9: node 1 package probe
		exec.FakeResponse{},                                                      // 10: pvecm qdevice setup
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, exec.FakeResponse{}, // 11-13: sdn
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, // 14-15: nfs
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, // 16-17: nfs
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, // 18-19: nfs
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2}, // 20-25: final validation
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "2", "--node", "node1", "--yes")
	require.NoError(t, err)

	require.Len(t, updateRec, 2, "every member node's vmbr0 gets its planned IPv6 staged")
	require.Len(t, applyRec, 2, "and each node's staged changes applied")
	for i, rec := range updateRec {
		addr6, aerr := labNodeMgmtIP6(lab.Network, i)
		require.NoError(t, aerr)
		assert.Equal(t, fmt.Sprintf("%s/%d", addr6, labV6InterfacePrefixBits), rec.body["cidr6"])
	}
	assert.Contains(t, out, "lab-wayne-1")
}

// TestScale_IPv6Disabled_SkipsNestedHostNetwork pins the opt-out: a lab with
// `network.ipv6: false` and no nested bonds has nothing for the phase to do,
// so no nested-context client is ever built.
func TestScale_IPv6Disabled_SkipsNestedHostNetwork(t *testing.T) {
	lab := scaleTestLab("wayne", 2, "auto")
	off := false
	lab.Network.IPv6 = &off
	lab.Name = "wayne"

	called := false
	prev := labInnerAPIClient
	t.Cleanup(func() { labInnerAPIClient = prev })
	labInnerAPIClient = func(_ *cobra.Command, _ *cli.Deps, _ string) (*apiclient.APIClient, error) {
		called = true
		return nil, fmt.Errorf("must not be called")
	}

	assert.False(t, hostnetReconcileNeeded(lab.Network))
	assert.False(t, called)
}

// TestScaleReconcileQdeviceIPv6_EnsuresExistingQdevice covers the second
// scale gap: the QDevice's IPv6 rides `qdevice add`'s wiring, which a
// transition only runs when it ADDS one. A lab that already had its QDevice
// — every lab predating dual-stack support — must still converge.
func TestScaleReconcileQdeviceIPv6_EnsuresExistingQdevice(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	lab.Name = "wayne"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newScaleCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{},           // reachability probe
		exec.FakeResponse{Stdout: ""}, // IPv6 probe: nothing converged
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"}, // iface resolve
		exec.FakeResponse{}, // apply
	)
	deps := cli.GetDeps(cmd)
	deps.Runner = fake

	rows := scaleReconcileQdeviceIPv6(deps, lab, "wayne")
	require.Len(t, rows, 1)
	assert.Contains(t, rows[0][0], addr6)
	assert.Equal(t, "done", rows[0][1])
	require.Len(t, fake.Calls, 4)
	assert.Contains(t, fmt.Sprintf("%v", fake.Calls[3].Args),
		fmt.Sprintf("ip -6 addr replace %s/48 dev ens18", addr6))
}

// TestScaleReconcileQdeviceIPv6_UnreachableIsDeferredNotFatal pins the
// non-fatal contract for the QDevice half: a QDevice VM that is powered off
// or still provisioning must not fail a transition whose cluster work
// already succeeded.
func TestScaleReconcileQdeviceIPv6_UnreachableIsDeferredNotFatal(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	lab.Name = "wayne"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newScaleCmd())

	fake := exec.Fake(exec.FakeResponse{ExitCode: 255, Err: fmt.Errorf("connection refused")})
	deps := cli.GetDeps(cmd)
	deps.Runner = fake

	rows := scaleReconcileQdeviceIPv6(deps, lab, "wayne")
	require.Len(t, rows, 1)
	assert.Contains(t, rows[0][1], "deferred")
	assert.Contains(t, rows[0][1], "pmx lab qdevice add wayne")
}

// TestScale_DryRun_PreviewsIPv6Addressing pins the preview: a dry-run must
// name the real planned per-node address, so an operator sees the addressing
// before the transition runs.
func TestScale_DryRun_PreviewsIPv6Addressing(t *testing.T) {
	lab := scaleTestLab("wayne", 3, "never")
	lab.Name = "wayne"
	plan := buildScalePlan(2, false, 3, false)

	res := renderScalePlanPreview(lab, plan)
	flat := fmt.Sprintf("%v", res.Rows)
	for i := range 3 {
		addr6, err := labNodeMgmtIP6(lab.Network, i)
		require.NoError(t, err)
		assert.Contains(t, flat, addr6, "node %d's planned management IPv6 is previewed", i)
	}
}

// TestScale_DryRun_IPv4OnlyLabPreviewsNoIPv6 is the opt-out's preview half.
func TestScale_DryRun_IPv4OnlyLabPreviewsNoIPv6(t *testing.T) {
	lab := scaleTestLab("wayne", 3, "never")
	lab.Name = "wayne"
	off := false
	lab.Network.IPv6 = &off

	res := renderScalePlanPreview(lab, buildScalePlan(2, false, 3, false))
	assert.NotContains(t, fmt.Sprintf("%v", res.Rows), "IPv6")
}
