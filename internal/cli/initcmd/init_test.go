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

// TestInitConfig_SSHProxyTimeoutBlocksDocumentedNotSet pins the ssh, proxy,
// and timeout blocks the template appends after tls: they stay commented
// out, they parse, config.UnknownKeys recognises every field inside them,
// and a context loaded from the file resolves as if the blocks were absent
// entirely.
func TestInitConfig_SSHProxyTimeoutBlocksDocumentedNotSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pmx", "config.yml")

	_, err := run(t, path, "config")
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(raw)

	// The three blocks are commented out, not live settings. NotRegexp anchors
	// on a line that starts (after leading whitespace) with the bare key, so
	// an uncommented "    ssh:" line under a context would be caught even
	// though it never appears at column 0.
	require.Contains(t, text, "# ssh:")
	require.Contains(t, text, "# proxy:")
	require.Contains(t, text, "# timeout:")
	require.NotRegexp(t, `(?m)^\s*ssh:`, text)
	require.NotRegexp(t, `(?m)^\s*proxy:`, text)
	require.NotRegexp(t, `(?m)^\s*timeout:`, text)
	require.Contains(t, text, "An absent key")
	require.Contains(t, text, "means the built-in default")

	// The long description on `pmx init` names every field pmx init config
	// documents, including the three blocks appended here.
	require.Contains(t, initcmd.NewCommand().Long,
		"the TLS options, the ssh and jump-host settings, the proxy, and the timeouts.")

	// The proxy password line is a reference, not a literal secret.
	require.Contains(t, text, "#   password: ${PMX_PROXY_PASSWORD}")

	// The rendered file parses as YAML and config.UnknownKeys finds no
	// unrecognized keys, commented-out or otherwise.
	keys, err := config.UnknownKeys(path)
	require.NoError(t, err)
	require.Empty(t, keys)

	// A context loaded from the template carries none of the three blocks,
	// and resolving it reports every timeout as defaulted, not set.
	cfg, err := config.Load(path)
	require.NoError(t, err)

	ctx := cfg.Contexts["lab"]
	require.NotNil(t, ctx)
	require.Equal(t, config.SSHBlock{}, ctx.SSH)
	require.Nil(t, ctx.Proxy.FromEnv)
	require.Equal(t, config.TimeoutBlock{}, ctx.Timeout)

	conn, err := cli.ResolveConnection("lab", ctx, cli.ConnectionOverrides{})
	require.NoError(t, err)
	require.False(t, conn.TimeoutsSet.Connect)
	require.False(t, conn.TimeoutsSet.TLSHandshake)
	require.False(t, conn.TimeoutsSet.Request)
}
