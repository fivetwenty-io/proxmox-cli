package lab

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// hostnetTestLab returns a Lab definition fully populated for `pmx lab
// hostnet apply` tests: a clean (non-peppi) baseline (cleanLab) plus a
// resolvable mgmt subnet (hostnetEnsureNICNaming's ssh phase needs a node
// mgmt IP, exactly like guestssh_test.go's multiNodeTestLab convention) plus
// one nested_network bond/bridge pair (bond0/vmbr0, active-backup, 2 nics).
func hostnetTestLab(name string) *config.Lab {
	lab := cleanLab(name)
	lab.Network.Mgmt = config.LabMgmt{Subnet: "10.10.1.0/24", Gateway: "10.10.1.1"}
	lab.Network.NestedNetwork = config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{
				Name:    "bond0",
				NICs:    []string{"nic0", "nic1"},
				Mode:    config.NestedBondModeActiveBackup,
				Primary: "nic0",
				Bridge:  "vmbr0",
			},
		},
	}
	return lab
}

// hostnetFakeNICEnumerationLines builds hostnetNICEnumerateCmd-shaped output
// (one tab-separated NAME\tMAC\tDEVPATH line per NIC) for count physical
// NICs, in the given input order. Each NIC i's device path carries a PCI
// function sort key ("0000:00:1c.<hex-of-i>") that places it at sorted
// position i regardless of the order these lines are emitted in or what
// virtio ordinal its path happens to carry (real virtio sysfs shape:
// ".../0000:00:1c.<fn>/virtio<ordinal>"); its MAC is
// "52:54:00:00:00:0<hex-of-i>". name(i) supplies each line's
// kernel-reported NAME field for sorted position i.
func hostnetFakeNICEnumerationLines(count int, name func(i int) string) string {
	var b strings.Builder
	for i := 0; i < count; i++ {
		mac := fmt.Sprintf("52:54:00:00:00:%02x", i)
		devpath := fmt.Sprintf("/sys/devices/pci0000:00/0000:00:1c.%x/virtio%d", i, i)
		fmt.Fprintf(&b, "%s\t%s\t%s\n", name(i), mac, devpath)
	}
	return b.String()
}

// hostnetFakeAlreadyNamedEnumeration returns hostnetNICEnumerateCmd-shaped
// output for a node whose hostnetRequiredNICCount physical NICs are already
// named nic0-nic5 in PCI order — hostnetEnsureNICNaming's no-op path.
func hostnetFakeAlreadyNamedEnumeration() string {
	return hostnetFakeNICEnumerationLines(hostnetRequiredNICCount, hostnetNICName)
}

// hostnetFakePredictableNamedEnumeration returns hostnetNICEnumerateCmd-
// shaped output for a freshly installed node whose hostnetRequiredNICCount
// physical NICs still carry systemd predictable names (ens18-ens23, the
// real shape for the q35 net0-net5 slots `pmx lab create` produces) —
// hostnetEnsureNICNaming's rename path.
func hostnetFakePredictableNamedEnumeration() string {
	return hostnetFakeNICEnumerationLines(hostnetRequiredNICCount, func(i int) string {
		return fmt.Sprintf("ens%d", 18+i)
	})
}

// hostnetAlreadyNamedResponse is one exec.FakeResponse standing in for a
// single node's hostnetNICEnumerateCmd ssh call reporting NICs already
// named nic0-nic5 — the fixture every bond/bridge-focused test in this file
// uses to keep the NIC-naming phase a no-op so its own assertions (about
// the API create/update/apply calls below it) are unaffected by it.
func hostnetAlreadyNamedResponse() exec.FakeResponse {
	return exec.FakeResponse{Stdout: hostnetFakeAlreadyNamedEnumeration()}
}

// hostnetRecordedRequest captures one HTTP request hostnet.go issued to the
// fake PVE server, decoding a form-urlencoded body into a plain string map
// so assertions can read field values directly (CreateNetwork/UpdateNetwork2
// both send form-encoded bodies, mirroring net_test.go's netRecordedRequest
// convention for the same class of generated-client call).
type hostnetRecordedRequest struct {
	method string
	path   string
	body   map[string]any
}

// hostnetRecord installs a handler on f for pattern that appends every
// matching request to *rec (also appending label to *order, when non-nil)
// and replies with payload, or a PVE-shaped error when status is >= 400.
func hostnetRecord(f *testhelper.FakePVE, rec *[]hostnetRecordedRequest, order *[]string, label, pattern string, payload any, status int) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if err := r.ParseForm(); err == nil {
			for k, v := range r.PostForm {
				if len(v) > 0 {
					body[k] = v[0]
				}
			}
		}
		if rec != nil {
			*rec = append(*rec, hostnetRecordedRequest{method: r.Method, path: r.URL.Path, body: body})
		}
		if order != nil {
			*order = append(*order, label)
		}
		if status >= 400 {
			testhelper.WriteError(w, status, "boom")
			return
		}
		testhelper.WriteData(w, payload)
	})
}

// buildHostnetCmd constructs `pmx lab hostnet` wired to a *cli.Deps pointed
// at f (the outer PVE API, for the bond/bridge phase) and fake (the ssh
// transport, for the NIC-naming ensure phase — hostnetEnsureNICNaming) under
// the given active context name, bypassing PersistentPreRunE via
// cli.WithDeps (the supported mechanism for group-package tests; see
// net_test.go's buildNetCmd for the established convention this mirrors,
// and guestssh_test.go's buildGuestSSHCmd for the ssh-context-wiring half).
// deps.Ctx is always populated (even for tests that never reach the ssh
// phase, e.g. the wrong-context/no-bonds-configured cases) since it costs
// nothing and keeps every test's *cli.Deps shape uniform.
func buildHostnetCmd(t *testing.T, configPath string, f *testhelper.FakePVE, ctxName string, fake *exec.FakeRunner) *cobra.Command {
	t.Helper()

	cfg, err := config.Load(configPath)
	require.NoError(t, err)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)

	deps := &cli.Deps{
		Cfg:        cfg,
		ConfigPath: configPath,
		API:        api,
		Out:        output.New(),
		Format:     output.FormatJSON,
		CtxName:    ctxName,
		Runner:     fake,
		Ctx: &config.Context{
			Host: "10.10.1.10",
			SSH:  config.SSHBlock{User: "root", Port: 22},
		},
	}

	cmd := newHostnetCmd()
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	return cmd
}

// runHostnetCmd executes cmd with args, capturing combined stdout/stderr.
func runHostnetCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

// TestHostnetApply_WrongContext_Refuses covers the context guard: hostnet
// apply must refuse before issuing any API call when the active context is
// not the lab's own lab-<name> context.
func TestHostnetApply_WrongContext_Refuses(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake()
	cmd := buildHostnetCmd(t, path, f, "some-other-context", fake)

	_, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, `lab-wayne`)
	assert.ErrorContains(t, err, `some-other-context`)
	assert.Empty(t, fake.Calls, "wrong-context refusal must issue zero ssh calls")
}

// TestHostnetApply_NoBondsConfigured_NoOp covers the zero-value shape: a lab
// with no network.nested_network.bonds is a no-op with a notice, issuing no
// API call at all (not even under the correct context).
func TestHostnetApply_NoBondsConfigured_NoOp(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake()
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "nothing to do")
	assert.Empty(t, fake.Calls, "a lab with no bonds configured must issue zero ssh calls (naming phase never runs)")
}

// TestHostnetApply_DryRun_PreviewsWithoutAnyAPICall covers --dry-run: it
// must never touch deps.API nor deps.Runner (zero ssh calls, including for
// the NIC-naming ensure phase), previewing every step it would take instead.
func TestHostnetApply_DryRun_PreviewsWithoutAnyAPICall(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake()
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "lab-wayne-0")
	assert.Contains(t, out, "NIC naming")
	assert.Contains(t, out, `bond0`)
	assert.Contains(t, out, `vmbr0`)
	assert.Contains(t, out, "would run")
	assert.Empty(t, fake.Calls, "--dry-run must issue zero ssh calls")
}

