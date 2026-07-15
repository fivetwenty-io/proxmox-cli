package remote

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newFakeClient returns a FakePVE and a constructed APIClient pointing at it.
// The fake server's Options carry "host:port" in Host with Port left at the
// client default, which would otherwise concatenate into an invalid
// "host:port:8006" URL; split them so the constructed client targets the
// fake correctly.
func newFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()
	f := testhelper.NewFakePVE(t)

	u, err := url.Parse(f.BaseURL())
	require.NoError(t, err)
	host, portStr, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	opts := f.Options
	opts.Host = host
	opts.Port = port

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return f, ac
}

// runSSH builds `pmx ssh` via SSH, injects deps directly into cmd's context
// (bypassing PersistentPreRunE, mirroring the qemu/node package test seams
// since SSH has no PersistentPreRunE of its own), and executes it with args.
func runSSH(deps *cli.Deps, args ...string) (*cobra.Command, error) {
	cmd := SSH(deps)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetArgs(args)
	return cmd, cmd.Execute()
}

// lastCall returns the single recorded invocation on fr.
func lastCall(t *testing.T, fr *exec.FakeRunner) exec.Call {
	t.Helper()
	require.Len(t, fr.Calls, 1, "expected exactly one runner invocation")
	return fr.Calls[0]
}

func TestSSH_ResolvesHostAndRunsInteractive(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := &cli.Deps{API: ac, Runner: fr}

	_, err := runSSH(deps, "pve1", "uptime")
	require.NoError(t, err)

	c := lastCall(t, fr)
	require.True(t, c.Interactive)
	require.Equal(t, "ssh", c.Name)
	// Default cluster/status maps pve1 -> 192.168.1.10 (testhelper default).
	require.Equal(t, []string{"-p", "22", "root@192.168.1.10", "uptime"}, c.Args)
}

func TestSSH_FlagsBeforeNodeAndPassthroughOptionsReorderedAfter(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := &cli.Deps{API: ac, Runner: fr}

	_, err := runSSH(deps, "-l", "admin", "-i", "key", "-p", "2222",
		"pve1", "-L", "8080:localhost:80", "-N")
	require.NoError(t, err)

	c := lastCall(t, fr)
	// ssh-flag-derived options come first, then passthrough options, then the
	// destination, since ssh's own parser doesn't permute across platforms.
	require.Equal(t, []string{
		"-p", "2222", "-i", "key",
		"-L", "8080:localhost:80", "-N",
		"admin@192.168.1.10",
	}, c.Args)
}

func TestSSH_DashDashForcesRemoteCommand(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := &cli.Deps{API: ac, Runner: fr}

	_, err := runSSH(deps, "pve1", "--", "-v")
	require.NoError(t, err)

	c := lastCall(t, fr)
	require.Equal(t, []string{"-p", "22", "root@192.168.1.10", "-v"}, c.Args)
}

func TestSSH_FallsBackToNodeNameWhenUnresolved(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "cluster", "name": "c", "online": 1},
	})
	fr := exec.Fake()
	deps := &cli.Deps{API: ac, Runner: fr}

	_, err := runSSH(deps, "othernode")
	require.NoError(t, err)

	c := lastCall(t, fr)
	require.Equal(t, "root@othernode", c.Args[len(c.Args)-1])
}

func TestSSH_ContextSSHDefaults_AppliedWhenFlagsNotSet(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := &cli.Deps{
		API: ac, Runner: fr,
		Ctx: &config.Context{SSH: config.SSHBlock{User: "admin", Port: 2200, Identity: "/home/user/.ssh/id"}},
	}

	_, err := runSSH(deps, "pve1")
	require.NoError(t, err)

	c := lastCall(t, fr)
	require.Equal(t, []string{"-p", "2200", "-i", "/home/user/.ssh/id", "admin@192.168.1.10"}, c.Args)
}

