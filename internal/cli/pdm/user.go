package pdm

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newUserCmd builds `pmx pdm user` — manage the users configured on this
// Proxmox Datacenter Manager instance (/access/users). API tokens belonging
// to a user are managed separately via `pmx pdm token`.
func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage Proxmox Datacenter Manager users",
		Long: "List, inspect, create, update, and delete the users configured on this " +
			"Proxmox Datacenter Manager instance. API tokens belonging to a user are " +
			"managed separately via 'pmx pdm token'.",
	}
	cmd.AddCommand(
		newUserLsCmd(),
		newUserShowCmd(),
		newUserAddCmd(),
		newUserUpdateCmd(),
		newUserDeleteCmd(),
	)
	return cmd
}

// userFormatEnable renders an "enable" value pulled from a raw decoded map
// for a table cell. Both users and their tokens default an absent "enable"
// key to true (pdm-apidoc.json GET /access/users and GET
// /access/users/{userid}/token returns.items schemas), so a nil value
// renders as "true" rather than being mistaken for a disabled account/token.
func userFormatEnable(v any) string {
	if v == nil {
		return "true"
	}
	return scalarString(v)
}

// userListEntry is the decoded shape of one element of GET /access/users.
// access_gen.go's GetUsersResponse (used by 'user show') declares only
// comment/email/enable/expire/firstname/lastname/userid, but the list
// endpoint's per-item schema additionally carries tokens/totp-locked/
// tfa-locked-until (pdm-apidoc.json GET /access/users returns.items). Only
// the guaranteed-present Userid is typed here; every other field is read
// from the raw decoded map via scalarString, mirroring ceph.go's
// cephClusterEntry convention.
type userListEntry struct {
	Userid string `json:"userid"`
}

// newUserLsCmd builds `pmx pdm user ls` — list configured users
// (GET /access/users).
func newUserLsCmd() *cobra.Command {
	var includeTokens bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List users",
		Long: "List the users configured on this Proxmox Datacenter Manager instance " +
			"(GET /access/users). Pass --include-tokens to include each user's API " +
			"tokens in the raw (JSON/YAML) output; the table view never shows token details.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmaccess.ListUsersParams{}
			if cmd.Flags().Changed("include-tokens") {
				params.IncludeTokens = boolPtr(includeTokens)
			}

			resp, err := deps.PDM.Access.ListUsers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list users: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[userListEntry](items, "user")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Userid < table[j].Entry.Userid })

			headers := []string{"USERID", "ENABLE", "EXPIRE", "FIRSTNAME", "LASTNAME", "EMAIL", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				m := t.Raw
				rows = append(rows, []string{
					t.Entry.Userid, userFormatEnable(m["enable"]), scalarString(m["expire"]),
					scalarString(m["firstname"]), scalarString(m["lastname"]),
					scalarString(m["email"]), scalarString(m["comment"]),
				})
				raws = append(raws, m)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&includeTokens, "include-tokens", false, "include each user's API tokens in the raw response")
	return cmd
}

// newUserShowCmd builds `pmx pdm user show <userid>` — show a single user's
// configuration (GET /access/users/{userid}).
func newUserShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <userid>",
		Short: "Show a user's configuration",
		Long: "Show every populated field of a single user's configuration (GET " +
			"/access/users/{userid}). This endpoint never returns password material, " +
			"so there is no secret field to strip.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.PDM.Access.GetUsers(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("get user %q: %w", userid, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode user %q: %w", userid, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newUserAddCmd builds `pmx pdm user add <userid>` — create a user
// (POST /access/users).
func newUserAddCmd() *cobra.Command {
	var (
		comment, email, firstname, lastname, password string
		expire                                        int64
		enable                                        bool
	)
	cmd := &cobra.Command{
		Use:   "add <userid>",
		Short: "Create a user",
		Long: "Create a new Proxmox Datacenter Manager user (POST /access/users). " +
			"Every flag beside the userid argument is optional and only forwarded " +
			"when explicitly set. --password is never echoed back by any command.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			params := &pdmaccess.CreateUsersParams{Userid: userid}

			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}

			if fl.Changed("email") {
				params.Email = strPtr(email)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(enable)
			}

			if fl.Changed("expire") {
				params.Expire = int64Ptr(expire)
			}

			if fl.Changed("firstname") {
				params.Firstname = strPtr(firstname)
			}

			if fl.Changed("lastname") {
				params.Lastname = strPtr(lastname)
			}

			if fl.Changed("password") {
				params.Password = strPtr(password)
			}

			err := deps.PDM.Access.CreateUsers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create user %q: %w", userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("User %q created.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&comment, "comment", "", "comment")
	f.StringVar(&email, "email", "", "email address")
	f.BoolVar(&enable, "enable", true, "enable the account")
	f.Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	f.StringVar(&firstname, "firstname", "", "first name")
	f.StringVar(&lastname, "lastname", "", "last name")
	f.StringVar(&password, "password", "", "initial password")
	return cmd
}

// newUserUpdateCmd builds `pmx pdm user update <userid>` — update a user's
// configuration (PUT /access/users/{userid}).
func newUserUpdateCmd() *cobra.Command {
	var (
		comment, email, firstname, lastname, password, digest string
		del                                                   []string
		expire                                                int64
		enable                                                bool
	)
	cmd := &cobra.Command{
		Use:   "update <userid>",
		Short: "Update a user",
		Long: "Update an existing Proxmox Datacenter Manager user (PUT " +
			"/access/users/{userid}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update user %q: no changes requested: pass at least one flag", userid)
			}

			params := &pdmaccess.UpdateUsersParams{}
			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}

			if fl.Changed("delete") {
				params.Delete = del
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("email") {
				params.Email = strPtr(email)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(enable)
			}

			if fl.Changed("expire") {
				params.Expire = int64Ptr(expire)
			}

			if fl.Changed("firstname") {
				params.Firstname = strPtr(firstname)
			}

			if fl.Changed("lastname") {
				params.Lastname = strPtr(lastname)
			}

			if fl.Changed("password") {
				params.Password = strPtr(password)
			}

			err := deps.PDM.Access.UpdateUsers(cmd.Context(), userid, params)
			if err != nil {
				return fmt.Errorf("update user %q: %w", userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("User %q updated.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&comment, "comment", "", "comment")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	f.StringVar(&email, "email", "", "email address")
	f.BoolVar(&enable, "enable", true, "enable the account")
	f.Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	f.StringVar(&firstname, "firstname", "", "first name")
	f.StringVar(&lastname, "lastname", "", "last name")
	f.StringVar(&password, "password", "", "new password")
	return cmd
}

// newUserDeleteCmd builds `pmx pdm user delete <userid>` — remove a user
// (DELETE /access/users/{userid}).
func newUserDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <userid>",
		Short: "Delete a user",
		Long: "Remove a user from the configuration file (DELETE /access/users/{userid}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete user %q without confirmation: pass --yes/-y", userid)
			}

			params := &pdmaccess.DeleteUsersParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Access.DeleteUsers(cmd.Context(), userid, params)
			if err != nil {
				return fmt.Errorf("delete user %q: %w", userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("User %q deleted.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
