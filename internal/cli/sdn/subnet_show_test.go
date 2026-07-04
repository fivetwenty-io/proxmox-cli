package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestSubnetShow verifies `pve sdn subnet show` issues a GET to the
// single-subnet endpoint and renders the returned fields.
func TestSubnetShow(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pvecli0/subnets/10.241.0.0-24", map[string]any{
		"subnet": "10.241.0.0/24", "vnet": "pvecli0", "gateway": "10.241.0.1", "zone": "pvecli",
	}, 200)

	out, err := run(t, f, "", "subnet", "show", "pvecli0", "10.241.0.0-24")
	require.NoError(t, err)
	require.Contains(t, out, "10.241.0.0/24")
	require.Contains(t, out, "10.241.0.1")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/subnets/10.241.0.0-24", rec[0].path)
}

// TestSubnetShowPendingRunning verifies the --pending and --running flags are
// forwarded as query parameters.
func TestSubnetShowPendingRunning(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pvecli0/subnets/10.241.0.0-24", map[string]any{
		"subnet": "10.241.0.0/24", "vnet": "pvecli0",
	}, 200)

	_, err := run(t, f, "", "subnet", "show", "pvecli0", "10.241.0.0-24", "--pending", "--running")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
	require.Equal(t, "1", rec[0].query.Get("running"))
}

// TestSubnetShowNotFound covers the error path when the subnet does not exist.
func TestSubnetShowNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pvecli0/subnets/missing", nil, 404)

	_, err := run(t, f, "", "subnet", "show", "pvecli0", "missing")
	require.Error(t, err)
	require.ErrorContains(t, err, "get subnet")
	require.Len(t, rec, 1)
}
