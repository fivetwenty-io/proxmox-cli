package logs_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/logs"
)

// runPrune executes `pmx logs prune <args...>` through the real root command
// (so PersistentPreRunE wires Deps) with HOME redirected to home.
func runPrune(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{logs.Group})

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"--config", filepath.Join(home, "config.yml"), "logs", "prune"}, args...))

	err := root.Execute()
	return buf.String(), err
}

// seedLog creates a log file with the given content and mtime under home's
// log directory and returns its path.
func seedLog(t *testing.T, home, rel, content string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(home, ".pmx", "logs", rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
	return path
}

func TestLogsPrune_OlderThanAndEmpty(t *testing.T) {
	home := t.TempDir()
	now := time.Now()

	aged := seedLog(t, home, "pve/qemu/start/20250101-000000.jsonl", `{"msg":"x"}`+"\n", now.Add(-60*24*time.Hour))
	empty := seedLog(t, home, "version/20250101-000001.jsonl", "", now.Add(-60*24*time.Hour))
	fresh := seedLog(t, home, "pve/qemu/stop/20260722-000000.jsonl", `{"msg":"y"}`+"\n", now)

	out, err := runPrune(t, home, "--older-than", "30", "--empty")
	require.NoError(t, err)

	require.NoFileExists(t, aged)
	require.NoFileExists(t, empty)
	require.FileExists(t, fresh)
	require.Contains(t, out, "files")
	require.Contains(t, out, "30d")
}

func TestLogsPrune_DryRun(t *testing.T) {
	home := t.TempDir()
	now := time.Now()

	aged := seedLog(t, home, "pve/task/ls/20250101-000000.jsonl", `{"msg":"x"}`+"\n", now.Add(-60*24*time.Hour))

	out, err := runPrune(t, home, "--older-than", "30", "--dry-run")
	require.NoError(t, err)
	require.FileExists(t, aged, "--dry-run must not delete")
	require.Contains(t, out, "true", "dry-run flag must be reflected in output")
}

func TestLogsPrune_RetentionConfigFallback(t *testing.T) {
	home := t.TempDir()
	now := time.Now()

	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yml"),
		[]byte("log:\n  retention: 30\n"), 0o600))
	aged := seedLog(t, home, "pve/node/ls/20250101-000000.jsonl", `{"msg":"x"}`+"\n", now.Add(-60*24*time.Hour))

	_, err := runPrune(t, home)
	require.NoError(t, err)
	require.NoFileExists(t, aged, "cutoff must fall back to log.retention")
}

func TestLogsPrune_NoCutoffNoEmptyErrors(t *testing.T) {
	home := t.TempDir()
	seedLog(t, home, "version/20250101-000000.jsonl", "", time.Now().Add(-time.Hour))

	_, err := runPrune(t, home)
	require.Error(t, err)
	require.Contains(t, err.Error(), "log.retention")
}

func TestLogsPrune_MissingLogDir(t *testing.T) {
	home := t.TempDir()

	// --no-log keeps this invocation from creating the log directory itself,
	// so the stat branch actually sees an absent directory.
	out, err := runPrune(t, home, "--older-than", "30", "--no-log")
	require.NoError(t, err, "missing log directory must not be an error")
	require.Contains(t, out, "nothing to prune")
}

func TestLogsPrune_NegativeOlderThan(t *testing.T) {
	home := t.TempDir()
	seedLog(t, home, "version/20250101-000000.jsonl", "x", time.Now())

	_, err := runPrune(t, home, "--older-than", "-3")
	require.Error(t, err)
}
