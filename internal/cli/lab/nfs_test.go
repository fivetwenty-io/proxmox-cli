package lab

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- server-side ensure phase test fixtures --------------------------------
//
// multiNodeTestLab("wayne", ...) resolves to mgmt /24 "10.10.1.0/24" (its
// own Mgmt.Subnet), so every dataset path/sharenfs value below is derived
// from that lab exactly the way buildNfsServerEnsurePlan derives it in
// nfsserver.go — kept as named constants so every test in this section
// asserts against the same values nfsserver.go's own doc comments describe.
const (
	nfsServerTestNode             = "sm-0"
	nfsServerTestNodeIP           = "192.168.9.180"
	nfsServerTestImagesDataset    = "tank/nfs/labs/wayne/images"
	nfsServerTestBackupDataset    = "tank/nfs/labs/wayne/backup"
	nfsServerTestLabDataset       = "tank/nfs/labs/wayne"
	nfsServerTestSharedIsoDataset = "tank/nfs/shared/iso"
	nfsServerTestMgmtCIDR         = "10.10.1.0/24"
	nfsServerTestRwSharenfs       = "rw=@10.10.1.0/24,no_root_squash,no_subtree_check,sec=sys"
)

// buildNfsServerCmdWithPVE is buildGuestSSHAndAPICmd plus a GET
// /cluster/status fixture mapping nfsServerTestNode to nfsServerTestNodeIP
// (the address createDatasetSSHHost's nodeaddr.Resolve call resolves it to —
// mirroring create_test.go's createRegisterDatasetNodeAddr/
// buildCreateCmdWithZFSFake for the identical ssh-host-resolution mechanism)
// and deps.Node set to nfsServerTestNode, both of which `nfs attach`'s
// server-side ensure phase now requires for any non-dry-run invocation. It
// registers NO firewall-rules route — callers needing the firewall ensure
// phase register their own fixture (or use buildNfsServerCmd, whose default
// is "both rules already present and enabled").
func buildNfsServerCmdWithPVE(t *testing.T, configPath string, fake *exec.FakeRunner) (*cobra.Command, *testhelper.FakePVE) {
	t.Helper()

	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "cluster", "name": "testcluster", "id": "testcluster", "quorate": 1, "nodes": 1, "online": 1},
		map[string]any{"type": "node", "name": nfsServerTestNode, "id": "node/" + nfsServerTestNode, "ip": nfsServerTestNodeIP, "online": 1},
	})

	cmd, _ := buildGuestSSHAndAPICmd(t, configPath, f, newNfsCmd())
	deps := cli.GetDeps(cmd)
	deps.Runner = fake
	deps.Node = nfsServerTestNode
	return cmd, f
}

// nfsTestFirewallRulesEnabled is the host-firewall list fixture for a lab
// ("wayne") whose two NFS rules already exist and are enabled — the
// firewall ensure phase's fully-idempotent skip case.
func nfsTestFirewallRulesEnabled() []any {
	return []any{
		map[string]any{"pos": 0, "type": "in", "action": "ACCEPT", "enable": 1,
			"comment": nfsFirewallRuleComment("wayne", "NFS", "2049")},
		map[string]any{"pos": 1, "type": "in", "action": "ACCEPT", "enable": 1,
			"comment": nfsFirewallRuleComment("wayne", "rpcbind", "111")},
	}
}

// buildNfsServerCmd is buildNfsServerCmdWithPVE with the default firewall
// fixture: both of the lab's host-firewall rules already present and
// enabled, so the firewall ensure phase is a pure skip.
func buildNfsServerCmd(t *testing.T, configPath string, fake *exec.FakeRunner) *cobra.Command {
	t.Helper()

	cmd, f := buildNfsServerCmdWithPVE(t, configPath, fake)
	f.HandleJSON("GET /api2/json/nodes/"+nfsServerTestNode+"/firewall/rules", nfsTestFirewallRulesEnabled())
	return cmd
}

// nfsServerSSHDest is the leading ssh argv prefix (options + destination)
// every server-side ensure-phase call in this section carries, mirroring
// create_test.go's own "-p", "22", "root@<ip>" assertions for the identical
// createDatasetSSHFlags/createDatasetSSHArgs convention.
var nfsServerSSHDest = []string{"-p", "22", "root@" + nfsServerTestNodeIP}

