package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

// RollingWaitError explains why waiting on a rolling restart or its dry run
// failed. When the client-side bound expired the server-side task is still
// running, so the operator must know not to start a second one and how to
// pick the wait back up; the wording stays neutral about what the task is
// doing, because a --dry-run worker only logs a plan and never restarts
// anything. Every other failure, including the operator's own interrupt, is
// returned as it came.
func RollingWaitError(err error, opts *tasks.WaitOptions, upid string) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	secs := 0
	if opts != nil {
		secs = opts.TimeoutSeconds
	}
	return fmt.Errorf("stopped waiting after %ds; the task is still running on the server: "+
		"follow it with 'pmx pve task wait %s': %w", secs, upid, err)
}
