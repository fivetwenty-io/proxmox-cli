package logx_test

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/logx"
)

// TestInit_NoLog verifies that NoLog=true produces no file on disk and that
// the returned logger does not panic when used.
func TestInit_NoLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	logger, closer, err := logx.Init(logx.Config{
		NoLog:   true,
		LogDir:  dir,
		Command: "test",
	})
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, closer, "closer must be non-nil even when NoLog=true")
	require.NoError(t, closer.Close(), "no-op closer must not return an error")

	// Using the logger must not panic.
	logger.Info("discard message", slog.String("k", "v"))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "no log file should be created when NoLog=true")
}

// TestInit_CreatesFile verifies that Init creates exactly one file under LogDir.
func TestInit_CreatesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	logger, closer, err := logx.Init(logx.Config{
		LogDir:  dir,
		Command: "qemu",
	})
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, closer)
	t.Cleanup(func() { _ = closer.Close() })

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly one log file should be created")

	name := entries[0].Name()
	require.True(t, strings.HasSuffix(name, ".jsonl"), "log file must have .jsonl extension, got %q", name)
	require.True(t, strings.HasPrefix(name, "qemu-"), "filename must start with command name, got %q", name)
}

// TestInit_FilePermissions verifies that the log file is created with mode 0600
// and the log directory with mode 0700.
func TestInit_FilePermissions(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// Use a sub-directory that Init must create.
	dir := filepath.Join(base, "nested")

	_, closer, err := logx.Init(logx.Config{
		LogDir:  dir,
		Command: "perm-test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm(),
		"log directory should have mode 0700")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	fileInfo, err := entries[0].Info()
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm(),
		"log file should have mode 0600")
}

// TestInit_ValidJSONL writes a log entry through Init and then reads the file
// back, asserting it is valid JSONL with the expected keys.
func TestInit_ValidJSONL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	logger, closer, err := logx.Init(logx.Config{
		LogDir:     dir,
		Command:    "node",
		Subcommand: "status",
		Node:       "pve1",
		VMID:       "100",
	})
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, closer)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("test message", slog.String("extra", "value"))

	// Read the single log file.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Parse each line as a JSON object.
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var lineCount int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lineCount++
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record),
			"each line must be valid JSON: %q", line)

		// Standard slog JSONL keys.
		require.Contains(t, record, "time", "record must contain 'time' key")
		require.Contains(t, record, "level", "record must contain 'level' key")
		require.Contains(t, record, "msg", "record must contain 'msg' key")

		// Base attributes from Config.
		require.Contains(t, record, "command", "record must contain 'command' base attr")
		require.Contains(t, record, "subcommand", "record must contain 'subcommand' base attr")
		require.Contains(t, record, "node", "record must contain 'node' base attr")
		require.Contains(t, record, "vmid", "record must contain 'vmid' base attr")

		require.Equal(t, "node", record["command"])
		require.Equal(t, "status", record["subcommand"])
		require.Equal(t, "pve1", record["node"])
		require.Equal(t, "100", record["vmid"])
		require.Equal(t, "test message", record["msg"])
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 1, lineCount, "expected exactly one log line")
}