func TestNfsAttach_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	out, err := runGuestCmd(t, cmd, "attach", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "zfs create -p -o recordsize=128K "+nfsServerTestImagesDataset)
	assert.Contains(t, out, "zfs create -p -o recordsize=1M "+nfsServerTestBackupDataset)
	assert.Contains(t, out, "zfs set quota=200G "+nfsServerTestLabDataset)
	assert.Contains(t, out, "zfs set sharenfs="+nfsServerTestRwSharenfs+" "+nfsServerTestImagesDataset)
	assert.Contains(t, out, "zfs set sharenfs="+nfsServerTestRwSharenfs+" "+nfsServerTestBackupDataset)
	assert.Contains(t, out, "ensure "+nfsServerTestSharedIsoDataset+"'s sharenfs ro= list includes @"+nfsServerTestMgmtCIDR)
	assert.Contains(t, out, "node firewall: ACCEPT tcp dport 2049 from "+nfsServerTestMgmtCIDR+" (tank/nfs: wayne mgmt subnet -> NFS (2049/tcp))")
	assert.Contains(t, out, "node firewall: ACCEPT tcp dport 111 from "+nfsServerTestMgmtCIDR+" (tank/nfs: wayne mgmt subnet -> rpcbind (111/tcp))")
	assert.Contains(t, out, "pvesm add nfs nfs-images --server 10.10.1.1 --export /tank/nfs/labs/wayne/images --content images,import,snippets,iso --options vers=4.1")
	assert.Contains(t, out, "pvesm add nfs nfs-backup --server 10.10.1.1 --export /tank/nfs/labs/wayne/backup --content backup --options vers=4.1")
	assert.Contains(t, out, "pvesm add nfs shared-iso --server 10.10.1.1 --export /tank/nfs/shared/iso --content iso,vztmpl --options vers=4.1,ro,soft")
	assert.Empty(t, fake.Calls, "dry-run must never touch ssh for the server-side ensure phase either")
}

// TestNfsServerIP_RejectsInvalidGateway covers M3-R03: a malformed
// network.mgmt.gateway must never reach `pvesm add nfs --server <value>`
// unvalidated.
func TestNfsServerIP_RejectsInvalidGateway(t *testing.T) {
	n := config.LabNetwork{Mgmt: config.LabMgmt{Gateway: "not-an-ip; rm -rf /"}}
	_, err := nfsServerIP(n)
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a valid IP address")
}

func TestNfsServerIP_ValidGatewayPassesThrough(t *testing.T) {
	n := config.LabNetwork{Mgmt: config.LabMgmt{Gateway: "10.10.1.1"}}
	ip, err := nfsServerIP(n)
	require.NoError(t, err)
	assert.Equal(t, "10.10.1.1", ip)
}

func TestNfsServerIP_FallsBackToDerivedGatewayWhenUnset(t *testing.T) {
	n := config.LabNetwork{Mgmt: config.LabMgmt{Subnet: "10.20.3.0/24"}}
	ip, err := nfsServerIP(n)
	require.NoError(t, err)
	assert.Equal(t, "10.20.3.1", ip)
}

func TestNfsAttach_RefusesInvalidGatewayBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	lab.Network.Mgmt.Gateway = "not-an-ip"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a valid IP address")
	assert.Empty(t, fake.Calls)
}

