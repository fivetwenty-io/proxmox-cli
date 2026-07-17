package lab

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// accessTestCmd builds a bare cobra.Command carrying a *cli.Deps loaded from
// configPath (via newCmdWithDeps, the shared package helper), plus the same
// --role/--dry-run flags newAccessGrantCmd registers, so
// cmd.Flags().Changed("role") reflects real flag-parsing semantics rather
// than a hardcoded bool.
func accessTestCmd(t *testing.T, configPath string, flagArgs ...string) *cobra.Command {
	t.Helper()

	cmd := newCmdWithDeps(t, configPath)
	cmd.Flags().String("role", "", "")
	cmd.Flags().Bool("dry-run", false, "")
	require.NoError(t, cmd.Flags().Parse(flagArgs))

	return cmd
}

// accessFlagValues reads back the two flags accessTestCmd registers, in the
// shape runAccessGrant's positional parameters expect.
func accessFlagValues(t *testing.T, cmd *cobra.Command) (role string, dryRun bool) {
	t.Helper()

	var err error
	role, err = cmd.Flags().GetString("role")
	require.NoError(t, err)
	dryRun, err = cmd.Flags().GetBool("dry-run")
	require.NoError(t, err)
	return role, dryRun
}

// wireAccessDeps attaches a real *apiclient.APIClient pointed at f (a
// FakePVE server) to cmd's already-loaded *cli.Deps (mutating the same
// pointer newCmdWithDeps stashed in cmd's context), an output.Renderer, and
// wires cmd's out/err streams to the given writers. stdout and stderr may
// be the same *bytes.Buffer when a test needs combined output.
func wireAccessDeps(t *testing.T, cmd *cobra.Command, f *testhelper.FakePVE, stdout, stderr *bytes.Buffer) {
	t.Helper()

	deps := cli.GetDeps(cmd)
	ac, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps.API = ac
	deps.Out = output.New()
	deps.Format = output.FormatPlain

	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
}

// accessCreateUserCall records one POST /access/users request's userid and
// password fields.
type accessCreateUserCall struct {
	userid   string
	password string
}

// accessCreateRoleCall records one POST /access/roles request's roleid and
// privs fields.
type accessCreateRoleCall struct {
	roleid string
	privs  string
}

// accessUpdateAclCall records one PUT /access/acl request's path, roles, and
// users fields.
type accessUpdateAclCall struct {
	path  string
	roles string
	users string
}

// accessFakeState is the in-memory pool/user/role existence state a
// FakePVE's registered routes read and mutate, plus every mutating call
// recorded for test assertions.
type accessFakeState struct {
	pools map[string]bool
	users map[string]bool
	roles map[string]bool

	createPoolCalls []string
	createUserCalls []accessCreateUserCall
	createRoleCalls []accessCreateRoleCall
	updateAclCalls  []accessUpdateAclCall
}

// newAccessFakeState builds an accessFakeState pre-seeded with the given
// already-existing pool IDs, userids, and roleids.
func newAccessFakeState(existingPools, existingUsers, existingRoles []string) *accessFakeState {
	s := &accessFakeState{
		pools: make(map[string]bool),
		users: make(map[string]bool),
		roles: make(map[string]bool),
	}
	for _, p := range existingPools {
		s.pools[p] = true
	}
	for _, u := range existingUsers {
		s.users[u] = true
	}
	for _, r := range existingRoles {
		s.roles[r] = true
	}
	return s
}

