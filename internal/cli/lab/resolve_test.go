package lab

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// fakeDefaultUserPassword is a placeholder value only — never the real
// production default; config.SaveForce always writes 0600, so tests that set
// it never hit the loader's 0600 enforcement error.
const fakeDefaultUserPassword = "s3cret-test!"

// writeConfig writes cfg to a config.yml under a fresh t.TempDir() via
// config.SaveForce (struct-marshal write, 0600) and returns the file's path.
// Mirrors the per-package writeConfig(t, f) helper used across the pve/pbs
// command test suites, extended here to carry Labs and DefaultUserPassword.
func writeConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, config.SaveForce(path, cfg))
	return path
}

// newCmdWithDeps builds a bare cobra.Command carrying a *cli.Deps loaded
// from configPath, the same shape resolveLab/resolveLabForMutate expect from
// cli.GetDeps(cmd). It bypasses PersistentPreRunE and API client
// construction entirely, since resolve.go never touches Deps.API.
func newCmdWithDeps(t *testing.T, configPath string) *cobra.Command {
	t.Helper()

	cfg, err := config.Load(configPath)
	require.NoError(t, err)

	deps := &cli.Deps{Cfg: cfg, ConfigPath: configPath}

	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	return cmd
}

// cleanLab returns a Lab definition with no field overlapping any peppi
// protected VMID or name pattern, suitable as a baseline for tests that
// assert the guard passes.
func cleanLab(name string) *config.Lab {
	return &config.Lab{
		Mode:  "nested",
		Owner: "alice@pve",
		Network: config.LabNetwork{
			VnetID: "lab" + name,
			CIDR:   "10.10.1.0/24",
		},
		Storage: config.LabStorage{
			RefquotaGB: 50,
		},
		DNS: config.LabDNS{
			Zone: name + ".lab.internal",
		},
		Access: config.LabAccess{
			Pool: "lab-" + name,
			Role: "PMXAdmin",
		},
	}
}

func TestResolveLab_InlineByName(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLab(cmd, "alpha")
	require.NoError(t, err)
	require.NotNil(t, lab)

	assert.Equal(t, "alpha", lab.Name)
	assert.Equal(t, "labalpha", lab.Network.VnetID)
	assert.Equal(t, "10.10.1.0/24", lab.Network.CIDR)
	assert.Equal(t, "lab-alpha", lab.Access.Pool)
	assert.Equal(t, "PMXAdmin", lab.Access.Role)
	assert.Equal(t, "alpha.lab.internal", lab.DNS.Zone)
}

func TestResolveLab_UnknownNameListsAvailable(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"beta":  cleanLab("beta"),
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLab(cmd, "missing")
	require.Error(t, err)
	assert.Nil(t, lab)
	assert.ErrorContains(t, err, `lab "missing" not found`)
	// Names must be sorted regardless of map iteration order.
	assert.ErrorContains(t, err, "available: alpha, beta")
}

func TestResolveLab_EmptyLabsMap(t *testing.T) {
	cfg := &config.Config{}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLab(cmd, "anything")
	require.Error(t, err)
	assert.Nil(t, lab)
	assert.ErrorContains(t, err, `lab "anything" not found`)
	assert.ErrorContains(t, err, "available: (none configured)")
}

func TestResolveLabForMutate_CleanLabPasses(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLabForMutate(cmd, "alpha")
	require.NoError(t, err)
	require.NotNil(t, lab)
	assert.Equal(t, "alpha", lab.Name)
}

func TestResolveLabForMutate_GuardFiresOnVnetID(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.Network.VnetID = "peppivn0"

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"dirty": dirty,
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLabForMutate(cmd, "dirty")
	require.Error(t, err)
	assert.Nil(t, lab)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppivn0")
}

func TestResolveLabForMutate_GuardFiresOnAccessPool(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.Access.Pool = "peppiprd-pool"

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"dirty": dirty,
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLabForMutate(cmd, "dirty")
	require.Error(t, err)
	assert.Nil(t, lab)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppiprd")
}

