package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestVnetShow verifies `pmx sdn vnet show` issues a GET to the single-vnet
// endpoint and renders the returned fields.
func TestVnetShow(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{
		"vnet": "pmxcli0", "zone": "pmxcli", "tag": 150, "alias": "lab-net",
	}, 200)

	out, err := run(t, f, "", "vnet", "show", "pmxcli0")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli0")
	require.Contains(t, out, "lab-net")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0", rec[0].path)
}

// TestVnetShowPendingRunning verifies the --pending and --running flags are
// forwarded as query parameters.
func TestVnetShowPendingRunning(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{
		"vnet": "pmxcli0", "zone": "pmxcli",
	}, 200)

	_, err := run(t, f, "", "vnet", "show", "pmxcli0", "--pending", "--running")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
	require.Equal(t, "1", rec[0].query.Get("running"))
}

// TestVnetShowNotFound covers the error path when the vnet does not exist.
func TestVnetShowNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/missing", nil, 404)

	_, err := run(t, f, "", "vnet", "show", "missing")
	require.Error(t, err)
	require.ErrorContains(t, err, "get SDN vnet")
	require.Len(t, rec, 1)
}