func TestSSH_ContextSSHDefaults_ExplicitFlagWins(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := &cli.Deps{
		API: ac, Runner: fr,
		Ctx: &config.Context{SSH: config.SSHBlock{User: "admin", Port: 2200, Identity: "/home/user/.ssh/id"}},
	}

	_, err := runSSH(deps, "-l", "root", "-p", "22", "pve1")
	require.NoError(t, err)

	c := lastCall(t, fr)
	require.Equal(t, []string{"-p", "22", "-i", "/home/user/.ssh/id", "root@192.168.1.10"}, c.Args)
}

func TestSSH_ExitCodePropagation(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake(exec.FakeResponse{ExitCode: 255})
	deps := &cli.Deps{API: ac, Runner: fr}

	_, err := runSSH(deps, "pve1")
	require.Error(t, err)

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 255, exitErr.Code)
}

// findSSH builds the full root command (via newRemoteRoot, so its ssh
// sub-command's ValidArgsFunction is the real one wired through AddGroups)
// pointed at cfgPath, sets --config to cfgPath directly on the root
// persistent flag set (bypassing full argv parsing, which a bare
// ValidArgsFunction call never triggers on its own), and returns the found
// "ssh" *cobra.Command ready for a direct ValidArgsFunction call.
func findSSH(t *testing.T, cfgPath string) *cobra.Command {
	t.Helper()
	root, _, _ := newRemoteRoot(t, cfgPath, exec.Fake())
	require.NoError(t, root.PersistentFlags().Set("config", cfgPath))

	sshCmd, _, err := root.Find([]string{"ssh"})
	require.NoError(t, err)
	sshCmd.SetContext(context.Background())
	return sshCmd
}

// TestSSH_ValidArgsFunction_CompletesNodeNames is the regression test for the
// dead-completion bug (H-2): completeNodeNames used to close over the
// factory-time placeholder *cli.Deps (nil API client) and always return no
// completions. It now builds its own client from cmd's parsed --config flag,
// exercised here through the real root command tree rather than a bare
// SSH(deps) instance.
func TestSSH_ValidArgsFunction_CompletesNodeNames(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "cluster", "name": "c", "online": 1},
		map[string]any{"type": "node", "name": "pve1", "ip": "192.168.1.10", "online": 1},
		map[string]any{"type": "node", "name": "pve2", "ip": "192.168.1.11", "online": 1},
	})
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	sshCmd := findSSH(t, cfgPath)

	names, directive := sshCmd.ValidArgsFunction(sshCmd, nil, "")
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.ElementsMatch(t, []string{"pve1", "pve2"}, names)
}

func TestSSH_ValidArgsFunction_NoCompletionsOnceNodeGiven(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	sshCmd := findSSH(t, cfgPath)

	names, directive := sshCmd.ValidArgsFunction(sshCmd, []string{"pve1"}, "")
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Nil(t, names)
}

// TestSSH_ValidArgsFunction_DegradesSilentlyNoConfig covers the no-config,
// no-context case (e.g. a fresh operator running completion before ever
// running `pmx context select`): completeNodeNames must return no
// completions rather than surface the "no context specified" error.
func TestSSH_ValidArgsFunction_DegradesSilentlyNoConfig(t *testing.T) {
	sshCmd := findSSH(t, filepath.Join(t.TempDir(), "missing.yml"))

	names, directive := sshCmd.ValidArgsFunction(sshCmd, nil, "")
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Nil(t, names)
}

func TestSSH_ValidArgsFunction_DegradesSilentlyOnAPIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	sshCmd := findSSH(t, cfgPath)

	names, directive := sshCmd.ValidArgsFunction(sshCmd, nil, "")
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Nil(t, names)
}

