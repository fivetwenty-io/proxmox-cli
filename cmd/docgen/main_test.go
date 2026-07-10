package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// generateInto runs the generator for all personas into dir and returns
// basename -> contents.
func generateInto(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	for _, k := range []string{"PMX_CONTEXT", "PMX_NODE", "PMX_OUTPUT", "PMX_TOKEN", "PMX_TOKEN_SECRET"} {
		t.Setenv(k, "")
	}
	require.NoError(t, run(runOpts{
		out:      dir,
		personas: []string{"pmx", "pve", "pbs", "pdm"},
		version:  "test",
		date:     fallbackDate,
	}))
	got := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if !d.IsDir() {
			b, rerr := os.ReadFile(p)
			require.NoError(t, rerr)
			got[filepath.Base(p)] = b
		}
		return nil
	}))
	return got
}

func TestGenerate_Deterministic(t *testing.T) {
	ga := generateInto(t, t.TempDir())
	gb := generateInto(t, t.TempDir())
	require.Equal(t, len(ga), len(gb))
	for name, ba := range ga {
		require.Equal(t, string(ba), string(gb[name]), "page %s differs between runs", name)
	}
}

// TestGenerate_PageCountFloor guards against a wiring regression that would
// silently drop most of the tree (e.g. a persona factory list collapsing to
// Shared() only). The floor is set ~15% below the observed four-persona
// count (3145 pages as of this writing) so it still trips on that class of
// bug without being brittle to small additions of new leaf commands.
func TestGenerate_PageCountFloor(t *testing.T) {
	got := generateInto(t, t.TempDir())
	require.GreaterOrEqual(t, len(got), 2670, "expected full four-persona output (~3145 pages observed)")
}

func TestPersonaTrees_DifferCorrectly(t *testing.T) {
	got := generateInto(t, t.TempDir())
	require.Contains(t, got, "pmx.1")
	require.Contains(t, got, "pmx-pve-qemu.1", "pmx persona must nest qemu under pve")
	require.Contains(t, got, "pve-qemu.1", "pve persona must hoist qemu to root")
	require.Contains(t, got, "pbs.1")
	require.Contains(t, got, "pdm.1")
	require.NotContains(t, got, "pve-pbs.1", "pve persona must not contain the pbs group")
}

func TestHiddenAndInternalCommandsAbsent(t *testing.T) {
	got := generateInto(t, t.TempDir())
	require.NotContains(t, got, "pmx-ctx.1", "hidden ctx alias must not be documented")
	require.NotContains(t, got, "pmx-help.1", "auto help command must not be documented")
	for name := range got {
		require.NotContains(t, name, "__complete", "cobra completion internals leaked: %s", name)
	}
}

func TestNoPageLeaksHostPathOrEnv(t *testing.T) {
	got := generateInto(t, t.TempDir())
	for name, b := range got {
		s := string(b)
		require.NotContains(t, s, "/Users/", "%s leaks a builder home path", name)
		require.NotContains(t, s, "/home/", "%s leaks a builder home path", name)
	}
}

func TestRun_UnknownPersonaErrors(t *testing.T) {
	err := run(runOpts{out: t.TempDir(), personas: []string{"nope"}, version: "test", date: fallbackDate})
	require.Error(t, err)
}
