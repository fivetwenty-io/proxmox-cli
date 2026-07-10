package access

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
)

// newACLCmd builds `pmx access acl` and its sub-commands.
func newACLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acl",
		Short: "Manage access control list entries",
		Long:  "List ACL entries and grant or revoke roles on a path.",
	}
	cmd.AddCommand(newACLListCmd(), newACLSetCmd())
	return cmd
}

// aclEntry is a single row of the GET /access/acl response.
type aclEntry struct {
	Path      string  `json:"path"`
	Type      string  `json:"type"`
	Ugid      string  `json:"ugid"`
	Roleid    string  `json:"roleid"`
	Propagate pveBool `json:"propagate,omitempty"`
}

// newACLListCmd builds `pmx access acl list`.
func newACLListCmd() *cobra.Command {
	var path string
	var exact bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List ACL entries",
		Long: "List every ACL entry (path, subject, role, propagate flag). Pass --path to " +
			"restrict to entries under a path (prefix match by default; pass --exact to " +
			"require an exact match).",
		Example: `  pmx pve access acl list
  pmx pve access acl list --path /vms/100 --exact`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Access.ListAcl(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acl: %w", err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e aclEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode acl entry: %w", err)
				}
				if !aclMatch(e.Path, path, exact) {
					continue
				}
				rows = append(rows, []string{e.Path, e.Type, e.Ugid, e.Roleid, e.Propagate.cell()})
			}

			result := output.Result{
				Headers: []string{"PATH", "TYPE", "UGID", "ROLEID", "PROPAGATE"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "only show entries for this path")
	cmd.Flags().BoolVar(&exact, "exact", false, "require an exact path match (default: prefix match)")
	return cmd
}

// aclMatch reports whether entryPath passes the optional path filter. An empty
// filter matches everything; exact requires equality, otherwise prefix match.
func aclMatch(entryPath, filter string, exact bool) bool {
	if filter == "" {
		return true
	}
	if exact {
		return entryPath == filter
	}
	return len(entryPath) >= len(filter) && entryPath[:len(filter)] == filter
}

// newACLSetCmd builds `pmx access acl set`.
func newACLSetCmd() *cobra.Command {
	var (
		path, roles, users, groups, tokens string
		propagate, del                     bool
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Grant or revoke roles on a path",
		Long: "Grant one or more roles on a path to the given users, groups, and/or tokens. " +
			"--path and --roles are required. Roles propagate to nested paths by default; " +
			"pass --propagate=false to grant non-propagating access. Pass --delete to remove " +
			"the listed permissions instead of adding them.",
		Example: `  pmx pve access acl set --path /vms/100 --roles PVEVMAdmin --users alice@pve
  pmx pve access acl set --path /vms/100 --roles PVEVMAdmin --users alice@pve --delete`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if path == "" {
				return fmt.Errorf("--path is required")
			}
			if roles == "" {
				return fmt.Errorf("--roles is required")
			}

			params := &access.UpdateAclParams{Path: path, Roles: roles}
			setIfChanged(cmd, "users", &params.Users, users)
			setIfChanged(cmd, "groups", &params.Groups, groups)
			setIfChanged(cmd, "tokens", &params.Tokens, tokens)
			if cmd.Flags().Changed("propagate") {
				params.Propagate = &propagate
			}
			if cmd.Flags().Changed("delete") {
				params.Delete = &del
			}

			if err := deps.API.Access.UpdateAcl(cmd.Context(), params); err != nil {
				return fmt.Errorf("update acl: %w", err)
			}

			result := output.Result{Message: "ACL updated."}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "access control path (required)")
	cmd.Flags().StringVar(&roles, "roles", "", "comma-separated role list (required)")
	cmd.Flags().StringVar(&users, "users", "", "comma-separated user list")
	cmd.Flags().StringVar(&groups, "groups", "", "comma-separated group list")
	cmd.Flags().StringVar(&tokens, "tokens", "", "comma-separated API token list")
	cmd.Flags().BoolVar(&propagate, "propagate", true, "allow permissions to propagate")
	cmd.Flags().BoolVar(&del, "delete", false, "remove the listed permissions instead of adding them")
	return cmd
}
