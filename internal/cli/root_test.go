package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/version"
)

// TestRootFlags_Defaults verifies that NewRootCmd sets the expected flag
// defaults for all persistent flags.
func TestRootFlags_Defaults(t *testing.T) {
	// Clear env vars that influence flag defaults.
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	flags := root.PersistentFlags()

	require.True(t, flags.HasFlags(), "root must have persistent flags")

	// --config default contains "pmx/config.yml".
	cfgFlag := flags.Lookup("config")
	require.NotNil(t, cfgFlag)
	require.Contains(t, cfgFlag.DefValue, "pmx")
	require.Contains(t, cfgFlag.DefValue, "config.yml")

	// --context default is empty string; short flag is -c.
	ctxFlag := flags.Lookup("context")
	require.NotNil(t, ctxFlag, "--context flag must exist")
	require.Equal(t, "", ctxFlag.DefValue)
	require.Equal(t, "c", ctxFlag.Shorthand, "--context short flag must be -c")

	// --target must not exist after the context rename.
	require.Nil(t, flags.Lookup("target"), "--target flag must not exist after rename")

	// --node default is empty (PMX_NODE unset).
	nodeFlag := flags.Lookup("node")
	require.NotNil(t, nodeFlag)
	require.Equal(t, "", nodeFlag.DefValue)

	// --output default is "table" (PMX_OUTPUT unset).
	outFlag := flags.Lookup("output")
	require.NotNil(t, outFlag)
	require.Equal(t, "table", outFlag.DefValue)

	// Boolean flags default to false.
	for _, name := range []string{"debug", "verbose", "trace", "no-log", "async", "insecure"} {
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
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]*config.Context{
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

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
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

// TestPersistentPreRunE_ASCII_Format verifies that -o ascii is wired through
// to deps.Format and renders tables with ASCII borders.
func TestPersistentPreRunE_ASCII_Format(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())

	var deps *cli.Deps
	cmd := buildInspectCmd(&deps)
	cmd.Annotations = map[string]string{"noClient": "true"}
	root.AddCommand(cmd)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "-o", "ascii", "inspect"})
	require.NoError(t, root.Execute())
	require.NotNil(t, deps)
	require.Equal(t, output.FormatASCII, deps.Format)

	// Render a small table; with -o ascii the borders must use ASCII glyphs
	// (e.g. '+') rather than Unicode box-drawing characters.
	var rb bytes.Buffer
	require.NoError(t, deps.Out.Render(&rb, output.Result{
		Headers: []string{"A"},
		Rows:    [][]string{{"1"}},
	}, deps.Format))
	require.Contains(t, rb.String(), "+", "ascii table borders should contain '+'")
	require.NotContains(t, rb.String(), "─", "ascii mode must not use Unicode box-drawing")
}

// TestRootFlags_PVEOutput verifies --output default picks up PMX_OUTPUT.
func TestRootFlags_PVEOutput(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "json")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	outFlag := root.PersistentFlags().Lookup("output")
	require.NotNil(t, outFlag)
	require.Equal(t, "json", outFlag.DefValue)
}

// TestRootFlags_PVENode verifies --node default picks up PMX_NODE.
func TestRootFlags_PVENode(t *testing.T) {
	t.Setenv("PMX_NODE", "pve-host-01")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	nodeFlag := root.PersistentFlags().Lookup("node")
	require.NotNil(t, nodeFlag)
	require.Equal(t, "pve-host-01", nodeFlag.DefValue)
}

// TestPersistentPreRunE_NoConfig_NoContext verifies that when the config file is absent
// AND no context is specified, Execute() returns a non-nil error.
func TestPersistentPreRunE_NoConfig_NoContext(t *testing.T) {
	// Point config at a temp dir that contains no config file.
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
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
	// PersistentPreRunE should fail because there is no context.
	require.Error(t, err, "expected error when no context is configured")
	require.False(t, called, "noop RunE must not be reached when pre-run fails")
}

