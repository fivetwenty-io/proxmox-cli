package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- lxc config set --set -----------------------------------------------------

func TestConfigSet_SetAlone_SendsRawBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101", "--set", "swap=1024")
	require.NoError(t, run())
	require.EqualValues(t, 1024, body["swap"])
	require.Contains(t, buf.String(), "updated")
}

func TestConfigSet_SetMergedWithTypedFlag_OneBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101",
		"--hostname", "newname", "--set", "swap=1024", "--set", "brand-new-pve-option=1")
	require.NoError(t, run())
	require.Equal(t, "newname", body["hostname"])
	require.EqualValues(t, 1024, body["swap"])
	require.Equal(t, true, body["brand-new-pve-option"])
}

func TestConfigSet_SetCollidesWithFlag_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101", "--memory", "1024", "--set", "memory=2048")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "--set memory=2048")
	require.ErrorContains(t, err, "--memory")
	require.False(t, called, "no request should be sent when --set collides with a dedicated flag")
}

func TestConfigSet_SetMalformed_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101", "--set", "no-equals-here")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "want KEY=VALUE")
	require.False(t, called)
}

func TestConfigSet_SetDuplicateKey_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101", "--set", "swap=512", "--set", "swap=1024")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "specified more than once")
}

func TestConfigSet_SetUnknownKey_WritesNote(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101", "--set", "brand-new-pve-option=1")
	require.NoError(t, run())
	require.Contains(t, buf.String(), `note: "brand-new-pve-option" is not in this CLI's known config schema; sending it anyway`)
}

func TestConfigSet_SetAlone_CountsAsChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101", "--set", "swap=512")
	require.NoError(t, run())
	require.True(t, called)
}

// --- lxc create --set ---------------------------------------------------------

func TestCreate_SetAlone_AsyncPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	upid := "UPID:pve1:0:0:0:vzcreate:101:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, upid)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101", "--ostemplate", "local:vztmpl/alpine.tar.zst",
		"--set", "swap=1024", "--set", "brand-new-pve-option=1")
	require.NoError(t, run())
	require.Equal(t, "local:vztmpl/alpine.tar.zst", body["ostemplate"])
	require.EqualValues(t, 1024, body["swap"])
	require.Equal(t, true, body["brand-new-pve-option"])
	require.Contains(t, buf.String(), upid)
}

func TestCreate_SetCollidesWithFlag_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzcreate:101:root@pam:")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101", "--ostemplate", "local:vztmpl/alpine.tar.zst",
		"--cores", "2", "--set", "cores=4")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "--set cores=4")
	require.False(t, called)
}

func TestCreate_NoSet_TypedPathUnchanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	upid := "UPID:pve1:0:0:0:vzcreate:101:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, upid)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101", "--ostemplate", "local:vztmpl/alpine.tar.zst", "--cores", "2")
	require.NoError(t, run())
	require.Equal(t, "/api2/json/nodes/pve1/lxc", gotPath)
	require.Contains(t, buf.String(), upid)
}
