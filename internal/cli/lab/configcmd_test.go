package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// runConfigCmd executes `pmx --config <cfgPath> config <args...>` through the
// real root command, so PersistentPreRunE wires Deps and applies the
// noClient annotation exactly as production does. It mirrors the run()
// helper in internal/cli/initcmd/init_test.go, adapted to this package's
// (not-yet-wired-under-"lab") newConfigCmd().
func runConfigCmd(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()

	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(newConfigCmd())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	full := append([]string{"--config", cfgPath, "config"}, args...)
	root.SetArgs(full)
	err := root.Execute()
	return buf.String(), err
}

// writeConfigCmdYAML writes cfg (marshalled) as a 0600 config.yml under
// t.TempDir()/config.yml and returns its path, for tests that need a real
// on-disk config the production loader (not writeConfig/SaveForce, which
// this package's resolve_test.go already provides) still reads correctly
// through the full root command flow.
func writeConfigCmdYAML(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, config.SaveForce(path, cfg))
	return path
}

func TestConfigInit_CreatesLabsDirAndExample(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{})

	out, err := runConfigCmd(t, cfgPath, "init")
	require.NoError(t, err)

	labsDir := filepath.Join(filepath.Dir(cfgPath), "labs.d")
	examplePath := filepath.Join(labsDir, "example.yaml")

	dirInfo, statErr := os.Stat(labsDir)
	require.NoError(t, statErr)
	assert.True(t, dirInfo.IsDir())
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	fileInfo, statErr := os.Stat(examplePath)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	assert.Contains(t, out, examplePath)
	// labs_dir absent from config.yml: the guidance line must be printed.
	assert.Contains(t, out, "labs_dir: labs.d/")
	assert.Contains(t, out, cfgPath)
}

func TestConfigInit_NoGuidanceLineWhenLabsDirAlreadySet(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	out, err := runConfigCmd(t, cfgPath, "init")
	require.NoError(t, err)
	assert.NotContains(t, out, "Add `labs_dir:")
}

func TestConfigInit_RefusesOverwriteWithoutForce(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{})

	_, err := runConfigCmd(t, cfgPath, "init")
	require.NoError(t, err)

	_, err = runConfigCmd(t, cfgPath, "init")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestConfigInit_ForceOverwrites(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{})

	_, err := runConfigCmd(t, cfgPath, "init")
	require.NoError(t, err)

	out, err := runConfigCmd(t, cfgPath, "init", "--force")
	require.NoError(t, err)
	assert.Contains(t, out, "example.yaml")
}

// TestConfigInit_ExampleDocumentsNewNetworkFields is P1-T11's own acceptance
// case: `pmx lab config init`'s rendered example.yaml must document the
// three new LabNetwork fields (vnets, host_nics, nested_network), not just
// carry them silently in the struct.
func TestConfigInit_ExampleDocumentsNewNetworkFields(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{})

	_, err := runConfigCmd(t, cfgPath, "init")
	require.NoError(t, err)

	examplePath := filepath.Join(filepath.Dir(cfgPath), "labs.d", "example.yaml")
	data, err := os.ReadFile(examplePath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "vnets:")
	assert.Contains(t, content, "host_nics:")
	assert.Contains(t, content, "nested_network:")
	assert.Contains(t, content, "bonds:")
	assert.Contains(t, content, "vlan_zone:")
}

// TestConfigInit_ExampleNewNetworkFieldsRoundTrip confirms the example lab's
// new-field example values are not just rendered as text but parse back
// correctly through the real ResolveLabs path.
func TestConfigInit_ExampleNewNetworkFieldsRoundTrip(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "init")
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	labs, err := config.ResolveLabs(cfg, cfgPath)
	require.NoError(t, err)

	got := labs["example"]
	require.NotNil(t, got)
	require.Len(t, got.Network.Vnets, 2)
	assert.Equal(t, "examplst", got.Network.Vnets[0].ID)
	require.Len(t, got.Network.HostNICs, 5)
	assert.Equal(t, 1, got.Network.HostNICs[0].Index)
	require.Len(t, got.Network.NestedNetwork.Bonds, 3)
	require.NotNil(t, got.Network.NestedNetwork.VlanZone)
	assert.Equal(t, "clivlan", got.Network.NestedNetwork.VlanZone.ZoneName)
	require.Len(t, got.Network.NestedNetwork.VlanZone.Vnets, 4)

	assert.Empty(t, config.ValidateNestedNetwork("example", got.Network.NestedNetwork),
		"the shipped example must itself be a valid nested_network plan")
}

