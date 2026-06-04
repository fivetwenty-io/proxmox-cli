package nodeaddr_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/nodeaddr"
	"github.com/stretchr/testify/require"
)

// fakeStatusLister implements nodeaddr.StatusLister for use in unit tests.
type fakeStatusLister struct {
	resp *cluster.ListStatusResponse
	err  error
}

func (f *fakeStatusLister) ListStatus(_ context.Context) (*cluster.ListStatusResponse, error) {
	return f.resp, f.err
}

// rawEntry marshals the given map into a json.RawMessage.
// Panics on marshal error (test-only helper).
func rawEntry(fields map[string]any) json.RawMessage {
	b, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return b
}

// statusResponse builds a *cluster.ListStatusResponse from a slice of raw JSON maps.
func statusResponse(entries ...map[string]any) *cluster.ListStatusResponse {
	raws := make(cluster.ListStatusResponse, 0, len(entries))
	for _, e := range entries {
		raws = append(raws, rawEntry(e))
	}
	return &raws
}

func TestResolve_MatchedNode_ReturnsIP(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{
		resp: statusResponse(
			map[string]any{
				"type": "cluster",
				"name": "testcluster",
				"id":   "testcluster",
			},
			map[string]any{
				"type":   "node",
				"name":   "pve1",
				"ip":     "192.168.1.10",
				"online": 1,
			},
			map[string]any{
				"type":   "node",
				"name":   "pve2",
				"ip":     "192.168.1.11",
				"online": 1,
			},
		),
	}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.10", got)
}

func TestResolve_SecondNode_ReturnsCorrectIP(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{
		resp: statusResponse(
			map[string]any{
				"type":   "node",
				"name":   "pve1",
				"ip":     "192.168.1.10",
				"online": 1,
			},
			map[string]any{
				"type":   "node",
				"name":   "pve2",
				"ip":     "192.168.1.11",
				"online": 1,
			},
		),
	}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve2")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.11", got)
}

func TestResolve_EmptyList_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	empty := cluster.ListStatusResponse{}
	svc := &fakeStatusLister{resp: &empty}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1")
	require.NoError(t, err)
	require.Equal(t, "pve1", got, "should fall back to node name when list is empty")
}

func TestResolve_NilResponse_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{resp: nil}

	got, err := nodeaddr.Resolve(context.Background(), svc, "mynode")
	require.NoError(t, err)
	require.Equal(t, "mynode", got)
}

func TestResolve_NodeNotFound_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{
		resp: statusResponse(
			map[string]any{
				"type":   "node",
				"name":   "pve1",
				"ip":     "10.0.0.1",
				"online": 1,
			},
		),
	}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve99")
	require.NoError(t, err)
	require.Equal(t, "pve99", got, "unknown node name should fall back to node name")
}

func TestResolve_ServiceError_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{
		err: errors.New("connection refused"),
	}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1")
	// Resolve is non-fatal on service errors; it falls back silently.
	require.NoError(t, err)
	require.Equal(t, "pve1", got, "service error should fall back to node name")
}

func TestResolve_NodeWithEmptyIP_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{
		resp: statusResponse(
			map[string]any{
				"type":   "node",
				"name":   "pve1",
				"ip":     "", // empty IP — malformed entry
				"online": 1,
			},
		),
	}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1")
	require.NoError(t, err)
	require.Equal(t, "pve1", got, "node with empty IP should fall back to node name")
}

func TestResolve_ClusterEntryIgnored_NodeResolved(t *testing.T) {
	t.Parallel()

	// Cluster entries must not be misinterpreted as node entries.
	svc := &fakeStatusLister{
		resp: statusResponse(
			map[string]any{
				"type": "cluster",
				"name": "pve1", // same name as a cluster, should NOT match
				"id":   "cluster",
			},
			map[string]any{
				"type":   "node",
				"name":   "pve1",
				"ip":     "172.16.0.5",
				"online": 1,
			},
		),
	}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1")
	require.NoError(t, err)
	require.Equal(t, "172.16.0.5", got, "should skip cluster entries and match only type==node")
}

func TestResolve_MalformedEntry_SkippedGracefully(t *testing.T) {
	t.Parallel()

	// Inject a malformed entry followed by a valid one.
	badRaw := json.RawMessage(`{not valid json`)
	raws := cluster.ListStatusResponse{
		badRaw,
		rawEntry(map[string]any{
			"type":   "node",
			"name":   "pve1",
			"ip":     "10.1.2.3",
			"online": 1,
		}),
	}
	svc := &fakeStatusLister{resp: &raws}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1")
	require.NoError(t, err)
	require.Equal(t, "10.1.2.3", got, "malformed entry should be skipped, valid entry should resolve")
}

func TestResolve_NilContext_ReturnsError(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{resp: statusResponse()}

	// A nil context.Context variable (not the nil literal, which SA1012 rejects)
	// exercises the function's explicit nil-context guard.
	var ctx context.Context
	_, err := nodeaddr.Resolve(ctx, svc, "pve1")
	require.Error(t, err)
}

func TestResolve_EmptyNodeName_ReturnsError(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{resp: statusResponse()}

	_, err := nodeaddr.Resolve(context.Background(), svc, "")
	require.Error(t, err)
}