// registerAccessFakeRoutes wires f's routes for every endpoint access grant
// touches (pools list/create, access users list/create, access roles
// list/create, access acl update) against the given accessFakeState: GET
// routes reflect current state, POST/PUT routes mutate it and append to the
// matching call-recording slice.
func registerAccessFakeRoutes(t *testing.T, f *testhelper.FakePVE, s *accessFakeState) {
	t.Helper()

	f.HandleFunc("GET /api2/json/pools", func(w http.ResponseWriter, r *http.Request) {
		poolid := r.URL.Query().Get("poolid")
		rows := []any{}
		if s.pools[poolid] {
			rows = append(rows, map[string]any{"poolid": poolid, "comment": "", "members": []any{}})
		}
		testhelper.WriteData(w, rows)
	})

	f.HandleFunc("POST /api2/json/pools", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		poolid := r.PostForm.Get("poolid")
		s.pools[poolid] = true
		s.createPoolCalls = append(s.createPoolCalls, poolid)
		testhelper.WriteData(w, nil)
	})

	f.HandleFunc("GET /api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		rows := []any{}
		for u := range s.users {
			rows = append(rows, map[string]any{"userid": u})
		}
		testhelper.WriteData(w, rows)
	})

	f.HandleFunc("POST /api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		userid := r.PostForm.Get("userid")
		password := r.PostForm.Get("password")
		s.users[userid] = true
		s.createUserCalls = append(s.createUserCalls, accessCreateUserCall{userid: userid, password: password})
		testhelper.WriteData(w, nil)
	})

	f.HandleFunc("GET /api2/json/access/roles", func(w http.ResponseWriter, r *http.Request) {
		rows := []any{}
		for rid := range s.roles {
			rows = append(rows, map[string]any{"roleid": rid})
		}
		testhelper.WriteData(w, rows)
	})

	f.HandleFunc("POST /api2/json/access/roles", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		roleid := r.PostForm.Get("roleid")
		privs := r.PostForm.Get("privs")
		s.roles[roleid] = true
		s.createRoleCalls = append(s.createRoleCalls, accessCreateRoleCall{roleid: roleid, privs: privs})
		testhelper.WriteData(w, nil)
	})

	f.HandleFunc("PUT /api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		s.updateAclCalls = append(s.updateAclCalls, accessUpdateAclCall{
			path:  r.PostForm.Get("path"),
			roles: r.PostForm.Get("roles"),
			users: r.PostForm.Get("users"),
		})
		testhelper.WriteData(w, nil)
	})
}

func TestAccessGrant_FreshGrant_CreatesPoolUserRoleAndGrantsAcl(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"), // Access.Pool "lab-alpha", Access.Role "PMXAdmin"
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(nil, nil, nil)
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	require.Equal(t, []string{"lab-alpha"}, state.createPoolCalls)
	require.Len(t, state.createUserCalls, 1)
	assert.Equal(t, "alice@pve", state.createUserCalls[0].userid)
	assert.Equal(t, fakeDefaultUserPassword, state.createUserCalls[0].password)
	require.Len(t, state.createRoleCalls, 1)
	assert.Equal(t, "PMXAdmin", state.createRoleCalls[0].roleid)
	assert.Equal(t, pmxAdminPrivs, state.createRoleCalls[0].privs)
	require.Len(t, state.updateAclCalls, 1)
	assert.Equal(t, "/pool/lab-alpha", state.updateAclCalls[0].path)
	assert.Equal(t, "PMXAdmin", state.updateAclCalls[0].roles)
	assert.Equal(t, "alice@pve", state.updateAclCalls[0].users)
}

func TestAccessGrant_Idempotent_NoCreateCallsButAclEnsured(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState([]string{"lab-alpha"}, []string{"alice@pve"}, []string{"PMXAdmin"})
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	assert.Empty(t, state.createPoolCalls)
	assert.Empty(t, state.createUserCalls)
	assert.Empty(t, state.createRoleCalls)
	require.Len(t, state.updateAclCalls, 1)
	assert.Equal(t, "/pool/lab-alpha", state.updateAclCalls[0].path)
	assert.Equal(t, "PMXAdmin", state.updateAclCalls[0].roles)
	assert.Equal(t, "alice@pve", state.updateAclCalls[0].users)
}

func TestAccessGrant_RoleFlagOverridesConfig(t *testing.T) {
	lab := cleanLab("alpha")
	lab.Access.Role = "ConfigRole"
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": lab},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path, "--role=FlagRole")

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(
		[]string{"lab-alpha"}, []string{"alice@pve"}, []string{"ConfigRole", "FlagRole"})
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	require.Len(t, state.updateAclCalls, 1)
	assert.Equal(t, "FlagRole", state.updateAclCalls[0].roles)
	assert.Empty(t, state.createRoleCalls)
}

func TestAccessGrant_ConfigRoleUsedWhenFlagAbsent(t *testing.T) {
	lab := cleanLab("alpha")
	lab.Access.Role = "ConfigRole"
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": lab},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState([]string{"lab-alpha"}, []string{"alice@pve"}, []string{"ConfigRole"})
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	require.Len(t, state.updateAclCalls, 1)
	assert.Equal(t, "ConfigRole", state.updateAclCalls[0].roles)
}