// TestInit_Debug verifies that debug messages are recorded when Debug=true.
func TestInit_Debug(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	logger, closer, err := logx.Init(logx.Config{
		LogDir:  dir,
		Command: "debug-test",
		Debug:   true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Debug("debug entry")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(data), "debug entry",
		"debug message must appear in log file when Debug=true")
}

// TestInit_VerboseLevel verifies that Verbose=true also enables DEBUG logging.
func TestInit_VerboseLevel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	logger, closer, err := logx.Init(logx.Config{
		LogDir:  dir,
		Command: "verbose-test",
		Verbose: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Debug("verbose debug entry")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(data), "verbose debug entry",
		"debug message must appear when Verbose=true")
}

// TestInit_InfoLevelSuppressesDebug verifies that at default Info level,
// debug messages are not written to the file.
func TestInit_InfoLevelSuppressesDebug(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	logger, closer, err := logx.Init(logx.Config{
		LogDir:  dir,
		Command: "info-test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Debug("should be suppressed")
	logger.Info("should appear")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	content := string(data)
	require.NotContains(t, content, "should be suppressed",
		"debug message must not appear at info level")
	require.Contains(t, content, "should appear",
		"info message must appear at info level")
}

// TestInit_LevelStringDebug verifies that Level="debug" enables DEBUG logging.
func TestInit_LevelStringDebug(t *testing.T) {
	t.Parallel()

	for _, lvl := range []string{"trace", "verbose", "debug"} {
		lvl := lvl
		t.Run(lvl, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			logger, closer, err := logx.Init(logx.Config{
				LogDir:  dir,
				Command: "level-test",
				Level:   lvl,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = closer.Close() })

			logger.Debug("level-controlled debug")

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)

			data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			require.NoError(t, err)
			require.Contains(t, string(data), "level-controlled debug",
				"debug message must appear for level %q", lvl)
		})
	}
}

// TestLogPath verifies the filename formula: {Command}[-{Subcommand}]-{YYYYMMDD-HHMMSS}.jsonl
func TestLogPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command    string
		subcommand string
		wantPrefix string
	}{
		{command: "qemu", subcommand: "start", wantPrefix: "qemu-start-"},
		{command: "node", subcommand: "", wantPrefix: "node-"},
		{command: "", subcommand: "", wantPrefix: "pmx-"},
		{command: "lxc", subcommand: "stop", wantPrefix: "lxc-stop-"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.wantPrefix, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			before := time.Now().Truncate(time.Second)
			_, closer, err := logx.Init(logx.Config{
				LogDir:     dir,
				Command:    tc.command,
				Subcommand: tc.subcommand,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = closer.Close() })

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)

			name := entries[0].Name()
			require.True(t, strings.HasPrefix(name, tc.wantPrefix),
				"filename %q must start with %q", name, tc.wantPrefix)
			require.True(t, strings.HasSuffix(name, ".jsonl"),
				"filename %q must end with .jsonl", name)

			// Extract timestamp portion and verify it is parseable.
			// Strip prefix and ".jsonl" suffix to get the timestamp.
			rest := strings.TrimPrefix(name, tc.wantPrefix)
			ts := strings.TrimSuffix(rest, ".jsonl")
			parsed, err := time.ParseInLocation("20060102-150405", ts, time.Local)
			require.NoError(t, err, "timestamp in filename %q must be parseable", name)
			require.False(t, parsed.Before(before),
				"timestamp %v must not be before test start %v", parsed, before)
		})
	}
}

// TestInit_BaseAttrsPartial verifies that only non-empty base attributes are
// attached to log records.
func TestInit_BaseAttrsPartial(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Only Command set; Node and VMID empty.
	logger, closer, err := logx.Init(logx.Config{
		LogDir:  dir,
		Command: "storage",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	logger.Info("partial attrs")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	var record map[string]any
	require.NoError(t, json.Unmarshal(data, &record))

	require.Equal(t, "storage", record["command"])
	require.NotContains(t, record, "node", "node attr must not appear when empty")
	require.NotContains(t, record, "vmid", "vmid attr must not appear when empty")
	require.NotContains(t, record, "subcommand", "subcommand attr must not appear when empty")
}

// TestLevelVar verifies that LevelVar returns a *slog.LevelVar reflecting cfg.
func TestLevelVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   logx.Config
		level slog.Level
	}{
		{name: "default", cfg: logx.Config{}, level: slog.LevelInfo},
		{name: "debug flag", cfg: logx.Config{Debug: true}, level: slog.LevelDebug},
		{name: "verbose flag", cfg: logx.Config{Verbose: true}, level: slog.LevelDebug},
		{name: "level debug string", cfg: logx.Config{Level: "debug"}, level: slog.LevelDebug},
		{name: "level trace string", cfg: logx.Config{Level: "trace"}, level: slog.LevelDebug},
		{name: "level verbose string", cfg: logx.Config{Level: "verbose"}, level: slog.LevelDebug},
		{name: "level info string", cfg: logx.Config{Level: "info"}, level: slog.LevelInfo},
		{name: "level warn string", cfg: logx.Config{Level: "warn"}, level: slog.LevelWarn},
		{name: "level warning string", cfg: logx.Config{Level: "warning"}, level: slog.LevelWarn},
		{name: "level error string", cfg: logx.Config{Level: "error"}, level: slog.LevelError},
		{name: "level err string", cfg: logx.Config{Level: "err"}, level: slog.LevelError},
		{name: "level unknown string", cfg: logx.Config{Level: "bogus"}, level: slog.LevelInfo},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := logx.LevelVar(tc.cfg)
			require.NotNil(t, v)
			require.Equal(t, tc.level, v.Level())
		})
	}
}

