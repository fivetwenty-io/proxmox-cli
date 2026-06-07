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

// TestCephMetadata_Success verifies `pve cluster ceph metadata` queries
// GET /cluster/ceph/metadata without a scope param by default.
func TestCephMetadata_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/cluster/ceph/metadata", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"mon": map[string]any{"mon.pve1@pve1": map[string]any{"addr": "192.168.1.1"}},
			"mgr": map[string]any{},
			"mds": map[string]any{},
			"osd": []any{},
			"node": map[string]any{
				"pve1": map[string]any{"ceph_version": "ceph version 17.2.6"},
			},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "ceph", "metadata"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/ceph/metadata", gotPath)
	require.NotContains(t, gotQuery, "scope", "omitted --scope must not appear in query")
}

// TestCephMetadata_ScopeForwarded verifies --scope is sent when supplied.
func TestCephMetadata_ScopeForwarded(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/ceph/metadata", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"mon":  map[string]any{},
			"mgr":  map[string]any{},
			"mds":  map[string]any{},
			"osd":  []any{},
			"node": map[string]any{},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "ceph", "metadata", "--scope", "versions"))
	require.Contains(t, gotQuery, "scope=versions")
}

// TestCephMetadata_ServerError verifies a server error (e.g., no Ceph cluster) surfaces correctly.
func TestCephMetadata_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ceph/metadata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusServiceUnavailable, "no ceph cluster")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "ceph", "metadata"))
}

// TestCephCommandTree_Metadata verifies metadata is registered under ceph.
func TestCephCommandTree_Metadata(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var cephCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "ceph" {
			cephCmd = c
		}
	}
	require.NotNil(t, cephCmd)

	names := make(map[string]bool)
	for _, c := range cephCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["metadata"], "ceph must expose metadata sub-command")
	require.True(t, names["flags"], "ceph must still expose flags sub-command")
}