// TestNfsAttach_HappyPath_AttachesAll covers a fully fresh lab: every
// server-side dataset/property is absent/different, so the ensure phase
// must issue its full ordered command sequence (dataset creates with their
// recordsize, quota set, both sharenfs sets, shared-iso ro-list append)
// BEFORE the existing pvesm-add phase runs.
func TestNfsAttach_HappyPath_AttachesAll(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		exec.FakeResponse{ExitCode: 1},                         // zfs list images: absent
		exec.FakeResponse{},                                    // zfs create images
		exec.FakeResponse{ExitCode: 1},                         // zfs list backup: absent
		exec.FakeResponse{},                                    // zfs create backup
		exec.FakeResponse{Stdout: "100G"},                      // zfs get quota: differs from 200G default
		exec.FakeResponse{},                                    // zfs set quota=200G
		exec.FakeResponse{Stdout: "off"},                       // zfs get sharenfs images: differs
		exec.FakeResponse{},                                    // zfs set sharenfs images
		exec.FakeResponse{Stdout: "off"},                       // zfs get sharenfs backup: differs
		exec.FakeResponse{},                                    // zfs set sharenfs backup
		exec.FakeResponse{Stdout: "ro=@10.108.0.0/24,sec=sys"}, // zfs get sharenfs shared/iso: missing our subnet
		exec.FakeResponse{},                                    // zfs set sharenfs shared/iso (appended)
		exec.FakeResponse{ExitCode: 1},                         // probe nfs-images: not configured
		exec.FakeResponse{},                                    // add nfs-images
		exec.FakeResponse{ExitCode: 1},                         // probe nfs-backup: not configured
		exec.FakeResponse{},                                    // add nfs-backup
		exec.FakeResponse{ExitCode: 1},                         // probe shared-iso: not configured
		exec.FakeResponse{},                                    // add shared-iso
	)
	cmd := buildNfsServerCmd(t, path, fake)

	out, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "created")
	assert.Contains(t, out, "attached")
	assert.Contains(t, out, "skip (already present)",
		"the default fixture's firewall rules already exist and are enabled")

	require.Len(t, fake.Calls, 18)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "create", "-p", "-o", "recordsize=128K", nfsServerTestImagesDataset), fake.Calls[1].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "create", "-p", "-o", "recordsize=1M", nfsServerTestBackupDataset), fake.Calls[3].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "quota=200G", nfsServerTestLabDataset), fake.Calls[5].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "sharenfs="+nfsServerTestRwSharenfs, nfsServerTestImagesDataset), fake.Calls[7].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "sharenfs="+nfsServerTestRwSharenfs, nfsServerTestBackupDataset), fake.Calls[9].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "sharenfs=ro=@10.108.0.0/24:@10.10.1.0/24,sec=sys", nfsServerTestSharedIsoDataset), fake.Calls[11].Args)
	assert.Contains(t, fake.Calls[13].Args, "pvesm add nfs nfs-images --server 10.10.1.1 --export /tank/nfs/labs/wayne/images --content images,import,snippets,iso --options vers=4.1")
	assert.Contains(t, fake.Calls[15].Args, "pvesm add nfs nfs-backup --server 10.10.1.1 --export /tank/nfs/labs/wayne/backup --content backup --options vers=4.1")
	assert.Contains(t, fake.Calls[17].Args, "pvesm add nfs shared-iso --server 10.10.1.1 --export /tank/nfs/shared/iso --content iso,vztmpl --options vers=4.1,ro,soft")
}

// TestNfsAttach_Idempotent_ReRun_NoMutations covers a lab whose server-side
// datasets/properties are already at their desired state (and whose pvesm
// storage entries are already registered): the whole run must issue zero
// "zfs create"/"zfs set"/pvesm-add calls, only the read-side probes,
// confirming the ensure phase still proceeds through to (and completes) the
// pvesm phase rather than short-circuiting.
func TestNfsAttach_Idempotent_ReRun_NoMutations(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		exec.FakeResponse{},                                                  // zfs list images: exists
		exec.FakeResponse{},                                                  // zfs list backup: exists
		exec.FakeResponse{Stdout: "200G"},                                    // zfs get quota: already correct
		exec.FakeResponse{Stdout: nfsServerTestRwSharenfs},                   // zfs get sharenfs images: already correct
		exec.FakeResponse{Stdout: nfsServerTestRwSharenfs},                   // zfs get sharenfs backup: already correct
		exec.FakeResponse{Stdout: "ro=@10.108.0.0/24:@10.10.1.0/24,sec=sys"}, // zfs get sharenfs shared/iso: already includes our subnet
		exec.FakeResponse{Stdout: `{"storage":"nfs-images","content":"images,import,snippets,iso"}`}, // probe nfs-images: attached, content full
		exec.FakeResponse{Stdout: `{"storage":"nfs-backup","content":"backup"}`},                     // probe nfs-backup: attached, content full
		exec.FakeResponse{Stdout: `{"storage":"shared-iso","content":"iso,vztmpl"}`},                 // probe shared-iso: attached, content full
	)
	cmd := buildNfsServerCmd(t, path, fake)

	out, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (already exists)")
	assert.Contains(t, out, "skip (already 200G)")
	assert.Contains(t, out, "skip (already "+nfsServerTestRwSharenfs+")")
	assert.Contains(t, out, "skip (already includes @"+nfsServerTestMgmtCIDR+")")
	assert.Contains(t, out, "skip (already present)")
	assert.Contains(t, out, "skip (already attached)")

	require.Len(t, fake.Calls, 9, "every step must be a read-only probe; no create/set/add calls at all")
	for _, call := range fake.Calls {
		assert.NotContains(t, call.Args, "create", "no zfs create call on a fully idempotent re-run")
		assert.NotContains(t, call.Args, "set", "no zfs set call on a fully idempotent re-run")
	}
}

