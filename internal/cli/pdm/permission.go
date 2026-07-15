package pdm

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newPermissionCmd builds `pmx pdm permission` — show effective permissions
// for a user or API token (GET /access/permissions).
func newPermissionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permission",
		Short: "Show effective permissions",
		Long: "Show the effective permissions granted to a user or API token, as a " +
			"map of access control path to the privileges (and their propagate bit) " +
			"granted there.",
	}
	cmd.AddCommand(newPermissionLsCmd())
	return cmd
}

// newPermissionLsCmd builds `pmx pdm permission ls` — list effective
// permissions (GET /access/permissions).
func newPermissionLsCmd() *cobra.Command {
	var authId, path string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List effective permissions",
		Long: "List the effective permissions for a user or API token, optionally " +
			"restricted to one access control path (GET /access/permissions). " +
			"--auth-id defaults to the currently authenticated identity when omitted.",
		Example: `  pmx pdm permission ls
  pmx pdm permission ls --auth-id alice@pdm --path /remote/pve-main`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmaccess.ListPermissionsParams{}

			fl := cmd.Flags()
			if fl.Changed("auth-id") {
				params.AuthId = strPtr(authId)
			}

			if fl.Changed("path") {
				params.Path = strPtr(path)
			}

			resp, err := deps.PDM.Access.ListPermissions(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list permissions: %w", err)
			}

			// pdm-apidoc.json declares GET /access/permissions' return schema as a
			// generic "additionalProperties: true" map with no fixed value type (the
			// propagate bit's wire representation isn't pinned to JSON boolean), so
			// the inner value is decoded as `any` and rendered via scalarString
			// rather than assuming a strict map[string]map[string]bool as pbs's
			// permission.go does.
			tree := map[string]map[string]any{}
			if resp != nil && len(*resp) > 0 {
				err := json.Unmarshal(*resp, &tree)
				if err != nil {
					return fmt.Errorf("decode permissions: %w", err)
				}
			}

			paths := make([]string, 0, len(tree))
			for p := range tree {
				paths = append(paths, p)
			}
			sort.Strings(paths)

			headers := []string{"PATH", "PRIV", "PROPAGATE"}
			rows := make([][]string, 0)

			for _, p := range paths {
				privs := make([]string, 0, len(tree[p]))
				for priv := range tree[p] {
					privs = append(privs, priv)
				}
				sort.Strings(privs)

				for _, priv := range privs {
					rows = append(rows, []string{p, priv, scalarString(tree[p][priv])})
				}
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: tree}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&authId, "auth-id", "", "user ID or full API token ID to show permissions for (default: caller)")
	cmd.Flags().StringVar(&path, "path", "", "only show permissions at this specific access control path")
	return cmd
}
