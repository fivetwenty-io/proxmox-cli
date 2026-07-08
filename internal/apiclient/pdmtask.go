package apiclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

// pdmUPIDFieldCount is the number of colon-separated fields in a PDM UPID
// after the trailing colon is trimmed:
//
//	UPID:<node>:<pid>:<pstart>:<task-id>:<starttime>:<worker-type>:<worker-id>:<user>:
//
// PDM runs the same proxmox-rest-server task stack as PBS, so its UPID shape
// carries the same extra hex task-id field between pstart and starttime that
// PVE's 8-field UPID lacks (verified against the vendored pdm-apidoc.json
// task-status UPID regex, which is byte-identical to PBS's).
const pdmUPIDFieldCount = 9

// PDMUPIDNode extracts the node name from a PDM UPID string.
//
// PDM UPIDs carry one more field than PVE UPIDs (a hex task-id between the
// process-start and start-time fields), so the library's tasks.ParseUPID —
// which validates the 8-field PVE shape — rejects them. Only the node is
// needed to poll task status, so this helper validates the PDM shape and
// returns that single field.
func PDMUPIDNode(upid string) (string, error) {
	trimmed := strings.TrimSuffix(upid, ":")
	parts := strings.Split(trimmed, ":")

	if len(parts) != pdmUPIDFieldCount {
		return "", fmt.Errorf("parse PDM UPID %q: expected %d fields, got %d", upid, pdmUPIDFieldCount, len(parts))
	}

	if parts[0] != "UPID" {
		return "", fmt.Errorf("parse PDM UPID %q: missing UPID prefix", upid)
	}

	if parts[1] == "" {
		return "", fmt.Errorf("parse PDM UPID %q: empty node field", upid)
	}

	return parts[1], nil
}

// WaitPDMTask blocks until the PDM task identified by upid reaches a terminal
// state. The task-status endpoint (/nodes/{node}/tasks/{upid}/status) and its
// status/exitstatus fields are identical between PVE and PDM, so polling is
// delegated to the library's tasks service with the node parsed from the PDM
// UPID. A nil opts is accepted and causes the service to use its default
// timeout and polling interval.
//
// On success it returns nil; on task failure or context cancellation it
// returns a descriptive error.
func WaitPDMTask(ctx context.Context, pc *PDMClient, upid string, opts *tasks.WaitOptions) error {
	node, err := PDMUPIDNode(upid)
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
