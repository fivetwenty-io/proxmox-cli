package context

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	yaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

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
	cmd := Group(nil)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
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
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t1", Secret: "${A}"}},
			"beta": {Host: "beta.example.com", Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t2", Secret: "${B}"}},
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

	cmd := Group(nil)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
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

	cmd := Group(nil)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
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
	require.Contains(t, err.Error(), "config has no contexts")
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

func TestSelect_MissingContext_ListsProducts(t *testing.T) {
	cfg := twoContextCfg()
	cfg.Contexts["beta"].Product = config.ProductPBS
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "select", "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "alpha (pve)")
	require.Contains(t, err.Error(), "beta (pbs)")
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
	root := Group(nil)
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

// TestAnnotations_NoClient_ExpectedVerbCount confirms the expected 10 canonical
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
		"rename":   true,
		"update":   true,
		"edit":     true,
		"validate": true,
	}

	root := Group(nil)
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

// TestShow_RendersTofuField asserts `context show` surfaces tls.tofu so an
// operator can see whether TOFU certificate pinning is active for a context.
func TestShow_RendersTofuField(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]*config.Context{
			"prod": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
				TLS:  config.TLSBlock{Tofu: true},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "show")
	require.NoError(t, err)
	require.Contains(t, out, "TOFU")
	require.Contains(t, out, "true")
}

// TestLs_RendersProductColumn asserts `context ls` surfaces an explicit
// product and renders the backward-compat default ("pve") for a context that
// predates the Product field.
func TestLs_RendersProductColumn(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"backup": {
				Host: "pbs.example.com", Port: 8007, Protocol: "https", Product: config.ProductPBS,
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
			},
			"legacy": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "token", TokenID: "t2", Secret: "${S}"},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "ls")
	require.NoError(t, err)
	require.Contains(t, out, "PRODUCT")
	require.Contains(t, out, "pbs")
	require.Contains(t, out, "pve")
}

// TestShow_RendersProductField asserts `context show` surfaces the product
// selector, defaulting an unset Product to "pve" for display.
func TestShow_RendersProductField(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "backup",
		Contexts: map[string]*config.Context{
			"backup": {
				Host: "pbs.example.com", Port: 8007, Protocol: "https", Product: config.ProductPBS,
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "show")
	require.NoError(t, err)
	require.Contains(t, out, "PRODUCT")
	require.Contains(t, out, "pbs")
}

// TestShow_ProductDefaultsToPVEWhenUnset asserts a context with no stored
// Product renders "pve" (backward compat), not an empty value.
func TestShow_ProductDefaultsToPVEWhenUnset(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "legacy",
		Contexts: map[string]*config.Context{
			"legacy": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "show")
	require.NoError(t, err)
	require.Contains(t, out, "pve")
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
// `context add`, so both `pmx context add <name>` and `pmx context create <name>`
// reach the same RunE.
func TestAdd_CreateAlias_Works(t *testing.T) {
	cfg := &config.Config{}
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "create", "newctx",
		"--host", "10.0.0.5",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "mytoken",
		"--secret", "${MY_SECRET}",
	)
	require.NoError(t, err, "create alias must succeed the same as add")

	updated := reloadCfg(t, path)
	require.Contains(t, updated.Contexts, "newctx",
		"create alias must persist the new context")
}

// TestAdd_CreateAlias_HelpContainsAlias verifies the command carries the alias
// declaration so `pmx context create --help` resolves.
func TestAdd_CreateAlias_HelpContainsAlias(t *testing.T) {
	root := Group(nil)
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

// ---------------------------------------------------------------------------
// add --tofu flag (IMP-02b — per-context opt-in TOFU)
// ---------------------------------------------------------------------------

// TestAdd_TofuFlag_Persisted asserts --tofu is written through to tls.tofu.
func TestAdd_TofuFlag_Persisted(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "newctx",
		"--host", "10.0.0.5",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "mytoken",
		"--secret", "${MY_SECRET}",
		"--tofu",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	ctx, ok := updated.Contexts["newctx"]
	require.True(t, ok)
	require.True(t, ctx.TLS.Tofu, "--tofu must persist as tls.tofu: true")
}

// TestAdd_TofuFlag_DefaultsFalse asserts omitting --tofu leaves tls.tofu at
// its zero value (false), preserving the pre-TOFU default behavior.
func TestAdd_TofuFlag_DefaultsFalse(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "newctx",
		"--host", "10.0.0.5",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "mytoken",
		"--secret", "${MY_SECRET}",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	ctx, ok := updated.Contexts["newctx"]
	require.True(t, ok)
	require.False(t, ctx.TLS.Tofu, "omitting --tofu must default to tls.tofu: false")
}

// ---------------------------------------------------------------------------
// persona mismatch and missing-credential warnings (select, previous)
// ---------------------------------------------------------------------------

// runUnderPersona executes a context sub-command mounted under a root named
// persona, capturing stdout and stderr separately so warning assertions can
// distinguish the two streams.
func runUnderPersona(t *testing.T, persona string, deps *cli.Deps, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: persona}
	root.AddCommand(Group(nil))
	root.SetContext(cli.WithDeps(context.Background(), deps))
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"context"}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestSelect_PersonaMismatch_WarnsButSwitches(t *testing.T) {
	cfg := twoContextCfg()
	cfg.Contexts["beta"].Product = config.ProductPDM
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, stderr, err := runUnderPersona(t, "pve", deps, "select", "beta")

	require.NoError(t, err, "mismatch must warn, never block")
	require.Contains(t, stderr, "warning:")
	require.Contains(t, stderr, "Proxmox Datacenter Manager")
	require.Equal(t, "beta", reloadCfg(t, path).CurrentContext)
}

func TestSelect_PersonaMatch_NoWarning(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, stderr, err := runUnderPersona(t, "pve", deps, "select", "beta")

	require.NoError(t, err)
	require.NotContains(t, stderr, "warning:")
}

func TestSelect_UnderPmx_NoPersonaWarning(t *testing.T) {
	cfg := twoContextCfg()
	cfg.Contexts["beta"].Product = config.ProductPBS
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, stderr, err := runUnderPersona(t, "pmx", deps, "select", "beta")

	require.NoError(t, err)
	require.NotContains(t, stderr, "warning:")
}

func TestSelect_MissingCredentials_Notes(t *testing.T) {
	cfg := twoContextCfg()
	cfg.Contexts["beta"].Auth.Secret = ""
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, stderr, err := runUnderPersona(t, "pmx", deps, "select", "beta")

	require.NoError(t, err)
	require.Contains(t, stderr, "no credentials")
	require.Contains(t, stderr, "pmx auth")
}

func TestPrevious_PersonaMismatch_Warns(t *testing.T) {
	cfg := twoContextCfg()
	cfg.PreviousContext = "beta"
	cfg.Contexts["beta"].Product = config.ProductPBS
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, stderr, err := runUnderPersona(t, "pve", deps, "previous")

	require.NoError(t, err)
	require.Contains(t, stderr, "warning:")
	require.Contains(t, stderr, "Proxmox Backup Server")
}

func TestLs_ProductFilter(t *testing.T) {
	cfg := twoContextCfg()
	cfg.Contexts["beta"].Product = config.ProductPBS
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "ls", "--product", "pbs")

	require.NoError(t, err)
	require.Contains(t, out, "beta")
	require.NotContains(t, out, "alpha")
}

func TestLs_ProductFilter_Invalid(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "ls", "--product", "bogus")

	require.Error(t, err)
	require.Contains(t, err.Error(), "pve, pbs, pdm")
}

func TestLs_PersonaMismatchMarker(t *testing.T) {
	cfg := twoContextCfg()
	cfg.Contexts["beta"].Product = config.ProductPBS
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, _, err := runUnderPersona(t, "pve", deps, "ls")

	require.NoError(t, err)
	require.Contains(t, out, "pbs (mismatch)")
	require.NotContains(t, out, "pve (mismatch)")
}

func TestLs_UnderPmx_NoMarker(t *testing.T) {
	cfg := twoContextCfg()
	cfg.Contexts["beta"].Product = config.ProductPBS
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, _, err := runUnderPersona(t, "pmx", deps, "ls")

	require.NoError(t, err)
	require.NotContains(t, out, "(mismatch)")
}