// TestHostnetApply_BondAndBridgeAbsent_CreatesBoth covers the fresh-node
// case: neither the bond nor the bridge exists yet, so both must be created
// (in that order — the bridge names the bond as its port), then the node's
// staged changes applied.
func TestHostnetApply_BondAndBridgeAbsent_CreatesBoth(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	var order []string
	var listRec, createRec, applyRec []hostnetRecordedRequest

	hostnetRecord(f, &listRec, &order, "list", "GET /api2/json/nodes/lab-wayne-0/network", []any{}, 200)
	hostnetRecord(f, &createRec, &order, "create", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, &applyRec, &order, "apply", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, createRec, 2, "must create exactly the bond and the bridge")
	assert.Equal(t, "bond0", createRec[0].body["iface"])
	assert.Equal(t, "bond", createRec[0].body["type"])
	assert.Equal(t, "nic0 nic1", createRec[0].body["slaves"])
	assert.Equal(t, "active-backup", createRec[0].body["bond_mode"])
	assert.Equal(t, "nic0", createRec[0].body["bond-primary"])
	assert.Equal(t, "1", createRec[0].body["autostart"], "a freshly-created bond must autostart (az2 parity)")

	assert.Equal(t, "vmbr0", createRec[1].body["iface"])
	assert.Equal(t, "bridge", createRec[1].body["type"])
	assert.Equal(t, "bond0", createRec[1].body["bridge_ports"])
	assert.Equal(t, "1", createRec[1].body["autostart"], "a freshly-created bridge must autostart (az2 parity)")

	require.Len(t, applyRec, 1, "must apply the node's staged changes exactly once")

	assert.Contains(t, out, "created")
	assert.Contains(t, out, "applied")
}

// TestHostnetApply_BondPresentBridgeMissing_CreatesBridgeOnly covers the
// partially-converged case: the bond already matches config exactly, so only
// the bridge must be created — the bond must receive no update call.
func TestHostnetApply_BondPresentBridgeMissing_CreatesBridgeOnly(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	var order []string
	var createRec, updateBondRec, applyRec []hostnetRecordedRequest

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		// autostart already 1 (az2-converged parity): the bond must stay
		// "already matches" and receive no update call at all — this is
		// the az2-shape regression guard for the autostart-drift check.
		{"iface": "bond0", "type": "bond", "slaves": "nic0 nic1", "bond_mode": "active-backup", "bond-primary": "nic0", "autostart": 1},
	})
	hostnetRecord(f, &createRec, &order, "create", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, &updateBondRec, &order, "update-bond0", "PUT /api2/json/nodes/lab-wayne-0/network/bond0", nil, 200)
	hostnetRecord(f, &applyRec, &order, "apply", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, createRec, 1, "must create only the bridge")
	assert.Equal(t, "vmbr0", createRec[0].body["iface"])
	assert.Equal(t, "bridge", createRec[0].body["type"])
	assert.Equal(t, "1", createRec[0].body["autostart"])

	assert.Empty(t, updateBondRec, "an already-matching (incl. autostart=1) bond must receive no update call")
	require.Len(t, applyRec, 1)

	assert.Contains(t, out, "already matches")
	assert.Contains(t, out, "created")
}

// TestHostnetApply_FullyConverged_NoOp covers the already-converged case:
// both the bond and the bridge already match config exactly, so no
// create/update call and no apply call must be issued at all.
func TestHostnetApply_FullyConverged_NoOp(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		// autostart already 1 on both (az2-converged parity): a pure no-op
		// run must issue zero calls at all — the az2-shape regression guard
		// for the autostart-drift check.
		{"iface": "bond0", "type": "bond", "slaves": "nic0 nic1", "bond_mode": "active-backup", "bond-primary": "nic0", "autostart": 1},
		{"iface": "vmbr0", "type": "bridge", "bridge_ports": "bond0", "bridge_vlan_aware": 0, "autostart": 1},
	})
	// Deliberately no POST/PUT (other than the list GET) route is
	// registered: any create/update/apply call this run should not make
	// would hit FakePVE's 404 fallback and surface as an error, failing the
	// require.NoError below.

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "already named nic0-nic5")
	assert.Contains(t, out, "already matches")
	assert.Contains(t, out, "skip (no pending changes)")
	require.Len(t, fake.Calls, 1,
		"an already-converged node (nic names everywhere) must issue only the enumeration call — "+
			"no .link write, no interfaces rewrite, no update-initramfs")
}

// TestHostnetApply_MultiNode_ReconcilesEveryNode covers a multi-node lab:
// every node index 0..topology.nodes-1 must be reconciled independently,
// against its own node name.
func TestHostnetApply_MultiNode_ReconcilesEveryNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	lab.Topology = config.LabTopology{Nodes: 3, Qdevice: config.QdeviceNever}

	var order []string
	var createRec, applyRec []hostnetRecordedRequest
	for _, idx := range []string{"0", "1", "2"} {
		nodeName := "lab-wayne-" + idx
		hostnetRecord(f, nil, nil, "list-"+idx, "GET /api2/json/nodes/"+nodeName+"/network", []any{}, 200)
		hostnetRecord(f, &createRec, &order, "create-"+idx, "POST /api2/json/nodes/"+nodeName+"/network", nil, 200)
		hostnetRecord(f, &applyRec, &order, "apply-"+idx, "PUT /api2/json/nodes/"+nodeName+"/network", nil, 200)
	}

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	// One already-named-nic0-nic5 enumeration response per node, consumed in
	// node-index order: the NIC-naming ensure phase must be a no-op for
	// every node so this test's own bond/bridge-focused assertions below are
	// unaffected by it.
	fake := exec.Fake(hostnetAlreadyNamedResponse(), hostnetAlreadyNamedResponse(), hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, createRec, 6, "2 creates (bond+bridge) per node across 3 nodes")
	require.Len(t, applyRec, 3, "one apply call per node")
	assert.Contains(t, out, "lab-wayne-0")
	assert.Contains(t, out, "lab-wayne-1")
	assert.Contains(t, out, "lab-wayne-2")
}

// TestHostnetApply_ExistingNonBondInterface_Refuses covers the
// non-overwrite guarantee: an interface already present under a configured
// bond's name, but of a different type, must refuse rather than silently
// repurpose it.
func TestHostnetApply_ExistingNonBondInterface_Refuses(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		{"iface": "bond0", "type": "eth"},
	})

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	_, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, `"bond0"`)
	assert.ErrorContains(t, err, "not bond")
}

// --- NIC-naming ensure phase -----------------------------------------------

