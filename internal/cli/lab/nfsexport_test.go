package lab

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

// --- shared-NFS-export alias test fixtures ---------------------------------
//
// aliasTestLabs returns two labs on DISTINCT mgmt /24s (multiNodeTestLab
// always pins "10.10.1.0/24", so both callers here override it): "pvecpi"
// (10.10.1.0/24, the export owner) and "pvecpiaz2" (10.10.2.0/24,
// storage.nfs_export: "pvecpi" — a member aliasing pvecpi's export). Every
// test below writes both into the SAME config so resolveNfsExportOwner sees
// the full sibling set, mirroring how two real Proxmox clusters (separate
// pmx labs) would share one export tree.
func aliasTestLabs() (owner, member *config.Lab) {
	owner = multiNodeTestLab("pvecpi", 1, "")
	owner.Network.Mgmt = config.LabMgmt{Subnet: "10.10.1.0/24", Gateway: "10.10.1.1"}

	member = multiNodeTestLab("pvecpiaz2", 1, "")
	member.Network.Mgmt = config.LabMgmt{Subnet: "10.10.2.0/24", Gateway: "10.10.2.1"}
	member.Storage.NFSExport = "pvecpi"

	return owner, member
}

// aliasTestUnionRwSharenfs is the rw sharenfs value both aliasTestLabs
// entries converge on once either has attached: the sorted union of both
// mgmt /24s.
const aliasTestUnionRwSharenfs = "rw=@10.10.1.0/24:@10.10.2.0/24,no_root_squash,no_subtree_check,sec=sys"

// aliasTestOwnerOnlyRwSharenfs is the rw sharenfs value the owner's images/
// backup datasets narrow to once the member alone detaches (owner remains
// the sole member).
const aliasTestOwnerOnlyRwSharenfs = "rw=@10.10.1.0/24,no_root_squash,no_subtree_check,sec=sys"

// TestNfsAttach_Alias_EnsuresOwnerDatasetsAndUnionACL covers the core alias
// feature: attaching the MEMBER lab (pvecpiaz2, storage.nfs_export:
// "pvecpi") must ensure the OWNER's datasets (tank/nfs/labs/pvecpi/*),
// apply the OWNER's own storage.nfs_quota_gb (300, deliberately distinct
// from the member's own 999 to prove the member's quota is ignored once
// aliased), and set the rw sharenfs ACL to the UNION of both labs' mgmt
// /24s — while the pvesm-add server address and firewall/shared-iso ACL
// stay scoped to the ATTACHING lab's own gateway/subnet, unaffected by the
// alias.
func TestNfsAttach_Alias_EnsuresOwnerDatasetsAndUnionACL(t *testing.T) {
	owner, member := aliasTestLabs()
	owner.Storage.NFSQuotaGB = 300
	member.Storage.NFSQuotaGB = 999 // must be ignored: the OWNER's quota governs once aliased
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pvecpi": owner, "pvecpiaz2": member}})

	fake := exec.Fake(
		exec.FakeResponse{ExitCode: 1},                         // zfs list images: absent
		exec.FakeResponse{},                                    // zfs create images
		exec.FakeResponse{ExitCode: 1},                         // zfs list backup: absent
		exec.FakeResponse{},                                    // zfs create backup
		exec.FakeResponse{Stdout: "100G"},                      // zfs get quota: differs from 300G
		exec.FakeResponse{},                                    // zfs set quota=300G
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
	cmd, f := buildNfsServerCmdWithPVE(t, path, fake)
	f.HandleJSON("GET /api2/json/nodes/"+nfsServerTestNode+"/firewall/rules", []any{
		map[string]any{"pos": 0, "type": "in", "action": "ACCEPT", "enable": 1, "source": "10.10.2.0/24",
			"comment": nfsFirewallRuleComment("pvecpiaz2", "NFS", "2049")},
		map[string]any{"pos": 1, "type": "in", "action": "ACCEPT", "enable": 1, "source": "10.10.2.0/24",
			"comment": nfsFirewallRuleComment("pvecpiaz2", "rpcbind", "111")},
	})

	out, err := runGuestCmd(t, cmd, "attach", "pvecpiaz2")
	require.NoError(t, err)
	assert.Contains(t, out, "created")
	assert.Contains(t, out, "attached")

	require.Len(t, fake.Calls, 18)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "create", "-p", "-o", "recordsize=128K", "tank/nfs/labs/pvecpi/images"), fake.Calls[1].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "create", "-p", "-o", "recordsize=1M", "tank/nfs/labs/pvecpi/backup"), fake.Calls[3].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "quota=300G", "tank/nfs/labs/pvecpi"), fake.Calls[5].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "sharenfs="+aliasTestUnionRwSharenfs, "tank/nfs/labs/pvecpi/images"), fake.Calls[7].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "sharenfs="+aliasTestUnionRwSharenfs, "tank/nfs/labs/pvecpi/backup"), fake.Calls[9].Args)
	assert.Contains(t, fake.Calls[13].Args, "pvesm add nfs nfs-images --server 10.10.2.1 --export /tank/nfs/labs/pvecpi/images --content images,import,snippets,iso --options vers=4.1")
	assert.Contains(t, fake.Calls[15].Args, "pvesm add nfs nfs-backup --server 10.10.2.1 --export /tank/nfs/labs/pvecpi/backup --content backup --options vers=4.1")
	assert.Contains(t, fake.Calls[17].Args, "pvesm add nfs shared-iso --server 10.10.2.1 --export /tank/nfs/shared/iso --content iso,vztmpl --options vers=4.1,ro,soft")
}

