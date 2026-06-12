package qemu

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestQemuFeature_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/feature", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"hasFeature": true,
			"nodes":      []string{"pve1", "pve2"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "feature", "100", "--feature", "clone"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/feature", gotPath)
	require.Contains(t, gotQuery, "feature=clone")
	out := buf.String()
	require.Contains(t, out, "true")
	require.Contains(t, out, "pve1")
}

func TestQemuFeature_WithSnapname(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/feature", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"hasFeature": false,
			"nodes":      []string{},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "feature", "100", "--feature", "snapshot", "--snapname", "pre-upgrade"))
	require.Contains(t, gotQuery, "snapname=pre-upgrade")
}

func TestQemuFeature_OmitSnapnameWhenUnset(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/feature", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"hasFeature": true, "nodes": []string{}})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "feature", "100", "--feature", "clone"))
	require.NotContains(t, gotQuery, "snapname")
}

func TestQemuFeature_RequiresFeature(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "feature", "100")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "feature")
}

func TestQemuFeature_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/feature", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "feature", "100", "--feature", "clone")
	require.Error(t, err)
	require.Contains(t, err.Error(), "feature check for VM 100")
}

func TestQemuFeature_RequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "feature", "100", "--feature", "clone")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node")
}

func TestQemuFeature_CommandTree(t *testing.T) {
	root := Group(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["feature"], "expected top-level sub-command 'feature'")
}