// TestHostnetApply_FreshPredictableNames_WritesLinkFilesAndStopsForReboot
// covers a freshly installed node whose physical NICs still carry systemd
// predictable names (ens18-ens23): the naming phase must write one
// MAC-matched .link file per NIC, refresh the initramfs, mark the node
// reboot-required, issue NO bond/bridge API calls for it at all, and the
// command must exit nonzero with an actionable message.
func TestHostnetApply_FreshPredictableNames_WritesLinkFilesAndStopsForReboot(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	// Deliberately no network route registered at all: if hostnet apply
	// issued any bond/bridge API call for this node, it would hit FakePVE's
	// 404 fallback and the test would fail well before its own assertions
	// run.

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		exec.FakeResponse{Stdout: hostnetFakePredictableNamedEnumeration()},
		// Simulates the remote rewrite script finding a stale reference (the
		// live az1 shape: /etc/network/interfaces' bridge-ports still names
		// ens18) and reporting it rewritten.
		exec.FakeResponse{Stdout: hostnetRewrittenPrefix + hostnetInterfacesFile + "\n"},
	)
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err, "a node left reboot-pending must make the overall command exit nonzero")
	assert.ErrorContains(t, err, "reboot")
	assert.ErrorContains(t, err, "hostnet apply wayne")

	require.Len(t, fake.Calls, 2,
		"one enumeration call, one .link-write+interfaces-rewrite+update-initramfs call")
	assert.Equal(t, "ssh", fake.Calls[0].Name)
	assert.Contains(t, strings.Join(fake.Calls[0].Args, " "), "readlink -f")

	renameArgv := strings.Join(fake.Calls[1].Args, " ")
	assert.Contains(t, renameArgv, "update-initramfs -u")
	assert.Contains(t, renameArgv, "/etc/systemd/network/10-lab-nic0.link")
	assert.Contains(t, renameArgv, "/etc/systemd/network/10-lab-nic5.link")
	assert.Contains(t, renameArgv, "Name=nic0")
	assert.Contains(t, renameArgv, "Name=nic5")
	assert.Contains(t, renameArgv, "MACAddress=52:54:00:00:00:00")
	assert.Contains(t, renameArgv, "MACAddress=52:54:00:00:00:05")
	// The interfaces-rewrite phase (fresh node -> rewrite + rename together):
	// stale-detection, timestamped backup, and in-place rewrite for BOTH
	// hostnetInterfacesFile and every file under hostnetInterfacesDDir, run
	// in the exact same composite call as the .link writes above.
	assert.Contains(t, renameArgv, "PMXNICTS=$(date -u +%Y%m%dT%H%M%SZ)")
	assert.Contains(t, renameArgv, "pmx_rewrite_stale_refs "+hostnetInterfacesFile+
		" \""+hostnetInterfacesFile+"."+hostnetInterfacesBackupInfix+".$PMXNICTS\"")
	assert.Contains(t, renameArgv, "if [ -d "+hostnetInterfacesDDir+" ]; then")
	assert.Contains(t, renameArgv, "grep -Eq")
	assert.Contains(t, renameArgv, "cp -p \"$f\" \"$bak\"")
	assert.Contains(t, renameArgv, "sed -E -i")
	// interfaces.d backups must be routed OUTSIDE hostnetInterfacesDDir
	// (hostnetInterfacesDBackupDir) — never a same-directory sibling of the
	// file being backed up — since hostnetInterfacesFile globs everything
	// under hostnetInterfacesDDir via `source .../interfaces.d/*`; a
	// same-directory backup would get sourced as live config AND get
	// re-picked-up (and re-backed-up) by this very script's own
	// interfaces.d loop on the next run, breaking idempotency.
	assert.Contains(t, renameArgv, hostnetInterfacesDBackupDir+"/$(basename \"$f\").")
	assert.NotContains(t, renameArgv, hostnetInterfacesDDir+"/*.pre-nic-rename")
	// update-initramfs must run AFTER the interfaces rewrite, not before —
	// the initramfs refresh is the final step of the whole composite call.
	assert.Less(t,
		strings.Index(renameArgv, "pmx_rewrite_stale_refs "+hostnetInterfacesFile),
		strings.Index(renameArgv, "update-initramfs -u"))

	assert.Contains(t, out, "REBOOT REQUIRED")
	assert.Contains(t, out, "rewrote stale interface-name references in: "+hostnetInterfacesFile)
	assert.NotContains(t, out, "already named")
}

// TestHostnetApply_IntermediateState_RerunStaysRebootPendingAndReconverges
// covers a node already left in the "links written, interfaces still
// stale, reboot still pending" state by a PRIOR run — exactly az1's live
// post-naming-phase, pre-reboot shape. Since no reboot has happened between
// runs, the node's kernel-reported NIC names are UNCHANGED from the prior
// run, so a re-run must reach the identical outcome (reboot-pending, not an
// error, not a false "already named" no-op): the naming phase has no state
// of its own beyond what it can currently observe over ssh (live
// enumeration + live file content), so idempotent reconvergence falls out
// of running the exact same steps again — including a NO-OP interfaces
// rewrite on the second run, since the first run's rewrite already left
// the file correct.
func TestHostnetApply_IntermediateState_RerunStaysRebootPendingAndReconverges(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	// Deliberately no network route registered: neither run may ever reach
	// the bond/bridge phase.

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		// Run 1 (the state left behind by the ORIGINAL run this test opens
		// in): enumeration still reports pre-rename kernel names, and the
		// rewrite step finds and fixes a stale interfaces reference.
		exec.FakeResponse{Stdout: hostnetFakePredictableNamedEnumeration()},
		exec.FakeResponse{Stdout: hostnetRewrittenPrefix + hostnetInterfacesFile + "\n"},
		// Run 2 (operator re-runs before ever rebooting): enumeration is
		// IDENTICAL — no reboot happened, so the kernel still reports
		// ens18-ens23 — but the interfaces file is now already correct, so
		// this run's rewrite step is a no-op (no REWRITTEN line).
		exec.FakeResponse{Stdout: hostnetFakePredictableNamedEnumeration()},
		exec.FakeResponse{Stdout: ""},
	)

	cmd1 := buildHostnetCmd(t, path, f, "lab-wayne", fake)
	out1, err1 := runHostnetCmd(t, cmd1, "apply", "wayne")
	require.Error(t, err1)
	assert.ErrorContains(t, err1, "reboot")
	assert.Contains(t, out1, "REBOOT REQUIRED")
	assert.Contains(t, out1, "rewrote stale interface-name references")

	// Same *exec.FakeRunner (fake), a fresh *cobra.Command — mirrors a
	// distinct operator invocation of `pmx lab hostnet apply wayne` while
	// continuing to consume fake's FIFO response queue at run 2's entries.
	cmd2 := buildHostnetCmd(t, path, f, "lab-wayne", fake)
	out2, err2 := runHostnetCmd(t, cmd2, "apply", "wayne")
	require.Error(t, err2, "still reboot-pending on the second run: no reboot happened between runs")
	assert.ErrorContains(t, err2, "reboot")
	assert.Contains(t, out2, "REBOOT REQUIRED")
	assert.Contains(t, out2, "no stale interface-name references found")

	require.Len(t, fake.Calls, 4, "2 ssh calls per run x 2 runs — no bond/bridge calls in either run")
}

// TestHostnetStaleRefPattern_WordBoundary proves hostnetStaleRefPattern's
// word-boundary match directly against Go's regexp engine, using the exact
// same literal pattern text hostnetBuildInterfacesRewriteScript embeds into
// the remote grep -E/sed -E commands (see that function's doc comment for
// why this is a faithful proof, not merely an analogous one).
func TestHostnetStaleRefPattern_WordBoundary(t *testing.T) {
	cases := []struct {
		name string
		line string
		old  string
		new  string
		want string
	}{
		{
			name: "bridge-ports value",
			line: "\tbridge-ports ens18",
			old:  "ens18", new: "nic0",
			want: "\tbridge-ports nic0",
		},
		{
			name: "iface stanza head",
			line: "iface ens18 inet manual",
			old:  "ens18", new: "nic0",
			want: "iface nic0 inet manual",
		},
		{
			name: "bond-slaves list, first token only",
			line: "\tbond-slaves ens18 ens19",
			old:  "ens18", new: "nic0",
			want: "\tbond-slaves nic0 ens19",
		},
		{
			name: "bond-slaves list, second token only",
			line: "\tbond-slaves ens18 ens19",
			old:  "ens19", new: "nic1",
			want: "\tbond-slaves ens18 nic1",
		},
		{
			name: "superstring suffix must NOT be touched",
			line: "iface ens180 inet manual",
			old:  "ens18", new: "nic0",
			want: "iface ens180 inet manual",
		},
		{
			name: "superstring prefix must NOT be touched",
			line: "myens18if ok",
			old:  "ens18", new: "nic0",
			want: "myens18if ok",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			re := regexp.MustCompile(hostnetStaleRefPattern(c.old))
			got := re.ReplaceAllString(c.line, "${1}"+c.new+"${2}")
			assert.Equal(t, c.want, got)
		})
	}
}

