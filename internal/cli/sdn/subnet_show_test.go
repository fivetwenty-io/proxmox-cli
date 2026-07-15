package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestSubnetShow verifies `pmx sdn subnet show` issues a GET to the
// single-subnet endpoint and renders the returned fields.
func TestSubnetShow(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{
		"subnet": "10.241.0.0/24", "vnet": "pmxcli0", "gateway": "10.241.0.1", "zone": "pmxcli",
	}, 200)

	out, err := run(t, f, "", "subnet", "show", "pmxcli0", "10.241.0.0-24")
	require.NoError(t, err)
	require.Contains(t, out, "10.241.0.0/24")
	require.Contains(t, out, "10.241.0.1")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", rec[0].path)
}

// TestSubnetShowPendingRunning verifies the --pending and --running flags are
// forwarded as query parameters.
func TestSubnetShowPendingRunning(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{
		"subnet": "10.241.0.0/24", "vnet": "pmxcli0",
	}, 200)

	_, err := run(t, f, "", "subnet", "show", "pmxcli0", "10.241.0.0-24", "--pending", "--running")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
	require.Equal(t, "1", rec[0].query.Get("running"))
}

// TestSubnetShowNotFound covers the error path when the subnet does not exist.
func TestSubnetShowNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets/missing", nil, 404)

	_, err := run(t, f, "", "subnet", "show", "pmxcli0", "missing")
	require.Error(t, err)
	require.ErrorContains(t, err, "get subnet")
	require.Len(t, rec, 1)
}

// TestSubnetShowByCIDR verifies a CIDR argument is resolved to the full
// subnet ID via the vnet's subnet list before the single-subnet GET.
func TestSubnetShowByCIDR(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets", []map[string]any{
		{"subnet": "pmxcli-10.241.0.0-24", "cidr": "10.241.0.0/24", "gateway": "10.241.0.1", "zone": "pmxcli"},
	}, 200)
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets/pmxcli-10.241.0.0-24", map[string]any{
		"subnet": "10.241.0.0/24", "vnet": "pmxcli0", "gateway": "10.241.0.1", "zone": "pmxcli",
	}, 200)

	out, err := run(t, f, "", "subnet", "show", "pmxcli0", "10.241.0.0/24")
	require.NoError(t, err)
	require.Contains(t, out, "10.241.0.0/24")
	require.Len(t, rec, 2)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0/subnets", rec[0].path)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0/subnets/pmxcli-10.241.0.0-24", rec[1].path)
}

// TestSubnetShowByCIDRNotFound covers a CIDR that matches no subnet on the
// vnet: the command fails during resolution, before any single-subnet GET.
func TestSubnetShowByCIDRNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets", []map[string]any{}, 200)

	_, err := run(t, f, "", "subnet", "show", "pmxcli0", "10.9.0.0/24")
	require.Error(t, err)
	require.ErrorContains(t, err, "not found on vnet")
	require.Len(t, rec, 1)
}
