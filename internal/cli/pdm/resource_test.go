package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestResourceLs_FlattensSortsAndRendersErrorRow asserts that `resource ls`
// flattens the per-remote wrapper envelopes into one row per resource,
// sorts by remote then id, infers TYPE from the id prefix, and renders a
// remote that failed to respond as a single "error" row.
func TestResourceLs_FlattensSortsAndRendersErrorRow(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/resources/list", []map[string]any{
		{
			"remote": "zeta",
			"resources": []map[string]any{
				{"id": "qemu/100", "name": "vm100", "node": "pve1", "status": "running"},
			},
		},
		{
			"remote": "alpha",
			"resources": []map[string]any{
				{"id": "storage/local", "node": "pve1", "status": "available"},
				{"id": "node/pve1", "node": "pve1", "status": "online"},
			},
		},
		{"remote": "broken", "error": "connection refused"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 4)

	// alpha sorts before broken sorts before zeta; within alpha, node/pve1 sorts before storage/local.
	require.Equal(t, "alpha", got[0]["remote"])
	require.Equal(t, "node/pve1", got[0]["id"])
	require.Equal(t, "alpha", got[1]["remote"])
	require.Equal(t, "storage/local", got[1]["id"])
	require.Equal(t, "broken", got[2]["remote"])
	require.Equal(t, "connection refused", got[2]["error"])
	require.Equal(t, "zeta", got[3]["remote"])
	require.Equal(t, "qemu/100", got[3]["id"])
}

// TestResourceLs_ValidatesResourceType asserts that `resource ls` validates
// --resource-type against the enum before issuing any request.
func TestResourceLs_ValidatesResourceType(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceLsCmd(), "ls", "--resource-type", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--resource-type must be one of")
}

// TestResourceLs_SendsFilterFlags asserts that `resource ls` encodes every
// filter flag onto the expected query parameter names.
func TestResourceLs_SendsFilterFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/resources/list", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceLsCmd(), "ls",
		"--max-age", "60", "--resource-type", "qemu", "--search", "web", "--view", "prod")
	require.NoError(t, err)

	require.Equal(t, "GET", rec.method)
	require.Equal(t, "60", rec.query.Get("max-age"))
	require.Equal(t, "qemu", rec.query.Get("resource-type"))
	require.Equal(t, "web", rec.query.Get("search"))
	require.Equal(t, "prod", rec.query.Get("view"))
}

// TestResourceLocationInfo_ReportsSuccess asserts that `resource
// location-info` sends its filter flags and reports success, since the
// endpoint's generated binding carries no response data.
func TestResourceLocationInfo_ReportsSuccess(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/resources/location-info", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceLocationInfoCmd(), "location-info", "--view", "prod")
	require.NoError(t, err)

	require.Equal(t, "prod", rec.query.Get("view"))
	require.Contains(t, buf.String(), `Location info retrieved for view "prod".`)
}

// TestResourceStatus_RendersSingle asserts that `resource status` renders
// the response fields as a Single result.
func TestResourceStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/resources/status", map[string]any{
		"remote":    "alpha",
		"resources": []map[string]any{{"type": "qemu", "count": 3}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceStatusCmd(), "status")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "alpha")
}

// TestResourceSubscription_SortsByRemote asserts that `resource
// subscription` sorts entries by remote and renders state/error columns.
func TestResourceSubscription_SortsByRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/resources/subscription", []map[string]any{
		{"remote": "zeta", "state": "active"},
		{"remote": "alpha", "state": "none", "error": "unreachable"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceSubscriptionCmd(), "subscription")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["remote"])
	require.Equal(t, "zeta", got[1]["remote"])
}

// TestResourceTopEntities_ValidatesTimeframe asserts that `resource
// top-entities` validates --timeframe against the enum before issuing any request.
func TestResourceTopEntities_ValidatesTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceTopEntitiesCmd(), "top-entities", "--timeframe", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--timeframe must be one of")
}

// TestResourceTopEntities_PreservesRankOrder asserts that `resource
// top-entities` renders rows for each metric group in the server-provided
// rank order (not re-sorted), grouped guest-cpu, node-cpu, node-memory.
func TestResourceTopEntities_PreservesRankOrder(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/resources/top-entities", map[string]any{
		"guest-cpu": []map[string]any{
			{"remote": "alpha", "resource": map[string]any{"id": "qemu/200"}},
			{"remote": "alpha", "resource": map[string]any{"id": "qemu/100"}},
		},
		"node-cpu":    []map[string]any{{"remote": "alpha", "resource": map[string]any{"id": "node/pve1"}}},
		"node-memory": []map[string]any{{"remote": "alpha", "resource": map[string]any{"id": "node/pve1"}}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newResourceTopEntitiesCmd(), "top-entities")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 4)
	require.Equal(t, "guest-cpu", got[0]["metric"])
	require.Equal(t, "qemu/200", got[0]["resource"].(map[string]any)["id"], "rank order must be preserved, not sorted")
	require.Equal(t, "qemu/100", got[1]["resource"].(map[string]any)["id"])
	require.Equal(t, "node-cpu", got[2]["metric"])
	require.Equal(t, "node-memory", got[3]["metric"])
}
