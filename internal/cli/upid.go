package cli

import (
	"fmt"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
)

// NodeFromUPID extracts the node a task ran on from its UPID (the first
// colon-separated field). Task endpoints only answer on that node, so the UPID
// is authoritative: an ambient default node is never consulted, and an
// explicit --node naming a different node is rejected as a conflict rather
// than silently overridden or silently ignored.
func NodeFromUPID(deps *Deps, upid string) (string, error) {
	parsed, err := tasks.ParseUPID(upid)
	if err != nil {
		return "", fmt.Errorf("parse upid %q: %w", upid, err)
	}
	if parsed.Node == "" {
		return "", fmt.Errorf("upid %q has an empty node field", upid)
	}
	if deps.NodeExplicit && deps.Node != parsed.Node {
		return "", fmt.Errorf("--node %q conflicts with node %q from the UPID; drop --node or pass the matching node",
			deps.Node, parsed.Node)
	}
	return parsed.Node, nil
}
