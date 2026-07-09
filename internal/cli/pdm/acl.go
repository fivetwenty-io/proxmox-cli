package pdm

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newACLCmd builds `pmx pdm acl` — list ACL entries and grant or revoke a
// role on a path for a user, group, or API token (GET/PUT /access/acl).
func newACLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acl",
		Short: "Manage the Proxmox Datacenter Manager access control list",
		Long:  "List ACL entries and grant or revoke a role on a path for a user, group, or API token.",
	}
	cmd.AddCommand(newACLLsCmd(), newACLUpdateCmd())
	return cmd
}

// aclListEntry is the decoded shape of one element of GET /access/acl, per
// pdm-apidoc.json's returns.items schema for GET /access/acl (path,
// propagate, roleid, ugid, ugid_type). access_gen.go declares only the outer
// ListAclResponse []json.RawMessage without a per-item type; this happens to
// match pbs/acl.go's aclListEntry exactly, since ACL entries share the same
// shape across Proxmox products.
type aclListEntry struct {
	Path      string `json:"path"`
	Propagate *bool  `json:"propagate,omitempty"`
	Roleid    string `json:"roleid"`
	Ugid      string `json:"ugid"`
	UgidType  string `json:"ugid_type"`
}

// newACLLsCmd builds `pmx pdm acl ls` — list ACL entries (GET /access/acl).
func newACLLsCmd() *cobra.Command {
	var path string
	var exact, allForAuthid bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List ACL entries",
		Long: "List Access Control List entries (GET /access/acl). --path restricts " +
			"to entries at or under a path (prefix match by default; pass --exact to " +
			"require an exact match). --all-for-authid returns every ACL entry for the " +
			"caller's own authid as user-type entries, ignoring group membership.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmaccess.ListAclParams{}

			fl := cmd.Flags()
			if fl.Changed("path") {
				params.Path = strPtr(path)
			}

			if fl.Changed("exact") {
				params.Exact = boolPtr(exact)
			}

			if fl.Changed("all-for-authid") {
				params.AllForAuthid = boolPtr(allForAuthid)
			}

			resp, err := deps.PDM.Access.ListAcl(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list acl: %w", err)
			}

			items := rawItemsOf(resp)
			type aclRow struct {
				entry aclListEntry
				raw   map[string]any
			}
			table := make([]aclRow, 0, len(items))

			for _, raw := range items {
				var e aclListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode acl entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode acl entry: %w", err)
				}

				table = append(table, aclRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].entry.Path != table[j].entry.Path {
					return table[i].entry.Path < table[j].entry.Path
				}
				return table[i].entry.Ugid < table[j].entry.Ugid
			})

			headers := []string{"PATH", "UGID", "UGID-TYPE", "ROLEID", "PROPAGATE"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{e.Path, e.Ugid, e.UgidType, e.Roleid, boolPtrString(e.Propagate)})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "only list entries at or under this access control path")
	f.BoolVar(&exact, "exact", false, "require an exact path match instead of a prefix match")
	f.BoolVar(&allForAuthid, "all-for-authid", false,
		"return every ACL entry for the caller's own authid as user-type entries")
	return cmd
}

// newACLUpdateCmd builds `pmx pdm acl update` — grant or revoke a role on a
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

			if !fl.Changed("auth-id") && !fl.Changed("group") {
				return fmt.Errorf("one of --auth-id or --group is required")
			}

			if fl.Changed("auth-id") && fl.Changed("group") {
				return fmt.Errorf("--auth-id and --group are mutually exclusive")
			}

			params := &pdmaccess.UpdateAclParams{Path: path, Role: role}
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

			err := deps.PDM.Access.UpdateAcl(cmd.Context(), params)
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
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "access control path (required)")
	f.StringVar(&role, "role", "", "role to grant or revoke (required)")
	f.StringVar(&authId, "auth-id", "", "authentication ID (user or API token) the role applies to")
	f.StringVar(&group, "group", "", "group the role applies to")
	f.BoolVar(&propagate, "propagate", true, "allow the permission to propagate (inherit) to sub-paths")
	f.BoolVar(&del, "delete", false, "remove the permission instead of granting it")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cli.MustMarkRequired(cmd, "path")
	cli.MustMarkRequired(cmd, "role")
	return cmd
}