// TestNfsAttach_SkipsAlreadyAttached also covers the content-ensure step's
// no-positive-reading guard: these probe payloads carry NO `content` field,
// and with no readable current list the step must skip rather than guess a
// `pvesm set` (which could silently clobber operator state).
func TestNfsAttach_SkipsAlreadyAttached(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		exec.FakeResponse{},                                   // zfs list images: exists
		exec.FakeResponse{},                                   // zfs list backup: exists
		exec.FakeResponse{Stdout: "200G"},                     // zfs get quota: already correct
		exec.FakeResponse{Stdout: nfsServerTestRwSharenfs},    // zfs get sharenfs images: already correct
		exec.FakeResponse{Stdout: nfsServerTestRwSharenfs},    // zfs get sharenfs backup: already correct
		exec.FakeResponse{Stdout: "ro=@10.10.1.0/24,sec=sys"}, // zfs get sharenfs shared/iso: already includes our subnet
		exec.FakeResponse{Stdout: `{"storage":"nfs-images"}`},
		exec.FakeResponse{Stdout: `{"storage":"nfs-backup"}`},
		exec.FakeResponse{Stdout: `{"storage":"shared-iso"}`},
	)
	cmd := buildNfsServerCmd(t, path, fake)

	out, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (already attached)")
	require.Len(t, fake.Calls, 9, "no add calls when every storage is already configured")
}

// nfsTestIdempotentServerSSH returns the six read-only ssh responses for a
// fully-converged server-side ZFS phase (datasets exist, quota/sharenfs
// already correct, shared-iso list already includes the lab's subnet) —
// shared by the firewall/content tests below so each isolates its assertion
// to its own phase.
func nfsTestIdempotentServerSSH() []exec.FakeResponse {
	return []exec.FakeResponse{
		{},                                   // zfs list images: exists
		{},                                   // zfs list backup: exists
		{Stdout: "200G"},                     // zfs get quota: already correct
		{Stdout: nfsServerTestRwSharenfs},    // zfs get sharenfs images: already correct
		{Stdout: nfsServerTestRwSharenfs},    // zfs get sharenfs backup: already correct
		{Stdout: "ro=@10.10.1.0/24,sec=sys"}, // zfs get sharenfs shared/iso: already includes our subnet
	}
}

// TestNfsAttach_FirewallRules_CreatedWhenMissing covers the firewall ensure
// phase's create path: with no matching rules on the node, attach must POST
// exactly two rule creates — 2049/tcp and 111/tcp, in that order — each an
// enabled ("enable":1 explicit; the PVE API default for an omitted enable is
// 0, a disabled no-op rule) ACCEPT scoped to the lab's mgmt /24, carrying
// the script-parity comment key.
func TestNfsAttach_FirewallRules_CreatedWhenMissing(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(append(nfsTestIdempotentServerSSH(),
		exec.FakeResponse{Stdout: `{"storage":"nfs-images","content":"images,import,snippets,iso"}`},
		exec.FakeResponse{Stdout: `{"storage":"nfs-backup","content":"backup"}`},
		exec.FakeResponse{Stdout: `{"storage":"shared-iso","content":"iso,vztmpl"}`},
	)...)
	cmd, f := buildNfsServerCmdWithPVE(t, path, fake)

	f.HandleJSON("GET /api2/json/nodes/"+nfsServerTestNode+"/firewall/rules", []any{})
	var created []map[string]any
	f.HandleFunc("POST /api2/json/nodes/"+nfsServerTestNode+"/firewall/rules",
		func(w http.ResponseWriter, r *http.Request) {
			body, rerr := io.ReadAll(r.Body)
			require.NoError(t, rerr)
			m := map[string]any{}
			if uerr := json.Unmarshal(body, &m); uerr != nil {
				vals, perr := url.ParseQuery(string(body))
				require.NoError(t, perr, "rule-create body neither JSON nor form-encoded: %s", body)
				for k, v := range vals {
					m[k] = v[0]
				}
			}
			created = append(created, m)
			testhelper.WriteData(w, nil)
		})

	out, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "created (ACCEPT tcp/2049 from "+nfsServerTestMgmtCIDR+")")
	assert.Contains(t, out, "created (ACCEPT tcp/111 from "+nfsServerTestMgmtCIDR+")")

	require.Len(t, created, 2)
	for i, port := range []string{"2049", "111"} {
		m := created[i]
		assert.Equal(t, "in", fmt.Sprint(m["type"]))
		assert.Equal(t, "ACCEPT", fmt.Sprint(m["action"]))
		assert.Equal(t, "tcp", fmt.Sprint(m["proto"]))
		assert.Equal(t, port, fmt.Sprint(m["dport"]))
		assert.Equal(t, nfsServerTestMgmtCIDR, fmt.Sprint(m["source"]))
		assert.Equal(t, "1", fmt.Sprint(m["enable"]), "enable must be explicit — the API default is a disabled rule")
		assert.Equal(t, "nolog", fmt.Sprint(m["log"]))
		if port == "2049" {
			assert.Equal(t, "tank/nfs: wayne mgmt subnet -> NFS (2049/tcp)", fmt.Sprint(m["comment"]))
		} else {
			assert.Equal(t, "tank/nfs: wayne mgmt subnet -> rpcbind (111/tcp)", fmt.Sprint(m["comment"]))
		}
	}
}

