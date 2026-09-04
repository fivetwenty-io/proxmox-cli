package testhelper

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestExpiringContext_ErrIsNilThenDeadlineExceeded checks the bare context on
// its own: no error before expire is called, context.DeadlineExceeded after.
func TestExpiringContext_ErrIsNilThenDeadlineExceeded(t *testing.T) {
	ctx, expire := ExpiringContext(context.Background())
	require.NoError(t, ctx.Err())

	expire()
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}

// TestExpiringContext_ExpireTwiceIsSafe checks that calling the returned
// function more than once neither panics nor double-closes the channel.
func TestExpiringContext_ExpireTwiceIsSafe(t *testing.T) {
	_, expire := ExpiringContext(context.Background())

	require.NotPanics(t, func() {
		expire()
		expire()
	})
}

// TestExpiringContext_ChildTimeoutReportsDeadlineExceededAfterExpire covers
// the actual reason this helper exists: a context.WithTimeout built on top
// of the expiring context must report context.DeadlineExceeded once expire
// is called, well before its own real timeout would fire, since that is the
// same relationship the SDK's task poller has with the context it is given.
func TestExpiringContext_ChildTimeoutReportsDeadlineExceededAfterExpire(t *testing.T) {
	ctx, expire := ExpiringContext(context.Background())
	child, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()

	require.NoError(t, child.Err())

	expire()

	require.Eventually(t, func() bool {
		return child.Err() != nil
	}, 100*time.Millisecond, time.Millisecond)
	require.ErrorIs(t, child.Err(), context.DeadlineExceeded)
}
