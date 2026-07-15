package pool

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestPoolCreate_AuditAllFlags verifies every `pool create` flag reaches the
// POST /pools body together in one request, proving they compose without
// clobbering each other. Individual flags are also exercised in pool_test.go.
func TestPoolCreate_AuditAllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "POST /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "create", "--poolid", "audit-pool", "--comment", "audit comment")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "audit-pool", rec[0].body["poolid"])
	require.Equal(t, "audit comment", rec[0].body["comment"])
}

// TestPoolCreate_OmitsUnsetComment verifies --comment, `create`'s only
// optional flag, is left out of the POST body entirely when not supplied.
func TestPoolCreate_OmitsUnsetComment(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "POST /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "create", "--poolid", "bare-pool")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "bare-pool", rec[0].body["poolid"])
	_, present := rec[0].body["comment"]
	require.False(t, present, "comment must be omitted from the body when --comment is not set")
}

// TestPoolSet_AuditAllFlags verifies every `pool set` flag (comment, vms,
// storage, delete, allow-move) reaches the PUT /pools body together in one
// request, using the exact API-side keys (allow-move keeps its hyphen).
func TestPoolSet_AuditAllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "set", "audit-pool",
		"--comment", "audit",
		"--vms", "100,101",
		"--storage", "local,local-lvm",
		"--delete",
		"--allow-move",
	)
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "audit-pool", rec[0].body["poolid"])
	require.Equal(t, "audit", rec[0].body["comment"])
	require.Equal(t, "100,101", rec[0].body["vms"])
	require.Equal(t, "local,local-lvm", rec[0].body["storage"])
	require.Equal(t, "1", rec[0].body["delete"])
	require.Equal(t, "1", rec[0].body["allow-move"])
}

// TestPoolSet_OmitsUnsetFlags verifies that when only the required <poolid>
// argument is given, none of `set`'s optional flags leak into the PUT body.
func TestPoolSet_OmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "set", "audit-pool")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "audit-pool", rec[0].body["poolid"])
	for _, key := range []string{"comment", "vms", "storage", "delete", "allow-move"} {
		_, present := rec[0].body[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

// TestPoolDelete_NoWriteFlags documents that `pool delete` registers no
// command-specific flag that reaches the API: --yes only gates the local
// confirmation prompt, and --destroy-vms/--destroy-storage are rejected
// before any request is sent (see TestPoolDeleteRejectsDestroyFlags in
// pool_test.go) because the API has no member-destruction parameter. The only
// field on the wire is poolid, carried as a query parameter (DELETE/GET
// requests are query-encoded by the client, not form-bodied).
func TestPoolDelete_NoWriteFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "DELETE /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "delete", "audit-pool", "--yes")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
	require.Equal(t, "poolid=audit-pool", rec[0].query, "delete query must carry only poolid; no other flag reaches the wire")
}

// TestPoolList_AuditQueryFlags verifies `pool list`'s --type and --poolid
// flags are forwarded together as GET /pools query parameters. Each is
// exercised individually elsewhere in pool_test.go; this proves they compose
// in a single request.
func TestPoolList_AuditQueryFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", []any{}, 200)

	_, err := run(t, f, "", "list", "--type", "qemu", "--poolid", "prod")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Contains(t, rec[0].query, "type=qemu")
	require.Contains(t, rec[0].query, "poolid=prod")
}

// TestPoolGet_TypeFilter verifies `pool get`'s --type flag is forwarded as a
// query parameter alongside poolid on GET /pools?poolid=<id>. This flag is not
// covered elsewhere in the package.
func TestPoolGet_TypeFilter(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", []any{
		map[string]any{"poolid": "prod", "comment": "production", "members": []any{}},
	}, 200)

	_, err := run(t, f, "", "get", "prod", "--type", "lxc")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Contains(t, rec[0].query, "poolid=prod")
	require.Contains(t, rec[0].query, "type=lxc")
}

// TestPool_AuditCommandTree verifies every documented pool sub-command is
// registered: list, get, create, set, delete.
func TestPool_AuditCommandTree(t *testing.T) {
	names := make(map[string]bool)
	for _, c := range Group(nil).Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"list", "get", "create", "set", "delete"} {
		require.True(t, names[want], "expected pool sub-command %q to be registered", want)
	}
}
