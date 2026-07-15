package initcmd_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/initcmd"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// run executes `pmx init <args>` through the real root command so the production
// PersistentPreRunE wires Deps and applies the noClient annotation.
func run(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()

	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(initcmd.NewCommand())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	full := append([]string{"--config", cfgPath, "init"}, args...)
	root.SetArgs(full)
	err := root.Execute()
	return buf.String(), err
}

func TestInitConfig_WritesParsableTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pmx", "config.yml")

	out, err := run(t, path, "config")
	require.NoError(t, err)
	require.Contains(t, out, path)

	// File exists, is 0600, and round-trips through the real loader.
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, "lab", cfg.CurrentContext)
	require.Equal(t, "table", cfg.DefaultOutput)

	ctx := cfg.Contexts["lab"]
	require.NotNil(t, ctx)
	require.Equal(t, "pve.example.com", ctx.Host)
	require.Equal(t, 8006, ctx.Port)
	require.Equal(t, "token", ctx.Auth.Type)
	require.Equal(t, "automation", ctx.Auth.TokenID)
	require.Equal(t, "${PMX_TOKEN}", ctx.Auth.Secret)

	// The template keeps its guiding comments.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "# pmx CLI configuration.")
}

func TestInitConfig_RefusesOverwriteWithoutForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte("current-context: keep\n"), 0o600))

	out, err := run(t, path, "config")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")

	// Untouched.
	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "current-context: keep\n", string(raw))
	require.NotContains(t, out, "Wrote config template")
}

func TestInitConfig_ForceOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte("current-context: old\n"), 0o600))

	_, err := run(t, path, "config", "--force")
	require.NoError(t, err)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, "lab", cfg.CurrentContext)
}

func TestInit_NoArgsShowsHelp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	out, err := run(t, path)
	require.NoError(t, err)
	require.Contains(t, out, "Write a config.yml template")
}