// TestSSH_Complete_ThroughRootMachineryReturnsNodeNames is the H-2
// regression test that exercises the ACTUAL `__complete` machinery cobra
// wires up (not just a direct ValidArgsFunction call), matching how a real
// shell invokes completion: `pmx --config <path> __complete ssh ”`. A
// pre-fix build only ever printed the bare ":4" directive here since
// completeNodeNames's closed-over deps.API was always nil.
func TestSSH_Complete_ThroughRootMachineryReturnsNodeNames(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "node", "name": "pve1", "ip": "192.168.1.10", "online": 1},
		map[string]any{"type": "node", "name": "pve2", "ip": "192.168.1.11", "online": 1},
	})
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	root, buf, prefix := newRemoteRoot(t, cfgPath, exec.Fake())

	root.SetArgs(append(prefix, "__complete", "ssh", ""))
	require.NoError(t, root.Execute())

	out := buf.String()
	require.Contains(t, out, "pve1")
	require.Contains(t, out, "pve2")
}

func TestSSH_RequiresAtLeastOneArg(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Runner: exec.Fake()}

	_, err := runSSH(deps)
	require.Error(t, err)
}

// TestSSH_ProductContextAnnotation asserts the top-level `pmx ssh` command
// carries the product:context annotation, so the root resolves whichever
// client (PVE or PBS) the active context needs instead of always requiring
// PVE.
func TestSSH_ProductContextAnnotation(t *testing.T) {
	cmd := SSH(&cli.Deps{})
	require.Equal(t, cli.ProductFromContext, cmd.Annotations[cli.ProductAnnotation])
}

// TestSSH_PBS_TargetsContextHostWithNoNodeLookup covers the PBS branch of
// RunSSH: a PBS context connects directly to deps.Ctx.Host with no node
// argument and no cluster lookup. deps.API is deliberately left nil so a
// regression that fell through to the PVE branch would nil-pointer panic on
// deps.API.Cluster instead of silently passing.
func TestSSH_PBS_TargetsContextHostWithNoNodeLookup(t *testing.T) {
	fr := exec.Fake()
	deps := &cli.Deps{
		Runner: fr,
		Ctx:    &config.Context{Product: config.ProductPBS, Host: "pbs.example.com"},
	}

	_, err := runSSH(deps, "uptime")
	require.NoError(t, err)

	c := lastCall(t, fr)
	require.True(t, c.Interactive)
	require.Equal(t, []string{"-p", "22", "root@pbs.example.com", "uptime"}, c.Args)
}

// TestSSH_PVE_RequiresNodeArgument covers the PVE branch when no node is
// given: unlike PBS, a PVE (or context-less) invocation must fail with a
// clear error rather than attempt to connect anywhere.
func TestSSH_PVE_RequiresNodeArgument(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := &cli.Deps{API: ac, Runner: fr}

	_, err := runSSH(deps)
	require.ErrorContains(t, err, "node argument is required")
	require.Empty(t, fr.Calls)
}

// TestSSH_PDMContextUsesDirectHost covers the PDM branch of RunSSH: like
// PBS, a PDM context is a single-host product, so it connects directly to
// deps.Ctx.Host with no node argument and no cluster lookup. deps.API is
// deliberately left nil so a regression that fell through to the PVE branch
// would nil-pointer panic on deps.API.Cluster instead of silently passing.
func TestSSH_PDMContextUsesDirectHost(t *testing.T) {
	fr := exec.Fake()
	deps := &cli.Deps{
		Runner: fr,
		Ctx:    &config.Context{Product: config.ProductPDM, Host: "pdm.example.com"},
	}

	_, err := runSSH(deps, "uptime")
	require.NoError(t, err)

	c := lastCall(t, fr)
	require.True(t, c.Interactive)
	require.Equal(t, []string{"-p", "22", "root@pdm.example.com", "uptime"}, c.Args)
}

// TestSSH_UnknownProductErrors covers the default arm of RunSSH's product
// switch: a context declaring a product outside {pve, pbs, pdm, ""} must
// fail loudly rather than silently falling into either addressing mode.
func TestSSH_UnknownProductErrors(t *testing.T) {
	fr := exec.Fake()
	deps := &cli.Deps{
		Runner: fr,
		Ctx:    &config.Context{Product: "bogus", Host: "somewhere.example.com"},
	}

	_, err := runSSH(deps, "uptime")
	require.ErrorContains(t, err, `unsupported product "bogus"`)
	require.Empty(t, fr.Calls)
}
