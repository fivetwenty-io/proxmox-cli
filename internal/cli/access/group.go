package access

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newGroupResourceCmd builds `pmx pve access group` and its sub-commands.
func newGroupResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage user groups",
		Long:  "List, inspect, create, update, and delete user groups.",
	}
	cmd.AddCommand(
		newGroupListCmd(),
		newGroupGetCmd(),
		newGroupCreateCmd(),
		newGroupSetCmd(),
		newGroupDeleteCmd(),
	)
	return cmd
}

// groupListEntry is a single row of the GET /access/groups response.
type groupListEntry struct {
	Groupid string `json:"groupid"`
	Comment string `json:"comment,omitempty"`
	Users   string `json:"users,omitempty"`
}

// newGroupListCmd builds `pmx pve access group list`.
func newGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List groups",
		Long:    "List every user group with its comment and comma-separated member list.",
		Example: `  pmx pve access group list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Access.ListGroups(cmd.Context())
			if err != nil {
				return fmt.Errorf("list groups: %w", err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e groupListEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode group entry: %w", err)
				}
				rows = append(rows, []string{e.Groupid, e.Comment, e.Users})
			}

			result := output.Result{
				Headers: []string{"GROUPID", "COMMENT", "USERS"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newGroupGetCmd builds `pmx pve access group get <groupid>`.
func newGroupGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <groupid>",
		Short:   "Show a group's details",
		Long:    "Show a group's comment and its member user IDs.",
		Example: `  pmx pve access group get admins`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			groupid := args[0]

			resp, err := deps.API.Access.GetGroups(cmd.Context(), groupid)
			if err != nil {
				return fmt.Errorf("get group %q: %w", groupid, err)
			}

			single := map[string]string{
				"GROUPID": groupid,
				"COMMENT": strVal(resp.Comment),
				"MEMBERS": joinComma(resp.Members),
			}
			result := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newGroupCreateCmd builds `pmx pve access group create <groupid>`.
func newGroupCreateCmd() *cobra.Command {
	var comment string
	cmd := &cobra.Command{
		Use:     "create <groupid>",
		Short:   "Create a group",
		Long:    "Create a new, empty user group. Add members with `pmx pve access user set`.",
		Example: `  pmx pve access group create admins --comment "Cluster administrators"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			groupid := args[0]

			params := &access.CreateGroupsParams{Groupid: groupid}
			setIfChanged(cmd, "comment", &params.Comment, comment)

			if err := deps.API.Access.CreateGroups(cmd.Context(), params); err != nil {
				return fmt.Errorf("create group %q: %w", groupid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Group '%s' created.", groupid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	return cmd
}

// newGroupSetCmd builds `pmx pve access group set <groupid>`.
func newGroupSetCmd() *cobra.Command {
	var comment string
	cmd := &cobra.Command{
		Use:     "set <groupid>",
		Short:   "Update a group",
		Long:    "Update a group's comment. Membership is managed via `pmx pve access user set`.",
		Example: `  pmx pve access group set admins --comment "Cluster administrators"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			groupid := args[0]

			params := &access.UpdateGroupsParams{}
			setIfChanged(cmd, "comment", &params.Comment, comment)

			if err := deps.API.Access.UpdateGroups(cmd.Context(), groupid, params); err != nil {
				return fmt.Errorf("update group %q: %w", groupid, err)
			}

			result := output.Result{Message: "Group updated."}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	return cmd
}

// newGroupDeleteCmd builds `pmx pve access group delete <groupid>`.
func newGroupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <groupid>",
		Short:   "Delete a group",
		Long:    "Delete a group. Refuses to run without --yes/-y.",
		Example: `  pmx pve access group delete admins --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			groupid := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete group %q without --yes/-y", groupid)
			}

			if err := deps.API.Access.DeleteGroups(cmd.Context(), groupid); err != nil {
				return fmt.Errorf("delete group %q: %w", groupid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Group '%s' deleted.", groupid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion")
	return cmd
}
