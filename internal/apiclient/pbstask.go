package apiclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

// pbsUPIDFieldCount is the number of colon-separated fields in a PBS UPID
// after the trailing colon is trimmed:
//
//	UPID:<node>:<pid>:<pstart>:<task-id>:<starttime>:<worker-type>:<worker-id>:<user>:
const pbsUPIDFieldCount = 9

// PBSUPIDNode extracts the node name from a PBS UPID string.
//
// PBS UPIDs carry one more field than PVE UPIDs (a hex task-id between the
// process-start and start-time fields), so the library's tasks.ParseUPID —
// which validates the 8-field PVE shape — rejects them. Only the node is
// needed to poll task status, so this helper validates the PBS shape and
// returns that single field.
func PBSUPIDNode(upid string) (string, error) {
	trimmed := strings.TrimSuffix(upid, ":")
	parts := strings.Split(trimmed, ":")

	if len(parts) != pbsUPIDFieldCount {
		return "", fmt.Errorf("parse PBS UPID %q: expected %d fields, got %d", upid, pbsUPIDFieldCount, len(parts))
	}

	if parts[0] != "UPID" {
		return "", fmt.Errorf("parse PBS UPID %q: missing UPID prefix", upid)
	}

	if parts[1] == "" {
		return "", fmt.Errorf("parse PBS UPID %q: empty node field", upid)
	}

	return parts[1], nil
}

// WaitPBSTask blocks until the PBS task identified by upid reaches a terminal
// state. The task-status endpoint (/nodes/{node}/tasks/{upid}/status) and its
// status/exitstatus fields are identical between PVE and PBS, so polling is
// delegated to the library's tasks service with the node parsed from the PBS
// UPID. A nil opts is accepted and causes the service to use its default
// timeout and polling interval.
//
// On success it returns nil; on task failure or context cancellation it
// returns a descriptive error.
func WaitPBSTask(ctx context.Context, pc *PBSClient, upid string, opts *tasks.WaitOptions) error {
	node, err := PBSUPIDNode(upid)
	if err != nil {
		return err
	}

	status, err := tasks.New(pc.Raw).Wait(ctx, node, upid, opts)
	if err != nil {
		return fmt.Errorf("wait task %s: %w", upid, err)
	}

	// Wait only returns a non-error status for OK or warning exits; guard
	// against a nil status defensively.
	if status == nil {
		return fmt.Errorf("wait task %s: nil status returned", upid)
	}

	return nil
}
