package lab

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
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

// labTokenContext returns a minimal lab token context whose auth secret is
// secretRef, valid under config.Load's lenient validation.
func labTokenContext(secretRef string) *config.Context {
	return &config.Context{
		Host:     "10.10.1.10",
		Port:     8006,
		Protocol: "https",
		Product:  config.ProductPVE,
		Auth: config.AuthBlock{
			Type:     "token",
			Username: "pmx@pve",
			TokenID:  "pmx",
			Secret:   secretRef,
		},
	}
}

// destroyTestOuter is the outer context every destroy keychain test runs
// under.
const destroyTestOuter = "fleet"

// destroyNS names lab demo's namespaced keychain services as seen from a
// destroy running under destroyTestOuter in the config at a given path.
type destroyNS struct {
	// cur is the service this invocation stores and deletes under.
	cur string
	// otherOuter is the service a same-named lab reached through another
	// outer context in the same config uses.
	otherOuter string
	// otherConfig is the service the same outer context name uses in a
	// config at another path (or in this config before it moved).
	otherConfig string
	// preHash is the namespaced form written before the config hash was
	// added to the namespace.
	preHash string
}

func newDestroyNS(t *testing.T, path string) destroyNS {
	t.Helper()
	hash, err := labConfigHash(path)
	require.NoError(t, err)
	elsewhere, err := labConfigHash(filepath.Join(filepath.Dir(path), "elsewhere", "config.yml"))
	require.NoError(t, err)
	return destroyNS{
		cur:         labKeychainService(destroyTestOuter, hash, "demo"),
		otherOuter:  labKeychainService("fleet-west", hash, "demo"),
		otherConfig: labKeychainService(destroyTestOuter, elsewhere, "demo"),
		preHash:     "pmx-lab." + destroyTestOuter + ".demo",
	}
}

// expand replaces {cur}, {otherOuter}, {otherConfig}, and {preHash} in s.
func (ns destroyNS) expand(s string) string {
	return strings.NewReplacer(
		"{cur}", ns.cur,
		"{otherOuter}", ns.otherOuter,
		"{otherConfig}", ns.otherConfig,
		"{preHash}", ns.preHash,
	).Replace(s)
}

// ref returns the keychain reference to the lab token item under service.
func (ns destroyNS) ref(service string) string {
	return config.KeychainRef(ns.expand(service), labCtxAccount())
}

