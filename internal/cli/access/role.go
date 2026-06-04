package access

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newRoleCmd builds `pve access role` and its sub-commands.
func newRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Inspect roles and their privileges",
		Long:  "List roles and show the privileges granted by a role.",
	}
	cmd.AddCommand(newRoleListCmd(), newRoleGetCmd())
	return cmd
}

// roleListEntry is a single row of the GET /access/roles response.
type roleListEntry struct {
	Roleid  string  `json:"roleid"`
	Special pveBool `json:"special,omitempty"`
	Privs   string  `json:"privs,omitempty"`
}

// newRoleListCmd builds `pve access role list`.
func newRoleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List roles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			resp, err := deps.API.Access.ListRoles(cmd.Context())
			if err != nil {
				return fmt.Errorf("list roles: %w", err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e roleListEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode role entry: %w", err)
				}
				rows = append(rows, []string{e.Roleid, e.Special.cell(), e.Privs})
			}

			result := output.Result{
				Headers: []string{"ROLEID", "SPECIAL", "PRIVS"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newRoleGetCmd builds `pve access role get <roleid>`. The response is a map of
// privilege name to a boolean flag; enabled privileges are listed.
func newRoleGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <roleid>",
		Short: "Show the privileges granted by a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			roleid := args[0]

			resp, err := deps.API.Access.GetRoles(cmd.Context(), roleid)
			if err != nil {
				return fmt.Errorf("get role %q: %w", roleid, err)
			}

			privs := enabledPrivs(resp)
			rows := make([][]string, 0, len(privs))
			for _, p := range privs {
				rows = append(rows, []string{roleid, p})
			}

			result := output.Result{
				Headers: []string{"ROLEID", "PRIV"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// enabledPrivs re-marshals the typed role response into a name→bool map and
// returns the sorted names of the privileges set to true.
func enabledPrivs(resp any) []string {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	var m map[string]bool
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for name, on := range m {
		if on {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
