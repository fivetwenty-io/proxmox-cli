package access

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newTokenCmd builds `pve access user token` and its sub-commands.
func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage a user's API tokens",
		Long:  "List, inspect, create, update, and delete API tokens for a user.",
	}
	cmd.AddCommand(
		newTokenListCmd(),
		newTokenGetCmd(),
		newTokenCreateCmd(),
		newTokenSetCmd(),
		newTokenDeleteCmd(),
	)
	return cmd
}

// tokenListEntry is a single row of the GET /access/users/{userid}/token list.
type tokenListEntry struct {
	Tokenid string  `json:"tokenid"`
	Expire  *int64  `json:"expire,omitempty"`
	Privsep pveBool `json:"privsep,omitempty"`
	Comment string  `json:"comment,omitempty"`
}

// newTokenListCmd builds `pve access user token list <userid>`.
func newTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <userid>",
		Short: "List a user's API tokens",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.API.Access.ListUsersToken(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("list tokens for %q: %w", userid, err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e tokenListEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode token entry: %w", err)
				}
				rows = append(rows, []string{e.Tokenid, intCell(e.Expire), e.Privsep.cell(), e.Comment})
			}

			result := output.Result{
				Headers: []string{"TOKENID", "EXPIRE", "PRIVSEP", "COMMENT"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newTokenGetCmd builds `pve access user token get <userid> <tokenid>`.
func newTokenGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <userid> <tokenid>",
		Short: "Show an API token's details",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenid := args[0], args[1]

			resp, err := deps.API.Access.GetUsersToken(cmd.Context(), userid, tokenid)
			if err != nil {
				return fmt.Errorf("get token %q for %q: %w", tokenid, userid, err)
			}

			single := map[string]string{
				"TOKENID": tokenid,
				"EXPIRE":  intCell(resp.Expire),
				"PRIVSEP": pveBoolCell(resp.Privsep),
				"COMMENT": strVal(resp.Comment),
			}
			result := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newTokenCreateCmd builds `pve access user token create <userid> <tokenid>`.
// The returned secret value is printed once and is not persisted by the CLI.
func newTokenCreateCmd() *cobra.Command {
	var (
		comment string
		expire  int64
		privsep bool
	)
	cmd := &cobra.Command{
		Use:   "create <userid> <tokenid>",
		Short: "Create an API token",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenid := args[0], args[1]

			params := &access.CreateUsersTokenParams{}
			setIfChanged(cmd, "comment", &params.Comment, comment)
			if cmd.Flags().Changed("expire") {
				params.Expire = &expire
			}
			if cmd.Flags().Changed("privsep") {
				params.Privsep = &privsep
			}

			resp, err := deps.API.Access.CreateUsersToken(cmd.Context(), userid, tokenid, params)
			if err != nil {
				return fmt.Errorf("create token %q for %q: %w", tokenid, userid, err)
			}

			result := output.Result{
				Headers: []string{"TOKENID", "VALUE", "EXPIRE", "PRIVSEP"},
				Rows: [][]string{{
					resp.FullTokenid, resp.Value, intCell(params.Expire), boolCell(params.Privsep),
				}},
				Raw: resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	cmd.Flags().BoolVar(&privsep, "privsep", true, "restrict token privileges with separate ACLs")
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	return cmd
}

// newTokenSetCmd builds `pve access user token set <userid> <tokenid>`.
func newTokenSetCmd() *cobra.Command {
	var (
		comment string
		expire  int64
		privsep bool
	)
	cmd := &cobra.Command{
		Use:   "set <userid> <tokenid>",
		Short: "Update an API token",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenid := args[0], args[1]

			params := &access.UpdateUsersTokenParams{}
			setIfChanged(cmd, "comment", &params.Comment, comment)
			if cmd.Flags().Changed("expire") {
				params.Expire = &expire
			}
			if cmd.Flags().Changed("privsep") {
				params.Privsep = &privsep
			}

			if _, err := deps.API.Access.UpdateUsersToken(cmd.Context(), userid, tokenid, params); err != nil {
				return fmt.Errorf("update token %q for %q: %w", tokenid, userid, err)
			}

			result := output.Result{Message: "Token updated."}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	cmd.Flags().BoolVar(&privsep, "privsep", true, "restrict token privileges with separate ACLs")
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	return cmd
}

// newTokenDeleteCmd builds `pve access user token delete <userid> <tokenid>`.
func newTokenDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <userid> <tokenid>",
		Short: "Delete an API token",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenid := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to delete token %q without --yes/-y", tokenid)
			}

			if err := deps.API.Access.DeleteUsersToken(cmd.Context(), userid, tokenid); err != nil {
				return fmt.Errorf("delete token %q for %q: %w", tokenid, userid, err)
			}

			result := output.Result{Message: fmt.Sprintf("Token '%s' deleted.", tokenid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion")
	return cmd
}
