package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestFabricListAll(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	// The API returns an object with "fabrics" and "nodes" arrays.
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/all", map[string]any{
		"fabrics": []any{
			map[string]any{"id": "fab1", "protocol": "openfabric"},
		},
		"nodes": []any{
			map[string]any{"fabric_id": "fab1", "node_id": "n1", "ip": "10.0.0.1"},
		},
	}, 200)

	out, err := run(t, f, "", "fabric", "list-all")
	require.NoError(t, err)
	require.Contains(t, out, "fab1")
	require.Contains(t, out, "n1")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/fabrics/all", rec[0].path)
}

func TestFabricListAllEmptyLists(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/all", map[string]any{
		"fabrics": []any{},
		"nodes":   []any{},
	}, 200)

	out, err := run(t, f, "", "fabric", "list-all")
	require.NoError(t, err)
	// Empty result is valid; just confirm no error and a request was made.
	_ = out
	require.Len(t, rec, 1)
}

func TestFabricListAllError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/all", nil, 500)

	_, err := run(t, f, "", "fabric", "list-all")
	require.Error(t, err)
	require.ErrorContains(t, err, "list all SDN fabrics")
}

// TestFabricCommandTreeIncludesListAll verifies list-all is wired into the
// fabric group.
func TestFabricCommandTreeIncludesListAll(t *testing.T) {
	cmd := newFabricCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	require.True(t, got["list-all"], "fabric is missing \"list-all\"")
}
