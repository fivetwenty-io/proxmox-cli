package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

func TestNodeNetworkLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/network", &rec, []map[string]any{
		{"name": "eth0", "type": "eth", "active": 1, "autostart": 1, "method": "static", "cidr": "10.0.0.1/24"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "eth0")
	require.Contains(t, buf.String(), "10.0.0.1/24")
}

func TestNodeNetworkLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/network", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list network interfaces")
}

func TestNodeNetworkShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/network/eth0", &rec, map[string]any{
		"name": "eth0", "type": "eth", "active": true, "autostart": true,
		"method": "static", "cidr": "10.0.0.1/24", "options": []any{}, "options6": []any{}, "altnames": []any{},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "show", "eth0")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "10.0.0.1/24")
}

func TestNodeNetworkCreate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/network", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "create", "vmbr0",
		"--autostart",
		"--bond-primary", "eth0",
		"--bond-mode", "active-backup",
		"--bond-xmit-hash-policy", "layer2",
		"--bridge-ports", "eth0,eth1",
		"--bridge-vlan-aware",
		"--cidr", "10.0.0.1/24",
		"--cidr6", "fd00::1/64",
		"--comments", "comment1",
		"--comments6", "comment6",
		"--gateway", "10.0.0.254",
		"--gateway6", "fd00::fe",
		"--method", "static",
		"--method6", "static",
		"--mtu", "1500",
		"--slaves", "eth0,eth1",
		"--type", "bridge",
		"--vlan-id", "10",
		"--vlan-raw-device", "eth0",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "vmbr0", rec.form.Get("iface"))

	want := map[string]string{
		"autostart":             "1",
		"bond-primary":          "eth0",
		"bond_mode":             "active-backup",
		"bond_xmit_hash_policy": "layer2",
		"bridge_ports":          "eth0,eth1",
		"bridge_vlan_aware":     "1",
		"cidr":                  "10.0.0.1/24",
		"cidr6":                 "fd00::1/64",
		"comments":              "comment1",
		"comments6":             "comment6",
		"gateway":               "10.0.0.254",
		"gateway6":              "fd00::fe",
		"method":                "static",
		"method6":               "static",
		"mtu":                   "1500",
		"slaves":                "eth0,eth1",
		"type":                  "bridge",
		"vlan-id":               "10",
		"vlan-raw-device":       "eth0",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestNodeNetworkUpdate_RequiresAFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "update", "eth0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestNodeNetworkUpdate_SendsDeleteAndDigest(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/network/eth0", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "update", "eth0",
		"--mtu", "9000", "--delete", "comments", "--digest", "d1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "9000", rec.form.Get("mtu"))
	require.Equal(t, []string{"comments"}, rec.form["delete"])
	require.Equal(t, "d1", rec.form.Get("digest"))
}

func TestNodeNetworkDelete_Deletes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+nodeAPIBase+"/network/eth0", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "delete", "eth0", "--digest", "d1", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "d1", rec.query.Get("digest"))
}

func TestNodeNetworkDelete_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "delete", "eth0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

func TestNodeNetworkRevert_Reverts(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+nodeAPIBase+"/network", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "revert", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, nodeAPIBase+"/network", rec.path)
	require.Contains(t, buf.String(), "reverted")
}

func TestNodeNetworkRevert_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "revert")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

func TestNodeNetworkApply_Applies(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/network", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "network", "apply")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, nodeAPIBase+"/network", rec.path)
	require.Contains(t, buf.String(), "reloaded")
}
