package cli

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// RenderTaskWait is the one async-or-wait-then-message block that every
// task-producing verb shares. Under --async it renders the UPID and returns
// immediately. Otherwise it waits for the task with opts and renders doneMsg
// once the task finishes. A failed wait is returned exactly as
// apiclient.WaitTask reports it, with nothing rendered, so a caller is free
// to wrap it with its own operation prefix. Wrap it exactly once, because a
// caller that already carries a prefix, like the bulk and Ceph task
// renderers, would otherwise stack a second one on top of any caller further
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