func TestAccessGrant_DefaultRoleWhenNeitherFlagNorConfigSet(t *testing.T) {
	lab := cleanLab("alpha")
	lab.Access.Role = ""
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"alpha": lab},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState([]string{"lab-alpha"}, []string{"alice@pve"}, nil)
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	require.Len(t, state.updateAclCalls, 1)
	assert.Equal(t, "PMXAdmin", state.updateAclCalls[0].roles)
	require.Len(t, state.createRoleCalls, 1)
	assert.Equal(t, "PMXAdmin", state.createRoleCalls[0].roleid)
}

func TestAccessGrant_Redaction_SecretAbsentFromDryRunOutput(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path, "--dry-run")

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(nil, nil, nil) // user missing -> password line printed
	registerAccessFakeRoutes(t, f, state)

	var combined bytes.Buffer
	wireAccessDeps(t, cmd, f, &combined, &combined)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	out := combined.String()
	assert.NotContains(t, out, fakeDefaultUserPassword)
	assert.Contains(t, out, redact.Placeholder)

	// Dry-run must not have created anything.
	assert.Empty(t, state.createPoolCalls)
	assert.Empty(t, state.createUserCalls)
	assert.Empty(t, state.createRoleCalls)
	assert.Empty(t, state.updateAclCalls)
}

func TestAccessGrant_Redaction_SecretAbsentFromApplyOutput(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(nil, nil, nil) // user missing -> password line printed and used
	registerAccessFakeRoutes(t, f, state)

	var combined bytes.Buffer
	wireAccessDeps(t, cmd, f, &combined, &combined)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	out := combined.String()
	assert.NotContains(t, out, fakeDefaultUserPassword)
	assert.Contains(t, out, redact.Placeholder)

	// The real secret must still have reached the API request, even though
	// it never reached the rendered output.
	require.Len(t, state.createUserCalls, 1)
	assert.Equal(t, fakeDefaultUserPassword, state.createUserCalls[0].password)
}

func TestAccessGrant_DryRun_NoMutatingCalls(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path, "--dry-run")

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(nil, nil, nil)
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	assert.Empty(t, state.createPoolCalls)
	assert.Empty(t, state.createUserCalls)
	assert.Empty(t, state.createRoleCalls)
	assert.Empty(t, state.updateAclCalls)
}

func TestAccessGrant_PeppiGuard_ProtectedPoolRefused(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.Access.Pool = "peppiprd-pool"
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"dirty": dirty},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	// No FakePVE/API wiring: resolveLabForMutate must refuse before any API
	// call is attempted, so deps.API is left nil deliberately.
	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "dirty", "alice@pve", role, dryRun)
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppiprd")
}

func TestAccessGrant_UserMissingWithNoDefaultPassword_Errors(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState([]string{"lab-alpha"}, nil, []string{"PMXAdmin"}) // user missing
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.Error(t, err)
	assert.ErrorContains(t, err, "default_user_password")
	assert.Empty(t, state.createUserCalls)
	assert.Empty(t, state.updateAclCalls)
}

// accessJSONResult mirrors output.tableJSON's unexported shape (headers/rows)
// well enough to decode `-o json`'s synthetic table object for assertions,
// without exporting that internal type from the output package.
type accessJSONResult struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// TestAccessGrant_JSONFormat_DryRunEmitsStructuredPlanNoPassword covers the
// -o json contract for a dry-run grant: the entire rendered output must be
// one valid JSON document (not plain lines interleaved with a JSON blob),
// its rows must carry the pool/user/role/grant plan, and the raw
// default_user_password must never appear anywhere in it.
func TestAccessGrant_JSONFormat_DryRunEmitsStructuredPlanNoPassword(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path, "--dry-run")

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(nil, nil, nil) // pool/user/role all missing
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)
	cli.GetDeps(cmd).Format = output.FormatJSON

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	out := stdout.String()
	assert.NotContains(t, out, fakeDefaultUserPassword)
	// Note: redact.Placeholder ("<redacted>") is checked post-decode below,
	// not against the raw JSON text: encoding/json HTML-escapes '<'/'>' by
	// default, so the raw bytes never contain the literal placeholder string.

	var decoded accessJSONResult
	require.NoError(t, json.Unmarshal([]byte(out), &decoded), "-o json output must be a single valid JSON document")
	require.Len(t, decoded.Headers, 2)
	require.Len(t, decoded.Rows, 5, "expected pool/user/role/grant plan rows plus the summary row")
	summaryRow := decoded.Rows[4]
	require.Equal(t, "summary", summaryRow[0])
	assert.Contains(t, summaryRow[1], "would grant", "the dry-run summary must reach the operator as a row")

	var sawPasswordCell bool
	for _, row := range decoded.Rows {
		for _, cell := range row {
			assert.NotContains(t, cell, fakeDefaultUserPassword)
			if strings.Contains(cell, redact.Placeholder) {
				sawPasswordCell = true
			}
		}
	}
	assert.True(t, sawPasswordCell, "the user step's row must carry the redacted password placeholder")

	assert.Empty(t, state.createPoolCalls, "dry-run must not mutate anything")
	assert.Empty(t, state.createUserCalls)
}

