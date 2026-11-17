package lab

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

const legacyDemoRef = "keychain:pmx-lab-demo/pmx@pve!pmx"

// legacyDemoContext seeds lab-demo with a context that references the legacy
// name-only keychain item, whose stored secret the test probe accepts.
func legacyDemoContext(deps *cli.Deps) config.Context {
	deps.Cfg.Contexts["lab-demo"] = labTokenContext(legacyDemoRef)
	return *deps.Cfg.Contexts["lab-demo"]
}

// TestSyncLabContext_RotateMovesLegacyReferenceAndKeepsLegacyItem covers the
// reason --rotate exists. The stored legacy secret still authenticates, so a
// plain sync would reuse it and the context could never leave the legacy item.
// With Rotate the run mints a new token, stores it in the namespaced item,
// points the context at it, prints the NOTE about the legacy item, and never
// deletes that item.
func TestSyncLabContext_RotateMovesLegacyReferenceAndKeepsLegacyItem(t *testing.T) {
	fake := mintingFake()
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	legacyDemoContext(deps)

	res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{Rotate: true})
	require.NoError(t, err)

	assert.True(t, res.Rotated, "--rotate rotates although the stored secret works")
	assert.True(t, mintedAToken(fake))
	ns := labTestService(t, deps, "demo")
	assert.Equal(t, []string{ns + "/pmx@pve!pmx"}, *stored, "the new secret goes to the namespaced item")
	assert.Equal(t, labTestRef(t, deps, "demo"), deps.Cfg.Contexts["lab-demo"].Auth.Secret)
	assert.Empty(t, *deleted, "the legacy item must never be deleted")

	assert.Contains(t, stderr.String(), "NOTE:")
	assert.Contains(t, stderr.String(), "pmx-lab-demo/pmx@pve!pmx is no longer referenced")
	assert.Contains(t, stderr.String(), "security delete-generic-password -s pmx-lab-demo")

	reloaded, err := config.Load(deps.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, labTestRef(t, deps, "demo"), reloaded.Contexts["lab-demo"].Auth.Secret, "the move is saved")
}

// TestSyncLabContext_WithoutRotateStillReusesWorkingLegacySecret covers the
// default: a working legacy secret is reused, the reference does not move, and
// nothing is minted, stored, or noted.
func TestSyncLabContext_WithoutRotateStillReusesWorkingLegacySecret(t *testing.T) {
	fake := exec.Fake(
		exec.FakeResponse{}, // ensure user
		exec.FakeResponse{}, // ensure ACL
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + syncTestFingerprint + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                                     // hostname
	)
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	legacyDemoContext(deps)

	res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{})
	require.NoError(t, err)

	assert.False(t, res.Rotated)
	assert.False(t, mintedAToken(fake))
	assert.Empty(t, *stored)
	assert.Equal(t, legacyDemoRef, deps.Cfg.Contexts["lab-demo"].Auth.Secret, "the reference does not move")
	assert.Empty(t, stderr.String(), "no NOTE without a move")
}

// TestSyncLabContext_RotateWithDryRunMutatesNothing covers --rotate together
// with --dry-run. The plan says it would rotate because of --rotate, names the
// item it would write, and names the legacy item it would stop referencing,
// while the node, the keychain, the in-memory config, and the file are as they
// were.
func TestSyncLabContext_RotateWithDryRunMutatesNothing(t *testing.T) {
	fake := dryRunFake(pmxUserList, pmxACLList)
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	existing := legacyDemoContext(deps)
	before := readConfigFile(t, deps.ConfigPath)

	res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true, Rotate: true})
	require.NoError(t, err)

	assert.False(t, res.Rotated, "nothing was rotated")
	require.NotNil(t, res.Plan)
	assert.True(t, res.Plan.Forced)
	assert.False(t, res.Plan.Reuse, "--rotate must not reuse the working legacy secret")
	ns := labTestService(t, deps, "demo")
	assert.Equal(t, ns, res.Plan.StoreService)
	require.NotNil(t, res.Plan.Dropped)
	assert.Equal(t, "pmx-lab-demo", res.Plan.Dropped.service)

	out := renderPlan(t, deps, res)
	assert.Contains(t, out, "would be rotated (--rotate was given)")
	assert.Contains(t, out, "would store the new secret in item "+ns+"/pmx@pve!pmx")
	assert.Contains(t, out, "would stop being referenced")
	assert.Contains(t, out, "pmx-lab-demo/pmx@pve!pmx")

	assertNoMutatingSSH(t, fake)
	assert.Empty(t, *stored)
	assert.Empty(t, *deleted)
	assert.Empty(t, stderr.String())
	assert.Equal(t, existing, *deps.Cfg.Contexts["lab-demo"])
	assert.Equal(t, before, readConfigFile(t, deps.ConfigPath))
}