// destroyLabVMRoutes registers a single running node VM in lab demo's pool,
// with the status, stop, and delete routes a destroy of it needs.
func destroyLabVMRoutes(f *testhelper.FakePVE) {
	destroyHandleClusterResources(f, map[string]any{
		"vmid": 100, "node": "pve1", "pool": "lab-demo", "name": "lab-demo", "status": "running", "type": "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{"status": "running", "vmid": 100})
	stopUPID := "UPID:pve1:00000001:00000001:65000000:qmstop:100:root@pam:"
	f.HandleJSON("POST /api2/json/nodes/pve1/qemu/100/status/stop", stopUPID)
	destroyHandleTaskStatus(f, "pve1", stopUPID)
	deleteUPID := "UPID:pve1:00000002:00000002:65000000:qmdestroy:100:root@pam:"
	f.HandleJSON("DELETE /api2/json/nodes/pve1/qemu/100", deleteUPID)
	destroyHandleTaskStatus(f, "pve1", deleteUPID)
}

// buildDestroyCmdWithContexts builds a destroy command for lab "demo" running
// under destroyTestOuter, whose config carries the contexts built from the
// config's namespaces. withVM puts one node VM in the lab's pool (the branch
// that has a VM teardown plan); otherwise the pool is empty. Pool and storage
// delete routes are registered so the --purge paths run too.
func buildDestroyCmdWithContexts(
	t *testing.T, withVM bool, contexts func(ns destroyNS) map[string]*config.Context,
) (*cobra.Command, *cli.Deps, destroyNS) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	ns := newDestroyNS(t, path)
	cfg := &config.Config{
		Labs:     map[string]*config.Lab{"demo": cleanLab("demo")},
		Contexts: contexts(ns),
	}
	require.NoError(t, config.SaveForce(path, cfg))

	f, ac := destroyFakeClient(t)
	if withVM {
		destroyLabVMRoutes(f)
	} else {
		f.HandleJSON("GET /api2/json/cluster/resources", []any{})
	}
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	f.HandleFunc("DELETE /api2/json/storage/tank-lab-demo", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	deps := cli.GetDeps(cmd)
	deps.CtxName = destroyTestOuter
	return cmd, deps, ns
}

// recordKeychainDeletes replaces labDeleteSecretFn with a recorder for the
// duration of the test and returns the slice it appends "service/account" to.
func recordKeychainDeletes(t *testing.T) *[]string {
	t.Helper()
	var deleted []string
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(service, account string) error {
		deleted = append(deleted, service+"/"+account)
		return nil
	}
	t.Cleanup(func() { labDeleteSecretFn = orig })
	return &deleted
}

// destroyKeychainCase is one context shape lab destroy can meet. Strings may
// carry the destroyNS placeholders. wantDeleted is the keychain item a real
// run must delete, or "" for none; keepsContext reports that the lab-demo
// context must survive the real run; wantDry and wantReal are the clauses the
// dry-run and real-run messages must carry, and wantAlso lists further text
// both must carry.
type destroyKeychainCase struct {
	name         string
	contexts     func(ns destroyNS) map[string]*config.Context
	wantDeleted  string
	keepsContext bool
	wantDry      string
	wantReal     string
	wantAlso     []string
}

func destroyKeychainCases() []destroyKeychainCase {
	only := func(service string) func(ns destroyNS) map[string]*config.Context {
		return func(ns destroyNS) map[string]*config.Context {
			return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref(service))}
		}
	}
	foreign := func(name, service string) destroyKeychainCase {
		return destroyKeychainCase{
			name:         name,
			contexts:     only(service),
			keepsContext: true,
			wantDry: `would keep context "lab-demo" and its keychain secret ` + service + `/pmx@pve!pmx, ` +
				`because the secret belongs to another outer context or config ` +
				`(this invocation's lab secrets use {cur})`,
			wantReal: `kept context "lab-demo" and its keychain secret ` + service + `/pmx@pve!pmx, ` +
				`because the secret belongs to another outer context or config ` +
				`(this invocation's lab secrets use {cur})`,
			wantAlso: []string{
				"run `pmx lab context rm demo` with the outer context and config that registered it",
				"`pmx context rm lab-demo`",
				"`security delete-generic-password -s " + service + " -a 'pmx@pve!pmx'`",
			},
		}
	}

	return []destroyKeychainCase{
		{
			// Under a config with no lab-demo context, destroy used to delete
			// pmx-lab-demo anyway, which belongs to a same-named lab reached
			// from some other config.
			name: "no context",
			contexts: func(destroyNS) map[string]*config.Context {
				return map[string]*config.Context{"fleet": labTokenContext("${FLEET_TOKEN}")}
			},
			wantDry:  `context "lab-demo" not found, so no keychain secret would be removed`,
			wantReal: `context "lab-demo" not found, so no keychain secret was removed`,
		},
		{
			name:        "context references this namespace's item",
			contexts:    only("{cur}"),
			wantDeleted: "{cur}/pmx@pve!pmx",
			wantDry:     `would remove context "lab-demo" and its keychain secret {cur}/pmx@pve!pmx`,
			wantReal:    `removed context "lab-demo" and its keychain secret {cur}/pmx@pve!pmx`,
		},
		{
			// A same-named lab in another config may share the legacy item,
			// so destroy keeps it and prints the command sync prints.
			name:     "context references legacy item",
			contexts: only("pmx-lab-demo"),
			wantDry: `would remove context "lab-demo" but keep its legacy keychain secret pmx-lab-demo/pmx@pve!pmx, ` +
				`since a same-named lab in another config may use it. If nothing uses it, remove it with: ` +
				`security delete-generic-password -s pmx-lab-demo -a 'pmx@pve!pmx'`,
			wantReal: `removed context "lab-demo" but kept its legacy keychain secret pmx-lab-demo/pmx@pve!pmx, ` +
				`since a same-named lab in another config may use it. If nothing uses it, remove it with: ` +
				`security delete-generic-password -s pmx-lab-demo -a 'pmx@pve!pmx'`,
		},
		foreign("context references another outer context's item", "{otherOuter}"),
		foreign("context references another config's item", "{otherConfig}"),
		foreign("context references an item from before the config hash", "{preHash}"),
		{
			name:     "context references a non-lab keychain item",
			contexts: only("ops-root"),
			wantDry: `would remove context "lab-demo", which references no keychain secret of lab "demo", ` +
				`so no keychain secret would be removed`,
			wantReal: `removed context "lab-demo", which references no keychain secret of lab "demo", ` +
				`so no keychain secret was removed`,
		},
		{
			name: "context references another lab's namespaced item",
			contexts: func(ns destroyNS) map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo": labTokenContext(config.KeychainRef(strings.TrimSuffix(ns.cur, ".demo")+".demo2",
						labCtxAccount())),
				}
			},
			wantDry: `would remove context "lab-demo", which references no keychain secret of lab "demo", ` +
				`so no keychain secret would be removed`,
			wantReal: `removed context "lab-demo", which references no keychain secret of lab "demo", ` +
				`so no keychain secret was removed`,
		},
		{
			name: "context references an env secret",
			contexts: func(destroyNS) map[string]*config.Context {
				return map[string]*config.Context{"lab-demo": labTokenContext("${LAB_DEMO_TOKEN}")}
			},
			wantDry: `would remove context "lab-demo", which references no keychain secret of lab "demo", ` +
				`so no keychain secret would be removed`,
			wantReal: `removed context "lab-demo", which references no keychain secret of lab "demo", ` +
				`so no keychain secret was removed`,
		},
		{
			name: "item shared with another context",
			contexts: func(ns destroyNS) map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo":      labTokenContext(ns.ref("{cur}")),
					"lab-demo-copy": labTokenContext(ns.ref("{cur}")),
				}
			},
			wantDry: `would remove context "lab-demo" but keep its keychain secret {cur}/pmx@pve!pmx, ` +
				`which context "lab-demo-copy" also references`,
			wantReal: `removed context "lab-demo" but kept its keychain secret {cur}/pmx@pve!pmx, ` +
				`which context "lab-demo-copy" also references`,
		},
	}
}

