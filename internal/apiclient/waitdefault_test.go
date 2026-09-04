package apiclient_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// pveRunningUPID is a well-formed PVE UPID (8 fields) whose node is "pve1",
// used for a task the fake server never lets finish.
const pveRunningUPID = "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:"

// defaultBoundSeconds is the process-wide bound the three funnel subtests run
// under. One second is the smallest bound the SDK can express, since it turns
// TimeoutSeconds into a context deadline measured in whole seconds.
const defaultBoundSeconds = 1

// funnelWatchdog is how long a subtest waits for its funnel to give up before
// declaring that the funnel ignored the bound. It is far above the one-second
// bound and far below the SDK's own 300 s default, so a funnel that lost its
// waitOptionsOrDefault call fails here in seconds instead of hanging until the
// package timeout.
const funnelWatchdog = 20 * time.Second

// runningTaskStatus is the task-status body the fakes answer with, a task that
// is always still running. The extra fields beyond status are what the
// generated response types require.
func runningTaskStatus(upid, node string) map[string]any {
	return map[string]any{
		"status":    "running",
		"upid":      upid,
		"node":      node,
		"pid":       1234,
		"pstart":    1,
		"starttime": 1,
		"type":      "qmstart",
		"user":      "root@pam",
	}
}

// alwaysRunning registers a task-status route that answers "running" forever,
// after a short delay. The delay matters: the SDK checks status once before it
// sleeps, so spending a few milliseconds there puts the poll interval's timer
// safely behind the one-second context deadline, and the wait then always ends
// through the deadline branch rather than racing it.
func alwaysRunning(handle func(string, http.HandlerFunc), route, upid, node string) {
	handle(route, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		testhelper.WriteData(w, runningTaskStatus(upid, node))
	})
}

// requireDeadlineExceeded runs wait on its own goroutine and requires that it
// ends with the operator's bound rather than running on. The goroutine never
// touches t, because it outlives the test whenever the guard fails.
func requireDeadlineExceeded(t *testing.T, wait func() error) {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- wait() }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded,
			"the funnel must stop at the operator's bound")
	case <-time.After(funnelWatchdog):
		t.Fatalf("the funnel was still waiting after %s, so it never applied the %ds bound",
			funnelWatchdog, defaultBoundSeconds)
	}
}

// TestWaitFunnels_NilOptionsHonourTheProcessWideBound is the far end of the
// --wait-timeout wire. Every funnel substitutes the operator's policy for a
// nil opts, and most callers pass nil, so dropping that substitution from any
// one of the three would silently hand those callers back to the SDK's 300 s
// default.
//
// Nothing but elapsed time tells the two apart, because a nil opts and the
// operator's opts differ only in the deadline they produce. The test therefore
// lets a real one-second bound expire against a task the fake server never
// finishes, and runs the three funnels as parallel subtests so the whole guard
// costs one second rather than three.
func TestWaitFunnels_NilOptionsHonourTheProcessWideBound(t *testing.T) {
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })
	apiclient.SetDefaultWaitTimeout(defaultBoundSeconds)

	t.Run("pve", func(t *testing.T) {
		t.Parallel()

		f := testhelper.NewFakePVE(t)
		alwaysRunning(f.HandleFunc, "GET /api2/json/nodes/pve1/tasks/"+pveRunningUPID+"/status",
			pveRunningUPID, "pve1")

		ac, err := apiclient.NewAPIClient(f.Options)
		require.NoError(t, err)

		requireDeadlineExceeded(t, func() error {
			return apiclient.WaitTask(context.Background(), ac, pveRunningUPID, nil)
		})
	})

	t.Run("pbs", func(t *testing.T) {
		t.Parallel()

		f := testhelper.NewFakePBS(t)
		alwaysRunning(f.HandleFunc, "GET /api2/json/nodes/pbs1/tasks/"+pbsTestUPID+"/status",
			pbsTestUPID, "pbs1")

		pc := newPBSClientForFake(t, f)

		requireDeadlineExceeded(t, func() error {
			return apiclient.WaitPBSTask(context.Background(), pc, pbsTestUPID, nil)
		})
	})

	t.Run("pdm", func(t *testing.T) {
		t.Parallel()

		f := testhelper.NewFakePDM(t)
		alwaysRunning(f.HandleFunc, "GET /api2/json/nodes/pdm-host/tasks/"+pdmTestUPID+"/status",
			pdmTestUPID, "pdm-host")

		pc := newPDMClientForFake(t, f)

		requireDeadlineExceeded(t, func() error {
			return apiclient.WaitPDMTask(context.Background(), pc, pdmTestUPID, nil)
		})
	})
}
