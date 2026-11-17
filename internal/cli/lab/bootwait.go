package lab

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
)

// A freshly started lab VM answers nothing for a while. The QEMU guest agent
// comes up after the guest OS does, and sshd (and the authorized key it needs)
// after that, so the first probes after `qm start` fail with a connection-level
// error that says only "not yet". Every wait here retries those errors until a
// bounded deadline and stops at once on anything else.

const (
	// labBootWaitDefault bounds a boot wait when the operator gave no
	// --wait-timeout. The global flag defaults to 0, meaning "wait until the
	// task ends", which suits a server-side task but would let a node that
	// never boots hang the command forever, so a boot wait substitutes its own
	// finite default instead.
	labBootWaitDefault = 5 * time.Minute

	// labBootPollInterval is the pause between two probes of a booting node.
	labBootPollInterval = 5 * time.Second
)

// labSSHPollSleep is a seam for testing: it pauses between probes, or is
// overridden in tests to advance a fake clock without sleeping.
var labSSHPollSleep = time.Sleep

// labWaitNow is the clock the boot waits read their deadline from. Tests
// replace it, together with labSSHPollSleep, so a wait runs at full speed.
var labWaitNow = time.Now

// labBootWaitBound returns how long a boot wait may last: the operator's
// --wait-timeout when it is positive, labBootWaitDefault otherwise.
func labBootWaitBound(deps *cli.Deps) time.Duration {
	if deps != nil && deps.WaitTimeout > 0 {
		return time.Duration(deps.WaitTimeout) * time.Second
	}
	return labBootWaitDefault
}

// labSSHDeniedPersistence is how long the ssh wait keeps retrying a refused
// key before it treats the refusal as final. A booting node starts sshd before
// pmxcfs mounts /etc/pve/priv, so for a short while its authorized keys are
// missing and every key is refused.
const labSSHDeniedPersistence = 30 * time.Second

// bootBudgetKey is the context key under which a run's shared boot budget
// travels.
type bootBudgetKey struct{}

// bootBudget is one deadline shared by every boot wait of a run: each VM's
// agent wait and the ssh wait that follows. Without it each wait would take a
// full bound of its own, and a five-VM lab whose guests never answer would
// wait five bounds end to end. The clock starts when the first wait does, not
// when the budget is created, so the plan building and the creation steps that
// come first do not spend it.
type bootBudget struct {
	bound time.Duration

	mu       sync.Mutex
	started  bool
	deadline time.Time
}

// deadlineAt returns the budget's deadline, fixing it on the first call.
func (b *bootBudget) deadlineAt() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.started {
		b.started = true
		b.deadline = labWaitNow().Add(b.bound)
	}

	return b.deadline
}

// withBootBudget returns ctx carrying one boot budget of the given bound, so
// every labWaitUntil run under it shares a single deadline.
func withBootBudget(ctx context.Context, bound time.Duration) context.Context {
	return context.WithValue(ctx, bootBudgetKey{}, &bootBudget{bound: bound})
}

// labWaitTimeoutError reports that a wait ran out its bound with the last
// probe still failing at the connection level.
type labWaitTimeoutError struct {
	// Bound is the deadline the wait was given.
	Bound time.Duration
	// Last is the failure of the final probe.
	Last error
}

// Error describes the expired wait together with the last probe failure.
func (e *labWaitTimeoutError) Error() string {
	return fmt.Sprintf("no answer within %s (last attempt: %v)", e.Bound, e.Last)
}

// labWaitUntil runs probe until it reports done, ctx ends, or the deadline
// passes. The deadline is bound from now, unless ctx carries a boot budget
// (withBootBudget), in which case it is the budget's deadline, shared with
// every other wait under that ctx; a wait that starts after the budget is
// spent still probes once. The timeout error reports the budget's own bound.
// A probe returns done=false for a connection-level failure that is worth
// retrying, and done=true to stop, with a non-nil error when the stop is a
// failure. It never sleeps past the deadline: when another interval would
// overshoot, the wait ends at once with a *labWaitTimeoutError.
func labWaitUntil(ctx context.Context, bound time.Duration, probe func() (done bool, err error)) error {
	deadline := labWaitNow().Add(bound)
	if budget, ok := ctx.Value(bootBudgetKey{}).(*bootBudget); ok {
		deadline, bound = budget.deadlineAt(), budget.bound
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := probe()
		if done {
			return err
		}
		if labWaitNow().Add(labBootPollInterval).After(deadline) {
			return &labWaitTimeoutError{Bound: bound, Last: err}
		}
		labSSHPollSleep(labBootPollInterval)
	}
}

// labBootTimeoutExit marks err as an infrastructure failure when it is a
// wait that expired, so the process exits with exitcode.Infra rather than
// Generic. Any other error is returned unchanged.
func labBootTimeoutExit(err error) error {
	if _, ok := errors.AsType[*labWaitTimeoutError](err); ok {
		return &exitcode.InfraError{Err: err}
	}
	return err
}

// createVMRunning reports whether the VM is currently running, from its live
// status. Any other state, including paused and stopped, reads as not running.
func createVMRunning(ctx context.Context, ac *apiclient.APIClient, node, vmid string) (bool, error) {
	current, err := ac.Nodes.ListQemuStatusCurrent(ctx, node, vmid)
	if err != nil {
		return false, err
	}
	return current != nil && current.Status == "running", nil
}

// createWaitForAgent pings the VM's QEMU guest agent until it answers or bound
// elapses. The agent starts after the guest OS, so an early ping fails with
// "QEMU guest agent is not running" even on a healthy boot; that and every
// other transient error is retried. An error that retrying cannot fix, an
// authentication, permission, or not-found failure, stops the wait at once.
// A cancelled ctx is returned as is.
func createWaitForAgent(ctx context.Context, ac *apiclient.APIClient, bound time.Duration, node, vmid string) error {
	return labWaitUntil(ctx, bound, func() (bool, error) {
		_, err := ac.Nodes.CreateQemuAgentPing(ctx, node, vmid)
		if err == nil {
			return true, nil
		}
		switch exitcode.FromError(err) {
		case exitcode.Auth, exitcode.TFARequired, exitcode.NotFound, exitcode.BadArgs:
			return true, err
		default:
			return false, err
		}
	})
}
