package apiclient

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

func TestWaitOptionsFor_NonPositiveMeansUnbounded(t *testing.T) {
	for _, in := range []int64{0, -1} {
		opts := WaitOptionsFor(in)
		require.NotNil(t, opts, "a nil would let the SDK fall back to its 300 s default")
		require.Equal(t, UnboundedWaitSeconds, opts.TimeoutSeconds)
		require.Zero(t, opts.IntervalMillis, "polling cadence stays at the SDK default")
	}
}

func TestWaitOptionsFor_SetsOnlyTheTimeout(t *testing.T) {
	opts := WaitOptionsFor(90)
	require.NotNil(t, opts)
	require.Equal(t, 90, opts.TimeoutSeconds)
	require.Zero(t, opts.IntervalMillis, "polling cadence stays at the SDK default")
}

func TestWaitOptionsOrDefault_NilReadsTheProcessWidePolicy(t *testing.T) {
	t.Cleanup(func() { SetDefaultWaitTimeout(0) })

	require.Equal(t, UnboundedWaitSeconds, waitOptionsOrDefault(nil).TimeoutSeconds,
		"an operator who set no --wait-timeout waits until the task ends")

	SetDefaultWaitTimeout(45)
	require.Equal(t, 45, waitOptionsOrDefault(nil).TimeoutSeconds)
}

func TestWaitOptionsOrDefault_NonNilPassesThrough(t *testing.T) {
	t.Cleanup(func() { SetDefaultWaitTimeout(0) })
	SetDefaultWaitTimeout(45)

	own := &tasks.WaitOptions{TimeoutSeconds: 120, IntervalMillis: 500}
	require.Same(t, own, waitOptionsOrDefault(own),
		"a caller that carries its own policy keeps it")
}
