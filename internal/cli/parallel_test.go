package cli_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// TestForEachIndex_CallsEveryIndexOnce is the basic contract: no index is
// skipped and none is visited twice, whatever the interleaving.
func TestForEachIndex_CallsEveryIndexOnce(t *testing.T) {
	const n = 50

	counts := make([]atomic.Int64, n)
	cli.ForEachIndex(context.Background(), n, 8, func(_ context.Context, i int) {
		counts[i].Add(1)
	})

	for i := range counts {
		require.Equal(t, int64(1), counts[i].Load(), "index %d", i)
	}
}

// TestForEachIndex_RespectsTheLimit pins the bound. Unbounded fan-out would
// turn a cluster-wide audit into hundreds of simultaneous connections to a
// single pvedaemon.
func TestForEachIndex_RespectsTheLimit(t *testing.T) {
	const limit = 4

	var inFlight, peak atomic.Int64
	cli.ForEachIndex(context.Background(), 40, limit, func(_ context.Context, _ int) {
		n := inFlight.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
	})

	require.LessOrEqual(t, peak.Load(), int64(limit))
	require.Greater(t, peak.Load(), int64(1), "the work must actually overlap")
}

// TestForEachIndex_DegenerateInputs covers the edges a caller can reach with a
// filtered-to-empty target list or a mis-set limit.
func TestForEachIndex_DegenerateInputs(t *testing.T) {
	t.Run("no work", func(t *testing.T) {
		called := false
		cli.ForEachIndex(context.Background(), 0, 8, func(context.Context, int) { called = true })
		require.False(t, called)
	})

	t.Run("negative n", func(t *testing.T) {
		called := false
		cli.ForEachIndex(context.Background(), -3, 8, func(context.Context, int) { called = true })
		require.False(t, called)
	})

	t.Run("limit below one runs serially rather than deadlocking", func(t *testing.T) {
		var inFlight, peak atomic.Int64
		cli.ForEachIndex(context.Background(), 5, 0, func(context.Context, int) {
			n := inFlight.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			inFlight.Add(-1)
		})
		require.Equal(t, int64(1), peak.Load())
	})
}

// TestForEachIndex_PassesTheContextThrough asserts fn can see cancellation,
// which is how a caller stops issuing API calls after the first ^C.
func TestForEachIndex_PassesTheContextThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var cancelled atomic.Int64
	cli.ForEachIndex(ctx, 10, 4, func(ctx context.Context, _ int) {
		if ctx.Err() != nil {
			cancelled.Add(1)
		}
	})

	require.Equal(t, int64(10), cancelled.Load())
}
