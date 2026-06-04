// Package exec provides the Runner interface for shell-out commands (ssh, rsync)
// with a real os/exec-backed implementation and a testable fake.
package exec

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
	// Code is the process exit code (always > 0 when this error is returned).
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
	var ee *ExitError
	if e, ok := err.(*ExitError); ok {
		ee = e
	}
	if ee != nil {
		return ee.Code
	}
	return -1
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
