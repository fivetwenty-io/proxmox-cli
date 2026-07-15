package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pbsaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newUserCmd builds `pmx pbs user` and its sub-commands: list, inspect,
// create, update, and delete Proxmox Backup Server users; unlock a user's
// two-factor authentication; change a user's password; and manage a user's
// API tokens (GET/POST/PUT/DELETE /access/users and PUT /access/password).
func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage Proxmox Backup Server users",
		Long: "List, inspect, create, update, and delete Proxmox Backup Server users, " +
			"unlock a user's two-factor authentication, change a user's password, and " +
			"manage a user's API tokens.",
	}
	cmd.AddCommand(
		newUserLsCmd(),
		newUserShowCmd(),
		newUserAddCmd(),
		newUserUpdateCmd(),
		newUserDeleteCmd(),
		newUserUnlockTfaCmd(),
		newUserPasswdCmd(),
		newUserTokenCmd(),
	)
	return cmd
}

// userFormatEnable renders an optional PVEBool "enable" field for table
// cells. The PBS API defaults an absent "enable" field to true, so nil
// renders as "true" rather than being mistaken for a disabled account.
func userFormatEnable(b *pve.PVEBool) string {
	if b == nil {
		return "true"
	}

	if b.Bool() {
		return "true"
	}

	return "false"
}

// userListEntry is the decoded shape of one element of GET /access/users.
type userListEntry struct {
	Comment   *string      `json:"comment,omitempty"`
	Email     *string      `json:"email,omitempty"`
	Enable    *pve.PVEBool `json:"enable,omitempty"`
	Expire    *int64       `json:"expire,omitempty"`
	Firstname *string      `json:"firstname,omitempty"`
	Lastname  *string      `json:"lastname,omitempty"`
	Userid    string       `json:"userid"`
}

// newUserLsCmd builds `pmx pbs user ls` — list configured users
// (GET /access/users).
func newUserLsCmd() *cobra.Command {
	var includeTokens bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List users",
		Long: "List the users configured on this Proxmox Backup Server (GET " +
			"/access/users). Pass --include-tokens to include each user's API tokens " +
			"in the raw (JSON/YAML) output; the table view never shows token details.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsaccess.ListUsersParams{}
			if cmd.Flags().Changed("include-tokens") {
				params.IncludeTokens = boolPtr(includeTokens)
			}

			resp, err := deps.PBS.Access.ListUsers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list users: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]userListEntry, 0, len(items))

			for _, raw := range items {
				var e userListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode user entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Userid < entries[j].Userid })

			headers := []string{"USERID", "ENABLE", "EXPIRE", "FIRSTNAME", "LASTNAME", "EMAIL", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Userid, userFormatEnable(e.Enable), pbsFormatOptionalInt64(e.Expire),
					pbsFormatOptionalString(e.Firstname), pbsFormatOptionalString(e.Lastname),
					pbsFormatOptionalString(e.Email), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&includeTokens, "include-tokens", false, "include each user's API tokens in the raw response")
	return cmd
}

// newUserShowCmd builds `pmx pbs user show <userid>` — show a single user's
// configuration (GET /access/users/{userid}).
func newUserShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <userid>",
		Short: "Show a user's configuration",
		Long:  "Show every populated field of a single user's configuration (GET /access/users/{userid}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.PBS.Access.GetUsers(cmd.Context(), userid)
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

// newUserAddCmd builds `pmx pbs user add <userid>` — create a user
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
		Long: "Create a new Proxmox Backup Server user (POST /access/users). Every " +
			"flag beside the userid argument is optional and only forwarded when " +
			"explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			params := &pbsaccess.CreateUsersParams{Userid: userid}

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

			err := deps.PBS.Access.CreateUsers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create user %q: %w", userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("User %q created.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().BoolVar(&enable, "enable", true, "enable the account")
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	cmd.Flags().StringVar(&firstname, "firstname", "", "first name")
	cmd.Flags().StringVar(&lastname, "lastname", "", "last name")
	cmd.Flags().StringVar(&password, "password", "", "initial password")
	return cmd
}

