package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

func TestLabProvenance_InlineLab(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": {Mode: "nested"},
		},
	}

	got, err := config.LabProvenance(cfg, configPath, "alpha")
	require.NoError(t, err)
	require.Equal(t, "config.yml (inline)", got)
}

func TestLabProvenance_InlineLab_NameFromExplicitField(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"key": {Name: "explicit-name", Mode: "nested"},
		},
	}

	got, err := config.LabProvenance(cfg, configPath, "explicit-name")
	require.NoError(t, err)
	require.Equal(t, "config.yml (inline)", got)

	_, err = config.LabProvenance(cfg, configPath, "key")
	require.Error(t, err, "the map key is not the lab's resolved name once Name is set explicitly")
}

func TestLabProvenance_IncludedFile_ReturnsFilePath(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labFile := filepath.Join(dir, "labs.d", "gamma.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(labFile), 0o700))
	require.NoError(t, os.WriteFile(labFile, []byte("name: gamma\nmode: nested\n"), 0o600))

	cfg := &config.Config{LabsDir: "labs.d"}

	got, err := config.LabProvenance(cfg, configPath, "gamma")
	require.NoError(t, err)
	require.Equal(t, labFile, got)
}

func TestLabProvenance_IncludedFile_NameFromFilenameStem(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labFile := filepath.Join(dir, "labs.d", "delta.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(labFile), 0o700))
	require.NoError(t, os.WriteFile(labFile, []byte("mode: nested\n"), 0o600))

	cfg := &config.Config{LabsDir: "labs.d"}

	got, err := config.LabProvenance(cfg, configPath, "delta")
	require.NoError(t, err)
	require.Equal(t, labFile, got)
}

func TestLabProvenance_UnknownName_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": {Mode: "nested"}},
	}

	_, err := config.LabProvenance(cfg, configPath, "missing")
	require.Error(t, err)
	require.ErrorContains(t, err, `"missing" not found`)
}

func TestLabProvenance_NilConfig_Errors(t *testing.T) {
	_, err := config.LabProvenance(nil, "/tmp/config.yml", "alpha")
	require.Error(t, err)
	require.ErrorContains(t, err, "config is nil")
}

func TestLabProvenance_EmptyName_Errors(t *testing.T) {
	_, err := config.LabProvenance(&config.Config{}, "/tmp/config.yml", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "name is required")
}