// TestHostnetBuildInterfacesRewriteScript_Shape covers
// hostnetBuildInterfacesRewriteScript's own structural contract: an empty
// pairs list produces no script at all (hostnetBuildNICRenameCmd embeds
// nothing extra when no NIC actually needs renaming), and a non-empty one
// covers both hostnetInterfacesFile and every file under
// hostnetInterfacesDDir, with stale-detection, a timestamped backup, and
// an in-place rewrite for each pair.
func TestHostnetBuildInterfacesRewriteScript_Shape(t *testing.T) {
	t.Run("empty pairs produces no script", func(t *testing.T) {
		assert.Empty(t, hostnetBuildInterfacesRewriteScript(nil))
	})

	t.Run("non-empty pairs cover interfaces, interfaces.d, backup, and rewrite", func(t *testing.T) {
		pairs := []hostnetRenamePair{{Old: "ens18", New: "nic0"}, {Old: "ens19", New: "nic1"}}
		script := hostnetBuildInterfacesRewriteScript(pairs)

		assert.Contains(t, script, "PMXNICTS=$(date -u +%Y%m%dT%H%M%SZ)")
		assert.Contains(t, script, "pmx_rewrite_stale_refs "+hostnetInterfacesFile+
			" \""+hostnetInterfacesFile+"."+hostnetInterfacesBackupInfix+".$PMXNICTS\"")
		assert.Contains(t, script, "if [ -d "+hostnetInterfacesDDir+" ]; then")
		assert.Contains(t, script, hostnetInterfacesDDir+"/*")
		assert.Contains(t, script, "grep -Eq")
		assert.Contains(t, script, "cp -p \"$f\" \"$bak\"")
		assert.Contains(t, script, "mkdir -p \"$(dirname \"$bak\")\"")
		assert.Contains(t, script, "sed -E -i")
		assert.Contains(t, script, hostnetRewrittenPrefix+"$f")
		// One grep/sed block per pair, each keyed on its own OLD name.
		assert.Contains(t, script, "ens18")
		assert.Contains(t, script, "ens19")
	})

	// This subtest locks in the fix for a real, confirmed idempotency-and-
	// glob-hazard defect found while verifying this phase against a live
	// Debian container: hostnetInterfacesFile globs and `source`s every
	// file directly under hostnetInterfacesDDir. A backup written AS A
	// SIBLING of an interfaces.d file (e.g. "extra.pre-nic-rename.<ts>"
	// sitting next to "extra") would therefore (a) itself get sourced by
	// ifupdown as live config, and (b) — confirmed live — get picked back
	// up by this script's own `for f in .../interfaces.d/*` loop on the
	// VERY NEXT run (it still contains the pre-rewrite stale name),
	// producing a new backup of the backup every re-run instead of a
	// stable no-op. Every interfaces.d backup must therefore land in
	// hostnetInterfacesDBackupDir, a sibling of hostnetInterfacesDDir
	// itself, never inside it.
	t.Run("interfaces.d backups stay outside interfaces.d itself", func(t *testing.T) {
		pairs := []hostnetRenamePair{{Old: "ens18", New: "nic0"}}
		script := hostnetBuildInterfacesRewriteScript(pairs)

		assert.Contains(t, script, hostnetInterfacesDBackupDir+"/$(basename \"$f\").")
		assert.NotContains(t, script, "\"$f."+hostnetInterfacesBackupInfix,
			"no backup expression may be a same-directory sibling of the file it backs up "+
				"(hostnetInterfacesFile's own call site spells its target out literally, "+
				"never via \"$f.<infix>\", so this only ever matches an unwanted "+
				"same-directory interfaces.d backup)")
		require.False(t, strings.HasPrefix(hostnetInterfacesDBackupDir, hostnetInterfacesDDir+"/"),
			"hostnetInterfacesDBackupDir must not be a subdirectory of hostnetInterfacesDDir — "+
				"it would still be glob-matched by hostnetInterfacesDDir+\"/*\" and by "+
				"hostnetInterfacesFile's own `source .../interfaces.d/*` directive")
	})
}

// TestHostnetApply_WrongNICCount_FailsLoudly covers the pre-reconcile shape
// (e.g. lab pve-cpi's az1 nodes before their 5 additional NICs are added
// live): a node reporting anything other than exactly 6 physical NICs must
// fail loudly, naming what was found and the expectation, and must issue no
// further ssh or API calls.
func TestHostnetApply_WrongNICCount_FailsLoudly(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(exec.FakeResponse{Stdout: hostnetFakeNICEnumerationLines(1, func(int) string { return "ens18" })})
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	_, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "expected exactly 6 physical NICs")
	assert.ErrorContains(t, err, "found 1")
	assert.ErrorContains(t, err, "ens18")
	require.Len(t, fake.Calls, 1, "must stop after the enumeration call — no .link writes, no bond/bridge calls")
}

// TestHostnetApply_MixedCluster_Node0AppliesNode1RebootPending covers a
// 2-node lab where node 0's NICs are already named nic0-nic5 but node 1's
// are still ens18-ens23: node 0 must fully apply its bond/bridge config,
// node 1 must be left reboot-pending with no bond/bridge calls issued for
// it, and the overall command must exit nonzero.
func TestHostnetApply_MixedCluster_Node0AppliesNode1RebootPending(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")
	lab.Topology = config.LabTopology{Nodes: 2, Qdevice: config.QdeviceAuto}

	var createRec, applyRec []hostnetRecordedRequest
	hostnetRecord(f, nil, nil, "list-0", "GET /api2/json/nodes/lab-wayne-0/network", []any{}, 200)
	hostnetRecord(f, &createRec, nil, "create-0", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, &applyRec, nil, "apply-0", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)
	// Deliberately no route registered for lab-wayne-1: it must never be
	// called, since node 1 is left reboot-pending before the bond/bridge
	// phase ever runs for it.

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		hostnetAlreadyNamedResponse(),                                       // node 0 enumeration: already named
		exec.FakeResponse{Stdout: hostnetFakePredictableNamedEnumeration()}, // node 1 enumeration: fresh
		exec.FakeResponse{},                                                 // node 1 .link-write+update-initramfs
	)
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err, "node 1 left reboot-pending must make the overall command exit nonzero")
	assert.ErrorContains(t, err, "reboot")

	require.Len(t, createRec, 2, "node 0's bond+bridge must both be created")
	require.Len(t, applyRec, 1, "node 0's staged changes must be applied exactly once")
	require.Len(t, fake.Calls, 3, "node 0 enum + node 1 enum + node 1 rename — node 1 gets no further ssh call")

	assert.Contains(t, out, "lab-wayne-0")
	assert.Contains(t, out, "created")
	assert.Contains(t, out, "applied")
	assert.Contains(t, out, "lab-wayne-1")
	assert.Contains(t, out, "REBOOT REQUIRED")
}

