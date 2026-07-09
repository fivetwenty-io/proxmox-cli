package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestNodeSubscriptionShow_RendersSingle asserts that `node subscription
// show` renders the node's subscription fields.
func TestNodeSubscriptionShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/subscription", map[string]any{
		"status": "notfound",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeSubscriptionShowCmd(), "show", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "notfound")
}

// TestNodeSubscriptionUpdate_SendsRequest asserts that `node subscription
// update` issues the refresh request with no parameters.
func TestNodeSubscriptionUpdate_SendsRequest(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/subscription", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeSubscriptionUpdateCmd(), "update", "pdm-host")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), `Subscription status for node "pdm-host" refreshed.`)
}
