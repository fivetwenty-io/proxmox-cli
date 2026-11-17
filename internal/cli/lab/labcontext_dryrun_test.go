package lab

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

const (
	emptyPveumList = "[]"
	pmxUserList    = `[{"userid":"root@pam"},{"userid":"pmx@pve"}]`
	pmxACLList     = `[{"path":"/","type":"user","ugid":"pmx@pve","roleid":"Administrator","propagate":1}]`
)

// dryRunFake scripts the four reads a dry-run sync makes, in order: the user
// list, the ACL list, the certificate fingerprint, and the hostname.
func dryRunFake(users, acls string) *exec.FakeRunner {
	return exec.Fake(
		exec.FakeResponse{Stdout: users},                                              // pveum user list
		exec.FakeResponse{Stdout: acls},                                               // pveum acl list
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + syncTestFingerprint + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                                     // hostname
	)
}

// assertNoMutatingSSH fails when any command the fake saw could create or
// destroy something on the node.
func assertNoMutatingSSH(t *testing.T, fake *exec.FakeRunner) {
	t.Helper()
	for _, c := range fake.Calls {
		remote := c.Args[len(c.Args)-1]
		for _, mutating := range []string{"user add", "acl modify", "token remove", "token add"} {
			assert.NotContains(t, remote, mutating, "a dry run must not run %q on the node", mutating)
		}
	}
}

func readConfigFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func demoLab() *config.Lab {
	lab := multiNodeTestLab("demo", 1, "never")
	// multiNodeTestLab does not set Name, and syncLabContext is called directly
	// here without the config loader's map-key defaulting.
	lab.Name = "demo"
	return lab
}

func renderPlan(t *testing.T, deps *cli.Deps, res labSyncResult) string {
	t.Helper()
	require.NotNil(t, res.Plan)
	var buf bytes.Buffer
	require.NoError(t, deps.Out.Render(&buf, labSyncPlanResult(res.ContextName, res.Plan), output.FormatPlain))
	return buf.String()
}

// TestSyncLabContext_DryRunFreshReportsPlanAndMutatesNothing covers a lab with
// no context yet: the plan says it would create the user, the ACL, a new
// token in the namespaced keychain item, and the context, while the node, the
// keychain, the in-memory config, and the config file all stay as they were.
func TestSyncLabContext_DryRunFreshReportsPlanAndMutatesNothing(t *testing.T) {
	fake := dryRunFake(emptyPveumList, emptyPveumList)
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)
	before := readConfigFile(t, deps.ConfigPath)

	res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true})
	require.NoError(t, err)

	assert.False(t, res.Rotated, "nothing was rotated")
	require.NotNil(t, res.Plan)
	assert.False(t, res.Plan.UserExists)
	assert.False(t, res.Plan.ACLExists)
	assert.False(t, res.Plan.ContextExists)
	assert.False(t, res.Plan.Reuse)
	assert.Equal(t, labTestService(t, deps, "demo"), res.Plan.StoreService)
	assert.Equal(t, "pmx@pve!pmx", res.Plan.StoreAccount)
	assert.Contains(t, res.Plan.Changed, "secret")

	out := renderPlan(t, deps, res)
	assert.Contains(t, out, "would be created")
	assert.Contains(t, out, "would be rotated")
	assert.Contains(t, out, "would store the new secret in item "+labTestService(t, deps, "demo")+"/pmx@pve!pmx")
	assert.Contains(t, out, `context "lab-demo"`)
	assert.Contains(t, out, "[dry-run]")

	assertNoMutatingSSH(t, fake)
	assert.Len(t, fake.Calls, 4, "a dry run makes only the four reads")
	assert.Empty(t, *stored, "no secret may be stored")
	assert.Empty(t, *deleted, "nothing may be deleted from the keychain")
	assert.NotContains(t, deps.Cfg.Contexts, "lab-demo", "the in-memory config must not gain the context")
	assert.Equal(t, before, readConfigFile(t, deps.ConfigPath), "the config file must not be written")
}

// TestSyncLabContext_DryRunReportsExistingUserAndACL covers a node that already
// has the user and the ACL, which the plan reports as skipped.
func TestSyncLabContext_DryRunReportsExistingUserAndACL(t *testing.T) {
	fake := dryRunFake(pmxUserList, pmxACLList)
	cmd, deps := syncTestDeps(t, fake)

	res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true})
	require.NoError(t, err)

	assert.True(t, res.Plan.UserExists)
	assert.True(t, res.Plan.ACLExists)
	out := renderPlan(t, deps, res)
	assert.Equal(t, 2, strings.Count(out, "already exists (skip)"))
	assertNoMutatingSSH(t, fake)
}

