package sdn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/permshared"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// ---- zone permissions -------------------------------------------------------

// zonePermPath derives an SDN zone's ACL path: /sdn/zones/{zone}.
func zonePermPath(zone string) string {
	return fmt.Sprintf("/sdn/zones/%s", zone)
}

// newZonePermissionsCmd builds `pmx sdn zone permissions` and its sub-commands:
// list/effective/grant/revoke against a zone's own ACL path (/sdn/zones/{zone}).
func newZonePermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Manage ACL entries and view effective permissions on an SDN zone",
		Long: "List, grant, and revoke ACL entries on an SDN zone's own access-control path " +
			"(/sdn/zones/{zone}), and view the effective permission set a user or token holds " +
			"there. This is a thin, path-deriving wrapper over `pmx pve access acl` and " +
			"`pmx pve access permissions`; use those directly for any path outside a single zone.",
	}
	cmd.AddCommand(
		newZonePermListCmd(),
		newZonePermEffectiveCmd(),
		newZonePermGrantRevokeCmd(false),
		newZonePermGrantRevokeCmd(true),
	)
	return cmd
}

// newZonePermListCmd builds `pmx sdn zone permissions list <zone>`.
func newZonePermListCmd() *cobra.Command {
	var inherited bool
	cmd := &cobra.Command{
		Use:   "list <zone>",
		Short: "List ACL entries on an SDN zone",
		Long: "List ACL entries whose path exactly matches the zone's own ACL path " +
			"(/sdn/zones/{zone}). Pass --inherited to also include entries inherited from the " +
			"zone's ancestor paths (/, /sdn, /sdn/zones); this walks the path client-side and " +
			"issues no extra API calls. Needs the same privilege as `pmx pve access acl list`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			return runPermList(cmd, deps, zonePermPath(args[0]), inherited)
		},
	}
	cmd.Flags().BoolVar(&inherited, "inherited", false,
		"also include ACL entries inherited from ancestor paths (/, /sdn, /sdn/zones)")
	return cmd
}

// newZonePermEffectiveCmd builds `pmx sdn zone permissions effective <zone>`.
func newZonePermEffectiveCmd() *cobra.Command {
	var userid string
	cmd := &cobra.Command{
		Use:   "effective <zone>",
		Short: "Show effective permissions on an SDN zone",
		Long: "Show the effective privileges the calling user (or --userid, if passed) holds on " +
			"the zone's ACL path (/sdn/zones/{zone}), after role and ACL propagation is resolved " +
			"server-side. Querying another user's or token's effective permissions with --userid " +
			"needs Sys.Audit on /access.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			return runPermEffective(cmd, deps, zonePermPath(args[0]), userid, cmd.Flags().Changed("userid"))
		},
	}
	cmd.Flags().StringVar(&userid, "userid", "",
		"query effective permissions for this user/token instead of the caller (requires Sys.Audit on /access)")
	return cmd
}

// newZonePermGrantRevokeCmd builds `pmx sdn zone permissions grant <zone>` when
// revoke is false, or `... revoke <zone>` when revoke is true.
func newZonePermGrantRevokeCmd(revoke bool) *cobra.Command {
	var (
		roles       string
		users       string
		groups      string
		tokens      string
		noPropagate bool
	)

	use, short, long := "grant <zone>",
		"Grant roles to users, groups, or tokens on an SDN zone",
		"Grant one or more roles on the zone's ACL path (/sdn/zones/{zone}) to the given users, "+
			"groups, and/or tokens (at least one of --users/--groups/--tokens is required). "+
			"Needs Permissions.Modify on the path. By default the granted roles propagate to any "+
			"path nested under the zone (including its vnets); pass --no-propagate to grant "+
			"non-propagating access instead."
	if revoke {
		use, short, long = "revoke <zone>",
			"Revoke roles from users, groups, or tokens on an SDN zone",
			"Revoke one or more roles on the zone's ACL path (/sdn/zones/{zone}) from the given "+
				"users, groups, and/or tokens (at least one of --users/--groups/--tokens is "+
				"required). Needs Permissions.Modify on the path. Revoking a role that was never "+
				"granted at this exact path silently succeeds. Nothing here stops an operator from "+
				"revoking their own access to this path (self-lockout): double-check --users before "+
				"running this against your own account."
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireSubjects(users, groups, tokens); err != nil {
				return err
			}
			return runPermGrantRevoke(cmd, deps, zonePermPath(args[0]), roles, users, groups, tokens, noPropagate, revoke)
		},
	}
	f := cmd.Flags()
	f.StringVar(&roles, "roles", "", "comma-separated role list (required)")
	f.StringVar(&users, "users", "", "comma-separated user list")
	f.StringVar(&groups, "groups", "", "comma-separated group list")
	f.StringVar(&tokens, "tokens", "", "comma-separated API token list (user@realm!token)")
	f.BoolVar(&noPropagate, "no-propagate", false, "do not propagate these roles to paths below the zone's ACL path")
	cli.MustMarkRequired(cmd, "roles")
	return cmd
}