// TestAccessGrant_JSONFormat_ApplyEmitsStructuredPlanNoPassword covers the
// same -o json contract for a completed (non-dry-run) grant: the plan must
// still render as one valid JSON document, even though the pool/user/role
// creation and ACL grant have all already happened by the time it renders.
func TestAccessGrant_JSONFormat_ApplyEmitsStructuredPlanNoPassword(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(nil, nil, nil) // pool/user/role all missing
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)
	cli.GetDeps(cmd).Format = output.FormatJSON

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	out := stdout.String()
	assert.NotContains(t, out, fakeDefaultUserPassword)

	var decoded accessJSONResult
	require.NoError(t, json.Unmarshal([]byte(out), &decoded), "-o json output must be a single valid JSON document")
	require.Len(t, decoded.Rows, 5, "expected pool/user/role/grant plan rows plus the summary row")
	applySummaryRow := decoded.Rows[4]
	require.Equal(t, "summary", applySummaryRow[0])
	assert.Contains(t, applySummaryRow[1], "granted role", "the completion summary must reach the operator as a row")

	var sawPasswordCell bool
	for _, row := range decoded.Rows {
		for _, cell := range row {
			assert.NotContains(t, cell, fakeDefaultUserPassword)
			if strings.Contains(cell, redact.Placeholder) {
				sawPasswordCell = true
			}
		}
	}
	assert.True(t, sawPasswordCell, "the user step's row must carry the redacted password placeholder")

	require.Len(t, state.createUserCalls, 1)
	assert.Equal(t, fakeDefaultUserPassword, state.createUserCalls[0].password,
		"the real secret must still have reached the API request")
	require.Len(t, state.updateAclCalls, 1)
}

// TestAccessGrant_SummaryReachesDefaultOutput pins the fix for a dropped
// completion message: buildAccessPlanResult used to set Result.Message
// alongside Rows, and every renderer drops Message once Rows/Headers are
// set, so the grant summary never reached the operator in any format. The
// summary is now a trailing row and must appear in the default rendering.
func TestAccessGrant_SummaryReachesDefaultOutput(t *testing.T) {
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState(nil, nil, nil)
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "granted role",
		"the completion summary must appear in the default rendered output")
}

func TestAccessGrant_RoleMissingAndNotPMXAdmin_Errors(t *testing.T) {
	lab := cleanLab("alpha")
	lab.Access.Role = "CustomRole"
	cfg := &config.Config{
		DefaultUserPassword: fakeDefaultUserPassword,
		Labs:                map[string]*config.Lab{"alpha": lab},
	}
	path := writeConfig(t, cfg)
	cmd := accessTestCmd(t, path)

	f := testhelper.NewFakePVE(t)
	state := newAccessFakeState([]string{"lab-alpha"}, []string{"alice@pve"}, nil) // CustomRole missing
	registerAccessFakeRoutes(t, f, state)

	var stdout, stderr bytes.Buffer
	wireAccessDeps(t, cmd, f, &stdout, &stderr)

	role, dryRun := accessFlagValues(t, cmd)
	err := runAccessGrant(cmd, "alpha", "alice@pve", role, dryRun)
	require.Error(t, err)
	assert.ErrorContains(t, err, `role "CustomRole" does not exist`)
	assert.Empty(t, state.createRoleCalls)
	assert.Empty(t, state.updateAclCalls)
}