// TestHostnetSortNICs_OrdersByLastPCIFunction_NotByVirtioOrdinal proves
// hostnetSortNICs' ordering key is the last PCI bus:device.function segment
// of each NIC's resolved device path, not the trailing virtioN ordinal that
// segment is often followed by — the sort must still come out correct even
// when the virtio ordinals are assigned in an order unrelated to (and, for
// one NIC, exceeding 9, where a lexical string sort of "virtioN" would
// mis-order it before "virtio2") the intended net0..net5 position.
func TestHostnetSortNICs_OrdersByLastPCIFunction_NotByVirtioOrdinal(t *testing.T) {
	// virtioOrdinals[i] is deliberately shuffled and includes >9 values, with
	// zero correlation to i (the intended sorted position) — only each
	// entry's PCI function ("0000:06:10.<hex-of-i>") correlates to i.
	virtioOrdinals := []int{15, 3, 10, 1, 22, 7}

	var input []hostnetPhysicalNIC
	// Built in REVERSE index order too, so neither input order nor virtio
	// ordinal order happens to already match the correct output order.
	for i := len(virtioOrdinals) - 1; i >= 0; i-- {
		devpath := fmt.Sprintf(
			"/sys/devices/pci0000:00/0000:00:1c.4/0000:06:10.%x/virtio%d", i, virtioOrdinals[i])
		input = append(input, hostnetPhysicalNIC{
			Name:    fmt.Sprintf("kernelname%d", i),
			MAC:     fmt.Sprintf("52:54:00:00:01:%02x", i),
			DevPath: devpath,
		})
	}

	sorted := hostnetSortNICs(input)
	require.Len(t, sorted, len(virtioOrdinals))
	for i, n := range sorted {
		assert.Equal(t, fmt.Sprintf("kernelname%d", i), n.Name,
			"sorted position %d must be the NIC whose PCI function is 0000:06:10.%x, regardless of its virtio ordinal", i, i)
	}
}

// TestHostnetSortNICs_SkipsUnresolvableDevicePath covers
// scripts/first-boot-network.sh.tmpl's own `[ -n "$pci" ] || continue`
// filter: a NIC whose device path contains no PCI-function-shaped segment
// at all must be excluded from both the ordering and (via
// hostnetEnsureNICNaming's len(sorted) preflight) the count, not treated as
// a malformed/error input.
func TestHostnetSortNICs_SkipsUnresolvableDevicePath(t *testing.T) {
	input := []hostnetPhysicalNIC{
		{Name: "nic0", MAC: "52:54:00:00:00:00", DevPath: "/sys/devices/pci0000:00/0000:00:1c.0/virtio0"},
		{Name: "veth-nopci", MAC: "52:54:00:00:00:aa", DevPath: "/sys/devices/virtual/net/veth-nopci"},
	}
	sorted := hostnetSortNICs(input)
	require.Len(t, sorted, 1)
	assert.Equal(t, "nic0", sorted[0].Name)
}

// TestHostnetPreserveUntouchedBridgeFields covers
// hostnetPreserveUntouchedBridgeFields's own field-by-field contract
// directly: every existing addressing/autostart/vlan-aware field cur has a
// value for gets copied onto a still-nil params field; an already-set
// params field (the caller's own opinion for THIS call) is never
// overwritten; and a field cur has NO value for is left nil on params
// (never sent as an explicit empty string, which — unlike nil — a
// `*string` with `omitempty` still serializes, incorrectly asserting "no
// value" onto a field that was simply never touched by this package).
func TestHostnetPreserveUntouchedBridgeFields(t *testing.T) {
	t.Run("copies every populated field onto a bare params", func(t *testing.T) {
		cur := hostnetIfaceState{
			Cidr: "10.254.0.10/24", Address: "10.254.0.10/24", Netmask: "24",
			Gateway: "10.254.0.1", Autostart: 1, BridgeVlanAware: 1,
		}
		params := &nodes.UpdateNetwork2Params{Type: "bridge"}
		hostnetPreserveUntouchedBridgeFields(cur, params)

		require.NotNil(t, params.Cidr)
		assert.Equal(t, "10.254.0.10/24", *params.Cidr)
		require.NotNil(t, params.Address)
		assert.Equal(t, "10.254.0.10/24", *params.Address)
		require.NotNil(t, params.Netmask)
		assert.Equal(t, "24", *params.Netmask)
		require.NotNil(t, params.Gateway)
		assert.Equal(t, "10.254.0.1", *params.Gateway)
		require.NotNil(t, params.Autostart)
		assert.True(t, *params.Autostart)
		require.NotNil(t, params.BridgeVlanAware)
		assert.True(t, *params.BridgeVlanAware)
	})

	t.Run("never overwrites a field the caller already set an opinion on", func(t *testing.T) {
		cur := hostnetIfaceState{Gateway: "10.254.0.1", Autostart: 1, BridgeVlanAware: 1}
		params := &nodes.UpdateNetwork2Params{
			Type: "bridge", Gateway: netPtr("10.254.0.99"), Autostart: netPtr(false), BridgeVlanAware: netPtr(false),
		}
		hostnetPreserveUntouchedBridgeFields(cur, params)

		require.NotNil(t, params.Gateway)
		assert.Equal(t, "10.254.0.99", *params.Gateway, "caller's own gateway opinion must survive untouched")
		require.NotNil(t, params.Autostart)
		assert.False(t, *params.Autostart, "caller's own autostart opinion must survive untouched")
		require.NotNil(t, params.BridgeVlanAware)
		assert.False(t, *params.BridgeVlanAware, "caller's own vlan_aware opinion must survive untouched")
	})

	t.Run("leaves an unset field nil, never an explicit empty string", func(t *testing.T) {
		cur := hostnetIfaceState{} // deliberately address-less/autostart-unset/vlan-unaware
		params := &nodes.UpdateNetwork2Params{Type: "bridge"}
		hostnetPreserveUntouchedBridgeFields(cur, params)

		assert.Nil(t, params.Cidr)
		assert.Nil(t, params.Address)
		assert.Nil(t, params.Netmask)
		assert.Nil(t, params.Gateway)
		assert.Nil(t, params.Autostart, "cur.Autostart==0 must NOT be actively preserved as false — that is "+
			"the caller's own drift-fix's job, not this helper's")
		assert.Nil(t, params.BridgeVlanAware)
	})
}

// --- retrofit bond/bridge restage (bridge already holds a future bond's slave nic) ---

// hostnetTestAPIClient builds a bare *apiclient.APIClient pointed at f, for
// tests exercising hostnetRestageBridgeIfSlaveConflict directly rather than
// through the full `hostnet apply` command pipeline.
func hostnetTestAPIClient(t *testing.T, f *testhelper.FakePVE) *apiclient.APIClient {
	t.Helper()
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	return api
}

// hostnetMultiBondTestLab returns a 3-bond lab (bond0/vmbr0 mgmt,
// bond1/vmbr1 storage, bond2/vmbr2 workload+VLAN-aware) — the same 3-bond
// shape lab pve-cpi actually runs, used by the retrofit convergence-state
// tests below (TestHostnetApply_Retrofit*) to prove every bond/bridge pair
// converges correctly regardless of what state any of the OTHER pairs are
// in.
func hostnetMultiBondTestLab(name string) *config.Lab {
	lab := cleanLab(name)
	lab.Network.Mgmt = config.LabMgmt{Subnet: "10.10.1.0/24", Gateway: "10.10.1.1"}
	lab.Network.NestedNetwork = config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: config.NestedBondModeActiveBackup, Primary: "nic0", Bridge: "vmbr0"},
			{Name: "bond1", NICs: []string{"nic2", "nic3"}, Mode: config.NestedBondModeActiveBackup, Primary: "nic2", Bridge: "vmbr1"},
			{Name: "bond2", NICs: []string{"nic4", "nic5"}, Mode: config.NestedBondModeActiveBackup, Primary: "nic4", Bridge: "vmbr2", VlanAware: true},
		},
	}
	return lab
}

