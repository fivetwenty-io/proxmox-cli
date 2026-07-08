package remote

import (
	"bytes"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// contextFor builds a token-auth *config.Context pointing at f's fake server.
func contextFor(t *testing.T, f *testhelper.FakePVE, sshBlock config.SSHBlock) *config.Context {
	t.Helper()
	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return &config.Context{
		Host:     host,
		Port:     port,
		Protocol: "http",
		Auth: config.AuthBlock{
			Type:     "token",
			Username: "root@pam",
			TokenID:  "test",
			Secret:   "00000000-0000-0000-0000-000000000000",
		},
		TLS: config.TLSBlock{Insecure: true},
		SSH: sshBlock,
	}
}

// writeFakeConfig writes a single-context ("fake") config file pointing at f,
// with the given SSH defaults block, and returns its path.
func writeFakeConfig(t *testing.T, f *testhelper.FakePVE, sshBlock config.SSHBlock) string {
	t.Helper()
	cfg := &config.Config{
		CurrentContext: "fake",
		Contexts:       map[string]*config.Context{"fake": contextFor(t, f, sshBlock)},
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(path, cfg))
	return path
}

// writeFakeConfigMulti writes a config file with one context per entry in
// servers, current-context set to current, and returns its path. Used to
// prove `pmx rsync -c <name> ...` actually selects the named context, since
// each server is independently distinguishable by its resolved node address.
func writeFakeConfigMulti(t *testing.T, current string, servers map[string]*testhelper.FakePVE) string {
	t.Helper()
	contexts := map[string]*config.Context{}
	for name, f := range servers {
		contexts[name] = contextFor(t, f, config.SSHBlock{})
	}
	cfg := &config.Config{CurrentContext: current, Contexts: contexts}
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(path, cfg))
	return path
}

// newRemoteRoot builds the real root command wired to cfgPath, with the given
// runner installed via a PersistentPreRunE wrapper that runs after the
// standard one (mirrors node_test.go's newNodeRoot). The returned prefix
// carries only --config: rsync never renders through deps.Out, so --output
// is deliberately absent from extractPMXFlags' recognised front table, and
// including it here would leak into rsync's own argv instead of being
// applied. The caller appends the command-specific args (including any
// "-c <context>" it wants to place among rsync's own argv, to exercise
// front-extraction rather than normal root parsing).
func newRemoteRoot(t *testing.T, cfgPath string, runner exec.Runner) (
	*cobra.Command, *bytes.Buffer, []string,
) {
	t.Helper()
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	t.Cleanup(cleanup)
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{Rsync, SSH})

	std := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := std(cmd, args); err != nil {
			return err
		}
		cli.GetDeps(cmd).Runner = runner
		return nil
	}

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	prefix := []string{"--config", cfgPath}
	return root, &buf, prefix
}

func TestRsync_RewritesSrcAndDstAndInjectsRemoteShell(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "-avz", "pve1:/etc", "./local/dst"))
	require.NoError(t, root.Execute())

	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.Equal(t, "rsync", c.Name)
	require.False(t, c.Interactive)
	// Default cluster/status maps pve1 -> 192.168.1.10 (testhelper default).
	require.Equal(t, []string{"-e", "ssh -p 22", "-avz", "root@192.168.1.10:/etc", "./local/dst"}, c.Args)
}

func TestRsync_RewritesDstPositionPreservingExplicitUser(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "-avz", "./local/site/", "admin@pve1:/var/www/"))
	require.NoError(t, root.Execute())

	c := runner.Calls[0]
	require.Equal(t, []string{"-e", "ssh -p 22", "-avz", "./local/site/", "admin@192.168.1.10:/var/www/"}, c.Args)
}

func TestRsync_MultipleSourcesSameNodeAllRewritten(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "pve1:/etc", "pve1:/var/log", "./dst/"))
	require.NoError(t, root.Execute())

	c := runner.Calls[0]
	require.Equal(t, []string{
		"-e", "ssh -p 22",
		"root@192.168.1.10:/etc", "root@192.168.1.10:/var/log", "./dst/",
	}, c.Args)
}

func TestRsync_NameFallbackWhenNodeUnresolved(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "cluster", "name": "c", "online": 1},
	})
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "othernode:/etc", "./dst"))
	require.NoError(t, root.Execute())

	c := runner.Calls[0]
	require.Equal(t, "root@othernode:/etc", c.Args[2])
}

func TestRsync_IPv6ResolvedHostIsBracketed(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "node", "name": "pve1", "ip": "fe80::1", "online": 1},
	})
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "pve1:/etc", "./dst"))
	require.NoError(t, root.Execute())

	c := runner.Calls[0]
	require.Equal(t, "root@[fe80::1]:/etc", c.Args[2])
}