// newUserUpdateCmd builds `pmx pbs user update <userid>` — update a user's
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
		Long: "Update an existing Proxmox Backup Server user (PUT /access/users/{userid}). " +
			"Only flags explicitly set are sent; use --delete to reset properties to their " +
			"default instead. --password is accepted by the API but ignored server-side; " +
			"use 'pmx pbs user passwd' to change a password.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update user %q: no changes requested: pass at least one flag", userid)
			}

			if fl.Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsaccess.UpdateUsersParams{}
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

			err := deps.PBS.Access.UpdateUsers(cmd.Context(), userid, params)
			if err != nil {
				return fmt.Errorf("update user %q: %w", userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("User %q updated.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	cmd.Flags().StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().BoolVar(&enable, "enable", true, "enable the account")
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	cmd.Flags().StringVar(&firstname, "firstname", "", "first name")
	cmd.Flags().StringVar(&lastname, "lastname", "", "last name")
	cmd.Flags().StringVar(&password, "password", "", "ignored by the server; use 'pmx pbs user passwd' instead")
	return cmd
}

// newUserDeleteCmd builds `pmx pbs user delete <userid>` — remove a user
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

			params := &pbsaccess.DeleteUsersParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Access.DeleteUsers(cmd.Context(), userid, params)
			if err != nil {
				return fmt.Errorf("delete user %q: %w", userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("User %q deleted.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newUserUnlockTfaCmd builds `pmx pbs user unlock-tfa <userid>` — clear a
// user's TFA lockout after too many failed attempts
// (PUT /access/users/{userid}/unlock-tfa).
func newUserUnlockTfaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unlock-tfa <userid>",
		Short: "Unlock a user's two-factor authentication",
		Long: "Clear a user's two-factor authentication lockout after too many failed " +
			"attempts (PUT /access/users/{userid}/unlock-tfa).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.PBS.Access.UpdateUsersUnlockTfa(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("unlock tfa for user %q: %w", userid, err)
			}

			wasLocked := false
			if resp != nil && len(*resp) > 0 {
				err := json.Unmarshal(*resp, &wasLocked)
				if err != nil {
					return fmt.Errorf("decode unlock-tfa response for user %q: %w", userid, err)
				}
			}

			msg := fmt.Sprintf("User %q was not TFA-locked; nothing to unlock.", userid)
			if wasLocked {
				msg = fmt.Sprintf("TFA unlocked for user %q.", userid)
			}

			res := output.Result{
				Message: msg,
				Raw:     map[string]any{"userid": userid, "was_locked": wasLocked},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newUserPasswdCmd builds `pmx pbs user passwd <userid> --password <pw>` —
// change a user's password (PUT /access/password).
func newUserPasswdCmd() *cobra.Command {
	var password, confirmationPassword string
	cmd := &cobra.Command{
		Use:   "passwd <userid>",
		Short: "Change a user's password",
		Long: "Change a user's password (PUT /access/password). Any user may change " +
			"their own password; the superuser may change any password. --password is " +
			"required. --confirmation-password supplies the operator's current " +
			"password, required unless logged in as root@pam.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			if password == "" {
				return fmt.Errorf("--password is required")
			}

			params := &pbsaccess.UpdatePasswordParams{Userid: userid, Password: password}
			if cmd.Flags().Changed("confirmation-password") {
				params.ConfirmationPassword = strPtr(confirmationPassword)
			}

			err := deps.PBS.Access.UpdatePassword(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("change password for user %q: %w", userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Password for user %q changed.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "new password (required)")
	cmd.Flags().StringVar(&confirmationPassword, "confirmation-password", "",
		"current password of the operator performing the change; required unless logged in as root@pam")
	cli.MustMarkRequired(cmd, "password")
	return cmd
}
