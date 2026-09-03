package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

// RollingWaitError explains why waiting on a rolling restart failed. When the
// client-side bound expired the server is still rolling, so the operator must
// know not to start a second run and how to pick the wait back up; every other
// failure, including the operator's own interrupt, is returned as it came.
func RollingWaitError(err error, opts *tasks.WaitOptions, upid string) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	secs := 0
	if opts != nil {
		secs = opts.TimeoutSeconds
	}
	return fmt.Errorf("stopped waiting after %ds; the rolling restart is still running on the server: "+
		"follow it with 'pmx pve task wait %s'", secs, upid)
}
