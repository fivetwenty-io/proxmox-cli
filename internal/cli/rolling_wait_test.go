package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
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
