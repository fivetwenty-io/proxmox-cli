package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ---- command tree registration -----------------------------------------------

func TestZoneCommandTree_IncludesPermissions(t *testing.T) {
	cmd := newZoneCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	require.True(t, got["permissions"], "zone is missing permissions")
}

func TestVnetCommandTree_IncludesPermissions(t *testing.T) {
	cmd := newVnetCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	require.True(t, got["permissions"], "vnet is missing permissions")
}

// ---- zone permissions --------------------------------------------------------

func TestZonePermissionsList_ExactPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/access/acl", []any{
		map[string]any{"path": "/sdn/zones/z1", "type": "user", "ugid": "alice@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/sdn/zones/z2", "type": "user", "ugid": "bob@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
	}, 200)

	out, err := run(t, f, "", "zone", "permissions", "list", "z1")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Contains(t, out, "alice@pve")
	require.NotContains(t, out, "bob@pve")
	require.NotContains(t, out, "admins")
	require.NotContains(t, out, "INHERITED")
}

func TestZonePermissionsList_Inherited(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	record(f, &[]recordedRequest{}, "GET /api2/json/access/acl", []any{
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
		map[string]any{"path": "/sdn", "type": "user", "ugid": "carol@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/sdn/zones", "type": "user", "ugid": "dave@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/sdn/zones/z1", "type": "user", "ugid": "alice@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{"path": "/sdn/zones/z2", "type": "user", "ugid": "bob@pve", "roleid": "PVEAuditor", "propagate": 1},
	}, 200)

	out, err := run(t, f, "", "zone", "permissions", "list", "z1", "--inherited")
	require.NoError(t, err)
	require.Contains(t, out, "INHERITED")
	require.Contains(t, out, "admins")
	require.Contains(t, out, "carol@pve")
	require.Contains(t, out, "dave@pve")
	require.Contains(t, out, "alice@pve")
	require.NotContains(t, out, "bob@pve")
}

func TestZonePermissionsGrant(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/access/acl", map[string]any{}, 200)

	out, err := run(t, f, "", "zone", "permissions", "grant", "z1", "--roles", "PVEAuditor", "--users", "alice@pve")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/sdn/zones/z1", rec[0].body["path"])
	require.Equal(t, "PVEAuditor", rec[0].body["roles"])
	require.Equal(t, "alice@pve", rec[0].body["users"])
	require.NotContains(t, rec[0].body, "delete")
	require.NotContains(t, rec[0].body, "propagate", "propagate must be omitted (nil) unless --no-propagate is passed")
	require.Contains(t, out, "Granted")
	require.Contains(t, out, "/sdn/zones/z1")
}

func TestZonePermissionsRevoke_NoPropagate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/access/acl", map[string]any{}, 200)

	out, err := run(t, f, "", "zone", "permissions", "revoke", "z1",
		"--roles", "PVEAuditor", "--groups", "ops", "--no-propagate")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "/sdn/zones/z1", rec[0].body["path"])
	require.Equal(t, "ops", rec[0].body["groups"])
	require.Equal(t, "1", rec[0].body["delete"])
	require.Equal(t, "0", rec[0].body["propagate"])
	require.Contains(t, out, "Revoked")
}

func TestZonePermissionsGrant_RequiresSubject(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/access/acl", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "permissions", "grant", "z1", "--roles", "PVEAuditor")
	require.Error(t, err)
	require.ErrorContains(t, err, "at least one of")
	require.Empty(t, rec, "grant must not call the API without a subject")
}

func TestZonePermissionsEffective(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/access/permissions", map[string]any{
		"/sdn/zones/z1": map[string]any{"SDN.Audit": 1},
	}, 200)

	out, err := run(t, f, "", "zone", "permissions", "effective", "z1", "--userid", "alice@pve")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "/sdn/zones/z1", rec[0].query.Get("path"))
	require.Equal(t, "alice@pve", rec[0].query.Get("userid"))
	require.Contains(t, out, "SDN.Audit")
}

// ---- vnet permissions ---------------------------------------------------------

func TestVnetPermissionsList_AutoResolveZone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var vnetRec []recordedRequest
	record(f, &vnetRec, "GET /api2/json/cluster/sdn/vnets/v1", map[string]any{"zone": "z1"}, 200)
	record(f, &[]recordedRequest{}, "GET /api2/json/access/acl", []any{
		map[string]any{
			"path": "/sdn/zones/z1/v1", "type": "user", "ugid": "alice@pve", "roleid": "PVEAuditor", "propagate": 1,
		},
		map[string]any{
			"path": "/sdn/zones/z1/v2", "type": "user", "ugid": "bob@pve", "roleid": "PVEAuditor", "propagate": 1,
		},
	}, 200)

	out, err := run(t, f, "", "vnet", "permissions", "list", "v1")
	require.NoError(t, err)
	require.Len(t, vnetRec, 1, "auto-resolve must hit the vnet lookup route")
	require.Contains(t, out, "alice@pve")
	require.NotContains(t, out, "bob@pve")
}