// TestNfsAttach_FirewallRuleDisabled_LoudFailure covers the
// present-but-disabled case: silently skipping would recreate the exact
// dead-mount symptom the phase exists to kill, and force-enabling would
// override a possibly deliberate operator action — so attach must refuse
// loudly, naming the remediation command with the rule's actual position,
// before the pvesm phase ever runs.
func TestNfsAttach_FirewallRuleDisabled_LoudFailure(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(nfsTestIdempotentServerSSH()...)
	cmd, f := buildNfsServerCmdWithPVE(t, path, fake)

	f.HandleJSON("GET /api2/json/nodes/"+nfsServerTestNode+"/firewall/rules", []any{
		map[string]any{"pos": 3, "type": "in", "action": "ACCEPT", "enable": 0,
			"comment": nfsFirewallRuleComment("wayne", "NFS", "2049")},
	})

	_, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "disabled")
	assert.ErrorContains(t, err, "rules update 3 --enable 1")

	require.Len(t, fake.Calls, 6, "the pvesm phase must never be reached past a disabled firewall rule")
}

// TestNfsAttach_ContentWidened_NfsImages covers the content-ensure step's
// mutation path: an nfs-images entry attached before the full content list
// became the default (content "images" only) must be widened in place via
// `pvesm set`, and an operator-added extra type must ride along, never be
// dropped.
func TestNfsAttach_ContentWidened_NfsImages(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(append(nfsTestIdempotentServerSSH(),
		exec.FakeResponse{Stdout: `{"storage":"nfs-images","content":"images,rootdir"}`}, // attached pre-widening, plus an operator extra
		exec.FakeResponse{}, // pvesm set nfs-images
		exec.FakeResponse{Stdout: `{"storage":"nfs-backup","content":"backup"}`},
		exec.FakeResponse{Stdout: `{"storage":"shared-iso","content":"iso,vztmpl"}`},
	)...)
	cmd := buildNfsServerCmd(t, path, fake)

	out, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "content widened (images,import,snippets,iso,rootdir)")

	require.Len(t, fake.Calls, 10)
	assert.Contains(t, fake.Calls[7].Args, "pvesm set nfs-images --content images,import,snippets,iso,rootdir")
}

func TestNfsMergeContent(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		existing string
		merged   string
		missing  bool
	}{
		{name: "widens and preserves extras",
			want: "images,import,snippets,iso", existing: "images,rootdir",
			merged: "images,import,snippets,iso,rootdir", missing: true},
		{name: "already full is not a mutation",
			want: "images,import,snippets,iso", existing: "images,import,snippets,iso",
			merged: "", missing: false},
		{name: "order-only difference is not a mutation",
			want: "images,import,snippets,iso", existing: "iso,images,snippets,import",
			merged: "", missing: false},
		{name: "empty existing gets the full want list",
			want: "images,import,snippets,iso", existing: "",
			merged: "images,import,snippets,iso", missing: true},
		{name: "single-type target already satisfied",
			want: "backup", existing: "backup",
			merged: "", missing: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merged, missing := nfsMergeContent(tc.want, tc.existing)
			assert.Equal(t, tc.missing, missing)
			assert.Equal(t, tc.merged, merged)
		})
	}
}

