package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/tasks"
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
// a descriptive error.
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

	return nil
}
