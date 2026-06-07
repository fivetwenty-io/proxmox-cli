package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- vnet ips create ---

func TestVnetIpsCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "ips", "create", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli", "--mac", "aa:bb:cc:dd:ee:ff")
	require.NoError(t, err)
	require.Contains(t, out, "created")
	require.Contains(t, out, "10.241.0.10")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/ips", rec[0].path)
	require.Equal(t, "10.241.0.10", rec[0].body["ip"])
	require.Equal(t, "pvecli", rec[0].body["zone"])
	require.Equal(t, "aa:bb:cc:dd:ee:ff", rec[0].body["mac"])
}

// TestVnetIpsCreateOmitsMac verifies MAC is sent only when explicitly given.
func TestVnetIpsCreateOmitsMac(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "create", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "10.241.0.10", rec[0].body["ip"])
	require.NotContains(t, rec[0].body, "mac", "unset mac must not be sent")
}

func TestVnetIpsCreateRequiresIp(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "create", "pvecli0", "--zone", "pvecli")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestVnetIpsCreateRequiresZone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "create", "pvecli0", "--ip", "10.241.0.10")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestVnetIpsCreateError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pvecli0/ips", nil, 500)

	_, err := run(t, f, "", "vnet", "ips", "create", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli")
	require.Error(t, err)
	require.ErrorContains(t, err, "create IP mapping in vnet")
}

// --- vnet ips set ---

func TestVnetIpsSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "set", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes to set")
	require.Empty(t, rec)
}

func TestVnetIpsSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "ips", "set", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli", "--mac", "aa:bb:cc:dd:ee:ff")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/ips", rec[0].path)
	require.Equal(t, "10.241.0.10", rec[0].body["ip"])
	require.Equal(t, "pvecli", rec[0].body["zone"])
	require.Equal(t, "aa:bb:cc:dd:ee:ff", rec[0].body["mac"])
}

func TestVnetIpsSetVmid(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "set", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli", "--vmid", "101")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "101", rec[0].body["vmid"])
	require.NotContains(t, rec[0].body, "mac", "unset mac must not be sent")
}

func TestVnetIpsSetRequiresIp(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "set", "pvecli0", "--zone", "pvecli", "--mac", "aa:bb:cc:dd:ee:ff")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestVnetIpsSetError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pvecli0/ips", nil, 500)

	_, err := run(t, f, "", "vnet", "ips", "set", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli", "--mac", "aa:bb:cc:dd:ee:ff")
	require.Error(t, err)
	require.ErrorContains(t, err, "update IP mapping in vnet")
}

// --- vnet ips delete ---

func TestVnetIpsDeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "delete", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestVnetIpsDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "ips", "delete", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pvecli0/ips", rec[0].path)
	// DELETE params are sent as query string; body map will be empty per the test helper.
}

func TestVnetIpsDeleteWithMac(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "delete", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli", "--mac", "aa:bb:cc:dd:ee:ff", "--yes")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	// DELETE params are encoded as query string; the test helper captures only POST form body.
}

func TestVnetIpsDeleteRequiresIp(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pvecli0/ips", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "ips", "delete", "pvecli0", "--zone", "pvecli", "--yes")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestVnetIpsDeleteError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pvecli0/ips", nil, 500)

	_, err := run(t, f, "", "vnet", "ips", "delete", "pvecli0",
		"--ip", "10.241.0.10", "--zone", "pvecli", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "delete IP mapping")
}

// TestVnetIpsCommandTree verifies the `ips` subgroup and its verbs are wired.
func TestVnetIpsCommandTree(t *testing.T) {
	cmd := newVnetIpsCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"create", "set", "delete"} {
		require.True(t, got[want], "vnet ips is missing %q", want)
	}
}

// TestVnetCommandTreeIncludesIps verifies `ips` is wired into the vnet group.
func TestVnetCommandTreeIncludesIps(t *testing.T) {
	cmd := newVnetCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	require.True(t, got["ips"], "vnet is missing \"ips\"")
}
