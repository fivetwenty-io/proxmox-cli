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
