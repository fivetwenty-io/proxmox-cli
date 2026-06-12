package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- dry-run ---

func TestDryRun_RendersDiff(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/dry-run", map[string]any{
		"frr-diff":        "+ router bgp 65000",
		"interfaces-diff": "+ auto vnet0",
	}, 200)

	out, err := run(t, f, "", "dry-run", "--node", "pve")
	require.NoError(t, err)
	require.Contains(t, out, "router bgp 65000")
	require.Contains(t, out, "auto vnet0")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/dry-run", rec[0].path)
}

// TestDryRun_RequiresNode verifies the command refuses to run without a node and
// issues no request.
func TestDryRun_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/dry-run", map[string]any{}, 200)

	_, err := run(t, f, "", "dry-run")
	require.Error(t, err)
	require.ErrorContains(t, err, "no node specified")
	require.Empty(t, rec, "no request must be issued without a node")
}

func TestDryRun_Error(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/dry-run", nil, 500)

	_, err := run(t, f, "", "dry-run", "--node", "pve")
	require.Error(t, err)
	require.ErrorContains(t, err, "preview SDN configuration on node \"pve\"")
}

// --- rollback ---

func TestRollback_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/rollback", map[string]any{}, 200)

	_, err := run(t, f, "", "rollback")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec, "no request must be issued without --yes")
}

func TestRollback_Succeeds(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/rollback", map[string]any{}, 200)

	out, err := run(t, f, "", "rollback", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "discarded")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/rollback", rec[0].path)
	require.NotContains(t, rec[0].body, "lock-token")
	require.NotContains(t, rec[0].body, "release-lock")
}

// TestRollback_ForwardsFlags verifies the optional lock parameters are sent when
// set on the command line.
func TestRollback_ForwardsFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/rollback", map[string]any{}, 200)

	_, err := run(t, f, "", "rollback", "--yes",
		"--lock-token", "abc123", "--release-lock")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "abc123", rec[0].body["lock-token"])
	require.Equal(t, "1", rec[0].body["release-lock"])
}

func TestRollback_Error(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/rollback", nil, 400)

	_, err := run(t, f, "", "rollback", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "roll back SDN configuration")
}

// TestSdnDryRunRollbackCommandTree verifies both leaf commands are registered on
// the sdn group.
func TestSdnDryRunRollbackCommandTree(t *testing.T) {
	cmd := Group(nil)
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"dry-run", "rollback"} {
		require.True(t, got[want], "sdn is missing %q", want)
	}
}
