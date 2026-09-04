package cli

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// RenderTaskWait is the one async-or-wait-then-message block every
// task-producing verb shares: under --async it renders the UPID and returns
// immediately, and otherwise it waits for the task with opts and, once it
// finishes, renders doneMsg. A failed wait is returned exactly as
// apiclient.WaitTask reports it, with nothing rendered, so callers are free
// to wrap it with their own operation prefix; do that exactly once, since a
// caller that already carries its own prefix (like the bulk and Ceph task
// renderers) would otherwise stack a second one on top of a caller further
// up that wraps the same error again.
func RenderTaskWait(cmd *cobra.Command, deps *Deps, upid, doneMsg string, opts *tasks.WaitOptions) error {
	if deps.Async {
		return RenderUPID(cmd, deps, upid)
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, opts); err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}
