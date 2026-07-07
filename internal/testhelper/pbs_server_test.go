package testhelper_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/ping"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/version"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

func TestNewFakePBS_DefaultVersionEndpoint(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	data := getJSON(t, f.BaseURL()+"/api2/json/version")

	var v map[string]any
	require.NoError(t, json.Unmarshal(data, &v))
	require.Equal(t, "3.4", v["version"])
	require.Equal(t, "3", v["release"])
}

func TestNewFakePBS_DefaultPingEndpoint(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	data := getJSON(t, f.BaseURL()+"/api2/json/ping")

	var p map[string]any
	require.NoError(t, json.Unmarshal(data, &p))
	require.Equal(t, true, p["pong"])
}

func TestNewFakePBS_CustomRoute_OverridesDefault(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	f.HandleJSON("GET /api2/json/version", map[string]any{
		"version": "2.4",
		"release": "2",
		"repoid":  "deadbeef",
	})

	data := getJSON(t, f.BaseURL()+"/api2/json/version")
	var v map[string]any
	require.NoError(t, json.Unmarshal(data, &v))
	require.Equal(t, "2.4", v["version"])
}

func TestNewFakePBS_CustomHandleFunc(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	f.HandleFunc("GET /api2/json/custom", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]string{"hello": "world"})
	})

	data := getJSON(t, f.BaseURL()+"/api2/json/custom")
	var result map[string]string
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, "world", result["hello"])
}

func TestNewFakePBS_WriteError_ReturnsHTTPStatus(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	f.HandleFunc("GET /api2/json/fail", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	resp, err := http.Get(f.BaseURL() + "/api2/json/fail") //nolint:noctx // test helper
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestNewFakePBS_BaseURL_MatchesServer(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	require.Equal(t, f.Server.URL, f.BaseURL())
	require.True(t, strings.HasPrefix(f.BaseURL(), "http://"), "base URL should use http scheme")
}

func TestNewFakePBS_Options_PointAtFakeServer(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	require.Equal(t, "http", f.Options.Protocol)
	require.NotEmpty(t, f.Options.Host, "Options.Host must be set")
	require.NotEmpty(t, f.Options.APIToken, "Options.APIToken must be set for construction to succeed")
	require.Equal(t, "PBSAPIToken", f.Options.APITokenName, "Options.APITokenName must be PBS-flavored")
	require.Equal(t, "PBSAuthCookie", f.Options.CookieName, "Options.CookieName must be PBS-flavored")
}

func TestNewFakePBS_MustNewClient_ReturnsClient(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	c := f.MustNewClient(t)
	require.NotNil(t, c)
}

func TestNewFakePBS_ServerClosedAfterTest(t *testing.T) {
	var serverURL string

	t.Run("inner", func(t *testing.T) {
		f := testhelper.NewFakePBS(t)
		serverURL = f.BaseURL()
		require.NotEmpty(t, serverURL)
	})

	_, err := http.Get(serverURL + "/api2/json/version") //nolint:noctx // test helper
	require.Error(t, err, "request to closed server should fail")
}

// TestNewFakePBS_RealClientRoundTrip_VersionAndPing exercises the fake server
// through a real PBS client (pbs.NewClient via MustNewClient) rather than a
// bare http.Get, verifying that the generated version/ping services decode
// the seeded envelope correctly and that the Authorization header carries the
// PBSAPIToken prefix (not PVEAPIToken).
func TestNewFakePBS_RealClientRoundTrip_VersionAndPing(t *testing.T) {
	t.Parallel()
	f := testhelper.NewFakePBS(t)

	var gotAuth string
	f.HandleFunc("GET /api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		testhelper.WriteData(w, map[string]any{
			"release": "3",
			"repoid":  "abc123de",
			"version": "3.4",
		})
	})

	cli := f.MustNewClient(t)

	verSvc := version.New(cli)
	verResp, err := verSvc.Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, verResp)
	require.Equal(t, "3.4", verResp.Version)
	require.Equal(t, "3", verResp.Release)

	require.True(t, strings.HasPrefix(gotAuth, "PBSAPIToken="),
		"PBS client must send the Authorization header with the PBSAPIToken prefix, got %q", gotAuth)

	pingSvc := ping.New(cli)
	pingResp, err := pingSvc.Ping(context.Background())
	require.NoError(t, err)
	require.NotNil(t, pingResp)
	require.True(t, pingResp.Pong.Bool())
}