// PDM round-trip: add with --product pdm must default the port to 8443 and
// show/ls must render it, closing the PDM coverage gap in this package.
func TestAdd_PDM_RoundTrip_DefaultPort(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "dcmgr",
		"--product", "pdm",
		"--host", "pdm.example.com",
		"--username", "root@pam",
		"--token-id", "automation",
		"--secret", "${PDM_TOKEN}",
	)
	require.NoError(t, err)

	saved := reloadCfg(t, path)
	require.Equal(t, config.ProductPDM, saved.Contexts["dcmgr"].Product)
	require.Equal(t, 8443, saved.Contexts["dcmgr"].Port,
		"add --product pdm without --port must default to 8443")

	deps = makeDeps(t, path, saved)
	out, err := run(t, deps, "", "show", "dcmgr")
	require.NoError(t, err)
	require.Contains(t, out, "pdm")
	require.Contains(t, out, "8443")

	_, err = run(t, deps, "", "select", "dcmgr")
	require.NoError(t, err)
	require.Equal(t, "dcmgr", reloadCfg(t, path).CurrentContext)
}

// validateConnectCfg returns a config whose single context points at ts.
func validateConnectCfg(t *testing.T, ts *httptest.Server) *config.Config {
	t.Helper()
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return &config.Config{
		CurrentContext: "live",
		Contexts: map[string]*config.Context{
			"live": {
				Host: u.Hostname(), Port: port, Protocol: u.Scheme,
				TLS:  config.TLSBlock{Insecure: true},
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t", Secret: "s"},
			},
		},
	}
}

func TestValidateConnect_Reachable(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer ts.Close()

	path, cfg := makeConfig(t, validateConnectCfg(t, ts))
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "validate", "live", "--connect")

	require.NoError(t, err)
	require.Contains(t, out, "REACHABLE")
	require.Contains(t, out, "yes")
	require.Contains(t, out, "match (")
}

func TestValidateConnect_Unreachable_Fails(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	cfg := validateConnectCfg(t, ts)
	ts.Close()

	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "validate", "live", "--connect")

	require.Error(t, err, "an unreachable context must exit non-zero under --connect")
}

func TestValidateConnect_ProductMismatch_WarnsNotFails(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "proxmox-backup-proxy/3.3")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer ts.Close()

	path, cfg := makeConfig(t, validateConnectCfg(t, ts))
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "validate", "live", "--connect")

	require.NoError(t, err, "a product mismatch is a warning, never a failure")
	require.Contains(t, out, "mismatch")
	require.Contains(t, out, "pbs")
}

func TestValidate_WithoutConnect_Unchanged(t *testing.T) {
	path, cfg := makeConfig(t, twoContextCfg())
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "validate", "alpha")

	require.NoError(t, err)
	require.NotContains(t, out, "REACHABLE", "without --connect the output shape must not change")
}

// TestContextVerbs_ActOnResolvedContextNotCurrent covers the defect where
// `pmx -c lab-ceph context validate --connect` validated whichever context
// current-context pointed at and presented the result as lab-ceph's.
//
// deps.CtxName carries the root's --context/-c > $PMX_CONTEXT >
// current-context resolution. These verbs read cfg.CurrentContext directly
// instead, so every one of them silently acted on the wrong context whenever
// the user named one.
func TestContextVerbs_ActOnResolvedContextNotCurrent(t *testing.T) {
	newCfg := func() *config.Config {
		ctx := func(host string) *config.Context {
			return &config.Context{
				Host: host, Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
			}
		}
		return &config.Config{
			CurrentContext: "current",
			Contexts: map[string]*config.Context{
				"current": ctx("current.example.com"),
				"named":   ctx("named.example.com"),
			},
		}
	}

	// show and validate report a context without needing an editor or a
	// server, so both can assert which one they picked from their output.
	for _, args := range [][]string{{"show"}, {"validate"}} {
		t.Run(args[0], func(t *testing.T) {
			path, cfg := makeConfig(t, newCfg())

			// validate reports an unresolvable secret as an error while
			// still rendering its row, so the rendered output, not the
			// error, is what says which context it picked.
			deps := makeDeps(t, path, cfg)
			out, _ := run(t, deps, "", args...)
			require.Contains(t, out, "current", "with nothing named, current-context still applies")

			deps = makeDeps(t, path, cfg)
			deps.CtxName = "named"
			out, _ = run(t, deps, "", args...)
			require.Contains(t, out, "named")
			require.NotContains(t, out, "current.example.com",
				"the verb must act on the context the invocation named, not on current-context")
		})
	}

	// An explicit argument still outranks everything.
	t.Run("explicit arg wins", func(t *testing.T) {
		path, cfg := makeConfig(t, newCfg())
		deps := makeDeps(t, path, cfg)
		deps.CtxName = "named"

		out, err := run(t, deps, "", "show", "current")
		require.NoError(t, err)
		require.Contains(t, out, "current.example.com")
	})
}

// ---------------------------------------------------------------------------
// show/ls — ssh, jump, proxy, timeout, and CA bundle rows
// ---------------------------------------------------------------------------

// TestContextShow_HelpNamesRootFlags pins that the help names real root
// flags when it says which overrides show ignores. The --timeout-* flags
// belong to add and update, and a saved value does change what show prints.
func TestContextShow_HelpNamesRootFlags(t *testing.T) {
	long := newShowCmd().Long
	require.Contains(t, long, "--api-endpoint and --api-connect-timeout, and PMX_API_* environment variables")
	require.NotContains(t, long, "--timeout-")
}

// allShowFormats runs "show" against deps once per output format and hands
// the rendered text to check, so a test covers table, JSON, and YAML with
// one assertion body.
func allShowFormats(
	t *testing.T, path string, cfg *config.Config, args []string,
	check func(t *testing.T, format output.Format, out string),
) {
	t.Helper()
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
		deps := makeDeps(t, path, cfg)
		deps.Format = format
		out, err := run(t, deps, "", args...)
		require.NoError(t, err, "format %s", format)
		check(t, format, out)
	}
}

// fullConnectionContext returns a context with every ssh, proxy, and timeout
// field populated, so a rendering test can assert on a known, non-default
// value for each of the twelve rows.
func fullConnectionContext() *config.Context {
	fromEnv := true
	return &config.Context{
		Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t1", Secret: "${SECRET}"},
		TLS:  config.TLSBlock{CACert: "/etc/pmx/ca.pem"},
		SSH: config.SSHBlock{
			User:     "admin",
			Port:     2222,
			Identity: "/home/op/.ssh/id_ed25519",
			Jump:     "admin@bastion.example.com",
		},
		Proxy: config.ProxyBlock{
			URL:      "socks5h://proxy.example.com:1080",
			Username: "proxyuser",
			Password: "hunter2literal",
			FromEnv:  &fromEnv,
		},
		Timeout: config.TimeoutBlock{
			Connect:      "2s",
			TLSHandshake: "8s",
			Request:      "45s",
		},
	}
}

// unparseableProxyURLs are the three proxy.url shapes url.Parse rejects (see
// redact.ProxyURL), each carrying its password in a different unparseable
// position. show and ls share this fixture so both redact the same shapes.
var unparseableProxyURLs = []struct {
	name   string
	url    string
	secret string
}{
	{name: "unescaped slash in password", url: "socks5://pmx:s3cr3t/x@proxy:1080", secret: "s3cr3t"},
	{name: "invalid percent-escape in password", url: "socks5://pmx:s3%zzt@proxy:1080", secret: "s3%zzt"},
	{name: "unterminated ipv6 literal", url: "socks5://u:s3cret@[::1", secret: "s3cret"},
}

