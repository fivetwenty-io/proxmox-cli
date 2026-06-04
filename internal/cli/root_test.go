package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// TestRootFlags_Defaults verifies that NewRootCmd sets the expected flag
// defaults for all persistent flags.
func TestRootFlags_Defaults(t *testing.T) {
	// Clear env vars that influence flag defaults.
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_OUTPUT", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	root := cli.NewRootCmd()
	flags := root.PersistentFlags()

	require.True(t, flags.HasFlags(), "root must have persistent flags")

	// --config default contains "pve/config.yml".
	cfgFlag := flags.Lookup("config")
	require.NotNil(t, cfgFlag)
	require.Contains(t, cfgFlag.DefValue, "pve")
	require.Contains(t, cfgFlag.DefValue, "config.yml")

	// --target default is empty string.
	targetFlag := flags.Lookup("target")
	require.NotNil(t, targetFlag)
	require.Equal(t, "", targetFlag.DefValue)

	// --node default is empty (PVE_NODE unset).
	nodeFlag := flags.Lookup("node")
	require.NotNil(t, nodeFlag)
	require.Equal(t, "", nodeFlag.DefValue)

	// --output default is "table" (PVE_OUTPUT unset).
	outFlag := flags.Lookup("output")
	require.NotNil(t, outFlag)
	require.Equal(t, "table", outFlag.DefValue)

	// Boolean flags default to false.
	for _, name := range []string{"debug", "verbose", "trace", "no-log", "async", "insecure", "ascii"} {
		f := flags.Lookup(name)
		require.NotNil(t, f, "flag %s must exist", name)
		require.Equal(t, "false", f.DefValue, "flag %s default must be false", name)
	}
}

// TestPersistentPreRunE_Insecure_WarnsOnStderr verifies that resolving an
// insecure (TLS-verification-disabled) connection emits a stderr warning.
func TestPersistentPreRunE_Insecure_WarnsOnStderr(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	t.Setenv("PVE_OUTPUT", "table")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_TARGET", "")

	cfg := &config.Config{
		CurrentTarget: "prod",
		Targets: map[string]*config.Target{
			"prod": {
				Host:     "127.0.0.1",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "cli",
					Secret:   "literal-secret",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	root := cli.NewRootCmd()
	root.SetContext(context.Background())

	called := false
	noop := buildNoopCmd(&called)
	root.AddCommand(noop)

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--config", cfgPath, "noop"})

	require.NoError(t, root.Execute())
	require.True(t, called)
	require.Contains(t, errBuf.String(), "TLS certificate verification disabled",
		"an insecure connection must warn the operator on stderr")
}

// TestPersistentPreRunE_ASCII_SetsRendererMode verifies the --ascii flag is
// wired through to the renderer (the deps Out renderer renders ASCII borders).
func TestPersistentPreRunE_ASCII_SetsRendererMode(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	t.Setenv("PVE_OUTPUT", "table")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_TARGET", "")

	root := cli.NewRootCmd()
	root.SetContext(context.Background())

	var deps *cli.Deps
	cmd := buildInspectCmd(&deps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "--ascii", "inspect"})
	require.NoError(t, root.Execute())
	require.NotNil(t, deps)

	// Render a small table; with --ascii the borders must use ASCII glyphs
	// (e.g. '+') rather than Unicode box-drawing characters.
	var rb bytes.Buffer
	require.NoError(t, deps.Out.Render(&rb, output.Result{
		Headers: []string{"A"},
		Rows:    [][]string{{"1"}},
	}, output.FormatTable))
	require.Contains(t, rb.String(), "+", "ascii table borders should contain '+'")
	require.NotContains(t, rb.String(), "─", "ascii mode must not use Unicode box-drawing")
}

// TestRootFlags_PVEOutput verifies --output default picks up PVE_OUTPUT.
func TestRootFlags_PVEOutput(t *testing.T) {
	t.Setenv("PVE_OUTPUT", "json")

	root := cli.NewRootCmd()
	outFlag := root.PersistentFlags().Lookup("output")
	require.NotNil(t, outFlag)
	require.Equal(t, "json", outFlag.DefValue)
}

// TestRootFlags_PVENode verifies --node default picks up PVE_NODE.
func TestRootFlags_PVENode(t *testing.T) {
	t.Setenv("PVE_NODE", "pve-host-01")

	root := cli.NewRootCmd()
	nodeFlag := root.PersistentFlags().Lookup("node")
	require.NotNil(t, nodeFlag)
	require.Equal(t, "pve-host-01", nodeFlag.DefValue)
}

// TestPersistentPreRunE_NoConfig verifies that when the config file is absent
// AND no target is specified, Execute() returns a non-nil error.
//
// Note: we do NOT call Execute() here (it would try to fully wire the CLI);
// instead we invoke a minimal sub-command that triggers PersistentPreRunE by
// constructing a real cobra tree and calling cmd.ExecuteC.
func TestPersistentPreRunE_NoConfig_NoTarget(t *testing.T) {
	// Point config at a temp dir that contains no config file.
	tmpDir := t.TempDir()
	t.Setenv("PVE_OUTPUT", "json")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_TARGET", "")

	root := cli.NewRootCmd()
	root.SetContext(context.Background())

	// A no-op child command that actually triggers PersistentPreRunE.
	called := false
	noop := buildNoopCmd(&called)
	root.AddCommand(noop)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"noop",
	})

	err := root.Execute()
	// PersistentPreRunE should fail because there is no target.
	require.Error(t, err, "expected error when no target is configured")
	require.False(t, called, "noop RunE must not be reached when pre-run fails")
}

