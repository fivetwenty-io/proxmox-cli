package lab

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/pools"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
)

// pmxAdminRoleID is the fleet-wide role every human staff member holds on
// every lab's resource pool, and the default `access grant` role when neither --role
// nor the lab's own access.role names one. It is the only role this command
// creates on the caller's behalf when missing: any other role must already
// exist on the target, since this command has no basis for guessing what
// privileges a role the operator named should carry.
const pmxAdminRoleID = "PMXAdmin"

// pmxAdminPrivs is the privilege list backing pmxAdminRoleID, chosen so
// the role stays pool-scoped. It grants VM
// lifecycle, storage-allocation, pool-audit, and SDN-use privileges scoped
// to whatever pool the role is granted on; it grants nothing at the
// datacenter, storage-configuration, or SDN-configuration level.
const pmxAdminPrivs = "VM.Allocate,VM.Clone,VM.Config.CDROM,VM.Config.CPU,VM.Config.Cloudinit," +
	"VM.Config.Disk,VM.Config.HWType,VM.Config.Memory,VM.Config.Network,VM.Config.Options," +
	"VM.Console,VM.Monitor,VM.PowerMgmt,VM.Audit,VM.Snapshot,VM.Snapshot.Rollback,VM.Backup," +
	"VM.Migrate,Datastore.Audit,Datastore.Allocate,Datastore.AllocateSpace,Datastore.AllocateTemplate," +
	"Pool.Audit,SDN.Use,Sys.Audit"

// newAccessCmd builds `pmx lab access` and its subcommands.
func newAccessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Manage a lab's pve access grants",
		Long: "Grant a pve-realm user pool-scoped access to a lab, creating the lab's " +
			"resource pool, the granted user's account, and the granted role along the way " +
			"when any of them is missing.",
	}
	cmd.AddCommand(newAccessGrantCmd())
	return cmd
}