func TestVnetPermissionsList_ZoneFlagSkipsLookup(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var vnetRec []recordedRequest
	record(f, &vnetRec, "GET /api2/json/cluster/sdn/vnets/v1", nil, 500)
	record(f, &[]recordedRequest{}, "GET /api2/json/access/acl", []any{
		map[string]any{
			"path": "/sdn/zones/z1/v1", "type": "user", "ugid": "alice@pve", "roleid": "PVEAuditor", "propagate": 1,
		},
	}, 200)

	out, err := run(t, f, "", "vnet", "permissions", "list", "v1", "--zone", "z1")
	require.NoError(t, err)
	require.Empty(t, vnetRec, "--zone must skip the vnet lookup route entirely")
	require.Contains(t, out, "alice@pve")
}

func TestVnetPermissionsList_Inherited(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	record(f, &[]recordedRequest{}, "GET /api2/json/cluster/sdn/vnets/v1", map[string]any{"zone": "z1"}, 200)
	record(f, &[]recordedRequest{}, "GET /api2/json/access/acl", []any{
		map[string]any{"path": "/", "type": "group", "ugid": "admins", "roleid": "Administrator", "propagate": 1},
		map[string]any{"path": "/sdn/zones/z1", "type": "user", "ugid": "carol@pve", "roleid": "PVEAuditor", "propagate": 1},
		map[string]any{
			"path": "/sdn/zones/z1/v1", "type": "user", "ugid": "alice@pve", "roleid": "PVEAuditor", "propagate": 1,
		},
		map[string]any{
			"path": "/sdn/zones/z1/v2", "type": "user", "ugid": "bob@pve", "roleid": "PVEAuditor", "propagate": 1,
		},
	}, 200)

	out, err := run(t, f, "", "vnet", "permissions", "list", "v1", "--inherited")
	require.NoError(t, err)
	require.Contains(t, out, "admins")
	require.Contains(t, out, "carol@pve")
	require.Contains(t, out, "alice@pve")
	require.NotContains(t, out, "bob@pve")
}

func TestVnetPermissionsGrant_AutoResolve(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	record(f, &[]recordedRequest{}, "GET /api2/json/cluster/sdn/vnets/v1", map[string]any{"zone": "z1"}, 200)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/access/acl", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "permissions", "grant", "v1", "--roles", "PVEAuditor", "--tokens", "alice@pve!ci")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "/sdn/zones/z1/v1", rec[0].body["path"])
	require.Equal(t, "PVEAuditor", rec[0].body["roles"])
	require.Equal(t, "alice@pve!ci", rec[0].body["tokens"])
	require.Contains(t, out, "Granted")
}

func TestVnetPermissionsRevoke_ZoneFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var vnetRec []recordedRequest
	record(f, &vnetRec, "GET /api2/json/cluster/sdn/vnets/v1", nil, 500)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/access/acl", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "permissions", "revoke", "v1", "--zone", "z1",
		"--roles", "PVEAuditor", "--users", "alice@pve")
	require.NoError(t, err)
	require.Empty(t, vnetRec, "--zone must skip the vnet lookup route entirely")
	require.Len(t, rec, 1)
	require.Equal(t, "/sdn/zones/z1/v1", rec[0].body["path"])
	require.Equal(t, "1", rec[0].body["delete"])
	require.Contains(t, out, "Revoked")
}

func TestVnetPermissionsEffective_AutoResolve(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	record(f, &[]recordedRequest{}, "GET /api2/json/cluster/sdn/vnets/v1", map[string]any{"zone": "z1"}, 200)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/access/permissions", map[string]any{
		"/sdn/zones/z1/v1": map[string]any{"SDN.Audit": 1},
	}, 200)

	out, err := run(t, f, "", "vnet", "permissions", "effective", "v1")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "/sdn/zones/z1/v1", rec[0].query.Get("path"))
	require.Contains(t, out, "SDN.Audit")
}

func TestVnetPermissions_ZoneNotFound_Error(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	record(f, &[]recordedRequest{}, "GET /api2/json/cluster/sdn/vnets/ghost", map[string]any{"zone": ""}, 200)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/access/acl", []any{}, 200)

	_, err := run(t, f, "", "vnet", "permissions", "list", "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, "resolve zone")
	require.Empty(t, rec, "acl endpoint must not be hit when zone resolution fails")
}