// destroyBranches are the destroy branches every keychain case runs through:
// the "nothing to destroy" branch, the --purge branch with no VM, and the
// branch with a VM teardown plan.
var destroyBranches = []struct {
	name   string
	withVM bool
	extra  []string
}{
	{"no VM", false, nil},
	{"no VM purge", false, []string{"--purge"}},
	{"VM", true, nil},
}

// TestDestroy_KeychainCleanup_FollowsContextReference runs every context
// shape through every destroy branch, as a dry run and as a real run. A dry
// run must never touch the keychain or config and must preview exactly the
// clause the real run then reports; a real run must delete only an item of
// this invocation's namespace that the lab-demo context itself references, and
// must keep a context whose item belongs to another namespace.
func TestDestroy_KeychainCleanup_FollowsContextReference(t *testing.T) {
	for _, tc := range destroyKeychainCases() {
		for _, b := range destroyBranches {
			t.Run(tc.name+"/"+b.name+"/dry-run", func(t *testing.T) {
				deleted := recordKeychainDeletes(t)
				cmd, deps, ns := buildDestroyCmdWithContexts(t, b.withVM, tc.contexts)
				_, hadCtx := deps.Cfg.Contexts["lab-demo"]

				out, stderr, err := destroyRun(t, cmd, append([]string{"demo", "--dry-run"}, b.extra...)...)
				require.NoError(t, err)

				assert.Empty(t, *deleted, "--dry-run must not touch the keychain")
				_, stillHasCtx := deps.Cfg.Contexts["lab-demo"]
				assert.Equal(t, hadCtx, stillHasCtx, "--dry-run must not change contexts")
				assert.Contains(t, out.String(), "[dry-run]")
				assert.Contains(t, out.String(), "; "+ns.expand(tc.wantDry))
				for _, also := range tc.wantAlso {
					assert.Contains(t, out.String(), ns.expand(also))
				}
				assert.NotContains(t, stderr.String(), "[y/N]", "--dry-run must not prompt")
			})

			t.Run(tc.name+"/"+b.name+"/real", func(t *testing.T) {
				deleted := recordKeychainDeletes(t)
				cmd, deps, ns := buildDestroyCmdWithContexts(t, b.withVM, tc.contexts)
				before := map[string]string{}
				for n, c := range deps.Cfg.Contexts {
					before[n] = c.Auth.Secret
				}

				out, _, err := destroyRun(t, cmd, append([]string{"demo", "--yes"}, b.extra...)...)
				require.NoError(t, err)

				if tc.wantDeleted == "" {
					assert.Empty(t, *deleted, "no keychain item may be deleted")
				} else {
					assert.Equal(t, []string{ns.expand(tc.wantDeleted)}, *deleted,
						"exactly the item the context references must be deleted")
				}
				_, stillHasCtx := deps.Cfg.Contexts["lab-demo"]
				_, hadCtx := before["lab-demo"]
				assert.Equal(t, tc.keepsContext && hadCtx, stillHasCtx, "lab-demo context survival")
				assert.Contains(t, out.String(), "; "+ns.expand(tc.wantReal))
				for _, also := range tc.wantAlso {
					assert.Contains(t, out.String(), ns.expand(also))
				}
				assert.NotContains(t, out.String(), "context cleanup warning")

				// Every other context is left exactly as it was, and a kept
				// lab-demo context keeps its reference.
				for n, secret := range before {
					if n == "lab-demo" && !tc.keepsContext {
						continue
					}
					require.NotNil(t, deps.Cfg.Contexts[n], "context %q must survive", n)
					assert.Equal(t, secret, deps.Cfg.Contexts[n].Auth.Secret)
				}
			})
		}
	}
}

