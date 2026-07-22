package logx_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/logx"
)

// writeLogFile creates path (and parents) with the given content and mtime.
func writeLogFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

func TestPrune_AgeCutoff(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	old := filepath.Join(dir, "pve", "qemu", "start", "20250101-000000.jsonl")
	fresh := filepath.Join(dir, "pve", "qemu", "stop", "20260722-000000.jsonl")
	writeLogFile(t, old, `{"msg":"old"}`+"\n", now.Add(-40*24*time.Hour))
	writeLogFile(t, fresh, `{"msg":"new"}`+"\n", now)

	stats, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now})
	require.NoError(t, err)
	require.Equal(t, 1, stats.Files)
	require.Equal(t, 0, stats.Empty)
	require.Equal(t, int64(14), stats.Bytes)

	require.NoFileExists(t, old)
	require.FileExists(t, fresh)

	// The emptied start/ directory chain collapses; the stop/ chain survives.
	require.NoDirExists(t, filepath.Join(dir, "pve", "qemu", "start"))
	require.DirExists(t, filepath.Join(dir, "pve", "qemu", "stop"))
	require.GreaterOrEqual(t, stats.Dirs, 1)
}

func TestPrune_EmptyFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	oldEmpty := filepath.Join(dir, "version", "20250101-000000.jsonl")
	freshEmpty := filepath.Join(dir, "version", "20260722-000000.jsonl")
	writeLogFile(t, oldEmpty, "", now.Add(-2*time.Hour))
	writeLogFile(t, freshEmpty, "", now)

	// Empty=false leaves both.
	stats, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now})
	require.NoError(t, err)
	require.Zero(t, stats.Files+stats.Empty)
	require.FileExists(t, oldEmpty)

	// Empty=true removes only the one past the EmptyMinAge floor.
	stats, err = logx.Prune(logx.PruneOptions{Dir: dir, Empty: true, Now: now})
	require.NoError(t, err)
	require.Equal(t, 1, stats.Empty)
	require.NoFileExists(t, oldEmpty)
	require.FileExists(t, freshEmpty,
		"a 0-byte file younger than EmptyMinAge may belong to a running command and must survive")
}

func TestPrune_DryRunRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	old := filepath.Join(dir, "pve", "node", "ls", "20250101-000000.jsonl")
	writeLogFile(t, old, `{"msg":"old"}`+"\n", now.Add(-400*24*time.Hour))

	stats, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, DryRun: true, Now: now})
	require.NoError(t, err)
	require.Equal(t, 1, stats.Files)
	require.GreaterOrEqual(t, stats.Dirs, 1, "dry-run must count directories that would empty out")

	require.FileExists(t, old)
	require.DirExists(t, filepath.Join(dir, "pve", "node", "ls"))
}

func TestPrune_SkipsNonJSONLFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	sentinel := filepath.Join(dir, logx.PruneSentinel)
	foreign := filepath.Join(dir, "notes.txt")
	writeLogFile(t, sentinel, "", now.Add(-100*24*time.Hour))
	writeLogFile(t, foreign, "keep me", now.Add(-100*24*time.Hour))

	stats, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: time.Hour, Empty: true, Now: now})
	require.NoError(t, err)
	require.Zero(t, stats.Files+stats.Empty)
	require.FileExists(t, sentinel)
	require.FileExists(t, foreign)
}

func TestPrune_EmptyDirRejected(t *testing.T) {
	_, err := logx.Prune(logx.PruneOptions{OlderThan: time.Hour})
	require.Error(t, err)
}

func TestAutoPrune_SentinelGatesToDaily(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	old := filepath.Join(dir, "pve", "task", "ls", "20250101-000000.jsonl")
	writeLogFile(t, old, `{"msg":"old"}`+"\n", now.Add(-90*24*time.Hour))

	stats, ran, err := logx.AutoPrune(dir, 30)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 1, stats.Files)
	require.NoFileExists(t, old)
	require.FileExists(t, filepath.Join(dir, logx.PruneSentinel))

	// Second call inside 24h is a no-op even with new prunable content.
	writeLogFile(t, old, `{"msg":"old-again"}`+"\n", now.Add(-90*24*time.Hour))
	_, ran, err = logx.AutoPrune(dir, 30)
	require.NoError(t, err)
	require.False(t, ran)
	require.FileExists(t, old)

	// An aged sentinel re-enables the prune.
	stale := now.Add(-25 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, logx.PruneSentinel), stale, stale))
	_, ran, err = logx.AutoPrune(dir, 30)
	require.NoError(t, err)
	require.True(t, ran)
	require.NoFileExists(t, old)
}

func TestAutoPrune_DisabledRetention(t *testing.T) {
	dir := t.TempDir()

	_, ran, err := logx.AutoPrune(dir, 0)
	require.NoError(t, err)
	require.False(t, ran)
	require.NoFileExists(t, filepath.Join(dir, logx.PruneSentinel))

	_, ran, err = logx.AutoPrune("", 30)
	require.NoError(t, err)
	require.False(t, ran)
}

func TestDefaultDir_UnderHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir, err := logx.DefaultDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tmp, ".pmx", "logs"), dir)
}
