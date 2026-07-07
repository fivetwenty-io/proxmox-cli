package lxc

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/access"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/cli/permshared"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// permissionsAclPath derives a container's ACL path from its resolved VMID:
// PVE scopes every VM/container permission under /vms/{vmid}, shared by both
// guest kinds.
func permissionsAclPath(vmid string) string {
	return "/vms/" + vmid
}

// newPermissionsCmd builds `pve lxc permissions` and its sub-commands: a
// thin, container-scoped wrapper over the global `pve access acl`/`pve
// access permissions` commands that derives the container's ACL path
// automatically.
func newPermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Inspect and manage ACL entries scoped to a container",
		Long: "Scope the global 'pve access acl' and 'pve access permissions' commands to a " +
			"single container's ACL path, /vms/{vmid} (the same path grammar shared by QEMU VMs " +
			"under 'pve qemu permissions'), so operators never have to hand-type it. 'list' and " +
			"'effective' are read-only views; 'grant' and 'revoke' are thin wrappers over the " +
			"underlying access.acl update call. For any ACL path other than a single " +
			"container's, use 'pve access acl'/'pve access permissions' directly.",
	}
	cmd.AddCommand(
		newPermissionsListCmd(),
		newPermissionsEffectiveCmd(),
		newPermissionsGrantCmd(),
		newPermissionsRevokeCmd(),
	)
	return cmd
}

// newPermissionsListCmd builds `pve lxc permissions list <vmid|name>`.
func newPermissionsListCmd() *cobra.Command {
	var inherited bool
	cmd := &cobra.Command{
		Use:   "list <vmid|name>",
		Short: "List ACL entries on a container's ACL path",
		Long: "List the ACL entries whose path is exactly the container's /vms/{vmid} path. With " +
			"--inherited, also include entries on every ancestor of that path (/, /vms), unioned " +
			"client-side from a single ACL read (no extra API calls); each row then shows the " +
			"path it was actually granted on so inherited and direct entries are never confused.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, _, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			path := permissionsAclPath(vmid)

			resp, err := deps.API.Access.ListAcl(cmd.Context())
			if err != nil {
				return fmt.Errorf("list acl for container %s: %w", vmid, err)
			}
			entries, err := permshared.DecodeAclList(resp)
			if err != nil {
				return fmt.Errorf("decode acl entries for container %s: %w", vmid, err)
			}

			var filtered []permshared.AclEntry
			if inherited {
				for _, ancestor := range permshared.ParentChain(path) {
					filtered = append(filtered, permshared.FilterByPath(entries, ancestor, true)...)
				}
			} else {
				filtered = permshared.FilterByPath(entries, path, true)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				permshared.RenderAclList(filtered, inherited), deps.Format)
		},
	}
	cmd.Flags().BoolVar(&inherited, "inherited", false,
		"also list entries inherited from ancestor paths (/, /vms)")
	return cmd
}

// newPermissionsEffectiveCmd builds `pve lxc permissions effective <vmid|name>`.
func newPermissionsEffectiveCmd() *cobra.Command {
	var userid string
	cmd := &cobra.Command{
		Use:   "effective <vmid|name>",
		Short: "Show effective permissions on a container's ACL path",
		Long: "Show the effective (post-inheritance, post-propagation) privileges on a " +
			"container's /vms/{vmid} path for the calling user, or for --userid when passed. " +
			"Querying another user's or token's effective permissions requires Sys.Audit on " +
			"/access.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, _, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			path := permissionsAclPath(vmid)

			params := &access.ListPermissionsParams{Path: &path}
			if cmd.Flags().Changed("userid") {
				params.Userid = &userid
			}

			resp, err := deps.API.Access.ListPermissions(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list effective permissions for container %s: %w", vmid, err)
			}
			tree, err := permshared.DecodePermissions(resp)
			if err != nil {
				return fmt.Errorf("decode effective permissions for container %s: %w", vmid, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(), permshared.RenderEffective(tree), deps.Format)
		},
	}
	cmd.Flags().StringVar(&userid, "userid", "",
		"show effective permissions for this user/token instead of the caller (requires Sys.Audit on /access)")
	return cmd
}

// newPermissionsGrantCmd builds `pve lxc permissions grant <vmid|name>`.
func newPermissionsGrantCmd() *cobra.Command {
	return newPermissionsGrantRevokeCmd(false)
}