func TestResolveLabForMutate_GuardFiresOnLabName(t *testing.T) {
	// A lab whose own name matches a protected pattern must be refused even
	// when every other identifier is clean.
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"peppiprd": cleanLab("safe"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLabForMutate(cmd, "peppiprd")
	require.Error(t, err)
	assert.Nil(t, lab)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppiprd")
}

func TestResolveLabForMutate_GuardFiresOnStoragePool(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.Storage.Pool = "tank-peppiprd-data"

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"dirty": dirty,
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLabForMutate(cmd, "dirty")
	require.Error(t, err)
	assert.Nil(t, lab)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "tank-peppiprd-data")
}

// TestStorageID_DerivesFromExplicitBasePool covers storageID's contract:
// Storage.Pool is always the base ZFS pool name, never a full PVE storage
// ID — "tank" (the config-add/example default) must still derive
// "tank-lab-<name>", not collapse to "tank" verbatim.
func TestStorageID_DerivesFromExplicitBasePool(t *testing.T) {
	lab := cleanLab("wayne")
	lab.Name = "wayne"
	lab.Storage.Pool = "tank"
	assert.Equal(t, "tank-lab-wayne", storageID(lab))
	assert.Equal(t, "tank/labs/wayne", zfsDatasetPath(lab))
}

// TestStorageID_DerivesFromNonDefaultBasePool covers a lab whose base ZFS
// pool is not "tank": both the storage ID and dataset path must be derived
// from that pool, so create's disk allocation and quota set's refquota
// target the same dataset.
func TestStorageID_DerivesFromNonDefaultBasePool(t *testing.T) {
	lab := cleanLab("wayne")
	lab.Name = "wayne"
	lab.Storage.Pool = "othertank"
	assert.Equal(t, "othertank-lab-wayne", storageID(lab))
	assert.Equal(t, "othertank/labs/wayne", zfsDatasetPath(lab))
}

// TestStorageID_DefaultsToTankWhenPoolUnset covers the config-add gap: a lab
// that never sets storage.pool at all (as cleanLab and this repo's own
// resolve_test fixtures do) must still derive the "tank" default base pool.
func TestStorageID_DefaultsToTankWhenPoolUnset(t *testing.T) {
	lab := cleanLab("wayne")
	lab.Name = "wayne"
	lab.Storage.Pool = ""
	assert.Equal(t, "tank-lab-wayne", storageID(lab))
	assert.Equal(t, "tank/labs/wayne", zfsDatasetPath(lab))
}

// TestLabPoolID_UsesAccessPoolWhenSet covers labPoolID's shared fallback
// helper: an explicit access.pool is used verbatim.
func TestLabPoolID_UsesAccessPoolWhenSet(t *testing.T) {
	lab := cleanLab("wayne")
	lab.Access.Pool = "custom-pool"
	assert.Equal(t, "custom-pool", labPoolID(lab))
}

// TestLabPoolID_DefaultsToLabDashName covers labPoolID's fallback when
// access.pool is empty: every mutating verb (create, destroy, access grant,
// start, stop) must resolve the identical "lab-<name>" pool in this case.
func TestLabPoolID_DefaultsToLabDashName(t *testing.T) {
	lab := cleanLab("wayne")
	lab.Name = "wayne"
	lab.Access.Pool = ""
	assert.Equal(t, "lab-wayne", labPoolID(lab))
}

func TestResolveLabForMutate_GuardFiresOnDNSZone(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.DNS.Zone = "peppivn0.internal"

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"dirty": dirty,
		},
	}
	path := writeConfig(t, cfg)
	cmd := newCmdWithDeps(t, path)

	lab, err := resolveLabForMutate(cmd, "dirty")
	require.Error(t, err)
	assert.Nil(t, lab)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppivn0.internal")
}
