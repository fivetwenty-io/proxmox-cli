package logx_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// requireNotRoot skips a test whose premise is that the filesystem denies
// access, since root is exempt from the permission bits that create the
// denial. This is the one case where skipping is honest: the scenario cannot
// exist for that user, as opposed to a skip that hides a real failure.
func requireNotRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits cannot deny access, so the failure under test cannot occur")
	}
}

// TestPrune_UnremovableFileIsReportedAndTheRestStillPrune covers the
// best-effort contract: a file the process cannot delete must be reported,
// but must not cost the operator every other deletion in the tree. Prune is
// how a 400 MB log directory gets back under control, so aborting on the
// first stubborn file would leave the disk full.
func TestPrune_UnremovableFileIsReportedAndTheRestStillPrune(t *testing.T) {
	requireNotRoot(t)

	dir := t.TempDir()
	now := time.Now()

	locked := filepath.Join(dir, "locked", "20250101-000000.jsonl")
	free := filepath.Join(dir, "free", "20250101-000000.jsonl")
	writeLogFile(t, locked, `{"msg":"x"}`+"\n", now.Add(-90*24*time.Hour))
	writeLogFile(t, free, `{"msg":"y"}`+"\n", now.Add(-90*24*time.Hour))

	// Removing a file needs write permission on its directory, not the file.
	require.NoError(t, os.Chmod(filepath.Dir(locked), 0o500))
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(locked), 0o700) })

	stats, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now})

	require.Error(t, err, "a file that could not be removed must be reported")
	require.Contains(t, err.Error(), "20250101-000000.jsonl", "the error must name the file")
	require.FileExists(t, locked)

	require.NoFileExists(t, free, "the rest of the tree must still be pruned")
	require.Equal(t, 1, stats.Files, "only the successful removal counts")
	require.Equal(t, int64(12), stats.Bytes, "and only its bytes are reclaimed")
}

// TestPrune_UnreadableDirectoryIsReportedAndTheRestStillPrune covers the walk
// error path: an unreadable subtree is skipped and reported rather than
// aborting the walk, so one bad directory does not hide the whole prune.
func TestPrune_UnreadableDirectoryIsReportedAndTheRestStillPrune(t *testing.T) {
	requireNotRoot(t)

	dir := t.TempDir()
	now := time.Now()

	hidden := filepath.Join(dir, "unreadable", "20250101-000000.jsonl")
	free := filepath.Join(dir, "free", "20250101-000000.jsonl")
	writeLogFile(t, hidden, `{"msg":"x"}`+"\n", now.Add(-90*24*time.Hour))
	writeLogFile(t, free, `{"msg":"y"}`+"\n", now.Add(-90*24*time.Hour))

	require.NoError(t, os.Chmod(filepath.Dir(hidden), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(hidden), 0o700) })

	stats, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now})

	require.Error(t, err, "an unreadable directory must be reported")
	require.NoFileExists(t, free, "the readable part of the tree must still be pruned")
	require.Equal(t, 1, stats.Files)
}

// TestPrune_ConcurrentPrunesBothSucceed pins the tolerance for a file another
// prune removed first. Two invocations can pass the daily sentinel gate at
// once, and "the file I was about to delete is already gone" is the outcome
// both wanted, not an error to report.
func TestPrune_ConcurrentPrunesBothSucceed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	for i := range 60 {
		writeLogFile(t,
			filepath.Join(dir, "pve", fmt.Sprintf("cmd%d", i), "20250101-000000.jsonl"),
			`{"msg":"x"}`+"\n", now.Add(-90*24*time.Hour))
	}

	opts := logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	files := make([]int, 2)
	for i := range errs {
		wg.Go(func() {
			stats, err := logx.Prune(opts)
			errs[i], files[i] = err, stats.Files
		})
	}
	wg.Wait()

	require.NoError(t, errs[0], "a file another prune removed first is not a failure")
	require.NoError(t, errs[1])

	// Only bounds are asserted, not an exact split: on darwin two concurrent
	// unlink calls on one path can both report success, so neither the kernel
	// nor Prune can attribute every deletion to exactly one caller.
	require.Positive(t, files[0]+files[1])
	require.LessOrEqual(t, files[0], 60)
	require.LessOrEqual(t, files[1], 60)

	// Every log file is gone, which is the point of the prune.
	require.Empty(t, remainingLogFiles(t, dir))

	// Directories are the weaker guarantee: each prune decides a directory is
	// empty from the children its own walk saw, so when the other prune
	// removed some of them first, a now-empty parent can survive the pair. The
	// residue is self-healing — the next prune sees a directory with nothing
	// under it and collapses it — which is what makes it acceptable.
	_, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now})
	require.NoError(t, err)
	require.NoDirExists(t, filepath.Join(dir, "pve"))
}

