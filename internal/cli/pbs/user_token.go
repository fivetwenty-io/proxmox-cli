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

// newUserTokenCmd builds `pmx pbs user token` and its sub-commands: manage
// the API tokens belonging to a user (GET/POST/PUT/DELETE
// /access/users/{userid}/token...).
func newUserTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage a user's API tokens",
		Long:  "List, inspect, create, update, and delete API tokens belonging to a user.",
	}
	cmd.AddCommand(
		newUserTokenLsCmd(),
		newUserTokenShowCmd(),
		newUserTokenAddCmd(),
		newUserTokenUpdateCmd(),
		newUserTokenDeleteCmd(),
	)
	return cmd
}

// userTokenListEntry is the decoded shape of one element of
// GET /access/users/{userid}/token.
type userTokenListEntry struct {
	Comment *string      `json:"comment,omitempty"`
	Enable  *pve.PVEBool `json:"enable,omitempty"`
	Expire  *int64       `json:"expire,omitempty"`
	Tokenid string       `json:"tokenid"`
}

// newUserTokenLsCmd builds `pmx pbs user token ls <userid>` — list a user's
// API tokens (GET /access/users/{userid}/token).
func newUserTokenLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls <userid>",
		Short:   "List a user's API tokens",
		Long:    "List the API tokens belonging to a user (GET /access/users/{userid}/token).",
		Example: "  pmx pbs user token ls alice@pbs",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.PBS.Access.ListUsersToken(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("list tokens for user %q: %w", userid, err)
			}

			items := rawItemsOf(resp)
			entries := make([]userTokenListEntry, 0, len(items))

			for _, raw := range items {
				var e userTokenListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode token entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Tokenid < entries[j].Tokenid })

			headers := []string{"TOKENID", "ENABLE", "EXPIRE", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Tokenid, userFormatEnable(e.Enable), pbsFormatOptionalInt64(e.Expire), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newUserTokenShowCmd builds `pmx pbs user token show <userid> <token-name>`
// — show an API token's metadata (GET /access/users/{userid}/token/{token-name}).
// The token secret is never returned by this endpoint; it is only available
// once, at creation or regeneration time.
func newUserTokenShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <userid> <token-name>",
		Short: "Show an API token's metadata",
		Long: "Show every populated field of one API token's metadata (GET " +
			"/access/users/{userid}/token/{token-name}). The token secret is never " +
			"returned by this endpoint.",
		Example: "  pmx pbs user token show alice@pbs backup-token",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenName := args[0], args[1]

			resp, err := deps.PBS.Access.GetUsersToken(cmd.Context(), userid, tokenName)
			if err != nil {
				return fmt.Errorf("get token %q for user %q: %w", tokenName, userid, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode token %q for user %q: %w", tokenName, userid, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newUserTokenAddCmd builds `pmx pbs user token add <userid> <token-name>`
// — create an API token (POST /access/users/{userid}/token/{token-name}).
func newUserTokenAddCmd() *cobra.Command {
	var (
		comment, digest string
		expire          int64
		enable          bool
	)
	cmd := &cobra.Command{
		Use:   "add <userid> <token-name>",
		Short: "Create an API token",
		Long: "Generate a new API token for a user (POST /access/users/{userid}/token/{token-name}). " +
			"The response's VALUE column carries the token secret; it is shown only once " +
			"here and is never retrievable again.",
		Example: "  pmx pbs user token add alice@pbs backup-token --comment ci-token",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenName := args[0], args[1]

			params := &pbsaccess.CreateUsersTokenParams{}

			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(enable)
			}

			if fl.Changed("expire") {
				params.Expire = int64Ptr(expire)
			}

			resp, err := deps.PBS.Access.CreateUsersToken(cmd.Context(), userid, tokenName, params)
			if err != nil {
				return fmt.Errorf("create token %q for user %q: %w", tokenName, userid, err)
			}

			if resp == nil {
				return fmt.Errorf("create token %q for user %q: nil response from PBS", tokenName, userid)
			}

			res := output.Result{
				Headers: []string{"TOKENID", "VALUE"},
				Rows:    [][]string{{resp.Tokenid, resp.Value}},
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	cmd.Flags().StringVar(&digest, "digest", "", "only create if the current config digest matches")
	cmd.Flags().BoolVar(&enable, "enable", true, "enable the token")
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	return cmd
}

// newUserTokenUpdateCmd builds `pmx pbs user token update <userid>
// <token-name>` — update an API token's metadata (PUT
// /access/users/{userid}/token/{token-name}).
func newUserTokenUpdateCmd() *cobra.Command {
	var (
		comment, digest    string
		del                []string
		expire             int64
		enable, regenerate bool
	)
	cmd := &cobra.Command{
		Use:   "update <userid> <token-name>",
		Short: "Update an API token",
		Long: "Update an existing API token's metadata (PUT " +
			"/access/users/{userid}/token/{token-name}). Only flags explicitly set are " +
			"sent; use --delete to reset properties to their default, or --regenerate " +
			"to issue a new secret while keeping the token's permissions — the new " +
			"secret is printed once in the response and is never retrievable again.",
		Example: "  pmx pbs user token update alice@pbs backup-token --comment rotated",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenName := args[0], args[1]

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update token %q for user %q: no changes requested: pass at least one flag", tokenName, userid)
			}

			if fl.Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsaccess.UpdateUsersTokenParams{}
			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}

			if fl.Changed("delete") {
				params.Delete = del
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("enable") {
				params.Enable = boolPtr(enable)
			}

			if fl.Changed("expire") {
				params.Expire = int64Ptr(expire)
			}

			if fl.Changed("regenerate") {
				params.Regenerate = boolPtr(regenerate)
			}

			resp, err := deps.PBS.Access.UpdateUsersToken(cmd.Context(), userid, tokenName, params)
			if err != nil {
				return fmt.Errorf("update token %q for user %q: %w", tokenName, userid, err)
			}

			if resp != nil && resp.Secret != nil && *resp.Secret != "" {
				res := output.Result{
					Headers: []string{"TOKENID", "VALUE"},
					Rows:    [][]string{{tokenName, *resp.Secret}},
					Raw:     resp,
				}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			res := output.Result{Message: fmt.Sprintf("Token %q for user %q updated.", tokenName, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "comment")
	cmd.Flags().StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().BoolVar(&enable, "enable", true, "enable the token")
	cmd.Flags().Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	cmd.Flags().BoolVar(&regenerate, "regenerate", false, "regenerate the token secret while keeping its permissions")
	return cmd
}

// newUserTokenDeleteCmd builds `pmx pbs user token delete <userid>
// <token-name>` — remove an API token (DELETE
// /access/users/{userid}/token/{token-name}).
func newUserTokenDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <userid> <token-name>",
		Short: "Delete an API token",
		Long: "Remove an API token from a user (DELETE /access/users/{userid}/token/{token-name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs user token delete alice@pbs backup-token --yes",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, tokenName := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to delete token %q for user %q without confirmation: pass --yes/-y",
					tokenName, userid)
			}

			params := &pbsaccess.DeleteUsersTokenParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Access.DeleteUsersToken(cmd.Context(), userid, tokenName, params)
			if err != nil {
				return fmt.Errorf("delete token %q for user %q: %w", tokenName, userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Token %q for user %q deleted.", tokenName, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
