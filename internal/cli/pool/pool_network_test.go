package pool

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestPoolSetNetwork verifies --network reaches the PUT /pools body, alone and
// together with --delete.
func TestPoolSetNetwork(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "set", "prod", "--network", "vnet/zone1/vnet1/100")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "vnet/zone1/vnet1/100", rec[0].body["network"])
	require.NotContains(t, rec[0].body, "delete")

	rec = nil
	_, err = run(t, f, "", "set", "prod", "--network", "vnet/zone1/vnet1", "--delete")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "vnet/zone1/vnet1", rec[0].body["network"])
	require.Equal(t, "1", rec[0].body["delete"])
}

// TestPoolSetNetworkOmittedWhenUnset verifies an unset --network is not sent.
func TestPoolSetNetworkOmittedWhenUnset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "set", "prod", "--vms", "100")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.NotContains(t, rec[0].body, "network")
}