// TestDestroy_ForeignContextUnderAnotherOuterContext is the reported
// scenario end to end: `pmx -c fleet-east lab destroy wayne` in a config
// whose lab-wayne context was registered through fleet-west. In both the
// no-VM and the VM branch, destroy keeps the context and the fleet-west item,
// and the saved config still carries the context.
func TestDestroy_ForeignContextUnderAnotherOuterContext(t *testing.T) {
	for _, b := range destroyBranches {
		t.Run(b.name, func(t *testing.T) {
			deleted := recordKeychainDeletes(t)
			cmd, deps, ns := buildDestroyCmdWithContexts(t, b.withVM, func(ns destroyNS) map[string]*config.Context {
				return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{otherOuter}"))}
			})
			deps.CtxName = "fleet-east"

			out, _, err := destroyRun(t, cmd, append([]string{"demo", "--yes"}, b.extra...)...)
			require.NoError(t, err)

			assert.Empty(t, *deleted, "another outer context's item must never be deleted")
			require.NotNil(t, deps.Cfg.Contexts["lab-demo"])
			assert.Equal(t, ns.ref("{otherOuter}"), deps.Cfg.Contexts["lab-demo"].Auth.Secret)
			assert.Contains(t, out.String(), `kept context "lab-demo" and its keychain secret `+ns.otherOuter)

			reloaded, err := config.Load(deps.ConfigPath)
			require.NoError(t, err)
			assert.NotNil(t, reloaded.Contexts["lab-demo"], "the kept context must still be on disk")
		})
	}
}

