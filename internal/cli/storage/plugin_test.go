package storage

import (
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// pluginFixture is a plugin record in the shape the server returns it.
func pluginFixture(typ, name, origin string) map[string]any {
	return map[string]any{
		"type":         typ,
		"display-name": name,
		"origin":       origin,
		"content":      map[string]any{"supported": []string{"images", "rootdir"}, "default": []string{"images"}},
		"schema":       map[string]any{"properties": map[string]any{"portal": map[string]any{"type": "string"}}},
	}
}

// pluginRequest is a recorded plugin-index request, with the query string the
// shared recordedRequest does not keep.
type pluginRequest struct {
	recordedRequest
	query string
}

// recordPlugin answers pattern with payload and records the request.
func recordPlugin(f *testhelper.FakePVE, pattern string, rec *pluginRequest, payload any) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.query = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, payload)
	})
}

// TestStoragePlugin_List verifies list reads the plugin index, sends no filter
// by default, and renders one row per plugin in every output format.
func TestStoragePlugin_List(t *testing.T) {
	cases := []struct {
		format string
		want   []string
	}{
		{"table", []string{"TYPE", "ORIGIN", "nvmeof", "NVMe over Fabrics", "images,rootdir", "builtin"}},
		{"json", []string{`"type": "nvmeof"`, `"display-name": "NVMe over Fabrics"`, `"portal"`}},
		{"yaml", []string{"type: nvmeof", "display-name: NVMe over Fabrics", "portal"}},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec pluginRequest
			recordPlugin(f, "GET /api2/json/plugins/storage/plugin", &rec, []any{
				pluginFixture("nvmeof", "NVMe over Fabrics", "builtin"),
			})

			out, err := run(t, f, "plugin", "list", "-o", tc.format)
			require.NoError(t, err)
			require.Equal(t, http.MethodGet, rec.method)
			require.Equal(t, "/api2/json/plugins/storage/plugin", rec.path)
			require.Empty(t, rec.query, "no filter may be sent unless --origin is given")
			for _, want := range tc.want {
				require.Contains(t, out, want)
			}
		})
	}
}

// TestStoragePlugin_ListLiveShape verifies a list in the shape PVE 9.3.1 returns
// (no display-name on any record) renders without an always-blank NAME column.
func TestStoragePlugin_ListLiveShape(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec pluginRequest
	live := func(typ string) map[string]any {
		return map[string]any{
			"type":    typ,
			"origin":  "builtin",
			"content": map[string]any{"supported": []string{"images", "iso"}, "default": []string{"images"}},
			"schema":  map[string]any{"path": map[string]any{"type": "string"}},
		}
	}
	recordPlugin(f, "GET /api2/json/plugins/storage/plugin", &rec, []any{live("btrfs"), live("nfs")})

	out, err := run(t, f, "plugin", "list")
	require.NoError(t, err)
	require.NotContains(t, out, "NAME")
	for _, want := range []string{"TYPE", "ORIGIN", "CONTENT", "DEFAULT", "btrfs", "nfs", "images,iso"} {
		require.Contains(t, out, want)
	}
}

// TestStoragePlugin_ListOrigin verifies --origin is passed through as a query
// parameter and that an unknown origin is refused before any request.
func TestStoragePlugin_ListOrigin(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec pluginRequest
	recordPlugin(f, "GET /api2/json/plugins/storage/plugin", &rec, []any{
		pluginFixture("zfs", "ZFS", "custom"),
	})

	out, err := run(t, f, "plugin", "list", "--origin", "custom")
	require.NoError(t, err)
	require.Equal(t, "origin=custom", rec.query)
	require.Contains(t, out, "custom")

	_, err = run(t, f, "plugin", "list", "--origin", "elsewhere")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --origin")
}

// TestStoragePlugin_Show verifies show reads one plugin by type and renders
// its fields in every output format.
func TestStoragePlugin_Show(t *testing.T) {
	cases := []struct {
		format string
		want   []string
	}{
		{"table", []string{"display-name", "NVMe over Fabrics", "content-supported", "images,rootdir", "content-default"}},
		{"json", []string{`"type": "nvmeof"`, `"origin": "builtin"`, `"portal"`}},
		{"yaml", []string{"type: nvmeof", "origin: builtin", "portal"}},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec pluginRequest
			recordPlugin(f, "GET /api2/json/plugins/storage/plugin/nvmeof", &rec,
				pluginFixture("nvmeof", "NVMe over Fabrics", "builtin"))

			out, err := run(t, f, "plugin", "show", "nvmeof", "-o", tc.format)
			require.NoError(t, err)
			require.Equal(t, http.MethodGet, rec.method)
			require.Equal(t, "/api2/json/plugins/storage/plugin/nvmeof", rec.path)
			for _, want := range tc.want {
				require.Contains(t, out, want)
			}
		})
	}
}

// TestStoragePlugin_ShowWithoutDisplayName verifies a plugin that gives no
// display name still renders.
func TestStoragePlugin_ShowWithoutDisplayName(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec pluginRequest
	recordPlugin(f, "GET /api2/json/plugins/storage/plugin/dir", &rec, map[string]any{
		"type": "dir", "origin": "builtin", "schema": map[string]any{},
		"content": map[string]any{"supported": []string{"iso"}, "default": []string{}},
	})

	out, err := run(t, f, "plugin", "show", "dir")
	require.NoError(t, err)
	require.Contains(t, out, "dir")
}

// TestStoragePlugin_ShowWrapsServerError verifies a server error names the
// type that was asked for.
func TestStoragePlugin_ShowWrapsServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/plugins/storage/plugin/nope", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "unknown plugin type")
	})

	_, err := run(t, f, "plugin", "show", "nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), `show storage plugin "nope"`)
}

// TestStoragePlugin_Completion verifies the type argument completes from the
// documented list, and only for the first argument.
func TestStoragePlugin_Completion(t *testing.T) {
	cmd := newPluginShowCmd()
	got, dir := cmd.ValidArgsFunction(cmd, nil, "")
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	require.Contains(t, got, "nvmeof")
	require.Contains(t, got, "nfs")

	got, _ = cmd.ValidArgsFunction(cmd, []string{"nfs"}, "")
	require.Empty(t, got)
}