// TestContextShow_RendersConnectionFields asserts all twelve connection rows
// (ssh user/port/identity, jump, proxy url/username/password/from-env, the
// three timeouts, and the CA bundle) render with the stored value, in table,
// JSON, and YAML form, and that the literal proxy password never appears in
// any of them.
func TestContextShow_RendersConnectionFields(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": fullConnectionContext()},
	}
	path, cfg := makeConfig(t, cfg)

	allShowFormats(t, path, cfg, []string{"show"}, func(t *testing.T, format output.Format, out string) {
		t.Helper()
		require.NotContains(t, out, "hunter2literal", "format %s must never carry the literal proxy password", format)

		if format == output.FormatTable {
			for _, want := range []string{
				"SSH USER", "admin",
				"SSH PORT", "2222",
				"SSH IDENTITY", "/home/op/.ssh/id_ed25519",
				"JUMP", "admin@bastion.example.com",
				"PROXY", "socks5h://proxy.example.com:1080",
				"PROXY USERNAME", "proxyuser",
				"PROXY PASSWORD", "***",
				"PROXY FROM ENV", "true",
				"TIMEOUT CONNECT", "2s",
				"TIMEOUT TLS HANDSHAKE", "8s",
				"TIMEOUT REQUEST", "45s",
				"CA CERT", "/etc/pmx/ca.pem",
			} {
				require.Contains(t, out, want)
			}
			return
		}

		var got map[string]any
		switch format {
		case output.FormatJSON:
			require.NoError(t, json.Unmarshal([]byte(out), &got))
		case output.FormatYAML:
			require.NoError(t, yaml.Unmarshal([]byte(out), &got))
		}
		require.Equal(t, "admin", got["ssh_user"])
		// JSON decodes a number into float64 and YAML (goccy) into uint64, so
		// the numeric comparison uses EqualValues rather than pinning a type.
		require.EqualValues(t, 2222, got["ssh_port"])
		require.Equal(t, "/home/op/.ssh/id_ed25519", got["ssh_identity"])
		require.Equal(t, "admin@bastion.example.com", got["jump"])
		require.Equal(t, "socks5h://proxy.example.com:1080", got["proxy"])
		require.Equal(t, "proxyuser", got["proxy_username"])
		require.Equal(t, "***", got["proxy_password"])
		require.Equal(t, true, got["proxy_from_env"])
		require.Equal(t, "2s", got["timeout_connect"])
		require.Equal(t, "8s", got["timeout_tls_handshake"])
		require.Equal(t, "45s", got["timeout_request"])
		require.Equal(t, "/etc/pmx/ca.pem", got["ca_cert"])
	})
}

// TestContextShow_RedactsProxyPassword asserts a proxy password never
// survives to output, whether it is a syntactically-invalid literal such as
// "$uper$ecret" or embedded in one of the three proxy.url shapes url.Parse
// rejects, and that a ${VAR} or keychain: reference renders verbatim
// instead, since a reference carries no secret of its own.
func TestContextShow_RedactsProxyPassword(t *testing.T) {
	t.Run("dollar-prefixed literal is masked", func(t *testing.T) {
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts: map[string]*config.Context{
				"lab": {
					Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
					Auth:  config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
					Proxy: config.ProxyBlock{URL: "socks5h://proxy.example.com:1080", Password: "$uper$ecret"},
				},
			},
		}
		path, cfg := makeConfig(t, cfg)

		allShowFormats(t, path, cfg, []string{"show"}, func(t *testing.T, format output.Format, out string) {
			t.Helper()
			require.Contains(t, out, "***")
			require.NotContains(t, out, "$uper$ecret")
		})
	})

	// requireProxyPasswordVerbatim runs show against a proxy.password of ref
	// in all three formats and asserts the table cell and the decoded
	// proxy_password field equal ref exactly, catching a guard that masks a
	// reference it should show as-is.
	requireProxyPasswordVerbatim := func(t *testing.T, ref string) {
		t.Helper()
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts: map[string]*config.Context{
				"lab": {
					Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
					Auth:  config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
					Proxy: config.ProxyBlock{URL: "socks5h://proxy.example.com:1080", Password: ref},
				},
			},
		}
		path, cfg := makeConfig(t, cfg)

		allShowFormats(t, path, cfg, []string{"show"}, func(t *testing.T, format output.Format, out string) {
			t.Helper()
			if format == output.FormatTable {
				require.Contains(t, out, ref)
				return
			}
			var got map[string]any
			switch format {
			case output.FormatJSON:
				require.NoError(t, json.Unmarshal([]byte(out), &got))
			case output.FormatYAML:
				require.NoError(t, yaml.Unmarshal([]byte(out), &got))
			}
			require.Equal(t, ref, got["proxy_password"], "format %s must show %q verbatim", format, ref)
		})
	}

	t.Run("dollar-brace reference is shown verbatim, variable unset", func(t *testing.T) {
		const varName = "PMX_TEST_PROXY_PW_UNSET"
		t.Setenv(varName, "")
		require.NoError(t, os.Unsetenv(varName))
		requireProxyPasswordVerbatim(t, "${"+varName+"}")
	})

	t.Run("dollar-brace reference is shown verbatim, variable set", func(t *testing.T) {
		const varName = "PMX_TEST_PROXY_PW_SET"
		t.Setenv(varName, "irrelevant-value")
		requireProxyPasswordVerbatim(t, "${"+varName+"}")
	})

	t.Run("keychain reference is shown verbatim", func(t *testing.T) {
		requireProxyPasswordVerbatim(t, "keychain:pmx/proxy")
	})

	for _, tc := range unparseableProxyURLs {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				CurrentContext: "lab",
				Contexts: map[string]*config.Context{
					"lab": {
						Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
						Auth:  config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
						Proxy: config.ProxyBlock{URL: tc.url},
					},
				},
			}
			path, cfg := makeConfig(t, cfg)
			wantProxy := redact.ProxyURL(tc.url)
			require.Contains(t, wantProxy, redact.Placeholder, "test fixture sanity: url must actually mask")

			allShowFormats(t, path, cfg, []string{"show"}, func(t *testing.T, format output.Format, out string) {
				t.Helper()
				require.NotContains(t, out, tc.secret)

				// JSON HTML-escapes "<" and ">", so the raw text check above
				// (which the secret itself must pass regardless of escaping)
				// is paired with a decoded-field check for the placeholder.
				if format == output.FormatTable {
					require.Contains(t, out, wantProxy)
					return
				}
				var got map[string]any
				switch format {
				case output.FormatJSON:
					require.NoError(t, json.Unmarshal([]byte(out), &got))
				case output.FormatYAML:
					require.NoError(t, yaml.Unmarshal([]byte(out), &got))
				}
				require.Equal(t, wantProxy, got["proxy"])
			})
		})
	}
}

// TestContextShow_RedactsProxyURLCredentials asserts a proxy.url that
// url.Parse accepts, and that carries userinfo, never prints its password in
// show output, in table, JSON, or YAML form, and that the redacted value
// equals redact.ProxyURL(url) exactly.
func TestContextShow_RedactsProxyURLCredentials(t *testing.T) {
	const rawURL = "socks5://proxyuser:sw0rdfish@proxy.example.com:1080"
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
				Auth:  config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
				Proxy: config.ProxyBlock{URL: rawURL},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	wantProxy := redact.ProxyURL(rawURL)
	require.Contains(t, wantProxy, redact.Placeholder, "test fixture sanity: url must actually mask")

	allShowFormats(t, path, cfg, []string{"show"}, func(t *testing.T, format output.Format, out string) {
		t.Helper()
		require.NotContains(t, out, "sw0rdfish", "format %s must never carry the proxy url password", format)

		if format == output.FormatTable {
			require.Contains(t, out, wantProxy)
			return
		}
		var got map[string]any
		switch format {
		case output.FormatJSON:
			require.NoError(t, json.Unmarshal([]byte(out), &got))
		case output.FormatYAML:
			require.NoError(t, yaml.Unmarshal([]byte(out), &got))
		}
		require.Equal(t, wantProxy, got["proxy"], "format %s must carry the redacted proxy url", format)
	})
}

// TestContextShow_RedactsJumpPassword asserts a stored ssh.jump chain that
// ValidateJumpChain rejects, because it carries a misused user:password hop,
// never prints the password in show output, in table, JSON, or YAML form,
// and that the redacted value equals apiclient.RedactJumpChain(jump)
// exactly. A chain the validator accepts still shows the plain hop, so the
// fixture (fullConnectionContext, covered by
// TestContextShow_RendersConnectionFields) is left untouched by this fix.
func TestContextShow_RedactsJumpPassword(t *testing.T) {
	const rawJump = "admin:s3cret@bastion.example.com"
	require.Error(t, apiclient.ValidateJumpChain(rawJump),
		"test fixture sanity: chain must be one the validator rejects")
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
				SSH:  config.SSHBlock{Jump: rawJump},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	wantJump := apiclient.RedactJumpChain(rawJump)
	require.NotContains(t, wantJump, "s3cret", "test fixture sanity: redaction must actually mask")

	allShowFormats(t, path, cfg, []string{"show"}, func(t *testing.T, format output.Format, out string) {
		t.Helper()
		require.NotContains(t, out, "s3cret", "format %s must never carry the jump chain password", format)

		if format == output.FormatTable {
			require.Contains(t, out, wantJump)
			return
		}
		var got map[string]any
		switch format {
		case output.FormatJSON:
			require.NoError(t, json.Unmarshal([]byte(out), &got))
		case output.FormatYAML:
			require.NoError(t, yaml.Unmarshal([]byte(out), &got))
		}
		require.Equal(t, wantJump, got["jump"], "format %s must carry the redacted jump chain", format)
	})
}