// TestDestroy_MovedConfigKeepsItem covers a config file that moved after its
// lab context was registered: the namespace now hashes a different path, so
// the item no longer matches and destroy keeps it (fail safe) and prints the
// removal command.
func TestDestroy_MovedConfigKeepsItem(t *testing.T) {
	deleted := recordKeychainDeletes(t)
	cmd, deps, ns := buildDestroyCmdWithContexts(t, false, func(ns destroyNS) map[string]*config.Context {
		return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{cur}"))}
	})

	moved := filepath.Join(t.TempDir(), "moved.yml")
	require.NoError(t, os.Rename(deps.ConfigPath, moved))
	deps.ConfigPath = moved

	out, _, err := destroyRun(t, cmd, "demo", "--yes")
	require.NoError(t, err)

	assert.Empty(t, *deleted)
	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
	assert.Contains(t, out.String(), `kept context "lab-demo" and its keychain secret `+ns.cur)
	assert.Contains(t, out.String(), "security delete-generic-password -s "+ns.cur+" -a 'pmx@pve!pmx'")
}

// TestDestroy_NoVMBranchConfirmsContextCleanup covers the confirmation gate
// in the branch with no VM to destroy: removing the context (and its item)
// needs --yes or an interactive yes, a refusal or a closed stdin changes
// nothing, and nothing prompts when there is nothing to clean up.
func TestDestroy_NoVMBranchConfirmsContextCleanup(t *testing.T) {
	withCur := func(ns destroyNS) map[string]*config.Context {
		return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{cur}"))}
	}

	for _, answer := range []string{"n\n", ""} {
		t.Run("declined "+strings.TrimSpace(answer), func(t *testing.T) {
			deleted := recordKeychainDeletes(t)
			cmd, deps, ns := buildDestroyCmdWithContexts(t, false, withCur)
			cmd.SetIn(strings.NewReader(answer))

			out, stderr, err := destroyRun(t, cmd, "demo")
			require.NoError(t, err)

			assert.Contains(t, stderr.String(),
				`Clean up lab "demo" (would remove context "lab-demo" and its keychain secret `+ns.cur+`/pmx@pve!pmx)? [y/N]`)
			assert.Contains(t, out.String(), "Aborted.")
			assert.Empty(t, *deleted)
			assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
		})
	}

	t.Run("accepted", func(t *testing.T) {
		deleted := recordKeychainDeletes(t)
		cmd, deps, ns := buildDestroyCmdWithContexts(t, false, withCur)
		cmd.SetIn(strings.NewReader("y\n"))

		out, stderr, err := destroyRun(t, cmd, "demo")
		require.NoError(t, err)

		assert.Contains(t, stderr.String(), "[y/N]")
		assert.Equal(t, []string{ns.cur + "/pmx@pve!pmx"}, *deleted)
		assert.Nil(t, deps.Cfg.Contexts["lab-demo"])
		assert.Contains(t, out.String(), `removed context "lab-demo" and its keychain secret`)
	})

	t.Run("nothing to clean up", func(t *testing.T) {
		for name, contexts := range map[string]func(destroyNS) map[string]*config.Context{
			"no context": func(destroyNS) map[string]*config.Context { return map[string]*config.Context{} },
			"foreign context": func(ns destroyNS) map[string]*config.Context {
				return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{otherOuter}"))}
			},
		} {
			t.Run(name, func(t *testing.T) {
				deleted := recordKeychainDeletes(t)
				cmd, _, _ := buildDestroyCmdWithContexts(t, false, contexts)
				cmd.SetIn(strings.NewReader(""))

				out, stderr, err := destroyRun(t, cmd, "demo")
				require.NoError(t, err)
				assert.NotContains(t, stderr.String(), "[y/N]", "nothing to remove, so nothing to confirm")
				assert.NotContains(t, out.String(), "Aborted.")
				assert.Empty(t, *deleted)
			})
		}
	})

	t.Run("keep-context", func(t *testing.T) {
		deleted := recordKeychainDeletes(t)
		cmd, deps, _ := buildDestroyCmdWithContexts(t, false, withCur)
		cmd.SetIn(strings.NewReader(""))

		_, stderr, err := destroyRun(t, cmd, "demo", "--keep-context")
		require.NoError(t, err)
		assert.NotContains(t, stderr.String(), "[y/N]")
		assert.Empty(t, *deleted)
		assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
	})
}

