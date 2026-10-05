package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// recordHealthMutePut answers PUT /cluster/ceph/health-mute/{code} and records
// the form, so a test can assert which fields were sent.
func recordHealthMutePut(f *testhelper.FakePVE, code string, form *url.Values, called *bool) {
	f.HandleFunc("PUT /api2/json/cluster/ceph/health-mute/"+code, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		*form = r.Form
		*called = true
		testhelper.WriteData(w, nil)
	})
}

// TestCephHealthMute_List verifies list reads GET /cluster/ceph/health-mute
// and renders one row per muted check.
func TestCephHealthMute_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ceph/health-mute", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{
				"code": "POOL_NO_REDUNDANCY", "sticky": 1, "ttl": "2026-10-06T12:00:00+0000",
				"summary": "1 pool has no replicas",
			},
			map[string]any{"code": "OSD_DOWN", "sticky": 0},
		})
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "health-mute", "list"))
	out := buf.String()
	require.Contains(t, out, "CODE")
	require.Contains(t, out, "EXPIRES")
	require.Contains(t, out, "2026-10-06T12:00:00+0000")
	require.Contains(t, out, "POOL_NO_REDUNDANCY")
	require.Contains(t, out, "OSD_DOWN")
	require.Contains(t, out, "1 pool has no replicas")
}

// TestCephHealthMute_ListEmpty verifies a cluster with nothing muted renders
// the headers rather than failing.
func TestCephHealthMute_ListEmpty(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ceph/health-mute", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{})
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "health-mute", "list"))
	require.Contains(t, buf.String(), "CODE")
}

// TestCephHealthMute_ListWrapsServerError verifies a server error, such as a
// cluster without Ceph, comes back wrapped and verbatim.
func TestCephHealthMute_ListWrapsServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ceph/health-mute", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "ceph is not initialized")
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "health-mute", "list")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list ceph health mutes")
	require.Contains(t, err.Error(), "ceph is not initialized")
}

// TestCephHealthMute_CreateSendsTtlAndSticky verifies create issues a PUT with
// value=1 and the flags the operator passed.
func TestCephHealthMute_CreateSendsTtlAndSticky(t *testing.T) {
	f, ac := newFakeClient(t)
	var form url.Values
	var called bool
	recordHealthMutePut(f, "POOL_NO_REDUNDANCY", &form, &called)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "health-mute", "create", "POOL_NO_REDUNDANCY",
		"--ttl", "2h", "--sticky"))
	require.True(t, called)
	require.Equal(t, "1", form.Get("value"))
	require.Equal(t, "2h", form.Get("ttl"))
	require.Equal(t, "1", form.Get("sticky"))
	require.Contains(t, buf.String(), "ceph health check POOL_NO_REDUNDANCY muted (ttl 2h, sticky).")
}

// TestCephHealthMute_CreateRejectsEmptyTTL verifies an explicitly empty --ttl
// fails before any request goes out.
func TestCephHealthMute_CreateRejectsEmptyTTL(t *testing.T) {
	for _, ttl := range []string{"", "  "} {
		f, ac := newFakeClient(t)
		var form url.Values
		var called bool
		recordHealthMutePut(f, "POOL_NO_REDUNDANCY", &form, &called)
		deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

		var buf bytes.Buffer
		err := run(deps, &buf, "ceph", "health-mute", "create", "POOL_NO_REDUNDANCY", "--ttl", ttl)
		require.Error(t, err, "ttl %q", ttl)
		require.Contains(t, err.Error(), "invalid --ttl")
		require.False(t, called, "no request may be sent for ttl %q", ttl)
	}
}

// TestCephHealthMute_CreateOmitsUnsetFlags verifies ttl and sticky stay out of
// the form when the operator did not pass them.
func TestCephHealthMute_CreateOmitsUnsetFlags(t *testing.T) {
	f, ac := newFakeClient(t)
	var form url.Values
	var called bool
	recordHealthMutePut(f, "OSD_DOWN", &form, &called)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "health-mute", "create", "OSD_DOWN"))
	require.True(t, called)
	require.Equal(t, "1", form.Get("value"))
	require.False(t, form.Has("ttl"), "ttl must be absent when --ttl is not passed")
	require.False(t, form.Has("sticky"), "sticky must be absent when --sticky is not passed")
	require.Contains(t, buf.String(), "ceph health check OSD_DOWN muted.")
}

