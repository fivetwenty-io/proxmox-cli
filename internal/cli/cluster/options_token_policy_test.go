package cluster

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// tokenPolicyOptions is the shape PVE 9.3 returns: an object whose booleans are
// 0/1 integers and whose max-lifetime is in seconds.
func tokenPolicyOptions() map[string]any {
	return map[string]any{
		"keyboard": "en-us",
		"token-policy": map[string]any{
			"require-expiry": 1, "max-lifetime": 7776000, "disallow-expiry-extension": 1,
		},
	}
}

// TestClusterOptions_GetRendersTokenPolicyObject verifies the object-valued
// token-policy prints as key=value pairs rather than as raw JSON.
func TestClusterOptions_GetRendersTokenPolicyObject(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, tokenPolicyOptions())
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "get"))
	out := buf.String()
	require.Contains(t, out, "max-lifetime=7776000")
	require.Contains(t, out, "require-expiry=1")
	require.NotContains(t, out, `{"`, "the object must not print as raw JSON")
}

// TestClusterOptions_GetKeepsTokenPolicyObjectInJSON verifies JSON output
// carries the nested object, not a flattened string.
func TestClusterOptions_GetKeepsTokenPolicyObjectInJSON(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, tokenPolicyOptions())
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "get"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	policy, ok := got["token-policy"].(map[string]any)
	require.True(t, ok, "token-policy must stay an object in JSON output: %s", buf.String())
	require.EqualValues(t, 7776000, policy["max-lifetime"])
}

// TestClusterOptions_SetForwardsTokenPolicy verifies the property string
// reaches the request body untouched.
func TestClusterOptions_SetForwardsTokenPolicy(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/options", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "set",
		"--token-policy", "require-expiry=1,max-lifetime=7776000"))
	require.Equal(t, "require-expiry=1,max-lifetime=7776000", gotForm.Get("token-policy"))
	require.NotContains(t, gotForm, "keyboard")
}