func TestNfsAttach_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 1, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "attach", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}

// TestNfsAttach_RefusesWithoutNode covers the server-side ensure phase's own
// precondition: with no --node/$PMX_NODE/context-default node resolvable
// (deps.Node == ""), attach must refuse loudly before touching ssh at all,
// rather than let createDatasetSSHHost fail deeper with a less actionable
// message.
func TestNfsAttach_RefusesWithoutNode(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd()) // deps.Node left unset

	_, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "outer PVE node")
	assert.Empty(t, fake.Calls)
}

// TestNfsAttach_SharedIsoSharenfsOff_LoudFailure covers the shared/iso
// export's own precondition: this phase can only ensure lab-subnet
// MEMBERSHIP in an already-built ro= list, never construct one from
// scratch (it cannot know every other lab's subnet) — a sharenfs value of
// "off" means the shared NFS service's host build (lab repo
// scripts/60-nfs-service) was never run, and attach must refuse loudly,
// naming that script, rather than silently do nothing or guess a value.
// Every dataset/property step before the shared/iso step is idempotent
// no-ops here, isolating the assertion to the failing step alone.
func TestNfsAttach_SharedIsoSharenfsOff_LoudFailure(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		exec.FakeResponse{},                                // zfs list images: exists
		exec.FakeResponse{},                                // zfs list backup: exists
		exec.FakeResponse{Stdout: "200G"},                  // zfs get quota: already correct
		exec.FakeResponse{Stdout: nfsServerTestRwSharenfs}, // zfs get sharenfs images: already correct
		exec.FakeResponse{Stdout: nfsServerTestRwSharenfs}, // zfs get sharenfs backup: already correct
		exec.FakeResponse{Stdout: "off"},                   // zfs get sharenfs shared/iso: never built
	)
	cmd := buildNfsServerCmd(t, path, fake)

	_, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "60-nfs-service")
	assert.ErrorContains(t, err, nfsServerTestSharedIsoDataset)

	require.Len(t, fake.Calls, 6, "nothing mutated, and the pvesm phase must never be reached")
}

// TestNfsAttach_TransportFailure_AbortsWithoutFurtherCalls covers ssh's own
// transport exit code (255 — connection refused/unreachable/auth failure,
// never reaching the remote shell at all) reaching the very first
// server-side probe: it must abort the whole command loudly, naming the
// dataset, ssh destination, exit code, and stderr, rather than being
// misread as "dataset absent" (which would proceed straight to a "zfs
// create" that fails the same way with no visible error).
func TestNfsAttach_TransportFailure_AbortsWithoutFurtherCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake(
		exec.FakeResponse{
			Stderr:   "ssh: connect to host 192.168.9.180 port 22: Connection refused\r\n",
			ExitCode: 255,
		},
	)
	cmd := buildNfsServerCmd(t, path, fake)

	_, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.Error(t, err, "an ssh transport failure (255) must abort the command, not silently proceed")
	assert.ErrorContains(t, err, nfsServerTestImagesDataset)
	assert.ErrorContains(t, err, nfsServerTestNodeIP)
	assert.ErrorContains(t, err, "255")
	assert.ErrorContains(t, err, "Connection refused")

	require.Len(t, fake.Calls, 1, "the probe ran once; the transport failure must abort before any other call")
}

// TestBuildNfsServerEnsurePlan_QuotaGB_DefaultsTo200WhenUnset and
// TestBuildNfsServerEnsurePlan_QuotaGB_ConfiguredOverridesDefault cover
// config.EffectiveNFSQuotaGB's wiring into the server-side plan directly
// (no ssh needed): storage.nfs_quota_gb unset resolves to the documented
// 200G default; set, it overrides that default verbatim.
func TestBuildNfsServerEnsurePlan_QuotaGB_DefaultsTo200WhenUnset(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	lab.Storage.NFSQuotaGB = 0

	plan, err := buildNfsServerEnsurePlan(lab)
	require.NoError(t, err)
	assert.Equal(t, 200, plan.quotaGB)
}

