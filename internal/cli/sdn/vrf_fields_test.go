package sdn

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestZoneCreateForwardsDedicatedVrfAndRoutes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/sdn/zones", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, map[string]any{})
	})

	_, err := run(t, f, "", "zone", "create", "z1", "--type", "simple",
		"--dedicated-vrf", "--route", "10.1.0.0/16,gw=10.0.0.1", "--route", "10.2.0.0/16,gw=10.0.0.1")
	require.NoError(t, err)
	require.Equal(t, "1", gotForm.Get("dedicated-vrf"))
	require.Equal(t, []string{"10.1.0.0/16,gw=10.0.0.1", "10.2.0.0/16,gw=10.0.0.1"}, gotForm["route"])
}

func TestZoneSetForwardsDedicatedVrfAndRoutes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/zones/z1", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "set", "z1", "--dedicated-vrf=false")
	require.NoError(t, err, "--dedicated-vrf alone is a change")
	require.Len(t, rec, 1)
	require.Equal(t, "0", rec[0].body["dedicated-vrf"])
	require.NotContains(t, rec[0].body, "route")

	rec = nil
	_, err = run(t, f, "", "zone", "set", "z1", "--route", "10.1.0.0/16,gw=10.0.0.1")
	require.NoError(t, err)
	require.Contains(t, rec[0].body["route"], "10.1.0.0/16,gw=10.0.0.1")
	require.NotContains(t, rec[0].body, "dedicated-vrf")
}

func TestFabricForwardsZone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", map[string]any{}, 200)
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "create", "fab1", "--protocol", "ospf", "--zone", "z1")
	require.NoError(t, err)
	_, err = run(t, f, "", "fabric", "set", "fab1", "--protocol", "ospf", "--zone", "z1")
	require.NoError(t, err)
	require.Len(t, rec, 2)
	require.Equal(t, "z1", rec[0].body["zone"])
	require.Equal(t, "z1", rec[1].body["zone"])

	rec = nil
	_, err = run(t, f, "", "fabric", "create", "fab1", "--protocol", "ospf")
	require.NoError(t, err)
	require.NotContains(t, rec[0].body, "zone")
}
