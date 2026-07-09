package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

func TestSubnetSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{}, 200)

	_, err := run(t, f, "", "subnet", "set", "pmxcli0", "10.241.0.0-24")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested")
	require.Empty(t, rec)
}

func TestSubnetSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{}, 200)

	out, err := run(t, f, "", "subnet", "set", "pmxcli0", "10.241.0.0-24",
		"--gateway", "10.241.0.1", "--snat")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", rec[0].path)
	require.Equal(t, "10.241.0.1", rec[0].body["gateway"])
	require.Equal(t, "1", rec[0].body["snat"])
}

// TestSubnetSetOmitsUnsetFlags verifies optional attributes are not sent unless
// their flag was explicitly set.
func TestSubnetSetOmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{}, 200)

	_, err := run(t, f, "", "subnet", "set", "pmxcli0", "10.241.0.0-24", "--gateway", "10.241.0.1")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "10.241.0.1", rec[0].body["gateway"])
	require.NotContains(t, rec[0].body, "snat", "unset optional must not be sent")
	require.NotContains(t, rec[0].body, "dhcp-dns-server", "unset optional must not be sent")
}

func TestSubnetSetDeleteOnly(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{}, 200)

	out, err := run(t, f, "", "subnet", "set", "pmxcli0", "10.241.0.0-24", "--delete", "gateway")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, "gateway", rec[0].body["delete"])
}

func TestSubnetSetDhcpDnsServer(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{}, 200)

	_, err := run(t, f, "", "subnet", "set", "pmxcli0", "10.241.0.0-24",
		"--dhcp-dns-server", "8.8.8.8")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "8.8.8.8", rec[0].body["dhcp-dns-server"])
}

func TestSubnetSetError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", nil, 500)

	_, err := run(t, f, "", "subnet", "set", "pmxcli0", "10.241.0.0-24", "--gateway", "10.241.0.1")
	require.Error(t, err)
	require.ErrorContains(t, err, "update subnet")
}

func TestSubnetCommandTree(t *testing.T) {
	cmd := newSubnetCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "set", "delete"} {
		require.True(t, got[want], "subnet is missing %q", want)
	}
}