// TestHostnetRestageBridgeIfSlaveConflict covers
// hostnetRestageBridgeIfSlaveConflict's own decision logic directly,
// independent of the full `hostnet apply` pipeline: every shape that must
// stay a pure no-op (zero API calls), the exact live az1 retrofit shape
// that must restage, and the shape that must fail loud rather than
// silently rewrite.
func TestHostnetRestageBridgeIfSlaveConflict(t *testing.T) {
	bond := config.LabNestedBond{
		Name: "bond0", NICs: []string{"nic0", "nic1"},
		Mode: config.NestedBondModeActiveBackup, Primary: "nic0", Bridge: "vmbr0",
	}

	t.Run("bond already exists: no-op (only relevant during a bond's first creation)", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{
			"bond0": {Iface: "bond0", Type: "bond"},
			"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: "nic0"},
		}
		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, row)
	})

	t.Run("bridge not found: no-op (fresh-install / az2 shape)", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		api := hostnetTestAPIClient(t, f)
		changed, row, err := hostnetRestageBridgeIfSlaveConflict(
			context.Background(), api, "node0", bond, map[string]hostnetIfaceState{})
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, row)
	})

	t.Run("bridge exists but wrong type: no-op, left to hostnetEnsureBridge's own guard", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "eth"}}
		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, row)
	})

	t.Run("bridge_ports already empty: no-op, nothing to free", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: ""}}
		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, row)
	})

	t.Run("bridge_ports already the bond name: no-op", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: "bond0"}}
		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, row)
	})

	t.Run("bridge_ports holds one slave nic directly (live az1 shape): restages to EMPTY", func(t *testing.T) {
		// Restages to "" rather than directly to the bond name: PVE's own
		// parameter verification rejects a bridge_ports value naming an
		// interface with NO representation at all yet, staged or applied
		// ("unable to find bridge port 'bond0'") — confirmed live against
		// az1 when this function's first version tried exactly that. The
		// bridge_ports=b.Name half of the restage instead falls out of
		// hostnetEnsureBridge's own ordinary drift-reconciliation, which
		// runs AFTER hostnetEnsureBond has staged the bond itself.
		f := testhelper.NewFakePVE(t)
		var rec []hostnetRecordedRequest
		hostnetRecord(f, &rec, nil, "restage", "PUT /api2/json/nodes/node0/network/vmbr0", nil, 200)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: "nic0"}}

		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.NoError(t, err)
		assert.True(t, changed)
		require.NotNil(t, row)
		assert.Equal(t, "restaged (bridge_ports cleared)", row[2])
		require.Len(t, rec, 1)
		assert.Equal(t, "", rec[0].body["bridge_ports"])
		// existing must reflect what was ACTUALLY staged (empty), not the
		// eventual target — so a subsequent hostnetEnsureBridge call still
		// sees BridgePorts drift and issues its own PUT to the bond name.
		assert.Equal(t, "", existing["vmbr0"].BridgePorts)
	})

	t.Run("bridge_ports holds both slave nics already: restages", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		var rec []hostnetRecordedRequest
		hostnetRecord(f, &rec, nil, "restage", "PUT /api2/json/nodes/node0/network/vmbr0", nil, 200)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: "nic0 nic1"}}

		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.NoError(t, err)
		assert.True(t, changed)
		require.NotNil(t, row)
		require.Len(t, rec, 1)
		assert.Equal(t, "", rec[0].body["bridge_ports"])
		assert.Equal(t, "", existing["vmbr0"].BridgePorts)
	})

	t.Run("bridge_ports already staged empty by a prior interrupted run: no-op (state e)", func(t *testing.T) {
		// A prior run may have restaged bridge_ports to "" (this function's
		// own first half) and then failed before bond0's own create ever
		// ran (e.g. a transport error) — nothing was ever applied (the
		// final UpdateNetwork reload never runs when an earlier step
		// errors), so the live node is untouched, but the STAGED
		// interfaces.new already has bridge_ports="". Since hostnetEnsureNode
		// reads existing[] from a live ListNetwork call, which reflects
		// PVE's own pending state, a re-run observes bridge_ports already
		// empty here and must treat it as nothing-to-free — a pure no-op —
		// falling through to hostnetEnsureBond/hostnetEnsureBridge to
		// finish the job, rather than erroring or re-issuing a redundant
		// identical PUT.
		f := testhelper.NewFakePVE(t)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: ""}}

		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, row)
	})

	t.Run("bridge_ports references an unrelated interface: fails loud, issues no call", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		// Deliberately no PUT route registered: a call here would 404 and be
		// reported as a different error, failing this test's own assertions.
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: "eth99"}}

		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.Error(t, err)
		assert.ErrorContains(t, err, "eth99")
		assert.ErrorContains(t, err, `"vmbr0"`)
		assert.ErrorContains(t, err, `"bond0"`)
		assert.False(t, changed)
		assert.Nil(t, row)
	})

	t.Run("bridge_ports mixes a valid slave and an unrelated interface: fails loud", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		api := hostnetTestAPIClient(t, f)
		existing := map[string]hostnetIfaceState{"vmbr0": {Iface: "vmbr0", Type: "bridge", BridgePorts: "nic0 eth99"}}

		changed, row, err := hostnetRestageBridgeIfSlaveConflict(context.Background(), api, "node0", bond, existing)
		require.Error(t, err)
		assert.ErrorContains(t, err, "eth99")
		assert.False(t, changed)
		assert.Nil(t, row)
	})
}

// TestHostnetApply_RetrofitCurrentAz1State covers state (a) — the ACTUAL
// live az1 shape today: vmbr0 exists, holding nic0 DIRECTLY (installer-
// created, single-NIC default), no bonds and no vmbr1/vmbr2 exist at all
// yet. bond0's creation must: restage vmbr0 to bridge_ports="" first
// (freeing nic0 WITHOUT naming a not-yet-existing bond — see
// hostnetRestageBridgeIfSlaveConflict's doc comment for why a direct
// bridge_ports="bond0" restage is rejected live), then create bond0, then
// let hostnetEnsureBridge's own ordinary drift-reconciliation issue a
// SECOND vmbr0 PUT setting bridge_ports=bond0 now that bond0 has a staged
// representation. bond1/vmbr1 and bond2/vmbr2 (vlan_aware) must both be
// created fresh, entirely unaffected by the restage logic (their bridges
// don't exist yet, so hostnetRestageBridgeIfSlaveConflict is a no-op for
// both).
func TestHostnetApply_RetrofitCurrentAz1State(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetMultiBondTestLab("wayne")

	var order []string
	var createRec, vmbr0UpdateRec, applyRec []hostnetRecordedRequest

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		// Realistic az1 addressing (the installer-written vmbr0 shape from
		// the very first live finding): `inet static`, address+gateway,
		// autostart already 1. Both bridge_ports-touching PUTs below
		// (restage-to-empty, then bridge_ports=bond0) must carry these
		// addressing fields forward unchanged — confirmed live this is
		// NOT automatic (PVE's network PUT is not a partial patch).
		{
			"iface": "vmbr0", "type": "bridge", "bridge_ports": "nic0", "bridge_vlan_aware": 0,
			"autostart": 1, "cidr": "10.254.0.10/24", "address": "10.254.0.10/24", "gateway": "10.254.0.1",
		},
	})
	hostnetRecord(f, &createRec, &order, "create", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	// One route captures BOTH vmbr0 PUTs (same path — PUT .../network/vmbr0
	// — regardless of body): the restage-to-empty from
	// hostnetRestageBridgeIfSlaveConflict, and the follow-up
	// bridge_ports=bond0 from hostnetEnsureBridge's own drift check.
	hostnetRecord(f, &vmbr0UpdateRec, &order, "vmbr0-update", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, &applyRec, &order, "apply", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, vmbr0UpdateRec, 2, "vmbr0 receives exactly two PUTs: restage-to-empty, then bridge_ports=bond0")
	assert.Equal(t, "", vmbr0UpdateRec[0].body["bridge_ports"], "1st PUT clears bridge_ports (frees nic0, references nothing)")
	assert.Equal(t, "bond0", vmbr0UpdateRec[1].body["bridge_ports"], "2nd PUT points vmbr0 at bond0 once it has a staged representation")
	for i, req := range vmbr0UpdateRec {
		assert.Equal(t, "10.254.0.10/24", req.body["cidr"], "PUT #%d must carry vmbr0's existing cidr forward unchanged", i+1)
		assert.Equal(t, "10.254.0.10/24", req.body["address"], "PUT #%d must carry vmbr0's existing address forward unchanged", i+1)
		assert.Equal(t, "10.254.0.1", req.body["gateway"], "PUT #%d must carry vmbr0's existing gateway forward unchanged", i+1)
	}

	require.Len(t, createRec, 5, "bond0, bond1, vmbr1, bond2, vmbr2 — vmbr0 itself is restaged/updated, never (re-)created")
	assert.Equal(t, "bond0", createRec[0].body["iface"])
	assert.Equal(t, "nic0 nic1", createRec[0].body["slaves"])
	assert.Equal(t, "1", createRec[0].body["autostart"])
	assert.Equal(t, "bond1", createRec[1].body["iface"])
	assert.Equal(t, "nic2 nic3", createRec[1].body["slaves"])
	assert.Equal(t, "1", createRec[1].body["autostart"])
	assert.Equal(t, "vmbr1", createRec[2].body["iface"])
	assert.Equal(t, "bond1", createRec[2].body["bridge_ports"])
	assert.Equal(t, "1", createRec[2].body["autostart"])
	assert.Equal(t, "bond2", createRec[3].body["iface"])
	assert.Equal(t, "nic4 nic5", createRec[3].body["slaves"])
	assert.Equal(t, "1", createRec[3].body["autostart"])
	assert.Equal(t, "vmbr2", createRec[4].body["iface"])
	assert.Equal(t, "bond2", createRec[4].body["bridge_ports"])
	assert.Equal(t, "1", createRec[4].body["bridge_vlan_aware"], "vmbr2 must be created vlan-aware per config")
	assert.Equal(t, "1", createRec[4].body["autostart"])

	require.Len(t, order, 8,
		"vmbr0 restage-to-empty, bond0 create, vmbr0 update-to-bond0, bond1 create, vmbr1 create, "+
			"bond2 create, vmbr2 create, apply")
	assert.Equal(t, "vmbr0-update", order[0], "the restage-to-empty must happen BEFORE bond0's own create")
	assert.Equal(t, "create", order[1], "bond0 create immediately follows the restage")
	assert.Equal(t, "vmbr0-update", order[2],
		"vmbr0's own bridge_ports=bond0 update immediately follows bond0's create — bond0 must already "+
			"have a staged representation before this PUT is issued")
	assert.Equal(t, "apply", order[len(order)-1], "apply is always the very last call")

	require.Len(t, applyRec, 1)
	assert.Contains(t, out, "restaged (bridge_ports cleared)")
	assert.Contains(t, out, "updated") // vmbr0's own bridge row: bridge_ports drifted "" -> "bond0"
}

