package lab

import (
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
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

// buildDestroyCmdWithContexts builds a destroy command for lab "demo" whose
// config carries exactly contexts, with an empty lab pool (no VMs) and
// pool/storage delete routes so both the plain and the --purge paths run.
func buildDestroyCmdWithContexts(t *testing.T, contexts map[string]*config.Context) (*cobra.Command, *cli.Deps) {
	t.Helper()

	cfg := &config.Config{
		Labs:     map[string]*config.Lab{"demo": cleanLab("demo")},
		Contexts: contexts,
	}
	path := writeConfig(t, cfg)
	f, ac := destroyFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/resources", []any{})
	f.HandleFunc("DELETE /api2/json/pools", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	f.HandleFunc("DELETE /api2/json/storage/tank-lab-demo", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})

	cmd := destroyTestCmd(t, path, ac, "pve1")
	return cmd, cli.GetDeps(cmd)
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

// destroyKeychainCase is one context shape lab destroy can meet, with the
// keychain item a real run must delete (or "" for none) and the clause the
// dry-run and real-run messages must carry.
type destroyKeychainCase struct {
	name        string
	contexts    func() map[string]*config.Context
	wantDeleted string
	wantDry     string
	wantReal    string
}

func destroyKeychainCases() []destroyKeychainCase {
	return []destroyKeychainCase{
		{
			// The reported defect: under a config with no lab-demo context,
			// destroy used to delete pmx-lab-demo anyway, which belongs to a
			// same-named lab reached from some other config.
			name: "no context",
			contexts: func() map[string]*config.Context {
				return map[string]*config.Context{
					"fleet": labTokenContext("${FLEET_TOKEN}"),
				}
			},
			wantDry:  `context "lab-demo" not found, so no keychain secret would be removed`,
			wantReal: `context "lab-demo" not found, so no keychain secret was removed`,
		},
		{
			name: "context references legacy item",
			contexts: func() map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo": labTokenContext("keychain:pmx-lab-demo/pmx@pve!pmx"),
				}
			},
			wantDeleted: "pmx-lab-demo/pmx@pve!pmx",
			wantDry:     `would remove context "lab-demo" and its keychain secret pmx-lab-demo/pmx@pve!pmx`,
			wantReal:    `removed context "lab-demo" and its keychain secret pmx-lab-demo/pmx@pve!pmx`,
		},
		{
			name: "context references namespaced item",
			contexts: func() map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo": labTokenContext("keychain:pmx-lab.fleet.demo/pmx@pve!pmx"),
				}
			},
			wantDeleted: "pmx-lab.fleet.demo/pmx@pve!pmx",
			wantDry:     `would remove context "lab-demo" and its keychain secret pmx-lab.fleet.demo/pmx@pve!pmx`,
			wantReal:    `removed context "lab-demo" and its keychain secret pmx-lab.fleet.demo/pmx@pve!pmx`,
		},
		{
			name: "context references a non-lab keychain item",
			contexts: func() map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo": labTokenContext("keychain:ops-root/root@pam!ci"),
				}
			},
			wantDry: `would remove context "lab-demo"; it references no keychain secret of lab "demo", ` +
				`so no keychain secret would be removed`,
			wantReal: `removed context "lab-demo"; it references no keychain secret of lab "demo", ` +
				`so no keychain secret was removed`,
		},
		{
			name: "context references another lab's namespaced item",
			contexts: func() map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo": labTokenContext("keychain:pmx-lab.fleet.demo2/pmx@pve!pmx"),
				}
			},
			wantDry: `would remove context "lab-demo"; it references no keychain secret of lab "demo", ` +
				`so no keychain secret would be removed`,
			wantReal: `removed context "lab-demo"; it references no keychain secret of lab "demo", ` +
				`so no keychain secret was removed`,
		},
		{
			name: "context references an env secret",
			contexts: func() map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo": labTokenContext("${LAB_DEMO_TOKEN}"),
				}
			},
			wantDry: `would remove context "lab-demo"; it references no keychain secret of lab "demo", ` +
				`so no keychain secret would be removed`,
			wantReal: `removed context "lab-demo"; it references no keychain secret of lab "demo", ` +
				`so no keychain secret was removed`,
		},
		{
			name: "item shared with another context",
			contexts: func() map[string]*config.Context {
				return map[string]*config.Context{
					"lab-demo":      labTokenContext("keychain:pmx-lab-demo/pmx@pve!pmx"),
					"lab-demo-copy": labTokenContext("keychain:pmx-lab-demo/pmx@pve!pmx"),
				}
			},
			wantDry: `would remove context "lab-demo" but keep its keychain secret pmx-lab-demo/pmx@pve!pmx, ` +
				`which context "lab-demo-copy" also references`,
			wantReal: `removed context "lab-demo" but kept its keychain secret pmx-lab-demo/pmx@pve!pmx, ` +
				`which context "lab-demo-copy" also references`,
		},
	}
}

