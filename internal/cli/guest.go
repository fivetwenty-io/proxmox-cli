package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
)

// Guest type identifiers. These match the "type" field of a cluster/resources
// entry, so they double as the filter applied when resolving a target.
const (
	GuestQemu = "qemu"
	GuestLXC  = "lxc"
)

// guestResource is the minimal decoded shape of one cluster/resources entry
// needed to resolve a guest target to its VMID and the node it runs on.
type guestResource struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Node string `json:"node"`
	VMID *int64 `json:"vmid"`
	ID   string `json:"id"`
}

// vmidString returns the entry's numeric VMID as a string, deriving it from the
// id suffix (e.g. "qemu/100") when the vmid field is absent.
func (g guestResource) vmidString() string {
	if g.VMID != nil {
		return strconv.FormatInt(*g.VMID, 10)
	}
	if i := strings.LastIndex(g.ID, "/"); i >= 0 {
		return g.ID[i+1:]
	}
	return ""
}

// ResolveGuest maps a <vmid|name> target to a numeric VMID and the node it runs
// on. guestType is GuestQemu or GuestLXC and restricts matches to that kind of
// guest.
//
// When the target is numeric and a node is already known (deps.Node != ""), it
// is returned as-is without any API call, preserving the latency and behavior of
// explicit-node invocations. Otherwise the cluster resource inventory is queried
// to resolve the VMID and/or node:
//
//   - a numeric target matches the entry with that VMID;
//   - a name target matches the entry whose name is exactly that string.
//
// When deps.Node is set, matches are restricted to that node, which disambiguates
// duplicate names across nodes. A target that matches no guest, or an unqualified
// name that matches guests on more than one node, is an error.
func ResolveGuest(ctx context.Context, deps *Deps, target, guestType string) (vmid, node string, err error) {
	numeric := isNumericVMID(target)

	// Fast path: numeric VMID with a node already known needs no API call.
	if numeric && deps.Node != "" {
		return target, deps.Node, nil
	}

	typeVM := "vm"
	resp, err := deps.API.Cluster.ListResources(ctx, &pvecluster.ListResourcesParams{Type: &typeVM})
	if err != nil {
		return "", "", fmt.Errorf("list cluster resources to resolve %s guest %q: %w", guestType, target, err)
	}

	var matches []guestResource
	if resp != nil {
		for _, raw := range *resp {
			var g guestResource
			if err := json.Unmarshal(raw, &g); err != nil {
				return "", "", fmt.Errorf("decode cluster resource entry: %w", err)
			}
			if g.Type != guestType {
				continue
			}
			if deps.Node != "" && g.Node != deps.Node {
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
		return "", "", fmt.Errorf("%s guest %q not found", guestType, target)
	case 1:
		return matches[0].vmidString(), matches[0].Node, nil
	default:
		nodes := make([]string, 0, len(matches))
		for _, m := range matches {
			nodes = append(nodes, m.Node)
		}
		return "", "", fmt.Errorf(
			"%s guest %q is ambiguous: found on nodes %s; pass --node or the VMID to disambiguate",
			guestType, target, strings.Join(nodes, ", "))
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