// TestNfsAttach_Alias_DryRun_ShowsOwnerDatasetsAndUnionACL covers --dry-run
// for an alias member: the preview must name the OWNER's dataset paths and
// the union rw ACL, and touch deps.Runner not at all.
func TestNfsAttach_Alias_DryRun_ShowsOwnerDatasetsAndUnionACL(t *testing.T) {
	owner, member := aliasTestLabs()
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pvecpi": owner, "pvecpiaz2": member}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	out, err := runGuestCmd(t, cmd, "attach", "pvecpiaz2", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "zfs create -p -o recordsize=128K tank/nfs/labs/pvecpi/images")
	assert.Contains(t, out, "zfs create -p -o recordsize=1M tank/nfs/labs/pvecpi/backup")
	assert.Contains(t, out, "zfs set sharenfs="+aliasTestUnionRwSharenfs+" tank/nfs/labs/pvecpi/images")
	assert.Contains(t, out, "zfs set sharenfs="+aliasTestUnionRwSharenfs+" tank/nfs/labs/pvecpi/backup")
	assert.Contains(t, out, "pvesm add nfs nfs-images --server 10.10.2.1 --export /tank/nfs/labs/pvecpi/images")
	assert.Empty(t, fake.Calls)
}

// TestNfsAttach_SelfReferentialExport_IsNoOp covers storage.nfs_export set
// to the lab's OWN name: harmless, identical to leaving it unset.
func TestNfsAttach_SelfReferentialExport_IsNoOp(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	lab.Storage.NFSExport = "wayne"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	out, err := runGuestCmd(t, cmd, "attach", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "tank/nfs/labs/wayne/images")
	assert.Contains(t, out, nfsServerTestRwSharenfs)
	assert.Empty(t, fake.Calls)
}

// TestNfsAttach_UnknownExportOwner_Errors covers storage.nfs_export naming
// a lab absent from the loaded config: attach must refuse loudly, naming
// the missing lab, before touching ssh at all.
func TestNfsAttach_UnknownExportOwner_Errors(t *testing.T) {
	lab := multiNodeTestLab("orphan", 1, "")
	lab.Storage.NFSExport = "doesnotexist"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"orphan": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "attach", "orphan")
	require.Error(t, err)
	assert.ErrorContains(t, err, `"doesnotexist"`)
	assert.ErrorContains(t, err, "does not exist in the loaded config")
	assert.Empty(t, fake.Calls)
}

