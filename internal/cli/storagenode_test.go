package cli_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// storageResources registers a cluster/resources response describing the given
// storage entries and reports whether the endpoint was hit.
func storageResources(f *testhelper.FakePVE, entries []map[string]any) *bool {
	hit := false
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		payload := make([]any, len(entries))
		for i, e := range entries {
			payload[i] = e
		}
		testhelper.WriteData(w, payload)
	})
	return &hit
}

func TestResolveStorageNode_ExplicitNodeSkipsCluster(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	hit := storageResources(f, nil)
	deps := &cli.Deps{API: ac, Node: "pve1", NodeExplicit: true}

	node, err := cli.ResolveStorageNode(context.Background(), deps, "local")
	require.NoError(t, err)
	require.Equal(t, "pve1", node)
	require.False(t, *hit, "an explicit --node must not query cluster resources")
}

// TestResolveStorageNode_AmbientNodeConsultsCluster verifies an ambient default
// node is not trusted as the storage's location: the cluster inventory places
// "backup" only on pve2, and pve2 must win over the ambient pve1.
func TestResolveStorageNode_AmbientNodeConsultsCluster(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	storageResources(f, []map[string]any{
		{"type": "storage", "storage": "backup", "node": "pve2", "status": "available"},
	})
	deps := &cli.Deps{API: ac, Node: "pve1"}

	node, err := cli.ResolveStorageNode(context.Background(), deps, "backup")
	require.NoError(t, err)
	require.Equal(t, "pve2", node)
}

// TestResolveStorageNode_AmbientNodeAmongCarriersWins verifies a shared storage
// present on the ambient default node resolves to that node, not an arbitrary
// carrier.
func TestResolveStorageNode_AmbientNodeAmongCarriersWins(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	storageResources(f, []map[string]any{
		{"type": "storage", "storage": "nfs", "node": "pve1", "status": "available"},
		{"type": "storage", "storage": "nfs", "node": "pve2", "status": "available"},
	})
	deps := &cli.Deps{API: ac, Node: "pve2"}

	node, err := cli.ResolveStorageNode(context.Background(), deps, "nfs")
	require.NoError(t, err)
	require.Equal(t, "pve2", node)
}

// TestResolveStorageNode_PrefersAvailableCarrier verifies a node where the
// storage is available beats a lexically earlier node where it is not.
func TestResolveStorageNode_PrefersAvailableCarrier(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	storageResources(f, []map[string]any{
		{"type": "storage", "storage": "nfs", "node": "pve1", "status": "unknown"},
		{"type": "storage", "storage": "nfs", "node": "pve2", "status": "available"},
	})
	deps := &cli.Deps{API: ac}

	node, err := cli.ResolveStorageNode(context.Background(), deps, "nfs")
	require.NoError(t, err)
	require.Equal(t, "pve2", node)
}

// TestResolveStorageNode_DeterministicPick verifies the lexically first
// available carrier is chosen when several qualify and none is the ambient
// default node.
func TestResolveStorageNode_DeterministicPick(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	storageResources(f, []map[string]any{
		{"type": "storage", "storage": "nfs", "node": "pve3", "status": "available"},
		{"type": "storage", "storage": "nfs", "node": "pve2", "status": "available"},
	})
	deps := &cli.Deps{API: ac}

	node, err := cli.ResolveStorageNode(context.Background(), deps, "nfs")
	require.NoError(t, err)
	require.Equal(t, "pve2", node)
}

func TestResolveStorageNode_UnknownStorage(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	storageResources(f, []map[string]any{
		{"type": "storage", "storage": "other", "node": "pve1", "status": "available"},
	})
	deps := &cli.Deps{API: ac}

	_, err := cli.ResolveStorageNode(context.Background(), deps, "nfs")
	require.Error(t, err)
	require.Contains(t, err.Error(), `storage "nfs" not found on any node`)
}
