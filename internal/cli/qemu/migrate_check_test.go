package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestQemuMigrateCheck_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"running":              false,
			"has-dbus-vmstate":     false,
			"allowed_nodes":        []string{"pve2", "pve3"},
			"local_disks":          []any{},
			"local_resources":      []string{},
			"mapped-resources":     []string{},
			"mapped-resource-info": map[string]any{},
		})
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "migrate", "check", "100"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/migrate", gotPath)
	out := buf.String()
	require.Contains(t, out, "running")
	require.Contains(t, out, "allowed_nodes")
}

func TestQemuMigrateCheck_WithTargetNode(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"running":              false,
			"has-dbus-vmstate":     false,
			"allowed_nodes":        []string{"pve2"},
			"local_disks":          []any{},
			"local_resources":      []string{},
			"mapped-resources":     []string{},
			"mapped-resource-info": map[string]any{},
		})
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "migrate", "check", "100", "--target-node", "pve2"))
	require.Contains(t, gotQuery, "target=pve2")
}

func TestQemuMigrateCheck_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "migrate", "check", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "migrate check for VM 100")
}

func TestQemuMigrateCheck_RequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(&buf, "migrate", "check", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node")
}

func TestQemuMigrateCheck_CommandTree(t *testing.T) {
	root := newGroupCmd(nil)
	var migrate *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "migrate" {
			migrate = c
			break
		}
	}
	require.NotNil(t, migrate)

	subNames := make(map[string]bool)
	for _, c := range migrate.Commands() {
		subNames[c.Name()] = true
	}
	require.True(t, subNames["check"], "expected migrate sub-command 'check'")
}
