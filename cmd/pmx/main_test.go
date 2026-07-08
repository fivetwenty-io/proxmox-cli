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
	require.True(t, pmx["pve"] && pmx["pbs"])
	require.True(t, pmx["context"] && pmx["auth"] && pmx["version"] && pmx["ssh"] && pmx["api"])
	require.False(t, pmx["node"], "pmx must not expose flat pve commands")

	pve := topNames(factoriesFor("pve"))
	require.True(t, pve["node"] && pve["cluster"] && pve["qemu"])
	require.True(t, pve["context"] && pve["version"] && pve["ssh"])
	require.False(t, pve["pbs"] && pve["datastore"], "pve persona hides pbs")

	pbs := topNames(factoriesFor("pbs"))
	require.True(t, pbs["datastore"] && pbs["snapshot"])
	require.True(t, pbs["context"] && pbs["version"] && pbs["ssh"])
	// pbs legitimately has its own "node" command (PBS host administration),
	// so "node" cannot discriminate PVE leakage here; use a PVE-only name.
	require.False(t, pbs["pve"] || pbs["qemu"], "pbs persona hides pve")
}