// ---- vnet permissions --------------------------------------------------------

// vnetPermPath derives an SDN vnet's ACL path: /sdn/zones/{zone}/{vnet}. Vnets
// have no independent ACL identity of their own; every vnet permission nests
// under its zone.
func vnetPermPath(zone, vnet string) string {
	return fmt.Sprintf("/sdn/zones/%s/%s", zone, vnet)
}

// resolveVnetZone returns zoneFlag unchanged when non-empty (the caller
// already knows the zone, so the lookup below is skipped entirely — one
// fewer API round-trip). Otherwise it resolves vnet's zone via
// GET /cluster/sdn/vnets/{vnet} (Cluster.GetSdnVnets).
func resolveVnetZone(ctx context.Context, deps *cli.Deps, vnet, zoneFlag string) (string, error) {
	if zoneFlag != "" {
		return zoneFlag, nil
	}

	resp, err := deps.API.Cluster.GetSdnVnets(ctx, vnet, &cluster.GetSdnVnetsParams{})
	if err != nil {
		return "", fmt.Errorf("resolve zone for vnet %q: %w", vnet, err)
	}
	if resp == nil {
		return "", fmt.Errorf("resolve zone for vnet %q: empty response (vnet may not exist)", vnet)
	}
	var v struct {
		Zone string `json:"zone"`
	}
	if err := json.Unmarshal(*resp, &v); err != nil {
		return "", fmt.Errorf("decode vnet %q: %w", vnet, err)
	}
	if v.Zone == "" {
		return "", fmt.Errorf("resolve zone for vnet %q: response carried no zone (vnet may not exist)", vnet)
	}
	return v.Zone, nil
}

// newVnetPermissionsCmd builds `pmx sdn vnet permissions` and its sub-commands:
// list/effective/grant/revoke against a vnet's derived ACL path
// (/sdn/zones/{zone}/{vnet}).
func newVnetPermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Manage ACL entries and view effective permissions on an SDN vnet",
		Long: "List, grant, and revoke ACL entries on an SDN vnet's derived access-control path " +
			"(/sdn/zones/{zone}/{vnet}), and view the effective permission set a user or token " +
			"holds there. Vnets have no independent ACL identity: their path nests under their " +
			"zone. Pass --zone to skip the extra GET /cluster/sdn/vnets/{vnet} lookup this command " +
			"otherwise performs to resolve the vnet's zone automatically. This is a thin, " +
			"path-deriving wrapper over `pmx pve access acl` and `pmx pve access permissions`; use those " +
			"directly for any path outside a single vnet.",
	}
	cmd.AddCommand(
		newVnetPermListCmd(),
		newVnetPermEffectiveCmd(),
		newVnetPermGrantRevokeCmd(false),
		newVnetPermGrantRevokeCmd(true),
	)
	return cmd
}

