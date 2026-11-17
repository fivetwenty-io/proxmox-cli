package lab

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

// TestSyncLabContext_RefusesOperatorOwnedContextBeforeAnyChange covers a
// lab-<name> context the operator owns (not the pmx@pve identity, host outside
// the lab's mgmt subnet). Sync must refuse before it runs anything on the node
// or writes anything to the keychain, so no token is minted and no item is
// left behind, and the operator's context stays exactly as it was.
func TestSyncLabContext_RefusesOperatorOwnedContextBeforeAnyChange(t *testing.T) {
	fake := exec.Fake()
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)

	operatorCtx := &config.Context{
		Host: "192.168.50.10", Port: 8006, Protocol: "https", Product: config.ProductPVE,
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "ops",
			Secret: "keychain:ops-root/root@pam!ops"},
	}
	before := *operatorCtx
	deps.Cfg.Contexts["lab-demo"] = operatorCtx

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `refusing to overwrite existing context "lab-demo"`)
	assert.False(t, res.Rotated)
	assert.Empty(t, fake.Calls, "nothing may run on the node, so no token is minted")
	assert.Empty(t, *stored, "nothing may be stored in the keychain")
	assert.Empty(t, *deleted, "nothing may be deleted from the keychain")
	assert.Equal(t, before, *deps.Cfg.Contexts["lab-demo"], "the operator's context must be untouched")
}

// TestSyncLabContext_FailureAfterStoreRollsBack covers a failure between the
// keychain store and the config save. The item the run wrote is removed only
// when no context references it, and the in-memory context returns to its
// persisted state.
func TestSyncLabContext_FailureAfterStoreRollsBack(t *testing.T) {
	cases := []struct {
		name string
		// existingRef is the lab-demo context's secret before the run, or ""
		// for no context.
		existingRef string
		wantDeleted []string
		wantNote    string
	}{
		{
			name:        "fresh context",
			wantDeleted: []string{"pmx-lab.fleet.demo/pmx@pve!pmx"},
			wantNote:    "removed the keychain item pmx-lab.fleet.demo/pmx@pve!pmx it had just written",
		},
		{
			name:        "context moving off the legacy item",
			existingRef: "keychain:pmx-lab-demo/pmx@pve!pmx",
			wantDeleted: []string{"pmx-lab.fleet.demo/pmx@pve!pmx"},
			wantNote:    "removed the keychain item pmx-lab.fleet.demo/pmx@pve!pmx it had just written",
		},
		{
			// The persisted context already references the namespaced item,
			// which the rotation overwrote in place: it now holds the only
			// valid secret and the persisted context still points at it.
			name:        "context already referencing the namespaced item",
			existingRef: "keychain:pmx-lab.fleet.demo/pmx@pve!pmx",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, deps := syncTestDeps(t, mintingFake())
			stored := recordKeychainStores(t)
			deleted := recordKeychainDeletes(t)

			var before config.Context
			if tc.existingRef != "" {
				ctx := labTokenContext(tc.existingRef)
				ctx.Host = "10.10.1.99"
				before = *ctx
				deps.Cfg.Contexts["lab-demo"] = ctx
				// Probe fails with an auth rejection the first time, so the
				// run rotates.
				probeCalls := 0
				labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error {
					probeCalls++
					if probeCalls == 1 {
						return assertAnErrAuth{}
					}
					return nil
				}
			}
			// A directory where the config file belongs makes the save fail.
			deps.ConfigPath = t.TempDir()

			lab := multiNodeTestLab("demo", 1, "never")
			lab.Name = "demo"

			res, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
			require.Error(t, err)
			assert.True(t, res.Rotated)
			assert.Contains(t, err.Error(), "save config")
			assert.Contains(t, err.Error(), "already removed", "the rotation note still applies")
			assert.Equal(t, []string{"pmx-lab.fleet.demo/pmx@pve!pmx"}, *stored)

			if tc.wantDeleted == nil {
				assert.Empty(t, *deleted, "an item a context references must be kept")
				assert.NotContains(t, err.Error(), "removed the keychain item")
			} else {
				assert.Equal(t, tc.wantDeleted, *deleted)
				assert.Contains(t, err.Error(), tc.wantNote)
			}

			if tc.existingRef == "" {
				_, present := deps.Cfg.Contexts["lab-demo"]
				assert.False(t, present, "a context the failed run added must be removed from memory")
			} else {
				assert.Equal(t, before, *deps.Cfg.Contexts["lab-demo"],
					"the context must return to its persisted state")
			}
		})
	}
}

// TestRemoveUnreferencedLabSecret covers the guard in the rollback itself: an
// item any context references stays, an unreferenced one is deleted, a build
// without a keychain counts as nothing removed, and a delete failure is
// reported.
func TestRemoveUnreferencedLabSecret(t *testing.T) {
	deleted := recordKeychainDeletes(t)

	cfg := &config.Config{Contexts: map[string]*config.Context{
		"other": labTokenContext("keychain:pmx-lab.fleet.demo/pmx@pve!pmx"),
	}}
	removed, err := removeUnreferencedLabSecret(cfg, "pmx-lab.fleet.demo", "pmx@pve!pmx")
	require.NoError(t, err)
	assert.False(t, removed)
	assert.Empty(t, *deleted)

	delete(cfg.Contexts, "other")
	removed, err = removeUnreferencedLabSecret(cfg, "pmx-lab.fleet.demo", "pmx@pve!pmx")
	require.NoError(t, err)
	assert.True(t, removed)
	assert.Equal(t, []string{"pmx-lab.fleet.demo/pmx@pve!pmx"}, *deleted)

	labDeleteSecretFn = func(string, string) error { return config.ErrKeychainUnsupported }
	removed, err = removeUnreferencedLabSecret(cfg, "pmx-lab.fleet.demo", "pmx@pve!pmx")
	require.NoError(t, err)
	assert.False(t, removed)

	labDeleteSecretFn = func(string, string) error { return errors.New("keychain locked") }
	_, err = removeUnreferencedLabSecret(cfg, "pmx-lab.fleet.demo", "pmx@pve!pmx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keychain locked")
}

// TestSyncLabContext_RollbackFailureIsReported covers a rollback delete that
// itself fails: the error names both failures.
func TestSyncLabContext_RollbackFailureIsReported(t *testing.T) {
	cmd, deps := syncTestDeps(t, mintingFake())
	recordKeychainStores(t)
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(string, string) error { return errors.New("keychain locked") }
	t.Cleanup(func() { labDeleteSecretFn = orig })
	deps.ConfigPath = t.TempDir()

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	_, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save config")
	assert.Contains(t, err.Error(), "remove the unreferenced keychain item pmx-lab.fleet.demo/pmx@pve!pmx")
	assert.Contains(t, err.Error(), "keychain locked")
}

// TestSyncLabContext_ProbeFailureAfterSaveKeepsItem covers a failure after the
// save: the persisted context references the new item, so it stays.
func TestSyncLabContext_ProbeFailureAfterSaveKeepsItem(t *testing.T) {
	cmd, deps := syncTestDeps(t, mintingFake())
	recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error { return assertAnErr{} }

	lab := multiNodeTestLab("demo", 1, "never")
	lab.Name = "demo"

	_, err := syncLabContext(cmd, deps, lab, labSyncOptions{WaitSSH: false})
	require.Error(t, err)
	assert.Empty(t, *deleted)
	assert.Equal(t, "keychain:pmx-lab.fleet.demo/pmx@pve!pmx", deps.Cfg.Contexts["lab-demo"].Auth.Secret)
}