// TestInit_NestedLayout verifies the default nested layout: with CommandPath
// set the file lands under per-segment subdirectories and the filename is
// just the timestamp.
func TestInit_NestedLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	before := time.Now().Truncate(time.Second)
	_, closer, err := logx.Init(logx.Config{
		LogDir:      dir,
		CommandPath: []string{"pve", "storage", "volume", "copy"},
		Command:     "pve",
		Subcommand:  "storage-volume-copy",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	nested := filepath.Join(dir, "pve", "storage", "volume", "copy")
	entries, err := os.ReadDir(nested)
	require.NoError(t, err, "nested log directory must exist")
	require.Len(t, entries, 1)

	name := entries[0].Name()
	require.True(t, strings.HasSuffix(name, ".jsonl"),
		"filename %q must end with .jsonl", name)

	ts := strings.TrimSuffix(name, ".jsonl")
	parsed, err := time.ParseInLocation("20060102-150405", ts, time.Local)
	require.NoError(t, err, "nested filename %q must be a bare timestamp", name)
	require.False(t, parsed.Before(before))

	// Intermediate directories are created with mode 0700.
	info, err := os.Stat(filepath.Join(dir, "pve"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// TestInit_FlatLayoutOverride verifies that Flat=true keeps the flat filename
// layout even when CommandPath is set.
func TestInit_FlatLayoutOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, closer, err := logx.Init(logx.Config{
		LogDir:      dir,
		Flat:        true,
		CommandPath: []string{"pve", "storage", "volume", "copy"},
		Command:     "pve",
		Subcommand:  "storage-volume-copy",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.False(t, entries[0].IsDir(), "flat layout must not create subdirectories")
	require.True(t, strings.HasPrefix(entries[0].Name(), "pve-storage-volume-copy-"),
		"flat filename must encode the command path, got %q", entries[0].Name())
}

// TestInit_NestedSkipsUnsafeSegments verifies that empty or path-unsafe
// CommandPath segments are dropped from the nested directory path.
func TestInit_NestedSkipsUnsafeSegments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, closer, err := logx.Init(logx.Config{
		LogDir:      dir,
		CommandPath: []string{"", "..", "pve", "a/b", "status"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	entries, err := os.ReadDir(filepath.Join(dir, "pve", "status"))
	require.NoError(t, err, "only safe segments must form the nested path")
	require.Len(t, entries, 1)
}

// TestInit_WarnLevelSuppressesInfo verifies that Level="warn" drops info
// records but keeps warnings, and Level="error" keeps only errors.
func TestInit_WarnLevelSuppressesInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level       string
		wantPresent string
		wantAbsent  []string
	}{
		{level: "warn", wantPresent: "warn record", wantAbsent: []string{"info record", "debug record"}},
		{level: "error", wantPresent: "error record", wantAbsent: []string{"info record", "warn record"}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.level, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			logger, closer, err := logx.Init(logx.Config{
				LogDir:  dir,
				Command: "level-filter",
				Level:   tc.level,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = closer.Close() })

			logger.Debug("debug record")
			logger.Info("info record")
			logger.Warn("warn record")
			logger.Error("error record")

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)

			data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			require.NoError(t, err)
			content := string(data)

			require.Contains(t, content, tc.wantPresent)
			for _, absent := range tc.wantAbsent {
				require.NotContains(t, content, absent,
					"%q must be suppressed at level %q", absent, tc.level)
			}
		})
	}
}

// TestInit_MultipleEntries writes several records and verifies all appear as
// separate JSONL lines.
func TestInit_MultipleEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	logger, closer, err := logx.Init(logx.Config{
		LogDir:  dir,
		Command: "multi",
		Debug:   true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	messages := []string{"first", "second", "third"}
	for _, m := range messages {
		logger.Info(m)
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, len(messages))

	for i, line := range lines {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		require.Equal(t, messages[i], record["msg"])
	}
}

// TestInit_CloserNonNilAndReleasesFile verifies that:
//   - the closer returned by Init is never nil (file and no-file paths),
//   - Close() on a file closer returns nil and releases the fd so the file
//     can be renamed/deleted on all platforms, and
//   - Close() on a no-op closer returns nil.
func TestInit_CloserNonNilAndReleasesFile(t *testing.T) {
	t.Parallel()

	t.Run("noop closer is non-nil and Close returns nil", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		_, closer, err := logx.Init(logx.Config{
			NoLog:   true,
			LogDir:  dir,
			Command: "closer-noop",
		})
		require.NoError(t, err)
		require.NotNil(t, closer, "closer must be non-nil when NoLog=true")
		require.NoError(t, closer.Close(), "noop closer.Close() must return nil")
	})

	t.Run("file closer is non-nil and Close releases fd", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		_, closer, err := logx.Init(logx.Config{
			LogDir:  dir,
			Command: "closer-file",
		})
		require.NoError(t, err)
		require.NotNil(t, closer, "closer must be non-nil when a log file is opened")

		// Close must succeed.
		require.NoError(t, closer.Close(), "file closer.Close() must return nil")

		// After Close the fd is released; we can delete the file on all platforms.
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.NoError(t,
			os.Remove(filepath.Join(dir, entries[0].Name())),
			"file must be removable after closer.Close()",
		)
	})
}
