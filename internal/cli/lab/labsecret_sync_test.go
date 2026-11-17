package lab

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

const syncTestFingerprint = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:" +
	"AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"

// mintingFake returns a fake ssh runner scripted for a sync that rotates the
// token: ensure user, ensure ACL, fingerprint, hostname, token remove, token add.
func mintingFake() *exec.FakeRunner {
	return exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + syncTestFingerprint + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                                     // hostname
		exec.FakeResponse{}, // token remove
		exec.FakeResponse{Stdout: `{"value":"the-secret"}`}, // token add
	)
}

// recordKeychainStores replaces labStoreSecretFn with a recorder that returns
// the keychain reference a real store would, and returns the slice it appends
// "service/account" to.
func recordKeychainStores(t *testing.T) *[]string {
	t.Helper()
	var stored []string
	orig := labStoreSecretFn
	labStoreSecretFn = func(_ *cobra.Command, _ *cli.Deps, service, account, _ string) (string, error) {
		stored = append(stored, service+"/"+account)
		return config.KeychainRef(service, account), nil
	}
	t.Cleanup(func() { labStoreSecretFn = orig })
	return &stored
}

// TestCreate_SameNamedLabsUnderDifferentOuterContextsDoNotCollide runs
// `lab create wayne --start` twice, from two configs whose outer contexts
// differ. Each run must store its secret in its own keychain item, and neither
// may write the legacy name-only item both used to share.
func TestCreate_SameNamedLabsUnderDifferentOuterContextsDoNotCollide(t *testing.T) {
	refs := map[string]string{}
	var stores []string

	for _, outer := range []string{"fleet-east", "fleet-west"} {
		f := testhelper.NewFakePVE(t)
		lab := createTestLab("wayne")
		lab.Network.Mgmt.Subnet = "10.10.1.0/24"
		createRegisterContextHookFreshVMRoutes(f, t, lab)
		createRegisterContextHookStartRoutes(f)

		path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
		cmd, _ := buildCreateCmdWithSSH(t, path, f, "node1")
		deps := cli.GetDeps(cmd)
		deps.CtxName = outer
		stored := recordKeychainStores(t)

		out, err := runCreateCmd(t, cmd, "wayne", "--start")
		require.NoError(t, err)
		assert.Contains(t, out, "context lab-wayne ready")

		require.Len(t, *stored, 1, "create must store exactly one secret")
		stores = append(stores, (*stored)[0])
		require.NotNil(t, deps.Cfg.Contexts["lab-wayne"])
		refs[outer] = deps.Cfg.Contexts["lab-wayne"].Auth.Secret
	}

	assert.Equal(t, []string{"pmx-lab.fleet-east.wayne/pmx@pve!pmx", "pmx-lab.fleet-west.wayne/pmx@pve!pmx"}, stores)
	assert.NotEqual(t, refs["fleet-east"], refs["fleet-west"], "the two labs must not share a keychain item")
	assert.Equal(t, "keychain:pmx-lab.fleet-east.wayne/pmx@pve!pmx", refs["fleet-east"],
		"the context records the full service and account it uses")
	for _, s := range stores {
		assert.NotEqual(t, "pmx-lab-wayne/pmx@pve!pmx", s, "create must never write the legacy item")
	}
}

// TestSyncLabContext_RefusesToOverwriteItemAnotherContextReferences covers the
// create/sync guard: when some other context in the config already references
// the namespaced item a rotation would write, sync refuses before minting, so
// neither that context's secret nor the lab's current token is destroyed.
func TestSyncLabContext_RefusesToOverwriteItemAnotherContextReferences(t *testing.T) {
	fake := mintingFake()
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	deps.Cfg.Contexts["ops-copy"] = labTokenContext("keychain:pmx-lab.fleet.demo/pmx@pve!pmx")

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	_, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `context "ops-copy" still references that item`)
	assert.Empty(t, *stored, "nothing may be written to the keychain")
	assert.False(t, mintedAToken(fake), "the refusal must come before the token is rotated")
	assert.Nil(t, deps.Cfg.Contexts["lab-demo"])
}

// TestSyncLabContext_RefusesWithoutOuterContext covers a rotation with no
// outer context to namespace the secret under: it refuses before minting
// rather than falling back to the collision-prone legacy name.
func TestSyncLabContext_RefusesWithoutOuterContext(t *testing.T) {
	fake := mintingFake()
	cmd, deps := syncTestDeps(t, fake)
	deps.CtxName = ""
	deps.Cfg.CurrentContext = ""
	stored := recordKeychainStores(t)

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	_, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.ErrorIs(t, err, errNoOuterContext)
	assert.Empty(t, *stored)
	assert.False(t, mintedAToken(fake))
}

