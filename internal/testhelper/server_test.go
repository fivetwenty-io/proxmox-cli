package testhelper_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
	"github.com/stretchr/testify/require"
)

// pveEnvelope mirrors the standard PVE JSON envelope {"data": ...}.
type pveEnvelope struct {
	Data json.RawMessage `json:"data"`
}

// getJSON performs an HTTP GET against url and decodes the PVE envelope,
// returning the raw data field.
func getJSON(t *testing.T, url string) json.RawMessage {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var env pveEnvelope
	require.NoError(t, json.Unmarshal(body, &env))
	return env.Data
}

func TestNewFakePVE_DefaultVersionEndpoint(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	data := getJSON(t, f.BaseURL()+"/api2/json/version")

	var v map[string]any
	require.NoError(t, json.Unmarshal(data, &v))
	require.Equal(t, "8.1.4", v["version"])
	require.Equal(t, "8.1", v["release"])
}

func TestNewFakePVE_DefaultClusterStatusEndpoint(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	data := getJSON(t, f.BaseURL()+"/api2/json/cluster/status")

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(data, &entries))
	require.Len(t, entries, 2)

	// First entry is the cluster summary.
	require.Equal(t, "cluster", entries[0]["type"])

	// Second entry is the node.
	require.Equal(t, "node", entries[1]["type"])
	require.Equal(t, "pve1", entries[1]["name"])
	require.Equal(t, "192.168.1.10", entries[1]["ip"])
}

func TestNewFakePVE_DefaultNodesEndpoint(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	data := getJSON(t, f.BaseURL()+"/api2/json/nodes")

	var nodes []map[string]any
	require.NoError(t, json.Unmarshal(data, &nodes))
	require.Len(t, nodes, 1)
	require.Equal(t, "pve1", nodes[0]["node"])
	require.Equal(t, "online", nodes[0]["status"])
}

func TestNewFakePVE_CustomRoute_OverridesDefault(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	// Override the default /version endpoint.
	f.HandleJSON("GET /api2/json/version", map[string]any{
		"version": "7.4.3",
		"release": "7.4",
		"repoid":  "deadbeef",
		"console": "applet",
	})

	data := getJSON(t, f.BaseURL()+"/api2/json/version")
	var v map[string]any
	require.NoError(t, json.Unmarshal(data, &v))
	require.Equal(t, "7.4.3", v["version"])
}

func TestNewFakePVE_CustomHandleFunc(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	f.HandleFunc("GET /api2/json/custom", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]string{"hello": "world"})
	})

	data := getJSON(t, f.BaseURL()+"/api2/json/custom")
	var result map[string]string
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, "world", result["hello"])
}

func TestNewFakePVE_WriteError_ReturnsHTTPStatus(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	f.HandleFunc("GET /api2/json/fail", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	resp, err := http.Get(f.BaseURL() + "/api2/json/fail") //nolint:noctx // test helper
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestNewFakePVE_BaseURL_MatchesServer(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	require.Equal(t, f.Server.URL, f.BaseURL())
	require.True(t, strings.HasPrefix(f.BaseURL(), "http://"), "base URL should use http scheme")
}

func TestNewFakePVE_MustNewClient_ReturnsClient(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	c := f.MustNewClient(t)
	require.NotNil(t, c)
}

func TestNewFakePVE_Options_PointAtFakeServer(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	require.Equal(t, "http", f.Options.Protocol)
	require.NotEmpty(t, f.Options.Host, "Options.Host must be set")
	require.NotEmpty(t, f.Options.APIToken, "Options.APIToken must be set for construction to succeed")
}

func TestNewFakePVE_MultipleRoutes_AllReachable(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePVE(t)

	f.HandleJSON("GET /api2/json/access/users", []map[string]any{
		{"userid": "root@pam"},
	})
	f.HandleJSON("GET /api2/json/pools", []map[string]any{
		{"poolid": "testpool"},
	})

	usersData := getJSON(t, f.BaseURL()+"/api2/json/access/users")
	var users []map[string]any
	require.NoError(t, json.Unmarshal(usersData, &users))
	require.Len(t, users, 1)
	require.Equal(t, "root@pam", users[0]["userid"])

	poolsData := getJSON(t, f.BaseURL()+"/api2/json/pools")
	var pools []map[string]any
	require.NoError(t, json.Unmarshal(poolsData, &pools))
	require.Len(t, pools, 1)
	require.Equal(t, "testpool", pools[0]["poolid"])
}

func TestNewFakePVE_ServerClosedAfterTest(t *testing.T) {
	// This test verifies t.Cleanup correctly registers server close.
	// We create a sub-test and grab the URL, then verify outside.
	var serverURL string

	t.Run("inner", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		serverURL = f.BaseURL()
		require.NotEmpty(t, serverURL)
	})

	// After inner completes, server is closed; requests should fail.
	_, err := http.Get(serverURL + "/api2/json/version") //nolint:noctx // test helper
	require.Error(t, err, "request to closed server should fail")
}
