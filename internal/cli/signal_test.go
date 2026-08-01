package cli

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSignalContext_CancelsOnSignal covers the reason the root context is no
// longer context.Background(): every ctx.Done() path in the tree — the
// task-wait poll, the lab SSH wait, the SDK's retry backoff — could only ever
// observe a context that was never cancellable, so ^C killed pmx outright and
// Execute never reached its exit audit record.
//
// Sending the signal to this process is safe precisely because
// signalContext registers a handler for it; if it did not, the test binary
// would die here rather than fail.
//
// Not parallel: it signals the whole test process.
func TestSignalContext_CancelsOnSignal(t *testing.T) {
	ctx, stop := signalContext()
	defer stop()

	require.NoError(t, ctx.Err(), "context must start live")

	p, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, p.Signal(syscall.SIGTERM))

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context was not cancelled by SIGTERM")
	}
}

// TestSignalContext_StopCancels covers the deferred-stop path: Execute defers
// stop, and a command that returned normally must not leave the context live
// or the notify registration installed.
func TestSignalContext_StopCancels(t *testing.T) {
	ctx, stop := signalContext()
	stop()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the context")
	}
}