// TestSyncLabContext_DryRunReusesWorkingSecretAndChangesNothing covers a
// context whose stored legacy secret still authenticates. The plan keeps it,
// names the reference it keeps, and the context is untouched.
func TestSyncLabContext_DryRunReusesWorkingSecretAndChangesNothing(t *testing.T) {
	fake := dryRunFake(pmxUserList, pmxACLList)
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	legacy := "keychain:pmx-lab-demo/pmx@pve!pmx"
	deps.Cfg.Contexts["lab-demo"] = labTokenContext(legacy)
	existing := *deps.Cfg.Contexts["lab-demo"]
	before := readConfigFile(t, deps.ConfigPath)

	res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true})
	require.NoError(t, err)

	require.NotNil(t, res.Plan)
	assert.True(t, res.Plan.Reuse)
	assert.Equal(t, legacy, res.Plan.ReusedRef)
	assert.Nil(t, res.Plan.Dropped)
	out := renderPlan(t, deps, res)
	assert.Contains(t, out, "would be reused")
	assert.Contains(t, out, "the context keeps "+legacy)

	assertNoMutatingSSH(t, fake)
	assert.Empty(t, *stored)
	assert.Equal(t, existing, *deps.Cfg.Contexts["lab-demo"], "the stored context must not change")
	assert.Equal(t, before, readConfigFile(t, deps.ConfigPath))
}

// TestSyncLabContext_DryRunRotatesWhenStoredSecretIsRejected covers a context
// whose secret no longer authenticates: the plan says it would rotate, the
// in-memory context is put back after the simulated upsert, and the previous
// legacy item is reported as one the context would stop referencing.
func TestSyncLabContext_DryRunRotatesWhenStoredSecretIsRejected(t *testing.T) {
	fake := dryRunFake(pmxUserList, pmxACLList)
	cmd, deps := syncTestDeps(t, fake)
	stored := recordKeychainStores(t)
	deleted := recordKeychainDeletes(t)
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error { return errors.New("401 unauthorized") }
	legacy := "keychain:pmx-lab-demo/pmx@pve!pmx"
	deps.Cfg.Contexts["lab-demo"] = labTokenContext(legacy)
	existing := *deps.Cfg.Contexts["lab-demo"]
	before := readConfigFile(t, deps.ConfigPath)

	res, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true})
	require.NoError(t, err)

	require.NotNil(t, res.Plan)
	assert.False(t, res.Plan.Reuse)
	require.NotNil(t, res.Plan.Dropped)
	assert.Equal(t, "pmx-lab-demo", res.Plan.Dropped.service)
	out := renderPlan(t, deps, res)
	assert.Contains(t, out, "would stop being referenced")
	assert.Contains(t, out, "pmx-lab-demo/pmx@pve!pmx")

	assertNoMutatingSSH(t, fake)
	assert.Empty(t, *stored)
	assert.Empty(t, *deleted, "the legacy item is never deleted")
	assert.Equal(t, existing, *deps.Cfg.Contexts["lab-demo"], "the stored context must be restored")
	assert.Equal(t, before, readConfigFile(t, deps.ConfigPath))
}

