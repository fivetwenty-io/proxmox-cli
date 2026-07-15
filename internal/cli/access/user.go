package access

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newUserCmd builds `pmx access user` and its sub-commands.
func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage Proxmox VE users",
		Long:  "List, inspect, create, update, and delete Proxmox VE users.",
	}
	cmd.AddCommand(
		newUserListCmd(),
		newUserGetCmd(),
		newUserCreateCmd(),
		newUserSetCmd(),
		newUserDeleteCmd(),
		newTokenCmd(),
	)
	return cmd
}

// userListEntry is a single row of the GET /access/users response, which the
// generated client returns as a slice of raw JSON objects.
type userListEntry struct {
	Userid    string  `json:"userid"`
	Enable    pveBool `json:"enable,omitempty"`
	Expire    *int64  `json:"expire,omitempty"`
	Firstname string  `json:"firstname,omitempty"`
	Lastname  string  `json:"lastname,omitempty"`
	Email     string  `json:"email,omitempty"`
	Comment   string  `json:"comment,omitempty"`
	Groups    string  `json:"groups,omitempty"`
}

// newUserListCmd builds `pmx access user list`.
func newUserListCmd() *cobra.Command {
	var enabled, full bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users",
		Long: "List every Proxmox VE user with their enable state, expiration, name, and " +
			"group membership. Pass --enabled to show only enabled users, or --full to " +
			"include group and token information in the raw output.",
		Example: `  pmx pve access user list
  pmx pve access user list --enabled`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &access.ListUsersParams{}
			if cmd.Flags().Changed("enabled") {
				params.Enabled = &enabled
			}
			if cmd.Flags().Changed("full") {
				params.Full = &full
			}

			resp, err := deps.API.Access.ListUsers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list users: %w", err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e userListEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode user entry: %w", err)
				}
				rows = append(rows, []string{
					e.Userid, e.Enable.cell(), intCell(e.Expire),
					e.Firstname, e.Lastname, e.Email, e.Comment, e.Groups,
				})
			}

			result := output.Result{
				Headers: []string{"USERID", "ENABLE", "EXPIRE", "FIRSTNAME", "LASTNAME", "EMAIL", "COMMENT", "GROUPS"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&enabled, "enabled", false, "only list enabled users")
	cmd.Flags().BoolVar(&full, "full", false, "include group and token information")
	return cmd
}

// newUserGetCmd builds `pmx access user get <userid>`.
func newUserGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <userid>",
		Short:   "Show a user's details",
		Long:    "Show one user's enable state, expiration, name, email, comment, and groups.",
		Example: `  pmx pve access user get alice@pve`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.API.Access.GetUsers(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("get user %q: %w", userid, err)
			}

			groups := ""
			if len(resp.Groups) > 0 {
				groups = joinComma(resp.Groups)
			}
			single := map[string]string{
				"USERID":    userid,
				"ENABLE":    pveBoolCell(resp.Enable),
				"EXPIRE":    intCell((*int64)(resp.Expire)),
				"FIRSTNAME": strVal(resp.Firstname),
				"LASTNAME":  strVal(resp.Lastname),
				"EMAIL":     strVal(resp.Email),
				"COMMENT":   strVal(resp.Comment),
				"GROUPS":    groups,
			}

			result := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newUserCreateCmd builds `pmx access user create <userid>`.
