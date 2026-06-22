package cli_test

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// newGuestFakeClient returns a FakePVE and an APIClient pointing at it, mirroring
// the per-group test setup so the cli package can exercise ResolveGuest against a
// real cluster/resources endpoint.
func newGuestFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()
	f := testhelper.NewFakePVE(t)

	u, err := url.Parse(f.BaseURL())
	require.NoError(t, err)
	host, portStr, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	opts := f.Options
	opts.Host = host
	opts.Port = port

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return f, ac
}

// guestResources registers a cluster/resources response describing the given vm
// guests and reports whether the endpoint was hit.
func guestResources(f *testhelper.FakePVE, entries []map[string]any) *bool {
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

func TestResolveGuest_NumericFastPath(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	hit := guestResources(f, nil)
	deps := &cli.Deps{API: ac, Node: "pve1"}

	vmid, node, err := cli.ResolveGuest(context.Background(), deps, "100", cli.GuestQemu)
	require.NoError(t, err)
	require.Equal(t, "100", vmid)
	require.Equal(t, "pve1", node)
	require.False(t, *hit, "numeric target with a known node must not query cluster resources")
}

func TestResolveGuest_NameResolvesVMIDAndNode(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	guestResources(f, []map[string]any{
		{"type": "qemu", "vmid": 100, "name": "peppi-cp", "node": "pve1"},
		{"type": "lxc", "vmid": 200, "name": "other", "node": "pve2"},
	})
	deps := &cli.Deps{API: ac}

	vmid, node, err := cli.ResolveGuest(context.Background(), deps, "peppi-cp", cli.GuestQemu)
	require.NoError(t, err)
	require.Equal(t, "100", vmid)
	require.Equal(t, "pve1", node)
}

func TestResolveGuest_NumericWithoutNodeResolvesNode(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	guestResources(f, []map[string]any{
		{"type": "qemu", "vmid": 100, "name": "web", "node": "pve3"},
	})
	deps := &cli.Deps{API: ac}

	vmid, node, err := cli.ResolveGuest(context.Background(), deps, "100", cli.GuestQemu)
	require.NoError(t, err)
	require.Equal(t, "100", vmid)
	require.Equal(t, "pve3", node)
}

func TestResolveGuest_NameFiltersByNode(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	guestResources(f, []map[string]any{
		{"type": "qemu", "vmid": 100, "name": "dup", "node": "pve1"},
		{"type": "qemu", "vmid": 101, "name": "dup", "node": "pve2"},
	})
	deps := &cli.Deps{API: ac, Node: "pve2"}

	vmid, node, err := cli.ResolveGuest(context.Background(), deps, "dup", cli.GuestQemu)
	require.NoError(t, err)
	require.Equal(t, "101", vmid)
	require.Equal(t, "pve2", node)
}

func TestResolveGuest_NotFound(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	guestResources(f, []map[string]any{
		{"type": "qemu", "vmid": 100, "name": "web", "node": "pve1"},
	})
	deps := &cli.Deps{API: ac}

	_, _, err := cli.ResolveGuest(context.Background(), deps, "missing", cli.GuestQemu)
	require.Error(t, err)
	require.ErrorContains(t, err, "qemu guest \"missing\" not found")
}

// TestResolveGuest_WrongType verifies a guest of the other type is not matched by
// name (an lxc named like the qemu target is ignored when resolving qemu).
func TestResolveGuest_WrongType(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	guestResources(f, []map[string]any{
		{"type": "lxc", "vmid": 100, "name": "ct", "node": "pve1"},
	})
	deps := &cli.Deps{API: ac}

	_, _, err := cli.ResolveGuest(context.Background(), deps, "ct", cli.GuestQemu)
	require.Error(t, err)
	require.ErrorContains(t, err, "not found")
}

func TestResolveGuest_DuplicateNameAcrossNodes(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	guestResources(f, []map[string]any{
		{"type": "qemu", "vmid": 100, "name": "dup", "node": "pve1"},
		{"type": "qemu", "vmid": 101, "name": "dup", "node": "pve2"},
	})
	deps := &cli.Deps{API: ac}

	_, _, err := cli.ResolveGuest(context.Background(), deps, "dup", cli.GuestQemu)
	require.Error(t, err)
	require.ErrorContains(t, err, "ambiguous")
	require.ErrorContains(t, err, "pve1")
	require.ErrorContains(t, err, "pve2")
}

// TestResolveGuest_VMIDFromIDSuffix verifies the VMID is derived from the id
// field (e.g. "qemu/100") when the entry omits an explicit vmid.
func TestResolveGuest_VMIDFromIDSuffix(t *testing.T) {
	f, ac := newGuestFakeClient(t)
	guestResources(f, []map[string]any{
		{"type": "qemu", "id": "qemu/100", "name": "web", "node": "pve1"},
	})
	deps := &cli.Deps{API: ac}

	vmid, node, err := cli.ResolveGuest(context.Background(), deps, "web", cli.GuestQemu)
	require.NoError(t, err)
	require.Equal(t, "100", vmid)
	require.Equal(t, "pve1", node)
}