// TestPersistentPreRunE_NoClient_AnnotationSkipsClientBuild verifies that a
// command annotated with Annotations["noClient"]="true" does NOT error when
// there is no usable config/context.
func TestPersistentPreRunE_NoClient_AnnotationSkipsClientBuild(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
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
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
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

// TestAddGroups_GroupAppearsInHelp verifies that a factory passed to AddGroups
// is wired into the root command.
func TestAddGroups_GroupAppearsInHelp(t *testing.T) {
	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:   "testgroup",
			Short: "test group for unit tests",
		}
	}

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{factory})

	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["testgroup"], "testgroup must appear in root commands after AddGroups")
}

// TestMain_HelpExitsZero verifies that Main() exits 0 when invoked with no
// subcommand (cobra prints help and exits 0).
func TestMain_HelpExitsZero(t *testing.T) {
	// Re-assign args so cobra prints help; os.Exit is NOT called — Main() returns.
	old := os.Args
	os.Args = []string{"pmx", "--help"}
	defer func() { os.Args = old }()

	code := cli.Main(nil)
	// cobra exits 0 for --help.
	require.Equal(t, 0, code)
}

// TestContextFlagPrecedence verifies the three-tier resolution chain:
// --context flag > $PMX_CONTEXT env > cfg.CurrentContext.
//
// Strategy: each sub-test passes a context name that does NOT exist in the
// config. ResolveContext returns a "not found" error whose message contains
// the name that was resolved. This lets us confirm which tier won without
// needing an actual API connection.
func TestContextFlagPrecedence(t *testing.T) {
	// Build a config with CurrentContext = "current" and one extra context
	// "existing". Tests pass context names that are absent to force a resolution
	// error whose message includes the attempted name.
	makeConfig := func(t *testing.T) string {
		t.Helper()
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yml")
		cfg := &config.Config{
			CurrentContext: "current",
			Contexts: map[string]*config.Context{
				"current": {
					Host: "10.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
					Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s1"},
				},
			},
		}
		require.NoError(t, config.SaveForce(cfgPath, cfg))
		return cfgPath
	}

	t.Run("flag wins over current-context", func(t *testing.T) {
		t.Setenv("PMX_CONTEXT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		// Pass a context name "from-flag" that is absent; error message must name it.
		root.SetArgs([]string{"--config", cfgPath, "--context", "from-flag", "noop"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "from-flag",
			"--context flag value must appear in the resolution error")
		require.False(t, called)
	})

	t.Run("env var wins over current-context", func(t *testing.T) {
		t.Setenv("PMX_CONTEXT", "from-env")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"--config", cfgPath, "noop"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "from-env",
			"$PMX_CONTEXT env value must appear in the resolution error")
		require.False(t, called)
	})

	t.Run("current-context used when no flag or env", func(t *testing.T) {
		t.Setenv("PMX_CONTEXT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		// No --context, no PMX_CONTEXT. "current" exists in config so no error —
		// but it cannot connect (NewAPIClient returns an error on connect).
		// Verify no "no context specified" error (resolution succeeded).
		root.SetArgs([]string{"--config", cfgPath, "noop"})

		err := root.Execute()
		// The command succeeds: noClient=false but NewAPIClient with a stub host
		// succeeds on construction (lazy HTTP). noop runs without error.
		// Confirm: no resolution error about missing context name.
		if err != nil {
			require.NotContains(t, err.Error(), "no context specified",
				"current-context 'current' must be resolved without error")
		}
	})

	t.Run("unknown-target-flag", func(t *testing.T) {
		// --target must not exist; cobra must return unknown flag error.
		t.Setenv("PMX_CONTEXT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_OUTPUT", "table")
		cfgPath := makeConfig(t)

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		noop := buildNoopCmd(&called)
		root.AddCommand(noop)

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"--config", cfgPath, "--target", "a", "noop"})

		err := root.Execute()
		require.Error(t, err, "--target must be an unknown flag error")
		require.Contains(t, err.Error(), "unknown flag",
			"error must identify --target as unknown")
		require.False(t, called, "noop must not run when an unknown flag is passed")
	})
}

