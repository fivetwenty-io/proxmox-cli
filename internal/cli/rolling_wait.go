package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
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
	// A nil opts is what a caller with no policy of its own hands the wait
	// funnels, and they read the operator's --wait-timeout for it. Resolving
	// it the same way here keeps the number in the message equal to the bound
	// that actually expired, rather than printing "0s" for a wait that was
	// unbounded.
	if opts == nil {
		opts = apiclient.WaitOptionsFor(apiclient.DefaultWaitTimeout())
	}

	return fmt.Errorf("stopped waiting after %s; the task is still running on the server: "+
		"follow it with 'pmx pve task wait %s': %w", waitBoundText(opts.TimeoutSeconds), upid, err)
}

// waitBoundText names the bound in the deadline message. The unbounded wait is
// spelled as a number of seconds only because the SDK needs one; the operator
// asked for no bound, so they read "7 days", not "604800s".
func waitBoundText(secs int) string {
	if secs == apiclient.UnboundedWaitSeconds {
		return "7 days"
	}
	return fmt.Sprintf("%ds", secs)
}

// RenderUPID renders the bare task handle for --async: the same shape every
// write verb prints when it returns before waiting, shared across node and
// cluster commands so their async and dry-run renderers cannot drift apart
// on it.
func RenderUPID(cmd *cobra.Command, deps *Deps, upid string) error {
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{
			Single:  map[string]string{"upid": upid},
			Raw:     map[string]string{"upid": upid},
			Message: upid,
		}, deps.Format)
}

// RenderDryRunLog waits for a dry-run worker and prints its log, which is
// where the API writes the plan it would have executed and, when it refuses,
// the reason. The log is fetched whether or not the worker succeeded, so a
// failed health gate still shows its message, and the wait error is returned
// afterwards. --async prints the UPID instead, and the log is one
// `pmx pve node task log` / `pmx pve task log` away.
//
// prefix names the caller's operation, for example "ceph rolling restart dry
// run" or fmt.Sprintf("ceph dry run on node %q", node), and is used to wrap
// both the wait error and a UPID-parse failure, so the two node and cluster
// callers keep their own message shape while sharing this control flow.
func RenderDryRunLog(cmd *cobra.Command, deps *Deps, upid string, opts *tasks.WaitOptions, prefix string) error {
	if deps.Async {
		return RenderUPID(cmd, deps, upid)
	}
	waitErr := apiclient.WaitTask(cmd.Context(), deps.API, upid, opts)
	if waitErr != nil {
		waitErr = RollingWaitError(fmt.Errorf("%s: %w", prefix, waitErr), opts, upid)
	}
	parsed, err := tasks.ParseUPID(upid)
	if err != nil {
		return errors.Join(fmt.Errorf("%s: %w", prefix, err), waitErr)
	}
	res, err := TaskLogResult(cmd.Context(), deps, parsed.Node, upid, nil)
	if err != nil {
		return errors.Join(err, waitErr)
	}
	if err := deps.Out.Render(cmd.OutOrStdout(), res, deps.Format); err != nil {
		return errors.Join(err, waitErr)
	}
	return waitErr
}
