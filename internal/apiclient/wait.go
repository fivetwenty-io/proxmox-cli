package apiclient

import (
	"sync/atomic"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

// UnboundedWaitSeconds is the bound used when the operator asks for none. The
// SDK ignores a non-positive TimeoutSeconds and falls back to its 300 s
// default, so "wait until the task ends" has to be spelled as a number that
// no realistic worker outlives; a week is that number.
const UnboundedWaitSeconds = 7 * 24 * 3600

// defaultWaitTimeout is the process-wide --wait-timeout, read by the wait
// funnels when a caller passes nil options. Zero means unbounded.
//
// It is process-wide state set once from the root command's flag resolution,
// for the same reason warningsAreErrors is: the alternative is threading a
// policy value through every task-producing call site, and none of them has
// any interest in it.
var defaultWaitTimeout atomic.Int64

// SetDefaultWaitTimeout records the operator's --wait-timeout for every wait
// that does not carry its own policy. It is called once from the root command,
// before any task runs.
func SetDefaultWaitTimeout(seconds int64) { defaultWaitTimeout.Store(seconds) }

// WaitOptionsFor returns a task-wait policy bounded by timeoutSeconds while
// leaving the polling cadence at the SDK default. A non-positive value means
// "wait as long as the task takes" and yields UnboundedWaitSeconds, because a
// nil policy or a zero TimeoutSeconds would make the SDK apply its 300 s
// default, which a Ceph rolling restart outlives by hours. The bound belongs
// to the operator (--wait-timeout), not the SDK.
func WaitOptionsFor(timeoutSeconds int64) *tasks.WaitOptions {
	if timeoutSeconds <= 0 {
		return &tasks.WaitOptions{TimeoutSeconds: UnboundedWaitSeconds}
	}
	return &tasks.WaitOptions{TimeoutSeconds: int(timeoutSeconds)}
}

// waitOptionsOrDefault substitutes the process-wide policy for a nil opts, so
// that a caller with no opinion of its own still gets the operator's bound
// rather than the SDK's 300 s default.
func waitOptionsOrDefault(opts *tasks.WaitOptions) *tasks.WaitOptions {
	if opts != nil {
		return opts
	}
	return WaitOptionsFor(defaultWaitTimeout.Load())
}
