package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// --- qemu config set --set ---------------------------------------------------

func TestQemuConfigSet_SetAlone_SendsRawBody(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--set", "balloon=2048"))
	require.Equal(t, "2048", parseForm(t, body).Get("balloon"))
	require.Contains(t, buf.String(), "updated")
}

func TestQemuConfigSet_SetMergedWithTypedFlag_OneBody(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--cores", "4", "--set", "balloon=2048", "--set", "brand-new-pve-option=1"))
	form := parseForm(t, body)
	require.Equal(t, "4", form.Get("cores"))
	require.Equal(t, "2048", form.Get("balloon"))
	require.Equal(t, "1", form.Get("brand-new-pve-option"))
}

func TestQemuConfigSet_SetCollidesWithFlag_Errors(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "set", "100", "--cores", "4", "--set", "cores=8")
	require.Error(t, err)
	require.ErrorContains(t, err, "--set cores=8")
	require.ErrorContains(t, err, "--cores")
	require.False(t, called, "no request should be sent when --set collides with a dedicated flag")
}

func TestQemuConfigSet_SetMalformed_Errors(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "set", "100", "--set", "balloon-no-equals")
	require.Error(t, err)
	require.ErrorContains(t, err, "want KEY=VALUE")
	require.False(t, called)
}

func TestQemuConfigSet_SetDuplicateKey_Errors(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "set", "100", "--set", "balloon=1024", "--set", "balloon=2048")
	require.Error(t, err)
	require.ErrorContains(t, err, "specified more than once")
	require.False(t, called)
}

func TestQemuConfigSet_SetUnknownKey_WritesNote(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--set", "brand-new-pve-option=1"))
	require.Contains(t, buf.String(), `note: "brand-new-pve-option" is not in this CLI's known config schema; sending it anyway`)
}

func TestQemuConfigSet_SetAlone_CountsAsChange(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	// --set is the only change specified; it must satisfy the "no changes" guard.
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--set", "balloon=2048"))
	require.True(t, called)
}

func TestQemuConfigSet_NoChanges_StillErrors(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "set", "100")
	require.Error(t, err)
	require.ErrorContains(t, err, "no configuration changes specified")
}

// --- qemu create --set -------------------------------------------------------

func TestQemuCreate_SetAlone_AsyncPath(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--set", "balloon=2048", "--set", "brand-new-pve-option=1"))
	form := parseForm(t, body)
	require.Equal(t, "100", form.Get("vmid"))
	require.Equal(t, "2048", form.Get("balloon"))
	require.Equal(t, "1", form.Get("brand-new-pve-option"))
	require.Contains(t, buf.String(), validUPID)
}

func TestQemuCreate_SetMergedWithTypedFlag_OneBody(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--cores", "4", "--set", "balloon=2048"))
	form := parseForm(t, body)
	require.Equal(t, "4", form.Get("cores"))
	require.Equal(t, "2048", form.Get("balloon"))
}

func TestQemuCreate_SetCollidesWithFlag_Errors(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	err := run(deps, &buf, "create", "100", "--cores", "4", "--set", "cores=8")
	require.Error(t, err)
	require.ErrorContains(t, err, "--set cores=8")
	require.False(t, called)
}

func TestQemuCreate_SetMalformed_Errors(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	err := run(deps, &buf, "create", "100", "--set", "no-equals-here")
	require.Error(t, err)
	require.ErrorContains(t, err, "want KEY=VALUE")
	require.False(t, called)
}

func TestQemuCreate_NoSet_TypedPathUnchanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--cores", "4"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu", gotPath)
	require.Contains(t, buf.String(), validUPID)
}