// TestAutoPrune_ReportsAnUntouchableSentinel covers the gate's own failure:
// if the sentinel cannot be written, the prune must not run, because a prune
// that cannot record itself would repeat on every single command.
func TestAutoPrune_ReportsAnUntouchableSentinel(t *testing.T) {
	requireNotRoot(t)

	dir := t.TempDir()
	now := time.Now()

	old := filepath.Join(dir, "pve", "task", "ls", "20250101-000000.jsonl")
	writeLogFile(t, old, `{"msg":"old"}`+"\n", now.Add(-90*24*time.Hour))

	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	stats, ran, err := logx.AutoPrune(dir, 30)

	require.Error(t, err)
	require.False(t, ran, "no prune may run without the sentinel that rate-limits it")
	require.Zero(t, stats.Files)
	require.FileExists(t, old, "and nothing may be deleted")
}

// remainingLogFiles returns every .jsonl file left under dir.
func remainingLogFiles(t *testing.T, dir string) []string {
	t.Helper()

	var left []string
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			left = append(left, path)
		}
		return nil
	}))

	return left
}

// TestPrune_RemovesAlreadyEmptyDirectories covers directories this run did not
// empty itself. A directory only entered the bookkeeping by having a child
// walked, so one left behind by an earlier prune survived every run after it —
// the log tree kept its skeleton forever.
func TestPrune_RemovesAlreadyEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	stale := filepath.Join(dir, "pve", "qemu", "start")
	require.NoError(t, os.MkdirAll(stale, 0o700))

	keep := filepath.Join(dir, "pve", "lxc", "20260101-000000.jsonl")
	writeLogFile(t, keep, `{"msg":"new"}`+"\n", now)

	stats, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now})
	require.NoError(t, err)

	require.NoDirExists(t, stale, "an empty directory must be collapsed even if this run did not empty it")
	require.NoDirExists(t, filepath.Join(dir, "pve", "qemu"), "and so must the parent it leaves empty")
	require.GreaterOrEqual(t, stats.Dirs, 2)

	require.FileExists(t, keep, "a directory with a surviving log file must stay")
	require.DirExists(t, dir, "the log root itself is never removed")
}

// TestPrune_KeepsDirectoriesHoldingForeignFiles is the guard on that: only
// .jsonl files are pruned, so a directory holding anything else must survive
// even though none of its children were removed.
func TestPrune_KeepsDirectoriesHoldingForeignFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	notes := filepath.Join(dir, "pve", "notes.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(notes), 0o700))
	require.NoError(t, os.WriteFile(notes, []byte("operator's own file\n"), 0o600))

	old := filepath.Join(dir, "pve", "20250101-000000.jsonl")
	writeLogFile(t, old, `{"msg":"old"}`+"\n", now.Add(-90*24*time.Hour))

	_, err := logx.Prune(logx.PruneOptions{Dir: dir, OlderThan: 30 * 24 * time.Hour, Now: now})
	require.NoError(t, err)

	require.NoFileExists(t, old)
	require.FileExists(t, notes, "a foreign file must never be removed")
	require.DirExists(t, filepath.Dir(notes), "nor the directory holding it")
}