// TestContextShow_MasksUnsetDollarName asserts a $NAME proxy password
// classifies as a reference (config.IsSecretReference) but renders as "***"
// while NAME is unset in the environment, because config.ResolveSecret would
// fall through and use it as a literal, and renders verbatim once NAME is
// set, because it would then resolve as the referenced variable's value.
func TestContextShow_MasksUnsetDollarName(t *testing.T) {
	const varName = "PMX_TEST_HUNTER2_VAR"
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
				Auth:  config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
				Proxy: config.ProxyBlock{Password: "$" + varName},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)

	t.Run("unset", func(t *testing.T) {
		// t.Setenv first, so its cleanup restores whatever the test process had
		// before this test ran; the immediate Unsetenv then clears it for the
		// duration of the subtest without leaking the removal past it.
		t.Setenv(varName, "")
		require.NoError(t, os.Unsetenv(varName))
		deps := makeDeps(t, path, cfg)
		out, err := run(t, deps, "", "show")
		require.NoError(t, err)
		require.Contains(t, out, "***")
		require.NotContains(t, out, "$"+varName)
	})

	t.Run("set", func(t *testing.T) {
		t.Setenv(varName, "irrelevant-value")
		deps := makeDeps(t, path, cfg)
		out, err := run(t, deps, "", "show")
		require.NoError(t, err)
		require.Contains(t, out, "$"+varName)
	})
}

// TestContextShow_RendersEffectiveProtocolAndRealm asserts a context that
// stores neither protocol nor realm renders "https" and "pam" — the values
// the API connection would use — in all three output formats, whether or
// not it is the current context, and that rendering never mutates the
// stored context. It builds a fresh config for every format and asserts the
// in-memory context right after each run, so a defaults-applied write that
// only reaches cfg.Contexts (never the file on disk) cannot hide behind a
// later run's correct read of the config it mutated, or behind a
// disk-reload check that a purely in-memory write could never fail.
func TestContextShow_RendersEffectiveProtocolAndRealm(t *testing.T) {
	bareCtx := func() *config.Context {
		return &config.Context{
			Host: "bare.example.com",
			Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
		}
	}

	for _, tc := range []struct {
		name    string
		current string
		show    []string
	}{
		{name: "current context", current: "bare", show: []string{"show"}},
		{name: "named, not current", current: "other", show: []string{"show", "bare"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
				cfg := &config.Config{
					CurrentContext: tc.current,
					Contexts: map[string]*config.Context{
						"bare":  bareCtx(),
						"other": bareCtx(),
					},
				}
				path, cfg := makeConfig(t, cfg)
				deps := makeDeps(t, path, cfg)
				deps.Format = format

				out, err := run(t, deps, "", tc.show...)
				require.NoError(t, err, "format %s", format)
				require.Contains(t, out, "https", "format %s", format)
				require.Contains(t, out, "pam", "format %s", format)

				// The stored context must stay bare in memory: rendering builds
				// a defaults-applied clone and must never write it back into
				// cfg.Contexts.
				stored := cfg.Contexts["bare"]
				require.Empty(t, stored.Protocol, "format %s must not mutate the in-memory protocol", format)
				require.Empty(t, stored.Realm, "format %s must not mutate the in-memory realm", format)
				require.Zero(t, stored.Port, "format %s must not mutate the in-memory port", format)

				// The stored context is unchanged on disk too: show never saves.
				reloaded := reloadCfg(t, path)
				require.Empty(t, reloaded.Contexts["bare"].Protocol, "format %s", format)
				require.Empty(t, reloaded.Contexts["bare"].Realm, "format %s", format)
			}
		})
	}
}

// TestContextShow_RendersTimeoutDefaults asserts an unset timeout.connect
// renders the built-in default marked "(default)", and a stored one renders
// verbatim with no suffix. It also asserts all three timeout defaults in the
// decoded JSON and YAML fields, not just the table's "5s (default)"
// substring, and that an absent from-env key resolves and renders as false.
func TestContextShow_RendersTimeoutDefaults(t *testing.T) {
	t.Run("unset renders default", func(t *testing.T) {
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts: map[string]*config.Context{
				"lab": {
					Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
					Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
				},
			},
		}
		path, cfg := makeConfig(t, cfg)

		allShowFormats(t, path, cfg, []string{"show"}, func(t *testing.T, format output.Format, out string) {
			t.Helper()
			if format == output.FormatTable {
				require.Contains(t, out, "5s (default)")
				require.Contains(t, out, "10s (default)")
				require.Contains(t, out, "30s (default)")
				require.Contains(t, out, "PROXY FROM ENV")
				require.Contains(t, out, "false")
				return
			}
			var got map[string]any
			switch format {
			case output.FormatJSON:
				require.NoError(t, json.Unmarshal([]byte(out), &got))
			case output.FormatYAML:
				require.NoError(t, yaml.Unmarshal([]byte(out), &got))
			}
			require.Equal(t, "5s (default)", got["timeout_connect"], "format %s", format)
			require.Equal(t, "10s (default)", got["timeout_tls_handshake"], "format %s", format)
			require.Equal(t, "30s (default)", got["timeout_request"], "format %s", format)
			require.Equal(t, false, got["proxy_from_env"],
				"format %s: an absent from-env key must resolve to false", format)
		})
	})

	t.Run("stored value renders verbatim", func(t *testing.T) {
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts: map[string]*config.Context{
				"lab": {
					Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
					Auth:    config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
					Timeout: config.TimeoutBlock{Connect: "2s"},
				},
			},
		}
		path, cfg := makeConfig(t, cfg)
		deps := makeDeps(t, path, cfg)

		out, err := run(t, deps, "", "show")
		require.NoError(t, err)
		require.Contains(t, out, "2s")
		require.NotContains(t, out, "2s (default)")
	})
}

// TestContextShow_RendersInvalidTimeout asserts an unparseable timeout
// renders as its stored string followed by " (invalid)", and that `show`
// still exits 0 — the verb an operator reaches for on a broken context must
// survive a bad value rather than failing on it.
func TestContextShow_RendersInvalidTimeout(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
				Auth:    config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
				Timeout: config.TimeoutBlock{Connect: "5 seconds"},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "show")
	require.NoError(t, err, "an unparseable timeout must not fail context show")
	require.Contains(t, out, "5 seconds (invalid)")
}

// TestContextLs_RawCarriesJumpAndProxy asserts the ls JSON entries carry the
// jump and proxy keys even when both are unset, and that the table headers
// gain no new columns for them.
func TestContextLs_RawCarriesJumpAndProxy(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)

	deps := makeDeps(t, path, cfg)
	deps.Format = output.FormatJSON
	out, err := run(t, deps, "", "ls")
	require.NoError(t, err)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &entries))
	require.Len(t, entries, 1)
	entry := entries[0]
	require.Contains(t, entry, "jump")
	require.Contains(t, entry, "proxy")
	require.Equal(t, "", entry["jump"])
	require.Equal(t, "", entry["proxy"])

	deps = makeDeps(t, path, cfg)
	out, err = run(t, deps, "", "ls")
	require.NoError(t, err)
	requireExactLsHeaderRow(t, out)
}

