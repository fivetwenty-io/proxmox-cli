package lab

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// quotaTestCmd builds a bare cobra.Command carrying a *cli.Deps loaded from
// configPath (via newCmdWithDeps, the shared package helper), plus the same
// --refquota-gb/--dry-run/--yes flags newQuotaSetCmd registers so
// cmd.Flags().Changed("refquota-gb") reflects real flag-parsing semantics
// rather than a hardcoded bool. flagArgs is parsed against those flags
// before returning, so e.g. quotaTestCmd(t, path, "--refquota-gb=999")
// leaves Changed("refquota-gb") true exactly as real invocation would.
func quotaTestCmd(t *testing.T, configPath string, flagArgs ...string) *cobra.Command {
	t.Helper()

	cmd := newCmdWithDeps(t, configPath)
	cmd.Flags().Int("refquota-gb", 0, "")
	cmd.Flags().Bool("dry-run", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, cmd.Flags().Parse(flagArgs))

	return cmd
}

// quotaFlagValues reads back the three flags quotaTestCmd registers, in the
// shape runQuotaSet's positional parameters expect.
func quotaFlagValues(t *testing.T, cmd *cobra.Command) (refquotaGB int, dryRun, yes bool) {
	t.Helper()

	var err error
	refquotaGB, err = cmd.Flags().GetInt("refquota-gb")
	require.NoError(t, err)
	dryRun, err = cmd.Flags().GetBool("dry-run")
	require.NoError(t, err)
	yes, err = cmd.Flags().GetBool("yes")
	require.NoError(t, err)
	return refquotaGB, dryRun, yes
}

// wireQuotaDeps attaches a fake ssh context (host + connection settings), a
// *exec.FakeRunner, and an output.Renderer to cmd's already-loaded *cli.Deps
// (mutating the same pointer newCmdWithDeps stashed in cmd's context), and
// wires cmd's in/out/err streams to the given buffers. Returns the
// FakeRunner so tests can inspect its recorded Calls.
func wireQuotaDeps(cmd *cobra.Command, stdin string, stdout, stderr *bytes.Buffer) *exec.FakeRunner {
	deps := cli.GetDeps(cmd)
	deps.Ctx = &config.Context{
		Host: "sm-0.lab.internal",
		SSH: config.SSHBlock{
			User:     "root",
			Port:     2222,
			Identity: "/home/wayne/.ssh/lab_ed25519",
		},
	}
	fake := exec.Fake()
	deps.Runner = fake
	deps.Out = output.New()
	deps.Format = output.FormatPlain

	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	return fake
}

func TestQuotaSet_HappyPathWithYes_UsesConfigRefquota(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"), // Storage.RefquotaGB: 50
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	require.Len(t, fake.Calls, 1)
	call := fake.Calls[0]
	assert.Equal(t, "ssh", call.Name)
	assert.False(t, call.Interactive)
	assert.Equal(t, []string{
		"-p", "2222",
		"-i", "/home/wayne/.ssh/lab_ed25519",
		"root@sm-0.lab.internal",
		"zfs", "set", "refquota=50G", "tank/labs/alpha",
	}, call.Args)
}

// TestQuotaSet_UsesConfiguredStoragePoolForDataset covers quota set's
// dataset targeting: it must derive the dataset from the lab's own
// storage.pool, the same base pool create.go allocates disks on, not a
// hardcoded "tank/labs/<name>".
func TestQuotaSet_UsesConfiguredStoragePoolForDataset(t *testing.T) {
	lab := cleanLab("alpha") // Storage.RefquotaGB: 50
	lab.Storage.Pool = "othertank"
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": lab,
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	require.Len(t, fake.Calls, 1)
	assert.Contains(t, fake.Calls[0].Args, "othertank/labs/alpha")
}

func TestQuotaSet_RefquotaFlagOverridesConfig(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"), // Storage.RefquotaGB: 50
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes", "--refquota-gb=999")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	require.Len(t, fake.Calls, 1)
	assert.Contains(t, fake.Calls[0].Args, "refquota=999G")
	assert.NotContains(t, fake.Calls[0].Args, "refquota=50G")
}

func TestQuotaSet_DryRun_RecordsNoCallAndPrintsCommand(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--dry-run")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	assert.Empty(t, fake.Calls, "dry-run must never invoke the runner")
	out := stdout.String()
	assert.Contains(t, out, "ssh")
	assert.Contains(t, out, "-p 2222")
	assert.Contains(t, out, "root@sm-0.lab.internal")
	assert.Contains(t, out, "zfs set refquota=50G tank/labs/alpha")
}

func TestQuotaSet_RefusesWithoutYesNonInteractively(t *testing.T) {
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": cleanLab("alpha"),
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path)
	var stdout, stderr bytes.Buffer
	// Empty stdin simulates a non-interactive invocation: the confirmation
	// read hits EOF immediately and must be treated as "no", not a hang or
	// a panic.
	fake := wireQuotaDeps(cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.NoError(t, err)

	assert.Empty(t, fake.Calls, "must refuse to run without confirmation")
	assert.Contains(t, stdout.String(), "Aborted")
}

func TestQuotaSet_MissingRefquotaEverywhere_HelpfulError(t *testing.T) {
	lab := cleanLab("alpha")
	lab.Storage.RefquotaGB = 0
	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": lab,
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no positive refquota")
	assert.ErrorContains(t, err, `"alpha"`)
	assert.Empty(t, fake.Calls)
}

func TestQuotaSet_PeppiGuardRefusesBeforeAnyRunnerCall(t *testing.T) {
	dirty := cleanLab("dirty")
	dirty.Network.VnetID = "peppivn0"
	dirty.Storage.RefquotaGB = 50 // valid, isolates the failure to the guard

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"dirty": dirty,
		},
	}
	path := writeConfig(t, cfg)
	cmd := quotaTestCmd(t, path, "--yes")
	var stdout, stderr bytes.Buffer
	fake := wireQuotaDeps(cmd, "", &stdout, &stderr)

	refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
	err := runQuotaSet(cmd, "dirty", refquotaGB, dryRun, yes)
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls, "peppi refusal must happen before any ssh call")
}
