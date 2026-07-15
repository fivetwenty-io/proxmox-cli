package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- fabric CRUD ---

// TestFabricList verifies `sdn fabric list` reads the fabric definitions from
// GET /cluster/sdn/fabrics/fabric (GET /cluster/sdn/fabrics is only a
// directory index of fabric/node/all).
func TestFabricList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/fabric", []any{
		map[string]any{"id": "fab1", "protocol": "openfabric"},
		map[string]any{"id": "fab2", "protocol": "ospf"},
	}, 200)

	out, err := run(t, f, "", "fabric", "list")
	require.NoError(t, err)
	require.Contains(t, out, "fab1")
	require.Contains(t, out, "openfabric")
	require.Contains(t, out, "fab2")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/fabrics/fabric", rec[0].path)
}

// TestFabricListPending verifies the --pending flag is forwarded.
func TestFabricListPending(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/fabric", []any{
		map[string]any{"id": "fab1", "protocol": "openfabric"},
	}, 200)

	_, err := run(t, f, "", "fabric", "list", "--pending")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
}

func TestFabricListError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/fabric", nil, 500)

	_, err := run(t, f, "", "fabric", "list")
	require.Error(t, err)
	require.ErrorContains(t, err, "list SDN fabrics")
}

func TestFabricCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", map[string]any{}, 200)

	out, err := run(t, f, "", "fabric", "create", "fab1",
		"--protocol", "openfabric", "--ip-prefix", "10.0.0.0/24", "--hello-interval", "3")
	require.NoError(t, err)
	require.Contains(t, out, "fab1")
	require.Contains(t, out, "created")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "fab1", rec[0].body["id"])
	require.Equal(t, "openfabric", rec[0].body["protocol"])
	require.Equal(t, "10.0.0.0/24", rec[0].body["ip_prefix"])
	require.Equal(t, "3", rec[0].body["hello_interval"])
}

// TestFabricCreateOmitsUnsetFlags verifies optional attributes are sent only
// when their flag was set.
func TestFabricCreateOmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "create", "fab1", "--protocol", "ospf")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "ospf", rec[0].body["protocol"])
	require.NotContains(t, rec[0].body, "ip_prefix")
	require.NotContains(t, rec[0].body, "area")
	require.NotContains(t, rec[0].body, "route_filter")
	require.NotContains(t, rec[0].body, "lock-token")
}

// TestFabricCreateForwardsLockToken verifies the global-SDN unlock token is sent
// only when --lock-token is set.
func TestFabricCreateForwardsLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "create", "fab1", "--protocol", "ospf", "--lock-token", "tok-123")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok-123", rec[0].body["lock-token"])
}

// TestFabricCreateError verifies the create error wrap.
func TestFabricCreateError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", nil, 500)

	_, err := run(t, f, "", "fabric", "create", "fab1", "--protocol", "ospf")
	require.Error(t, err)
	require.ErrorContains(t, err, "create SDN fabric \"fab1\"")
}

// TestFabricCreateRequiresProtocol verifies --protocol is mandatory and no
// request is issued without it.
func TestFabricCreateRequiresProtocol(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "create", "fab1")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestFabricGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{
		"id": "fab1", "protocol": "openfabric", "ip_prefix": "10.0.0.0/24",
	}, 200)

	out, err := run(t, f, "", "fabric", "get", "fab1")
	require.NoError(t, err)
	require.Contains(t, out, "openfabric")
	require.Contains(t, out, "10.0.0.0/24")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/fabrics/fabric/fab1", rec[0].path)
}

func TestFabricSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{}, 200)

	out, err := run(t, f, "", "fabric", "set", "fab1",
		"--protocol", "openfabric", "--area", "0.0.0.0")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "openfabric", rec[0].body["protocol"])
	require.Equal(t, "0.0.0.0", rec[0].body["area"])
	require.NotContains(t, rec[0].body, "ip_prefix", "unset optional must be omitted from the update body")
}