// TestDestroy_VMBranchPromptNamesContextCleanup covers the VM branch: the
// confirmation the teardown already required now names the context cleanup
// too, and declining it changes nothing.
func TestDestroy_VMBranchPromptNamesContextCleanup(t *testing.T) {
	deleted := recordKeychainDeletes(t)
	cmd, deps, ns := buildDestroyCmdWithContexts(t, true, func(ns destroyNS) map[string]*config.Context {
		return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{cur}"))}
	})
	cmd.SetIn(strings.NewReader("n\n"))

	out, stderr, err := destroyRun(t, cmd, "demo")
	require.NoError(t, err)
	assert.Contains(t, stderr.String(), `Destroy lab "demo" (node 0 VM 100 on node "pve1"; `+
		`would remove context "lab-demo" and its keychain secret `+ns.cur+`/pmx@pve!pmx)? [y/N]`)
	assert.Contains(t, out.String(), "Aborted.")
	assert.Empty(t, *deleted)
	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
}

// TestDestroy_KeychainCleanup_UnsupportedPlatform covers a build without a
// keychain backend: the preview and the real run both say the secret is not
// removed, and the context still goes.
func TestDestroy_KeychainCleanup_UnsupportedPlatform(t *testing.T) {
	origSupported := labKeychainSupported
	labKeychainSupported = func() bool { return false }
	t.Cleanup(func() { labKeychainSupported = origSupported })

	contexts := func(ns destroyNS) map[string]*config.Context {
		return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{cur}"))}
	}

	deleted := recordKeychainDeletes(t)
	cmd, _, ns := buildDestroyCmdWithContexts(t, false, contexts)
	out, _, err := destroyRun(t, cmd, "demo", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `would remove context "lab-demo", but keychain secret `+
		ns.cur+`/pmx@pve!pmx would not be removed because this platform has no keychain`)

	cmd, deps, ns := buildDestroyCmdWithContexts(t, false, contexts)
	out, _, err = destroyRun(t, cmd, "demo", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `removed context "lab-demo", but keychain secret `+
		ns.cur+`/pmx@pve!pmx was not removed because this platform has no keychain`)
	assert.Empty(t, *deleted)
	assert.Nil(t, deps.Cfg.Contexts["lab-demo"])
}

// TestDestroy_KeychainDeleteFailureKeepsContext covers a real keychain error:
// the context (the only trail to the item) stays, and destroy reports the
// failure as a cleanup warning rather than failing a completed teardown.
func TestDestroy_KeychainDeleteFailureKeepsContext(t *testing.T) {
	orig := labDeleteSecretFn
	labDeleteSecretFn = func(string, string) error { return assert.AnError }
	t.Cleanup(func() { labDeleteSecretFn = orig })

	cmd, deps, _ := buildDestroyCmdWithContexts(t, false, func(ns destroyNS) map[string]*config.Context {
		return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{cur}"))}
	})
	out, _, err := destroyRun(t, cmd, "demo", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "context cleanup warning")
	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
}

// runContextRm runs `pmx lab context rm <name>` against deps.
func runContextRm(t *testing.T, deps *cli.Deps, name string) (string, error) {
	t.Helper()
	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))
	return runGuestCmd(t, root, "rm", name)
}