// TestConfigAdd_ZeroValueNewNetworkFieldsRoundTrip confirms `config add` (no
// flags exist yet for vnets/host_nics/nested_network) writes a file whose
// new fields stay nil/zero-value after a full write-then-resolve round
// trip — the "absent means today's shape, unchanged" backward-compat rule
// applied to config add's own output, not just a hand-authored file.
func TestConfigAdd_ZeroValueNewNetworkFieldsRoundTrip(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "xi", "--vxlan-tag", "5020", "--cidr", "10.121.0.0/16")
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	labs, err := config.ResolveLabs(cfg, cfgPath)
	require.NoError(t, err)

	got := labs["xi"]
	require.NotNil(t, got)
	assert.Nil(t, got.Network.Vnets)
	assert.Nil(t, got.Network.HostNICs)
	assert.Equal(t, config.LabNestedNetwork{}, got.Network.NestedNetwork)
}

// TestValidateConfigAddLab_NestedNetworkHardError_Refuses is a direct
// unit-level check of the wiring the CLI-level tests above cannot exercise
// (config add has no flags yet that populate nested_network): a hard-error
// nested_network issue (too few NICs on a bond) must refuse the write.
func TestValidateConfigAddLab_NestedNetworkHardError_Refuses(t *testing.T) {
	lab := configSchemaDefaultLab("nestbad")
	lab.Network.VxlanTag = 5021
	lab.Network.CIDR = "10.122.0.0/16"
	lab.Network.NestedNetwork = config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0"}, Mode: config.NestedBondModeActiveBackup, Bridge: "vmbr0"},
		},
	}

	err := validateConfigAddLab(lab)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nested network plan is incoherent")
	assert.Contains(t, err.Error(), "need at least 2")
}

// TestValidateConfigAddLab_NestedNetworkWarningOnly_DoesNotRefuse confirms
// a warning-class nested_network issue (D-04's 802.3ad non-functionality
// note) does not block the write — only hard errors do.
func TestValidateConfigAddLab_NestedNetworkWarningOnly_DoesNotRefuse(t *testing.T) {
	lab := configSchemaDefaultLab("nestwarn")
	lab.Network.VxlanTag = 5022
	lab.Network.CIDR = "10.123.0.0/16"
	lab.Network.NestedNetwork = config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: config.NestedBondMode8023ad, Bridge: "vmbr0"},
		},
	}

	require.NoError(t, validateConfigAddLab(lab))
}

func TestConfigInit_LabsDirFlagOverridesDefault(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{})
	custom := filepath.Join(filepath.Dir(cfgPath), "custom-labs")

	_, err := runConfigCmd(t, cfgPath, "init", "--labs-dir", custom)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(custom, "example.yaml"))
	require.NoError(t, statErr)
}

