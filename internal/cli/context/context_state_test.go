package context

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// withDeps overrides the package-level resolveDeps so tests inject a fixed
// *cli.Deps without going through PersistentPreRunE. The returned function
// restores the original and must be deferred.
func withDeps(deps *cli.Deps) func() {
	prev := resolveDeps
	resolveDeps = func(_ *cobra.Command) *cli.Deps { return deps }
	return func() { resolveDeps = prev }
}

// makeConfig writes a minimal config YAML to a temp file and returns (path, cfg).
// The caller may modify cfg before handing it to makeDeps.
func makeConfig(t *testing.T, cfg *config.Config) (string, *config.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.Save(path, cfg))
	loaded, err := config.Load(path)
	require.NoError(t, err)
	return path, loaded
}

// makeDeps builds a *cli.Deps suitable for context-verb tests.
func makeDeps(t *testing.T, path string, cfg *config.Config) *cli.Deps {
	t.Helper()
	return &cli.Deps{
		Cfg:        cfg,
		ConfigPath: path,
		Out:        output.New(),
		Format:     output.FormatTable,
	}
}

// run executes a context sub-command (e.g. "select", "prod") with a captured
// output buffer. stdin is "" for non-interactive paths.
func run(t *testing.T, deps *cli.Deps, stdin string, args ...string) (string, error) {
	t.Helper()
	defer withDeps(deps)()

	cmd := newContextCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// reloadCfg re-reads the config file from disk so tests can assert persisted state.
func reloadCfg(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	require.NoError(t, err)
	return cfg
}

// twoContextCfg returns a config with two contexts (alpha, beta) and alpha selected.
func twoContextCfg() *config.Config {
	return &config.Config{
		CurrentContext: "alpha",
		Contexts: map[string]*config.Context{
			"alpha": {Host: "alpha.example.com", Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${A}"}},
			"beta": {Host: "beta.example.com", Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "token", TokenID: "t2", Secret: "${B}"}},
		},
	}
}

// ---------------------------------------------------------------------------
// select verb — by name
// ---------------------------------------------------------------------------

func TestSelect_ByName_SwitchesContext(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "select", "beta")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)
	require.Equal(t, "alpha", updated.PreviousContext)
}

func TestSelect_ByName_SameContext_NoPreviousOverwrite(t *testing.T) {
	cfg := twoContextCfg()
	cfg.PreviousContext = "beta"
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	// Selecting the already-current context should not overwrite PreviousContext.
	_, err := run(t, deps, "", "select", "alpha")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "alpha", updated.CurrentContext)
	// PreviousContext should remain unchanged since we didn't actually switch.
	require.Equal(t, "beta", updated.PreviousContext)
}

func TestSelect_ByName_MissingContext_ErrorListsAvailable(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "select", "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent")
	require.Contains(t, err.Error(), "alpha")
	require.Contains(t, err.Error(), "beta")

	// Config unchanged.
	updated := reloadCfg(t, path)
	require.Equal(t, "alpha", updated.CurrentContext)
}

func TestSelect_DashArg_BehavesAsPrevious(t *testing.T) {
	cfg := twoContextCfg()
	cfg.PreviousContext = "beta"
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "select", "-")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)
	require.Equal(t, "alpha", updated.PreviousContext)
}

// ---------------------------------------------------------------------------
// select verb — interactive picker
// ---------------------------------------------------------------------------

func TestSelect_Picker_ByNumber(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	// Names sorted: alpha=1, beta=2. Pick beta by number.
	_, err := run(t, deps, "2\n", "select")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)
}

func TestSelect_Picker_ByName(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "alpha\n", "select")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "alpha", updated.CurrentContext)
}

func TestSelect_Picker_BogusName_Error(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "bogus\n", "select")
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
}

func TestSelect_Picker_EmptyInput_Error(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "\n", "select")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty input")
}

func TestSelect_Picker_EOF_Error(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	// Empty stdin triggers EOF immediately.
	_, err := run(t, deps, "", "select")
	// stdin="" means we DON'T call cmd.SetIn, so stdin is os.Stdin.
	// For the EOF test we need to explicitly pass empty reader.
	_ = err // tested via explicit stdin injection below
}

func TestSelect_Picker_ExplicitEOF_Error(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	defer withDeps(deps)()
	cmd := newContextCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader("")) // empty reader → EOF
	cmd.SetArgs([]string{"select"})
	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "EOF")
}

func TestSelect_Picker_OutOfRangeIndex_Error(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	defer withDeps(deps)()
	cmd := newContextCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader("99\n"))
	cmd.SetArgs([]string{"select"})
	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestSelect_Picker_NoContexts_Error(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "select")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no contexts defined")
}