// TestContextRm_KeepsLegacyItem covers rm of a context that references the
// legacy item: the context goes, the item stays, and the output carries the
// removal command sync prints.
func TestContextRm_KeepsLegacyItem(t *testing.T) {
	deleted := recordKeychainDeletes(t)
	deps := contextRmDeps(t, "demo", false)
	deps.Cfg.Contexts["lab-demo"] = labTokenContext("keychain:pmx-lab-demo/pmx@pve!pmx")

	out, err := runContextRm(t, deps, "demo")
	require.NoError(t, err)

	assert.Empty(t, *deleted, "rm must never delete a legacy item")
	assert.Nil(t, deps.Cfg.Contexts["lab-demo"])
	assert.Contains(t, out, `removed context "lab-demo" but kept its legacy keychain secret pmx-lab-demo/pmx@pve!pmx`)
	assert.Contains(t, out, "security delete-generic-password -s pmx-lab-demo -a 'pmx@pve!pmx'")

	reloaded, err := config.Load(deps.ConfigPath)
	require.NoError(t, err)
	assert.Nil(t, reloaded.Contexts["lab-demo"], "removal must be saved")
}

// TestContextRm_KeepsForeignContextAndItem covers rm of a context whose item
// belongs to another outer context, another config, or an invocation with no
// outer context at all: neither the context nor the item is touched, and the
// output names the commands that would remove them.
func TestContextRm_KeepsForeignContextAndItem(t *testing.T) {
	cases := map[string]func(t *testing.T, deps *cli.Deps) string{
		"another outer context": func(t *testing.T, deps *cli.Deps) string {
			t.Helper()
			hash, err := labConfigHash(deps.ConfigPath)
			require.NoError(t, err)
			return labKeychainService("fleet-west", hash, "demo")
		},
		"another config": func(t *testing.T, deps *cli.Deps) string {
			t.Helper()
			hash, err := labConfigHash(filepath.Join(t.TempDir(), "config.yml"))
			require.NoError(t, err)
			return labKeychainService("fleet", hash, "demo")
		},
		"no outer context to compare with": func(t *testing.T, deps *cli.Deps) string {
			t.Helper()
			service := labTestService(t, deps, "demo")
			deps.CtxName = ""
			deps.Cfg.CurrentContext = ""
			return service
		},
	}

	for name, serviceFor := range cases {
		t.Run(name, func(t *testing.T) {
			deleted := recordKeychainDeletes(t)
			deps := contextRmDeps(t, "demo", false)
			service := serviceFor(t, deps)
			ref := config.KeychainRef(service, labCtxAccount())
			deps.Cfg.Contexts["lab-demo"] = labTokenContext(ref)

			out, err := runContextRm(t, deps, "demo")
			require.NoError(t, err)

			assert.Empty(t, *deleted)
			require.NotNil(t, deps.Cfg.Contexts["lab-demo"])
			assert.Equal(t, ref, deps.Cfg.Contexts["lab-demo"].Auth.Secret)
			assert.Contains(t, out, `kept context "lab-demo" and its keychain secret `+service+`/pmx@pve!pmx`)
			assert.Contains(t, out, "`pmx context rm lab-demo`")
			assert.Contains(t, out, "security delete-generic-password -s "+service+" -a 'pmx@pve!pmx'")
		})
	}
}