// TestHostnetApply_RetrofitPartiallyConverged covers state (b): bond0 and
// vmbr0 are ALREADY fully converged (a prior hostnet apply run already
// fixed the az1 shape for bond0), but bond1/vmbr1 and bond2/vmbr2 do not
// exist yet. No restage call may be issued at all — bond0 already exists,
// so hostnetRestageBridgeIfSlaveConflict is a no-op for that pair, and
// bond1/bond2's bridges don't exist yet either.
func TestHostnetApply_RetrofitPartiallyConverged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetMultiBondTestLab("wayne")

	var createRec []hostnetRecordedRequest

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		// autostart already 1 on both (az2-converged parity, plus what a
		// FIXED prior run for this pair would have left behind).
		{"iface": "bond0", "type": "bond", "slaves": "nic0 nic1", "bond_mode": "active-backup", "bond-primary": "nic0", "autostart": 1},
		{"iface": "vmbr0", "type": "bridge", "bridge_ports": "bond0", "bridge_vlan_aware": 0, "autostart": 1},
	})
	// Deliberately no PUT .../network/vmbr0 or .../network/bond0 route: any
	// restage/update call here would 404 and fail this test — bond0/vmbr0
	// already fully match, so neither is due.
	hostnetRecord(f, &createRec, nil, "create", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, nil, nil, "apply", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, createRec, 4, "bond1, vmbr1, bond2, vmbr2 only — bond0/vmbr0 already converged")
	assert.Equal(t, "bond1", createRec[0].body["iface"])
	assert.Equal(t, "1", createRec[0].body["autostart"])
	assert.Equal(t, "vmbr1", createRec[1].body["iface"])
	assert.Equal(t, "1", createRec[1].body["autostart"])
	assert.Equal(t, "bond2", createRec[2].body["iface"])
	assert.Equal(t, "1", createRec[2].body["autostart"])
	assert.Equal(t, "vmbr2", createRec[3].body["iface"])
	assert.Equal(t, "1", createRec[3].body["autostart"])
	assert.Contains(t, out, "already matches")
}

// TestHostnetApply_RetrofitFullyConverged covers state (c): every bond and
// every bridge already matches config exactly — a pure no-op, zero
// create/update/restage/apply calls, mirroring
// TestHostnetApply_FullyConverged_NoOp's single-bond version at 3-bond
// scale.
func TestHostnetApply_RetrofitFullyConverged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetMultiBondTestLab("wayne")

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		// autostart already 1 everywhere — az2-converged parity, and the
		// az2-shape regression guard for the autostart-drift check: a
		// fully-converged node (autostart included) must remain a
		// zero-call no-op, never start PUTting autostart just because this
		// package now knows how to.
		{"iface": "bond0", "type": "bond", "slaves": "nic0 nic1", "bond_mode": "active-backup", "bond-primary": "nic0", "autostart": 1},
		{"iface": "vmbr0", "type": "bridge", "bridge_ports": "bond0", "bridge_vlan_aware": 0, "autostart": 1},
		{"iface": "bond1", "type": "bond", "slaves": "nic2 nic3", "bond_mode": "active-backup", "bond-primary": "nic2", "autostart": 1},
		{"iface": "vmbr1", "type": "bridge", "bridge_ports": "bond1", "bridge_vlan_aware": 0, "autostart": 1},
		{"iface": "bond2", "type": "bond", "slaves": "nic4 nic5", "bond_mode": "active-backup", "bond-primary": "nic4", "autostart": 1},
		{"iface": "vmbr2", "type": "bridge", "bridge_ports": "bond2", "bridge_vlan_aware": 1, "autostart": 1},
	})
	// Deliberately no POST/PUT (other than the list GET) route registered at
	// all: any create/update/restage/apply call this run should not make
	// would hit FakePVE's 404 fallback and fail the require.NoError below.

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (no pending changes)")
	assert.NotContains(t, out, "restaged")
}

// TestHostnetApply_RetrofitFreshAz2Shape covers state (d): nothing exists
// yet on any of the 3 bond/bridge pairs — the fresh-install (az2) shape —
// proving the restage logic introduces zero behavioral change to the path
// that already worked there: every bond and bridge is created exactly as
// before, in bond-then-bridge order per pair, with no restage call at all.
func TestHostnetApply_RetrofitFreshAz2Shape(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetMultiBondTestLab("wayne")

	var createRec []hostnetRecordedRequest

	hostnetRecord(f, nil, nil, "list", "GET /api2/json/nodes/lab-wayne-0/network", []any{}, 200)
	// Deliberately no PUT .../network/vmbr0 (or any bridge) route: nothing
	// exists yet, so no restage is ever due.
	hostnetRecord(f, &createRec, nil, "create", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, nil, nil, "apply", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, createRec, 6, "bond0, vmbr0, bond1, vmbr1, bond2, vmbr2 — all created fresh")
	assert.Equal(t, "bond0", createRec[0].body["iface"])
	assert.Equal(t, "vmbr0", createRec[1].body["iface"])
	assert.Equal(t, "bond1", createRec[2].body["iface"])
	assert.Equal(t, "vmbr1", createRec[3].body["iface"])
	assert.Equal(t, "bond2", createRec[4].body["iface"])
	assert.Equal(t, "vmbr2", createRec[5].body["iface"])
	assert.Equal(t, "1", createRec[5].body["bridge_vlan_aware"])
	assert.Contains(t, out, "created")
	assert.NotContains(t, out, "restaged")
}

