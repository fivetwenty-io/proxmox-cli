package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newTfaCmd builds `pmx pdm tfa` — inspect and manage second-factor (TFA)
// entries configured for Proxmox Datacenter Manager users (/access/tfa...).
//
// Enrollment is deliberately out of scope: CreateTfa (POST
// /access/tfa/{userid}) requires an interactive challenge/response
// round-trip (a TOTP code, a WebAuthn/U2F browser ceremony, or a delivered
// recovery-code batch) that a one-shot CLI invocation cannot carry out — the
// same class of exclusion as this task's pbs-analog interactive-flow
// omissions. GetTfa2 (GET /access/tfa/{userid}/{id}) is also excluded:
// 'tfa show <userid>' already surfaces every entry for a user via GetTfa, so
// a single-entry fetch adds no capability.
func newTfaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tfa",
		Short: "Manage user two-factor authentication entries",
		Long: "Inspect, update the description of, and delete two-factor authentication " +
			"(TFA) entries configured for Proxmox Datacenter Manager users. Enrolling a " +
			"new TFA entry requires an interactive challenge/response round-trip and is " +
			"not supported by this CLI.",
	}
	cmd.AddCommand(newTfaLsCmd(), newTfaShowCmd(), newTfaUpdateCmd(), newTfaDeleteCmd())
	return cmd
}

// tfaEntryItem is the decoded shape of one TFA entry, per pdm-apidoc.json's
// GET /access/tfa/{userid} returns.items schema — the same shape is also
// embedded in GET /access/tfa's per-user "entries" array. access_gen.go's
// GetTfa2Response declares the same fields for the single-entry GET, which
// this task excludes (see newTfaCmd's doc comment); GetTfa and ListTfa
// themselves only declare the outer []json.RawMessage, so the per-item shape
// here is taken directly from the API spec.
type tfaEntryItem struct {
	Created     int64  `json:"created"`
	Description string `json:"description"`
	Enable      bool   `json:"enable"`
	Id          string `json:"id"`
	Type        string `json:"type"`
}

// tfaUserEntry is the decoded shape of one element of GET /access/tfa: a
// per-user tuple of TFA entries plus lockout state, per pdm-apidoc.json's
// returns.items schema.
type tfaUserEntry struct {
	Entries        []tfaEntryItem `json:"entries"`
	TfaLockedUntil *int64         `json:"tfa-locked-until,omitempty"`
	TotpLocked     bool           `json:"totp-locked"`
	Userid         string         `json:"userid"`
}

// newTfaLsCmd builds `pmx pdm tfa ls` — list every user's TFA configuration
// (GET /access/tfa).
func newTfaLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List TFA configuration for every user",
		Long: "List every user's two-factor authentication configuration (GET " +
			"/access/tfa): lockout state and a summary of their TFA entries. Use " +
			"'tfa show <userid>' for the full per-entry detail of a single user.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Access.ListTfa(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tfa: %w", err)
			}

			items := rawItemsOf(resp)
			type tfaRow struct {
				entry tfaUserEntry
				raw   map[string]any
			}
			table := make([]tfaRow, 0, len(items))

			for _, raw := range items {
				var e tfaUserEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tfa entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode tfa entry: %w", err)
				}

				table = append(table, tfaRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Userid < table[j].entry.Userid })

			headers := []string{"USERID", "TOTP-LOCKED", "TFA-LOCKED-UNTIL", "ENTRIES"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				ids := make([]string, 0, len(e.Entries))
				for _, entry := range e.Entries {
					ids = append(ids, entry.Type+":"+entry.Id)
				}
				rows = append(rows, []string{
					e.Userid, strconv.FormatBool(e.TotpLocked), int64PtrString(e.TfaLockedUntil), strings.Join(ids, ","),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTfaShowCmd builds `pmx pdm tfa show <userid>` — list a single user's
// TFA entries (GET /access/tfa/{userid}).
func newTfaShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <userid>",
		Short: "List a user's TFA entries",
		Long:  "List every two-factor authentication entry configured for a user (GET /access/tfa/{userid}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.PDM.Access.GetTfa(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("get tfa entries for user %q: %w", userid, err)
			}

			items := rawItemsOf(resp)
			type tfaEntryRow struct {
				entry tfaEntryItem
				raw   map[string]any
			}
			table := make([]tfaEntryRow, 0, len(items))

			for _, raw := range items {
				var e tfaEntryItem

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tfa entry for user %q: %w", userid, err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode tfa entry for user %q: %w", userid, err)
				}

				table = append(table, tfaEntryRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Id < table[j].entry.Id })

			headers := []string{"ID", "TYPE", "DESCRIPTION", "ENABLE", "CREATED"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Id, e.Type, e.Description, strconv.FormatBool(e.Enable), strconv.FormatInt(e.Created, 10),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTfaUpdateCmd builds `pmx pdm tfa update <userid> <id>` — update a TFA
// entry's description (PUT /access/tfa/{userid}/{id}). UpdateTfaParams
// (access_gen.go) also accepts an --enable toggle, but this command only
// exposes --description, per this task's brief ("description only"):
// enabling/disabling a second factor is a security-sensitive action better
// made explicit through a dedicated flow than folded into a generic update.
func newTfaUpdateCmd() *cobra.Command {
	var description, password string
	cmd := &cobra.Command{
		Use:   "update <userid> <id>",
		Short: "Update a TFA entry's description",
		Long: "Update the description of a user's two-factor authentication entry " +
			"(PUT /access/tfa/{userid}/{id}). --password supplies the operator's own " +
			"password for re-authentication, if the server requires it for this change.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, id := args[0], args[1]

			if !cmd.Flags().Changed("description") {
				return fmt.Errorf("update tfa entry %q for user %q: no changes given: pass --description", id, userid)
			}

			params := &pdmaccess.UpdateTfaParams{Description: strPtr(description)}
			if cmd.Flags().Changed("password") {
				params.Password = strPtr(password)
			}

			err := deps.PDM.Access.UpdateTfa(cmd.Context(), userid, id, params)
			if err != nil {
				return fmt.Errorf("update tfa entry %q for user %q: %w", id, userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("TFA entry %q for user %q updated.", id, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&description, "description", "", "new description for the entry (required)")
	f.StringVar(&password, "password", "", "the operator's own password, for re-authentication if the server requires it")
	return cmd
}

// newTfaDeleteCmd builds `pmx pdm tfa delete <userid> <id>` — remove a TFA
// entry (DELETE /access/tfa/{userid}/{id}).
func newTfaDeleteCmd() *cobra.Command {
	var (
		password string
		yes      bool
	)
	cmd := &cobra.Command{
		Use:   "delete <userid> <id>",
		Short: "Delete a TFA entry",
		Long: "Remove a single two-factor authentication entry from a user (DELETE " +
			"/access/tfa/{userid}/{id}). This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, id := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to delete tfa entry %q for user %q without confirmation: pass --yes/-y",
					id, userid)
			}

			params := &pdmaccess.DeleteTfaParams{}
			if cmd.Flags().Changed("password") {
				params.Password = strPtr(password)
			}

			err := deps.PDM.Access.DeleteTfa(cmd.Context(), userid, id, params)
			if err != nil {
				return fmt.Errorf("delete tfa entry %q for user %q: %w", id, userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("TFA entry %q for user %q deleted.", id, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&password, "password", "", "the operator's own password, for re-authentication if the server requires it")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