// TestDestroy_KeychainCleanup_FollowsContextReference runs every context
// shape through both destroy paths (the "nothing to destroy" path, and the
// --purge path that has a plan), as a dry run and as a real run. A dry run
// must never touch the keychain or config and must preview exactly the clause
// the real run then reports; a real run must delete only the item the
// lab-demo context itself references.
func TestDestroy_KeychainCleanup_FollowsContextReference(t *testing.T) {
	paths := []struct {
		name  string
		extra []string
	}{
		{"nothing to destroy", nil},
		{"purge plan", []string{"--purge"}},
	}

	for _, tc := range destroyKeychainCases() {
		for _, p := range paths {
			t.Run(tc.name+"/"+p.name+"/dry-run", func(t *testing.T) {
				deleted := recordKeychainDeletes(t)
				cmd, deps := buildDestroyCmdWithContexts(t, tc.contexts())
				_, hadCtx := deps.Cfg.Contexts["lab-demo"]

				out, _, err := destroyRun(t, cmd, append([]string{"demo", "--dry-run"}, p.extra...)...)
				require.NoError(t, err)

				assert.Empty(t, *deleted, "--dry-run must not touch the keychain")
				_, stillHasCtx := deps.Cfg.Contexts["lab-demo"]
				assert.Equal(t, hadCtx, stillHasCtx, "--dry-run must not change contexts")
				assert.Contains(t, out.String(), "[dry-run]")
				assert.Contains(t, out.String(), "; "+tc.wantDry)
			})

			t.Run(tc.name+"/"+p.name+"/real", func(t *testing.T) {
				deleted := recordKeychainDeletes(t)
				cmd, deps := buildDestroyCmdWithContexts(t, tc.contexts())

				out, _, err := destroyRun(t, cmd, append([]string{"demo", "--yes"}, p.extra...)...)
				require.NoError(t, err)

				if tc.wantDeleted == "" {
					assert.Empty(t, *deleted, "no keychain item may be deleted")
				} else {
					assert.Equal(t, []string{tc.wantDeleted}, *deleted,
						"exactly the item the context references must be deleted")
				}
				_, stillHasCtx := deps.Cfg.Contexts["lab-demo"]
				assert.False(t, stillHasCtx, "no lab-demo context may remain after a real destroy")
				assert.Contains(t, out.String(), "; "+tc.wantReal)
				assert.NotContains(t, out.String(), "context cleanup warning")

				// Every other context is left exactly as it was.
				for n := range tc.contexts() {
					if n != "lab-demo" {
						assert.NotNil(t, deps.Cfg.Contexts[n], "context %q must survive", n)
					}
				}
			})
		}
	}
}

// TestDestroy_KeychainCleanup_UnsupportedPlatform covers a build without a
// keychain backend: the preview and the real run both say the secret is not
// removed, and the context still goes.
func TestDestroy_KeychainCleanup_UnsupportedPlatform(t *testing.T) {
	origSupported := labKeychainSupported
	labKeychainSupported = func() bool { return false }
	t.Cleanup(func() { labKeychainSupported = origSupported })

	contexts := func() map[string]*config.Context {
		return map[string]*config.Context{
			"lab-demo": labTokenContext("keychain:pmx-lab.fleet.demo/pmx@pve!pmx"),
		}
	}

	deleted := recordKeychainDeletes(t)
	cmd, _ := buildDestroyCmdWithContexts(t, contexts())
	out, _, err := destroyRun(t, cmd, "demo", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `would remove context "lab-demo"; keychain secret `+
		`pmx-lab.fleet.demo/pmx@pve!pmx would not be removed because this platform has no keychain`)

	cmd, deps := buildDestroyCmdWithContexts(t, contexts())
	out, _, err = destroyRun(t, cmd, "demo", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `removed context "lab-demo"; keychain secret `+
		`pmx-lab.fleet.demo/pmx@pve!pmx was not removed because this platform has no keychain`)
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

	cmd, deps := buildDestroyCmdWithContexts(t, map[string]*config.Context{
		"lab-demo": labTokenContext("keychain:pmx-lab.fleet.demo/pmx@pve!pmx"),
	})
	out, _, err := destroyRun(t, cmd, "demo", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "context cleanup warning")
	assert.NotNil(t, deps.Cfg.Contexts["lab-demo"])
}
