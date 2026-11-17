package nodeaddr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/nodeaddr"
)

// TestResolveStrict covers the lookup a destructive caller uses: it returns
// the address cluster status lists, and every case where Resolve would fall
// back to the bare node name is an error wrapping ErrNodeNotMatched instead.
func TestResolveStrict(t *testing.T) {
	t.Parallel()

	listed := statusResponse(
		map[string]any{"type": "cluster", "name": "pve1", "id": "cluster"},
		map[string]any{"type": "node", "name": "pve1", "ip": "10.0.0.1", "online": 1},
		map[string]any{"type": "node", "name": "pve2", "ip": "", "online": 1},
	)

	t.Run("a listed node returns its address", func(t *testing.T) {
		t.Parallel()

		got, err := nodeaddr.ResolveStrict(context.Background(), &fakeStatusLister{resp: listed}, "pve1")
		require.NoError(t, err)
		require.Equal(t, "10.0.0.1", got, "the cluster entry of the same name is not a node")
	})

	for _, tc := range []struct {
		name string
		svc  *fakeStatusLister
		node string
		want string
	}{
		{"an unlisted node", &fakeStatusLister{resp: listed}, "pve9", `does not list node "pve9"`},
		{"a listed node with no address", &fakeStatusLister{resp: listed}, "pve2", "without an address"},
		{"an empty list", &fakeStatusLister{resp: statusResponse()}, "pve1", "lists no nodes"},
		{"a nil response", &fakeStatusLister{}, "pve1", "lists no nodes"},
		{"a failed lookup", &fakeStatusLister{err: errors.New("401 unauthorized")}, "pve1", "401 unauthorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := nodeaddr.ResolveStrict(context.Background(), tc.svc, tc.node)
			require.Error(t, err)
			require.Empty(t, got, "no fallback to the node name")
			require.ErrorIs(t, err, nodeaddr.ErrNodeNotMatched)
			require.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("a nil context and an empty name are refused", func(t *testing.T) {
		t.Parallel()

		//nolint:staticcheck // SA1012: the nil context is the case under test
		_, err := nodeaddr.ResolveStrict(nil, &fakeStatusLister{resp: listed}, "pve1")
		require.Error(t, err)

		_, err = nodeaddr.ResolveStrict(context.Background(), &fakeStatusLister{resp: listed}, "")
		require.Error(t, err)
	})
}
