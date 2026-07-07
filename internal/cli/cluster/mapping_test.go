package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestMappingDir_List verifies `pmx cluster mapping dir list` reads
// GET /cluster/mapping/dir and renders the focused columns.
func TestMappingDir_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/mapping/dir", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"id": "shared", "description": "shared data", "map": []any{"node=pve,path=/mnt/data"}},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "dir", "list"))
	out := buf.String()
	require.Contains(t, out, "shared")
	require.Contains(t, out, "shared data")
}

// TestMappingDir_ListCheckNodeQuery verifies --check-node is query-encoded.
func TestMappingDir_ListCheckNodeQuery(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotCheckNode string
	f.HandleFunc("GET /api2/json/cluster/mapping/dir", func(w http.ResponseWriter, r *http.Request) {
		gotCheckNode = r.URL.Query().Get("check-node")
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "dir", "list", "--check-node", "pve"))
	require.Equal(t, "pve", gotCheckNode)
}

// TestMappingDir_Get verifies get reads GET /cluster/mapping/dir/{id} and the
// raw object (a json.RawMessage alias) is rendered losslessly.
func TestMappingDir_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/mapping/dir/shared", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"id": "shared", "description": "shared data", "map": []any{"node=pve,path=/mnt/data"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "dir", "get", "shared"))
	require.Contains(t, buf.String(), "shared data")
}

// TestMappingDir_CreateForwardsFields verifies create posts id, the repeated map
// entries, and changed optionals while omitting unset ones.
func TestMappingDir_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/mapping/dir", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "dir", "create", "shared",
		"--map", "node=pve,path=/mnt/data", "--map", "node=pve2,path=/mnt/data"))
	require.Equal(t, "shared", gotForm.Get("id"))
	require.Contains(t, gotForm["map"], "node=pve,path=/mnt/data")
	require.Contains(t, gotForm["map"], "node=pve2,path=/mnt/data")
	_, hasDesc := gotForm["description"]
	require.False(t, hasDesc, "unset --description must be omitted from the request body")
}

// TestMappingCommands_RequiredFlags consolidates shape-1 (flag-required) guards for
// mapping dir, pci, and usb create/set commands. Each case omits the required --map
// flag and verifies the error message names the missing flag and no HTTP request
// is issued.
func TestMappingCommands_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		handlerPath string
		args        []string
		wantErr     string
	}{
		{
			name:        "MappingDirCreate_RequiresMap",
			handlerPath: "POST /api2/json/cluster/mapping/dir",
			args:        []string{"mapping", "dir", "create", "shared"},
			wantErr:     "map",
		},
		{
			name:        "MappingDirSet_RequiresMap",
			handlerPath: "PUT /api2/json/cluster/mapping/dir/shared",
			args:        []string{"mapping", "dir", "set", "shared", "--description", "renamed"},
			wantErr:     "map",
		},
		{
			name:        "MappingPciCreate_RequiresMap",
			handlerPath: "POST /api2/json/cluster/mapping/pci",
			args:        []string{"mapping", "pci", "create", "gpu"},
			wantErr:     "map",
		},
		{
			name:        "MappingPciSet_RequiresMap",
			handlerPath: "PUT /api2/json/cluster/mapping/pci/gpu",
			args:        []string{"mapping", "pci", "set", "gpu", "--description", "renamed"},
			wantErr:     "map",
		},
		{
			name:        "MappingUsbCreate_RequiresMap",
			handlerPath: "POST /api2/json/cluster/mapping/usb",
			args:        []string{"mapping", "usb", "create", "yubikey"},
			wantErr:     "map",
		},
		{
			name:        "MappingUsbSet_RequiresMap",
			handlerPath: "PUT /api2/json/cluster/mapping/usb/yubikey",
			args:        []string{"mapping", "usb", "set", "yubikey", "--description", "renamed"},
			wantErr:     "map",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ac := newFakeClient(t)

			var called bool
			f.HandleFunc(tc.handlerPath, func(w http.ResponseWriter, _ *http.Request) {
				called = true
				testhelper.WriteData(w, nil)
			})

			deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
			require.False(t, called, "request must not be issued when required flag %q is missing", tc.wantErr)
		})
	}
}

// TestMappingDir_SetForwardsChangedOmitsUnset verifies set forwards the required
// map plus changed flags and omits unset ones.
func TestMappingDir_SetForwardsChangedOmitsUnset(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/mapping/dir/shared", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "dir", "set", "shared",
		"--map", "node=pve,path=/mnt/data", "--description", "renamed"))
	require.Contains(t, gotForm["map"], "node=pve,path=/mnt/data")
	require.Equal(t, "renamed", gotForm.Get("description"))
	_, hasDigest := gotForm["digest"]
	require.False(t, hasDigest, "unset --digest must be omitted from the request body")
}

