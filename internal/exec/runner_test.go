package exec_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	execpkg "github.com/fivetwenty-io/pmx-cli/internal/exec"
)

// ---------------------------------------------------------------------------
// Real runner tests
// ---------------------------------------------------------------------------

// TestReal_Echo verifies that the real runner executes /bin/echo and captures
// its stdout through the provided writer.
func TestReal_Echo(t *testing.T) {
	t.Parallel()

	r := execpkg.Real()
	var stdout, stderr bytes.Buffer

	err := r.Run("/bin/echo", []string{"hello", "world"}, nil, nil, &stdout, &stderr)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", stdout.String())
	require.Empty(t, stderr.String())
}

// TestReal_EchoEnv verifies that extra env entries are available to the child
// process by running sh -c 'echo $PMX_TEST_VAR'.
func TestReal_EchoEnv(t *testing.T) {
	t.Parallel()

	r := execpkg.Real()
	var stdout bytes.Buffer

	err := r.Run("/bin/sh", []string{"-c", "echo $PMX_TEST_VAR"}, []string{"PMX_TEST_VAR=fivetwenty"}, nil, &stdout, nil)
	require.NoError(t, err)
	require.Equal(t, "fivetwenty\n", stdout.String())
}

// TestReal_Stdin verifies that stdin content is forwarded to the child process.
// We use 'cat' to echo back whatever is on stdin.
func TestReal_Stdin(t *testing.T) {
	t.Parallel()

	r := execpkg.Real()
	input := "ping"
	var stdout bytes.Buffer

	err := r.Run("/bin/cat", nil, nil, strings.NewReader(input), &stdout, nil)
	require.NoError(t, err)
	require.Equal(t, input, stdout.String())
}

// TestReal_ExitError verifies that a non-zero exit status is surfaced as an
// *ExitError with the correct code, and that ExitCodeOf extracts it.
// We use 'sh -c "exit 1"' for portability across Linux and macOS.
func TestReal_ExitError(t *testing.T) {
	t.Parallel()

	r := execpkg.Real()
	var stdout, stderr bytes.Buffer

	err := r.Run("/bin/sh", []string{"-c", "exit 1"}, nil, nil, &stdout, &stderr)
	require.Error(t, err)

	var exitErr *execpkg.ExitError
	require.True(t, errors.As(err, &exitErr), "expected *ExitError, got %T: %v", err, err)
	require.Equal(t, 1, exitErr.Code)
	require.Equal(t, 1, execpkg.ExitCodeOf(err))
}

// TestReal_ExitCode_NonOne runs 'sh -c "exit 42"' and checks that the returned
// ExitError carries exactly code 42.
func TestReal_ExitCode_NonOne(t *testing.T) {
	t.Parallel()

	r := execpkg.Real()
	err := r.Run("/bin/sh", []string{"-c", "exit 42"}, nil, nil, nil, nil)
	require.Error(t, err)

	require.Equal(t, 42, execpkg.ExitCodeOf(err))
}

// TestReal_NonExistentBinary verifies that an exec-level error (binary not
// found) is returned as a wrapped error (not *ExitError).
func TestReal_NonExistentBinary(t *testing.T) {
	t.Parallel()

	r := execpkg.Real()
	err := r.Run("/nonexistent/binary/xyz", nil, nil, nil, nil, nil)
	require.Error(t, err)

	// ExitCodeOf returns -1 for non-ExitError errors.
	require.Equal(t, -1, execpkg.ExitCodeOf(err))
}

// ---------------------------------------------------------------------------
// ExitCodeOf helper
// ---------------------------------------------------------------------------

func TestExitCodeOf_Nil(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, execpkg.ExitCodeOf(nil))
}

func TestExitCodeOf_ExitError(t *testing.T) {
	t.Parallel()
	err := &execpkg.ExitError{Code: 7}
	require.Equal(t, 7, execpkg.ExitCodeOf(err))
}

func TestExitCodeOf_OtherError(t *testing.T) {
	t.Parallel()
	require.Equal(t, -1, execpkg.ExitCodeOf(fmt.Errorf("boom")))
}

// TestExitCodeOf_WrappedExitError verifies that ExitCodeOf unwraps through
// fmt.Errorf("%w", ...) to find a nested *ExitError. The direct type assertion
// previously used would return -1 for wrapped errors; errors.As must be used.
func TestExitCodeOf_WrappedExitError(t *testing.T) {
	t.Parallel()

	// Produce a real *ExitError from a failing child process.
	r := execpkg.Real()
	baseErr := r.Run("/bin/sh", []string{"-c", "exit 7"}, nil, nil, nil, nil)
	require.Error(t, baseErr)
	require.Equal(t, 7, execpkg.ExitCodeOf(baseErr), "sanity: unwrapped base err")

	// Wrap it one level and confirm ExitCodeOf still finds the code.
	wrapped := fmt.Errorf("wrap: %w", baseErr)
	require.Equal(t, 7, execpkg.ExitCodeOf(wrapped), "errors.As must unwrap through fmt.Errorf %%w")

	// Double-wrap — errors.As traverses the full chain.
	doubleWrapped := fmt.Errorf("outer: %w", wrapped)
	require.Equal(t, 7, execpkg.ExitCodeOf(doubleWrapped), "errors.As must traverse two wrap layers")
}

// ---------------------------------------------------------------------------
// ExitError
// ---------------------------------------------------------------------------

func TestExitError_Error(t *testing.T) {
	t.Parallel()
	inner := fmt.Errorf("inner")
	e := &execpkg.ExitError{Code: 3, Err: inner}
	require.Contains(t, e.Error(), "3")
	require.ErrorIs(t, e, inner)
}