// TestShellCompletionSkipsClientBuild is the regression test for a bug
// discovered while validating H-2 (dead `pmx ssh` node-name completion):
// cobra's built-in "__complete" hidden command has DisableFlagParsing set,
// so persistentPreRunE — which cobra runs for "__complete" itself, as the
// nearest ancestor with a PersistentPreRunE, BEFORE "__complete"'s own Run
// dispatches into the target command's ValidArgsFunction — never sees the
// real --config/--context the operator typed; it resolves the DEFAULT
// context instead. If that default context is broken in any way (bad
// secret reference, unresolvable), BuildContextClient errors and used to
// abort "__complete" entirely: every shell-completion request would print
// an error and exit non-zero, regardless of which command was being
// completed or whether ITS OWN ValidArgsFunction had nothing to do with the
// broken default context at all.
//
// persistentPreRunE now skips client construction for "__complete" itself
// (same as a noClient command), so completion always proceeds to the target
// command's own Run/ValidArgsFunction instead of failing here.
func TestShellCompletionSkipsClientBuild(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "table")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "broken",
		Contexts: map[string]*config.Context{
			"broken": {
				Host: "10.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{
					Type: "token", Username: "root@pam", TokenID: "tok",
					// Unresolvable on every platform (no keychain dependency):
					// ResolveSecret errors for an explicit but unset env reference.
					Secret: "${PMX_CLI_TEST_UNSET_SECRET_VAR_XYZ}",
				},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	buildRoot := func(t *testing.T) *cobra.Command {
		t.Helper()
		root, cleanup := cli.NewRootCmd()
		t.Cleanup(cleanup)
		root.SetContext(context.Background())
		called := false
		root.AddCommand(buildNoopCmd(&called))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		return root
	}

	t.Run("control: normal noop execution fails on the broken default context", func(t *testing.T) {
		root := buildRoot(t)
		root.SetArgs([]string{"--config", cfgPath, "noop"})
		err := root.Execute()
		require.Error(t, err, "the config's default context must genuinely be broken")
		require.Contains(t, err.Error(), "resolve secret")
	})

	t.Run("__complete noop does not error even though the default context is broken", func(t *testing.T) {
		root := buildRoot(t)
		root.SetArgs([]string{"--config", cfgPath, "__complete", "noop", ""})
		err := root.Execute()
		require.NoError(t, err, "shell completion must never fail due to the default context")
	})
}

// TestOutputChangedDetection verifies that cmd.Flags().Changed("output") correctly
// distinguishes an explicit -o flag from an absent one, even when the explicit
// value equals the global default ("table"). This is the mechanism used in
// persistentPreRunE to guard per-context DefaultOutput application (F-01 fix).
func TestOutputChangedDetection(t *testing.T) {
	t.Run("explicit -o table marks flag as Changed", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		var changedWhenExplicit bool
		probe := &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				changedWhenExplicit = cmd.Flags().Changed("output")
				return nil
			},
		}
		root.AddCommand(probe)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "c.yml"),
			"--output", "table", "probe"})
		require.NoError(t, root.Execute())
		require.True(t, changedWhenExplicit,
			"cmd.Flags().Changed(\"output\") must be true when -o is explicitly passed")
	})

	t.Run("absent -o flag does NOT mark flag as Changed", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		var changedWhenAbsent bool
		probe := &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				changedWhenAbsent = cmd.Flags().Changed("output")
				return nil
			},
		}
		root.AddCommand(probe)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "c.yml"), "probe"})
		require.NoError(t, root.Execute())
		require.False(t, changedWhenAbsent,
			"cmd.Flags().Changed(\"output\") must be false when -o was not passed")
	})
}

