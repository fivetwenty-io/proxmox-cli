package storage

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestStorage_OciPullCommandTree verifies the oci-pull command is registered
// directly under the storage Group.
func TestStorage_OciPullCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["oci-pull"], "storage group must expose the oci-pull command")
}

// TestOciPull_Success verifies `pve storage oci-pull <storage>` issues a POST
// to the correct path, forwards the reference form field, waits on the task,
// and reports success.
func TestOciPull_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:ocipull::root@pam:"
	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	out, err := run(t, f, "--node", "pve1", "oci-pull", "local",
		"--reference", "docker.io/library/alpine:latest",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/storage/local/oci-registry-pull", gotPath)
	require.Equal(t, "docker.io/library/alpine:latest", gotForm.Get("reference"))
	require.Contains(t, out, "complete")
}

// TestOciPull_OptionalFilenameOmitted verifies --filename is not sent when absent.
func TestOciPull_OptionalFilenameOmitted(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:ocipull::root@pam:"
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	_, err := run(t, f, "--node", "pve1", "oci-pull", "local",
		"--reference", "docker.io/library/alpine:latest",
	)
	require.NoError(t, err)
	require.Empty(t, gotForm.Get("filename"), "filename must not be sent when --filename is absent")
}

// TestOciPull_FilenameForwarded verifies --filename is sent when provided.
func TestOciPull_FilenameForwarded(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:ocipull::root@pam:"
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	_, err := run(t, f, "--node", "pve1", "oci-pull", "local",
		"--reference", "docker.io/library/alpine:latest",
		"--filename", "alpine-latest.tar",
	)
	require.NoError(t, err)
	require.Equal(t, "alpine-latest.tar", gotForm.Get("filename"))
}

// TestOciPull_RequiredFlags verifies the command refuses to run when --reference
// is omitted.
func TestOciPull_RequiredFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "")
	})

	_, err := run(t, f, "--node", "pve1", "oci-pull", "local")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reference")
	require.False(t, called, "no request must be made when --reference is absent")
}

// TestOciPull_RequiresNode verifies the command fails clearly without a resolved node.
func TestOciPull_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "")
	})

	_, err := run(t, f, "oci-pull", "local", "--reference", "docker.io/library/alpine:latest")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
	require.False(t, called, "no request must be made without a node")
}

// TestOciPull_Async verifies --async prints the UPID without waiting.
func TestOciPull_Async(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:ocipull::root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	out, err := run(t, f, "--async", "--node", "pve1", "oci-pull", "local",
		"--reference", "docker.io/library/alpine:latest",
	)
	require.NoError(t, err)
	require.Contains(t, out, upid)
}
