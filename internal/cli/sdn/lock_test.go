package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// --- lock acquire ---

func TestLockAcquire(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/lock", "tok-abc123", 200)

	out, err := run(t, f, "", "lock", "acquire")
	require.NoError(t, err)
	require.Contains(t, out, "acquired")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/lock", rec[0].path)
}

func TestLockAcquireAllowPending(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/lock", "tok-abc123", 200)

	_, err := run(t, f, "", "lock", "acquire", "--allow-pending")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].body["allow-pending"])
}

// TestLockAcquireOmitsAllowPending verifies allow-pending is sent only when
// explicitly set.
func TestLockAcquireOmitsAllowPending(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/lock", "tok-abc123", 200)

	_, err := run(t, f, "", "lock", "acquire")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.NotContains(t, rec[0].body, "allow-pending", "unset flag must not be sent")
}

func TestLockAcquireError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/lock", nil, 500)

	_, err := run(t, f, "", "lock", "acquire")
	require.Error(t, err)
	require.ErrorContains(t, err, "acquire SDN lock")
}

// --- lock release ---

func TestLockReleaseRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/lock", map[string]any{}, 200)

	_, err := run(t, f, "", "lock", "release", "--lock-token", "tok-abc123")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestLockRelease(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/lock", map[string]any{}, 200)

	out, err := run(t, f, "", "lock", "release", "--lock-token", "tok-abc123", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "released")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/lock", rec[0].path)
	// DELETE params are sent as query string; body map is empty per the test helper.
}

func TestLockReleaseForce(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/lock", map[string]any{}, 200)

	_, err := run(t, f, "", "lock", "release", "--force", "--yes")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	// DELETE params are sent as query string; body map is empty per the test helper.
}

// TestLockReleaseOmitsToken verifies the command succeeds without --lock-token;
// DELETE params are query string, so no body assertion is needed here.
func TestLockReleaseOmitsToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/lock", map[string]any{}, 200)

	_, err := run(t, f, "", "lock", "release", "--yes")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

func TestLockReleaseError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/lock", nil, 500)

	_, err := run(t, f, "", "lock", "release", "--lock-token", "tok-abc123", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "release SDN lock")
}

// TestLockCommandTree verifies acquire and release are wired under lock.
func TestLockCommandTree(t *testing.T) {
	cmd := newLockCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"acquire", "release"} {
		require.True(t, got[want], "lock is missing %q", want)
	}
}

// TestSdnGroupIncludesLock verifies lock is registered at the sdn group level.
func TestSdnGroupIncludesLock(t *testing.T) {
	cmd := Group(nil)
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	require.True(t, got["lock"], "sdn is missing \"lock\"")
}
