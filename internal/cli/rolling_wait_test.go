package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
)

func TestRollingWaitError_DeadlineNamesTheTaskAndTheBound(t *testing.T) {
	upid := "UPID:pve1:00001234:00000ABC:66D0F2A0:cephrestartbulk:osd:root@pam:"
	err := fmt.Errorf("wait task %s: task polling canceled: %w", upid, context.DeadlineExceeded)

	got := RollingWaitError(err, &tasks.WaitOptions{TimeoutSeconds: 90}, upid)
	require.Error(t, got)
	require.Contains(t, got.Error(), "stopped waiting after 90s")
	require.Contains(t, got.Error(), "still running")
	require.Contains(t, got.Error(), "pmx pve task wait "+upid)
	require.ErrorIs(t, got, context.DeadlineExceeded, "the cause must still be wrapped, not discarded")
	require.Contains(t, got.Error(), "task polling canceled", "the wrapped cause, including any node name, survives")
}

// TestRollingWaitError_WordingIsNeutralAboutWhatTheTaskDoes uses a plan-shaped
// UPID rather than the shared restart-bulk fixture, whose task type
// ("cephrestartbulk") itself contains the substring "restart" and would
// defeat this assertion regardless of the message wording. RollingWaitError
// is shared by both the rolling-restart wait and the --dry-run wait, and a
// dry run never restarts anything, so its wording must not claim otherwise.
func TestRollingWaitError_WordingIsNeutralAboutWhatTheTaskDoes(t *testing.T) {
	upid := "UPID:pve1:00001234:00000ABC:66D0F2A0:cephosdplan:osd:root@pam:"
	err := fmt.Errorf("wait task %s: task polling canceled: %w", upid, context.DeadlineExceeded)

	got := RollingWaitError(err, &tasks.WaitOptions{TimeoutSeconds: 90}, upid)
	require.Error(t, got)
	require.NotContains(t, got.Error(), "restart",
		"a --dry-run worker only logs a plan and never restarts anything")
}

// TestRollingWaitError_UnboundedWaitSaysSevenDays covers the operator's
// choice of no bound (--wait-timeout 0, WaitOptionsFor's default): the
// deadline message must read as a human duration, not the raw seconds the
// SDK needs internally.
func TestRollingWaitError_UnboundedWaitSaysSevenDays(t *testing.T) {
	upid := "UPID:pve1:00001234:00000ABC:66D0F2A0:cephrestartbulk:osd:root@pam:"
	err := fmt.Errorf("wait task %s: task polling canceled: %w", upid, context.DeadlineExceeded)

	got := RollingWaitError(err, WaitOptionsFor(0), upid)
	require.Error(t, got)
	require.Contains(t, got.Error(), "stopped waiting after 7 days")
	require.NotContains(t, got.Error(), "604800")
}

// TestRollingWaitError_NilOptsReadsTheOperatorsBound covers the caller that
// carries no policy of its own, which is what RenderDryRunLog forwards when
// its own caller passed nil. The wait funnels resolve that nil against the
// operator's --wait-timeout, so the message has to resolve it the same way
// rather than reporting a bound of zero seconds.
func TestRollingWaitError_NilOptsReadsTheOperatorsBound(t *testing.T) {
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })

	upid := "UPID:pve1:00001234:00000ABC:66D0F2A0:cephrestartbulk:osd:root@pam:"
	err := fmt.Errorf("wait task %s: task polling canceled: %w", upid, context.DeadlineExceeded)

	apiclient.SetDefaultWaitTimeout(0)
	got := RollingWaitError(err, nil, upid)
	require.Error(t, got)
	require.Contains(t, got.Error(), "stopped waiting after 7 days")
	require.NotContains(t, got.Error(), "after 0s")

	apiclient.SetDefaultWaitTimeout(90)
	got = RollingWaitError(err, nil, upid)
	require.Contains(t, got.Error(), "stopped waiting after 90s")
}

func TestRollingWaitError_OtherErrorsPassThrough(t *testing.T) {
	err := errors.New("task failed: TASK ERROR")
	got := RollingWaitError(err, &tasks.WaitOptions{TimeoutSeconds: 90}, "UPID:x")
	require.Same(t, err, got)
}

func TestRollingWaitError_CancelIsNotADeadline(t *testing.T) {
	err := fmt.Errorf("wait: %w", context.Canceled)
	got := RollingWaitError(err, nil, "UPID:x")
	require.Same(t, err, got)
}