func TestFabricSetRequiresProtocol(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "set", "fab1", "--area", "0.0.0.0")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestFabricDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{}, 200)

	out, err := run(t, f, "", "fabric", "delete", "fab1", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

func TestFabricDeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "delete", "fab1")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

// --- fabric node sub-resource ---

func TestFabricNodeListAll(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/node", []any{
		map[string]any{"fabric_id": "fab1", "node_id": "n1"},
	}, 200)

	out, err := run(t, f, "", "fabric", "node", "list")
	require.NoError(t, err)
	require.Contains(t, out, "n1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/fabrics/node", rec[0].path)
}

func TestFabricNodeListByFabric(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/node/fab1", []any{
		map[string]any{"fabric_id": "fab1", "node_id": "n1"},
	}, 200)

	out, err := run(t, f, "", "fabric", "node", "list", "--fabric", "fab1")
	require.NoError(t, err)
	require.Contains(t, out, "n1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/fabrics/node/fab1", rec[0].path)
}

func TestFabricNodeGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/fabrics/node/fab1/n1", map[string]any{
		"node_id": "n1", "protocol": "openfabric", "ip": "10.0.0.1",
	}, 200)

	out, err := run(t, f, "", "fabric", "node", "get", "fab1", "n1")
	require.NoError(t, err)
	require.Contains(t, out, "10.0.0.1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/fabrics/node/fab1/n1", rec[0].path)
}

func TestFabricNodeCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/node/fab1", map[string]any{}, 200)

	out, err := run(t, f, "", "fabric", "node", "create", "fab1", "n1",
		"--protocol", "openfabric", "--ip", "10.0.0.1")
	require.NoError(t, err)
	require.Contains(t, out, "added")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "n1", rec[0].body["node_id"])
	require.Equal(t, "openfabric", rec[0].body["protocol"])
	require.Equal(t, "10.0.0.1", rec[0].body["ip"])
	require.NotContains(t, rec[0].body, "ip6", "unset optional must be omitted")
	require.NotContains(t, rec[0].body, "endpoint", "unset optional must be omitted")
	require.NotContains(t, rec[0].body, "public_key", "unset optional must be omitted")
}

// TestFabricNodeCreateForwardsPublicKey verifies the WireGuard public key and
// lock token are forwarded only when their flags are set.
func TestFabricNodeCreateForwardsPublicKey(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/node/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "node", "create", "fab1", "n1",
		"--protocol", "openfabric", "--public-key", "pubkey==", "--lock-token", "tok-1")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "pubkey==", rec[0].body["public_key"])
	require.Equal(t, "tok-1", rec[0].body["lock-token"])
}

func TestFabricNodeCreateRequiresProtocol(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/node/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "node", "create", "fab1", "n1")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestFabricNodeSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/node/fab1/n1", map[string]any{}, 200)

	out, err := run(t, f, "", "fabric", "node", "set", "fab1", "n1",
		"--protocol", "openfabric", "--ip", "10.0.0.2")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "openfabric", rec[0].body["protocol"])
	require.Equal(t, "10.0.0.2", rec[0].body["ip"])
	require.NotContains(t, rec[0].body, "ip6", "unset optional must be omitted from the update body")
}

func TestFabricNodeDeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/fabrics/node/fab1/n1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "node", "delete", "fab1", "n1")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestFabricNodeDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/fabrics/node/fab1/n1", map[string]any{}, 200)

	out, err := run(t, f, "", "fabric", "node", "delete", "fab1", "n1", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "removed")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

// TestFabricCommandTree verifies the fabric verbs and node sub-tree are wired.
func TestFabricCommandTree(t *testing.T) {
	cmd := Group(nil)
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
		if c.Name() == "fabric" {
			node := map[string]bool{}
			for _, sc := range c.Commands() {
				node[sc.Name()] = true
			}
			for _, want := range []string{"list", "create", "get", "set", "delete", "node"} {
				require.True(t, node[want], "fabric is missing %q", want)
			}
		}
	}
	require.True(t, got["fabric"], "sdn is missing \"fabric\"")
}
