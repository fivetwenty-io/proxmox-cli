package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestZoneShow verifies `pve sdn zone show` issues a GET to the single-zone
// endpoint and renders the returned fields.
func TestZoneShow(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/zones/pvecli", map[string]any{
		"zone": "pvecli", "type": "simple", "nodes": "pve1", "ipam": "pve",
	}, 200)

	out, err := run(t, f, "", "zone", "show", "pvecli")
	require.NoError(t, err)
	require.Contains(t, out, "pvecli")
	require.Contains(t, out, "simple")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/zones/pvecli", rec[0].path)
}

// TestZoneShowPendingRunning verifies the --pending and --running flags are
// forwarded as query parameters.
func TestZoneShowPendingRunning(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/zones/pvecli", map[string]any{
		"zone": "pvecli", "type": "simple",
	}, 200)

	_, err := run(t, f, "", "zone", "show", "pvecli", "--pending", "--running")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
	require.Equal(t, "1", rec[0].query.Get("running"))
}

// TestZoneShowNotFound covers the error path when the zone does not exist.
func TestZoneShowNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/zones/missing", nil, 500)

	_, err := run(t, f, "", "zone", "show", "missing")
	require.Error(t, err)
	require.ErrorContains(t, err, "get SDN zone")
	require.Len(t, rec, 1)
}
