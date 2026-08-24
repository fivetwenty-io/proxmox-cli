package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// Guest type identifiers. These match the "type" field of a cluster/resources
// entry, so they double as the filter applied when resolving a target.
// GuestAny matches either kind; VMIDs are unique cluster-wide, so a numeric
// target stays unambiguous without a type filter.
const (
	GuestQemu = "qemu"
	GuestLXC  = "lxc"
	GuestAny  = ""
)

// guestLabel names the guest kind in error messages ("qemu guest", or just
// "guest" for GuestAny).
func guestLabel(guestType string) string {
	if guestType == GuestAny {
		return "guest"
	}
	return guestType + " guest"
}

// guestResource is the minimal decoded shape of one cluster/resources entry
// needed to resolve a guest target to its VMID and the node it runs on.
type guestResource struct {
	Type string      `json:"type"`
	Name string      `json:"name"`
	Node string      `json:"node"`
	VMID *pve.PVEInt `json:"vmid"`
	ID   string      `json:"id"`
}

// vmidString returns the entry's numeric VMID as a string, deriving it from the
// id suffix (e.g. "qemu/100") when the vmid field is absent.
func (g guestResource) vmidString() string {
	if g.VMID != nil {
		return strconv.FormatInt(int64(*g.VMID), 10)
	}
	if i := strings.LastIndex(g.ID, "/"); i >= 0 {
		return g.ID[i+1:]
	}
	return ""
}

// ResolveGuest maps a <vmid|name> target to a numeric VMID and the node the
// guest actually runs on. guestType is GuestQemu or GuestLXC and restricts
// matches to that kind of guest; GuestAny matches either kind.
//
// An ambient default node (PMX_NODE or the context default-node) describes
// where node-scoped commands run by default — not where an arbitrary guest
// lives — so deps.Node is trusted as the guest's location only when
// deps.NodeExplicit reports that --node was passed on the command line. In
// that case a numeric target returns the pinned node without any API call,
// and a name target is matched against that node only (which disambiguates
// duplicate names across nodes). Otherwise the cluster resource inventory is
// queried regardless of any default:
//
//   - a numeric target matches the entry with that VMID;
//   - a name target matches the entry whose name is exactly that string.
//
// A target that matches no guest, or one that matches guests on more than one
// node, is an error asking for an explicit --node.
func ResolveGuest(ctx context.Context, deps *Deps, target, guestType string) (vmid, node string, err error) {
	if deps.NodeExplicit {
		return resolveGuestOn(ctx, deps, target, guestType, deps.Node)
	}
	return resolveGuestOn(ctx, deps, target, guestType, "")
}

// resolveGuestOn implements guest resolution with pinnedNode as the known/
// filter node (see ResolveGuest for semantics; an empty pinnedNode forces a
// cluster lookup even when a default node is configured).
func resolveGuestOn(ctx context.Context, deps *Deps, target, guestType, pinnedNode string) (vmid, node string, err error) {
	numeric := isNumericVMID(target)

	// Fast path: numeric VMID with an explicitly pinned node needs no API call.
	if numeric && pinnedNode != "" {
		return target, pinnedNode, nil
	}

	typeVM := "vm"
	resp, err := deps.API.Cluster.ListResources(ctx, &pvecluster.ListResourcesParams{Type: &typeVM})
	if err != nil {
		return "", "", fmt.Errorf("list cluster resources to resolve %s %q: %w", guestLabel(guestType), target, err)
	}

	var matches []guestResource
	if resp != nil {
		for _, raw := range *resp {
			var g guestResource
			if err := json.Unmarshal(raw, &g); err != nil {
				return "", "", fmt.Errorf("decode cluster resource entry: %w", err)
			}
			if guestType != GuestAny && g.Type != guestType {
				continue
			}
			if pinnedNode != "" && g.Node != pinnedNode {
				continue
			}
			if numeric {
				if g.vmidString() == target {
					matches = append(matches, g)
				}
			} else if g.Name == target {
				matches = append(matches, g)
			}
		}
	}

	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("%s %q not found", guestLabel(guestType), target)
	case 1:
		return matches[0].vmidString(), matches[0].Node, nil
	default:
		nodes := make([]string, 0, len(matches))
		for _, m := range matches {
			nodes = append(nodes, m.Node)
		}
		hint := "pass --node or the VMID to disambiguate"
		if numeric {
			hint = "pass --node to disambiguate"
		}
		return "", "", fmt.Errorf(
			"%s %q is ambiguous: found on nodes %s; %s",
			guestLabel(guestType), target, strings.Join(nodes, ", "), hint)
	}
}

// isNumericVMID reports whether s is a base-10 integer (a VMID), as opposed to a
// guest name.
func isNumericVMID(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}