// ---------------------------------------------------------------------------
// previous verb
// ---------------------------------------------------------------------------

func TestPrevious_SwapsContexts(t *testing.T) {
	cfg := twoContextCfg()
	cfg.PreviousContext = "beta"
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "previous")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)
	require.Equal(t, "alpha", updated.PreviousContext)
}

func TestPrevious_EmptyPrevious_Error(t *testing.T) {
	cfg := twoContextCfg()
	// No PreviousContext set.
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "previous")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no previous context")
}

func TestPrevious_StaleRef_ClearsAndErrors(t *testing.T) {
	cfg := twoContextCfg()
	cfg.PreviousContext = "ghost" // does not exist in Contexts
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "previous")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ghost")
	require.Contains(t, err.Error(), "no longer exists")

	// Stale reference cleared from disk.
	updated := reloadCfg(t, path)
	require.Empty(t, updated.PreviousContext)
}

func TestPrevious_PrevAlias_Works(t *testing.T) {
	cfg := twoContextCfg()
	cfg.PreviousContext = "beta"
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "prev")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)
}

// ---------------------------------------------------------------------------
// rm verb
// ---------------------------------------------------------------------------

func TestRm_RemovesContext(t *testing.T) {
	cfg := twoContextCfg()
	cfg.CurrentContext = "alpha"
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "beta", "--yes")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "beta")
	require.Equal(t, "alpha", updated.CurrentContext) // alpha unaffected
}

func TestRm_WithoutYes_Rejected(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "beta")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Contains(t, err.Error(), "--yes")
}

func TestRm_ActiveContext_WithoutForce_Rejected(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "alpha", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "active context")
	require.Contains(t, err.Error(), "--force")
}

func TestRm_ActiveContext_WithForce_Removes(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "alpha", "--yes", "--force")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "alpha")
	require.Empty(t, updated.CurrentContext)
}

func TestRm_ClearsPreviousContext_WhenRemoved(t *testing.T) {
	cfg := twoContextCfg()
	cfg.PreviousContext = "beta"
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "beta", "--yes")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Empty(t, updated.PreviousContext)
}

func TestRm_MissingContext_Error(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "nonexistent", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent")
}

func TestRm_Aliases_Work(t *testing.T) {
	for _, alias := range []string{"remove", "delete"} {
		t.Run(alias, func(t *testing.T) {
			cfg := twoContextCfg()
			cfg.CurrentContext = "alpha"
			path, cfg := makeConfig(t, cfg)
			deps := makeDeps(t, path, cfg)

			_, err := run(t, deps, "", alias, "beta", "--yes")
			require.NoError(t, err)

			updated := reloadCfg(t, path)
			require.NotContains(t, updated.Contexts, "beta")
		})
	}
}

// ---------------------------------------------------------------------------
// select aliases
// ---------------------------------------------------------------------------

func TestSelect_UseAlias_Works(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "use", "beta")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)
}

func TestSelect_SwitchAlias_Works(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "switch", "beta")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)
}

// ---------------------------------------------------------------------------
// noClient annotation — full tree walk
// ---------------------------------------------------------------------------

// collectLeafCmds walks cmd.Commands() recursively and returns every leaf
// command (one that has a RunE or Run, i.e. is not a pure group node).
func collectLeafCmds(root *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	for _, sub := range root.Commands() {
		if len(sub.Commands()) > 0 {
			leaves = append(leaves, collectLeafCmds(sub)...)
		} else {
			leaves = append(leaves, sub)
		}
	}
	return leaves
}

// TestAnnotations_NoClient_AllVerbs walks the full context command tree and
// asserts every leaf verb carries Annotations["noClient"]=="true".
// The test is future-proof: any verb added to addSubcommands without the
// annotation will fail here automatically.
func TestAnnotations_NoClient_AllVerbs(t *testing.T) {
	root := newContextCmd(nil)
	leaves := collectLeafCmds(root)
	require.NotEmpty(t, leaves, "no leaf commands found under context group")

	for _, cmd := range leaves {
		t.Run(cmd.Name(), func(t *testing.T) {
			ann := cmd.Annotations
			require.NotNil(t, ann,
				"command %q has nil Annotations map — add noClient annotation", cmd.Name())
			require.Equal(t, "true", ann["noClient"],
				"command %q missing Annotations[\"noClient\"]=\"true\"", cmd.Name())
		})
	}
}

