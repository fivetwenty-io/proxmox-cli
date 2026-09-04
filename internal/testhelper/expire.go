package testhelper

import (
	"context"
	"sync"
)

// expiringCtx is a context.Context whose Done channel closes only when its
// owner asks it to, not on any timer. Everything else, including Deadline
// and Value, comes from the embedded parent.
type expiringCtx struct {
	context.Context
	done chan struct{}
	once sync.Once
}

// Done returns the channel that closes when the context is expired.
func (c *expiringCtx) Done() <-chan struct{} { return c.done }

// Err reports context.DeadlineExceeded once the context has been expired,
// and otherwise defers to the parent, matching the contract callers expect
// from a context that stands in for one built with context.WithTimeout.
func (c *expiringCtx) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return c.Context.Err()
	}
}

// ExpiringContext returns a context that stays open until the returned
// function is called, and reports context.DeadlineExceeded from then on.
// It exists so a test can end an SDK task wait deterministically, without
// sleeping through the wait's own bound: a real *tasks.WaitOptions timeout
// or the plain-second bound the CLI's --wait-timeout builds both wrap the
// parent context they are given with context.WithTimeout, and a child built
// that way reports its parent's Err() the moment the parent is done, so
// calling the returned function ends the poll loop at its next select
// instead of after the real deadline elapses.
//
// A typical caller registers the task-status HTTP handler to call the
// returned function right after writing a "running" response, then runs the
// command under test with this context: the wait sees the task is still
// running, moves into its poll interval's select, and returns immediately
// once the context reports it is done, rather than waiting out the
// wait-timeout in real time.
func ExpiringContext(parent context.Context) (context.Context, func()) {
	c := &expiringCtx{Context: parent, done: make(chan struct{})}

	return c, func() { c.once.Do(func() { close(c.done) }) }
}
