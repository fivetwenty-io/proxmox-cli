package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
)

// storageResource is the minimal decoded shape of one cluster/resources entry
// of type "storage": which storage is visible on which node, whether it is
// currently usable there, and whether every node sees the same contents.
type storageResource struct {
	Storage string `json:"storage"`
	Node    string `json:"node"`
	Status  string `json:"status"`
	Shared  int    `json:"shared"`
}

// ResolveStorageNode maps a storage identifier to a node that can serve it.
//
// Storage-scoped commands need some node's view of the storage, but which node
// has it is cluster knowledge: a local storage exists per node, and a shared
// one is the same everywhere. As with guests, an ambient default node
// (PMX_NODE or the context default-node) is trusted only when --node was
// passed explicitly (deps.NodeExplicit); otherwise the cluster resource
// inventory is queried and a node carrying the storage is chosen:
//
//   - the ambient default node, whenever it is among the carriers (no
//     surprise, even if the storage is momentarily unavailable there);
//   - a sole carrier;
//   - for a shared storage, the first carrier in lexical order, preferring
//     nodes where the storage is currently available over ones where it is
//     not — every carrier serves the same contents, so any is correct;
//   - never a guess for a non-shared storage on several nodes: each node's
//     copy has distinct contents (think "local"), so picking one would
//     silently target data the user never named. That case is an error
//     asking for --node.
//
// A storage no node carries is also an error.
func ResolveStorageNode(ctx context.Context, deps *Deps, storage string) (string, error) {
	if deps.NodeExplicit {
		return deps.Node, nil
	}

	typeStorage := "storage"
	resp, err := deps.API.Cluster.ListResources(ctx, &pvecluster.ListResourcesParams{Type: &typeStorage})
	if err != nil {
		return "", fmt.Errorf("list cluster resources to resolve storage %q: %w", storage, err)
	}

	var carriers, available []string
	shared := false
	seen := map[string]bool{}
	if resp != nil {
		for _, raw := range *resp {
			var s storageResource
			if err := json.Unmarshal(raw, &s); err != nil {
				return "", fmt.Errorf("decode cluster resource entry: %w", err)
			}
			if s.Storage != storage || s.Node == "" || seen[s.Node] {
				continue
			}
			seen[s.Node] = true
			carriers = append(carriers, s.Node)
			if s.Status == "available" {
				available = append(available, s.Node)
			}
			if s.Shared != 0 {
				shared = true
			}
		}
	}

	if len(carriers) == 0 {
		return "", fmt.Errorf("storage %q not found on any node; pass --node if it exists", storage)
	}
	if deps.Node != "" && seen[deps.Node] {
		return deps.Node, nil
	}
	if len(carriers) == 1 {
		return carriers[0], nil
	}
	if !shared {
		sort.Strings(carriers)
		return "", fmt.Errorf("storage %q is not shared and exists on several nodes (%s) with distinct contents; pass --node to choose one",
			storage, strings.Join(carriers, ", "))
	}
	candidates := available
	if len(candidates) == 0 {
		candidates = carriers
	}
	sort.Strings(candidates)
	return candidates[0], nil
}

// NoteResolvedStorageNode writes the migrate-family-style stderr note for a
// storage whose node was chosen from the cluster rather than pinned by --node
// or satisfied by the ambient default node.
func NoteResolvedStorageNode(w io.Writer, deps *Deps, storage, node string) {
	if deps.NodeExplicit || node == deps.Node {
		return
	}
	_, _ = fmt.Fprintf(w, "note: auto-resolved node %q for storage %s\n", node, storage)
}