func TestBuildNfsServerEnsurePlan_QuotaGB_ConfiguredOverridesDefault(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	lab.Storage.NFSQuotaGB = 600

	plan, err := buildNfsServerEnsurePlan(lab)
	require.NoError(t, err)
	assert.Equal(t, 600, plan.quotaGB)
}

// TestNfsSharedIsoContainsSubnet_AlreadyPresent_NoAppendNeeded and
// TestNfsSharedIsoAppendSubnet_PreservesOrderAndSuffix cover the shared/iso
// ro= list parser directly: a subnet already present is detected without
// needing any ssh round trip, and appending preserves every existing
// token's order plus the ",sec=sys" suffix byte-for-byte.
func TestNfsSharedIsoContainsSubnet_AlreadyPresent_NoAppendNeeded(t *testing.T) {
	contains, err := nfsSharedIsoContainsSubnet("ro=@10.108.0.0/24:@10.10.1.0/24,sec=sys", "10.10.1.0/24")
	require.NoError(t, err)
	assert.True(t, contains)
}

func TestNfsSharedIsoContainsSubnet_Absent(t *testing.T) {
	contains, err := nfsSharedIsoContainsSubnet("ro=@10.108.0.0/24,sec=sys", "10.10.1.0/24")
	require.NoError(t, err)
	assert.False(t, contains)
}

func TestNfsSharedIsoAppendSubnet_PreservesOrderAndSuffix(t *testing.T) {
	got, err := nfsSharedIsoAppendSubnet("ro=@10.108.0.0/24:@10.109.0.0/24,sec=sys", "10.10.1.0/24")
	require.NoError(t, err)
	assert.Equal(t, "ro=@10.108.0.0/24:@10.109.0.0/24:@10.10.1.0/24,sec=sys", got)
}

func TestNfsSharedIsoContainsSubnet_UnrecognizedShape_Errors(t *testing.T) {
	_, err := nfsSharedIsoContainsSubnet("off", "10.10.1.0/24")
	require.Error(t, err)
}

const samplePvesmStatus = `Name             Type     Status           Total            Used       Available        %
local             dir     active       100000000        10000000        90000000    10.00%
nfs-images        nfs     active      1073741824               0      1073741824    0.00%
shared-iso        nfs   inactive               0               0               0    0.00%
`

func TestNfsStatus_RendersConfiguredAndStatus(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvesmStatus})
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "status", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "nfs-images")
	assert.Contains(t, out, "active")
	assert.Contains(t, out, "shared-iso")
	assert.Contains(t, out, "inactive")
	assert.Contains(t, out, "nfs-backup")
	assert.Contains(t, out, "n/a", "nfs-backup is not in the sample pvesm status output, so it must render n/a")
}

func TestParsePvesmStatus_ParsesDataRows(t *testing.T) {
	statuses := parsePvesmStatus(samplePvesmStatus)
	assert.Equal(t, "active", statuses["nfs-images"])
	assert.Equal(t, "inactive", statuses["shared-iso"])
	assert.NotContains(t, statuses, "nfs-backup")
}

func TestNfsDetach_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	out, err := runGuestCmd(t, cmd, "detach", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "pvesm remove nfs-images")
	assert.Contains(t, out, "pvesm remove nfs-backup")
	assert.Contains(t, out, "pvesm remove shared-iso")
	assert.Empty(t, fake.Calls)
}

func TestNfsDetach_RefusesWithoutYesNonInteractively(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())
	cmd.SetIn(strings.NewReader(""))

	out, err := runGuestCmd(t, cmd, "detach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "Aborted")
	assert.Empty(t, fake.Calls, "must refuse to run without confirmation")
}

func TestNfsDetach_HappyPath(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"storage":"nfs-images"}`}, // configured
		exec.FakeResponse{},                                   // remove
		exec.FakeResponse{ExitCode: 1},                        // nfs-backup: not configured
		exec.FakeResponse{Stdout: `{"storage":"shared-iso"}`}, // configured
		exec.FakeResponse{},                                   // remove
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "detach", "wayne", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "detached")

	require.Len(t, fake.Calls, 5)
	assert.Contains(t, fake.Calls[1].Args, "pvesm remove nfs-images")
	assert.Contains(t, fake.Calls[4].Args, "pvesm remove shared-iso")
}

func TestNfsDetach_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 1, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "detach", "dirty", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}
