package exec

import (
	"bytes"
	"io"
	"sync"
)

// FakeResponse configures what FakeRunner returns for a single call in sequence.
type FakeResponse struct {
	// Stdout is written to the stdout writer passed to Run.
	Stdout string
	// Stderr is written to the stderr writer passed to Run.
	Stderr string
	// Err is returned as the call's error. If ExitCode > 0 and Err is nil, an
	// *ExitError is synthesised with the given code.
	Err error
	// ExitCode is used to synthesise an *ExitError when Err is nil and the
	// code is non-zero.
	ExitCode int
}

// Call records one invocation made against a FakeRunner.
type Call struct {
	// Name is the executable name passed to Run or RunInteractive.
	Name string
	// Args are the arguments passed.
	Args []string
	// Env are the extra environment entries passed by the caller.
	Env []string
	// StdinContents holds the bytes read from stdin (only for Run; empty for
	// RunInteractive which has no controllable stdin in tests).
	StdinContents []byte
	// Interactive is true when the call was made via RunInteractive.
	Interactive bool
}

// FakeRunner is a testable Runner that records every invocation and returns
// pre-configured responses in FIFO order. After all pre-configured responses
// are consumed it returns nil (success) for subsequent calls.
//
// Construct with Fake(responses...) and inspect Calls after the SUT runs.
type FakeRunner struct {
	mu        sync.Mutex
	responses []FakeResponse
	idx       int
	// Calls holds one entry per Run or RunInteractive invocation, in order.
	Calls []Call
}

// Fake returns a new *FakeRunner pre-loaded with the given responses. Responses
// are consumed in FIFO order; once exhausted every subsequent call succeeds
// with no output.
func Fake(responses ...FakeResponse) *FakeRunner {
	return &FakeRunner{
		responses: responses,
	}
}

// next returns the next FakeResponse in sequence. It is safe for concurrent
// use. If all responses are consumed, a zero-value (success, no output)
// FakeResponse is returned.
func (f *FakeRunner) next() FakeResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idx < len(f.responses) {
		r := f.responses[f.idx]
		f.idx++
		return r
	}
	return FakeResponse{}
}

// record appends c to f.Calls under the mutex.
func (f *FakeRunner) record(c Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, c)
}

// Run records the invocation, writes configured stdout/stderr, and returns the
// configured error (or a synthesised *ExitError when ExitCode > 0).
func (f *FakeRunner) Run(name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Read stdin fully so callers can inspect what was written.
	var stdinBuf []byte
	if stdin != nil {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, stdin); err == nil {
			stdinBuf = buf.Bytes()
		}
	}

	resp := f.next()

	f.record(Call{
		Name:          name,
		Args:          args,
		Env:           env,
		StdinContents: stdinBuf,
		Interactive:   false,
	})

	// Write configured output.
	if resp.Stdout != "" && stdout != nil {
		_, _ = io.WriteString(stdout, resp.Stdout)
	}
	if resp.Stderr != "" && stderr != nil {
		_, _ = io.WriteString(stderr, resp.Stderr)
	}

	return f.resolveErr(resp)
}

// RunInteractive records the invocation and returns the configured error.
// stdin/stdout/stderr are not wired in the fake (they would be the real
// process streams; tests should not need to interact with them).
func (f *FakeRunner) RunInteractive(name string, args []string, env []string) error {
	resp := f.next()

	f.record(Call{
		Name:        name,
		Args:        args,
		Env:         env,
		Interactive: true,
	})

	return f.resolveErr(resp)
}

// resolveErr converts a FakeResponse into the error value Run/RunInteractive
// should return. Priority: explicit Err > synthesised ExitError > nil.
func (f *FakeRunner) resolveErr(resp FakeResponse) error {
	if resp.Err != nil {
		return resp.Err
	}
	if resp.ExitCode != 0 {
		return &ExitError{
			Code: resp.ExitCode,
			Err:  nil,
		}
	}
	return nil
}