// TestSyncLabContext_DryRunKeepsTheRealRunGuards covers the refusals a real
// run makes before it changes anything. A dry run must raise the same ones, so
// a preview never promises a run that would be refused.
func TestSyncLabContext_DryRunKeepsTheRealRunGuards(t *testing.T) {
	t.Run("operator-owned context", func(t *testing.T) {
		fake := exec.Fake()
		cmd, deps := syncTestDeps(t, fake)
		deps.Cfg.Contexts["lab-demo"] = &config.Context{
			Host: "192.168.50.10", Port: 8006, Protocol: "https", Product: config.ProductPVE,
			Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "ops", Secret: "keychain:ops/root@pam!ops"},
		}

		_, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `refusing to overwrite existing context "lab-demo"`)
		assert.Empty(t, fake.Calls, "the refusal comes before anything runs on the node")
	})

	t.Run("another context references the target item", func(t *testing.T) {
		fake := dryRunFake(pmxUserList, pmxACLList)
		cmd, deps := syncTestDeps(t, fake)
		stored := recordKeychainStores(t)
		deps.Cfg.Contexts["ops-copy"] = labTokenContext(labTestRef(t, deps, "demo"))

		_, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `context "ops-copy" still references that item`)
		assert.Empty(t, *stored)
		assertNoMutatingSSH(t, fake)
	})

	t.Run("a failed read is an error", func(t *testing.T) {
		fake := exec.Fake(exec.FakeResponse{Stdout: "not json"})
		cmd, deps := syncTestDeps(t, fake)

		_, err := syncLabContext(cmd, deps, demoLab(), labSyncOptions{DryRun: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse user list")
	})
}

// TestContextSync_HasDryRunFlag guards the flag on both verbs.
func TestContextSync_HasDryRunFlag(t *testing.T) {
	for _, cmd := range []*cobra.Command{newContextSyncCmd(), newContextRmCmd()} {
		flag := cmd.Flags().Lookup("dry-run")
		require.NotNil(t, flag, "%s has no --dry-run", cmd.Name())
		assert.Equal(t, "false", flag.DefValue)
		assert.NotContains(t, flag.Usage, "`")
	}
}

// runContextRmArgs runs `pmx lab context rm <args...>` against deps.
func runContextRmArgs(t *testing.T, deps *cli.Deps, args ...string) (string, error) {
	t.Helper()
	root := newContextCmd()
	root.SetContext(cli.WithDeps(context.Background(), deps))
	return runGuestCmd(t, root, append([]string{"rm"}, args...)...)
}

// TestContextRm_DryRunReportsTheRealPlanAndMutatesNothing runs rm --dry-run
// against each context shape and checks the message is the plan's own dry-run
// description, while the config, the config file, and the keychain are
// untouched. The real run of the same shape then agrees with the preview.
func TestContextRm_DryRunReportsTheRealPlanAndMutatesNothing(t *testing.T) {
	cases := []struct {
		name       string
		ref        func(t *testing.T, deps *cli.Deps) string
		want       []string
		wantDelete bool
	}{
		{
			name: "namespaced item is deleted",
			ref:  func(t *testing.T, deps *cli.Deps) string { return labTestRef(t, deps, "demo") },
			want: []string{`would remove context "lab-demo" and its keychain secret`},
			// A real run deletes the item.
			wantDelete: true,
		},
		{
			name: "legacy item is kept",
			ref:  func(*testing.T, *cli.Deps) string { return "keychain:pmx-lab-demo/pmx@pve!pmx" },
			want: []string{
				`would remove context "lab-demo" but keep its legacy keychain secret pmx-lab-demo/pmx@pve!pmx`,
				"security delete-generic-password -s pmx-lab-demo",
			},
		},
		{
			name: "no lab secret is referenced",
			ref:  func(*testing.T, *cli.Deps) string { return "${PMX_LAB_TOKEN}" },
			want: []string{`would remove context "lab-demo", which references no keychain secret of lab "demo"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deleted := recordKeychainDeletes(t)
			deps := contextRmDeps(t, "demo", false)
			deps.Cfg.Contexts["lab-demo"] = labTokenContext(tc.ref(t, deps))
			require.NoError(t, config.Save(deps.ConfigPath, deps.Cfg))
			before := readConfigFile(t, deps.ConfigPath)
			existing := *deps.Cfg.Contexts["lab-demo"]
			deps.Cfg.CurrentContext = "lab-demo"

			plan := planLabContextCleanup(deps, "demo")
			out, err := runContextRmArgs(t, deps, "demo", "--dry-run")
			require.NoError(t, err)

			assert.Contains(t, out, "[dry-run] "+plan.describe(true), "the preview is the plan's own description")
			for _, w := range tc.want {
				assert.Contains(t, out, w)
			}
			assert.Empty(t, *deleted, "a dry run must not delete a keychain item")
			require.NotNil(t, deps.Cfg.Contexts["lab-demo"], "the context must stay in the config")
			assert.Equal(t, existing, *deps.Cfg.Contexts["lab-demo"])
			assert.Equal(t, "lab-demo", deps.Cfg.CurrentContext, "the active context must not be unset")
			assert.Equal(t, before, readConfigFile(t, deps.ConfigPath), "the config file must not be written")

			// The same shape, run for real, does what the preview said.
			_, err = runContextRmArgs(t, deps, "demo")
			require.NoError(t, err)
			assert.Nil(t, deps.Cfg.Contexts["lab-demo"])
			assert.Equal(t, tc.wantDelete, len(*deleted) == 1)
		})
	}
}

// TestContextRm_DryRunOfMissingContext covers a context that is not in the
// config: the preview says so and nothing is written.
func TestContextRm_DryRunOfMissingContext(t *testing.T) {
	deleted := recordKeychainDeletes(t)
	deps := contextRmDeps(t, "demo", false)
	before := readConfigFile(t, deps.ConfigPath)

	out, err := runContextRmArgs(t, deps, "demo", "--dry-run")
	require.NoError(t, err)

	assert.Contains(t, out, `[dry-run] context "lab-demo" not found, so no keychain secret would be removed`)
	assert.Empty(t, *deleted)
	assert.Equal(t, before, readConfigFile(t, deps.ConfigPath))
}
