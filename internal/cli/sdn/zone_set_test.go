package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

func TestZoneSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "set", "pmxcli")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested")
	require.Empty(t, rec)
}

func TestZoneSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	out, err := run(t, f, "", "zone", "set", "pmxcli",
		"--nodes", "pve1,pve2", "--ipam", "pve", "--mtu", "1500")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/zones/pmxcli", rec[0].path)
	require.Equal(t, "pve1,pve2", rec[0].body["nodes"])
	require.Equal(t, "pve", rec[0].body["ipam"])
	require.Equal(t, "1500", rec[0].body["mtu"])
}

// TestZoneSetOmitsUnsetFlags verifies optional attributes are not sent unless
// their flag was explicitly set.
func TestZoneSetOmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "set", "pmxcli", "--bridge", "vmbr0")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "vmbr0", rec[0].body["bridge"])
	require.NotContains(t, rec[0].body, "nodes", "unset optional must not be sent")
	require.NotContains(t, rec[0].body, "ipam", "unset optional must not be sent")
	require.NotContains(t, rec[0].body, "controller", "unset optional must not be sent")
}

func TestZoneSetDeleteOnly(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	out, err := run(t, f, "", "zone", "set", "pmxcli", "--delete", "bridge")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, "bridge", rec[0].body["delete"])
}

func TestZoneSetBoolFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "set", "pmxcli", "--advertise-subnets")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].body["advertise-subnets"])
}

func TestZoneSetError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/zones/pmxcli", nil, 500)

	_, err := run(t, f, "", "zone", "set", "pmxcli", "--nodes", "pve1")
	require.Error(t, err)
	require.ErrorContains(t, err, "update SDN zone")
}

func TestZoneCommandTree(t *testing.T) {
	cmd := newZoneCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "set", "delete"} {
		require.True(t, got[want], "zone is missing %q", want)
	}
}
