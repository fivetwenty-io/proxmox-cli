package qemu

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/permshared"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// permissionsAclPath derives a VM's ACL path from its resolved VMID: PVE
// scopes every VM/container permission under /vms/{vmid}, shared by both
// guest kinds.
func permissionsAclPath(vmid string) string {
	return "/vms/" + vmid
}

// newPermissionsCmd builds `pmx pve qemu permissions` and its sub-commands: a
// thin, VM-scoped wrapper over the global `pmx pve access acl`/`pmx pve access
// permissions` commands that derives the VM's ACL path automatically.
func newPermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Inspect and manage ACL entries scoped to a VM",
		Long: "Manage the ACL entries on a VM's own path, /vms/{vmid}, and inspect the " +
			"privileges those entries resolve to. The path is derived from the VM you name, so " +
			"you never have to type it.\n\n" +
			"  list        ACL entries written on this VM's path\n" +
			"  effective   privileges a user actually holds there\n" +
			"  grant       add roles for users, groups, or tokens\n" +
			"  revoke      take those roles away again\n\n" +
			"LXC containers share the same /vms/{vmid} path grammar; see " +
			"`pmx pve lxc permissions`. For any other ACL path, use `pmx pve access acl` and " +
			"`pmx pve access permissions` directly.",
	}
	cmd.AddCommand(
		newPermissionsListCmd(),
		newPermissionsEffectiveCmd(),
		newPermissionsGrantCmd(),
		newPermissionsRevokeCmd(),
	)
	return cmd
}

// newPermissionsListCmd builds `pmx pve qemu permissions list <vmid|name>`.
func newPermissionsListCmd() *cobra.Command {
	var inherited bool
	cmd := &cobra.Command{
		Use:   "list <vmid|name>",
		Short: "List ACL entries on a VM's ACL path",
		Long: "List the ACL entries whose path is exactly the VM's /vms/{vmid} path.\n\n" +
			"With --inherited, the listing also covers every ancestor of that path (/ and " +
			"/vms), unioned client-side from a single ACL read rather than extra API calls. " +
			"Each row then names the path its entry was granted on, so direct and inherited " +
			"entries never blur together.",
		Example: `  pmx pve qemu permissions list 100
  pmx pve qemu permissions list 100 --inherited`,
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
				return fmt.Errorf("list acl for VM %s: %w", vmid, err)
			}
			entries, err := permshared.DecodeAclList(resp)
			if err != nil {
				return fmt.Errorf("decode acl entries for VM %s: %w", vmid, err)
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

// newPermissionsEffectiveCmd builds `pmx pve qemu permissions effective <vmid|name>`.
func newPermissionsEffectiveCmd() *cobra.Command {
	var userid string
	cmd := &cobra.Command{
		Use:   "effective <vmid|name>",
		Short: "Show effective permissions on a VM's ACL path",
		Long: "Show the privileges that a VM's /vms/{vmid} path resolves to for the calling " +
			"user, or for --userid when passed, once inheritance and propagation have been " +
			"applied.\n\n" +
			"Querying another user's or token's permissions requires Sys.Audit on /access.",
		Example: `  pmx pve qemu permissions effective 100`,
		Args:    cobra.ExactArgs(1),
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
				return fmt.Errorf("list effective permissions for VM %s: %w", vmid, err)
			}
			tree, err := permshared.DecodePermissions(resp)
			if err != nil {
				return fmt.Errorf("decode effective permissions for VM %s: %w", vmid, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(), permshared.RenderEffective(tree), deps.Format)
		},
	}
	cmd.Flags().StringVar(&userid, "userid", "",
		"show effective permissions for this user/token instead of the caller (requires Sys.Audit on /access)")
	return cmd
}

// newPermissionsGrantCmd builds `pmx pve qemu permissions grant <vmid|name>`.
func newPermissionsGrantCmd() *cobra.Command {
	return newPermissionsGrantRevokeCmd(false)
}

// newPermissionsRevokeCmd builds `pmx pve qemu permissions revoke <vmid|name>`.
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
	shortDesc := "Grant roles to users, groups, or tokens on a VM's ACL path"
	longDesc := "Grant roles on a VM's ACL path, /vms/{vmid}, to any mix of users, groups, " +
		"and API tokens. Each flag takes a comma-separated list, and at least one of " +
		"--users, --groups, or --tokens is required.\n\n" +
		"Writing ACL entries requires Permissions.Modify on the path.\n\n" +
		"A granted role propagates to every path below /vms/{vmid} by default; pass " +
		"--no-propagate to confine it to this path alone."
	example := "  pmx pve qemu permissions grant 100 --roles PVEVMAdmin --users alice@pve"
	if revoke {
		verb, pastTense, prep = "revoke", "Revoked", "from"
		shortDesc = "Revoke roles from users, groups, or tokens on a VM's ACL path"
		longDesc = "Revoke roles on a VM's ACL path, /vms/{vmid}, from any mix of users, " +
			"groups, and API tokens. Each flag takes a comma-separated list, and at least one " +
			"of --users, --groups, or --tokens is required.\n\n" +
			"Removing ACL entries requires Permissions.Modify on the path. Revoking an entry " +
			"that was never there succeeds silently: PVE reports nothing that would confirm " +
			"it ever matched.\n\n" +
			"Nothing here guards against self-lockout. Revoking roles you rely on, your own " +
			"access to this VM included, is allowed and takes effect at once. Run " +
			"`permissions effective` first to confirm what you are about to lose."
		example = "  pmx pve qemu permissions revoke 100 --roles PVEVMAdmin --users alice@pve"
	}

	cmd := &cobra.Command{
		Use:     verb + " <vmid|name>",
		Short:   shortDesc,
		Long:    longDesc,
		Example: example,
		Args:    cobra.ExactArgs(1),
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
				return fmt.Errorf("%s acl for VM %s: %w", verb, vmid, err)
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
		"do not propagate these roles to paths below the VM's ACL path")
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
