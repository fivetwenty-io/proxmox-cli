package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

func topNames(factories []cli.GroupFactory) map[string]bool {
	deps := &cli.Deps{}
	m := map[string]bool{}
	for _, f := range factories {
		m[f(deps).Name()] = true
	}
	return m
}

func TestFactoriesFor_Personas(t *testing.T) {
	pmx := topNames(factoriesFor("pmx"))
	require.True(t, pmx["pve"] && pmx["pbs"] && pmx["pdm"])
	require.True(t, pmx["context"] && pmx["auth"] && pmx["version"] && pmx["ssh"] && pmx["api"])
	require.False(t, pmx["node"], "pmx must not expose flat pve commands")

	pve := topNames(factoriesFor("pve"))
	require.True(t, pve["node"] && pve["cluster"] && pve["qemu"])
	require.True(t, pve["context"] && pve["version"] && pve["ssh"])
	require.False(t, pve["pbs"] || pve["datastore"], "pve persona hides pbs")
	// pdm-only discriminators (present in pdm.ChildFactories, absent from
	// both pve.ChildFactories and pbs.ChildFactories) must not leak onto the
	// pve root either.
	require.False(t, pve["subscription"] || pve["resource"] || pve["auto-install"],
		"pve persona hides pdm")

	pbs := topNames(factoriesFor("pbs"))
	require.True(t, pbs["datastore"] && pbs["snapshot"])
	require.True(t, pbs["context"] && pbs["version"] && pbs["ssh"])
	// pbs legitimately has its own "node" command (PBS host administration),
	// so "node" cannot discriminate PVE leakage here; use a PVE-only name.
	require.False(t, pbs["pve"] || pbs["qemu"], "pbs persona hides pve")
	// pbs also legitimately has its own "remote" group (remote PBS instances
	// for sync), so "remote" cannot discriminate pdm leakage here the way it
	// does for pve; use discriminators absent from pbs.ChildFactories too.
	require.False(t, pbs["subscription"] || pbs["resource"] || pbs["auto-install"],
		"pbs persona hides pdm")
}

// TestFactoriesFor_PDM_Hoist verifies that invoking the binary as "pdm" hoists
// every pdm.ChildFactories() group (native and proxied) directly onto the
// root, alongside the shared product-neutral commands, while the pve-native
// and pbs-native root discriminators ("qemu", "datastore") do NOT leak onto
// the pdm root itself — under the pdm persona, "pve" and "pbs" are the
// PDM-proxied remote-operation groups (see
// TestFactoriesFor_PDM_ProxiedGroupsContainNativeChildren for what's nested
// under them), not the native pve/pbs persona trees, so "qemu"/"datastore"
// only ever appear nested one level deeper.
func TestFactoriesFor_PDM_Hoist(t *testing.T) {
	pdm := topNames(factoriesFor("pdm"))

	for _, name := range []string{
		"remote", "resource", "sdn", "ceph", "subscription", "user", "token",
		"acl", "role", "permission", "tfa", "realm", "config", "node",
		"auto-install", "pbs", "pve",
	} {
		require.True(t, pdm[name], "pdm persona must hoist %q to the root", name)
	}
	require.True(t, pdm["context"] && pdm["version"] && pdm["ssh"],
		"pdm persona must still expose the shared product-neutral commands")

	// Nesting trap: "pve" and "pbs" DO exist as pdm root child names (the
	// proxied groups), but their own native discriminators must not be
	// hoisted a second time directly onto the pdm root.
	require.False(t, pdm["qemu"],
		"pdm root must not expose the pve-native qemu discriminator directly (it nests under pdm's root-level \"pve\")")
	require.False(t, pdm["datastore"],
		"pdm root must not expose the pbs-native datastore discriminator directly (it nests under pdm's root-level \"pbs\")")
}

// TestFactoriesFor_PDM_ProxiedGroupsContainNativeChildren resolves the
// nesting trap TestFactoriesFor_PDM_Hoist can only gesture at: under the pdm
// persona, root's "pve" and "pbs" children are not absent — they are the
// PDM-proxied remote-operation groups (pdm.newPveCmd / pdm.newPbsCmd), a
// different, smaller command set than the native pve.ChildFactories /
// pbs.ChildFactories subtrees hoisted by the "pve"/"pbs" personas. Proving
// that requires building the actual command tree (not just the top-level
// factory names from topNames) and inspecting what's nested one level below
// the hoisted "pve"/"pbs" commands.
func TestFactoriesFor_PDM_ProxiedGroupsContainNativeChildren(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pdm")
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, factoriesFor("pdm"))

	pveProxy, _, err := root.Find([]string{"pve"})
	require.NoError(t, err)
	pveChildren := map[string]bool{}
	for _, c := range pveProxy.Commands() {
		pveChildren[c.Name()] = true
	}
	require.True(t, pveChildren["qemu"], "pdm's proxied pve group must nest qemu as a child")
	require.True(t, pveChildren["remote"],
		"pdm's proxied pve group nests its own remote directory — the proxied set, not native pve.ChildFactories")
	require.False(t, pveChildren["access"],
		"pdm's proxied pve group must not have an \"access\" child: that belongs to the native pve.ChildFactories subtree, not the proxied set")
	require.False(t, pveChildren["pool"],
		"pdm's proxied pve group must not have a \"pool\" child: that belongs to the native pve.ChildFactories subtree, not the proxied set")

	pbsProxy, _, err := root.Find([]string{"pbs"})
	require.NoError(t, err)
	pbsChildren := map[string]bool{}
	for _, c := range pbsProxy.Commands() {
		pbsChildren[c.Name()] = true
	}
	require.True(t, pbsChildren["datastore"], "pdm's proxied pbs group must nest datastore as a child")
	require.False(t, pbsChildren["snapshot"],
		"pdm's proxied pbs group must not have a top-level \"snapshot\" child: that belongs to the native pbs.ChildFactories subtree, not the proxied (remote-scoped) set")
	require.False(t, pbsChildren["tape"],
		"pdm's proxied pbs group must not have a \"tape\" child: that belongs to the native pbs.ChildFactories subtree, not the proxied set")
}
