package pdm

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newTokenCmd builds `pmx pdm token` — manage the API tokens belonging to a
// Proxmox Datacenter Manager user (/access/users/{userid}/token...). Unlike
// pbs, where tokens nest under `pbs user token`, PDM registers `token` as
// its own top-level group per this task's brief.
func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage a user's API tokens",
		Long:  "List, inspect, create, update, and delete API tokens belonging to a Proxmox Datacenter Manager user.",
	}
	cmd.AddCommand(
		newTokenLsCmd(),
		newTokenShowCmd(),
		newTokenAddCmd(),
		newTokenUpdateCmd(),
		newTokenDeleteCmd(),
	)
	return cmd
}

// tokenListEntry is the decoded shape of one element of
// GET /access/users/{userid}/token. Only the guaranteed-present Tokenid is
// typed here; comment/enable/expire are read from the raw decoded map via
// scalarString/userFormatEnable, mirroring userListEntry's convention.
type tokenListEntry struct {
	Tokenid string `json:"tokenid"`
}

// newTokenLsCmd builds `pmx pdm token ls <userid>` — list a user's API
// tokens (GET /access/users/{userid}/token).
func newTokenLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls <userid>",
		Short:   "List a user's API tokens",
		Long:    "List the API tokens belonging to a user (GET /access/users/{userid}/token).",
		Example: "  pmx pdm token ls alice@pdm",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid := args[0]

			resp, err := deps.PDM.Access.ListUsersToken(cmd.Context(), userid)
			if err != nil {
				return fmt.Errorf("list tokens for user %q: %w", userid, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[tokenListEntry](items, "token")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Tokenid < table[j].Entry.Tokenid })

			headers := []string{"TOKENID", "ENABLE", "EXPIRE", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				m := t.Raw
				rows = append(rows, []string{
					t.Entry.Tokenid, userFormatEnable(m["enable"]), scalarString(m["expire"]), scalarString(m["comment"]),
				})
				raws = append(raws, m)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTokenShowCmd builds `pmx pdm token show <userid> <name>` — show an API
// token's metadata (GET /access/users/{userid}/token/{token-name}). The
// token secret is never returned by this endpoint; it is only available
// once, at creation or regeneration time.
func newTokenShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <userid> <name>",
		Short: "Show an API token's metadata",
		Long: "Show every populated field of one API token's metadata (GET " +
			"/access/users/{userid}/token/{token-name}). The token secret is never " +
			"returned by this endpoint.",
		Example: "  pmx pdm token show alice@pdm ci",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, name := args[0], args[1]

			resp, err := deps.PDM.Access.GetUsersToken(cmd.Context(), userid, name)
			if err != nil {
				return fmt.Errorf("get token %q for user %q: %w", name, userid, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode token %q for user %q: %w", name, userid, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTokenAddCmd builds `pmx pdm token add <userid> <name>` — create an API
// token (POST /access/users/{userid}/token/{token-name}).
func newTokenAddCmd() *cobra.Command {
	var (
		comment, digest string
		expire          int64
		enable          bool
	)
	cmd := &cobra.Command{
		Use:   "add <userid> <name>",
		Short: "Create an API token",
		Long: "Generate a new API token for a user (POST " +
			"/access/users/{userid}/token/{token-name}). The response's VALUE column " +
			"carries the token secret; it is shown only once here and is never " +
			"retrievable again.",
		Example: "  pmx pdm token add alice@pdm ci --comment 'CI automation'",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, name := args[0], args[1]

			params := &pdmaccess.CreateUsersTokenParams{}

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

			resp, err := deps.PDM.Access.CreateUsersToken(cmd.Context(), userid, name, params)
			if err != nil {
				return fmt.Errorf("create token %q for user %q: %w", name, userid, err)
			}

			if resp == nil {
				return fmt.Errorf("create token %q for user %q: nil response from PDM", name, userid)
			}

			res := output.Result{
				Headers: []string{"TOKENID", "VALUE"},
				Rows:    [][]string{{resp.Tokenid, resp.Value}},
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&comment, "comment", "", "comment")
	f.StringVar(&digest, "digest", "", "only create if the current config digest matches")
	f.BoolVar(&enable, "enable", true, "enable the token")
	f.Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	return cmd
}

// newTokenUpdateCmd builds `pmx pdm token update <userid> <name>` — update
// an API token's metadata (PUT /access/users/{userid}/token/{token-name}).
func newTokenUpdateCmd() *cobra.Command {
	var (
		comment, digest    string
		del                []string
		expire             int64
		enable, regenerate bool
	)
	cmd := &cobra.Command{
		Use:   "update <userid> <name>",
		Short: "Update an API token",
		Long: "Update an existing API token's metadata (PUT " +
			"/access/users/{userid}/token/{token-name}). Only flags explicitly set are " +
			"sent; use --delete to reset properties to their default, or --regenerate " +
			"to issue a new secret while keeping the token's permissions — the new " +
			"secret is printed once in the response and is never retrievable again.",
		Example: `  pmx pdm token update alice@pdm ci --comment 'CI automation (rotated)'
  pmx pdm token update alice@pdm ci --regenerate`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, name := args[0], args[1]

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update token %q for user %q: no changes requested: pass at least one flag", name, userid)
			}

			params := &pdmaccess.UpdateUsersTokenParams{}
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

			resp, err := deps.PDM.Access.UpdateUsersToken(cmd.Context(), userid, name, params)
			if err != nil {
				return fmt.Errorf("update token %q for user %q: %w", name, userid, err)
			}

			// UpdateUsersTokenResponse.Value is only populated when regenerate is
			// set to true; otherwise the response carries no data.
			if resp != nil && resp.Value != nil {
				res := output.Result{
					Headers: []string{"TOKENID", "VALUE"},
					Rows:    [][]string{{name, *resp.Value}},
					Raw:     resp,
				}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			res := output.Result{Message: fmt.Sprintf("Token %q for user %q updated.", name, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&comment, "comment", "", "comment")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	f.BoolVar(&enable, "enable", true, "enable the token")
	f.Int64Var(&expire, "expire", 0, "expiration (epoch seconds; 0 = never)")
	f.BoolVar(&regenerate, "regenerate", false, "regenerate the token secret while keeping its permissions")
	return cmd
}

// newTokenDeleteCmd builds `pmx pdm token delete <userid> <name>` — remove
// an API token (DELETE /access/users/{userid}/token/{token-name}).
func newTokenDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <userid> <name>",
		Short: "Delete an API token",
		Long: "Remove an API token from a user (DELETE " +
			"/access/users/{userid}/token/{token-name}). This is destructive: pass " +
			"--yes/-y to confirm.",
		Example: "  pmx pdm token delete alice@pdm ci --yes",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			userid, name := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to delete token %q for user %q without confirmation: pass --yes/-y",
					name, userid)
			}

			params := &pdmaccess.DeleteUsersTokenParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Access.DeleteUsersToken(cmd.Context(), userid, name, params)
			if err != nil {
				return fmt.Errorf("delete token %q for user %q: %w", name, userid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Token %q for user %q deleted.", name, userid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