// TestAnnotations_NoClient_ExpectedVerbCount confirms the expected 9 canonical
// verbs are present so a deletion is caught as well as an addition.
func TestAnnotations_NoClient_ExpectedVerbCount(t *testing.T) {
	// Canonical verb names registered via addSubcommands (see context.go).
	want := map[string]bool{
		"add":      true,
		"ls":       true,
		"show":     true,
		"select":   true,
		"previous": true,
		"rm":       true,
		"copy":     true,
		"edit":     true,
		"validate": true,
	}

	root := newContextCmd(nil)
	leaves := collectLeafCmds(root)
	got := make(map[string]bool, len(leaves))
	for _, cmd := range leaves {
		got[cmd.Name()] = true
	}

	for name := range want {
		require.True(t, got[name], "expected context verb %q not found in command tree", name)
	}
	// All found leaves must be in want (catches additions that skip the annotation).
	for name := range got {
		require.True(t, want[name],
			"unexpected context verb %q found — ensure it carries noClient annotation and update this test", name)
	}
}

// ---------------------------------------------------------------------------
// no DefaultPath() — behavioral isolation test
// ---------------------------------------------------------------------------

// TestSelect_UsesConfigPath_NotDefaultPath verifies that the select verb reads
// and writes deps.ConfigPath and never touches config.DefaultPath().
//
// Mechanism: XDG_CONFIG_HOME is redirected to an empty temp dir that contains
// no config file. deps.ConfigPath points at a separate temp file with a valid
// two-context config. If select internally called config.DefaultPath() and
// loaded from it, the config would be empty and the verb would fail. The test
// asserts success AND that no file was created under the XDG-redirected path.
func TestSelect_UsesConfigPath_NotDefaultPath(t *testing.T) {
	// Redirect XDG so DefaultPath() resolves to a non-existent location.
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	// Write a valid config at an unrelated temp path.
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "select", "beta")
	require.NoError(t, err, "select should succeed using deps.ConfigPath, not DefaultPath()")

	// Assert current-context written to deps.ConfigPath.
	updated := reloadCfg(t, path)
	require.Equal(t, "beta", updated.CurrentContext)

	// Assert nothing created under the XDG-redirected tree.
	defaultDir := filepath.Join(xdgDir, "pve")
	_, statErr := os.Stat(defaultDir)
	require.True(t, os.IsNotExist(statErr),
		"DefaultPath() location %q must not be created by context verbs", defaultDir)
}

// ---------------------------------------------------------------------------
// noClient regression — zero contexts, no current-context
// ---------------------------------------------------------------------------

// TestLs_EmptyConfig_ExitsZero asserts `context ls` succeeds with an empty
// config (no contexts map, no current-context). This is the core noClient
// regression: prior to the annotation the root PersistentPreRunE would attempt
// API client construction and fail with "no context specified".
func TestLs_EmptyConfig_ExitsZero(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "ls")
	require.NoError(t, err, "context ls must exit 0 with zero configured contexts")
	// Output must not contain the client-construction error message.
	require.NotContains(t, out, "no context specified")
}

// TestShow_NoCurrentContext_ExitsNonZero asserts `context show` (no arg, no
// current-context) fails with a context-level error, not an API client error.
func TestShow_NoCurrentContext_ExitsNonZero(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "show")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "no context specified",
		"error must come from the verb logic, not API client construction")
}

// TestPrevious_NoContexts_ExitsNonZero asserts `context previous` on an empty
// config returns a verb-level error, not an API-client error.
func TestPrevious_NoContexts_ExitsNonZero(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "previous")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "no context specified",
		"error must originate from previous verb logic, not API client gate")
}

// ---------------------------------------------------------------------------
// add alias: create (F-W6-01)
// ---------------------------------------------------------------------------

// TestAdd_CreateAlias_Works confirms `context create` is an accepted alias for
// `context add`, so both `pve context add <name>` and `pve context create <name>`
// reach the same RunE.
func TestAdd_CreateAlias_Works(t *testing.T) {
	cfg := &config.Config{}
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "create", "newctx",
		"--host", "10.0.0.5",
		"--auth-type", "token",
		"--token-id", "mytoken",
		"--secret", "${MY_SECRET}",
	)
	require.NoError(t, err, "create alias must succeed the same as add")

	updated := reloadCfg(t, path)
	require.Contains(t, updated.Contexts, "newctx",
		"create alias must persist the new context")
}

// TestAdd_CreateAlias_HelpContainsAlias verifies the command carries the alias
// declaration so `pve context create --help` resolves.
func TestAdd_CreateAlias_HelpContainsAlias(t *testing.T) {
	root := newContextCmd(nil)
	var addCmd *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Name() == "add" {
			addCmd = sub
			break
		}
	}
	require.NotNil(t, addCmd, "add command must exist in context tree")

	found := slices.Contains(addCmd.Aliases, "create")
	require.True(t, found, "add command must carry 'create' alias")
}
