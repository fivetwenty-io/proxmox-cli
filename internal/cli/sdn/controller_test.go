package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestControllerList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/controllers", []any{
		map[string]any{"controller": "evpn1", "type": "evpn", "asn": 65000},
		map[string]any{"controller": "bgp1", "type": "bgp"},
	}, 200)

	out, err := run(t, f, "", "controller", "list")
	require.NoError(t, err)
	require.Contains(t, out, "evpn1")
	require.Contains(t, out, "evpn")
	require.Contains(t, out, "bgp1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/controllers", rec[0].path)
}

func TestControllerListError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/controllers", nil, 500)

	_, err := run(t, f, "", "controller", "list")
	require.Error(t, err)
	require.ErrorContains(t, err, "list SDN controllers")
}

func TestControllerCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/controllers", map[string]any{}, 200)

	out, err := run(t, f, "", "controller", "create", "evpn1",
		"--type", "evpn", "--asn", "65000", "--peers", "10.0.0.1,10.0.0.2", "--ebgp")
	require.NoError(t, err)
	require.Contains(t, out, "evpn1")
	require.Contains(t, out, "created")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "evpn1", rec[0].body["controller"])
	require.Equal(t, "evpn", rec[0].body["type"])
	require.Equal(t, "65000", rec[0].body["asn"])
	require.Equal(t, "10.0.0.1,10.0.0.2", rec[0].body["peers"])
	require.Equal(t, "1", rec[0].body["ebgp"])
}

// TestControllerCreateOmitsUnsetFlags verifies optional attributes are sent only
// when their flag was set.
func TestControllerCreateOmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/controllers", map[string]any{}, 200)

	_, err := run(t, f, "", "controller", "create", "bgp1", "--type", "bgp")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "bgp1", rec[0].body["controller"])
	require.Equal(t, "bgp", rec[0].body["type"])
	require.NotContains(t, rec[0].body, "asn")
	require.NotContains(t, rec[0].body, "peers")
	require.NotContains(t, rec[0].body, "ebgp")
}

// TestControllerCreateRequiresType verifies --type is mandatory and no request is
// issued without it.
func TestControllerCreateRequiresType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/controllers", map[string]any{}, 200)

	_, err := run(t, f, "", "controller", "create", "bgp1")
	require.Error(t, err)
	require.ErrorContains(t, err, "type")
	require.Empty(t, rec)
}

func TestControllerGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/controllers/evpn1", map[string]any{
		"controller": "evpn1", "type": "evpn", "asn": 65000, "peers": "10.0.0.1",
	}, 200)

	out, err := run(t, f, "", "controller", "get", "evpn1")
	require.NoError(t, err)
	require.Contains(t, out, "evpn1")
	require.Contains(t, out, "65000")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/controllers/evpn1", rec[0].path)
}

// TestControllerSetRequiresChange verifies a no-op set is rejected before any
// request is issued.
func TestControllerSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/controllers/evpn1", map[string]any{}, 200)

	_, err := run(t, f, "", "controller", "set", "evpn1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes to set")
	require.Empty(t, rec)
}

func TestControllerSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/controllers/evpn1", map[string]any{}, 200)

	out, err := run(t, f, "", "controller", "set", "evpn1", "--asn", "65001", "--delete", "peers")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "65001", rec[0].body["asn"])
	require.Equal(t, "peers", rec[0].body["delete"])
	require.NotContains(t, rec[0].body, "ebgp", "unset attributes must not be sent")
}

func TestControllerDeleteWithoutConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/controllers/evpn1", map[string]any{}, 200)

	_, err := run(t, f, "", "controller", "delete", "evpn1")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestControllerDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/controllers/evpn1", map[string]any{}, 200)

	out, err := run(t, f, "", "controller", "delete", "evpn1", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

// TestControllerCommandTree verifies the controller verb set is registered.
func TestControllerCommandTree(t *testing.T) {
	cmd := newControllerCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "get", "set", "delete"} {
		require.True(t, got[want], "controller is missing %q", want)
	}
}
