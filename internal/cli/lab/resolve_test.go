package lab

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
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

func TestResolveLabForMutate_AcceptsHyphenatedAndNumericNames(t *testing.T) {
	for _, name := range []string{"pve-cpi", "wayneeseguin", "lab2", "a", "0a"} {
		lab := cleanLab(name)
		cfg := &config.Config{Labs: map[string]*config.Lab{name: lab}}
		path := writeConfig(t, cfg)
		cmd := newCmdWithDeps(t, path)

		got, err := resolveLabForMutate(cmd, name)
		require.NoError(t, err, "name %q should pass the charset check", name)
		require.NotNil(t, got)
	}
}

// TestResolveLabForMutate_RejectsInvalidNameCharset covers M3-R02: a lab
// name outside the strict charset (lowercase alnum + internal hyphen) must
// be refused before resolveLabForMutate returns, since several M3 verbs
// (cluster init/join, qdevice add, sdn apply, nfs attach/detach, quota set)
// interpolate lab.Name directly into a remote ssh command line. Every case
// is configured with a "placeholder" map key and an explicit Lab.Name
// override, so config.ResolveLabs (which defaults an empty/unset Name to
// its map key) always resolves the exact invalid string under test, indexed
// by that same string — resolveLabForMutate is then called with that
// string, exactly as a `pmx lab cluster init <name>` invocation would pass
// whatever the operator typed.
func TestResolveLabForMutate_RejectsInvalidNameCharset(t *testing.T) {
	cases := []string{
		"Wayne",         // uppercase
		"wayne eseguin", // space
		"wayne; reboot", // shell metacharacter
		"wayne_eseguin", // underscore
		"-wayne",        // leading hyphen
		"wayne-",        // trailing hyphen
	}

	for _, name := range cases {
		lab := cleanLab("placeholder")
		lab.Name = name
		cfg := &config.Config{Labs: map[string]*config.Lab{"placeholder": lab}}
		path := writeConfig(t, cfg)
		cmd := newCmdWithDeps(t, path)

		got, err := resolveLabForMutate(cmd, name)
		require.Error(t, err, "name %q must be rejected", name)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "charset")
	}
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

// --- node/QDevice naming ---------------------------------------------------

func TestLabNodeVMName(t *testing.T) {
	assert.Equal(t, "lab-wayne-0", labNodeVMName("wayne", 0))
	assert.Equal(t, "lab-wayne-4", labNodeVMName("wayne", 4))
}

func TestLabQdeviceVMName(t *testing.T) {
	assert.Equal(t, "lab-wayne-q", labQdeviceVMName("wayne"))
}

func TestLegacyLabVMName(t *testing.T) {
	assert.Equal(t, "lab-wayne", legacyLabVMName("wayne"))
}

// --- node/QDevice management IP derivation ---------------------------------

// TestLabNodeMgmtIP_DerivesFromMgmtSubnet covers the primary source: the
// lab's mgmt subnet CIDR, base + 10 + i.
func TestLabNodeMgmtIP_DerivesFromMgmtSubnet(t *testing.T) {
	net := config.LabNetwork{Mgmt: config.LabMgmt{Subnet: "10.10.1.0/24"}}

	for i, want := range map[int]string{0: "10.10.1.10", 1: "10.10.1.11", 4: "10.10.1.14"} {
		ip, err := labNodeMgmtIP(net, i)
		require.NoError(t, err)
		assert.Equal(t, want, ip, "node index %d", i)
	}
}

// TestLabNodeMgmtIP_DerivesFromHostIPWhenSubnetUnset covers the fallback
// source: today's convention of Mgmt.HostIP always being node 0's own .10
// address, masked to /24 to recover the same network base an explicit
// Subnet would state.
func TestLabNodeMgmtIP_DerivesFromHostIPWhenSubnetUnset(t *testing.T) {
	net := config.LabNetwork{Mgmt: config.LabMgmt{HostIP: "10.20.3.10"}}

	ip, err := labNodeMgmtIP(net, 2)
	require.NoError(t, err)
	assert.Equal(t, "10.20.3.12", ip)
}

// TestLabNodeMgmtIP_NeitherSourceSet_Errors covers a lab whose network
// config has no mgmt subnet or host IP at all: node IP derivation has no
// source of truth and must error, not silently return a zero-value string.
func TestLabNodeMgmtIP_NeitherSourceSet_Errors(t *testing.T) {
	_, err := labNodeMgmtIP(config.LabNetwork{}, 0)
	require.Error(t, err)
	assert.ErrorContains(t, err, "mgmt.subnet")
}

// TestLabNodeMgmtIP_OutOfRangeIndex_Errors covers the [0, maxLabNodeIndex]
// bound on the node index parameter.
func TestLabNodeMgmtIP_OutOfRangeIndex_Errors(t *testing.T) {
	net := config.LabNetwork{Mgmt: config.LabMgmt{Subnet: "10.10.1.0/24"}}

	_, err := labNodeMgmtIP(net, 5)
	require.Error(t, err)
	assert.ErrorContains(t, err, "out of range")

	_, err = labNodeMgmtIP(net, -1)
	require.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

// TestLabQdeviceMgmtIP_IsDotFifteen covers the QDevice's fixed offset within
// the lab's mgmt /24.
func TestLabQdeviceMgmtIP_IsDotFifteen(t *testing.T) {
	net := config.LabNetwork{Mgmt: config.LabMgmt{Subnet: "10.10.1.0/24"}}

	ip, err := labQdeviceMgmtIP(net)
	require.NoError(t, err)
	assert.Equal(t, "10.10.1.15", ip)
}