// newVnetPermListCmd builds `pmx sdn vnet permissions list <vnet>`.
func newVnetPermListCmd() *cobra.Command {
	var (
		inherited bool
		zoneFlag  string
	)
	cmd := &cobra.Command{
		Use:   "list <vnet>",
		Short: "List ACL entries on an SDN vnet",
		Long: "List ACL entries whose path exactly matches the vnet's derived ACL path " +
			"(/sdn/zones/{zone}/{vnet}). Pass --inherited to also include entries inherited from " +
			"the vnet's ancestor paths (/, /sdn, /sdn/zones, /sdn/zones/{zone}); this walks the " +
			"path client-side and issues no extra API calls beyond the zone lookup below. Pass " +
			"--zone to skip the GET /cluster/sdn/vnets/{vnet} lookup that otherwise auto-resolves " +
			"the vnet's zone. Needs the same privilege as `pmx pve access acl list`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			zone, err := resolveVnetZone(cmd.Context(), deps, vnet, zoneFlag)
			if err != nil {
				return err
			}
			return runPermList(cmd, deps, vnetPermPath(zone, vnet), inherited)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&inherited, "inherited", false,
		"also include ACL entries inherited from ancestor paths (/, /sdn, /sdn/zones, /sdn/zones/{zone})")
	f.StringVar(&zoneFlag, "zone", "", "zone the vnet belongs to (skips the auto-resolve lookup)")
	return cmd
}

// newVnetPermEffectiveCmd builds `pmx sdn vnet permissions effective <vnet>`.
func newVnetPermEffectiveCmd() *cobra.Command {
	var (
		userid   string
		zoneFlag string
	)
	cmd := &cobra.Command{
		Use:   "effective <vnet>",
		Short: "Show effective permissions on an SDN vnet",
		Long: "Show the effective privileges the calling user (or --userid, if passed) holds on " +
			"the vnet's derived ACL path (/sdn/zones/{zone}/{vnet}), after role and ACL " +
			"propagation is resolved server-side. Pass --zone to skip the " +
			"GET /cluster/sdn/vnets/{vnet} lookup that otherwise auto-resolves the vnet's zone. " +
			"Querying another user's or token's effective permissions with --userid needs " +
			"Sys.Audit on /access.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			zone, err := resolveVnetZone(cmd.Context(), deps, vnet, zoneFlag)
			if err != nil {
				return err
			}
			return runPermEffective(cmd, deps, vnetPermPath(zone, vnet), userid, cmd.Flags().Changed("userid"))
		},
	}
	f := cmd.Flags()
	f.StringVar(&userid, "userid", "",
		"query effective permissions for this user/token instead of the caller (requires Sys.Audit on /access)")
	f.StringVar(&zoneFlag, "zone", "", "zone the vnet belongs to (skips the auto-resolve lookup)")
	return cmd
}

// newVnetPermGrantRevokeCmd builds `pmx sdn vnet permissions grant <vnet>` when
// revoke is false, or `... revoke <vnet>` when revoke is true.
func newVnetPermGrantRevokeCmd(revoke bool) *cobra.Command {
	var (
		roles       string
		users       string
		groups      string
		tokens      string
		noPropagate bool
		zoneFlag    string
	)

	use, short, long := "grant <vnet>",
		"Grant roles to users, groups, or tokens on an SDN vnet",
		"Grant one or more roles on the vnet's derived ACL path (/sdn/zones/{zone}/{vnet}) to "+
			"the given users, groups, and/or tokens (at least one of --users/--groups/--tokens is "+
			"required). Pass --zone to skip the GET /cluster/sdn/vnets/{vnet} lookup that "+
			"otherwise auto-resolves the vnet's zone. Needs Permissions.Modify on the path. By "+
			"default the granted roles propagate to any path nested under the vnet; pass "+
			"--no-propagate to grant non-propagating access instead."
	if revoke {
		use, short, long = "revoke <vnet>",
			"Revoke roles from users, groups, or tokens on an SDN vnet",
			"Revoke one or more roles on the vnet's derived ACL path (/sdn/zones/{zone}/{vnet}) "+
				"from the given users, groups, and/or tokens (at least one of "+
				"--users/--groups/--tokens is required). Pass --zone to skip the "+
				"GET /cluster/sdn/vnets/{vnet} lookup that otherwise auto-resolves the vnet's "+
				"zone. Needs Permissions.Modify on the path. Revoking a role that was never "+
				"granted at this exact path silently succeeds. Nothing here stops an operator "+
				"from revoking their own access to this path (self-lockout): double-check "+
				"--users before running this against your own account."
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			if err := requireSubjects(users, groups, tokens); err != nil {
				return err
			}
			zone, err := resolveVnetZone(cmd.Context(), deps, vnet, zoneFlag)
			if err != nil {
				return err
			}
			return runPermGrantRevoke(cmd, deps, vnetPermPath(zone, vnet), roles, users, groups, tokens, noPropagate, revoke)
		},
	}
	f := cmd.Flags()
	f.StringVar(&roles, "roles", "", "comma-separated role list (required)")
	f.StringVar(&users, "users", "", "comma-separated user list")
	f.StringVar(&groups, "groups", "", "comma-separated group list")
	f.StringVar(&tokens, "tokens", "", "comma-separated API token list (user@realm!token)")
	f.BoolVar(&noPropagate, "no-propagate", false, "do not propagate these roles to paths below the vnet's ACL path")
	f.StringVar(&zoneFlag, "zone", "", "zone the vnet belongs to (skips the auto-resolve lookup)")
	cli.MustMarkRequired(cmd, "roles")
	return cmd
}

