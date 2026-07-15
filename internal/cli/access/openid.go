package access

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newOpenidCmd builds `pmx access openid` and its sub-commands.
func newOpenidCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openid",
		Short: "Manage OpenID Connect realms",
		Long:  "Inspect OpenID Connect realms configured in Proxmox VE.",
	}
	cmd.AddCommand(newOpenidListCmd())
	return cmd
}

// openidRealm is the minimal decoded shape of a GET /access/domains entry.
type openidRealm struct {
	Realm   string `json:"realm"`
	Type    string `json:"type"`
	Comment string `json:"comment,omitempty"`
}

// newOpenidListCmd builds `pmx access openid list`.
//
// GET /access/openid is only a directory index (auth-url, login), not a realm
// list; realms live in GET /access/domains, filtered to type openid here.
func newOpenidListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List OpenID Connect realms",
		Long: "List every configured OpenID Connect authentication realm, filtered " +
			"client-side from the full realm list. Use `pmx pve access domain get <realm>` " +
			"for a realm's full configuration.",
		Example: `  pmx pve access openid list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Access.ListDomains(cmd.Context())
			if err != nil {
				return fmt.Errorf("list openid realms: %w", err)
			}

			rows := make([][]string, 0)
			raws := make([]json.RawMessage, 0)
			if resp != nil {
				for _, raw := range *resp {
					var r openidRealm
					if err := json.Unmarshal(raw, &r); err != nil {
						return fmt.Errorf("decode openid realm: %w", err)
					}
					if r.Type != "openid" {
						continue
					}
					rows = append(rows, []string{r.Realm, r.Type, r.Comment})
					raws = append(raws, raw)
				}
			}

			result := output.Result{
				Headers: []string{"REALM", "TYPE", "COMMENT"},
				Rows:    rows,
				Raw:     raws,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}
