package pool

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/permshared"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// poolACLPath derives the ACL/permission path for a resource pool.
//
// NB: ACL path is singular "/pool/", unlike the "pools" API/command noun — do
// not "fix" this to plural.
func poolACLPath(poolid string) string {
	return fmt.Sprintf("/pool/%s", poolid)
}

// newPermissionsCmd builds `pmx pve pool permissions` and its sub-commands.
func newPermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Manage ACL entries and inspect effective permissions on a pool",
		Long: "Manage the ACL entries on a pool's own path, /pool/{poolid}, and inspect the " +
			"privileges those entries resolve to.\n\n" +
			"  list        ACL entries written on this pool's path\n" +
			"  effective   privileges a user actually holds there\n" +
			"  grant       add roles for users, groups, or tokens\n" +
			"  revoke      take those roles away again\n\n" +
			"What this controls is who may administer the pool object itself, through roles " +
			"such as PVEPoolAdmin. Pool membership is a separate thing: to change which guests " +
			"and storage belong to the pool, use `pmx pve pool set --vms/--storage`.",
	}
	cmd.AddCommand(
		newPermissionsListCmd(),
		newPermissionsEffectiveCmd(),
		newPermissionsGrantRevokeCmd(false),
		newPermissionsGrantRevokeCmd(true),
	)
	return cmd
}

// newPermissionsListCmd builds `pmx pve pool permissions list <poolid>`.
func newPermissionsListCmd() *cobra.Command {
	var inherited bool
	cmd := &cobra.Command{
		Use:   "list <poolid>",
		Short: "List ACL entries on a pool's ACL path",
		Long: "List the ACL entries whose path exactly matches /pool/{poolid}. Note the " +
			"singular \"pool\" in that path, not \"pools\".\n\n" +
			"With --inherited, the listing also covers every ancestor path (/ and /pool), and " +
			"each row names the path its entry was granted on.",
		Example: `  pmx pve pool permissions list prod
  pmx pve pool permissions list prod --inherited`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			path := poolACLPath(args[0])

			resp, err := deps.API.Access.ListAcl(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acl for pool %q: %w", args[0], err)
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
		"also include ACL entries inherited from ancestor paths (/, /pool)")
	return cmd
}

// newPermissionsEffectiveCmd builds `pmx pve pool permissions effective <poolid>`.
func newPermissionsEffectiveCmd() *cobra.Command {
	var userid string
	cmd := &cobra.Command{
		Use:   "effective <poolid>",
		Short: "Show effective permissions on a pool's ACL path",
		Long: "Show the privileges that /pool/{poolid} resolves to for the calling user, or " +
			"for --userid when passed, once inheritance has been applied.\n\n" +
			"Querying another user's or token's permissions requires Sys.Audit on /access.",
		Example: `  pmx pve pool permissions effective prod
  pmx pve pool permissions effective prod --userid alice@pve`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			path := poolACLPath(args[0])

			params := &access.ListPermissionsParams{Path: &path}
			if cmd.Flags().Changed("userid") {
				params.Userid = &userid
			}

			resp, err := deps.API.Access.ListPermissions(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get effective permissions for pool %q: %w", args[0], err)
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

// newPermissionsGrantRevokeCmd builds `pmx pve pool permissions grant <poolid>`
// when revoke is false, and `pmx pve pool permissions revoke <poolid>` when true.
// The two verbs share identical mechanics: both call Access.UpdateAcl with the
// derived (singular) pool path, differing only in the Delete flag.
func newPermissionsGrantRevokeCmd(revoke bool) *cobra.Command {
	var roles, users, groups, tokens string
	var noPropagate bool

	verb, verbPast, prep := "grant", "Granted", "to"
	short := "Grant roles to users, groups, or tokens on a pool's ACL path"
	long := "Grant roles on a pool's ACL path, /pool/{poolid}, to any mix of users, groups, " +
		"and API tokens. Name at least one of --users, --groups, or --tokens.\n\n" +
		"This confers administrative access to the pool object, not membership in it. To " +
		"change which guests and storage belong to the pool, use " +
		"`pmx pve pool set --vms/--storage`.\n\n" +
		"Writing ACL entries requires Permissions.Modify on the path.\n\n" +
		"A granted role propagates to every path below this one by default; pass " +
		"--no-propagate to confine it to this path alone."
	if revoke {
		verb, verbPast, prep = "revoke", "Revoked", "from"
		short = "Revoke roles from users, groups, or tokens on a pool's ACL path"
		long = "Revoke roles on a pool's ACL path, /pool/{poolid}, from any mix of users, " +
			"groups, and API tokens. Name at least one of --users, --groups, or --tokens.\n\n" +
			"This withdraws administrative access to the pool object, not membership in it. " +
			"To change which guests and storage belong to the pool, use " +
			"`pmx pve pool set --vms/--storage`.\n\n" +
			"Removing ACL entries requires Permissions.Modify on the path. Revoking an entry " +
			"that was never there succeeds silently, matching the PVE server.\n\n" +
			"Nothing here guards against self-lockout: revoking your own access to the path " +
			"you are managing is allowed and takes effect at once. Run `permissions " +
			"effective` first if you are unsure what you are about to lose."
	}

	example := `  pmx pve pool permissions grant prod --roles PVEVMAdmin --users alice@pve`
	if revoke {
		example = `  pmx pve pool permissions revoke prod --roles PVEVMAdmin --users alice@pve`
	}

	cmd := &cobra.Command{
		Use:     verb + " <poolid>",
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			poolid := args[0]

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

			path := poolACLPath(poolid)
			params := permshared.GrantRevokeParams(path, roles, usersPtr, groupsPtr, tokensPtr, propagate, revoke)
			if err := deps.API.Access.UpdateAcl(cmd.Context(), params); err != nil {
				return fmt.Errorf("%s roles on pool %q: %w", verb, poolid, err)
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
		"do not propagate these roles to paths below the pool's ACL path")
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