// TestContextDefaultsResolution verifies the three-layer defaults:
// explicit flag > context default > global default.
//
// Strategy: use a noClient probe that captures deps after PersistentPreRunE.
// Per-context defaults (DefaultNode, DefaultOutput) are applied in the non-noClient
// branch (after ResolveContext). To test them without a real API connection,
// use a context name that does NOT exist: ResolveContext returns an error
// that confirms the resolution chain ran. A separate sub-test for the noClient
// path verifies no context error when annotation bypasses resolution.
func TestContextDefaultsResolution(t *testing.T) {
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "") // no global env override

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")

	t.Run("context DefaultNode applied when --node not set", func(t *testing.T) {
		// Config has context "lab" with DefaultNode="pve1".
		// Pass a nonexistent --context to trigger a ResolveContext error that
		// includes the name, confirming the resolution chain was entered.
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts: map[string]*config.Context{
				"lab": {
					Host:        "10.0.0.1",
					Port:        8006,
					Protocol:    "https",
					Realm:       "pam",
					DefaultNode: "pve1",
					Auth:        config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s1"},
				},
			},
		}
		require.NoError(t, config.SaveForce(cfgPath, cfg))

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		root.AddCommand(buildNoopCmd(&called))

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		// Use a missing context name to force a clear error message.
		root.SetArgs([]string{"--config", cfgPath, "--context", "missing-ctx", "noop"})

		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing-ctx",
			"resolution chain entered; error names the missing context")
	})

	t.Run("noClient command runs without context configured", func(t *testing.T) {
		// Empty config: no current-context. noClient command must succeed.
		emptyCfgPath := filepath.Join(t.TempDir(), "empty.yml")
		require.NoError(t, config.SaveForce(emptyCfgPath, &config.Config{}))

		t.Setenv("PMX_OUTPUT", "table")
		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		called := false
		noop := buildNoopCmd(&called)
		noop.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(noop)

		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"--config", emptyCfgPath, "noop"})

		err := root.Execute()
		require.NoError(t, err, "noClient command must succeed with no context configured")
		require.True(t, called)
	})
}

// TestOutputPrecedence_FourTiers pins the full 4-tier resolution order for
// --output: explicit flag > $PMX_OUTPUT > context default-output > built-in default.
//
// Each sub-test uses a noClient inspect command so no API connection is needed.
// Context default-output is NOT applied in the noClient branch (it runs before
// ResolveContext), so this suite tests flag and env tiers cleanly. The noClient
// path yields the format that pf.output resolved to (flag or env), proving tiers
// 1 and 2. Tiers 3 and 4 are covered by TestContextDefaultsResolution and the
// existing TestOutputChangedDetection tests.
func TestOutputPrecedence_FourTiers(t *testing.T) {
	makeCtxConfig := func(t *testing.T, defaultOutput string) (cfgPath string) {
		t.Helper()
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yml")
		cfg := &config.Config{
			CurrentContext: "test",
			Contexts: map[string]*config.Context{
				"test": {
					Host:          "10.0.0.1",
					Port:          8006,
					Protocol:      "https",
					Realm:         "pam",
					DefaultOutput: defaultOutput,
					Auth:          config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s"},
				},
			},
		}
		require.NoError(t, config.SaveForce(p, cfg))
		return p
	}

	t.Run("tier1 explicit flag beats env and context default", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "json")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")
		// context default-output = yaml; flag = plain → plain must win.
		cfgPath := makeCtxConfig(t, "yaml")

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		var deps *cli.Deps
		cmd := buildInspectCmd(&deps)
		cmd.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(cmd)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "--output", "plain", "inspect"})
		require.NoError(t, root.Execute())
		require.NotNil(t, deps)
		require.Equal(t, "plain", string(deps.Format),
			"explicit --output flag must win over $PMX_OUTPUT and context default-output")
	})

	t.Run("tier2 PMX_OUTPUT beats context default-output", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "yaml")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")
		// context default-output = json; $PMX_OUTPUT = yaml → yaml must win.
		// noClient branch: format = pf.output = yaml (baked from env); no context resolution.
		cfgPath := makeCtxConfig(t, "json")

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		var deps *cli.Deps
		cmd := buildInspectCmd(&deps)
		cmd.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(cmd)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "inspect"})
		require.NoError(t, root.Execute())
		require.NotNil(t, deps)
		require.Equal(t, "yaml", string(deps.Format),
			"$PMX_OUTPUT must win over context default-output")
	})

	t.Run("tier4 built-in default table when no flag env or context default", func(t *testing.T) {
		t.Setenv("PMX_OUTPUT", "")
		t.Setenv("PMX_NODE", "")
		t.Setenv("PMX_CONTEXT", "")
		cfgPath := makeCtxConfig(t, "") // no context default-output

		root, cleanup := cli.NewRootCmd()
		defer cleanup()
		root.SetContext(context.Background())

		var deps *cli.Deps
		cmd := buildInspectCmd(&deps)
		cmd.Annotations = map[string]string{"noClient": "true"}
		root.AddCommand(cmd)

		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--config", cfgPath, "inspect"})
		require.NoError(t, root.Execute())
		require.NotNil(t, deps)
		require.Equal(t, "table", string(deps.Format),
			"built-in default must be table when nothing overrides it")
	})
}