// TestSyncLabContext_RotateKeepsTheNormalRotationGuards covers the checks the
// rotation path makes before it destroys the previous token and the rollback
// after it. --rotate takes that same path, so none of them may be skipped.
func TestSyncLabContext_RotateKeepsTheNormalRotationGuards(t *testing.T) {
	t.Run("operator-owned context is refused before anything runs", func(t *testing.T) {
		fake := exec.Fake()
		cmd, deps := syncTestDeps(t, fake)
		stored := recordKeychainStores(t)
		deps.Cfg.Contexts["lab-demo"] = &config.Context{
			Host: "192.168.50.10", Port: 8006, Protocol: "https", Product: config.ProductPVE,
			Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "ops", Secret: "keychain:ops/root@pam!ops"},
		}

		_, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{Rotate: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `refusing to overwrite existing context "lab-demo"`)
		assert.Empty(t, fake.Calls)
		assert.Empty(t, *stored)
	})

	t.Run("store target checked before the mint", func(t *testing.T) {
		fake := mintingFake()
		cmd, deps := syncTestDeps(t, fake)
		stored := recordKeychainStores(t)
		legacyDemoContext(deps)
		deps.Cfg.Contexts["ops-copy"] = labTokenContext(labTestRef(t, deps, "demo"))

		res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{Rotate: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `context "ops-copy" still references that item`)
		assert.False(t, res.Rotated)
		assert.False(t, mintedAToken(fake), "the refusal comes before the token is destroyed")
		assert.Empty(t, *stored)
		assert.Equal(t, legacyDemoRef, deps.Cfg.Contexts["lab-demo"].Auth.Secret)
	})

	t.Run("failure before the save rolls back", func(t *testing.T) {
		cmd, deps := syncTestDeps(t, mintingFake())
		stored := recordKeychainStores(t)
		deleted := recordKeychainDeletes(t)
		// The save fails because a directory sits where the file belongs.
		deps.ConfigPath = t.TempDir()
		before := legacyDemoContext(deps)
		item := labTestService(t, deps, "demo") + "/pmx@pve!pmx"

		res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{Rotate: true})
		require.Error(t, err)
		assert.True(t, res.Rotated)
		assert.Contains(t, err.Error(), "save config")
		assert.Contains(t, err.Error(), "already removed", "the rotation note still applies")
		assert.Equal(t, []string{item}, *stored)
		assert.Equal(t, []string{item}, *deleted, "only the item this run wrote is removed")
		assert.Equal(t, before, *deps.Cfg.Contexts["lab-demo"], "the context returns to its persisted state")
	})
}

// TestContextSync_HasRotateFlag guards the flag, its default, and its wording.
func TestContextSync_HasRotateFlag(t *testing.T) {
	cmd := newContextSyncCmd()
	flag := cmd.Flags().Lookup("rotate")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
	assert.NotContains(t, flag.Usage, "`")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	help := buf.String()
	assert.Contains(t, help, "--rotate")
	assert.Contains(t, help, "moves onto the")
	assert.Contains(t, help, "NOTE naming the command that removes it")
	assert.NotContains(t, help, "delete the old", "the help must not advise deleting the older item")
}