// requireExactLsHeaderRow asserts the ls table header line is exactly the
// eight names ls has always carried, in order, so a new column or a
// reordered header fails the test rather than passing on a partial match.
// tablewriter draws a border line before the header, so the check reads the
// second line, strips the column-separator glyph, and collapses whitespace
// by splitting on fields, since a two-word header such as "AUTH TYPE"
// renders as two space-separated tokens, same as any other column boundary.
func requireExactLsHeaderRow(t *testing.T, out string) {
	t.Helper()
	wantFields := []string{
		"NAME", "HOST", "PORT", "PRODUCT", "AUTH", "TYPE", "USERNAME", "DEFAULT", "NODE", "DEFAULT", "OUTPUT",
	}
	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 2, "ls table output must have a border line and a header line")
	headerLine := strings.ReplaceAll(lines[1], "│", " ")
	gotFields := strings.Fields(headerLine)
	require.Equal(t, wantFields, gotFields, "ls table header row must carry exactly these columns, in order")
}

// TestContextLs_RedactsProxyURL asserts a context whose proxy.url carries
// userinfo never prints the password in ls JSON or YAML output, whether the
// URL parses or is one of the three unparseable forms redact.ProxyURL still
// masks by inspecting the raw string.
func TestContextLs_RedactsProxyURL(t *testing.T) {
	t.Run("parseable url with credentials", func(t *testing.T) {
		const rawURL = "socks5://proxyuser:sw0rdfish@proxy.example.com:1080"
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts: map[string]*config.Context{
				"lab": {
					Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
					Auth:  config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
					Proxy: config.ProxyBlock{URL: rawURL},
				},
			},
		}
		path, cfg := makeConfig(t, cfg)
		wantProxy := redact.ProxyURL(rawURL)
		require.Contains(t, wantProxy, redact.Placeholder, "test fixture sanity: url must actually mask")

		for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
			deps := makeDeps(t, path, cfg)
			deps.Format = format
			out, err := run(t, deps, "", "ls")
			require.NoError(t, err, "format %s", format)
			require.NotContains(t, out, "sw0rdfish", "format %s must never carry the proxy password", format)

			var entries []map[string]any
			switch format {
			case output.FormatJSON:
				require.NoError(t, json.Unmarshal([]byte(out), &entries))
			case output.FormatYAML:
				require.NoError(t, yaml.Unmarshal([]byte(out), &entries))
			}
			require.Len(t, entries, 1)
			require.Equal(t, wantProxy, entries[0]["proxy"], "format %s must carry the redacted proxy url", format)
		}
	})

	for _, tc := range unparseableProxyURLs {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				CurrentContext: "lab",
				Contexts: map[string]*config.Context{
					"lab": {
						Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
						Auth:  config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
						Proxy: config.ProxyBlock{URL: tc.url},
					},
				},
			}
			path, cfg := makeConfig(t, cfg)
			wantProxy := redact.ProxyURL(tc.url)
			require.Contains(t, wantProxy, redact.Placeholder, "test fixture sanity: url must actually mask")

			for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
				deps := makeDeps(t, path, cfg)
				deps.Format = format
				out, err := run(t, deps, "", "ls")
				require.NoError(t, err, "format %s", format)
				require.NotContains(t, out, tc.secret, "format %s must never carry the proxy url password", format)

				var entries []map[string]any
				switch format {
				case output.FormatJSON:
					require.NoError(t, json.Unmarshal([]byte(out), &entries))
				case output.FormatYAML:
					require.NoError(t, yaml.Unmarshal([]byte(out), &entries))
				}
				require.Len(t, entries, 1)
				require.Equal(t, wantProxy, entries[0]["proxy"], "format %s must carry the redacted proxy url", format)
			}
		})
	}
}

// TestContextLs_RedactsJumpPassword asserts a stored ssh.jump chain that
// ValidateJumpChain rejects never prints the password in ls JSON or YAML
// output, and that the redacted value equals apiclient.RedactJumpChain(jump)
// exactly.
func TestContextLs_RedactsJumpPassword(t *testing.T) {
	const rawJump = "admin:s3cret@bastion.example.com"
	require.Error(t, apiclient.ValidateJumpChain(rawJump),
		"test fixture sanity: chain must be one the validator rejects")
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: "pve.example.com", Port: 8006, Protocol: "https", Realm: "pam",
				Auth: config.AuthBlock{Type: "token", TokenID: "t1", Secret: "${S}"},
				SSH:  config.SSHBlock{Jump: rawJump},
			},
		},
	}
	path, cfg := makeConfig(t, cfg)
	wantJump := apiclient.RedactJumpChain(rawJump)
	require.NotContains(t, wantJump, "s3cret", "test fixture sanity: redaction must actually mask")

	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
		deps := makeDeps(t, path, cfg)
		deps.Format = format
		out, err := run(t, deps, "", "ls")
		require.NoError(t, err, "format %s", format)
		require.NotContains(t, out, "s3cret", "format %s must never carry the jump chain password", format)

		var entries []map[string]any
		switch format {
		case output.FormatJSON:
			require.NoError(t, json.Unmarshal([]byte(out), &entries))
		case output.FormatYAML:
			require.NoError(t, yaml.Unmarshal([]byte(out), &entries))
		}
		require.Len(t, entries, 1)
		require.Equal(t, wantJump, entries[0]["jump"], "format %s must carry the redacted jump chain", format)
	}
}

// ---------------------------------------------------------------------------
// validate --connect — route, overrides, and failure reporting
// ---------------------------------------------------------------------------

// validateEntry is one entry of `context validate -o json`.
type validateEntry struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Reachable string   `json:"reachable"`
	Via       string   `json:"via"`
	Product   string   `json:"product_check"`
	Auth      string   `json:"auth_check"`
	Errors    []string `json:"errors"`
}

// parseValidateJSON decodes `context validate -o json` output into entries
// keyed by context name.
func parseValidateJSON(t *testing.T, out string) map[string]validateEntry {
	t.Helper()

	var entries []validateEntry
	require.NoError(t, json.Unmarshal([]byte(out), &entries), out)

	byName := make(map[string]validateEntry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
	}

	return byName
}

// parseValidateYAML decodes `context validate -o yaml` output into entries
// keyed by context name, through the same field names the JSON carries.
func parseValidateYAML(t *testing.T, out string) map[string]validateEntry {
	t.Helper()

	var entries []validateEntry
	require.NoError(t, yaml.Unmarshal([]byte(out), &entries), out)

	byName := make(map[string]validateEntry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
	}

	return byName
}

// runWithConnectionFlags runs `context <args>` under a root that registers
// the --api-* flags and --insecure as persistent flags, the way the real
// root does, and points deps.Conn at cli.OverridesFromCommand, so a test
// drives the overrides exactly as an operator would. ctx is the command's
// context. It returns standard output and standard error separately.
func runWithConnectionFlags(
	t *testing.T, ctx context.Context, deps *cli.Deps, args ...string,
) (string, string, error) {
	t.Helper()

	root := &cobra.Command{Use: "pmx", SilenceUsage: true, SilenceErrors: true}
	cli.RegisterConnectionFlags(root.PersistentFlags())
	root.PersistentFlags().Bool("insecure", false, "skip TLS verification")

	group := Group(nil)
	root.AddCommand(group)

	deps.Conn = sync.OnceValues(func() (cli.ConnectionOverrides, error) {
		return cli.OverridesFromCommand(group)
	})

	root.SetContext(cli.WithDeps(ctx, deps))

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"context"}, args...))

	err := root.Execute()

	return stdout.String(), stderr.String(), err
}

// jsonDeps returns deps that render JSON.
func jsonDeps(t *testing.T, cfg *config.Config) *cli.Deps {
	t.Helper()

	path, loaded := makeConfig(t, cfg)
	deps := makeDeps(t, path, loaded)
	deps.Format = output.FormatJSON

	return deps
}

// sshOnPath puts an ssh stand-in first on PATH under the name "ssh", so a
// probe that resolves its jump from the configuration, with no program of
// its own, runs the stand-in. The link lives in a directory whose path
// starts with the stand-in's own, so the stand-in's cleanup, which matches
// processes by that path, still finds every invocation.
func sshOnPath(t *testing.T, opts testhelper.SSHStandInOptions) testhelper.SSHScript {
	t.Helper()

	script := testhelper.SSHStandIn(t, opts)

	dir := script.Program + "-bin"
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.Symlink(script.Program, filepath.Join(dir, "ssh")))

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	apiclient.ReopenJumps()

	return script
}

// versionServer starts a TLS server that answers every path, the probe's
// root page included, as PVE does, counting the requests it serves.
func versionServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(ts.Close)

	return ts, &hits
}

