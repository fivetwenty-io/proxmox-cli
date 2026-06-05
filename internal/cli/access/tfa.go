package access

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newTfaCmd builds `pve access tfa` and its sub-commands for administering the
// two-factor authentication entries registered for users. Entry enrollment is a
// browser-interactive TOTP/WebAuthn flow and is intentionally not surfaced here;
// the CLI covers inspection, removal, and unlocking a locked-out user.
func newTfaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tfa",
		Short: "Administer two-factor authentication entries",
		Long: "List configured two-factor authentication entries, inspect a user's " +
			"entries, delete an entry, and unlock a user locked out by failed " +
			"second-factor attempts.",
	}
	cmd.AddCommand(
		newTfaListCmd(),
		newTfaGetCmd(),
		newTfaDeleteCmd(),
		newTfaUnlockCmd(),
	)
	return cmd
}

// tfaUserEntry is a single row of GET /access/tfa: a user and their registered
// entries. Only the stable columns are decoded; `tfa get` shows the entries.
type tfaUserEntry struct {
	Userid  string            `json:"userid"`
	Entries []json.RawMessage `json:"entries"`
}

// newTfaListCmd builds `pve access tfa list`.
func newTfaListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List users with two-factor authentication entries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			resp, err := deps.API.Access.ListTfa(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tfa: %w", err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e tfaUserEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode tfa entry: %w", err)
				}
				rows = append(rows, []string{e.Userid, strconv.Itoa(len(e.Entries))})
			}

			result := output.Result{
				Headers: []string{"USERID", "ENTRIES"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// tfaEntry is a single two-factor entry as returned by GET /access/tfa/{userid}.
type tfaEntry struct {
	Id          string  `json:"id"`
	Type        string  `json:"type"`
	Description string  `json:"description,omitempty"`
	Created     int64   `json:"created,omitempty"`
	Enable      pveBool `json:"enable,omitempty"`
}

// newTfaGetCmd builds `pve access tfa get <userid>`, listing one user's entries.
func newTfaGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <userid>",
		Short: "List a user's two-factor authentication entries",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			userid := args[0]

			resp, err := deps.API.Access.GetTfa(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("get tfa for %q: %w", userid, err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e tfaEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode tfa entry: %w", err)
				}
				created := ""
				if e.Created != 0 {
					created = strconv.FormatInt(e.Created, 10)
				}
				rows = append(rows, []string{e.Id, e.Type, e.Description, e.Enable.cell(), created})
			}

			result := output.Result{
				Headers: []string{"ID", "TYPE", "DESCRIPTION", "ENABLE", "CREATED"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newTfaDeleteCmd builds `pve access tfa delete <userid> <id>`.
func newTfaDeleteCmd() *cobra.Command {
	var yes bool
	var password string
	cmd := &cobra.Command{
		Use:   "delete <userid> <id>",
		Short: "Delete a user's two-factor authentication entry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			userid, id := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to delete tfa entry %q for %q without --yes/-y", id, userid)
			}

			params := &access.DeleteTfaParams{}
			setIfChanged(cmd, "password", &params.Password, password)

			if err := deps.API.Access.DeleteTfa(cmd.Context(), userid, id, params); err != nil {
				return fmt.Errorf("delete tfa entry %q for %q: %w", id, userid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Deleted tfa entry '%s' for '%s'.", id, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion")
	cmd.Flags().StringVar(&password, "password", "", "current password of the user performing the change")
	return cmd
}

// newTfaUnlockCmd builds `pve access tfa unlock <userid>`, clearing the
// second-factor lockout a user incurs after repeated failed attempts.
func newTfaUnlockCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "unlock <userid>",
		Short: "Unlock a user locked out of two-factor authentication",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			userid := args[0]

			if !yes {
				return fmt.Errorf("refusing to unlock tfa for %q without --yes/-y", userid)
			}

			if _, err := deps.API.Access.UpdateUsersUnlockTfa(cmd.Context(), userid); err != nil {
				return fmt.Errorf("unlock tfa for %q: %w", userid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Unlocked tfa for '%s'.", userid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the unlock")
	return cmd
}