func TestExitError_Unwrap_Nil(t *testing.T) {
	t.Parallel()
	e := &execpkg.ExitError{Code: 1, Err: nil}
	require.Nil(t, e.Unwrap())
}

// ---------------------------------------------------------------------------
// FakeRunner tests
// ---------------------------------------------------------------------------

// TestFakeRunner_RecordsCall verifies that a single Run invocation is captured
// in FakeRunner.Calls with the correct name, args, and env.
func TestFakeRunner_RecordsCall(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake()
	var out bytes.Buffer

	err := f.Run("ssh", []string{"-p", "22", "root@host"}, []string{"SSH_AUTH_SOCK=/tmp/sock"}, nil, &out, nil)
	require.NoError(t, err)
	require.Len(t, f.Calls, 1)

	c := f.Calls[0]
	require.Equal(t, "ssh", c.Name)
	require.Equal(t, []string{"-p", "22", "root@host"}, c.Args)
	require.Equal(t, []string{"SSH_AUTH_SOCK=/tmp/sock"}, c.Env)
	require.False(t, c.Interactive)
}

// TestFakeRunner_RecordsStdin verifies that stdin contents are captured.
func TestFakeRunner_RecordsStdin(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake()
	_ = f.Run("cat", nil, nil, strings.NewReader("hello"), nil, nil)

	require.Len(t, f.Calls, 1)
	require.Equal(t, []byte("hello"), f.Calls[0].StdinContents)
}

// TestFakeRunner_ProgrammedOutput verifies that configured Stdout/Stderr are
// written to the provided writers.
func TestFakeRunner_ProgrammedOutput(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake(execpkg.FakeResponse{
		Stdout: "node1\nnode2\n",
		Stderr: "warning\n",
	})
	var stdout, stderr bytes.Buffer

	err := f.Run("pve-nodes", nil, nil, nil, &stdout, &stderr)
	require.NoError(t, err)
	require.Equal(t, "node1\nnode2\n", stdout.String())
	require.Equal(t, "warning\n", stderr.String())
}

// TestFakeRunner_ProgrammedError verifies that a configured Err is returned
// as-is.
func TestFakeRunner_ProgrammedError(t *testing.T) {
	t.Parallel()

	sentinel := fmt.Errorf("connection refused")
	f := execpkg.Fake(execpkg.FakeResponse{Err: sentinel})

	err := f.Run("ssh", nil, nil, nil, nil, nil)
	require.ErrorIs(t, err, sentinel)
}

// TestFakeRunner_ExitCode verifies that a non-zero ExitCode synthesises an
// *ExitError with the right code.
func TestFakeRunner_ExitCode(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake(execpkg.FakeResponse{ExitCode: 5})

	err := f.Run("rsync", nil, nil, nil, nil, nil)
	require.Error(t, err)
	require.Equal(t, 5, execpkg.ExitCodeOf(err))
}

// TestFakeRunner_MultipleResponses verifies FIFO consumption of pre-configured
// responses and that a zero-value response is returned after exhaustion.
func TestFakeRunner_MultipleResponses(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake(
		execpkg.FakeResponse{Stdout: "first\n"},
		execpkg.FakeResponse{ExitCode: 2},
	)
	var out bytes.Buffer

	// First call — configured response.
	require.NoError(t, f.Run("cmd", nil, nil, nil, &out, nil))
	require.Equal(t, "first\n", out.String())

	// Second call — exit code.
	err := f.Run("cmd", nil, nil, nil, nil, nil)
	require.Equal(t, 2, execpkg.ExitCodeOf(err))

	// Third call — exhausted; success with no output.
	var out2 bytes.Buffer
	require.NoError(t, f.Run("cmd", nil, nil, nil, &out2, nil))
	require.Empty(t, out2.String())

	require.Len(t, f.Calls, 3)
}

// TestFakeRunner_RunInteractive verifies that RunInteractive is recorded with
// Interactive=true.
func TestFakeRunner_RunInteractive(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake(execpkg.FakeResponse{})
	err := f.RunInteractive("ssh", []string{"root@pve01"}, nil)
	require.NoError(t, err)
	require.Len(t, f.Calls, 1)
	require.True(t, f.Calls[0].Interactive)
	require.Equal(t, "ssh", f.Calls[0].Name)
	require.Equal(t, []string{"root@pve01"}, f.Calls[0].Args)
}

// TestFakeRunner_RunInteractive_Error verifies that a configured error is
// returned from RunInteractive.
func TestFakeRunner_RunInteractive_Error(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake(execpkg.FakeResponse{ExitCode: 255})
	err := f.RunInteractive("ssh", nil, nil)
	require.Equal(t, 255, execpkg.ExitCodeOf(err))
}

// TestFakeRunner_NilEnv verifies that a nil env slice does not panic and is
// recorded faithfully.
func TestFakeRunner_NilEnv(t *testing.T) {
	t.Parallel()

	f := execpkg.Fake()
	require.NoError(t, f.Run("true", []string{"a"}, nil, nil, nil, nil))
	require.Nil(t, f.Calls[0].Env)
}

// TestFakeRunner_Interface asserts that *FakeRunner satisfies the Runner
// interface at compile time.
func TestFakeRunner_Interface(t *testing.T) {
	t.Parallel()
	var _ execpkg.Runner = execpkg.Fake()
}

// TestRealRunner_Interface asserts that Real() returns a usable Runner.
// Real()'s signature already returns the interface type, so the value is
// checked for non-nil to verify the constructor hands back a working runner.
func TestRealRunner_Interface(t *testing.T) {
	t.Parallel()
	require.NotNil(t, execpkg.Real(), "Real must return a usable Runner")
}