// TestHostnetApply_RetrofitStagedLeftoverConvergesCorrectly covers state
// (e) end-to-end: a prior run already restaged vmbr0's bridge_ports to ""
// (staged, never applied — bond0's own create failed or was never reached)
// and bond0 still does not exist. The GET this run's own ListNetwork call
// makes reflects that pending state (bridge_ports already ""), so the
// restage step is correctly a no-op on this run — no redundant PUT — and
// the run proceeds straight to creating bond0 and letting
// hostnetEnsureBridge issue the one PUT vmbr0 still actually needs
// (bridge_ports="" -> "bond0"), then applies. This is the "belt-and-
// braces" leftover-state case; no special-casing code was needed for it —
// hostnetRestageBridgeIfSlaveConflict's own ordinary "bridge_ports already
// empty" no-op branch (see its ports_holds_both/empty subtests in
// TestHostnetRestageBridgeIfSlaveConflict) already covers it, since a
// staged-but-unapplied bridge_ports="" reads back identically to a
// never-touched empty bridge.
func TestHostnetApply_RetrofitStagedLeftoverConvergesCorrectly(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")

	var order []string
	var createRec, vmbr0UpdateRec, applyRec []hostnetRecordedRequest

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		{"iface": "vmbr0", "type": "bridge", "bridge_ports": "", "bridge_vlan_aware": 0},
	})
	hostnetRecord(f, &createRec, &order, "create", "POST /api2/json/nodes/lab-wayne-0/network", nil, 200)
	hostnetRecord(f, &vmbr0UpdateRec, &order, "vmbr0-update", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, &applyRec, &order, "apply", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, vmbr0UpdateRec, 1, "exactly ONE PUT to vmbr0 this run — the restage-to-empty is a no-op "+
		"since bridge_ports is already empty from the prior interrupted run")
	assert.Equal(t, "bond0", vmbr0UpdateRec[0].body["bridge_ports"])
	assert.Equal(t, "1", vmbr0UpdateRec[0].body["autostart"], "the one PUT this run issues must also fix autostart")

	require.Len(t, createRec, 1, "only bond0 itself needs creating")
	assert.Equal(t, "bond0", createRec[0].body["iface"])
	assert.Equal(t, "1", createRec[0].body["autostart"])

	require.Len(t, order, 3, "bond0 create, vmbr0 update-to-bond0, apply — no restage call at all")
	assert.Equal(t, "create", order[0])
	assert.Equal(t, "vmbr0-update", order[1])
	assert.Equal(t, "apply", order[2])

	require.Len(t, applyRec, 1)
	assert.NotContains(t, out, "restaged")
	assert.Contains(t, out, "created")
	assert.Contains(t, out, "updated")
}

// TestHostnetApply_RetrofitAutostartMissingEverywhere covers state (f): the
// shape the live az1 3-bond lab was ACTUALLY left in after follow-up 3's
// restage+bond0-create+vmbr0-reconcile+apply ran (confirmed live) —  every
// bond and bridge stanza already exists and already matches config exactly
// (bridge_ports, bond mode/slaves, vlan_aware) — the slave-conflict/
// restage concern from states (a)/(e) is fully behind this state — but
// NONE of them have autostart set. ifreload therefore only brought up
// bond0 (as vmbr0's own dependency, since vmbr0 itself still had
// autostart=1 from the original installer config); bond1, bond2, vmbr1,
// and vmbr2 had no live link at all. A re-run must PUT autostart=1 on
// every interface reporting it unset (all five day-2-created ones; vmbr0
// is modeled here as ALSO unset — the live uncertainty this test
// deliberately covers, per "and vmbr0 if GET shows it unset") and issue
// ZERO restage or bridge_ports/bond-fields update calls, since nothing
// else has drifted.
func TestHostnetApply_RetrofitAutostartMissingEverywhere(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetMultiBondTestLab("wayne")

	var order []string
	var updateRec, applyRec []hostnetRecordedRequest

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		{"iface": "bond0", "type": "bond", "slaves": "nic0 nic1", "bond_mode": "active-backup", "bond-primary": "nic0"},
		{"iface": "vmbr0", "type": "bridge", "bridge_ports": "bond0", "bridge_vlan_aware": 0},
		{"iface": "bond1", "type": "bond", "slaves": "nic2 nic3", "bond_mode": "active-backup", "bond-primary": "nic2"},
		{"iface": "vmbr1", "type": "bridge", "bridge_ports": "bond1", "bridge_vlan_aware": 0},
		{"iface": "bond2", "type": "bond", "slaves": "nic4 nic5", "bond_mode": "active-backup", "bond-primary": "nic4"},
		{"iface": "vmbr2", "type": "bridge", "bridge_ports": "bond2", "bridge_vlan_aware": 1},
	})
	// No POST route registered at all: nothing needs creating in this
	// state — a create call here would 404 and fail this test.
	for _, iface := range []string{"bond0", "vmbr0", "bond1", "vmbr1", "bond2", "vmbr2"} {
		hostnetRecord(f, &updateRec, &order, "update-"+iface, "PUT /api2/json/nodes/lab-wayne-0/network/"+iface, nil, 200)
	}
	hostnetRecord(f, &applyRec, &order, "apply", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, updateRec, 6, "one autostart-only PUT per interface: bond0, vmbr0, bond1, vmbr1, bond2, vmbr2")
	for i, req := range updateRec {
		assert.Equal(t, "1", req.body["autostart"], "PUT #%d must set autostart=1", i+1)
	}
	// Every update PUT's only OTHER opinion must be Type — bridge_ports/
	// bond_mode/slaves/bridge_vlan_aware must be absent from these bodies
	// entirely (never touched, since none of them actually drifted):
	// FakePVE's form-decoded body only contains keys that were actually
	// sent, so an absent key reads back as "" via Go's zero-value map
	// lookup — assert that shape directly.
	for i, req := range updateRec {
		assert.Empty(t, req.body["bridge_ports"], "PUT #%d must not touch bridge_ports (unchanged)", i+1)
		assert.Empty(t, req.body["bond_mode"], "PUT #%d must not touch bond_mode (unchanged)", i+1)
	}

	require.Len(t, applyRec, 1)
	assert.Contains(t, out, "updated")
	assert.NotContains(t, out, "restaged")
	assert.NotContains(t, out, "created")
}

// TestHostnetApply_RetrofitBridgeUnexpectedPorts_FailsLoud covers
// requirement 6 end-to-end (through the full `hostnet apply` command, not
// just hostnetRestageBridgeIfSlaveConflict directly): a bridge already
// exists, bond0 does not, and the bridge's bridge_ports references
// something other than a slave of bond0 or bond0 itself — hostnet apply
// must fail loud, naming the node, the bridge, and what was found, and
// must never issue any create/update/restage/apply call.
func TestHostnetApply_RetrofitBridgeUnexpectedPorts_FailsLoud(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := hostnetTestLab("wayne")

	f.HandleJSON("GET /api2/json/nodes/lab-wayne-0/network", []map[string]any{
		{"iface": "vmbr0", "type": "bridge", "bridge_ports": "eth99", "bridge_vlan_aware": 0},
	})
	// Deliberately no POST/PUT route registered at all: a fail-loud refusal
	// must issue zero API calls.

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(hostnetAlreadyNamedResponse())
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	_, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, `"wayne"`)
	assert.ErrorContains(t, err, `"lab-wayne-0"`)
	assert.ErrorContains(t, err, `"vmbr0"`)
	assert.ErrorContains(t, err, "eth99")
	assert.ErrorContains(t, err, `"bond0"`)
}
