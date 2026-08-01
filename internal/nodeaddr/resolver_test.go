package nodeaddr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-cli/internal/nodeaddr"
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

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1", nil)
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

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve2", nil)
	require.NoError(t, err)
	require.Equal(t, "192.168.1.11", got)
}

func TestResolve_EmptyList_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	empty := cluster.ListStatusResponse{}
	svc := &fakeStatusLister{resp: &empty}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1", nil)
	require.NoError(t, err)
	require.Equal(t, "pve1", got, "should fall back to node name when list is empty")
}

func TestResolve_NilResponse_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{resp: nil}

	got, err := nodeaddr.Resolve(context.Background(), svc, "mynode", nil)
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

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve99", nil)
	require.NoError(t, err)
	require.Equal(t, "pve99", got, "unknown node name should fall back to node name")
}

func TestResolve_ServiceError_FallsBackToNodeName(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{
		err: errors.New("connection refused"),
	}

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1", nil)
	// Resolve is non-fatal on service errors; it reports the cause (see
	// TestResolve_ServiceError_ReportsCause) and falls back.
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

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1", nil)
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

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1", nil)
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

	got, err := nodeaddr.Resolve(context.Background(), svc, "pve1", nil)
	require.NoError(t, err)
	require.Equal(t, "10.1.2.3", got, "malformed entry should be skipped, valid entry should resolve")
}

func TestResolve_NilContext_ReturnsError(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{resp: statusResponse()}

	// A nil context.Context variable (not the nil literal, which SA1012 rejects)
	// exercises the function's explicit nil-context guard.
	var ctx context.Context
	_, err := nodeaddr.Resolve(ctx, svc, "pve1", nil)
	require.Error(t, err)
}

func TestResolve_EmptyNodeName_ReturnsError(t *testing.T) {
	t.Parallel()

	svc := &fakeStatusLister{resp: statusResponse()}

	_, err := nodeaddr.Resolve(context.Background(), svc, "", nil)
	require.Error(t, err)
}

// captureStderr swaps os.Stderr for a pipe, runs fn, and returns what fn
// wrote. Resolve announces a failed lookup on stderr rather than through a
// threaded writer (see warnLookupFailed), so this is the only way to observe
// it. Tests using it must not run in parallel — os.Stderr is process-wide.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	require.NoError(t, w.Close())
	out := <-done
	require.NoError(t, r.Close())

	return out
}

// TestResolve_ServiceError_ReportsCause is the regression test for a lookup
// failure that used to vanish: the fallback host is rarely a real hostname, so
// the operator's only symptom was ssh reporting that it could not resolve
// "pve1" — naming neither the API call nor the expired token behind it.
func TestResolve_ServiceError_ReportsCause(t *testing.T) {
	svc := &fakeStatusLister{err: errors.New("401 authentication failure")}

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))

	var got string
	stderr := captureStderr(t, func() {
		var err error
		got, err = nodeaddr.Resolve(context.Background(), svc, "pve1", log)
		require.NoError(t, err)
	})

	require.Equal(t, "pve1", got, "the fallback itself is deliberate and must not change")
	require.Contains(t, stderr, "pve1", "the warning must name the node")
	require.Contains(t, stderr, "401 authentication failure", "the warning must name the cause")
	require.Contains(t, logged.String(), "401 authentication failure", "the cause must reach the log too")
}

// TestResolve_ServiceError_RedactsQueryParams asserts that a transport error
// carrying the request URL cannot leak an authentication ticket through the
// warning, which goes to both stderr and the log file.
func TestResolve_ServiceError_RedactsQueryParams(t *testing.T) {
	svc := &fakeStatusLister{
		err: errors.New(`Get "https://pve:8006/api2/json/cluster/status?password=hunter2": connection refused`),
	}

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))

	stderr := captureStderr(t, func() {
		_, err := nodeaddr.Resolve(context.Background(), svc, "pve1", log)
		require.NoError(t, err)
	})

	require.NotContains(t, stderr, "hunter2")
	require.NotContains(t, logged.String(), "hunter2")
	require.Contains(t, stderr, "connection refused", "redaction must keep the error diagnosable")
}

// TestResolve_OrdinaryFallbacks_StaySilent guards the other half of the
// contract: a single-node install returns no cluster status at all, and a node
// absent from the list is a normal outcome for callers that pass a bare
// hostname. Warning on those would train operators to ignore the warning that
// matters.
func TestResolve_OrdinaryFallbacks_StaySilent(t *testing.T) {
	cases := map[string]*fakeStatusLister{
		"empty list":   {resp: statusResponse()},
		"nil response": {resp: nil},
		"not found":    {resp: statusResponse(map[string]any{"type": "node", "name": "other", "ip": "10.0.0.9"})},
	}

	for name, svc := range cases {
		t.Run(name, func(t *testing.T) {
			var logged bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))

			stderr := captureStderr(t, func() {
				got, err := nodeaddr.Resolve(context.Background(), svc, "pve1", log)
				require.NoError(t, err)
				require.Equal(t, "pve1", got)
			})

			require.Empty(t, stderr, "an ordinary fallback must not warn")
			require.Empty(t, logged.String(), "an ordinary fallback must not log")
		})
	}
}