// TestValidateConnect_RendersVIAColumn proves the VIA column sits between
// REACHABLE and PRODUCT and that the JSON entries carry all six routes.
func TestValidateConnect_RendersVIAColumn(t *testing.T) {
	ts, _ := versionServer(t)
	socks := testhelper.SOCKS5StandIn(t)
	sshOnPath(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

	withTarget := func(edit func(c *config.Context)) *config.Context {
		c := probeTarget(t, ts, config.ProductPVE)
		c.Timeout = config.TimeoutBlock{Connect: "500ms", Request: "900ms"}
		edit(c)

		return c
	}

	cfg := &config.Config{
		CurrentContext: "a-direct",
		Contexts: map[string]*config.Context{
			"a-direct": withTarget(func(*config.Context) {}),
			"b-jump":   withTarget(func(c *config.Context) { c.SSH.Jump = "bastion" }),
			"c-proxy":  withTarget(func(c *config.Context) { c.Proxy.URL = "socks5h://" + socks.Addr }),
			"d-jump-proxy": withTarget(func(c *config.Context) {
				c.SSH.Jump = "bastion"
				c.Proxy.URL = "socks5h://" + socks.Addr
			}),
			"e-env": withTarget(func(c *config.Context) {
				c.Host = "pve-env.test"
				c.Proxy.FromEnv = new(true)
			}),
			// Go never proxies a loopback address, so the environment proxy
			// does not apply to this one.
			"f-env-not-applicable": withTarget(func(c *config.Context) { c.Proxy.FromEnv = new(true) }),
		},
	}

	want := map[string]string{
		"a-direct":             "direct",
		"b-jump":               "jump bastion",
		"c-proxy":              "proxy socks5h://" + socks.Addr,
		"d-jump-proxy":         "jump bastion + proxy socks5h://" + socks.Addr,
		"e-env":                "proxy " + redact.ProxyURL(testEnvHTTPSProxy) + " (from environment)",
		"f-env-not-applicable": "direct (environment proxy not applicable)",
	}

	deps := jsonDeps(t, cfg)
	out, _, _ := runWithConnectionFlags(t, context.Background(), deps, "validate", "--all", "--connect")

	entries := parseValidateJSON(t, out)
	require.Len(t, entries, len(want))

	for name, via := range want {
		require.Equal(t, via, entries[name].Via, "context %s", name)
	}

	require.Equal(t, "yes", entries["a-direct"].Reachable)
	require.Equal(t, "yes", entries["c-proxy"].Reachable, entries["c-proxy"].Errors)
	require.Equal(t, "yes", entries["f-env-not-applicable"].Reachable, entries["f-env-not-applicable"].Errors)
	require.Equal(t, "no", entries["e-env"].Reachable, "the pinned environment proxy does not exist")
	require.NotContains(t, out, "envs3cret", "the environment proxy's password must never print")

	deps.Format = output.FormatYAML
	out, _, _ = runWithConnectionFlags(t, context.Background(), deps, "validate", "--all", "--connect")
	require.Contains(t, out, "via: direct\n")

	yamlEntries := parseValidateYAML(t, out)
	require.Len(t, yamlEntries, len(want))

	for name, via := range want {
		require.Equal(t, via, yamlEntries[name].Via, "yaml context %s", name)
	}

	require.NotContains(t, out, "envs3cret", "the environment proxy's password must never print")

	deps.Format = output.FormatTable
	deps.Out = output.NewWidth(output.WidthUnbounded)
	out, _, _ = runWithConnectionFlags(t, context.Background(), deps, "validate", "a-direct", "--connect")

	var header []string

	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "NAME") {
			header = strings.Fields(strings.ReplaceAll(line, "│", " "))
			break
		}
	}

	require.Equal(t, []string{"NAME", "STATUS", "REACHABLE", "VIA", "PRODUCT", "AUTH", "ERRORS"}, header)
}

// TestValidateConnect_RedactsProxyCredentials proves a proxy URL carrying a
// password never prints it: from $PMX_API_PROXY, the VIA cell, the via
// field, the unreachable text, and the note all carry the placeholder, and a
// stored proxy.url with credentials is rejected with the placeholder too.
func TestValidateConnect_RedactsProxyCredentials(t *testing.T) {
	const raw = "socks5://u:p@host:1080"

	masked := redact.ProxyURL(raw)
	require.Contains(t, masked, redact.Placeholder, "test fixture sanity: the url must mask")

	t.Run("from the environment", func(t *testing.T) {
		t.Setenv("PMX_API_PROXY", raw)

		target := &config.Context{
			Host: "127.0.0.1", Port: closedPort(t), Protocol: "https",
			TLS:     config.TLSBlock{Insecure: true},
			Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t", Secret: "s"},
			Timeout: config.TimeoutBlock{Connect: "200ms", Request: "200ms"},
		}
		cfg := &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": target}}

		deps := jsonDeps(t, cfg)
		out, stderr, err := runWithConnectionFlags(t, context.Background(), deps, "validate", "--connect")
		require.Error(t, err)

		entry := parseValidateJSON(t, out)["lab"]
		require.Equal(t, "proxy "+masked, entry.Via)
		require.Len(t, entry.Errors, 1)
		require.True(t, strings.HasPrefix(entry.Errors[0], "unreachable via proxy "+masked+": "), entry.Errors[0])
		require.Contains(t, stderr, "note: $PMX_API_PROXY ("+masked+")")

		deps.Format = output.FormatTable
		deps.Out = output.NewWidth(output.WidthUnbounded)
		table, tableStderr, _ := runWithConnectionFlags(t, context.Background(), deps, "validate", "--connect")
		require.Contains(t, table, "proxy "+masked)

		deps.Format = output.FormatYAML
		yamlOut, yamlStderr, _ := runWithConnectionFlags(t, context.Background(), deps, "validate", "--connect")

		yamlEntry := parseValidateYAML(t, yamlOut)["lab"]
		require.Equal(t, "proxy "+masked, yamlEntry.Via)
		require.Len(t, yamlEntry.Errors, 1)
		require.True(t,
			strings.HasPrefix(yamlEntry.Errors[0], "unreachable via proxy "+masked+": "), yamlEntry.Errors[0])

		for _, text := range []string{out, stderr, table, tableStderr, yamlOut, yamlStderr} {
			require.NotContains(t, text, "u:p@", "the proxy password must never print")
		}
	})

	t.Run("stored in proxy.url", func(t *testing.T) {
		target := &config.Context{
			Host: "127.0.0.1", Port: 8006, Protocol: "https",
			Auth:  config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t", Secret: "s"},
			Proxy: config.ProxyBlock{URL: raw},
		}
		cfg := &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": target}}

		out, stderr, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "--connect")
		require.Error(t, err)

		entry := parseValidateJSON(t, out)["lab"]
		require.Equal(t, "INVALID", entry.Status)
		require.Empty(t, entry.Via, "a context that fails validation is never probed")
		require.Contains(t, strings.Join(entry.Errors, "; "), redact.Placeholder)
		require.NotContains(t, out+stderr, "u:p@")
	})
}