// TestMappingDir_DeleteRequiresYes verifies delete refuses without --yes.
func TestMappingDir_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/mapping/dir/shared", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "mapping", "dir", "delete", "shared")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

// TestMappingDir_DeleteWithYes verifies delete issues the DELETE with --yes.
func TestMappingDir_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/mapping/dir/shared", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "dir", "delete", "shared", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "deleted")
}

// TestMappingPci_CreateForwardsBools verifies PCI create forwards the map and the
// boolean flags, omitting an unset --mdev.
func TestMappingPci_CreateForwardsBools(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/mapping/pci", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "pci", "create", "gpu",
		"--map", "node=pve,path=0000:01:00.0,id=10de:1b80", "--live-migration-capable"))
	require.Equal(t, "gpu", gotForm.Get("id"))
	require.Contains(t, gotForm["map"], "node=pve,path=0000:01:00.0,id=10de:1b80")
	require.Equal(t, "1", gotForm.Get("live-migration-capable"))
	_, hasMdev := gotForm["mdev"]
	require.False(t, hasMdev, "unset --mdev must be omitted from the request body")
}

// TestMappingPci_SetForwardsChanged verifies PCI set forwards changed flags.
func TestMappingPci_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/mapping/pci/gpu", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "pci", "set", "gpu",
		"--map", "node=pve,path=0000:01:00.0,id=10de:1b80", "--mdev=false"))
	require.Contains(t, gotForm["map"], "node=pve,path=0000:01:00.0,id=10de:1b80")
	require.Equal(t, "0", gotForm.Get("mdev"))
	_, hasDesc := gotForm["description"]
	require.False(t, hasDesc, "unset --description must be omitted from the request body")
}

// TestMappingUsb_CreateForwardsFields verifies USB create posts id and map.
func TestMappingUsb_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/mapping/usb", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "usb", "create", "yubikey",
		"--map", "node=pve,path=1-2,id=046d:c52b", "--description", "key"))
	require.Equal(t, "yubikey", gotForm.Get("id"))
	require.Contains(t, gotForm["map"], "node=pve,path=1-2,id=046d:c52b")
	require.Equal(t, "key", gotForm.Get("description"))
}

// TestMappingPci_Get verifies get reads GET /cluster/mapping/pci/{id}.
func TestMappingPci_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/mapping/pci/gpu", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"id": "gpu", "description": "nvidia", "map": []any{"node=pve,path=0000:01:00.0,id=10de:1b80"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "pci", "get", "gpu"))
	require.Contains(t, buf.String(), "nvidia")
}

// TestMappingPci_DeleteRequiresYes verifies PCI delete refuses without --yes.
func TestMappingPci_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/mapping/pci/gpu", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "mapping", "pci", "delete", "gpu")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

// TestMappingPci_DeleteWithYes verifies PCI delete issues the DELETE with --yes.
func TestMappingPci_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/mapping/pci/gpu", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "pci", "delete", "gpu", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "deleted")
}

// TestMappingUsb_Get verifies get reads GET /cluster/mapping/usb/{id}.
func TestMappingUsb_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/mapping/usb/yubikey", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"id": "yubikey", "description": "key", "map": []any{"node=pve,path=1-2,id=046d:c52b"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "usb", "get", "yubikey"))
	require.Contains(t, buf.String(), "key")
}

// TestMappingUsb_SetForwardsChanged verifies USB set forwards the required map
// plus changed flags and omits unset ones.
func TestMappingUsb_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/mapping/usb/yubikey", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "usb", "set", "yubikey",
		"--map", "node=pve,path=1-2,id=046d:c52b", "--description", "renamed"))
	require.Contains(t, gotForm["map"], "node=pve,path=1-2,id=046d:c52b")
	require.Equal(t, "renamed", gotForm.Get("description"))
	_, hasDigest := gotForm["digest"]
	require.False(t, hasDigest, "unset --digest must be omitted from the request body")
}

// TestMappingUsb_DeleteRequiresYes verifies USB delete refuses without --yes.
func TestMappingUsb_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/mapping/usb/yubikey", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "mapping", "usb", "delete", "yubikey")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

// TestMappingUsb_DeleteWithYes verifies USB delete issues the DELETE with --yes.
func TestMappingUsb_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/mapping/usb/yubikey", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "mapping", "usb", "delete", "yubikey", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "deleted")
}

// TestMappingCommandTree verifies each mapping type exposes the full verb set.
func TestMappingCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	mapping := childCommands(root)["mapping"]
	require.NotNil(t, mapping, "cluster must have a mapping command")

	types := childCommands(mapping)
	for _, kind := range []string{"pci", "usb", "dir"} {
		sub := types[kind]
		require.NotNilf(t, sub, "mapping must have a %s command", kind)
		verbs := childCommands(sub)
		for _, v := range []string{"list", "get", "create", "set", "delete"} {
			require.Containsf(t, verbs, v, "mapping %s must have a %s command", kind, v)
		}
	}
}