// ---- shared verb mechanics ----------------------------------------------------

// requireSubjects reports an error unless at least one of users, groups, or
// tokens is non-empty. The server also enforces this, but pre-validating here
// gives a cleaner error before any request is sent.
func requireSubjects(users, groups, tokens string) error {
	if users == "" && groups == "" && tokens == "" {
		return fmt.Errorf("at least one of --users, --groups, or --tokens is required")
	}
	return nil
}

// runPermList fetches the full ACL table, filters it to path (and, when
// inherited is true, to every ancestor of path per permshared.ParentChain),
// and renders the result.
func runPermList(cmd *cobra.Command, deps *cli.Deps, path string, inherited bool) error {
	resp, err := deps.API.Access.ListAcl(cmd.Context())
	if err != nil {
		return fmt.Errorf("list acl: %w", err)
	}
	entries, err := permshared.DecodeAclList(resp)
	if err != nil {
		return err
	}

	var filtered []permshared.AclEntry
	if inherited {
		for _, p := range permshared.ParentChain(path) {
			filtered = append(filtered, permshared.FilterByPath(entries, p, true)...)
		}
	} else {
		filtered = permshared.FilterByPath(entries, path, true)
	}

	return deps.Out.Render(cmd.OutOrStdout(), permshared.RenderAclList(filtered, inherited), deps.Format)
}

// runPermEffective fetches the effective-permissions tree for path (and,
// when useridChanged is true, for userid instead of the calling user) and
// renders it.
func runPermEffective(cmd *cobra.Command, deps *cli.Deps, path, userid string, useridChanged bool) error {
	params := &access.ListPermissionsParams{Path: &path}
	if useridChanged {
		params.Userid = &userid
	}
	resp, err := deps.API.Access.ListPermissions(cmd.Context(), params)
	if err != nil {
		return fmt.Errorf("list permissions for %q: %w", path, err)
	}
	tree, err := permshared.DecodePermissions(resp)
	if err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), permshared.RenderEffective(tree), deps.Format)
}

// runPermGrantRevoke sends the UpdateAcl request granting or revoking roles
// on path for the given comma-separated subject lists, and renders the
// resulting message.
func runPermGrantRevoke(
	cmd *cobra.Command, deps *cli.Deps,
	path, roles, users, groups, tokens string,
	noPropagate, revoke bool,
) error {
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

	params := permshared.GrantRevokeParams(path, roles, usersPtr, groupsPtr, tokensPtr, propagate, revoke)
	if err := deps.API.Access.UpdateAcl(cmd.Context(), params); err != nil {
		verb := "grant"
		if revoke {
			verb = "revoke"
		}
		return fmt.Errorf("%s roles on %q: %w", verb, path, err)
	}

	verbed, prep := "Granted", "to"
	if revoke {
		verbed, prep = "Revoked", "from"
	}
	subjects := make([]string, 0, 3)
	if users != "" {
		subjects = append(subjects, "users "+users)
	}
	if groups != "" {
		subjects = append(subjects, "groups "+groups)
	}
	if tokens != "" {
		subjects = append(subjects, "tokens "+tokens)
	}
	msg := fmt.Sprintf("%s roles %s %s %s on %s.", verbed, roles, prep, strings.Join(subjects, ", "), path)
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}