// TestCephHealthMute_DeleteSendsValueZero verifies delete issues a PUT with
// value=0 and nothing else.
func TestCephHealthMute_DeleteSendsValueZero(t *testing.T) {
	f, ac := newFakeClient(t)
	var form url.Values
	var called bool
	recordHealthMutePut(f, "OSD_DOWN", &form, &called)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "health-mute", "delete", "OSD_DOWN"))
	require.True(t, called)
	require.Equal(t, "0", form.Get("value"))
	require.False(t, form.Has("ttl"))
	require.False(t, form.Has("sticky"))
	require.Contains(t, buf.String(), "ceph health check OSD_DOWN unmuted.")
}

// TestCephHealthMute_RejectsMalformedCode verifies a lowercase or malformed
// code is refused before any request goes out.
func TestCephHealthMute_RejectsMalformedCode(t *testing.T) {
	long := "A"
	for len(long) < 65 {
		long += "B"
	}
	for _, verb := range []string{"create", "delete"} {
		for _, code := range []string{"osd_down", "1OSD", "OSD DOWN", "A", "OSD-DOWN", long} {
			f, ac := newFakeClient(t)
			called := false
			f.HandleFunc("PUT /api2/json/cluster/ceph/health-mute/", func(w http.ResponseWriter, _ *http.Request) {
				called = true
				testhelper.WriteData(w, nil)
			})
			deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

			var buf bytes.Buffer
			err := run(deps, &buf, "ceph", "health-mute", verb, code)
			require.Errorf(t, err, "%s %q", verb, code)
			require.Contains(t, err.Error(), "invalid ceph health check code")
			require.Falsef(t, called, "%s %q must not issue a request", verb, code)
		}
	}
}

// TestCephHealthMute_RequiresExactlyOneCode verifies a missing or extra code
// is a usage error.
func TestCephHealthMute_RequiresExactlyOneCode(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	for _, verb := range []string{"create", "delete"} {
		var buf bytes.Buffer
		require.Errorf(t, run(deps, &buf, "ceph", "health-mute", verb), "%s without a code", verb)
		buf.Reset()
		require.Errorf(t, run(deps, &buf, "ceph", "health-mute", verb, "OSD_DOWN", "MON_DOWN"),
			"%s with two codes", verb)
	}
}

// TestCephHealthMute_WrapsServerError verifies a failed mute names the code
// and carries the server's message.
func TestCephHealthMute_WrapsServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/cluster/ceph/health-mute/OSD_DOWN", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "ceph is not initialized")
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "health-mute", "create", "OSD_DOWN")
	require.Error(t, err)
	require.Contains(t, err.Error(), `mute ceph health check "OSD_DOWN"`)
	require.Contains(t, err.Error(), "ceph is not initialized")

	buf.Reset()
	err = run(deps, &buf, "ceph", "health-mute", "delete", "OSD_DOWN")
	require.Error(t, err)
	require.Contains(t, err.Error(), `unmute ceph health check "OSD_DOWN"`)
}

// TestCephHealthMute_VerbAliases verifies the native mute and unmute spellings
// and the generic add and rm verbs resolve to the commands they stand for.
func TestCephHealthMute_VerbAliases(t *testing.T) {
	root := Group(&cli.Deps{})
	cli.NormalizeAliases(root)
	mute, _, err := root.Find([]string{"ceph", "health-mute"})
	require.NoError(t, err)
	require.Equal(t, "health-mute", mute.Name())

	for alias, want := range map[string]string{
		"mute": "create", "add": "create", "unmute": "delete", "rm": "delete", "ls": "list",
	} {
		got, _, err := mute.Find([]string{alias})
		require.NoErrorf(t, err, "alias %s", alias)
		require.Equalf(t, want, got.Name(), "alias %s", alias)
	}
}

// TestCephHealthMute_HelpStatesPermissions verifies the help names the
// permissions each verb needs and does not promise an async mode that these
// synchronous verbs lack.
func TestCephHealthMute_HelpStatesPermissions(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}
	for verb, want := range map[string]string{
		"list": "Sys.Audit", "create": "Sys.Modify", "delete": "Sys.Modify",
	} {
		var buf bytes.Buffer
		require.NoError(t, run(deps, &buf, "ceph", "health-mute", verb, "--help"))
		require.Containsf(t, buf.String(), want, "%s help", verb)
		require.NotContainsf(t, buf.String(), "--async", "%s help", verb)
	}
}
