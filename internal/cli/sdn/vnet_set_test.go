package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestVnetSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "set", "pmxcli0")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested")
	require.Empty(t, rec)
}

func TestVnetSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "set", "pmxcli0", "--alias", "lab-net", "--tag", "150", "--vlanaware")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0", rec[0].path)
	require.Equal(t, "lab-net", rec[0].body["alias"])
	require.Equal(t, "150", rec[0].body["tag"])
	require.Equal(t, "1", rec[0].body["vlanaware"])
	require.NotContains(t, rec[0].body, "zone", "unset attributes must not be sent")
}

func TestVnetSetDeleteOnly(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "set", "pmxcli0", "--delete", "alias")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, "alias", rec[0].body["delete"])
}

func TestVnetCommandTree(t *testing.T) {
	cmd := newVnetCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "set", "delete"} {
		require.True(t, got[want], "vnet is missing %q", want)
	}
}
