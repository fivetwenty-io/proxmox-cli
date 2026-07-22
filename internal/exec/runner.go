// Package exec provides the Runner interface for shell-out commands (ssh, rsync)
// with a real os/exec-backed implementation and a testable fake.
package exec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Runner abstracts os/exec for SSH/rsync shell-outs.
// The real implementation calls exec.Command; the fake implementation records
// calls for unit tests.
type Runner interface {
	// Run executes name with args, merging env into the process environment.
	// stdin, stdout, and stderr are wired to the given io readers/writers.
	// Returns an error whose exit code is accessible via ExitCodeOf.
	Run(name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error

	// RunInteractive attaches the current process stdin/stdout/stderr for PTY
	// pass-through (e.g. interactive SSH sessions, shell).
	// env is merged into the inherited environment.
	RunInteractive(name string, args []string, env []string) error
}

// ExitError is returned by Run and RunInteractive when the child process exits
// with a non-zero status. It wraps the underlying error and exposes the code.
type ExitError struct {
	// Code is the process exit code, taken directly from (*exec.ExitError).
	// ExitCode(): usually > 0, but -1 when the child was terminated by a
	// signal rather than exiting normally (Go's os/exec cannot recover a
	// signal-terminated process's "exit code" — there isn't one).
	Code int
	// Err is the underlying *exec.ExitError from os/exec.
	Err error
}

// Error implements the error interface.
func (e *ExitError) Error() string {
	return fmt.Sprintf("process exited with code %d: %v", e.Code, e.Err)
}

// Unwrap returns the underlying error so errors.Is / errors.As work correctly.
func (e *ExitError) Unwrap() error {
	return e.Err
}

// ExitCodeOf returns the exit code from err if it is an *ExitError, otherwise
// returns -1. Callers that need only the code can use this helper.
func ExitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*ExitError](err); ok {
		return ee.Code
	}
	return -1
}

// CapturedError marks an error as originating from a Run call whose
// stdout/stderr were captured into in-memory buffers rather than passed
// through to the real terminal (as RunInteractive does, and as a Run call
// wired directly to e.g. cmd.OutOrStdout()/cmd.ErrOrStderr() does). The
// distinction matters to the CLI's top-level error handler (internal/cli.
// Execute): when a child process's own stdout/stderr WAS the real terminal,
// whatever it printed is already visible to the user, so re-printing a
// wrapped *ExitError on top would just duplicate that output — but when a
// caller captured the child's streams into buffers instead (e.g. this
// package's callers that build their own error message folding captured
// stderr into it), the user has been shown NOTHING; silently swallowing
// that error under the same "already printed" assumption hides it entirely.
//
// A caller that captures output and constructs its own ready-to-print error
// (already including whatever captured output it wants shown) should wrap
// that error with NewCapturedError so Execute knows to print it after all.
type CapturedError struct {
	err error
}

// NewCapturedError wraps err as a CapturedError. Returns nil for a nil err,
// so callers can use it unconditionally on a possibly-nil error return
// without an extra nil check.
func NewCapturedError(err error) error {
	if err == nil {
		return nil
	}
	return &CapturedError{err: err}
}

// Error returns the wrapped error's message unchanged — CapturedError is a
// pure marker, not a distinct error message.
func (e *CapturedError) Error() string { return e.err.Error() }

// Unwrap returns the wrapped error so errors.Is/errors.As (e.g. ExitCodeOf,
// exitcode.FromError's *ExitError lookup) still see through this marker to
// whatever real error/exit-code information is underneath it.
func (e *CapturedError) Unwrap() error { return e.err }

// realRunner is the production Runner backed by os/exec.
type realRunner struct{}

// Real returns a Runner backed by os/exec.
func Real() Runner {
	return &realRunner{}
}

// Run executes name with the given args. env entries (KEY=VALUE) are appended
// to the current process environment. stdin, stdout, and stderr are wired to
// the provided readers/writers. A non-zero exit code is wrapped as *ExitError.
func (r *realRunner) Run(name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command(name, args...) //nolint:gosec // caller provides vetted arguments
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// The parent must not die from SIGINT/SIGQUIT while the child runs: both
	// signals are delivered to the whole foreground process group (parent and
	// child alike), so without this the shell's ^C would kill pve itself
	// before it can read the child's real exit status and propagate it via
	// ExitError. signal.Ignore sets SIG_IGN in this process, and SIG_IGN is
	// inherited across execve by the child — that is fine here because ssh
	// and rsync each install their own SIGINT/SIGQUIT handlers at startup, so
	// the child still responds to ^C normally; the point of ignoring here is
	// only that the PARENT survives ^C long enough to read and report the
	// child's real exit status.
	signal.Ignore(os.Interrupt, syscall.SIGQUIT)
	defer signal.Reset(os.Interrupt, syscall.SIGQUIT)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &ExitError{
				Code: exitErr.ExitCode(),
				Err:  exitErr,
			}
		}
		return fmt.Errorf("exec %s: %w", name, err)
	}
	return nil
}

// RunInteractive executes name with the given args and wires the current
// process's stdin/stdout/stderr directly (PTY pass-through). env entries are
// appended to the inherited environment. A non-zero exit code is wrapped as
// *ExitError.
func (r *realRunner) RunInteractive(name string, args []string, env []string) error {
	cmd := exec.Command(name, args...) //nolint:gosec // caller provides vetted arguments
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// See the matching comment in Run: without this, ^C (SIGINT) or SIGQUIT
	// delivered to the foreground process group would kill pve itself, not
	// just the interactive child (ssh, shell), preventing pve from reading
	// and propagating the child's real exit code. SIG_IGN is inherited by
	// the child, but ssh installs its own SIGINT/SIGQUIT handlers at
	// startup, so it still responds to ^C normally (e.g. forwarding it to
	// the remote session).
	signal.Ignore(os.Interrupt, syscall.SIGQUIT)
	defer signal.Reset(os.Interrupt, syscall.SIGQUIT)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &ExitError{
				Code: exitErr.ExitCode(),
				Err:  exitErr,
			}
		}
		return fmt.Errorf("exec interactive %s: %w", name, err)
	}
	return nil
}