func newUserCreateCmd() *cobra.Command {
	var (
		password, firstname, lastname, email, groups, comment, keys string
		expire                                                      int64
		enable                                                      bool
	)
	cmd := &cobra.Command{
		Use:   "create <userid>",
		Short: "Create a user",
		Long: "Create a new user in the given userid's realm (name@realm). The account is " +
			"enabled by default; pass --enable=false to create it disabled.",
		Example: `  pmx pve access user create alice@pve --email alice@example.com
  pmx pve access user create bob@pve --groups admins,ops --comment "Ops team"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			params := &access.CreateUsersParams{Userid: userid}
			setIfChanged(cmd, "password", &params.Password, password)
			setIfChanged(cmd, "firstname", &params.Firstname, firstname)
			setIfChanged(cmd, "lastname", &params.Lastname, lastname)
			setIfChanged(cmd, "email", &params.Email, email)
			setIfChanged(cmd, "groups", &params.Groups, groups)
			setIfChanged(cmd, "comment", &params.Comment, comment)
			setIfChanged(cmd, "keys", &params.Keys, keys)
			if cmd.Flags().Changed("expire") {
				params.Expire = &expire
			}
			if cmd.Flags().Changed("enable") {
				params.Enable = &enable
			}

			if err := deps.API.Access.CreateUsers(cmd.Context(), params); err != nil {
				return fmt.Errorf("create user %q: %w", userid, err)
			}

			result := output.Result{Message: fmt.Sprintf("User '%s' created.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "initial password")
	cmd.Flags().StringVar(&firstname, "firstname", "", "first name")
	cmd.Flags().StringVar(&lastname, "lastname", "", "last name")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().StringVar(&groups, "groups", "", "comma-separated group list")
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	cmd.Flags().BoolVar(&enable, "enable", true, "enable the account")
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	cmd.Flags().StringVar(&keys, "keys", "", "two-factor (yubico) keys")
	return cmd
}

// newUserSetCmd builds `pmx access user set <userid>`.
func newUserSetCmd() *cobra.Command {
	var (
		firstname, lastname, email, groups, comment, keys string
		expire                                            int64
		enable, appendGroups                              bool
	)
	cmd := &cobra.Command{
		Use:   "set <userid>",
		Short: "Update a user",
		Long: "Update a user's profile fields. Only the flags you pass are changed. " +
			"--groups replaces the user's group membership unless --append is also passed, " +
			"which merges the given groups into the existing membership instead.",
		Example: `  pmx pve access user set alice@pve --email alice@newdomain.example
  pmx pve access user set alice@pve --groups ops --append
  pmx pve access user set alice@pve --enable=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			params := &access.UpdateUsersParams{}
			setIfChanged(cmd, "firstname", &params.Firstname, firstname)
			setIfChanged(cmd, "lastname", &params.Lastname, lastname)
			setIfChanged(cmd, "email", &params.Email, email)
			setIfChanged(cmd, "groups", &params.Groups, groups)
			setIfChanged(cmd, "comment", &params.Comment, comment)
			setIfChanged(cmd, "keys", &params.Keys, keys)
			if cmd.Flags().Changed("expire") {
				params.Expire = &expire
			}
			if cmd.Flags().Changed("enable") {
				params.Enable = &enable
			}
			if cmd.Flags().Changed("append") {
				params.Append = &appendGroups
			}

			if err := deps.API.Access.UpdateUsers(cmd.Context(), userid, params); err != nil {
				return fmt.Errorf("update user %q: %w", userid, err)
			}

			result := output.Result{Message: fmt.Sprintf("User '%s' updated.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&firstname, "firstname", "", "first name")
	cmd.Flags().StringVar(&lastname, "lastname", "", "last name")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().StringVar(&groups, "groups", "", "comma-separated group list")
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	cmd.Flags().BoolVar(&enable, "enable", true, "enable the account")
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	cmd.Flags().StringVar(&keys, "keys", "", "two-factor (yubico) keys")
	cmd.Flags().BoolVar(&appendGroups, "append", false, "merge --groups into existing membership instead of replacing")
	return cmd
}

// newUserDeleteCmd builds `pmx access user delete <userid>`.
func newUserDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <userid>",
		Short:   "Delete a user",
		Long:    "Delete a user account. Refuses to run without --yes/-y.",
		Example: `  pmx pve access user delete alice@pve --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete user %q without --yes/-y", userid)
			}

			if err := deps.API.Access.DeleteUsers(cmd.Context(), userid); err != nil {
				return fmt.Errorf("delete user %q: %w", userid, err)
			}

			result := output.Result{Message: fmt.Sprintf("User '%s' deleted.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion")
	return cmd
}

// setIfChanged assigns *dst = &val when the named flag was explicitly set.
func setIfChanged(cmd *cobra.Command, name string, dst **string, val string) {
	if cmd.Flags().Changed(name) {
		v := val
		*dst = &v
	}
}

// joinComma joins a string slice with commas without importing strings.
func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
