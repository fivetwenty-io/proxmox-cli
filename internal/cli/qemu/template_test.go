package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestQemuTemplate_RequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "template", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
}

func TestQemuTemplate_ConvertsNullBody(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/template", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "template", "100", "--yes"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/template", gotPath)
	require.Contains(t, buf.String(), "converted into a template")
}

func TestQemuTemplate_WithDisk(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/template", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "template", "100", "--yes", "--disk", "scsi0"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "scsi0", form.Get("disk"))
}

func TestQemuTemplate_BlocksOnUPID(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/template", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "template", "100", "--yes"))
	require.Contains(t, buf.String(), "converted into a template")
}

func TestQemuTemplate_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/template", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, validUPID)
	})
	depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "template", "100", "--yes", "--async"))
	require.Contains(t, buf.String(), validUPID)
}

func TestQemuTemplate_RequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(&buf, "template", "100", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestQemuTemplateCommandTree(t *testing.T) {
	cmd := newGroupCmd(nil)
	var tmpl *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "template" {
			tmpl = c
			break
		}
	}
	require.NotNil(t, tmpl, "template command should be registered")
	require.NotNil(t, tmpl.Flags().Lookup("yes"), "template should define --yes")
	require.NotNil(t, tmpl.Flags().Lookup("disk"), "template should define --disk")
}
