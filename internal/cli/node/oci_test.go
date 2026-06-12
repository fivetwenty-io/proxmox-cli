package node_test

import (
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

const ociUPID = "UPID:pve1:00000001:00000002:AABBCCDD:imgpull:local:root@pam:"

// ---- oci tags --------------------------------------------------------------

func TestNodeOci_Tags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/query-oci-repo-tags", &rec,
		[]string{"latest", "3.20", "edge"})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "tags", "--reference", "alpine"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/query-oci-repo-tags", rec.path)
	require.Contains(t, rec.query, "reference=alpine")
	out := buf.String()
	require.Contains(t, out, "TAG")
	require.Contains(t, out, "latest")
	require.Contains(t, out, "edge")
}

func TestNodeOci_TagsRequiresReference(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "tags"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "reference")
}

func TestNodeOci_TagsAPIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/query-oci-repo-tags", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadGateway, "registry unreachable")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "tags", "--reference", "alpine"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "query OCI repo tags")
}

// ---- oci pull --------------------------------------------------------------

func TestNodeOci_PullRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull",
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			testhelper.WriteData(w, ociUPID)
		})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "pull", "local", "--reference", "alpine:latest"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "pull must not POST without --yes")
}

func TestNodeOci_PullBlocksUntilTaskDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull",
		func(w http.ResponseWriter, r *http.Request) {
			rec.method = r.Method
			_ = r.ParseForm()
			rec.body = r.Form.Encode()
			testhelper.WriteData(w, ociUPID)
		})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+ociUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": ociUPID,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "pull", "local",
		"--reference", "alpine:latest", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "reference=alpine%3Alatest")
	// --filename was not passed, so it must be omitted from the body.
	require.NotContains(t, rec.body, "filename")
	require.Contains(t, buf.String(), "pulled into local")
}

func TestNodeOci_PullForwardsFilename(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull",
		func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			rec.body = r.Form.Encode()
			testhelper.WriteData(w, ociUPID)
		})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+ociUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": ociUPID,
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "pull", "local",
		"--reference", "alpine:latest", "--filename", "alpine.oci", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "filename=alpine.oci")
}

func TestNodeOci_PullAsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, ociUPID)
		})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "oci", "pull", "local",
		"--reference", "alpine:latest", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), ociUPID)
}

// TestNodeOci_PullNonUPIDFallback covers the renderOciTask branch where the
// POST returns an empty body: UPIDFromRaw errors, so the command falls back to
// a plain success message without polling any task-status endpoint.
func TestNodeOci_PullNonUPIDFallback(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, nil)
		})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "pull", "local",
		"--reference", "alpine:latest", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "pulled into local")
}

func TestNodeOci_PullAPIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/oci-registry-pull",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "boom")
		})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "oci", "pull", "local",
		"--reference", "alpine:latest", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pull OCI image")
}

func TestNodeOci_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "oci", "tags", "--reference", "alpine"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeOci_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	addNodeGroup(root)

	var node, oci *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "node" {
			node = c
		}
	}
	require.NotNil(t, node)
	for _, c := range node.Commands() {
		if c.Name() == "oci" {
			oci = c
		}
	}
	require.NotNil(t, oci, "node must expose an oci sub-command")

	names := map[string]bool{}
	for _, c := range oci.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"tags", "pull"} {
		require.True(t, names[want], "expected oci sub-command %q", want)
	}
}
