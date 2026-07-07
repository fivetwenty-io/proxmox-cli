package access

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// validTfaTypes is the exhaustive set of PVE TFA entry types accepted by
// POST /access/tfa/{userid}. Used for --type validation.
var validTfaTypes = map[string]bool{
	"totp":     true,
	"webauthn": true,
	"recovery": true,
	"yubico":   true,
}

// newTfaCmd builds `pve access tfa` and its sub-commands for administering the
// two-factor authentication entries registered for users.
func newTfaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tfa",
		Short: "Administer two-factor authentication entries",
		Long: "List configured two-factor authentication entries, inspect a user's " +
			"entries, create an entry, update or delete an entry, and unlock a " +
			"user locked out by failed second-factor attempts.",
	}
	cmd.AddCommand(
		newTfaListCmd(),
		newTfaGetCmd(),
		newTfaGetEntryCmd(),
		newTfaCreateCmd(),
		newTfaSetCmd(),
		newTfaDeleteCmd(),
		newTfaUnlockCmd(),
		newTfaTypesCmd(),
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
			deps := cli.GetDeps(cmd)

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
			deps := cli.GetDeps(cmd)
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
			deps := cli.GetDeps(cmd)
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
			deps := cli.GetDeps(cmd)
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

// newTfaGetEntryCmd builds `pve access tfa get-entry <userid> <id>`, retrieving
// a single TFA entry by its entry ID (GET /access/tfa/{userid}/{id}).
func newTfaGetEntryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-entry <userid> <id>",
		Short: "Get a single two-factor authentication entry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, id := args[0], args[1]

			resp, err := deps.API.Access.GetTfa2(cmd.Context(), userid, id)
			if err != nil {
				return fmt.Errorf("get tfa entry %q for %q: %w", id, userid, err)
			}

			created := ""
			if resp.Created != 0 {
				created = strconv.FormatInt(resp.Created, 10)
			}

			single := map[string]string{
				"ID":          resp.Id,
				"TYPE":        resp.Type,
				"DESCRIPTION": resp.Description,
				"ENABLE":      pveBoolCell(resp.Enable),
				"CREATED":     created,
			}
			result := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newTfaCreateCmd builds `pve access tfa create <userid>`, enrolling a new TFA
// entry (POST /access/tfa/{userid}).
//
// Security note: --value and --totp accept credential material. The operator's
// own password (--password) is required by PVE when modifying TFA. Neither flag
// value is logged. If --password is not supplied, the command reads it from
// stdin without echo so it never appears in process args or shell history.
// The response may contain recovery codes; these are printed to stdout once so
// the user can save them — that is intentional output, not a secret leak.
func newTfaCreateCmd() *cobra.Command {
	var (
		tfaType     string
		description string
		totp        string
		value       string
		challenge   string
		password    string
	)
	cmd := &cobra.Command{
		Use:   "create <userid>",
		Short: "Enroll a new two-factor authentication entry for a user",
		Long: "Enroll a new TFA entry (totp, webauthn, recovery, or yubico). " +
			"The operator must supply their own --password (or be prompted) as " +
			"required by the PVE API. --value and --totp accept credential material " +
			"and are never logged.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			if tfaType == "" {
				return fmt.Errorf("--type is required (totp, webauthn, recovery, yubico)")
			}
			if !validTfaTypes[strings.ToLower(tfaType)] {
				return fmt.Errorf("invalid --type %q: must be one of totp, webauthn, recovery, yubico", tfaType)
			}

			// Require the operator's current password. Prompt from stdin if omitted
			// so it never appears in shell history.
			if password == "" {
				prompted, err := promptPassword(cmd)
				if err != nil {
					return err
				}
				password = prompted
			}
			if password == "" {
				return fmt.Errorf("--password (or prompted password) is required by the PVE API to enroll TFA")
			}

			params := &access.CreateTfaParams{
				Type:     tfaType,
				Password: &password,
			}
			setIfChanged(cmd, "description", &params.Description, description)
			setIfChanged(cmd, "totp", &params.Totp, totp)
			setIfChanged(cmd, "value", &params.Value, value)
			setIfChanged(cmd, "challenge", &params.Challenge, challenge)

			resp, err := deps.API.Access.CreateTfa(cmd.Context(), userid, params)
			if err != nil {
				return fmt.Errorf("create tfa entry for %q: %w", userid, err)
			}

			// Recovery codes are legitimate output — the user must save them.
			// They are printed in the table/JSON result, not logged separately.
			recovery := strings.Join(resp.Recovery, "\n")
			challengeOut := ""
			if resp.Challenge != nil {
				challengeOut = *resp.Challenge
			}

			result := output.Result{
				Headers: []string{"ID", "CHALLENGE", "RECOVERY"},
				Rows:    [][]string{{resp.Id, challengeOut, recovery}},
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&tfaType, "type", "", "TFA entry type: totp, webauthn, recovery, or yubico (required)")
	cmd.Flags().StringVar(&description, "description", "", "description to distinguish entries")
	cmd.Flags().StringVar(&totp, "totp", "", "TOTP URI (totp type)")
	cmd.Flags().StringVar(&value, "value", "", "current TOTP value or WebAuthn/U2F challenge response")
	cmd.Flags().StringVar(&challenge, "challenge", "", "original U2F challenge string (when responding to a challenge)")
	cmd.Flags().StringVar(&password, "password", "", "current password of the user performing the change (prompts if absent)")
	return cmd
}

// newTfaSetCmd builds `pve access tfa set <userid> <id>`, updating a TFA entry
// (PUT /access/tfa/{userid}/{id}). Only flags explicitly passed are forwarded;
// unset optional flags are omitted so partial updates work correctly.
//
// Security note: --password accepts credential material and is never logged.
// If omitted, the command prompts from stdin without echo.
func newTfaSetCmd() *cobra.Command {
	var (
		description string
		enable      bool
		password    string
	)
	cmd := &cobra.Command{
		Use:   "set <userid> <id>",
		Short: "Update a two-factor authentication entry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, id := args[0], args[1]

			// At least one field must be changed; otherwise the call is a no-op.
			descChanged := cmd.Flags().Changed("description")
			enableChanged := cmd.Flags().Changed("enable")
			passwdChanged := cmd.Flags().Changed("password")

			if !descChanged && !enableChanged && !passwdChanged {
				return fmt.Errorf("at least one of --description, --enable, or --password must be specified")
			}

			// Require the operator's current password when making changes. The PVE
			// API enforces this server-side; we surface the prompt for usability.
			if password == "" {
				prompted, err := promptPassword(cmd)
				if err != nil {
					return err
				}
				password = prompted
			}
			if password == "" {
				return fmt.Errorf("--password (or prompted password) is required by the PVE API to modify TFA")
			}

			params := &access.UpdateTfaParams{
				Password: &password,
			}
			setIfChanged(cmd, "description", &params.Description, description)
			if enableChanged {
				params.Enable = &enable
			}

			if err := deps.API.Access.UpdateTfa(cmd.Context(), userid, id, params); err != nil {
				return fmt.Errorf("update tfa entry %q for %q: %w", id, userid, err)
			}

			result := output.Result{Message: fmt.Sprintf("TFA entry '%s' for '%s' updated.", id, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "description to distinguish entries")
	cmd.Flags().BoolVar(&enable, "enable", true, "enable or disable this TFA entry")
	cmd.Flags().StringVar(&password, "password", "", "current password of the user performing the change (prompts if absent)")
	return cmd
}

// newTfaTypesCmd builds `pve access tfa types <userid>`, returning the TFA type
// summary for a user via GET /access/users/{userid}/tfa.
func newTfaTypesCmd() *cobra.Command {
	var multiple bool
	cmd := &cobra.Command{
		Use:   "types <userid>",
		Short: "Show TFA type summary for a user",
		Long: "Return the TFA type summary for a user from GET /access/users/{userid}/tfa. " +
			"Without --multiple, returns realm-level type, user-level type, and any " +
			"configured entry types. With --multiple, returns all entries as an array.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			params := &access.ListUsersTfaParams{}
			if cmd.Flags().Changed("multiple") {
				params.Multiple = &multiple
			}

			resp, err := deps.API.Access.ListUsersTfa(cmd.Context(), userid, params)
			if err != nil {
				return fmt.Errorf("list tfa types for %q: %w", userid, err)
			}

			realmVal := ""
			if resp.Realm != nil {
				realmVal = *resp.Realm
			}
			userVal := ""
			if resp.User != nil {
				userVal = *resp.User
			}
			typesVal := strings.Join(resp.Types, ",")

			single := map[string]string{
				"USERID": userid,
				"REALM":  realmVal,
				"USER":   userVal,
				"TYPES":  typesVal,
			}
			result := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&multiple, "multiple", false, "request all entries as an array")
	return cmd
}
