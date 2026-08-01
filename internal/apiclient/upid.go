package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

// UPIDFromRaw extracts a UPID string from a json.RawMessage.
//
// All async PVE responses (DeleteQemu, CreateQemuStatusStart, etc.) are typed
// as json.RawMessage aliases whose underlying data is a JSON-encoded string, for
// example: `"UPID:pve:000A1B2C:..."`. This helper unmarshals the message to a
// plain string and validates that it is a well-formed UPID (every PVE UPID
// begins with the "UPID:" prefix). Callers that classify a response as async vs
// sync — e.g. disk resize and SDN apply — rely on this rejecting a non-UPID
// body rather than mistaking it for a task handle.
func UPIDFromRaw(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("decode UPID: empty raw message")
	}

	var upid string
	if err := json.Unmarshal(raw, &upid); err != nil {
		return "", fmt.Errorf("decode UPID: %w", err)
	}

	if upid == "" {
		return "", fmt.Errorf("decode UPID: empty UPID string in response")
	}

	if !strings.HasPrefix(upid, "UPID:") {
		return "", fmt.Errorf("decode UPID: %q is not a UPID", upid)
	}

	return upid, nil
}

// WaitTask blocks until the Proxmox task identified by upid reaches a terminal
// state, delegating to ac.Tasks.WaitForUPID. A nil opts is accepted and causes
// the service to use its default timeout and polling interval.
//
// On success it returns nil; on task failure or context cancellation it returns
// a descriptive error. A task that completed with warnings returns nil — the
// task did finish — but reports the warning via taskWarned.
func WaitTask(ctx context.Context, ac *APIClient, upid string, opts *tasks.WaitOptions) error {
	status, err := ac.Tasks.WaitForUPID(ctx, upid, opts)
	if err != nil {
		return fmt.Errorf("wait task %s: %w", upid, err)
	}

	// WaitForUPID only returns a non-error status for OK or warning exits;
	// guard against a nil status defensively.
	if status == nil {
		return fmt.Errorf("wait task %s: nil status returned", upid)
	}

	return taskWarned(status)
}

// warningsAreErrors selects what a "WARNINGS: N" task means to the exit code.
// It is process-wide state set once from the root command's flag/config
// resolution, for the same reason the warning itself goes straight to stderr:
// the alternative is threading a policy value through 33 call sites that
// otherwise have no interest in it.
var warningsAreErrors atomic.Bool

// SetWarningsAsErrors selects whether a task that finished with warnings is
// reported as a failure. It is called once, from the root command, before any
// task runs.
//
// The default (false) is the historical behaviour: the warning is printed and
// the command still exits 0. Whether a warning constitutes failure is a
// judgement about the operator's own tolerances — a vzdump that skipped one
// unreachable guest is routine for some fleets and an incident for others —
// and scripts already branch on the current codes, so it is opt-in rather
// than a changed default.
func SetWarningsAsErrors(v bool) { warningsAreErrors.Store(v) }

// WarningsAsErrorsEnabled reports the policy currently in effect. It exists so
// the root command's flag/env/config resolution can be asserted end to end
// without running a real task.
func WarningsAsErrorsEnabled() bool { return warningsAreErrors.Load() }

// TaskWarnedError reports a task that reached a terminal state with a
// "WARNINGS: N" exit status while --warnings-as-errors was in effect. The task
// itself completed: the work it did is done, and this error describes the
// outcome rather than an aborted operation.
type TaskWarnedError struct {
	// UPID identifies the task on the server, so the full log is one
	// `pmx pve task log <upid>` away.
	UPID string
	// ExitStatus is the server's verbatim terminal status, e.g. "WARNINGS: 2".
	ExitStatus string
}

func (e *TaskWarnedError) Error() string {
	return fmt.Sprintf("task %s finished with %q", e.UPID, e.ExitStatus)
}

// taskWarned reports a task that reached a terminal state with a
// "WARNINGS: N" exit status.
//
// The SDK returns such a task as a success with Status.Warned set, and all
// three wait helpers discarded that flag, so a vzdump that skipped a guest
// printed "Backup completed" and exited 0 with nothing anywhere saying
// otherwise. The warning now always reaches stderr; it additionally becomes an
// error when the operator opted in via SetWarningsAsErrors.
//
// Writing to stderr from here rather than threading a writer through 33 call
// sites follows internal/config's existing inline-secret warning.
func taskWarned(status *tasks.Status) error {
	if status == nil || !status.Warned {
		return nil
	}
	fmt.Fprintf(os.Stderr, "WARN: task %s finished with %q\n", status.UpID, status.ExitStatus)

	if warningsAreErrors.Load() {
		return &TaskWarnedError{UPID: status.UpID, ExitStatus: status.ExitStatus}
	}
	return nil
}