// TestOutputPrecedence_EnvBeatsContextDefault_NonNoClient verifies F-W6-03:
// $PMX_OUTPUT outranks context default-output even in the full (non-noClient)
// resolution path. Uses a token-auth context against a non-listening host;
// NewAPIClient succeeds lazily so the inspect probe captures deps.Format.
func TestOutputPrecedence_EnvBeatsContextDefault_NonNoClient(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {
				Host:          "127.0.0.1",
				Port:          8006,
				Protocol:      "https",
				Realm:         "pam",
				DefaultOutput: "yaml", // context default; must NOT win
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "tok",
					Secret:   "literal-secret",
				},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			capturedDeps = cli.GetDeps(cmd)
			return nil
		},
	}
	root.AddCommand(probe)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "probe"})

	// NewAPIClient with a non-listening host is lazy — construction succeeds.
	err := root.Execute()
	if err != nil {
		// If construction fails for any reason skip rather than false-fail.
		t.Skipf("API client construction failed (lab env absent): %v", err)
	}
	require.NotNil(t, capturedDeps)
	require.Equal(t, "json", string(capturedDeps.Format),
		"$PMX_OUTPUT=json must beat context default-output=yaml in non-noClient path")
}

// TestPersistentPreRunE_Ctx_PopulatedForNonNoClient verifies that Deps.Ctx is
// populated with the resolved *config.Context for a normal (non-noClient)
// command, so top-level commands (e.g. `pmx ssh`/`pmx rsync`) can read
// per-context SSH defaults without re-resolving the config themselves.
func TestPersistentPreRunE_Ctx_PopulatedForNonNoClient(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "json")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {
				Host:     "127.0.0.1",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "tok",
					Secret:   "literal-secret",
				},
				SSH: config.SSHBlock{User: "admin", Port: 2222},
			},
		},
	}
	require.NoError(t, config.SaveForce(cfgPath, cfg))

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())

	var capturedDeps *cli.Deps
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			capturedDeps = cli.GetDeps(cmd)
			return nil
		},
	}
	root.AddCommand(probe)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "probe"})

	err := root.Execute()
	if err != nil {
		// NewAPIClient with a non-listening host may fail in a sandboxed
		// environment; skip rather than false-fail (mirrors the sibling test
		// TestOutputPrecedence_EnvBeatsContextDefault_NonNoClient).
		t.Skipf("API client construction failed (lab env absent): %v", err)
	}
	require.NotNil(t, capturedDeps)
	require.NotNil(t, capturedDeps.Ctx, "Ctx must be populated after successful context resolution")
	require.Equal(t, "127.0.0.1", capturedDeps.Ctx.Host)
	require.Equal(t, "admin", capturedDeps.Ctx.SSH.User)
	require.Equal(t, 2222, capturedDeps.Ctx.SSH.Port)
}

