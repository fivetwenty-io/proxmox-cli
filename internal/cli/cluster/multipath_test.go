package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

var sanMultipathFormats = []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML}

// recordMultipath answers pattern with payload and records the method, path, and
// merged query and form values.
func recordMultipath(f *testhelper.FakePVE, pattern string, method *string, path *string, form *url.Values,
	payload any,
) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		*method, *path, *form = r.Method, r.URL.Path, r.Form
		testhelper.WriteData(w, payload)
	})
}

// TestClusterSan_Reads verifies the cluster SAN reads hit their endpoints and
// render in every output format.
func TestClusterSan_Reads(t *testing.T) {
	cases := []struct {
		args    []string
		path    string
		payload any
		want    string
	}{
		{[]string{"san", "list"}, "/api2/json/cluster/san", []any{map[string]any{"name": "status"}}, "status"},
		{[]string{"san", "status"}, "/api2/json/cluster/san/status",
			map[string]any{
				"luns":  []any{map[string]any{"wwid": "3600a0980383030", "state": "ok"}},
				"nodes": map[string]any{"pve1": map[string]any{"luns": 1}},
			}, "3600a0980383030"},
		{[]string{"multipath", "list"}, "/api2/json/cluster/multipath",
			[]any{map[string]any{"name": "wwid"}}, "wwid"},
		{[]string{"multipath", "wwid", "list"}, "/api2/json/cluster/multipath/wwid",
			[]any{map[string]any{"wwid": "3600a0980383030", "alias": "data1"}}, "data1"},
		{[]string{"multipath", "wwid", "show", "3600a0980383030"}, "/api2/json/cluster/multipath/wwid/3600a0980383030",
			map[string]any{"wwid": "3600a0980383030", "alias": "data1", "paths-expected": 4, "digest": "abc"}, "data1"},
		{[]string{"multipath", "override", "show"}, "/api2/json/cluster/multipath/overrides",
			map[string]any{"digest": "abc", "overrides": "devices {}"}, "devices {}"},
	}
	for _, tc := range cases {
		for _, format := range sanMultipathFormats {
			t.Run(tc.path+"/"+string(format), func(t *testing.T) {
				f, ac := newFakeClient(t)
				var method, path string
				var form url.Values
				recordMultipath(f, "GET "+tc.path, &method, &path, &form, tc.payload)
				deps := &cli.Deps{API: ac, Out: output.New(), Format: format}

				var buf bytes.Buffer
				require.NoError(t, run(deps, &buf, tc.args...))
				require.Equal(t, http.MethodGet, method)
				require.Equal(t, tc.path, path)
				require.Contains(t, buf.String(), tc.want)
			})
		}
	}
}

// TestClusterSan_ReadWrapsServerError verifies a refusal is wrapped.
func TestClusterSan_ReadWrapsServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/san/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "Permission check failed")
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "san", "status")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read cluster SAN status")
}

// TestMultipathWwid_Create verifies create sends the wwid and every setting.
func TestMultipathWwid_Create(t *testing.T) {
	for _, format := range sanMultipathFormats {
		t.Run(string(format), func(t *testing.T) {
			f, ac := newFakeClient(t)
			var method, path string
			var form url.Values
			recordMultipath(f, "POST /api2/json/cluster/multipath/wwid", &method, &path, &form, nil)
			deps := &cli.Deps{API: ac, Out: output.New(), Format: format}

			var buf bytes.Buffer
			require.NoError(t, run(deps, &buf, "multipath", "wwid", "create", "3600a0980383030",
				"--alias", "data1", "--digest", "abc", "--no-path-retry", "queue", "--nodes", "pve1,pve2",
				"--paths-expected", "4"))
			require.Equal(t, http.MethodPost, method)
			require.Equal(t, "3600a0980383030", form.Get("wwid"))
			require.Equal(t, "data1", form.Get("alias"))
			require.Equal(t, "abc", form.Get("digest"))
			require.Equal(t, "queue", form.Get("no-path-retry"))
			require.Equal(t, "pve1,pve2", form.Get("nodes"))
			require.Equal(t, "4", form.Get("paths-expected"))
			require.Contains(t, buf.String(), "created")
		})
	}
}

// TestMultipathWwid_Set verifies set sends only what was given, and refuses an
// empty change.
func TestMultipathWwid_Set(t *testing.T) {
	f, ac := newFakeClient(t)
	var method, path string
	var form url.Values
	recordMultipath(f, "PUT /api2/json/cluster/multipath/wwid/3600a0980383030", &method, &path, &form, nil)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "multipath", "wwid", "set", "3600a0980383030",
		"--paths-expected", "2", "--delete", "nodes"))
	require.Equal(t, http.MethodPut, method)
	require.Equal(t, "2", form.Get("paths-expected"))
	require.Equal(t, "nodes", form.Get("delete"))
	require.False(t, form.Has("alias"))
	require.Contains(t, buf.String(), "updated")

	err := run(deps, &buf, "multipath", "wwid", "set", "3600a0980383030")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing to change")
}

