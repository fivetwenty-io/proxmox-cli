package cluster

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestBackupInfoNotBackedUp_Table verifies `pve cluster backup-info not-backed-up`
// queries GET /cluster/backup-info/not-backed-up and renders a dynamic table.
func TestBackupInfoNotBackedUp_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/backup-info/not-backed-up", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"guest": 200, "name": "nobackup-vm", "type": "qemu"},
			map[string]any{"guest": 201, "name": "nobackup-ct", "type": "lxc"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "backup-info", "not-backed-up"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/backup-info/not-backed-up", gotPath)

	out := buf.String()
	require.Contains(t, out, "GUEST")
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "nobackup-vm")
	require.Contains(t, out, "nobackup-ct")
}

// TestBackupInfoNotBackedUp_ServerError verifies a server error surfaces correctly.
func TestBackupInfoNotBackedUp_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/backup-info/not-backed-up", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "backup-info", "not-backed-up"))
}

// TestBackupInfoCommandTree verifies backup-info exposes the not-backed-up sub-command.
func TestBackupInfoCommandTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var biCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "backup-info" {
			biCmd = c
		}
	}
	require.NotNil(t, biCmd, "cluster must expose a backup-info sub-command")

	names := make(map[string]bool)
	for _, c := range biCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["not-backed-up"], "backup-info must expose not-backed-up sub-command")
}

// TestBackupIncludedVolumes_Table verifies `pve cluster backup included-volumes <id>`
// queries GET /cluster/backup/{id}/included_volumes and renders the children.
func TestBackupIncludedVolumes_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/backup/backup-pvecli/included_volumes", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"children": []any{
				map[string]any{"id": "qemu/100", "name": "web", "type": "qemu"},
				map[string]any{"id": "qemu/101", "name": "db", "type": "qemu"},
			},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "backup", "included-volumes", "backup-pvecli"))

	require.Equal(t, "/api2/json/cluster/backup/backup-pvecli/included_volumes", gotPath)
	out := buf.String()
	require.Contains(t, out, "web")
	require.Contains(t, out, "db")
}

// TestBackupIncludedVolumes_ServerError verifies a server error surfaces correctly.
func TestBackupIncludedVolumes_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/backup/backup-pvecli/included_volumes", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "backup", "included-volumes", "backup-pvecli"))
}

// TestBackupCommandTree_IncludedVolumes verifies included-volumes is registered.
func TestBackupCommandTree_IncludedVolumes(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var backupCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "backup" {
			backupCmd = c
		}
	}
	require.NotNil(t, backupCmd)

	names := make(map[string]bool)
	for _, c := range backupCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["included-volumes"], "backup must expose included-volumes sub-command")
}
