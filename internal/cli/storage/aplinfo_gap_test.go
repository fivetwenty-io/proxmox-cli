package storage

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestStorage_AplinfoCommandTree verifies the aplinfo sub-group and its
// sub-commands are registered in the storage Group command tree.
func TestStorage_AplinfoCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})

	var aplinfo *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "aplinfo" {
			aplinfo = c
			break
		}
	}
	require.NotNil(t, aplinfo, "aplinfo sub-group must be registered under storage")

	names := make(map[string]bool)
	for _, c := range aplinfo.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["list"], "aplinfo must expose the list sub-command")
	require.True(t, names["download"], "aplinfo must expose the download sub-command")
}

// TestAplinfoList_Success verifies `pve storage aplinfo list` issues a GET to
// the correct path and renders the package name in the table output.
func TestAplinfoList_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/aplinfo", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []map[string]any{
			{
				"package":     "debian-12-standard",
				"version":     "12.0-1",
				"section":     "system",
				"description": "Debian 12 (Bookworm)",
			},
		})
	})

	out, err := run(t, f, "--node", "pve1", "aplinfo", "list")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/aplinfo", gotPath)
	require.Contains(t, out, "debian-12-standard")
	require.Contains(t, out, "PACKAGE")
}

// TestAplinfoList_RequiresNode verifies the list sub-command fails clearly when
// no node is resolved.
func TestAplinfoList_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("GET /api2/json/nodes/pve1/aplinfo", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, []any{})
	})

	_, err := run(t, f, "aplinfo", "list")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
	require.False(t, called, "no request must be made without a node")
}

// TestAplinfoDownload_Success verifies `pve storage aplinfo download` issues a
// POST to the correct path with the required form fields, waits on the task, and
// reports success.
func TestAplinfoDownload_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:apldownload::root@pam:"
	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/nodes/pve1/aplinfo", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	out, err := run(t, f, "--node", "pve1", "aplinfo", "download",
		"--storage", "local",
		"--template", "debian-12-standard_12.0-1_amd64.tar.zst",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/aplinfo", gotPath)
	require.Equal(t, "local", gotForm.Get("storage"))
	require.Equal(t, "debian-12-standard_12.0-1_amd64.tar.zst", gotForm.Get("template"))
	require.Contains(t, out, "Downloaded")
}

// TestAplinfoDownload_RequiredFlags verifies the download sub-command refuses to
// run when a required flag is omitted.
func TestAplinfoDownload_RequiredFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing storage",
			args:    []string{"--node", "pve1", "aplinfo", "download", "--template", "t.tar.zst"},
			wantErr: "storage",
		},
		{
			name:    "missing template",
			args:    []string{"--node", "pve1", "aplinfo", "download", "--storage", "local"},
			wantErr: "template",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			_, err := run(t, f, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestAplinfoDownload_Async verifies --async prints the UPID without waiting.
func TestAplinfoDownload_Async(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:apldownload::root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/aplinfo", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	out, err := run(t, f, "--async", "--node", "pve1", "aplinfo", "download",
		"--storage", "local",
		"--template", "debian-12-standard_12.0-1_amd64.tar.zst",
	)
	require.NoError(t, err)
	require.Contains(t, out, upid)
}