func TestRsync_ContextFlagThroughFullRootExecutionSelectsNamedContext(t *testing.T) {
	alpha := testhelper.NewFakePVE(t)
	alpha.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "node", "name": "pve1", "ip": "10.0.0.1", "online": 1},
	})
	beta := testhelper.NewFakePVE(t)
	beta.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "node", "name": "pve1", "ip": "10.0.0.2", "online": 1},
	})

	cfgPath := writeFakeConfigMulti(t, "alpha", map[string]*testhelper.FakePVE{"alpha": alpha, "beta": beta})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	// "-c beta" appears among rsync's own argv (after the "rsync" verb), which
	// only front-extraction inside rsync's PersistentPreRunE can apply; a
	// wrong or skipped delegation would resolve pve1 via the DEFAULT ("alpha")
	// context's fake server instead (10.0.0.1).
	root.SetArgs(append(prefix, "rsync", "-c", "beta", "pve1:/etc", "./dst"))
	require.NoError(t, root.Execute())

	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.Equal(t, "root@10.0.0.2:/etc", c.Args[2])
}

func TestRsync_EInjectionQuotesIdentityWithSpecialCharacters(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "--ssh-identity", "/path with space/id_ed25519", "pve1:/etc", "./dst"))
	require.NoError(t, root.Execute())

	c := runner.Calls[0]
	require.Equal(t, "-e", c.Args[0])
	require.Equal(t, `ssh -p 22 -i '/path with space/id_ed25519'`, c.Args[1])
}

func TestRsync_ExitCodePropagation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake(exec.FakeResponse{ExitCode: 23})
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "pve1:/etc", "./dst"))
	err := root.Execute()
	require.Error(t, err)

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 23, exitErr.Code)
}

func TestRsync_ContextSSHDefaultsAppliedWhenFlagsNotSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{User: "admin", Port: 2200, Identity: "/k"})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "pve1:/etc", "./dst"))
	require.NoError(t, root.Execute())

	c := runner.Calls[0]
	require.Equal(t, "ssh -p 2200 -i /k", c.Args[1])
	require.Equal(t, "admin@192.168.1.10:/etc", c.Args[2])
}

func TestRsync_ContextSSHDefaultsOverriddenByExplicitFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{User: "admin", Port: 2200, Identity: "/k"})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "--ssh-user", "root", "pve1:/etc", "./dst"))
	require.NoError(t, root.Execute())

	c := runner.Calls[0]
	require.Equal(t, "root@192.168.1.10:/etc", c.Args[2])
}

func TestRsync_CrossNodeOperandsRejectedNoRunnerCall(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "pve1:/etc", "pve2:/etc", "./dst"))
	err := root.Execute()
	require.Error(t, err)
	require.Empty(t, runner.Calls)
}

func TestRsync_UserSuppliedDashERejectedNoRunnerCall(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	cfgPath := writeFakeConfig(t, f, config.SSHBlock{})
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, cfgPath, runner)

	root.SetArgs(append(prefix, "rsync", "-e", "ssh -p 2200", "pve1:/etc", "./dst"))
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "reserved")
	require.Empty(t, runner.Calls)
}

func TestRsync_BareInvocationShowsHelpWithoutBuildingDeps(t *testing.T) {
	// No config file at all; if help short-circuits before delegating to the
	// root PersistentPreRunE, context resolution never runs and this must
	// still succeed.
	runner := exec.Fake()
	root, buf, prefix := newRemoteRoot(t, filepath.Join(t.TempDir(), "missing.yml"), runner)

	root.SetArgs(append(prefix, "rsync"))
	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "Usage:")
	require.Empty(t, runner.Calls)
}

func TestRsync_HelpFlagShowsHelpWithoutBuildingDeps(t *testing.T) {
	runner := exec.Fake()
	root, buf, prefix := newRemoteRoot(t, filepath.Join(t.TempDir(), "missing.yml"), runner)

	root.SetArgs(append(prefix, "rsync", "-h"))
	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "Usage:")
	require.Empty(t, runner.Calls)
}

func TestRsync_APIErrorSurfacesWhenConfigMissing(t *testing.T) {
	// Sanity check for the help short-circuit above: a NON-help invocation
	// against the same missing config must fail normally (proving help really
	// is what skipped deps construction, not some blanket bypass).
	runner := exec.Fake()
	root, _, prefix := newRemoteRoot(t, filepath.Join(t.TempDir(), "missing.yml"), runner)

	root.SetArgs(append(prefix, "rsync", "pve1:/etc", "./dst"))
	err := root.Execute()
	require.Error(t, err)
	require.Empty(t, runner.Calls)
}
