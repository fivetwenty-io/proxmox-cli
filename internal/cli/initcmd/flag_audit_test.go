package initcmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// This file audits the sole command-specific flag in the initcmd package,
// following the flag_audit_test.go convention established in
// internal/cli/access. initcmd has exactly one sub-command (`pmx init
// config`) and one flag on it (--force, bool) — there is no PVE request wire
// to inspect (init only writes a local template file), so the audit instead
// asserts --force is the sole gate on overwrite behavior against the local
// filesystem: unset it refuses an existing file untouched, set it overwrites.
// See context/flag_audit_test.go for the same adaptation applied to a
// config-file-backed command group.
//
// The run helper used below is defined in init_test.go (same package).

// TestInitCmdAudit_Config_ForceFlag asserts --force is honored: without it, an
// existing config file is left untouched and the command errors; with it,
// the file is overwritten with the template.
func TestInitCmdAudit_Config_ForceFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte("current-context: keep\n"), 0o600))

	_, err := run(t, path, "config")
	require.Error(t, err, "config without --force must refuse to overwrite an existing file")
	require.Contains(t, err.Error(), "--force")

	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "current-context: keep\n", string(raw),
		"config without --force must not modify the existing file")

	_, err = run(t, path, "config", "--force")
	require.NoError(t, err, "config --force must overwrite the existing file")

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, "lab", cfg.CurrentContext,
		"config --force must persist the template's current-context")
}

// TestInitCmdAudit_Config_ForceFlag_DefaultFalseAllowsFreshWrite asserts
// --force's registered default (false) does not itself block writing to a
// path with no existing file — the flag only gates the overwrite-existing-
// file case, not a fresh write.
func TestInitCmdAudit_Config_ForceFlag_DefaultFalseAllowsFreshWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pmx", "config.yml") // does not exist yet

	_, err := run(t, path, "config")
	require.NoError(t, err, "config without --force must still write a fresh (non-existing) file")

	_, statErr := os.Stat(path)
	require.NoError(t, statErr)
}
