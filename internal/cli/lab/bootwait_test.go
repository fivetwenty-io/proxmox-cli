package lab

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
)

// neverReady is a probe that never succeeds and counts how often it ran.
func neverReady(probes *int) func() (bool, error) {
	return func() (bool, error) {
		*probes++
		return false, errors.New("not yet")
	}
}

// TestBootBudget_SharedAcrossWaits covers the single deadline of a create
// --start run. Waits under one budget share it, so the second wait gets
// whatever the first left, and a wait that begins after the budget is spent
// still probes once. Without a budget every wait takes a full bound of its own.
func TestBootBudget_SharedAcrossWaits(t *testing.T) {
	const bound = 30 * time.Second

	t.Run("two waits split one bound", func(t *testing.T) {
		ctx := withBootBudget(context.Background(), bound)

		// Time spent before the first wait, such as building the VMs, is not
		// charged to the budget: its clock starts with the first wait.
		labTestClock = labTestClock.Add(time.Hour)
		began := labTestClock

		var first, second int
		err := labWaitUntil(ctx, bound, neverReady(&first))
		var timeout *labWaitTimeoutError
		require.ErrorAs(t, err, &timeout)
		assert.Equal(t, bound, timeout.Bound)
		assert.Equal(t, 7, first, "probes at 0, 5, ... 30 seconds")

		err = labWaitUntil(ctx, bound, neverReady(&second))
		require.ErrorAs(t, err, &timeout)
		assert.Equal(t, 1, second, "a spent budget still allows one probe")
		assert.Equal(t, bound, labTestClock.Sub(began), "both waits together took one bound")
	})

	t.Run("a wait that succeeds leaves the rest for the next", func(t *testing.T) {
		ctx := withBootBudget(context.Background(), bound)
		began := labTestClock

		ready := 0
		require.NoError(t, labWaitUntil(ctx, bound, func() (bool, error) {
			ready++
			return ready == 3, nil // two sleeps of 5s
		}))
		assert.Equal(t, 10*time.Second, labTestClock.Sub(began))

		var probes int
		require.Error(t, labWaitUntil(ctx, bound, neverReady(&probes)))
		assert.Equal(t, 5, probes, "20 seconds are left, so probes at 0, 5, 10, 15, and 20")
	})

	t.Run("without a budget each wait takes a full bound", func(t *testing.T) {
		began := labTestClock

		var first, second int
		require.Error(t, labWaitUntil(context.Background(), bound, neverReady(&first)))
		require.Error(t, labWaitUntil(context.Background(), bound, neverReady(&second)))
		assert.Equal(t, 7, first)
		assert.Equal(t, 7, second)
		assert.Equal(t, 2*bound, labTestClock.Sub(began))
	})
}

// TestLabWaitForSSH_SharesTheBootBudget covers the ssh wait that follows the
// agent waits: when they have spent the run's budget it probes once and fails
// as an infrastructure error, instead of starting a fresh bound.
func TestLabWaitForSSH_SharesTheBootBudget(t *testing.T) {
	ctx := withBootBudget(context.Background(), 20*time.Second)

	var agentProbes int
	require.Error(t, labWaitUntil(ctx, 20*time.Second, neverReady(&agentProbes)))
	assert.Equal(t, 5, agentProbes)

	fake := exec.Fake(sshConnectFailure, sshConnectFailure, sshConnectFailure)
	err := labWaitForSSH(ctx, waitDepsWithFake(fake), "10.10.1.10")
	require.Error(t, err)
	assert.Equal(t, exitcode.Infra, exitcode.FromError(err))
	assert.ErrorContains(t, err, "timed out waiting for ssh on 10.10.1.10")
	assert.Len(t, fake.Calls, 1, "the budget was spent by the agent wait, so the ssh wait probes once")
}
