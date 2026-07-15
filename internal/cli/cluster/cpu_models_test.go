package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestClusterCpuModel_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/qemu/custom-cpu-models", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "custom-epyc", "reported-model": "EPYC", "flags": "+aes"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu-model", "list"))
	out := buf.String()
	require.Contains(t, out, "custom-epyc")
	require.Contains(t, out, "EPYC")
}

func TestClusterCpuModel_ListError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/qemu/custom-cpu-models", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "cpu-model", "list")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list custom CPU models")
}

func TestClusterCpuModel_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/qemu/custom-cpu-models/custom-epyc", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"cputype": "custom-epyc", "reported-model": "EPYC", "flags": "+aes;-spec-ctrl", "level": 30,
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu-model", "get", "custom-epyc"))
	out := buf.String()
	require.Contains(t, out, "reported-model")
	require.Contains(t, out, "EPYC")
}

func TestClusterCpuModel_GetError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/qemu/custom-cpu-models/custom-epyc", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such model")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "cpu-model", "get", "custom-epyc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get custom CPU model")
}

func TestClusterCpuModel_CreateForwardsAllFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/qemu/custom-cpu-models", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu-model", "create", "custom-epyc",
		"--reported-model", "EPYC", "--guest-phys-bits", "46", "--level", "30",
		"--phys-bits", "host", "--hv-vendor-id", "PVE"))
	require.Equal(t, "custom-epyc", gotForm.Get("cputype"))
	require.Equal(t, "EPYC", gotForm.Get("reported-model"))
	require.Equal(t, "46", gotForm.Get("guest-phys-bits"))
	require.Equal(t, "30", gotForm.Get("level"))
	require.Equal(t, "host", gotForm.Get("phys-bits"))
	require.Equal(t, "PVE", gotForm.Get("hv-vendor-id"))
}

func TestClusterCpuModel_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/qemu/custom-cpu-models", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu-model", "create", "custom-epyc",
		"--reported-model", "EPYC", "--flags", "+aes", "--hidden"))
	require.Equal(t, "custom-epyc", gotForm.Get("cputype"))
	require.Equal(t, "EPYC", gotForm.Get("reported-model"))
	require.Equal(t, "+aes", gotForm.Get("flags"))
	require.Equal(t, "1", gotForm.Get("hidden"))
	// --level was not passed, so it must be omitted from the body.
	_, hasLevel := gotForm["level"]
	require.False(t, hasLevel, "unset --level must be omitted from the request body")
	require.Contains(t, buf.String(), "Custom CPU model custom-epyc created.")
}

func TestClusterCpuModel_CreateRequiresReportedModel(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/qemu/custom-cpu-models", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "cpu-model", "create", "custom-epyc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reported-model")
	require.False(t, called, "create must not POST without --reported-model")
}

func TestClusterCpuModel_CreateError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/cluster/qemu/custom-cpu-models", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "bad model")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "cpu-model", "create", "custom-epyc", "--reported-model", "EPYC")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create custom CPU model")
}

func TestClusterCpuModel_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/qemu/custom-cpu-models/custom-epyc", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu-model", "set", "custom-epyc", "--level", "30", "--delete", "flags"))
	require.Equal(t, "30", gotForm.Get("level"))
	require.Equal(t, "flags", gotForm.Get("delete"))
	// --reported-model was not passed, so it must be omitted from the body.
	_, hasRM := gotForm["reported-model"]
	require.False(t, hasRM, "unset --reported-model must be omitted from the request body")
	require.Contains(t, buf.String(), "Custom CPU model custom-epyc updated.")
}

func TestClusterCpuModel_SetError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/cluster/qemu/custom-cpu-models/custom-epyc", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "bad flag")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "cpu-model", "set", "custom-epyc", "--level", "30")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update custom CPU model")
}

func TestClusterCpuModel_SetRequiresChange(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/qemu/custom-cpu-models/custom-epyc", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "cpu-model", "set", "custom-epyc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
	require.False(t, called, "set must not PUT when no field flags change")
}

func TestClusterCpuModel_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/qemu/custom-cpu-models/custom-epyc", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "cpu-model", "delete", "custom-epyc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not DELETE without --yes")
}

func TestClusterCpuModel_Delete(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/qemu/custom-cpu-models/custom-epyc", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu-model", "delete", "custom-epyc", "--yes"))
	require.Equal(t, "DELETE", gotMethod)
	require.Contains(t, buf.String(), "Custom CPU model custom-epyc deleted.")
}

func TestClusterCpuModel_CommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	var cpuModel *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "cpu-model" {
			cpuModel = c
		}
	}
	require.NotNil(t, cpuModel, "cluster must expose a cpu-model sub-command")

	names := map[string]bool{}
	for _, c := range cpuModel.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"list", "get", "create", "set", "delete"} {
		require.True(t, names[want], "expected cpu-model sub-command %q", want)
	}
}