// TestNfsAttach_ChainedExportAlias_Errors covers a THIRD-lab chain
// (laba -> labb -> labc): the owner (labb) must not itself alias a further
// lab, so resolving laba's export ownership must refuse, naming both labb
// and its own alias target labc, before touching ssh at all.
func TestNfsAttach_ChainedExportAlias_Errors(t *testing.T) {
	a := multiNodeTestLab("laba", 1, "")
	a.Network.Mgmt = config.LabMgmt{Subnet: "10.10.3.0/24", Gateway: "10.10.3.1"}
	a.Storage.NFSExport = "labb"

	b := multiNodeTestLab("labb", 1, "")
	b.Network.Mgmt = config.LabMgmt{Subnet: "10.10.4.0/24", Gateway: "10.10.4.1"}
	b.Storage.NFSExport = "labc"

	c := multiNodeTestLab("labc", 1, "")
	c.Network.Mgmt = config.LabMgmt{Subnet: "10.10.5.0/24", Gateway: "10.10.5.1"}

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"laba": a, "labb": b, "labc": c}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "attach", "laba")
	require.Error(t, err)
	assert.ErrorContains(t, err, "chained nfs_export aliases are not supported")
	assert.ErrorContains(t, err, `"labb"`)
	assert.ErrorContains(t, err, `"labc"`)
	assert.Empty(t, fake.Calls)
}

// TestNfsStatus_ShowsExportOwnerAndMembers covers `nfs status`'s new
// owner/members rows: both the member's own status and the owner's own
// status must report the identical, sorted member set.
func TestNfsStatus_ShowsExportOwnerAndMembers(t *testing.T) {
	owner, member := aliasTestLabs()
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pvecpi": owner, "pvecpiaz2": member}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvesmStatus}, exec.FakeResponse{Stdout: samplePvesmStatus})
	cli.GetDeps(cmd).Runner = fake

	memberOut, err := runGuestCmd(t, cmd, "status", "pvecpiaz2")
	require.NoError(t, err)
	assert.Contains(t, memberOut, "export owner")
	assert.Contains(t, memberOut, "pvecpi")
	assert.Contains(t, memberOut, "export members")
	assert.Contains(t, memberOut, "pvecpi, pvecpiaz2")

	ownerOut, err := runGuestCmd(t, cmd, "status", "pvecpi")
	require.NoError(t, err)
	assert.Contains(t, ownerOut, "export members")
	assert.Contains(t, ownerOut, "pvecpi, pvecpiaz2")
}

// TestNfsStatus_NonAlias_ShowsSelfAsOwnerAndSoleMember covers the regression
// case: a lab with no storage.nfs_export set reports itself as both the
// owner and the sole member.
func TestNfsStatus_NonAlias_ShowsSelfAsOwnerAndSoleMember(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvesmStatus})
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "status", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "export owner")
	assert.Contains(t, out, "wayne")
	assert.Contains(t, out, "export members")
}

