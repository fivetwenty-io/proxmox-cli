package node

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// warnNonResidentGuests warns on stderr when guests named in a --vmid/--vmids
// filter are not resident on the target node: the server-side bulk action and
// vzdump only cover guests on the node they run on, so a non-resident VMID is
// silently skipped rather than failed. The check is best-effort — it never
// blocks the action, and an unreadable cluster inventory is ignored.
func warnNonResidentGuests(cmd *cobra.Command, deps *cli.Deps, node, vmids string) {
	typeVM := "vm"
	resp, err := deps.API.Cluster.ListResources(cmd.Context(), &pvecluster.ListResourcesParams{Type: &typeVM})
	if err != nil || resp == nil {
		return
	}

	residentOn := map[string]string{}
	for _, raw := range *resp {
		var g struct {
			VMID *int64 `json:"vmid"`
			Node string `json:"node"`
		}
		if json.Unmarshal(raw, &g) != nil || g.VMID == nil {
			continue
		}
		residentOn[fmt.Sprintf("%d", *g.VMID)] = g.Node
	}

	for id := range strings.SplitSeq(vmids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		switch on, known := residentOn[id]; {
		case !known:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: guest %s not found in the cluster; it will not be included\n", id)
		case on != node:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: guest %s is on node %q, not %q; it will not be included\n", id, on, node)
		}
	}
}