// TestValidateConnect_UnreachableMessages proves each unreachable route is
// named once: a bastion failure by the JumpError's Detail, whatever the
// transport wrapped around it, a refused proxy by its proxyconnect error,
// and a direct failure in the form it always had.
func TestValidateConnect_UnreachableMessages(t *testing.T) {
	ts, _ := versionServer(t)

	jumpTarget := func(request string) *config.Config {
		c := probeTarget(t, ts, config.ProductPVE)
		c.SSH.Jump = "admin@bastion.example.com"
		c.Timeout.Request = request

		return &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}}
	}

	probeError := func(t *testing.T, cfg *config.Config) string {
		t.Helper()

		out, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "--connect")
		require.Error(t, err)

		entry := parseValidateJSON(t, out)["lab"]
		require.Equal(t, "no", entry.Reachable)
		require.Len(t, entry.Errors, 1)

		return entry.Errors[0]
	}

	t.Run("a bastion that refused", func(t *testing.T) {
		sshOnPath(t, testhelper.SSHStandInOptions{
			Mode:       testhelper.SSHFail,
			Stderr:     []string{"ssh: connect to host bastion.example.com port 22: Connection refused"},
			ExitStatus: 255,
		})

		require.Equal(t,
			"unreachable via jump admin@bastion.example.com: "+
				"ssh: connect to host bastion.example.com port 22: Connection refused",
			probeError(t, jumpTarget("900ms")))
	})

	t.Run("a bastion that printed nothing", func(t *testing.T) {
		sshOnPath(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHFail, ExitStatus: 255})

		require.Equal(t,
			"unreachable via jump admin@bastion.example.com: ssh exited with status 255 and printed nothing",
			probeError(t, jumpTarget("900ms")))
	})

	t.Run("a bastion that timed out", func(t *testing.T) {
		sshOnPath(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHHang, IgnoreEOF: true})

		// The first-byte timer is the request bound less a quarter of it:
		// 900ms - 225ms.
		require.Equal(t,
			"unreachable via jump admin@bastion.example.com: no response within 675ms",
			probeError(t, jumpTarget("900ms")))
	})

	t.Run("a refused proxy", func(t *testing.T) {
		proxyAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(closedPort(t)))

		c := probeTarget(t, ts, config.ProductPVE)
		c.Proxy.URL = "socks5://" + proxyAddr
		c.Timeout.Request = "900ms"

		got := probeError(t, &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}})
		require.True(t, strings.HasPrefix(got, "unreachable via proxy socks5://"+proxyAddr+": proxyconnect tcp: "), got)
	})

	t.Run("a direct failure", func(t *testing.T) {
		c := probeTarget(t, ts, config.ProductPVE)
		c.Port = closedPort(t)
		c.Timeout.Request = "900ms"

		got := probeError(t, &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}})
		require.True(t, strings.HasPrefix(got,
			fmt.Sprintf(`unreachable: Get "https://127.0.0.1:%d/": `, c.Port)), got)
	})

	t.Run("a wrapped JumpError is found and formatted by its Detail", func(t *testing.T) {
		conn, err := cli.ResolveConnection("lab", &config.Context{
			Host: "pve.example.com", Port: 8006, Protocol: "https",
			SSH:   config.SSHBlock{Jump: "admin@bastion.example.com"},
			Proxy: config.ProxyBlock{URL: "socks5h://proxy.example.com:1080"},
		}, cli.ConnectionOverrides{})
		require.NoError(t, err)

		signalled := &apiclient.JumpError{
			Chain: "admin@bastion.example.com", Addr: "proxy.example.com:1080", ExitStatus: -1, Signaled: true,
		}
		wrapped := &url.Error{Op: "Get", URL: "https://pve.example.com:8006/api2/json/version",
			Err: &net.OpError{Op: "proxyconnect", Net: "tcp", Err: signalled}}

		require.Equal(t,
			"unreachable via jump admin@bastion.example.com: ssh was ended by a signal and printed nothing",
			unreachableText(conn, wrapped))

		refused := &url.Error{Op: "Get", URL: "https://pve.example.com:8006/api2/json/version",
			Err: &net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New("connection refused")}}
		require.Equal(t,
			"unreachable via jump admin@bastion.example.com + proxy socks5h://proxy.example.com:1080: "+
				"proxyconnect tcp: connection refused",
			unreachableText(conn, refused))
	})

	t.Run("a multi-hop chain is shown as written", func(t *testing.T) {
		for _, chain := range []string{
			"ssh://admin@bastion:2222,ssh://root@inner",
			"admin@[fd00::1]:22,root@inner",
			"ssh://a@[fd00::1]:22,ssh://b@host",
			"ssh://admin@bastion:2222,root@inner",
		} {
			t.Run(chain, func(t *testing.T) {
				conn, err := cli.ResolveConnection("lab", &config.Context{
					Host: "pve.example.com", Port: 8006, Protocol: "https",
					SSH: config.SSHBlock{Jump: chain},
				}, cli.ConnectionOverrides{})
				require.NoError(t, err, "the validator must accept the chain")
				require.Equal(t, "jump "+chain, conn.Via())

				failed := &apiclient.JumpError{Chain: chain, Addr: "pve.example.com:8006", Stderr: "boom"}
				require.Equal(t, "unreachable via jump "+chain+": boom", unreachableText(conn, failed))
			})
		}
	})

	t.Run("the cause is still masked", func(t *testing.T) {
		const chain = "ssh://admin@bastion:2222,ssh://root@inner"

		conn, err := cli.ResolveConnection("lab", &config.Context{
			Host: "pve.example.com", Port: 8006, Protocol: "https",
			SSH: config.SSHBlock{Jump: chain},
		}, cli.ConnectionOverrides{})
		require.NoError(t, err)

		failed := &apiclient.JumpError{
			Chain: chain, Addr: "pve.example.com:8006",
			Stderr: "proxyconnect socks5://u:s3cret@proxy.example.com:1080 refused",
		}
		require.Equal(t,
			"unreachable via jump "+chain+": proxyconnect socks5://u:<redacted>@proxy.example.com:1080 refused",
			unreachableText(conn, failed))

		direct := &url.Error{Op: "Get", URL: "https://pve.example.com:8006/api2/json/version",
			Err: errors.New("dial socks5://u:s3cret@proxy.example.com:1080: refused")}
		require.Equal(t,
			`unreachable: Get "https://pve.example.com:8006/api2/json/version": `+
				"dial socks5://u:<redacted>@proxy.example.com:1080: refused",
			unreachableText(cli.Connection{Host: "pve.example.com", Port: 8006}, direct))
	})
}

// TestValidateConnect_AllRejectsAPIOverrides proves an override that names
// one host is refused under --all, from the flag or the environment, while
// jump, proxy, and timeout overrides sweep every context.
func TestValidateConnect_AllRejectsAPIOverrides(t *testing.T) {
	tsA, _ := versionServer(t)
	tsB, _ := versionServer(t)

	cfg := func() *config.Config {
		return &config.Config{
			CurrentContext: "a",
			Contexts: map[string]*config.Context{
				"a": probeTarget(t, tsA, config.ProductPVE),
				"b": probeTarget(t, tsB, config.ProductPVE),
			},
		}
	}

	const tail = " cannot be combined with --all; unset it or validate one context by name"

	refused := []struct {
		name   string
		env    string
		value  string
		source string
	}{
		{name: "endpoint", env: "PMX_API_ENDPOINT", value: "h", source: "api-endpoint"},
		{name: "fingerprint", env: "PMX_API_FINGERPRINT", value: wrongFingerprint, source: "api-fingerprint"},
		{name: "CA bundle", env: "PMX_API_CA_CERT", value: "/etc/pmx/ca.pem", source: "api-ca-cert"},
	}

	for _, tc := range refused {
		t.Run(tc.name+" from the flag", func(t *testing.T) {
			_, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg()),
				"validate", "--all", "--connect", "--"+tc.source, tc.value)
			require.EqualError(t, err, "--"+tc.source+tail)
		})

		t.Run(tc.name+" from the environment", func(t *testing.T) {
			t.Setenv(tc.env, tc.value)

			_, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg()),
				"validate", "--all", "--connect")
			require.EqualError(t, err, "$"+tc.env+tail)
		})
	}

	for _, args := range [][]string{
		{"--api-jump", "none"},
		{"--api-connect-timeout", "2s"},
		{"--api-proxy", "none"},
	} {
		t.Run("sweeps with "+args[0], func(t *testing.T) {
			out, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg()),
				append([]string{"validate", "--all", "--connect"}, args...)...)
			require.NoError(t, err)

			entries := parseValidateJSON(t, out)
			require.Len(t, entries, 2)
			require.Equal(t, "yes", entries["a"].Reachable)
			require.Equal(t, "yes", entries["b"].Reachable)
		})
	}
}

