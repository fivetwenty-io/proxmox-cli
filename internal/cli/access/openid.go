package access

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newOpenidCmd builds `pve access openid` and its sub-commands.
func newOpenidCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openid",
		Short: "Manage OpenID Connect realms",
		Long:  "Inspect OpenID Connect realms configured in Proxmox VE.",
	}
	cmd.AddCommand(newOpenidListCmd())
	return cmd
}

// openidRealm is the minimal shape of each entry returned by GET /access/openid.
// PVE returns a heterogeneous list; we decode the stable fields for table output.
type openidRealm struct {
	Realm  string `json:"realm"`
	Type   string `json:"type"`
	Issuer string `json:"issuer-url,omitempty"`
}

// newOpenidListCmd builds `pve access openid list`.
func newOpenidListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List OpenID Connect realms",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			resp, err := deps.API.Access.ListOpenid(cmd.Context())
			if err != nil {
				return fmt.Errorf("list openid realms: %w", err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var r openidRealm
				if err := json.Unmarshal(raw, &r); err != nil {
					return fmt.Errorf("decode openid realm: %w", err)
				}
				rows = append(rows, []string{r.Realm, r.Type, r.Issuer})
			}

			result := output.Result{
				Headers: []string{"REALM", "TYPE", "ISSUER"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}
