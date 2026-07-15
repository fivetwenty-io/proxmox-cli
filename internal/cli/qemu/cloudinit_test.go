package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestQemuCloudinitPending_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/cloudinit", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"key": "ciuser", "value": "root", "pending": "admin"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cloudinit", "pending", "100"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/cloudinit", gotPath)
	out := buf.String()
	require.Contains(t, out, "ciuser")
	require.Contains(t, out, "admin")
}

func TestQemuCloudinitDump(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotType string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/cloudinit/dump", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotType = r.URL.Query().Get("type")
		testhelper.WriteData(w, "#cloud-config\nhostname: web\n")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cloudinit", "dump", "100", "--type", "user"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/cloudinit/dump", gotPath)
	require.Equal(t, "user", gotType)
	require.Contains(t, buf.String(), "#cloud-config")
}

func TestQemuCloudinitDump_DefaultType(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotType string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/cloudinit/dump", func(w http.ResponseWriter, r *http.Request) {
		gotType = r.URL.Query().Get("type")
		testhelper.WriteData(w, "data")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cloudinit", "dump", "100"))
	require.Equal(t, "user", gotType)
}

// TestQemuCloudinitDump_NonStringFallback covers the branch where the dump body
// is not a bare JSON string: the raw bytes are rendered verbatim rather than
// failing to decode.
func TestQemuCloudinitDump_NonStringFallback(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/cloudinit/dump", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"unexpected": "shape"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cloudinit", "dump", "100"))
	require.Contains(t, buf.String(), "unexpected")
}

func TestQemuCloudinitUpdate(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/cloudinit", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cloudinit", "update", "100"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/cloudinit", gotPath)
	require.Contains(t, buf.String(), "Regenerated cloud-init drive")
}

func TestQemuCloudinit_UnknownGuestErrors(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "cloudinit", "pending", "100")
	require.Error(t, err)
	require.ErrorContains(t, err, "not found")
}

func TestQemuCloudinitCommandTree(t *testing.T) {
	cmd := Group(nil)
	var ci *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "cloudinit" {
			ci = c
			break
		}
	}
	require.NotNil(t, ci, "cloudinit command should be registered")

	sub := map[string]bool{}
	for _, c := range ci.Commands() {
		sub[c.Name()] = true
	}
	for _, want := range []string{"pending", "dump", "update"} {
		require.True(t, sub[want], "cloudinit should register %q", want)
	}
}