// TestNfsDetach_Alias_RemovesOnlyMemberSubnet_KeepsDatasets covers a
// non-owner member's detach: client-side storages are removed as always,
// and the owner's rw ACL narrows to exclude the departing member's subnet —
// but no dataset create/destroy or quota call is ever issued.
func TestNfsDetach_Alias_RemovesOnlyMemberSubnet_KeepsDatasets(t *testing.T) {
	owner, member := aliasTestLabs()
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pvecpi": owner, "pvecpiaz2": member}})
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"storage":"nfs-images"}`}, // configured
		exec.FakeResponse{}, // remove
		exec.FakeResponse{Stdout: `{"storage":"nfs-backup"}`}, // configured
		exec.FakeResponse{}, // remove
		exec.FakeResponse{Stdout: `{"storage":"shared-iso"}`}, // configured
		exec.FakeResponse{}, // remove
		exec.FakeResponse{Stdout: aliasTestUnionRwSharenfs}, // get sharenfs images: differs (still union)
		exec.FakeResponse{}, // set sharenfs images (narrowed)
		exec.FakeResponse{Stdout: aliasTestUnionRwSharenfs}, // get sharenfs backup: differs (still union)
		exec.FakeResponse{}, // set sharenfs backup (narrowed)
	)
	cmd, _ := buildNfsServerCmdWithPVE(t, path, fake)

	out, err := runGuestCmd(t, cmd, "detach", "pvecpiaz2", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "removed")
	assert.Contains(t, out, "set ("+aliasTestOwnerOnlyRwSharenfs+")")

	require.Len(t, fake.Calls, 10, "client-side removes plus the owner's images/backup ACL narrow — never a dataset create or quota call")
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "sharenfs="+aliasTestOwnerOnlyRwSharenfs, "tank/nfs/labs/pvecpi/images"), fake.Calls[7].Args)
	assert.Equal(t, append(append([]string{}, nfsServerSSHDest...), "zfs", "set", "sharenfs="+aliasTestOwnerOnlyRwSharenfs, "tank/nfs/labs/pvecpi/backup"), fake.Calls[9].Args)
	for _, call := range fake.Calls {
		assert.NotContains(t, call.Args, "create", "detach must never create/destroy a dataset")
		assert.NotContains(t, call.Args, "quota=300G", "detach must never touch the owner's quota")
	}
}

// TestNfsDetach_Alias_DryRun_ShowsACLNarrowing covers --dry-run for a
// non-owner member's detach: the preview must show the narrowed rw ACL
// against the OWNER's dataset paths, without touching deps.Runner.
func TestNfsDetach_Alias_DryRun_ShowsACLNarrowing(t *testing.T) {
	owner, member := aliasTestLabs()
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pvecpi": owner, "pvecpiaz2": member}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	out, err := runGuestCmd(t, cmd, "detach", "pvecpiaz2", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "pvesm remove nfs-images")
	assert.Contains(t, out, "zfs set sharenfs="+aliasTestOwnerOnlyRwSharenfs+" tank/nfs/labs/pvecpi/images")
	assert.Contains(t, out, "zfs set sharenfs="+aliasTestOwnerOnlyRwSharenfs+" tank/nfs/labs/pvecpi/backup")
	assert.Empty(t, fake.Calls)
}

// TestNfsDetach_OwnerWithMembers_Refuses covers the owner-detach guard: the
// owner must not be detached while another lab still aliases its export —
// doing so would strand that lab's server-side backing. No ssh call at all.
func TestNfsDetach_OwnerWithMembers_Refuses(t *testing.T) {
	owner, member := aliasTestLabs()
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pvecpi": owner, "pvecpiaz2": member}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "detach", "pvecpi", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot detach")
	assert.ErrorContains(t, err, "pvecpiaz2")
	assert.Empty(t, fake.Calls, "refusal must happen before any client or server ssh call")
}

// TestNfsDetach_OwnerWithMembers_RefusesEvenWithDryRun covers the same
// owner-detach guard under --dry-run: the refusal is a config-level
// decision, not a live-state check, so it must fire regardless.
func TestNfsDetach_OwnerWithMembers_RefusesEvenWithDryRun(t *testing.T) {
	owner, member := aliasTestLabs()
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"pvecpi": owner, "pvecpiaz2": member}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "detach", "pvecpi", "--dry-run")
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot detach")
	assert.Empty(t, fake.Calls)
}

// TestNfsDetach_OwnerAlone_Refuses covers the sole-non-alias-owner-with-one-
// member-remaining edge case NOT being confused with the solo (never
// aliased) case: if pvecpi is set as its own explicit alias target and
// nothing else references it, it is not "sharing" anything and detaches
// exactly like the pre-alias regression case (see
// TestNfsDetach_HappyPath) — asserted here for the true single-lab config
// (no member ever configured) to pin that resolveNfsExportOwner's
// single-entry members list never triggers the owner-refusal path.
func TestNfsDetach_OwnerAlone_NeverRefuses(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())
	fake2 := exec.Fake(
		exec.FakeResponse{ExitCode: 1}, // nfs-images: not configured
		exec.FakeResponse{ExitCode: 1}, // nfs-backup: not configured
		exec.FakeResponse{ExitCode: 1}, // shared-iso: not configured
	)
	cli.GetDeps(cmd).Runner = fake2

	out, err := runGuestCmd(t, cmd, "detach", "wayne", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "detached")
	assert.Empty(t, fake.Calls)
}

// TestResolveNfsExportOwner_Members_SortedDeterministically covers
// resolveNfsExportOwner directly: the members slice must be sorted by name
// regardless of the labs map's own (random) iteration order, so
// nfsMemberMgmtCIDRs' resulting rw= list is deterministic across runs.
func TestResolveNfsExportOwner_Members_SortedDeterministically(t *testing.T) {
	owner, member := aliasTestLabs()
	owner.Name = "pvecpi"
	member.Name = "pvecpiaz2"
	labs := map[string]*config.Lab{"pvecpiaz2": member, "pvecpi": owner}

	eo, err := resolveNfsExportOwner(labs, member)
	require.NoError(t, err)
	assert.Equal(t, "pvecpi", eo.owner.Name)
	require.Len(t, eo.members, 2)
	assert.Equal(t, []string{"pvecpi", "pvecpiaz2"}, nfsExportMemberNames(eo.members))
}