// TestValidateConnect_ResolveFailureMarksInvalid proves a connection that
// cannot be resolved or set up is reported as invalid with its reason, and
// is never probed.
func TestValidateConnect_ResolveFailureMarksInvalid(t *testing.T) {
	t.Run("a rejected jump chain", func(t *testing.T) {
		ts, hits := versionServer(t)
		cfg := &config.Config{
			CurrentContext: "lab",
			Contexts:       map[string]*config.Context{"lab": probeTarget(t, ts, config.ProductPVE)},
		}

		out, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg),
			"validate", "--connect", "--api-jump", "bad;host")
		require.Error(t, err)

		entry := parseValidateJSON(t, out)["lab"]
		require.Equal(t, "INVALID", entry.Status)
		require.Len(t, entry.Errors, 1)
		require.True(t, strings.HasPrefix(entry.Errors[0], `connection: --api-jump "bad;host" is not valid: `),
			entry.Errors[0])
		require.Empty(t, entry.Reachable)
		require.Zero(t, hits.Load(), "a context whose connection does not resolve is never probed")
	})

	t.Run("an unresolvable proxy password", func(t *testing.T) {
		t.Setenv("PMX_TEST_VALIDATE_UNSET_PASSWORD", "")
		require.NoError(t, os.Unsetenv("PMX_TEST_VALIDATE_UNSET_PASSWORD"))

		socks := testhelper.SOCKS5StandIn(t)
		ts, hits := versionServer(t)

		c := probeTarget(t, ts, config.ProductPVE)
		c.Proxy = config.ProxyBlock{
			URL:      "socks5h://" + socks.Addr,
			Username: "pmx",
			Password: "${PMX_TEST_VALIDATE_UNSET_PASSWORD}",
		}
		cfg := &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}}

		out, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "--connect")
		require.Error(t, err)

		entry := parseValidateJSON(t, out)["lab"]
		require.Equal(t, "INVALID", entry.Status)
		require.Len(t, entry.Errors, 1)
		require.True(t, strings.HasPrefix(entry.Errors[0], `connection: resolve proxy.password for context "lab": `),
			entry.Errors[0])
		require.Empty(t, entry.Reachable)
		require.Zero(t, hits.Load())
		require.Empty(t, socks.Connections(), "nothing is dialled when the proxy cannot be set up")
	})
}

// TestValidateConnect_ToleratesBareDeps proves validate --connect never
// panics on a hand-built Deps with nothing filled in.
func TestValidateConnect_ToleratesBareDeps(t *testing.T) {
	for _, args := range [][]string{
		{"validate", "--connect"},
		{"validate", "--connect", "--all"},
		{"validate", "--connect", "lab"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			require.NotPanics(t, func() {
				_, _ = run(t, &cli.Deps{}, "", args...)
			})
		})
	}
}

// TestValidateConnect_SweepPrintsEachNoteOnce proves an exported PMX_API_*
// variable is announced once for a whole sweep, and that a single-context
// run prints that context's own note once.
func TestValidateConnect_SweepPrintsEachNoteOnce(t *testing.T) {
	t.Setenv("PMX_API_JUMP", "none")

	tsA, _ := versionServer(t)
	tsB, _ := versionServer(t)

	withJump := func(ts *httptest.Server) *config.Context {
		c := probeTarget(t, ts, config.ProductPVE)
		c.SSH.Jump = "bastion"

		return c
	}

	cfg := &config.Config{
		CurrentContext: "a",
		Contexts:       map[string]*config.Context{"a": withJump(tsA), "b": withJump(tsB)},
	}

	out, stderr, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg),
		"validate", "--all", "--connect")
	require.NoError(t, err, stderr)
	require.Len(t, parseValidateJSON(t, out), 2)

	require.Equal(t, 1, strings.Count(stderr, "note: $PMX_API_JUMP (none) applies to every context in this sweep"),
		stderr)
	require.Equal(t, 1, strings.Count(stderr, "note:"), "a sweep prints no per-context note: %s", stderr)

	_, stderr, err = runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "a", "--connect")
	require.NoError(t, err, stderr)
	require.Equal(t, 1, strings.Count(stderr, `note: $PMX_API_JUMP=none disables the bastion of context "a"`), stderr)
	require.Equal(t, 1, strings.Count(stderr, "note:"), stderr)
	require.NotContains(t, stderr, "applies to every context")
}

// TestValidateConnect_WarnsFromResolvedInsecure proves the insecure warning
// follows the resolved trust: a fingerprint override replaces an insecure
// context's settings and prints none, while an insecure context without an
// override prints exactly one, even across a sweep.
func TestValidateConnect_WarnsFromResolvedInsecure(t *testing.T) {
	const warning = "WARN: TLS certificate verification disabled"

	ts, _ := versionServer(t)

	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab":   probeTarget(t, ts, config.ProductPVE),
			"other": probeTarget(t, ts, config.ProductPVE),
		},
	}

	out, stderr, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg),
		"validate", "lab", "--connect", "--api-fingerprint", serverCertFingerprint(t, ts))
	require.NoError(t, err, stderr)
	require.Equal(t, "yes", parseValidateJSON(t, out)["lab"].Reachable)
	require.NotContains(t, stderr, warning)

	_, stderr, err = runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "lab", "--connect")
	require.NoError(t, err, stderr)
	require.Equal(t, 1, strings.Count(stderr, warning), stderr)

	_, stderr, err = runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "--all", "--connect")
	require.NoError(t, err, stderr)
	require.Equal(t, 1, strings.Count(stderr, warning), "a sweep warns once: %s", stderr)
}

// TestValidateConnect_StopsSweepOnCancel proves a cancelled command stops
// the sweep: only the context already probed is rendered, and the context
// error is returned rather than a table blaming contexts never reached.
func TestValidateConnect_StopsSweepOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(first.Close)

	second, secondHits := versionServer(t)

	cfg := &config.Config{
		CurrentContext: "a",
		Contexts: map[string]*config.Context{
			"a": probeTarget(t, first, config.ProductPVE),
			"b": probeTarget(t, second, config.ProductPVE),
		},
	}

	out, _, err := runWithConnectionFlags(t, ctx, jsonDeps(t, cfg), "validate", "--all", "--connect")
	require.ErrorIs(t, err, context.Canceled)

	entries := parseValidateJSON(t, out)
	require.Len(t, entries, 1, "only the context probed before the cancel is rendered")
	require.Contains(t, entries, "a")
	require.Zero(t, secondHits.Load(), "the sweep must not probe past a cancel")
}

// TestValidateConnect_CancelInFlightIsNotUnreachable proves that a probe the
// operator cancels while it waits on the server is reported as interrupted,
// never as an unreachable context, so a script reading the JSON does not
// blame the context for a Ctrl-C.
func TestValidateConnect_CancelInFlightIsNotUnreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hanging := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(hanging.Close)

	second, secondHits := versionServer(t)

	cfg := &config.Config{
		CurrentContext: "a",
		Contexts: map[string]*config.Context{
			"a": probeTarget(t, hanging, config.ProductPVE),
			"b": probeTarget(t, second, config.ProductPVE),
		},
	}

	out, _, err := runWithConnectionFlags(t, ctx, jsonDeps(t, cfg), "validate", "--all", "--connect")
	require.ErrorIs(t, err, context.Canceled)

	entries := parseValidateJSON(t, out)
	require.Contains(t, entries, "a")
	require.Equal(t, "interrupted", entries["a"].Reachable)
	require.Empty(t, entries["a"].Errors, "an interrupted probe blames nothing")
	require.Equal(t, "OK", entries["a"].Status)

	for name, entry := range entries {
		require.NotEqual(t, "no", entry.Reachable, "context %s must not read as unreachable", name)
	}

	require.Zero(t, secondHits.Load(), "the sweep must not probe past a cancel")
}

// TestValidateConnect_PinMismatchUnderEndpointOverride proves a pinned
// context is probed under an endpoint override rather than refused, and
// that a server presenting another certificate is explained in the terms of
// the override.
func TestValidateConnect_PinMismatchUnderEndpointOverride(t *testing.T) {
	ts, _ := versionServer(t)

	cfg := &config.Config{
		CurrentContext: "pinned",
		Contexts: map[string]*config.Context{
			"pinned": {
				Host: "pve-pinned.invalid", Port: 8006, Protocol: "https",
				TLS:  config.TLSBlock{Fingerprint: wrongFingerprint},
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t", Secret: "s"},
			},
		},
	}

	endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(serverPort(t, ts)))

	out, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg),
		"validate", "pinned", "--connect", "--api-endpoint", endpoint)
	require.Error(t, err)

	entry := parseValidateJSON(t, out)["pinned"]
	require.Equal(t, "OK", entry.Status, "the override is probed, not refused")
	require.Equal(t, "no", entry.Reachable)
	require.Len(t, entry.Errors, 1)
	require.Equal(t,
		fmt.Sprintf(`unreachable: context "pinned" pins a certificate that %s (from --api-endpoint) does not present; `+
			"pass --api-fingerprint for that host", endpoint),
		entry.Errors[0])
}
