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

// shieldParentSignals keeps this process alive across a terminal signal while
// a child (ssh, rsync) runs, so it survives long enough to read the child's
// real exit status and propagate it as an *ExitError. It returns a stop
// function the caller defers.
//
// It uses signal.Notify rather than signal.Ignore deliberately. SIG_IGN is
// inherited across execve and — unlike rsync, which installs its handlers
// unconditionally — ssh installs its own with the guarded POSIX idiom
// (`if ssh_signal(SIGINT, SIG_IGN) != SIG_IGN`), which preserves an inherited
// SIG_IGN and never installs the handler. An ignoring parent therefore made
// every scripted ssh uninterruptible: neither ^C nor ^\ reached it, and the
// only way to stop a wrong `node exec` or a wrong `lab create` was to kill
// pmx from another terminal, which orphaned the ssh. A Notify-registered
// signal instead leaves the child with SIG_DFL across execve, so ssh installs
// its handler and dies on ^C exactly as an operator expects, while the Go
// runtime keeps this process from dying.
//
// The child is deliberately left in this process's own process group, so a
// terminal-generated SIGINT/SIGQUIT still reaches it directly. A signal
// directed at pmx alone (`kill <pid>`, which the group never sees) is relayed
// instead, so a killed pmx never leaves an orphaned ssh running a remote
// command with nothing supervising it.
// It returns a relay function and a stop function. Catching begins
// immediately — before the child is started, so no signal in that window can
// kill this process — while relay is called with the started child, handing
// the process over the channel rather than sharing it, since cmd.Process is
// written by Start and must not be read concurrently.
func shieldParentSignals() (relay func(*os.Process), stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGQUIT, syscall.SIGTERM)

	procCh := make(chan *os.Process, 1)
	done := make(chan struct{})

	go func() {
		var proc *os.Process
		for {
			select {
			case proc = <-procCh:
			case sig := <-ch:
				if proc != nil {
					// An already-exited child returns an error here, which
					// is the expected case when the terminal just delivered
					// the same signal to the whole process group.
					_ = proc.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()

	return func(p *os.Process) { procCh <- p }, func() {
		signal.Stop(ch)
		close(done)
	}
}

// runShielded starts cmd under shieldParentSignals, waits for it, and maps a
// non-zero exit to *ExitError. what names the operation for the start-failure
// message ("exec ssh", "exec interactive ssh").
func runShielded(cmd *exec.Cmd, what string) error {
	relay, stop := shieldParentSignals()
	defer stop()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	relay(cmd.Process)

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return &ExitError{
				Code: exitErr.ExitCode(),
				Err:  exitErr,
			}
		}
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

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

	return runShielded(cmd, "exec "+name)
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

	return runShielded(cmd, "exec interactive "+name)
}