// newAccessGrantCmd builds `pmx lab access grant <name> <user>`.
func newAccessGrantCmd() *cobra.Command {
	var role string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "grant <name> <user>",
		Short: "Grant a pve user pool-scoped access to a lab",
		Long: "Grant a user (a pve-realm userid, e.g. wayne@pve) a role scoped to the named " +
			"lab's resource pool. Ensures, in order, that the lab's pool exists (creating it " +
			"if absent), that the user's account exists (creating it from the config's " +
			"default_user_password if absent — refusing with guidance when that password " +
			"is not configured), and that the effective role exists (PMXAdmin is created " +
			"automatically when absent; any other " +
			"role must already exist), then grants the role to the user on the pool via an " +
			"ACL entry. The effective role is --role when given, else the lab's own " +
			"access.role, else PMXAdmin.\n\n" +
			"This grants the single named user only; it never loops over every staff " +
			"member. The pipelines automation account (pipelines@pve) is the standing " +
			"counter-example: it is granted PVEVMUser scoped to its own lab's pool, never " +
			"PMXAdmin, and is never added to any cross-lab grant.",
		Example: `  pmx lab access grant wayne wayne@pve
  pmx lab access grant pipeline pipelines@pve --role PVEVMUser
  pmx lab access grant wayne wayne@pve --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccessGrant(cmd, args[0], args[1], role, dryRun)
		},
	}

	cmd.Flags().StringVar(&role, "role", "",
		"role granted to the user on the lab's pool (default: the lab's access.role, else PMXAdmin)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the grant without mutating anything")

	return cmd
}

// runAccessGrant resolves and peppi-guards name via resolveLabForMutate,
// determines the effective role (--role when passed, else lab.Access.Role, else
// pmxAdminRoleID), checks the live state of the lab's pool, user, and role,
// then — unless dryRun — creates whichever of the pool/user/role is missing
// and grants the role to user on the pool via an ACL entry. The ordered plan
// is rendered exactly once, via deps.Out.Render (respecting -o json/yaml
// like every other lab verb), with the configured password always redacted:
// a preview render for dry-run, or the completed plan after every mutation
// for an apply run. Every state check happens before any mutation, so both
// runs describe the same accurate picture of what will (or did) happen.
func runAccessGrant(cmd *cobra.Command, name, user, roleFlag string, dryRun bool) error {
	deps := cli.GetDeps(cmd)

	lab, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	role := lab.Access.Role
	if cmd.Flags().Changed("role") {
		role = roleFlag
	}
	if role == "" {
		role = pmxAdminRoleID
	}

	poolID := labPoolID(lab)
	ctx := cmd.Context()

	poolFound, err := accessPoolExists(ctx, deps.API, poolID)
	if err != nil {
		return fmt.Errorf("check pool %q: %w", poolID, err)
	}

	userFound, err := accessUserExists(ctx, deps.API, user)
	if err != nil {
		return fmt.Errorf("check user %q: %w", user, err)
	}

	var password string
	if deps.Cfg != nil {
		password = deps.Cfg.DefaultUserPassword
	}
	if !userFound && password == "" {
		return fmt.Errorf(
			"user %q does not exist and default_user_password is not configured in config.yml; "+
				"set default_user_password, or create %q manually first with "+
				"`pmx pve access user create`", user, user)
	}

	roleFound, err := accessRoleExists(ctx, deps.API, role)
	if err != nil {
		return fmt.Errorf("check role %q: %w", role, err)
	}
	if !roleFound && role != pmxAdminRoleID {
		return fmt.Errorf(
			"role %q does not exist; create it first with `pmx pve access role create` "+
				"(only %q is created automatically)", role, pmxAdminRoleID)
	}

	if dryRun {
		plan := buildAccessPlanResult(dryRun, poolID, poolFound, user, userFound, redact.Password(password), role, roleFound,
			fmt.Sprintf("[dry-run] lab %q: would grant role %q to user %q on pool %q", name, role, user, poolID))
		return deps.Out.Render(cmd.OutOrStdout(), plan, deps.Format)
	}

	if !poolFound {
		if err := deps.API.Pools.CreatePools(ctx, &pools.CreatePoolsParams{Poolid: poolID}); err != nil {
			return fmt.Errorf("create pool %q: %w", poolID, err)
		}
	}

	if !userFound {
		pw := password
		if err := deps.API.Access.CreateUsers(ctx, &access.CreateUsersParams{Userid: user, Password: &pw}); err != nil {
			return fmt.Errorf("create user %q: %w", user, err)
		}
	}

	if !roleFound {
		privs := pmxAdminPrivs
		if err := deps.API.Access.CreateRoles(ctx, &access.CreateRolesParams{Roleid: role, Privs: &privs}); err != nil {
			return fmt.Errorf("create role %q: %w", role, err)
		}
	}

	path := fmt.Sprintf("/pool/%s", poolID)
	users := user
	if err := deps.API.Access.UpdateAcl(ctx, &access.UpdateAclParams{Path: path, Roles: role, Users: &users}); err != nil {
		return fmt.Errorf("grant role %q to user %q on pool %q: %w", role, user, poolID, err)
	}

	plan := buildAccessPlanResult(dryRun, poolID, poolFound, user, userFound, redact.Password(password), role, roleFound,
		fmt.Sprintf("lab %q: granted role %q to user %q on pool %q", name, role, user, poolID))
	return deps.Out.Render(cmd.OutOrStdout(), plan, deps.Format)
}

// buildAccessPlanResult builds the pool/user/role/grant plan as a structured
// output.Result (STEP/STATUS rows, mirroring create's plan rendering) so
// `-o json`/`-o yaml` emit valid structured data for both the dry-run
// preview and the completed apply. redactedPassword is redact.Password's
// output for the configured default_user_password: never the raw secret,
// and blank when no password is configured. It is folded into the user
// step's STEP cell only when the user does not already exist, in both
// dry-run and apply runs, so a create-user step is never silently missing
// from the output — the password value itself never appears in any cell.
func buildAccessPlanResult(
	dryRun bool,
	poolID string, poolFound bool,
	user string, userFound bool, redactedPassword string,
	role string, roleFound bool,
	msg string,
) output.Result {
	userStep := fmt.Sprintf("user %q", user)
	if !userFound && redactedPassword != "" {
		userStep = fmt.Sprintf("user %q (--password %s)", user, redactedPassword)
	}

	rows := [][]string{
		{fmt.Sprintf("pool %q", poolID), accessStepVerb(poolFound, dryRun)},
		{userStep, accessStepVerb(userFound, dryRun)},
		{fmt.Sprintf("role %q", role), accessStepVerb(roleFound, dryRun)},
		{fmt.Sprintf("grant role %q to user %q on pool %q", role, user, poolID), accessGrantStepVerb(dryRun)},
	}

	return output.Result{Headers: []string{"STEP", "STATUS"}, Rows: rows, Message: msg}
}

// accessGrantStepVerb describes the final ACL-grant step's status: "would be
// applied" for a dry-run preview, "applied" once UpdateAcl has actually run.
func accessGrantStepVerb(dryRun bool) string {
	if dryRun {
		return "would be applied"
	}
	return "applied"
}

// accessStepVerb describes one plan step's status: "already exists (skip)"
// when the resource is already present, "would be created" for a dry-run
// preview of a missing resource, or "created" for an apply run that is
// about to create it.
func accessStepVerb(found, dryRun bool) string {
	if found {
		return "already exists (skip)"
	}
	if dryRun {
		return "would be created"
	}
	return "created"
}

// accessPoolEntry is the subset of a /pools list element access grant reads
// to decide whether the lab's pool already exists.
type accessPoolEntry struct {
	Poolid string `json:"poolid"`
}

// accessPoolExists reports whether poolID already exists, via GET
// /pools?poolid=<id>: an empty result list means absent, at least one
// element means present.
func accessPoolExists(ctx context.Context, api *apiclient.APIClient, poolID string) (bool, error) {
	resp, err := api.Pools.ListPools(ctx, &pools.ListPoolsParams{Poolid: &poolID})
	if err != nil {
		return false, fmt.Errorf("list pools: %w", err)
	}
	if resp == nil {
		return false, nil
	}
	for _, raw := range *resp {
		var e accessPoolEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, fmt.Errorf("decode pool entry: %w", err)
		}
		if e.Poolid == poolID {
			return true, nil
		}
	}
	return false, nil
}

// accessUserEntry is the subset of a /access/users list element access
// grant reads to decide whether the named user already exists.
type accessUserEntry struct {
	Userid string `json:"userid"`
}

// accessUserExists reports whether userid already exists, via GET
// /access/users, scanning every entry for an exact userid match.
func accessUserExists(ctx context.Context, api *apiclient.APIClient, userid string) (bool, error) {
	resp, err := api.Access.ListUsers(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("list users: %w", err)
	}
	if resp == nil {
		return false, nil
	}
	for _, raw := range *resp {
		var e accessUserEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, fmt.Errorf("decode user entry: %w", err)
		}
		if e.Userid == userid {
			return true, nil
		}
	}
	return false, nil
}

// accessRoleEntry is the subset of a /access/roles list element access
// grant reads to decide whether the effective role already exists.
type accessRoleEntry struct {
	Roleid string `json:"roleid"`
}

// accessRoleExists reports whether roleid already exists (built-in or
// custom), via GET /access/roles, scanning every entry for an exact roleid
// match.
func accessRoleExists(ctx context.Context, api *apiclient.APIClient, roleid string) (bool, error) {
	resp, err := api.Access.ListRoles(ctx)
	if err != nil {
		return false, fmt.Errorf("list roles: %w", err)
	}
	if resp == nil {
		return false, nil
	}
	for _, raw := range *resp {
		var e accessRoleEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, fmt.Errorf("decode role entry: %w", err)
		}
		if e.Roleid == roleid {
			return true, nil
		}
	}
	return false, nil
}
