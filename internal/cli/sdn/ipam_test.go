package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestIpamList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/ipams", []any{
		map[string]any{"ipam": "pve", "type": "pve"},
		map[string]any{"ipam": "netbox1", "type": "netbox", "url": "https://nb.example"},
	}, 200)

	out, err := run(t, f, "", "ipam", "list")
	require.NoError(t, err)
	require.Contains(t, out, "netbox1")
	require.Contains(t, out, "netbox")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/ipams", rec[0].path)
}

func TestIpamCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/ipams", map[string]any{}, 200)

	out, err := run(t, f, "", "ipam", "create", "netbox1",
		"--type", "netbox", "--url", "https://nb.example/api", "--token", "supersecrettoken", "--section", "3")
	require.NoError(t, err)
	require.Contains(t, out, "netbox1")
	require.Contains(t, out, "created")
	require.NotContains(t, out, "supersecrettoken", "the API token must never be echoed")
	require.Len(t, rec, 1)
	require.Equal(t, "netbox1", rec[0].body["ipam"])
	require.Equal(t, "netbox", rec[0].body["type"])
	require.Equal(t, "https://nb.example/api", rec[0].body["url"])
	require.Equal(t, "supersecrettoken", rec[0].body["token"], "token is forwarded to the API")
	require.Equal(t, "3", rec[0].body["section"])
}

func TestIpamCreateOmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/ipams", map[string]any{}, 200)

	_, err := run(t, f, "", "ipam", "create", "pve", "--type", "pve")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "pve", rec[0].body["ipam"])
	require.NotContains(t, rec[0].body, "token")
	require.NotContains(t, rec[0].body, "url")
	require.NotContains(t, rec[0].body, "section")
}

func TestIpamCreateRequiresType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/ipams", map[string]any{}, 200)

	_, err := run(t, f, "", "ipam", "create", "netbox1")
	require.Error(t, err)
	require.ErrorContains(t, err, "type")
	require.Empty(t, rec)
}

func TestIpamGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/ipams/netbox1", map[string]any{
		"ipam": "netbox1", "type": "netbox", "url": "https://nb.example",
	}, 200)

	out, err := run(t, f, "", "ipam", "get", "netbox1")
	require.NoError(t, err)
	require.Contains(t, out, "netbox1")
	require.Contains(t, out, "https://nb.example")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/ipams/netbox1", rec[0].path)
}

// TestIpamGetScrubsToken verifies that if the API returns the stored provider
// token on get, the CLI strips it from the rendered output (table and raw).
func TestIpamGetScrubsToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/ipams/netbox1", map[string]any{
		"ipam": "netbox1", "type": "netbox", "url": "https://nb.example",
		"token": "supersecrettoken",
	}, 200)

	for _, format := range []string{"table", "json", "yaml"} {
		out, err := run(t, f, "", "--output", format, "ipam", "get", "netbox1")
		require.NoError(t, err)
		require.Contains(t, out, "netbox1")
		require.NotContains(t, out, "supersecrettoken", "token must not be echoed (%s)", format)
	}
}

func TestIpamSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/ipams/netbox1", map[string]any{}, 200)

	_, err := run(t, f, "", "ipam", "set", "netbox1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested")
	require.Empty(t, rec)
}

func TestIpamSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/ipams/netbox1", map[string]any{}, 200)

	out, err := run(t, f, "", "ipam", "set", "netbox1", "--token", "rotatedsecret", "--url", "https://nb2.example")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.NotContains(t, out, "rotatedsecret", "the API token must never be echoed")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "rotatedsecret", rec[0].body["token"])
	require.Equal(t, "https://nb2.example", rec[0].body["url"])
	require.NotContains(t, rec[0].body, "section", "unset attributes must not be sent")
}

func TestIpamDeleteWithoutConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/ipams/netbox1", map[string]any{}, 200)

	_, err := run(t, f, "", "ipam", "delete", "netbox1")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestIpamDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/ipams/netbox1", map[string]any{}, 200)

	out, err := run(t, f, "", "ipam", "delete", "netbox1", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

func TestIpamStatus(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/ipams/pve/status", []any{
		map[string]any{"vmid": "100", "ip": "10.241.0.10", "vnet": "pmxcli0"},
		map[string]any{"vmid": "101", "ip": "10.241.0.11", "vnet": "pmxcli0"},
	}, 200)

	out, err := run(t, f, "", "ipam", "status", "pve")
	require.NoError(t, err)
	require.Contains(t, out, "10.241.0.10")
	require.Contains(t, out, "IP")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/ipams/pve/status", rec[0].path)
}

func TestIpamCommandTree(t *testing.T) {
	cmd := newIpamCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "get", "set", "delete", "status"} {
		require.True(t, got[want], "ipam is missing %q", want)
	}
}
