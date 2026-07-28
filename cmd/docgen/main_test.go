package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
)

// generateInto runs the generator for all personas into dir and returns
// basename -> contents.
func generateInto(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	for _, k := range []string{"PMX_CONTEXT", "PMX_NODE", "PMX_OUTPUT", "PMX_TOKEN", "PMX_TOKEN_SECRET"} {
		t.Setenv(k, "")
	}
	require.NoError(t, run(runOpts{
		out:      dir,
		personas: []string{"pmx", "pve", "pbs", "pdm"},
		version:  "test",
		date:     fallbackDate,
	}))
	got := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if !d.IsDir() {
			b, rerr := os.ReadFile(p)
			require.NoError(t, rerr)
			got[filepath.Base(p)] = b
		}
		return nil
	}))
	return got
}

func TestGenerate_Deterministic(t *testing.T) {
	ga := generateInto(t, t.TempDir())
	gb := generateInto(t, t.TempDir())
	require.Equal(t, len(ga), len(gb))
	for name, ba := range ga {
		require.Equal(t, string(ba), string(gb[name]), "page %s differs between runs", name)
	}
}

// TestGenerate_PageCountFloor guards against a wiring regression that would
// silently drop most of the tree (e.g. a persona factory list collapsing to
// Shared() only). The floor is set ~15% below the observed four-persona
// count (3145 pages as of this writing) so it still trips on that class of
// bug without being brittle to small additions of new leaf commands.
func TestGenerate_PageCountFloor(t *testing.T) {
	got := generateInto(t, t.TempDir())
	require.GreaterOrEqual(t, len(got), 2670, "expected full four-persona output (~3145 pages observed)")
}

func TestPersonaTrees_DifferCorrectly(t *testing.T) {
	got := generateInto(t, t.TempDir())
	require.Contains(t, got, "pmx.1")
	require.Contains(t, got, "pmx-pve-qemu.1", "pmx persona must nest qemu under pve")
	require.Contains(t, got, "pve-qemu.1", "pve persona must hoist qemu to root")
	require.Contains(t, got, "pbs.1")
	require.Contains(t, got, "pdm.1")
	require.NotContains(t, got, "pve-pbs.1", "pve persona must not contain the pbs group")
}

func TestHiddenAndInternalCommandsAbsent(t *testing.T) {
	got := generateInto(t, t.TempDir())
	require.NotContains(t, got, "pmx-ctx.1", "hidden ctx alias must not be documented")
	require.NotContains(t, got, "pmx-help.1", "auto help command must not be documented")
	for name := range got {
		require.NotContains(t, name, "__complete", "cobra completion internals leaked: %s", name)
	}
}

func TestNoPageLeaksHostPathOrEnv(t *testing.T) {
	got := generateInto(t, t.TempDir())
	for name, b := range got {
		s := string(b)
		require.NotContains(t, s, "/Users/", "%s leaks a builder home path", name)
		require.NotContains(t, s, "/home/", "%s leaks a builder home path", name)
	}
}

func TestRun_UnknownPersonaErrors(t *testing.T) {
	err := run(runOpts{out: t.TempDir(), personas: []string{"nope"}, version: "test", date: fallbackDate})
	require.Error(t, err)
}

func TestRootPages_RequiredSections(t *testing.T) {
	got := generateInto(t, t.TempDir())
	for _, page := range []string{"pmx.1", "pve.1", "pbs.1", "pdm.1"} {
		s := string(got[page])
		for _, section := range []string{"NAME", "SYNOPSIS", "DESCRIPTION", "OPTIONS",
			"ENVIRONMENT", "FILES", "EXIT STATUS", "SEE ALSO"} {
			require.Regexp(t, `(?m)^\.SH "?`+section+`"?`, s, "%s missing section %s", page, section)
		}
	}
}

func TestConfigPage_Generated(t *testing.T) {
	got := generateInto(t, t.TempDir())
	page := string(got["pmx-config.5"])
	require.Regexp(t, `(?m)^\.TH `, page)
	for _, key := range []string{"current-context", "contexts", "host", "product", "auth", "tls"} {
		require.Contains(t, page, key, "pmx-config.5 must document %q", key)
	}
}

func TestEscapeAnglesText(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":          {"agent <vmid> <command>", `agent \<vmid\> \<command\>`},
		"no angles":      {"start a virtual machine", "start a virtual machine"},
		"code span kept": {"pass `<vmid>` or <name>", `pass ` + "`<vmid>`" + ` or \<name\>`},
		"indented block": {"run it:\n\n    source <(pmx completion bash)\n", "run it:\n\n    source <(pmx completion bash)\n"},
		"tab block":      {"run:\n\n\tcat <file>\n", "run:\n\n\tcat <file>\n"},
		"fenced block":   {"```\ncat <file>\n```\nthen <vmid>", "```\ncat <file>\n```\nthen \\<vmid\\>"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, escapeAnglesText(tc.in))
		})
	}
}

// TestPlaceholdersSurviveManRendering guards the md2man escaping: without it
// blackfriday reads "<vmid>" as inline HTML and drops it, collapsing the
// synopsis to "pmx pve qemu agent   [flags]".
func TestPlaceholdersSurviveManRendering(t *testing.T) {
	got := generateInto(t, t.TempDir())

	page := string(got["pmx-pve-qemu-agent.1"])
	require.Contains(t, page, "pmx pve qemu agent <vmid|name> <command> [flags]")

	// A backslash that reached the roff output means the escape leaked out of
	// a context blackfriday does not unescape, such as a fenced code block.
	for name, b := range got {
		require.NotContains(t, string(b), `\\<`, "%s renders a literal backslash before <", name)
		require.NotContains(t, string(b), `\\>`, "%s renders a literal backslash before >", name)
	}

	// The completion pages carry indented shell snippets that must stay verbatim.
	require.Contains(t, string(got["pmx-completion-bash.1"]), "source <(pmx completion bash)")

	// The hand-authored man5 page goes through the same escaping.
	require.Contains(t, string(got["pmx-config.5"]), "<pool>-lab-<name>")
}

func TestExitStatus_MatchesExitcodeConsts(t *testing.T) {
	page := string(generateInto(t, t.TempDir())["pmx.1"])
	for _, code := range []int{
		exitcode.OK, exitcode.Generic, exitcode.BadArgs, exitcode.Infra,
		exitcode.Auth, exitcode.NotFound, exitcode.Conflict, exitcode.TFARequired,
	} {
		require.Contains(t, page, "\\fB"+strconv.Itoa(code)+"\\fP", "EXIT STATUS omits code %d", code)
	}
}