// TestMultipathWwid_DeleteWithYes verifies the delete passes the digest.
func TestMultipathWwid_DeleteWithYes(t *testing.T) {
	for _, format := range sanMultipathFormats {
		t.Run(string(format), func(t *testing.T) {
			f, ac := newFakeClient(t)
			var method, path string
			var form url.Values
			recordMultipath(f, "DELETE /api2/json/cluster/multipath/wwid/3600a0980383030", &method, &path, &form, nil)
			deps := &cli.Deps{API: ac, Out: output.New(), Format: format}

			var buf bytes.Buffer
			require.NoError(t, run(deps, &buf, "multipath", "wwid", "delete", "3600a0980383030",
				"--digest", "abc", "--yes"))
			require.Equal(t, http.MethodDelete, method)
			require.Equal(t, "abc", form.Get("digest"))
			require.Contains(t, buf.String(), "deleted")
		})
	}
}

// TestMultipathWwid_DeleteRefusesWithoutYes verifies nothing is sent without
// confirmation.
func TestMultipathWwid_DeleteRefusesWithoutYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/multipath/wwid/3600a0980383030", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "multipath", "wwid", "delete", "3600a0980383030")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// TestMultipathOverride_Set verifies set sends text from --overrides, --file,
// and --clear, and demands exactly one of them.
func TestMultipathOverride_Set(t *testing.T) {
	file := filepath.Join(t.TempDir(), "overrides.conf")
	require.NoError(t, os.WriteFile(file, []byte("devices {\n}\n"), 0o600))

	cases := []struct {
		name     string
		args     []string
		wantText string
		hasText  bool
	}{
		{"inline", []string{"--overrides", "defaults {}", "--yes"}, "defaults {}", true},
		{"file", []string{"--file", file, "--digest", "abc"}, "devices {\n}\n", true},
		{"clear", []string{"--clear", "--digest", "abc", "--yes"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ac := newFakeClient(t)
			var method, path string
			var form url.Values
			recordMultipath(f, "PUT /api2/json/cluster/multipath/overrides", &method, &path, &form, nil)
			deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

			var buf bytes.Buffer
			require.NoError(t, run(deps, &buf, append([]string{"multipath", "override", "set"}, tc.args...)...))
			require.Equal(t, http.MethodPut, method)
			require.Equal(t, tc.hasText, form.Has("overrides"))
			require.Equal(t, tc.wantText, form.Get("overrides"))
		})
	}

	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	err := run(deps, &buf, "multipath", "override", "set")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one")
}

// TestMultipathOverride_SetClearFalseSendsNothing verifies --clear=false on its
// own is not a clear request and sends no request.
func TestMultipathOverride_SetClearFalseSendsNothing(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/multipath/overrides", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "multipath", "override", "set", "--clear=false", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one")
	require.False(t, called)
}

// TestMultipathOverride_SetRequiresConfirmation verifies a clear, or a full
// replacement without a digest, is refused without --yes and sends nothing,
// while a digest-guarded replacement needs no confirmation.
func TestMultipathOverride_SetRequiresConfirmation(t *testing.T) {
	for _, args := range [][]string{
		{"--clear"},
		{"--clear", "--digest", "abc"},
		{"--overrides", "defaults {}"},
		{"--overrides", "defaults {}", "--digest", ""},
	} {
		f, ac := newFakeClient(t)
		called := false
		f.HandleFunc("PUT /api2/json/cluster/multipath/overrides", func(w http.ResponseWriter, _ *http.Request) {
			called = true
			testhelper.WriteData(w, nil)
		})
		deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

		var buf bytes.Buffer
		err := run(deps, &buf, append([]string{"multipath", "override", "set"}, args...)...)
		require.Error(t, err, "%v", args)
		require.Contains(t, err.Error(), "--yes")
		require.False(t, called, "%v", args)
	}

	f, ac := newFakeClient(t)
	var method, path string
	var form url.Values
	recordMultipath(f, "PUT /api2/json/cluster/multipath/overrides", &method, &path, &form, nil)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "multipath", "override", "set", "--overrides", "defaults {}", "--digest", "abc"))
	require.Equal(t, http.MethodPut, method)
}

// TestMultipathOverride_SetReadsFileBeforePrompt verifies a missing --file fails
// with the read error rather than a confirmation refusal, and sends nothing.
func TestMultipathOverride_SetReadsFileBeforePrompt(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/multipath/overrides", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.conf")
	err := run(deps, &buf, "multipath", "override", "set", "--file", missing)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read --file")
	require.False(t, called)
}
