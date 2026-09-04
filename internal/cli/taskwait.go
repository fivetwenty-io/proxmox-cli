package cli

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// WaitBoundHelp is the sentence a task-producing verb appends to its Long text
// right after it says that the command blocks until the task finishes unless
// --async is set. The paragraph that describes the wait is where an operator
// looks for a way to cap it, so it names the global flag that does. A guard
// test walks every persona tree and fails on a leaf that mentions --async
// without this sentence.
const WaitBoundHelp = "The global --wait-timeout flag bounds how long the command waits for the task, " +
	"and its default of 0 waits until the task ends."

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

// AnnotationNoWaitBound marks a command whose help text mentions --async only
// to say that it has no effect, because the endpoint answers synchronously
// and starts no task. The wait-bound help guard skips such a command, since
// appending WaitBoundHelp to it would promise a bound on a wait that never
// happens.
const AnnotationNoWaitBound = "pmx-no-wait-bound"