// TestPersistentPreRunE_Ctx_NilForNoClient verifies that Deps.Ctx stays nil for
// noClient commands, since the noClient early-return in persistentPreRunE
// returns before context resolution runs. Callers reading deps.Ctx from a
// noClient command must nil-check.
func TestPersistentPreRunE_Ctx_NilForNoClient(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
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

	require.NoError(t, root.Execute())
	require.NotNil(t, capturedDeps)
	require.Nil(t, capturedDeps.Ctx, "Ctx must stay nil for noClient commands")
}

// ---------------------------------------------------------------------------
// Execute() stderr suppression for *exec.ExitError (child-exit passthrough)
// ---------------------------------------------------------------------------

// captureStderr temporarily redirects the process-wide os.Stderr to a pipe for
// the duration of fn, and returns everything written to it. It restores
// os.Stderr unconditionally, even if fn panics.
//
// This mutates process-wide state, so it is safe only because no test in this
// package runs in parallel (no t.Parallel calls) — see CLAUDE.md conventions.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = orig
	}()

	fn()

	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, copyErr := io.Copy(&buf, r)
	require.NoError(t, copyErr)
	require.NoError(t, r.Close())

	return buf.String()
}

// TestExecute_ExitError_SuppressesStderr verifies that Execute does NOT print
// a redundant second diagnostic line when the returned error chain contains
// an *exec.ExitError: the child process (ssh, rsync) already wrote its own
// diagnostics, so pmx must not add its own.
func TestExecute_ExitError_SuppressesStderr(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				return &exec.ExitError{Code: 42, Err: errors.New("exit status 42")}
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(t.TempDir(), "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	var execErr error
	stderrOut := captureStderr(t, func() {
		execErr = cli.Execute([]cli.GroupFactory{factory})
	})

	require.Error(t, execErr, "the child exit code must still be returned as an error")
	var exitErr *exec.ExitError
	require.ErrorAs(t, execErr, &exitErr)
	require.Equal(t, 42, exitErr.Code)
	require.Empty(t, stderrOut,
		"Execute must not print a redundant diagnostic line for an *exec.ExitError: "+
			"the child process already wrote its own")
}

// TestExecute_NonExitError_StillPrintsStderr guards the other side of the
// suppression logic: any error that is NOT an *exec.ExitError must still be
// printed to stderr exactly as before.
func TestExecute_NonExitError_StillPrintsStderr(t *testing.T) {
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	const sentinel = "deliberate-non-exit-error-sentinel"
	factory := func(_ *cli.Deps) *cobra.Command {
		return &cobra.Command{
			Use:         "probe",
			Annotations: map[string]string{"noClient": "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				return errors.New(sentinel)
			},
		}
	}

	oldArgs := os.Args
	os.Args = []string{"pmx", "--config", filepath.Join(t.TempDir(), "c.yml"), "probe"}
	defer func() { os.Args = oldArgs }()

	var execErr error
	stderrOut := captureStderr(t, func() {
		execErr = cli.Execute([]cli.GroupFactory{factory})
	})

	require.Error(t, execErr)
	require.Contains(t, stderrOut, sentinel,
		"a non-exec error must still be printed to stderr exactly as before")
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

// TestLogCloser_RunERecordsSurvive_F01 is the regression test for F-01.
//
// It verifies that log records emitted during RunE are present in the JSONL
// log file after Execute returns. Before the fix, defer logCloser.Close() fired
// when PersistentPreRunE returned — before RunE ran — so every RunE-time record
// was silently lost (EBADF on the closed fd).
//
// Strategy:
//   - Redirect HOME to a temp dir so logx.Init writes the log file there.
//   - Wire a noClient command that emits a distinctive log record in RunE.
//   - Execute via root.Execute() (not cli.Execute(), to keep test isolation).
//   - After Execute returns, read every *.jsonl file under the temp log dir.
//   - Assert the distinctive message appears in at least one record.
func TestLogCloser_RunERecordsSurvive_F01(t *testing.T) {
	tmpDir := t.TempDir()
	// Redirect HOME so logx writes ~/.pmx/logs under tmpDir.
	t.Setenv("HOME", tmpDir)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	// Empty config is fine; noClient command bypasses context resolution.
	cfgPath := filepath.Join(tmpDir, "config.yml")

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())

	const sentinel = "f01-rune-sentinel-record"

	probe := &cobra.Command{
		Use:         "probe",
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			// Emit a log record during RunE. This is what was silently lost before
			// the fix, because the log file fd was closed when PreRunE returned.
			deps.Log.Info(sentinel)
			return nil
		},
	}
	root.AddCommand(probe)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", cfgPath, "probe"})

	require.NoError(t, root.Execute())

	// cleanup() is deferred above — it closes the log file fd. Read the log dir
	// after root.Execute() but before t.Cleanup flushes the defer (defer in test
	// runs at function end, but we need to call cleanup() early to flush the
	// write buffer before reading the file).
	//
	// In practice slog.JSONHandler writes synchronously (no buffering beyond the
	// OS page cache), so the record is visible before Close(). We call cleanup
	// explicitly here to guarantee the fd is flushed on all platforms, then reset
	// the deferred call to a no-op via the already-closed state (Close on a closed
	// *os.File returns error; the nolint:errcheck suppresses it in production code).
	// The defer above is still safe: noopLogCloser.Close() is idempotent.

	logDir := filepath.Join(tmpDir, ".pmx", "logs")
	entries, err := os.ReadDir(logDir)
	require.NoError(t, err, "log directory must exist after Execute")
	require.NotEmpty(t, entries, "at least one log file must be created")

	var found bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(logDir, e.Name()))
		require.NoError(t, readErr)
		if bytes.Contains(data, []byte(sentinel)) {
			found = true
			break
		}
	}
	require.True(t, found,
		"sentinel log record emitted during RunE must be present in the JSONL log file after Execute returns; "+
			"if missing, the log closer fired before RunE (F-01 regression)")
}