// TestSyncLabContext_ReusedLegacyReferenceStaysUnchanged covers backward
// compatibility: a context written before namespacing keeps its legacy
// reference untouched when its secret still authenticates.
func TestSyncLabContext_ReusedLegacyReferenceStaysUnchanged(t *testing.T) {
	fake := exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + syncTestFingerprint + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                                     // hostname
	)
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)
	deps.Cfg.Contexts["lab-demo"] = labTokenContext("keychain:pmx-lab-demo/pmx@pve!pmx")

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.False(t, res.Rotated)
	assert.Equal(t, "keychain:pmx-lab-demo/pmx@pve!pmx", deps.Cfg.Contexts["lab-demo"].Auth.Secret)
	assert.Empty(t, *stored)
	assert.Empty(t, *deleted)
}

// TestSyncLabContext_RotationOffLegacyItemLeavesItAndNotes covers a rotation
// of a context that referenced the legacy item: the fresh secret goes to the
// namespaced item, the legacy item is never deleted (a same-named lab in
// another config may still use it), and stderr names it with the removal
// command.
func TestSyncLabContext_RotationOffLegacyItemLeavesItAndNotes(t *testing.T) {
	fake := mintingFake()
	cmd, deps := syncTestDeps(t, fake)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	stored := recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)
	deps.Cfg.Contexts["lab-demo"] = labTokenContext("keychain:pmx-lab-demo/pmx@pve!pmx")

	probeCalls := 0
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error {
		probeCalls++
		if probeCalls == 1 {
			return assertAnErrAuth{}
		}
		return nil
	}

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.True(t, res.Rotated)
	assert.Equal(t, []string{"pmx-lab.fleet.demo/pmx@pve!pmx"}, *stored)
	assert.Empty(t, *deleted, "sync never deletes a keychain item")
	assert.Equal(t, "keychain:pmx-lab.fleet.demo/pmx@pve!pmx", deps.Cfg.Contexts["lab-demo"].Auth.Secret)
	assert.Contains(t, stderr.String(), "the previous item pmx-lab-demo/pmx@pve!pmx is no longer referenced")
	assert.Contains(t, stderr.String(), "security delete-generic-password -s pmx-lab-demo -a 'pmx@pve!pmx'")
}

// TestSyncLabContext_RotationKeepsNamespacedItemQuiet covers a rotation whose
// context already referenced this outer context's namespaced item: the item
// is rewritten in place and no note is printed.
func TestSyncLabContext_RotationKeepsNamespacedItemQuiet(t *testing.T) {
	fake := mintingFake()
	cmd, deps := syncTestDeps(t, fake)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	stored := recordKeychainStores(t)
	deps.Cfg.Contexts["lab-demo"] = labTokenContext("keychain:pmx-lab.fleet.demo/pmx@pve!pmx")

	probeCalls := 0
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error {
		probeCalls++
		if probeCalls == 1 {
			return assertAnErrAuth{}
		}
		return nil
	}

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	_, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	assert.Equal(t, []string{"pmx-lab.fleet.demo/pmx@pve!pmx"}, *stored,
		"the item the context itself references may be rewritten")
	assert.NotContains(t, stderr.String(), "NOTE:")
}

// TestSyncLabContext_SanitizesOuterContextName covers an outer context name
// carrying characters a keychain service or reference cannot hold.
func TestSyncLabContext_SanitizesOuterContextName(t *testing.T) {
	fake := mintingFake()
	cmd, deps := syncTestDeps(t, fake)
	deps.CtxName = "ops/east lab"
	stored := recordKeychainStores(t)

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	_, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.NoError(t, err)
	require.Len(t, *stored, 1)

	service, account, ok := config.ParseKeychainRef(deps.Cfg.Contexts["lab-demo"].Auth.Secret)
	require.True(t, ok)
	assert.Equal(t, "pmx@pve!pmx", account)
	assert.Equal(t, labKeychainService("ops/east lab", "demo"), service)
	assert.True(t, keychainServiceSafe(service), "service %q must be keychain-safe", service)
	assert.True(t, labOwnsKeychainService(service, "demo"))
}

// assertAnErrAuth is a non-transport probe failure, so the reuse check treats
// the stored secret as bad and rotates.
type assertAnErrAuth struct{}

func (assertAnErrAuth) Error() string { return "401 authentication failure" }