// newPermissionsRevokeCmd builds `pve lxc permissions revoke <vmid|name>`.
func newPermissionsRevokeCmd() *cobra.Command {
	return newPermissionsGrantRevokeCmd(true)
}

// newPermissionsGrantRevokeCmd builds the `grant` (revoke=false) or `revoke`
// (revoke=true) sub-command; the two differ only in the Delete flag sent to
// access.UpdateAcl and their reported diction.
func newPermissionsGrantRevokeCmd(revoke bool) *cobra.Command {
	var (
		roles, users, groups, tokens string
		noPropagate                  bool
	)

	verb, pastTense, prep := "grant", "Granted", "to"
	shortDesc := "Grant roles to users, groups, or tokens on a container's ACL path"
	longDesc := "Grant one or more roles on a container's /vms/{vmid} ACL path to the given " +
		"users, groups, and/or tokens (comma-separated; at least one of --users, --groups, or " +
		"--tokens is required). Requires Permissions.Modify on the path. By default the " +
		"granted roles propagate to any path below /vms/{vmid}; pass --no-propagate to " +
		"restrict them to this path only."
	if revoke {
		verb, pastTense, prep = "revoke", "Revoked", "from"
		shortDesc = "Revoke roles from users, groups, or tokens on a container's ACL path"
		longDesc = "Revoke one or more roles on a container's /vms/{vmid} ACL path from the " +
			"given users, groups, and/or tokens (comma-separated; at least one of --users, " +
			"--groups, or --tokens is required). Requires Permissions.Modify on the path. " +
			"Revoking an entry that does not exist succeeds silently (PVE behavior: there is " +
			"nothing to confirm it ever matched). Revoking roles you rely on, including your " +
			"own access to this container, is not blocked by this command (self-lockout); run 'permissions " +
			"effective' first to confirm what you are about to lose."
	}

	cmd := &cobra.Command{
		Use:   verb + " <vmid|name>",
		Short: shortDesc,
		Long:  longDesc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, _, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if err := requireGrantRevokeSubject(users, groups, tokens); err != nil {
				return err
			}
			path := permissionsAclPath(vmid)

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
			var propagatePtr *bool
			if noPropagate {
				f := false
				propagatePtr = &f
			}

			params := permshared.GrantRevokeParams(path, roles, usersPtr, groupsPtr, tokensPtr, propagatePtr, revoke)
			if err := deps.API.Access.UpdateAcl(cmd.Context(), params); err != nil {
				return fmt.Errorf("%s acl for container %s: %w", verb, vmid, err)
			}

			msg := fmt.Sprintf("%s roles %s %s %s on %s.",
				pastTense, roles, prep, describeGrantRevokeSubjects(users, groups, tokens), path)
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&roles, "roles", "", "comma-separated role list (required)")
	cmd.Flags().StringVar(&users, "users", "", "comma-separated user list")
	cmd.Flags().StringVar(&groups, "groups", "", "comma-separated group list")
	cmd.Flags().StringVar(&tokens, "tokens", "", "comma-separated API token list (user@realm!token)")
	cmd.Flags().BoolVar(&noPropagate, "no-propagate", false,
		"do not propagate these roles to paths below the container's ACL path")
	cli.MustMarkRequired(cmd, "roles")
	return cmd
}

// requireGrantRevokeSubject enforces that at least one of --users, --groups,
// or --tokens carries a non-empty value, giving a clean CLI-side error
// instead of relying on the server to reject an empty ACL update. An
// explicitly empty flag (--users "") counts as absent.
func requireGrantRevokeSubject(users, groups, tokens string) error {
	if users == "" && groups == "" && tokens == "" {
		return fmt.Errorf("at least one of --users, --groups, or --tokens is required")
	}
	return nil
}

// describeGrantRevokeSubjects renders the subject portion of the
// grant/revoke confirmation message ("users a,b, groups c") from whichever
// of --users/--groups/--tokens carried a value.
func describeGrantRevokeSubjects(users, groups, tokens string) string {
	parts := make([]string, 0, 3)
	if users != "" {
		parts = append(parts, "users "+users)
	}
	if groups != "" {
		parts = append(parts, "groups "+groups)
	}
	if tokens != "" {
		parts = append(parts, "tokens "+tokens)
	}
	return strings.Join(parts, ", ")
}