// TestDestroy_PurgeDatasetGatesContextCleanup covers --purge-dataset together
// with the context cleanup. Every dataset refusal (the node choice and the
// identity preflight) comes before the prompt and before the context or its
// keychain item is touched, the one prompt names both the dataset and the
// context cleanup, and --dry-run previews both without prompting or probing.
func TestDestroy_PurgeDatasetGatesContextCleanup(t *testing.T) {
	withCur := func(ns destroyNS) map[string]*config.Context {
		return map[string]*config.Context{"lab-demo": labTokenContext(ns.ref("{cur}"))}
	}

	// wire builds the destroy of lab demo with the dataset's ssh target wired
	// to a fake that answers the identity preflight as node pve1.
	wire := func(t *testing.T, withVM bool) (*cobra.Command, *cli.Deps, destroyNS, *exec.FakeRunner, *[]string) {
		t.Helper()
		deleted := recordKeychainDeletes(t)
		cmd, deps, ns := buildDestroyCmdWithContexts(t, withVM, withCur)
		deps.Ctx = &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}}
		fake := exec.Fake()
		deps.Runner = newIdentityRunner(fake)
		return cmd, deps, ns, fake, deleted
	}

	t.Run("a refused preflight keeps the context and its item", func(t *testing.T) {
		cmd, deps, _, fake, deleted := wire(t, true)
		replyIdentity(deps, "pve7", preflightFingerprint("pve1"))

		_, _, err := destroyRun(t, cmd, "demo", "--yes", "--purge-dataset")
		require.Error(t, err)
		assert.ErrorContains(t, err, `reached a machine named "pve7", not node "pve1"`)
		assert.Empty(t, *deleted, "no keychain item is deleted when the preflight refuses")
		assert.NotNil(t, deps.Cfg.Contexts["lab-demo"], "the context survives a refused run")
		assert.Empty(t, fake.Calls, "no zfs command is issued")
	})

	t.Run("no VMs and no --node refuses before any cleanup", func(t *testing.T) {
		cmd, deps, _, fake, deleted := wire(t, false)
		cmd.SetIn(strings.NewReader("y\n"))

		_, stderr, err := destroyRun(t, cmd, "demo", "--purge-dataset")
		require.Error(t, err)
		assert.ErrorContains(t, err, "found no VM of the lab")
		assert.NotContains(t, stderr.String(), "[y/N]", "a refused run is not offered for confirmation")
		assert.Empty(t, *deleted)
		assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
		assert.Empty(t, identityProbes(t, deps))
		assert.Empty(t, fake.Calls)
	})

	t.Run("the prompt names the dataset and the context cleanup, after the preflight", func(t *testing.T) {
		cmd, deps, ns, fake, deleted := wire(t, true)
		cmd.SetIn(strings.NewReader("n\n"))

		out, stderr, err := destroyRun(t, cmd, "demo", "--purge-dataset")
		require.NoError(t, err)
		assert.Contains(t, stderr.String(), "ZFS dataset tank/labs/demo on node pve1 (192.168.1.10) (recursive); "+
			`would remove context "lab-demo" and its keychain secret `+ns.cur+`/pmx@pve!pmx)? [y/N]`)
		assert.Contains(t, out.String(), "Aborted.")
		assert.Len(t, identityProbes(t, deps), 1, "the preflight ran before the prompt")
		assert.Empty(t, *deleted)
		assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
		assert.Empty(t, fake.Calls)
	})

	t.Run("dry-run previews both without prompting or probing", func(t *testing.T) {
		cmd, deps, ns, fake, deleted := wire(t, true)
		cmd.SetIn(strings.NewReader(""))

		out, stderr, err := destroyRun(t, cmd, "demo", "--dry-run", "--purge-dataset")
		require.NoError(t, err)
		assert.Contains(t, out.String(), "[dry-run] would destroy lab")
		assert.Contains(t, out.String(), "ZFS dataset tank/labs/demo on node pve1 (192.168.1.10) (recursive)")
		assert.Contains(t, out.String(), `would remove context "lab-demo" and its keychain secret `+ns.cur+`/pmx@pve!pmx`)
		assert.NotContains(t, stderr.String(), "[y/N]")
		assert.Empty(t, identityProbes(t, deps))
		assert.Empty(t, *deleted)
		assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
		assert.Empty(t, fake.Calls)
	})

	t.Run("an accepted run purges the dataset, then removes the context and its item", func(t *testing.T) {
		cmd, deps, ns, fake, deleted := wire(t, true)

		out, _, err := destroyRun(t, cmd, "demo", "--yes", "--purge-dataset")
		require.NoError(t, err)
		require.Len(t, fake.Calls, 1, "one zfs destroy")
		assert.Contains(t, fake.Calls[0].Args, "root@192.168.1.10")
		assert.Equal(t, []string{ns.cur + "/pmx@pve!pmx"}, *deleted)
		assert.Nil(t, deps.Cfg.Contexts["lab-demo"])
		assert.Contains(t, out.String(), `removed context "lab-demo" and its keychain secret`)
	})
}
