package sdn

import (
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// --- prefix-list ---

func TestPrefixListList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/prefix-lists", []any{
		map[string]any{"id": "pl1"},
		map[string]any{"id": "pl2"},
	}, 200)

	out, err := run(t, f, "", "prefix-list", "list")
	require.NoError(t, err)
	require.Contains(t, out, "pl1")
	require.Contains(t, out, "pl2")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/prefix-lists", rec[0].path)
}

func TestPrefixListListError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/prefix-lists", nil, 500)

	_, err := run(t, f, "", "prefix-list", "list")
	require.Error(t, err)
	require.ErrorContains(t, err, "list SDN prefix lists")
}

func TestPrefixListCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/prefix-lists", map[string]any{}, 200)

	out, err := run(t, f, "", "prefix-list", "create", "pl1", "--entry", "permit 10.0.0.0/8")
	require.NoError(t, err)
	require.Contains(t, out, "created")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "pl1", rec[0].body["id"])
}

// TestPrefixListCreateError verifies the create error wrap.
func TestPrefixListCreateError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/prefix-lists", nil, 500)

	_, err := run(t, f, "", "prefix-list", "create", "pl1")
	require.Error(t, err)
	require.ErrorContains(t, err, "create SDN prefix list \"pl1\"")
}

func TestPrefixListGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/prefix-lists/pl1", map[string]any{
		"id": "pl1", "entries": []any{"permit 10.0.0.0/8"},
	}, 200)

	out, err := run(t, f, "", "prefix-list", "get", "pl1")
	require.NoError(t, err)
	require.Contains(t, out, "pl1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/prefix-lists/pl1", rec[0].path)
}

func TestPrefixListSetRequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/prefix-lists/pl1", map[string]any{}, 200)

	_, err := run(t, f, "", "prefix-list", "set", "pl1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested")
	require.Empty(t, rec)
}

func TestPrefixListSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/prefix-lists/pl1", map[string]any{}, 200)

	out, err := run(t, f, "", "prefix-list", "set", "pl1", "--entry", "deny 0.0.0.0/0")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
}

func TestPrefixListDeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/prefix-lists/pl1", map[string]any{}, 200)

	_, err := run(t, f, "", "prefix-list", "delete", "pl1")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestPrefixListDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/prefix-lists/pl1", map[string]any{}, 200)

	out, err := run(t, f, "", "prefix-list", "delete", "pl1", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

// --- prefix-list entries ---

func TestPrefixListEntryList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/prefix-lists/pl1/entries", []any{
		map[string]any{"seq": 10, "action": "permit", "prefix": "10.0.0.0/8"},
	}, 200)

	out, err := run(t, f, "", "prefix-list", "entry", "list", "pl1")
	require.NoError(t, err)
	require.Contains(t, out, "permit")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/prefix-lists/pl1/entries", rec[0].path)
}

func TestPrefixListEntryAdd(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/prefix-lists/pl1/entries", map[string]any{}, 200)

	out, err := run(t, f, "", "prefix-list", "entry", "add", "pl1",
		"--action", "permit", "--prefix", "10.0.0.0/8", "--ge", "16", "--seq", "10")
	require.NoError(t, err)
	require.Contains(t, out, "added")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "permit", rec[0].body["action"])
	require.Equal(t, "10.0.0.0/8", rec[0].body["prefix"])
	require.Equal(t, "16", rec[0].body["ge"])
	require.Equal(t, "10", rec[0].body["seq"])
	require.NotContains(t, rec[0].body, "le")
}

func TestPrefixListEntryAddRequiresActionAndPrefix(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/prefix-lists/pl1/entries", map[string]any{}, 200)

	_, err := run(t, f, "", "prefix-list", "entry", "add", "pl1", "--action", "permit")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestPrefixListEntryGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/prefix-lists/pl1/entries/10", map[string]any{
		"seq": 10, "action": "permit", "prefix": "10.0.0.0/8",
	}, 200)

	out, err := run(t, f, "", "prefix-list", "entry", "get", "pl1", "10")
	require.NoError(t, err)
	require.Contains(t, out, "permit")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/prefix-lists/pl1/entries/10", rec[0].path)
}

func TestPrefixListEntrySetRequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/prefix-lists/pl1/entries/10", map[string]any{}, 200)

	_, err := run(t, f, "", "prefix-list", "entry", "set", "pl1", "10")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested")
	require.Empty(t, rec)
}

func TestPrefixListEntrySet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/prefix-lists/pl1/entries/10", map[string]any{}, 200)

	out, err := run(t, f, "", "prefix-list", "entry", "set", "pl1", "10", "--action", "deny")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, "deny", rec[0].body["action"])
}

func TestPrefixListEntryDeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/prefix-lists/pl1/entries/10", map[string]any{}, 200)

	_, err := run(t, f, "", "prefix-list", "entry", "delete", "pl1", "10")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

// --- route-map ---

func TestRouteMapList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/route-maps", []any{
		map[string]any{"id": "rm1"},
	}, 200)

	out, err := run(t, f, "", "route-map", "list")
	require.NoError(t, err)
	require.Contains(t, out, "rm1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/route-maps", rec[0].path)
}

func TestRouteMapGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/route-maps/entries/rm1", []any{
		map[string]any{"order": 10, "action": "permit"},
	}, 200)

	out, err := run(t, f, "", "route-map", "get", "rm1")
	require.NoError(t, err)
	require.Contains(t, out, "permit")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/route-maps/entries/rm1", rec[0].path)
}

func TestRouteMapEntryList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/route-maps/entries", []any{
		map[string]any{"route-map-id": "rm1", "order": 10, "action": "permit"},
	}, 200)

	out, err := run(t, f, "", "route-map", "entry", "list")
	require.NoError(t, err)
	require.Contains(t, out, "rm1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/route-maps/entries", rec[0].path)
}

func TestRouteMapEntryAdd(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/route-maps/entries", map[string]any{}, 200)

	out, err := run(t, f, "", "route-map", "entry", "add", "rm1",
		"--order", "10", "--action", "permit", "--match", "ip address prefix-list pl1")
	require.NoError(t, err)
	require.Contains(t, out, "added")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "rm1", rec[0].body["route-map-id"])
	require.Equal(t, "10", rec[0].body["order"])
	require.Equal(t, "permit", rec[0].body["action"])
}

func TestRouteMapEntryAddRequiresOrderAndAction(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/route-maps/entries", map[string]any{}, 200)

	_, err := run(t, f, "", "route-map", "entry", "add", "rm1", "--order", "10")
	require.Error(t, err)
	require.Empty(t, rec)
}

func TestRouteMapEntryGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/route-maps/entries/rm1/entry/10", map[string]any{
		"order": 10, "action": "permit",
	}, 200)

	out, err := run(t, f, "", "route-map", "entry", "get", "rm1", "10")
	require.NoError(t, err)
	require.Contains(t, out, "permit")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/route-maps/entries/rm1/entry/10", rec[0].path)
}

func TestRouteMapEntrySetRequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/route-maps/entries/rm1/entry/10", map[string]any{}, 200)

	_, err := run(t, f, "", "route-map", "entry", "set", "rm1", "10")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested")
	require.Empty(t, rec)
}

func TestRouteMapEntrySet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/route-maps/entries/rm1/entry/10", map[string]any{}, 200)

	out, err := run(t, f, "", "route-map", "entry", "set", "rm1", "10", "--action", "deny")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, "deny", rec[0].body["action"])
	require.NotContains(t, rec[0].body, "call", "unset optional must be omitted from the update body")
	require.NotContains(t, rec[0].body, "exit-action", "unset optional must be omitted from the update body")
}

// TestRouteMapEntrySetForwardsClauses verifies the scalar policy clauses are
// forwarded when set.
func TestRouteMapEntrySetForwardsClauses(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/route-maps/entries/rm1/entry/10", map[string]any{}, 200)

	out, err := run(t, f, "", "route-map", "entry", "set", "rm1", "10",
		"--call", "rm2", "--exit-action", "next")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, "rm2", rec[0].body["call"])
	require.Equal(t, "next", rec[0].body["exit-action"])
}

func TestRouteMapEntryDeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/route-maps/entries/rm1/entry/10", map[string]any{}, 200)

	_, err := run(t, f, "", "route-map", "entry", "delete", "rm1", "10")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

// TestPolicyCommandTree verifies the prefix-list and route-map sub-trees are wired.
func TestPolicyCommandTree(t *testing.T) {
	cmd := Group(nil)
	groups := map[string]*cobra.Command{}
	for _, c := range cmd.Commands() {
		groups[c.Name()] = c
	}
	pl := groups["prefix-list"]
	require.NotNil(t, pl, "sdn is missing \"prefix-list\"")
	rm := groups["route-map"]
	require.NotNil(t, rm, "sdn is missing \"route-map\"")

	plSub := childNames(pl)
	for _, want := range []string{"list", "create", "get", "set", "delete", "entry"} {
		require.True(t, plSub[want], "prefix-list is missing %q", want)
	}
	rmSub := childNames(rm)
	for _, want := range []string{"list", "get", "entry"} {
		require.True(t, rmSub[want], "route-map is missing %q", want)
	}
}

func childNames(cmd *cobra.Command) map[string]bool {
	out := map[string]bool{}
	for _, c := range cmd.Commands() {
		out[c.Name()] = true
	}
	return out
}
