package apiclient_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// pveTestUPID is a well-formed PVE UPID (8 fields) whose node is "pve1".
const pveTestUPID = "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:"

// operatorBound is a --wait-timeout no default could be mistaken for: it is
// neither the SDK's 300 s nor the one-week unbounded sentinel.
const operatorBound = 1234

// recordingTasks stands in for the SDK's task service and keeps the wait
// options a funnel hands it. Both wait methods answer with a finished task, so
// the funnel returns at once and the test reads the bound off the recorder
// instead of waiting for it to expire. The embedded nil Service covers the
// methods the funnels never call.
type recordingTasks struct {
	tasks.Service
	got *tasks.WaitOptions
}

func (r *recordingTasks) Wait(_ context.Context, _, _ string, opts *tasks.WaitOptions) (*tasks.Status, error) {
	r.got = opts
	return &tasks.Status{Status: "stopped", ExitStatus: "OK"}, nil
}

func (r *recordingTasks) WaitForUPID(_ context.Context, _ string, opts *tasks.WaitOptions) (*tasks.Status, error) {
	r.got = opts
	return &tasks.Status{Status: "stopped", ExitStatus: "OK"}, nil
}

// funnel is one of the three wait funnels, closed over a client whose task
// service is the recorder, so the subtests differ only in which funnel they
// exercise.
type funnel struct {
	name string
	wait func(t *testing.T, rec *recordingTasks) error
}

func waitFunnels() []funnel {
	return []funnel{
		{name: "pve", wait: func(t *testing.T, rec *recordingTasks) error {
			ac, err := apiclient.NewAPIClient(testhelper.NewFakePVE(t).Options)
			require.NoError(t, err)
			ac.Tasks = rec
			return apiclient.WaitTask(context.Background(), ac, pveTestUPID, nil)
		}},
		{name: "pbs", wait: func(t *testing.T, rec *recordingTasks) error {
			pc := newPBSClientForFake(t, testhelper.NewFakePBS(t))
			pc.Tasks = rec
			return apiclient.WaitPBSTask(context.Background(), pc, pbsTestUPID, nil)
		}},
		{name: "pdm", wait: func(t *testing.T, rec *recordingTasks) error {
			pc := newPDMClientForFake(t, testhelper.NewFakePDM(t))
			pc.Tasks = rec
			return apiclient.WaitPDMTask(context.Background(), pc, pdmTestUPID, nil)
		}},
	}
}

// TestWaitFunnels_NilOptionsHonourTheProcessWideBound is the far end of the
// --wait-timeout wire. Every funnel substitutes the operator's policy for a
// nil opts, and most callers pass nil, so dropping that substitution from any
// one of the three would silently hand those callers back to the SDK's 300 s
// default. The recorder sees exactly what the SDK would have been given, so
// the test checks the number itself rather than waiting for a bound to
// expire.
func TestWaitFunnels_NilOptionsHonourTheProcessWideBound(t *testing.T) {
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })
	apiclient.SetDefaultWaitTimeout(operatorBound)

	for _, fn := range waitFunnels() {
		t.Run(fn.name, func(t *testing.T) {
			rec := &recordingTasks{}
			require.NoError(t, fn.wait(t, rec))
			require.NotNil(t, rec.got, "the funnel handed the SDK a nil policy, which means its 300 s default")
			require.Equal(t, operatorBound, rec.got.TimeoutSeconds)
		})
	}
}

// TestWaitFunnels_NilOptionsAndNoBoundWaitForAWeek pins the other half of the
// default: with no --wait-timeout at all, a nil opts must reach the SDK as the
// one-week sentinel rather than as nil or zero, either of which the SDK would
// read as its own 300 s default.
func TestWaitFunnels_NilOptionsAndNoBoundWaitForAWeek(t *testing.T) {
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })
	apiclient.SetDefaultWaitTimeout(0)

	for _, fn := range waitFunnels() {
		t.Run(fn.name, func(t *testing.T) {
			rec := &recordingTasks{}
			require.NoError(t, fn.wait(t, rec))
			require.NotNil(t, rec.got)
			require.Equal(t, apiclient.UnboundedWaitSeconds, rec.got.TimeoutSeconds)
		})
	}
}