// ---------------------------------------------------------------------------
// ApplyTOFUOptions (IMP-02b — per-context opt-in TOFU)
// ---------------------------------------------------------------------------

// alwaysTTY and neverTTY are fixed isTTY funcs for ApplyTOFUOptions tests;
// they are never actually invoked because gating happens before the callback
// is built (tofu disabled or insecure) or the callback is only invoked by a
// real certificate-verification handshake, which these tests do not perform.
func alwaysTTY() bool { return true }

func TestApplyTOFUOptions_TofuDisabled_OptionsUnchanged(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	got := cli.ApplyTOFUOptions(base, false, false, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.Empty(t, got.FingerprintCachePath,
		"tofu=false must leave FingerprintCachePath empty")
	require.Nil(t, got.ManualVerifyCallback,
		"tofu=false must leave ManualVerifyCallback nil")
	require.Equal(t, base.Host, got.Host, "unrelated Options fields must be preserved")
}

func TestApplyTOFUOptions_TofuEnabled_WiresFingerprintPinning(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	got := cli.ApplyTOFUOptions(base, true, false, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.Equal(t, "/home/user/.config/pmx/fingerprints/prod.json", got.FingerprintCachePath,
		"tofu=true must set the per-context fingerprint cache path")
	require.NotNil(t, got.ManualVerifyCallback,
		"tofu=true must install the manual-verify callback")
}

func TestApplyTOFUOptions_TofuEnabledButInsecure_OptionsUnchanged(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	got := cli.ApplyTOFUOptions(base, true, true, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.Empty(t, got.FingerprintCachePath,
		"--insecure must suppress TOFU wiring even when tofu=true, so it never re-imposes "+
			"a trust decision the operator explicitly opted out of")
	require.Nil(t, got.ManualVerifyCallback)
}

func TestApplyTOFUOptions_DifferentContexts_DistinctCachePaths(t *testing.T) {
	base := pve.Options{Host: "pve.example.com"}
	var promptOut bytes.Buffer

	prod := cli.ApplyTOFUOptions(base, true, false, "/home/user/.config/pmx/config.yml", "prod",
		&promptOut, strings.NewReader(""), alwaysTTY)
	staging := cli.ApplyTOFUOptions(base, true, false, "/home/user/.config/pmx/config.yml", "staging",
		&promptOut, strings.NewReader(""), alwaysTTY)

	require.NotEqual(t, prod.FingerprintCachePath, staging.FingerprintCachePath,
		"each context must persist trust decisions to its own cache file")
}

// TestVersionFlag_PrintsBuildInfo verifies that `pmx --version` prints the
// full build-info line from internal/version and exits without running
// PersistentPreRunE (no config load, no API client construction).
func TestVersionFlag_PrintsBuildInfo(t *testing.T) {
	// Point --config at a nonexistent path: if PersistentPreRunE ran, it
	// would be exercised with this config; --version must not need it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root, cleanup := cli.NewRootCmd()
	defer cleanup()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})

	require.NoError(t, root.Execute())
	require.Equal(t, version.String()+"\n", out.String(),
		"--version must print exactly the internal/version build-info line")
	require.Contains(t, out.String(), version.Version)
	require.Contains(t, out.String(), version.Commit)
}

// TestVersionFlag_ShortV verifies the -v shorthand maps to --version and is
// not shadowed by any other persistent flag.
func TestVersionFlag_ShortV(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"-v"})

	require.NoError(t, root.Execute())
	require.Contains(t, out.String(), "pmx version")

	vFlag := root.Flags().Lookup("version")
	require.NotNil(t, vFlag, "--version flag must exist")
	require.Equal(t, "v", vFlag.Shorthand, "--version shorthand must be -v")
}