// TestPersistentPreRunE_NoClient_AnnotationSkipsClientBuild verifies that a
// command annotated with Annotations["noClient"]="true" does NOT error when
// there is no usable config/target.
func TestPersistentPreRunE_NoClient_AnnotationSkipsClientBuild(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PVE_OUTPUT", "json")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_TARGET", "")

	root := cli.NewRootCmd()
	root.SetContext(context.Background())

	called := false
	noop := buildNoopCmd(&called)
	noop.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(noop)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"noop",
	})

	err := root.Execute()
	require.NoError(t, err, "noClient annotation must bypass client build error")
	require.True(t, called, "noop RunE must run when noClient annotation is set")
}

// TestPersistentPreRunE_NoClient_DepsAreInjected verifies that GetDeps returns
// a populated Deps (Out, Format, Log, Runner) even for noClient commands.
func TestPersistentPreRunE_NoClient_DepsAreInjected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PVE_OUTPUT", "table")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_TARGET", "")

	root := cli.NewRootCmd()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	cmd := buildInspectCmd(&capturedDeps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	root.SetArgs([]string{
		"--config", filepath.Join(tmpDir, "config.yml"),
		"inspect",
	})

	err := root.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedDeps)
	require.NotNil(t, capturedDeps.Out, "Out renderer must be populated")
	require.NotNil(t, capturedDeps.Log, "Log must be populated")
	require.NotNil(t, capturedDeps.Runner, "Runner must be populated")
	require.Equal(t, "table", string(capturedDeps.Format))
	require.Nil(t, capturedDeps.API, "API must be nil for noClient commands")
}

// TestRegisterGroup_GroupAppearsInHelp verifies that a factory registered via
// RegisterGroup is wired into the root command by AddGroups.
func TestRegisterGroup_GroupAppearsInHelp(t *testing.T) {
	// Register a dummy group factory.
	cli.RegisterGroup(func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:   "testgroup",
			Short: "test group for unit tests",
		}
	})

	root := cli.NewRootCmd()
	root.SetContext(context.Background())
	cli.AddGroups(root, &cli.Deps{})

	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["testgroup"], "testgroup must appear in root commands after RegisterGroup")
}

// TestMain_NoArgsReturnsOK verifies that Main() exits 0 when invoked with no
// subcommand (cobra prints help and exits 0).
func TestMain_HelpExitsZero(t *testing.T) {
	// Re-assign args so cobra prints help; os.Exit is NOT called — Main() returns.
	old := os.Args
	os.Args = []string{"pve", "--help"}
	defer func() { os.Args = old }()

	code := cli.Main()
	// cobra exits 0 for --help.
	require.Equal(t, 0, code)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildNoopCmd returns a cobra.Command whose RunE sets *called = true.
func buildNoopCmd(called *bool) *cobra.Command {
	return &cobra.Command{
		Use: "noop",
		RunE: func(cmd *cobra.Command, _ []string) error {
			*called = true
			return nil
		},
	}
}

// buildInspectCmd returns a cobra.Command that stores the Deps from context.
func buildInspectCmd(deps **cli.Deps) *cobra.Command {
	return &cobra.Command{
		Use: "inspect",
		RunE: func(cmd *cobra.Command, _ []string) error {
			*deps = cli.GetDeps(cmd)
			return nil
		},
	}
}