func TestConfigAdd_WritesFileThatReResolvesWithFlagValues(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	out, err := runConfigCmd(t, cfgPath, "add", "wayne",
		"--vnet-id", "wayne",
		"--vxlan-tag", "5001",
		"--cidr", "10.108.0.0/16",
		"--vcpu", "24",
		"--memory-max-gb", "128",
		"--data-disk-gb", "800",
		"--refquota-gb", "880",
		"--owner", "wayne@pve",
		"--pool", "lab-wayne",
		"--role", "PVEVMUser",
		"--mode", "nested",
	)
	require.NoError(t, err)

	labsDir := filepath.Join(filepath.Dir(cfgPath), "labs.d")
	wantPath := filepath.Join(labsDir, "wayne.yaml")
	assert.Contains(t, out, wantPath)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	labs, err := config.ResolveLabs(cfg, cfgPath)
	require.NoError(t, err)

	got := labs["wayne"]
	require.NotNil(t, got)
	assert.Equal(t, "wayne", got.Network.VnetID)
	assert.Equal(t, 5001, got.Network.VxlanTag)
	assert.Equal(t, "10.108.0.0/16", got.Network.CIDR)
	assert.Equal(t, 24, got.Compute.VCPU)
	assert.Equal(t, 128, got.Compute.Memory.MaxGB)
	assert.Equal(t, 800, got.Storage.DataDiskGB)
	assert.Equal(t, 880, got.Storage.RefquotaGB)
	assert.Equal(t, "wayne@pve", got.Owner)
	assert.Equal(t, "lab-wayne", got.Access.Pool)
	assert.Equal(t, "PVEVMUser", got.Access.Role)
	assert.Equal(t, "nested", got.Mode)
}

func TestConfigAdd_AppliesSchemaDefaultsWhenFlagsOmitted(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "gamma",
		"--vxlan-tag", "5002",
		"--cidr", "10.109.0.0/16",
	)
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	labs, err := config.ResolveLabs(cfg, cfgPath)
	require.NoError(t, err)

	got := labs["gamma"]
	require.NotNil(t, got)
	assert.Equal(t, "gamma", got.Network.VnetID, "vnet-id defaults to the lab name")
	assert.Equal(t, "lab-gamma", got.Access.Pool, "pool defaults to lab-<name>")
	assert.Equal(t, configDefaultVCPU, got.Compute.VCPU)
	assert.Equal(t, configDefaultMemoryMaxGB, got.Compute.Memory.MaxGB)
	assert.Equal(t, configDefaultDataDiskGB, got.Storage.DataDiskGB)
	assert.Equal(t, configDefaultRefquotaGB, got.Storage.RefquotaGB)
	assert.Equal(t, configDefaultAccessRole, got.Access.Role)
	assert.Equal(t, configDefaultMode, got.Mode)
}

func TestConfigAdd_RefusesExistingFileWithoutForce(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "delta", "--vxlan-tag", "5003", "--cidr", "10.110.0.0/16")
	require.NoError(t, err)

	_, err = runConfigCmd(t, cfgPath, "add", "delta", "--vxlan-tag", "5003", "--cidr", "10.110.0.0/16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already")
}

func TestConfigAdd_RefusesAlreadyResolvingInlineName(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{
		LabsDir: "labs.d",
		Labs: map[string]*config.Lab{
			"epsilon": {Mode: "nested"},
		},
	})

	_, err := runConfigCmd(t, cfgPath, "add", "epsilon", "--vxlan-tag", "5004", "--cidr", "10.111.0.0/16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already resolves")

	// The file must not have been written since the refusal fired first.
	_, statErr := os.Stat(filepath.Join(filepath.Dir(cfgPath), "labs.d", "epsilon.yaml"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestConfigAdd_ForceWritesDespiteExistingFile(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "zeta", "--vxlan-tag", "5005", "--cidr", "10.112.0.0/16")
	require.NoError(t, err)

	_, err = runConfigCmd(t, cfgPath, "add", "zeta",
		"--vxlan-tag", "5005", "--cidr", "10.112.0.0/16", "--vcpu", "32", "--force")
	require.NoError(t, err)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	labs, err := config.ResolveLabs(cfg, cfgPath)
	require.NoError(t, err)
	assert.Equal(t, 32, labs["zeta"].Compute.VCPU)
}

func TestConfigAdd_RejectsPeppiPatternInName(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "peppiprd",
		"--vnet-id", "peppi01", "--vxlan-tag", "5006", "--cidr", "10.113.0.0/16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peppi guard")
	assert.Contains(t, err.Error(), "peppiprd")

	_, statErr := os.Stat(filepath.Join(filepath.Dir(cfgPath), "labs.d", "peppiprd.yaml"))
	assert.True(t, os.IsNotExist(statErr), "guard refusal must fire before any write")
}

