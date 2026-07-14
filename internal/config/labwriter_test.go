package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// fullLab returns a fully-populated Lab with realistic values, used to
// exercise every field in the template and round-trip tests.
func fullLab() *config.Lab {
	return &config.Lab{
		Name:  "wayne",
		Mode:  "nested",
		Owner: "wayne@pve",
		Network: config.LabNetwork{
			VnetID:    "labwayne",
			VnetAlias: "lab-wayne",
			VxlanTag:  5001,
			CIDR:      "10.108.0.0/16",
			Mgmt: config.LabMgmt{
				Subnet:  "10.108.0.0/24",
				HostIP:  "10.108.0.1",
				Gateway: "10.108.0.1",
			},
			BoshBloc: "10.108.1.0/24",
			MTU:      1450,
		},
		Compute: config.LabCompute{
			VCPU:     4,
			CPUType:  "host",
			NUMA:     true,
			Machine:  "q35",
			Firmware: "ovmf",
			Memory: config.LabMemory{
				MinGB: 32,
				MaxGB: 96,
			},
		},
		Storage: config.LabStorage{
			Pool:       "tank-lab-wayne",
			OSDiskGB:   64,
			DataDiskGB: 400,
			RefquotaGB: 480,
			Controller: "virtio-scsi-single",
			IOThread:   true,
			Discard:    true,
			SSD:        true,
		},
		DNS: config.LabDNS{
			Zone: "wayne.lab.fivetwenty.io",
		},
		Provisioning: config.LabProvisioning{
			Mode:           "cloud-init",
			AnswerTemplate: "/etc/pmx/answer.tmpl",
			SSHKeys:        []string{"ssh-ed25519 AAAAC3example wayne@laptop"},
		},
		Access: config.LabAccess{
			Realm: "pve",
			Pool:  "lab-wayne",
			Role:  "PMXAdmin",
		},
	}
}

func TestWriteLabFile_WritesFileAtDirNameYAML_Mode0600(t *testing.T) {
	dir := t.TempDir()

	path, err := config.WriteLabFile(dir, fullLab(), false)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "wayne.yaml"), path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteLabFile_RefusesExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()

	path, err := config.WriteLabFile(dir, fullLab(), false)
	require.NoError(t, err)

	_, err = config.WriteLabFile(dir, fullLab(), false)
	require.Error(t, err)
	require.ErrorContains(t, err, path)
}

func TestWriteLabFile_ForceOverwritesExisting(t *testing.T) {
	dir := t.TempDir()

	lab := fullLab()
	_, err := config.WriteLabFile(dir, lab, false)
	require.NoError(t, err)

	lab.Access.Role = "PMXAuditor"
	path, err := config.WriteLabFile(dir, lab, true)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "PMXAuditor")
}

func TestWriteLabFile_EmptyName_Errors(t *testing.T) {
	dir := t.TempDir()

	lab := fullLab()
	lab.Name = ""

	_, err := config.WriteLabFile(dir, lab, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "lab.Name is required")
}

func TestWriteLabFile_NilLab_Errors(t *testing.T) {
	dir := t.TempDir()

	_, err := config.WriteLabFile(dir, nil, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "lab is nil")
}

func TestWriteLabFile_NameWithPathSeparator_Errors(t *testing.T) {
	dir := t.TempDir()

	lab := fullLab()
	lab.Name = "../escape"

	_, err := config.WriteLabFile(dir, lab, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "not a valid file name")
}

func TestWriteLabFile_EmptyDir_Errors(t *testing.T) {
	_, err := config.WriteLabFile("", fullLab(), false)
	require.Error(t, err)
	require.ErrorContains(t, err, "dir is required")
}

func TestWriteLabFile_RoundTripThroughResolveLabs(t *testing.T) {
	configDir := t.TempDir()
	configPath := writeConfigFile(t, configDir, "config.yml", 0o600)

	labsDir := filepath.Join(configDir, "labs.d")
	want := fullLab()

	path, err := config.WriteLabFile(labsDir, want, false)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(labsDir, "wayne.yaml"), path)

	cfg := &config.Config{LabsDir: "labs.d"}
	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)

	got := labs["wayne"]
	require.NotNil(t, got)
	require.Equal(t, want, got)
}

func TestWriteLabFile_PathologicalStringsRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	configPath := writeConfigFile(t, configDir, "config.yml", 0o600)

	labsDir := filepath.Join(configDir, "labs.d")
	want := fullLab()
	// Values Go's %q and YAML's double-quoted style escape differently:
	// control characters, tabs, embedded newlines, quotes, backslashes,
	// DEL, a C1 control, NEL, and the unicode line/paragraph separators.
	want.Owner = "own\ter\"quoted\"\\back\\slash"
	want.Network.VnetAlias = "line1\nline2\rline3"
	want.Compute.Machine = "bell\x07esc\x1bdel\x7fc1\u009bnel\u0085"
	want.DNS.Zone = "sep\u2028par\u2029end"
	want.Provisioning.AnswerTemplate = "café \U0001F600 plain"

	path, err := config.WriteLabFile(labsDir, want, false)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(labsDir, "wayne.yaml"), path)

	cfg := &config.Config{LabsDir: "labs.d"}
	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)

	got := labs["wayne"]
	require.NotNil(t, got)
	require.Equal(t, want, got)
}

func TestWriteLabFile_NameWithControlCharacter_Errors(t *testing.T) {
	lab := fullLab()
	lab.Name = "way\nne"

	_, err := config.WriteLabFile(t.TempDir(), lab, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "control character")
}

func TestLabFileTemplate_NeverContainsPasswordSubstring(t *testing.T) {
	template := config.LabFileTemplate(fullLab())
	require.NotContains(t, strings.ToLower(string(template)), "password")
}

func TestLabFileTemplate_NilLab_DoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		template := config.LabFileTemplate(nil)
		require.NotContains(t, strings.ToLower(string(template)), "password")
	})
}

func TestLabFileTemplate_HeaderCommentSurvivesOnDisk(t *testing.T) {
	dir := t.TempDir()

	path, err := config.WriteLabFile(dir, fullLab(), false)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Contains(t, string(data), "# Lab environment: wayne.")
	require.True(t, strings.Contains(string(data), "\n#"), "expected at least one comment line in written file")
}
