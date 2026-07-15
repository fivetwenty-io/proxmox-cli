package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pbsaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newACLCmd builds `pmx pbs acl` and its sub-commands: list Access Control
// List entries and grant or revoke a role on a path for a user, group, or
// API token (GET/PUT /access/acl).
func newACLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acl",
		Short: "Manage the Proxmox Backup Server access control list",
		Long:  "List ACL entries and grant or revoke a role on a path for a user, group, or API token.",
	}
	cmd.AddCommand(newACLLsCmd(), newACLUpdateCmd())
	return cmd
}

// aclListEntry is the decoded shape of one element of GET /access/acl.
type aclListEntry struct {
	Path      string `json:"path"`
	Propagate *bool  `json:"propagate,omitempty"`
	Roleid    string `json:"roleid"`
	Ugid      string `json:"ugid"`
	UgidType  string `json:"ugid_type"`
}

// newACLLsCmd builds `pmx pbs acl ls` — list ACL entries (GET /access/acl).
func newACLLsCmd() *cobra.Command {
	var path string
	var exact bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACL entries",
		Long: "List Access Control List entries (GET /access/acl). --path restricts " +
			"to entries at or under a path (prefix match by default; pass --exact to " +
			"require an exact match).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsaccess.ListAclParams{}

			fl := cmd.Flags()
			if fl.Changed("path") {
				params.Path = strPtr(path)
			}

			if fl.Changed("exact") {
				params.Exact = boolPtr(exact)
			}

			resp, err := deps.PBS.Access.ListAcl(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list acl: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]aclListEntry, 0, len(items))

			for _, raw := range items {
				var e aclListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode acl entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Path != entries[j].Path {
					return entries[i].Path < entries[j].Path
				}

				return entries[i].Ugid < entries[j].Ugid
			})

			headers := []string{"PATH", "UGID", "UGID-TYPE", "ROLEID", "PROPAGATE"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Path, e.Ugid, e.UgidType, e.Roleid, pbsFormatOptionalBool(e.Propagate),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "only list entries at or under this access control path")
	cmd.Flags().BoolVar(&exact, "exact", false, "require an exact path match instead of a prefix match")
	return cmd
}

// newACLUpdateCmd builds `pmx pbs acl update` — grant or revoke a role on a
// path for a user, group, or API token (PUT /access/acl).
func newACLUpdateCmd() *cobra.Command {
	var (
		path, role, authId, group, digest string
		propagate, del                    bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Grant or revoke a role on a path",
		Long: "Grant a role to a user, group, or API token on an access control path " +
			"(PUT /access/acl). --path and --role are required, and exactly one of " +
			"--auth-id or --group identifies the subject. Pass --delete to remove the " +
			"role instead of granting it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if path == "" {
				return fmt.Errorf("--path is required")
			}

			if role == "" {
				return fmt.Errorf("--role is required")
			}

			if !fl.Changed("auth-id") && !fl.Changed("group") {
				return fmt.Errorf("one of --auth-id or --group is required")
			}

			if fl.Changed("auth-id") && fl.Changed("group") {
				return fmt.Errorf("--auth-id and --group are mutually exclusive")
			}

			params := &pbsaccess.UpdateAclParams{Path: path, Role: role}
			if fl.Changed("auth-id") {
				params.AuthId = strPtr(authId)
			}

			if fl.Changed("group") {
				params.Group = strPtr(group)
			}

			if fl.Changed("propagate") {
				params.Propagate = boolPtr(propagate)
			}

			if fl.Changed("delete") {
				params.Delete = boolPtr(del)
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Access.UpdateAcl(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("update acl: %w", err)
			}

			verb := "granted"
			if del {
				verb = "revoked"
			}

			res := output.Result{Message: fmt.Sprintf("Role %q %s on %q.", role, verb, path)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "access control path (required)")
	cmd.Flags().StringVar(&role, "role", "", "role to grant or revoke (required)")
	cmd.Flags().StringVar(&authId, "auth-id", "", "authentication ID (user or API token) the role applies to")
	cmd.Flags().StringVar(&group, "group", "", "group the role applies to")
	cmd.Flags().BoolVar(&propagate, "propagate", true, "allow the permission to propagate (inherit) to sub-paths")
	cmd.Flags().BoolVar(&del, "delete", false, "remove the permission instead of granting it")
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cli.MustMarkRequired(cmd, "path")
	cli.MustMarkRequired(cmd, "role")
	return cmd
}
