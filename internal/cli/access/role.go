package access

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newRoleCmd builds `pve access role` and its sub-commands.
func newRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Manage roles and their privileges",
		Long:  "List, inspect, create, update, and delete roles and the privileges they grant.",
	}
	cmd.AddCommand(
		newRoleListCmd(),
		newRoleGetCmd(),
		newRoleCreateCmd(),
		newRoleSetCmd(),
		newRoleDeleteCmd(),
	)
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
			deps := cli.GetDeps(cmd)

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
			deps := cli.GetDeps(cmd)
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

// newRoleCreateCmd builds `pve access role create <roleid> [--privs <csv>]`.
func newRoleCreateCmd() *cobra.Command {
	var privs string
	cmd := &cobra.Command{
		Use:   "create <roleid>",
		Short: "Create a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			roleid := args[0]

			params := &access.CreateRolesParams{Roleid: roleid}
			setIfChanged(cmd, "privs", &params.Privs, privs)

			if err := deps.API.Access.CreateRoles(cmd.Context(), params); err != nil {
				return fmt.Errorf("create role %q: %w", roleid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Role '%s' created.", roleid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&privs, "privs", "", "comma-separated list of privileges to grant")
	return cmd
}

// newRoleSetCmd builds `pve access role set <roleid> --privs <csv> [--append]`.
// Without --append the privilege list replaces the role's existing privileges.
func newRoleSetCmd() *cobra.Command {
	var privs string
	var appendPrivs bool
	cmd := &cobra.Command{
		Use:   "set <roleid> --privs <csv>",
		Short: "Update a role's privileges",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			roleid := args[0]

			if !cmd.Flags().Changed("privs") {
				return fmt.Errorf("--privs is required")
			}

			params := &access.UpdateRolesParams{}
			setIfChanged(cmd, "privs", &params.Privs, privs)
			setBoolIfChanged(cmd, "append", &params.Append, appendPrivs)

			if err := deps.API.Access.UpdateRoles(cmd.Context(), roleid, params); err != nil {
				return fmt.Errorf("update role %q: %w", roleid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Role '%s' updated.", roleid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&privs, "privs", "", "comma-separated list of privileges (required)")
	cmd.Flags().BoolVar(&appendPrivs, "append", false, "add the privileges to the role instead of replacing them")
	return cmd
}

// newRoleDeleteCmd builds `pve access role delete <roleid>`.
func newRoleDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <roleid>",
		Short: "Delete a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			roleid := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete role %q without --yes/-y", roleid)
			}

			if err := deps.API.Access.DeleteRoles(cmd.Context(), roleid); err != nil {
				return fmt.Errorf("delete role %q: %w", roleid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Role '%s' deleted.", roleid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion")
	return cmd
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
