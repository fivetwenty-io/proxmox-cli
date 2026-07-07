package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newRoleCmd builds `pmx pbs role` — list the built-in Proxmox Backup
// Server roles and the privileges each one grants (GET /access/roles).
// Unlike Proxmox VE, PBS roles are a fixed enum: the API exposes no
// create/update/delete operations for them, so this group only has `ls`.
func newRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "List Proxmox Backup Server roles",
		Long: "List the built-in Proxmox Backup Server roles and the privileges each " +
			"one grants. PBS roles are a fixed enum with no create, update, or delete " +
			"operations.",
	}
	cmd.AddCommand(newRoleLsCmd())
	return cmd
}

// roleListEntry is the decoded shape of one element of GET /access/roles.
type roleListEntry struct {
	Comment *string  `json:"comment,omitempty"`
	Privs   []string `json:"privs"`
	Roleid  string   `json:"roleid"`
}

// newRoleLsCmd builds `pmx pbs role ls` — list roles (GET /access/roles).
func newRoleLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List roles",
		Long:  "List the built-in roles and the privileges each one grants (GET /access/roles).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Access.ListRoles(cmd.Context())
			if err != nil {
				return fmt.Errorf("list roles: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]roleListEntry, 0, len(items))

			for _, raw := range items {
				var e roleListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode role entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Roleid < entries[j].Roleid })

			headers := []string{"ROLEID", "PRIVS", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{e.Roleid, strings.Join(e.Privs, ","), pbsFormatOptionalString(e.Comment)})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
