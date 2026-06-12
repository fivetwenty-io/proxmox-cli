// Package logx initialises the slog JSONL file logger for pve CLI commands.
package logx

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Config mirrors the OCFP logger config adapted for pve (R3).
// All fields are optional; zero values produce sensible defaults.
type Config struct {
	// Level is the log level string: "trace", "verbose", "debug", "info", "warn", "error".
	// "trace", "verbose", and "debug" all map to slog.LevelDebug.
	Level string

	// Debug enables DEBUG-level logging regardless of Level.
	Debug bool

	// Verbose enables DEBUG-level logging regardless of Level.
	Verbose bool

	// NoLog suppresses all file I/O; returns a discard logger when true.
	NoLog bool

	// LogDir overrides the default log directory (~/.pve/logs).
	// Ignored when NoLog is true.
	LogDir string

	// Command is the top-level cobra command name (e.g. "qemu").
	Command string

	// Subcommand is the sub-cobra command name (e.g. "start").
	Subcommand string

	// Node is the --node flag value; attached as a base slog attribute.
	Node string

	// VMID is the --vmid flag value; attached as a base slog attribute.
	VMID string
}

// nopCloser is a no-op io.Closer returned when no log file is opened.
// Close always succeeds and is safe to defer unconditionally.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// Init initialises a *slog.Logger that writes JSONL to a file under LogDir.
//
// When cfg.NoLog is true Init returns a logger backed by io.Discard, a no-op
// io.Closer, and a nil error — no files or directories are created.
//
// When a log file is opened the returned io.Closer wraps the *os.File; the
// caller must call Close() (typically via defer) to flush buffered data and
// release the file descriptor.  The closer is always non-nil on a nil error.
//
// The log file is named:
//
//	{Command}[-{Subcommand}]-{YYYYMMDD-HHMMSS}.jsonl
//
// The directory is created with mode 0700; the file is opened with mode 0600.
// Base attributes command, node, and vmid (non-empty values only) are
// attached to every log record via logger.With.
//
// Level precedence: cfg.Debug || cfg.Verbose || cfg.Level in
// {"trace","verbose","debug"} → slog.LevelDebug; anything else → slog.LevelInfo.
func Init(cfg Config) (*slog.Logger, io.Closer, error) {
	level := levelFromCfg(cfg)

	if cfg.NoLog {
		h := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: level})
		return withBaseAttrs(slog.New(h), cfg), nopCloser{}, nil
	}

	logDir, err := resolveLogDir(cfg.LogDir)
	if err != nil {
		return nil, nil, fmt.Errorf("logx: resolve log dir: %w", err)
	}

	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("logx: mkdir %s: %w", logDir, err)
	}

	filename := buildFilename(cfg.Command, cfg.Subcommand, time.Now())
	path := filepath.Join(logDir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: path is logDir+timestamped-filename under ~/.pve/logs, not untrusted input
	if err != nil {
		return nil, nil, fmt.Errorf("logx: open log file %s: %w", path, err)
	}

	h := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level})
	return withBaseAttrs(slog.New(h), cfg), f, nil
}

// LevelVar returns a *slog.LevelVar initialised from cfg.
// Callers can use it to adjust the log level at runtime.
func LevelVar(cfg Config) *slog.LevelVar {
	v := &slog.LevelVar{}
	v.Set(levelFromCfg(cfg))
	return v
}

// levelFromCfg maps the Config fields to a slog.Level.
// Debug/Verbose flags and the "trace"/"verbose"/"debug" strings all select LevelDebug.
func levelFromCfg(cfg Config) slog.Level {
	if cfg.Debug || cfg.Verbose {
		return slog.LevelDebug
	}
	switch cfg.Level {
	case "trace", "verbose", "debug":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// resolveLogDir returns the effective log directory.
// An empty override means the default ~/.pve/logs.
func resolveLogDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".pve", "logs"), nil
}

// buildFilename returns the log filename for the given command, optional
// subcommand, and timestamp.
//
// Format: {Command}[-{Subcommand}]-{YYYYMMDD-HHMMSS}.jsonl
//
// If Command is empty the prefix "pve" is used as a fallback.
func buildFilename(command, subcommand string, ts time.Time) string {
	prefix := command
	if prefix == "" {
		prefix = "pve"
	}
	if subcommand != "" {
		prefix = prefix + "-" + subcommand
	}
	return prefix + "-" + ts.Format("20060102-150405") + ".jsonl"
}

// withBaseAttrs attaches non-empty base attributes (command, node, vmid)
// to the logger and returns it.
func withBaseAttrs(l *slog.Logger, cfg Config) *slog.Logger {
	var attrs []any
	if cfg.Command != "" {
		attrs = append(attrs, slog.String("command", cfg.Command))
	}
	if cfg.Subcommand != "" {
		attrs = append(attrs, slog.String("subcommand", cfg.Subcommand))
	}
	if cfg.Node != "" {
		attrs = append(attrs, slog.String("node", cfg.Node))
	}
	if cfg.VMID != "" {
		attrs = append(attrs, slog.String("vmid", cfg.VMID))
	}
	if len(attrs) == 0 {
		return l
	}
	return l.With(attrs...)
}
