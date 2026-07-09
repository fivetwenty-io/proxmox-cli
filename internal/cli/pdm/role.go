package pdm

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newRoleCmd builds `pmx pdm role` — list the roles available on this
// Proxmox Datacenter Manager instance and the privileges each one grants
// (GET /access/roles). The API exposes no create/update/delete operations
// for roles, so this group only has `ls`.
func newRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "List Proxmox Datacenter Manager roles",
		Long: "List the roles available on this Proxmox Datacenter Manager instance " +
			"and the privileges each one grants (GET /access/roles).",
	}
	cmd.AddCommand(newRoleLsCmd())
	return cmd
}

// roleListEntry is the decoded shape of one element of GET /access/roles,
// per pdm-apidoc.json's returns.items schema (comment, privs, roleid).
// access_gen.go declares only the outer ListRolesResponse []json.RawMessage
// without a per-item type.
type roleListEntry struct {
	Comment *string  `json:"comment,omitempty"`
	Privs   []string `json:"privs"`
	Roleid  string   `json:"roleid"`
}

// newRoleLsCmd builds `pmx pdm role ls` — list roles (GET /access/roles).
func newRoleLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List roles",
		Long:  "List the available roles and the privileges each one grants (GET /access/roles).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Access.ListRoles(cmd.Context())
			if err != nil {
				return fmt.Errorf("list roles: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[roleListEntry](items, "role")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Roleid < table[j].Entry.Roleid })

			headers := []string{"ROLEID", "PRIVS", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{e.Roleid, strings.Join(e.Privs, ","), strPtrString(e.Comment)})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
