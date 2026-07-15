package storage

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/permshared"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// storageACLPath derives the ACL/permission path for a storage definition.
// Unlike storage get/set/delete, this path is never checked against the
// storage's actual existence: ACL paths are free-form strings in PVE, and
// granting roles on a storage ahead of its provisioning (or after its
// deletion) is legal and sometimes intentional.
func storageACLPath(storageID string) string {
	return fmt.Sprintf("/storage/%s", storageID)
}

// newPermissionsCmd builds `pmx pve storage permissions` and its sub-commands.
func newPermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Manage ACL entries and inspect effective permissions on a storage",
		Long: "List, grant, and revoke ACL entries on a storage's ACL path (/storage/{storage}), " +
			"and inspect the resulting effective permissions. The path is derived from the " +
			"storage identifier; no existence check is performed against the storage's actual " +
			"configuration, since ACL paths are free-form and granting ahead of provisioning is legal.",
	}
	cmd.AddCommand(
		newPermissionsListCmd(),
		newPermissionsEffectiveCmd(),
		newPermissionsGrantRevokeCmd(false),
		newPermissionsGrantRevokeCmd(true),
	)
	return cmd
}

// newPermissionsListCmd builds `pmx pve storage permissions list <storage>`.
func newPermissionsListCmd() *cobra.Command {
	var inherited bool
	cmd := &cobra.Command{
		Use:   "list <storage>",
		Short: "List ACL entries on a storage's ACL path",
		Long: "List the ACL entries whose path exactly matches /storage/{storage}. With " +
			"--inherited, also include entries from every ancestor path (/, /storage), each " +
			"row showing which path it came from.",
		Example: `  pmx pve storage permissions list local-lvm
  pmx pve storage permissions list local-lvm --inherited`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			path := storageACLPath(args[0])

			resp, err := deps.API.Access.ListAcl(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acl for storage %q: %w", args[0], err)
			}
			entries, err := permshared.DecodeAclList(resp)
			if err != nil {
				return err
			}

			var matched []permshared.AclEntry
			if inherited {
				for _, p := range permshared.ParentChain(path) {
					matched = append(matched, permshared.FilterByPath(entries, p, true)...)
				}
			} else {
				matched = permshared.FilterByPath(entries, path, true)
			}

			res := permshared.RenderAclList(matched, inherited)
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&inherited, "inherited", false,
		"also include ACL entries inherited from ancestor paths (/, /storage)")
	return cmd
}

// newPermissionsEffectiveCmd builds `pmx pve storage permissions effective <storage>`.
func newPermissionsEffectiveCmd() *cobra.Command {
	var userid string
	cmd := &cobra.Command{
		Use:   "effective <storage>",
		Short: "Show effective permissions on a storage's ACL path",
		Long: "Show the effective (post-inheritance) privileges on /storage/{storage} for the " +
			"caller, or for --userid when passed. Querying another user's or token's permissions " +
			"requires Sys.Audit on /access.",
		Example: `  pmx pve storage permissions effective local-lvm
  pmx pve storage permissions effective local-lvm --userid alice@pve`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			path := storageACLPath(args[0])

			params := &access.ListPermissionsParams{Path: &path}
			if cmd.Flags().Changed("userid") {
				params.Userid = &userid
			}

			resp, err := deps.API.Access.ListPermissions(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get effective permissions for storage %q: %w", args[0], err)
			}
			tree, err := permshared.DecodePermissions(resp)
			if err != nil {
				return err
			}

			res := permshared.RenderEffective(tree)
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&userid, "userid", "",
		"query effective permissions for this user/token instead of the caller (requires Sys.Audit on /access)")
	return cmd
}

// newPermissionsGrantRevokeCmd builds `pmx pve storage permissions grant <storage>`
// when revoke is false, and `pmx pve storage permissions revoke <storage>` when true.
// The two verbs share identical mechanics: both call Access.UpdateAcl with the
// derived storage path, differing only in the Delete flag.
func newPermissionsGrantRevokeCmd(revoke bool) *cobra.Command {
	var roles, users, groups, tokens string
	var noPropagate bool

	verb, verbPast, prep := "grant", "Granted", "to"
	short := "Grant roles to users, groups, or tokens on a storage's ACL path"
	if revoke {
		verb, verbPast, prep = "revoke", "Revoked", "from"
		short = "Revoke roles from users, groups, or tokens on a storage's ACL path"
	}

	example := `  pmx pve storage permissions grant local-lvm --roles PVEVMAdmin --users alice@pve`
	if revoke {
		example = `  pmx pve storage permissions revoke local-lvm --roles PVEVMAdmin --users alice@pve`
	}

	cmd := &cobra.Command{
		Use:   verb + " <storage>",
		Short: short,
		Long: short + " (/storage/{storage}). Mutating ACL entries requires Permissions.Modify " +
			"on the path. Revoking an entry that does not exist succeeds silently, matching PVE " +
			"server behavior. This command does not block self-lockout (e.g. revoking your own " +
			"access to the path you are managing); check `permissions effective` first if unsure.",
		Example: example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storageID := args[0]

			if users == "" && groups == "" && tokens == "" {
				return fmt.Errorf("at least one of --users, --groups, or --tokens is required")
			}

			var usersPtr, groupsPtr, tokensPtr *string
			if users != "" {
				usersPtr = &users
			}
			if groups != "" {
				groupsPtr = &groups
			}
			if tokens != "" {
				tokensPtr = &tokens
			}

			var propagate *bool
			if noPropagate {
				f := false
				propagate = &f
			}

			path := storageACLPath(storageID)
			params := permshared.GrantRevokeParams(path, roles, usersPtr, groupsPtr, tokensPtr, propagate, revoke)
			if err := deps.API.Access.UpdateAcl(cmd.Context(), params); err != nil {
				return fmt.Errorf("%s roles on storage %q: %w", verb, storageID, err)
			}

			msg := fmt.Sprintf("%s roles %s %s %s on %s.",
				verbPast, roles, prep, strings.Join(subjectClauses(users, groups, tokens), ", "), path)
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&roles, "roles", "", "comma-separated role list (required)")
	cmd.Flags().StringVar(&users, "users", "", "comma-separated user list")
	cmd.Flags().StringVar(&groups, "groups", "", "comma-separated group list")
	cmd.Flags().StringVar(&tokens, "tokens", "", "comma-separated API token list (user@realm!token)")
	cmd.Flags().BoolVar(&noPropagate, "no-propagate", false,
		"do not propagate these roles to paths below the storage's ACL path")
	cli.MustMarkRequired(cmd, "roles")
	return cmd
}

// subjectClauses renders the subject flags that carried a value (users,
// groups, tokens, in that order) as human-readable clauses for the
// grant/revoke success message, e.g. ["users alice,bob", "tokens root@pam!ci"].
func subjectClauses(users, groups, tokens string) []string {
	clauses := make([]string, 0, 3)
	if users != "" {
		clauses = append(clauses, "users "+users)
	}
	if groups != "" {
		clauses = append(clauses, "groups "+groups)
	}
	if tokens != "" {
		clauses = append(clauses, "tokens "+tokens)
	}
	return clauses
}