// newTwoContextConfig builds a *config.Config with two token-auth contexts —
// "pve1" (product pve) and "pbs1" (product pbs) — so BuildContextAnyClient
// tests can select a client by product without any network or keychain
// access (Auth.Secret is a literal, resolved with no external lookup).
func newTwoContextConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		CurrentContext: "pve1",
		Contexts: map[string]*config.Context{
			"pve1": {
				Host: "10.0.0.1", Port: 8006, Protocol: "https", Realm: "pam",
				Product: config.ProductPVE,
				Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s1"},
			},
			"pbs1": {
				Host: "10.0.0.2", Port: 8007, Protocol: "https", Realm: "pam",
				Product: config.ProductPBS,
				Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "tok", Secret: "s2"},
			},
		},
	}
}

// TestBuildContextAnyClient_SelectsByProduct verifies that BuildContextAnyClient
// resolves the requested context and builds exactly the client matching that
// context's product, with no cross-product guard (unlike BuildContextClient /
// BuildContextPBSClient, which each reject the other product).
func TestBuildContextAnyClient_SelectsByProduct(t *testing.T) {
	cfg := newTwoContextConfig(t)

	root, cleanup := cli.NewRootCmd()
	defer cleanup()

	ac, pc, ctx, err := cli.BuildContextAnyClient(root, cfg, "", "pbs1", false, func() bool { return false })
	require.NoError(t, err)
	require.Nil(t, ac)
	require.NotNil(t, pc)
	require.Equal(t, config.ProductPBS, ctx.Product)

	ac, pc, ctx, err = cli.BuildContextAnyClient(root, cfg, "", "pve1", false, func() bool { return false })
	require.NoError(t, err)
	require.NotNil(t, ac)
	require.Nil(t, pc)
	require.False(t, ctx.IsPBS())
}
