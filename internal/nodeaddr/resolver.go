// Package nodeaddr resolves a Proxmox node name to an IP address via cluster status.
package nodeaddr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
)

// clusterStatusEntry is the minimal shape of each JSON object in the
// cluster.ListStatus response ([]json.RawMessage).  Fields not needed for
// resolution are intentionally omitted.
type clusterStatusEntry struct {
	// Type is "node" or "cluster".
	Type string `json:"type"`
	// Name is the PVE node name.
	Name string `json:"name"`
	// IP is the node's management IP address (present only for type=="node").
	IP string `json:"ip"`
	// Online is 1 when the node is reachable, 0 otherwise.
	Online int `json:"online"`
}

// StatusLister is the minimal interface required by Resolve for dependency
// injection in tests.  cluster.Service satisfies this interface.
type StatusLister interface {
	// ListStatus calls GET /cluster/status and returns the raw JSON items.
	ListStatus(ctx context.Context) (*cluster.ListStatusResponse, error)
}

// Resolve returns the IP address for the named Proxmox node by querying
// the cluster status endpoint.  The lookup matches the first entry whose
// type is "node" and whose name equals node (case-sensitive).
//
// Fallback behaviour: if the cluster status list is empty, if the named node
// is not found, or if any error occurs during resolution, node is returned
// unchanged as the host string so that callers can still attempt a connection
// using the symbolic node name as a hostname.
//
// The empty-list and not-found fallbacks are ordinary (a single-node install
// returns no cluster status at all), but a failed lookup is not: an expired
// token or an unreachable API silently degrades into a node name that is
// rarely a real hostname, so the operator sees "ssh: Could not resolve
// hostname pve-1" — a DNS error naming neither the API call that failed nor
// why. That one case is reported via log and stderr before falling back. log
// may be nil.
func Resolve(ctx context.Context, svc StatusLister, node string, log *slog.Logger) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("nodeaddr.Resolve: ctx must not be nil")
	}
	if node == "" {
		return "", fmt.Errorf("nodeaddr.Resolve: node name must not be empty")
	}

	resp, err := svc.ListStatus(ctx)
	if err != nil {
		// Non-fatal: fall back to the node name so callers can still proceed,
		// but say why, or the cause is lost behind a downstream DNS error.
		warnLookupFailed(node, err, log)
		return node, nil
	}

	if resp == nil || len(*resp) == 0 {
		// Single-node installs may return an empty list; fall back.
		return node, nil
	}

	for _, raw := range *resp {
		var entry clusterStatusEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			// Skip malformed entries; continue scanning.
			continue
		}
		if entry.Type == "node" && entry.Name == node {
			if entry.IP == "" {
				// Matched node but IP field is absent; fall back.
				return node, nil
			}
			return entry.IP, nil
		}
	}

	// Node not found in cluster status list; fall back to node name as host.
	return node, nil
}

// warnLookupFailed reports a failed cluster-status lookup to log (when one was
// supplied) and to stderr, naming both the node and the underlying cause.
//
// Writing to stderr from this package rather than threading a writer through
// every caller follows the same reasoning as internal/apiclient's task-warning
// notice: the message exists for the operator watching the command, and the
// fallback it announces is invisible everywhere else. Query parameters are
// redacted because a transport error can carry the request URL, and an
// authentication ticket travels in it.
func warnLookupFailed(node string, err error, log *slog.Logger) {
	reason := redact.QueryParams(err.Error())
	if log != nil {
		log.Warn("cluster status lookup failed; using the node name as the host",
			"node", node, "error", reason)
	}
	fmt.Fprintf(os.Stderr,
		"WARN: could not look up node %q in cluster status (%s); using %q as the host\n",
		node, reason, node)
}