func TestConfigAdd_RejectsPeppiPatternInVnetID(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "eta",
		"--vnet-id", "peppivn0", "--vxlan-tag", "5007", "--cidr", "10.114.0.0/16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peppi guard")
}

func TestConfigAdd_RejectsPeppiPatternInPool(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "iota",
		"--pool", "team-peppiprd-pool",
		"--vxlan-tag", "5009", "--cidr", "10.117.0.0/16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peppi guard")

	_, statErr := os.Stat(filepath.Join(filepath.Dir(cfgPath), "labs.d", "iota.yaml"))
	assert.True(t, os.IsNotExist(statErr), "guard refusal must fire before any write")
}

func TestConfigAdd_RequiresVxlanTag(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "theta", "--cidr", "10.116.0.0/16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vxlan-tag")
}

func TestConfigAdd_RequiresCIDR(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "iota", "--vxlan-tag", "5009")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cidr")
}

func TestConfigAdd_RejectsInvalidCIDR(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "kappa", "--vxlan-tag", "5011", "--cidr", "not-a-cidr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestConfigAdd_RejectsVnetIDTooLong(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "lambda",
		"--vnet-id", "waytoolongvnetid", "--vxlan-tag", "5012", "--cidr", "10.117.0.0/16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vnet-id")
}

func TestConfigAdd_RejectsNonPositiveVCPU(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "mu",
		"--vxlan-tag", "5013", "--cidr", "10.118.0.0/16", "--vcpu", "0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vcpu")
}

func TestConfigAdd_NeverWritesPasswordKey(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{
		LabsDir:             "labs.d",
		DefaultUserPassword: fakeDefaultUserPassword,
	})

	_, err := runConfigCmd(t, cfgPath, "add", "nu", "--vxlan-tag", "5014", "--cidr", "10.119.0.0/16")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), "labs.d", "nu.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(data)), "password")
	assert.NotContains(t, string(data), fakeDefaultUserPassword)
}

func TestConfigShow_InlineLab_ProvenanceIsInline(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	})

	out, err := runConfigCmd(t, cfgPath, "show", "alpha")
	require.NoError(t, err)
	assert.Contains(t, out, "config.yml (inline)")
	assert.Contains(t, out, "labalpha")
}

func TestConfigShow_FileLab_ProvenanceNamesFile(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{LabsDir: "labs.d"})

	_, err := runConfigCmd(t, cfgPath, "add", "omicron", "--vxlan-tag", "5015", "--cidr", "10.120.0.0/16")
	require.NoError(t, err)

	wantPath := filepath.Join(filepath.Dir(cfgPath), "labs.d", "omicron.yaml")
	out, err := runConfigCmd(t, cfgPath, "show", "omicron")
	require.NoError(t, err)
	assert.Contains(t, out, wantPath)
}

func TestConfigShow_UnknownName_HelpfulError(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	})

	_, err := runConfigCmd(t, cfgPath, "show", "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"missing" not found`)
	assert.Contains(t, err.Error(), "alpha")
}

func TestConfigShow_OutputJSON_RendersValidJSON(t *testing.T) {
	cfgPath := writeConfigCmdYAML(t, &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	})

	out, err := runConfigCmdWithOutput(t, cfgPath, "json", "show", "alpha")
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &decoded), "output must be valid JSON: %s", out)
}

// runConfigCmdWithOutput is runConfigCmd extended with an explicit
// --output/-o value, for the one test that must assert against a specific
// non-default render format.
func runConfigCmdWithOutput(t *testing.T, cfgPath, format string, args ...string) (string, error) {
	t.Helper()

	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(newConfigCmd())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	full := append([]string{"--config", cfgPath, "--output", format, "config"}, args...)
	root.SetArgs(full)
	err := root.Execute()
	return buf.String(), err
}
