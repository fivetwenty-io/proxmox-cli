package lxc

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// lxcInterfaceEntry is the subset of a /lxc/{vmid}/interfaces element rendered in
// the table. The PVE response is an array of untyped objects describing each NIC
// as seen from the host; the full object is preserved in the JSON/Raw output.
type lxcInterfaceEntry struct {
	Name     string `json:"name"`
	HwAddr   string `json:"hwaddr"`
	Inet     string `json:"inet"`
	Inet6    string `json:"inet6"`
	HardName string `json:"hardware-address"`
}

// newInterfacesCmd builds `pve lxc interfaces <vmid>`. It lists the container's
// network interfaces as reported by the host. The call is purely read-only.
func newInterfacesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "interfaces <vmid>",
		Short: "List a container's network interfaces",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListLxcInterfaces(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("list interfaces for container %s: %w", vmid, err)
			}

			entries := make([]lxcInterfaceEntry, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e lxcInterfaceEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode interface entry: %w", err)
					}
					entries = append(entries, e)
				}
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name < entries[j].Name
			})

			res := output.Result{
				Headers: []string{"NAME", "HWADDR", "INET", "INET6"},
				Raw:     resp,
			}
			for _, e := range entries {
				hwaddr := e.HwAddr
				if hwaddr == "" {
					hwaddr = e.HardName
				}
				res.Rows = append(res.Rows, []string{e.Name, hwaddr, e.Inet, e.Inet6})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
