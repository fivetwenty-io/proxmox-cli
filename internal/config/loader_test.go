package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// writeConfigFile writes an empty (or near-empty) config.yml to dir/name so
// tests have a configPath to resolve globs and file-mode checks against, and
// returns its full path. mode is applied after creation.
func writeConfigFile(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("current-context: \"\"\n"), 0o600))
	require.NoError(t, os.Chmod(path, mode))
	return path
}

func TestResolveLabs_InlineOnly_NameDefaultsToMapKey(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": {Mode: "nested"},
			"beta":  {Name: "beta-explicit", Mode: "nested"},
		},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 2)

	require.NotNil(t, labs["alpha"])
	require.Equal(t, "alpha", labs["alpha"].Name)

	require.NotNil(t, labs["beta-explicit"])
	require.Equal(t, "beta-explicit", labs["beta-explicit"].Name)
	require.Nil(t, labs["beta"])
}

func TestResolveLabs_IncludeGlob_NameFromFileKeyAndFilenameStem(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "gamma.yaml"),
		[]byte("name: gamma-explicit\nmode: nested\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "delta.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 2)

	require.NotNil(t, labs["gamma-explicit"])
	require.Equal(t, "gamma-explicit", labs["gamma-explicit"].Name)

	require.NotNil(t, labs["delta"])
	require.Equal(t, "delta", labs["delta"].Name)
}

func TestResolveLabs_LabsDirSugar_ResolvesDirGlob(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labsDir := filepath.Join(dir, "labs.d")
	require.NoError(t, os.MkdirAll(labsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(labsDir, "epsilon.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{LabsDir: "labs.d"}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)
	require.NotNil(t, labs["epsilon"])
}

func TestResolveLabs_DuplicateName_InlineVsInclude_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labFile := filepath.Join(dir, "zeta.yaml")
	require.NoError(t, os.WriteFile(labFile, []byte("name: zeta\nmode: nested\n"), 0o600))

	cfg := &config.Config{
		Labs:    map[string]*config.Lab{"zeta": {Mode: "nested"}},
		Include: []string{"*.yaml"},
	}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `duplicate lab "zeta"`)
	require.ErrorContains(t, err, "config.yml (inline)")
	require.ErrorContains(t, err, labFile)
}

func TestResolveLabs_DuplicateName_IncludeVsInclude_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	subA := filepath.Join(dir, "a")
	subB := filepath.Join(dir, "b")
	require.NoError(t, os.MkdirAll(subA, 0o700))
	require.NoError(t, os.MkdirAll(subB, 0o700))

	fileA := filepath.Join(subA, "eta.yaml")
	fileB := filepath.Join(subB, "eta.yaml")
	require.NoError(t, os.WriteFile(fileA, []byte("mode: nested\n"), 0o600))
	require.NoError(t, os.WriteFile(fileB, []byte("mode: nested\n"), 0o600))

	cfg := &config.Config{Include: []string{"a/*.yaml", "b/*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `duplicate lab "eta"`)
	require.ErrorContains(t, err, fileA)
	require.ErrorContains(t, err, fileB)
}

func TestResolveLabs_SameFileMatchedByOverlappingGlobs_LoadsOnce(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labsDir := filepath.Join(dir, "labs.d")
	require.NoError(t, os.MkdirAll(labsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(labsDir, "theta.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	// labs_dir expands to labs.d/*.yaml — the explicit include overlaps it,
	// so both globs match the same file. Relative and absolute forms of the
	// same pattern must also collapse to one load.
	cfg := &config.Config{
		LabsDir: "labs.d",
		Include: []string{"labs.d/*.yaml", filepath.Join(labsDir, "*.yaml")},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)
	require.NotNil(t, labs["theta"])
	require.Equal(t, "theta", labs["theta"].Name)
}

func TestResolveLabs_RelativeGlob_ResolvesAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	nestedDir := filepath.Join(dir, "conf")
	require.NoError(t, os.MkdirAll(nestedDir, 0o700))
	configPath := writeConfigFile(t, nestedDir, "config.yml", 0o600)

	require.NoError(t, os.WriteFile(
		filepath.Join(nestedDir, "theta.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	// A relative working directory elsewhere must not affect resolution:
	// the glob resolves against configPath's directory, not os.Getwd().
	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)
	require.NotNil(t, labs["theta"])
}

func TestResolveLabs_FileMode0600Enforcement(t *testing.T) {
	t.Run("group-readable config with secret errors", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeConfigFile(t, dir, "config.yml", 0o644)
		cfg := &config.Config{DefaultUserPassword: "s3cret-test!"}

		_, err := config.ResolveLabs(cfg, configPath)
		require.Error(t, err)
		require.ErrorContains(t, err, "chmod 0600")
		require.NotContains(t, err.Error(), "s3cret-test!")
	})

	t.Run("group-readable config without secret is fine", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeConfigFile(t, dir, "config.yml", 0o644)
		cfg := &config.Config{}

		_, err := config.ResolveLabs(cfg, configPath)
		require.NoError(t, err)
	})

	t.Run("0600 config with secret is fine", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeConfigFile(t, dir, "config.yml", 0o600)
		cfg := &config.Config{DefaultUserPassword: "s3cret-test!"}

		_, err := config.ResolveLabs(cfg, configPath)
		require.NoError(t, err)
	})
}

func TestResolveLabs_MalformedLabFile_ErrorsWithPath(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	badFile := filepath.Join(dir, "broken.yaml")
	require.NoError(t, os.WriteFile(badFile, []byte("mode: [nested\n"), 0o600))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, badFile)
}

func TestResolveLabs_EmptyLabFile_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	emptyFile := filepath.Join(dir, "empty.yaml")
	require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0o600))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, emptyFile)
	require.ErrorContains(t, err, "is empty")
}

func TestResolveLabs_CommentOnlyLabFile_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	commentFile := filepath.Join(dir, "commented.yaml")
	require.NoError(t, os.WriteFile(
		commentFile,
		[]byte("# this is meant to become a lab someday\n# still nothing here\n\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, commentFile)
	require.ErrorContains(t, err, "is empty")
}

func TestResolveLabs_ConfigShapedLabFile_ErrorsNamingUnknownKey(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	wrapperFile := filepath.Join(dir, "pasted.yaml")
	require.NoError(t, os.WriteFile(
		wrapperFile,
		[]byte("labs:\n  copied:\n    mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, wrapperFile)
	require.ErrorContains(t, err, "labs")
}

func TestResolveLabs_UnknownFieldInLabFile_ErrorsNamingField(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	typoFile := filepath.Join(dir, "typo.yaml")
	require.NoError(t, os.WriteFile(
		typoFile,
		[]byte("name: typo-lab\nmode: nested\nnetwork:\n  vxlan_tg: 5\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, typoFile)
	require.ErrorContains(t, err, "vxlan_tg")
}

func TestResolveLabs_NilLabsMap_IsFine(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Empty(t, labs)
}

func TestResolveLabs_GlobMatchingZeroFiles_IsNotAnError(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{Include: []string{"nothing-here/*.yaml"}}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Empty(t, labs)
}
