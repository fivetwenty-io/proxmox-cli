package storage

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newFakeClient returns a FakePVE and a constructed APIClient pointing at it,
// for tests that need to inject Deps directly (e.g. to swap in a FakeRunner)
// instead of driving through the root command like run does.
func newFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()
	f := testhelper.NewFakePVE(t)

	// The fake server's default Options.Host carries "host:port" while Port is
	// left at the client default (8006); split them so the constructed client
	// targets the fake correctly.
	u, err := url.Parse(f.BaseURL())
	require.NoError(t, err)
	host, portStr, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	opts := f.Options
	opts.Host = host
	opts.Port = port

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return f, ac
}

// runDeps builds the storage group command with the given Deps injected via
// context, captures output in buf, and executes it with the supplied args.
func runDeps(deps *cli.Deps, buf *bytes.Buffer, args ...string) error {
	cmd := Group(&cli.Deps{})
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// handleStorageConfig registers the GET /storage/{name} response the snippets
// path reads to locate the storage's filesystem path and content types.
func handleStorageConfig(f *testhelper.FakePVE, name string, cfg map[string]any) {
	f.HandleJSON("GET /api2/json/storage/"+name, cfg)
}

// snippetsDeps builds Deps against the fake client with a FakeRunner and node
// pve1, the common fixture for the SSH snippets tests.
func snippetsDeps(ac *apiclient.APIClient, fr *exec.FakeRunner) *cli.Deps {
	return &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable, Node: "pve1", Runner: fr}
}

// TestStorageUploadSnippets_StreamsOverSSH verifies --content snippets bypasses
// the API upload endpoint entirely and streams the file over batch-mode ssh
// into the storage's snippets directory on the resolved node address.
func TestStorageUploadSnippets_StreamsOverSSH(t *testing.T) {
	f, ac := newFakeClient(t)
	handleStorageConfig(f, "local", map[string]any{
		"type": "dir", "path": "/var/lib/vz", "content": "iso,snippets",
	})
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "node", "name": "pve1", "ip": "10.0.0.5", "online": 1},
	})
	var uploadCalled bool
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/upload",
		func(w http.ResponseWriter, _ *http.Request) {
			uploadCalled = true
			testhelper.WriteData(w, "")
		})

	fr := exec.Fake()
	deps := snippetsDeps(ac, fr)

	path := writeTempFile(t, "user-data.yaml", "#cloud-config\npackages: [qemu-guest-agent]\n")
	var buf bytes.Buffer
	require.NoError(t, runDeps(deps, &buf, "upload", "local", "--file", path, "--content", "snippets"))

	require.Len(t, fr.Calls, 1, "expected exactly one ssh invocation")
	c := fr.Calls[0]
	require.Equal(t, "ssh", c.Name)
	require.False(t, c.Interactive)
	require.Contains(t, c.Args, "BatchMode=yes")
	require.Contains(t, c.Args, "root@10.0.0.5")
	require.Equal(t,
		"mkdir -p '/var/lib/vz/snippets' && cat > '/var/lib/vz/snippets/user-data.yaml'",
		c.Args[len(c.Args)-1])
	require.Equal(t, "#cloud-config\npackages: [qemu-guest-agent]\n", string(c.StdinContents))
	require.Contains(t, buf.String(), "Uploaded snippet")
	require.Contains(t, buf.String(), "#2208")
	require.False(t, uploadCalled, "the PVE upload API must not be called for snippets")
}

// TestStorageUploadSnippets_FilenameOverride verifies --filename controls the
// remote destination name independently of the local source path.
func TestStorageUploadSnippets_FilenameOverride(t *testing.T) {
	f, ac := newFakeClient(t)
	handleStorageConfig(f, "local", map[string]any{
		"type": "dir", "path": "/var/lib/vz", "content": "snippets",
	})

	fr := exec.Fake()
	deps := snippetsDeps(ac, fr)

	path := writeTempFile(t, "local-name.yaml", "data")
	var buf bytes.Buffer
	require.NoError(t, runDeps(deps, &buf, "upload", "local",
		"--file", path, "--content", "snippets", "--filename", "renamed.yaml"))

	require.Len(t, fr.Calls, 1)
	c := fr.Calls[0]
	require.Contains(t, c.Args[len(c.Args)-1], "'/var/lib/vz/snippets/renamed.yaml'")
}

// TestStorageUploadSnippets_ContextSSHDefaults verifies the active context's
// ssh block fills user/port when the operator did not pass -l/-p explicitly.
func TestStorageUploadSnippets_ContextSSHDefaults(t *testing.T) {
	f, ac := newFakeClient(t)
	handleStorageConfig(f, "local", map[string]any{
		"type": "dir", "path": "/var/lib/vz", "content": "snippets",
	})

	fr := exec.Fake()
	deps := snippetsDeps(ac, fr)
	deps.Ctx = &config.Context{SSH: config.SSHBlock{User: "admin", Port: 2222}}

	path := writeTempFile(t, "s.yaml", "x")
	var buf bytes.Buffer
	require.NoError(t, runDeps(deps, &buf, "upload", "local", "--file", path, "--content", "snippets"))

	require.Len(t, fr.Calls, 1)
	c := fr.Calls[0]
	// The destination is the second-to-last arg (the remote command is last);
	// the host part comes from whatever cluster/status the fake serves, so
	// assert only the context-supplied user.
	require.True(t, strings.HasPrefix(c.Args[len(c.Args)-2], "admin@"),
		"context ssh user must apply, got %q", c.Args[len(c.Args)-2])
	require.Contains(t, c.Args, "2222")
}

// TestStorageUploadSnippets_RequiresSnippetsContent verifies a storage without
// the snippets content type is rejected before any ssh call, with a hint that
// includes the corrected content list.
func TestStorageUploadSnippets_RequiresSnippetsContent(t *testing.T) {
	f, ac := newFakeClient(t)
	handleStorageConfig(f, "local", map[string]any{
		"type": "dir", "path": "/var/lib/vz", "content": "iso,vztmpl",
	})

	fr := exec.Fake()
	deps := snippetsDeps(ac, fr)

	path := writeTempFile(t, "s.yaml", "x")
	var buf bytes.Buffer
	err := runDeps(deps, &buf, "upload", "local", "--file", path, "--content", "snippets")
	require.Error(t, err)
	require.Contains(t, err.Error(), "snippets content is not enabled")
	require.Contains(t, err.Error(), "--content iso,vztmpl,snippets")
	require.Empty(t, fr.Calls, "no ssh attempt when snippets content is disabled")
}

// TestStorageUploadSnippets_RequiresPathBackedStorage verifies a storage with
// no filesystem path (e.g. rbd) is rejected: the SSH workaround cannot place a
// file on it.
func TestStorageUploadSnippets_RequiresPathBackedStorage(t *testing.T) {
	f, ac := newFakeClient(t)
	handleStorageConfig(f, "ceph-pool", map[string]any{
		"type": "rbd", "content": "images",
	})

	fr := exec.Fake()
	deps := snippetsDeps(ac, fr)

	path := writeTempFile(t, "s.yaml", "x")
	var buf bytes.Buffer
	err := runDeps(deps, &buf, "upload", "ceph-pool", "--file", path, "--content", "snippets")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no filesystem path")
	require.Empty(t, fr.Calls)
}

// TestStorageUploadSnippets_RejectsChecksum verifies --checksum is refused in
// snippets mode: the file never passes through the PVE upload API that would
// verify it.
func TestStorageUploadSnippets_RejectsChecksum(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := snippetsDeps(ac, fr)

	path := writeTempFile(t, "s.yaml", "x")
	var buf bytes.Buffer
	err := runDeps(deps, &buf, "upload", "local",
		"--file", path, "--content", "snippets", "--checksum", "abc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--checksum is not supported with --content snippets")
	require.Empty(t, fr.Calls)
}

// TestStorageUploadSnippets_RejectsPathInFilename verifies destination names
// with path separators (traversal) are refused.
func TestStorageUploadSnippets_RejectsPathInFilename(t *testing.T) {
	_, ac := newFakeClient(t)
	fr := exec.Fake()
	deps := snippetsDeps(ac, fr)

	path := writeTempFile(t, "s.yaml", "x")
	var buf bytes.Buffer
	err := runDeps(deps, &buf, "upload", "local",
		"--file", path, "--content", "snippets", "--filename", "../outside.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid snippet file name")
	require.Empty(t, fr.Calls)
}

// TestStorageUploadSnippets_SSHFailurePropagates verifies a non-zero ssh exit
// surfaces as a command error naming the snippet and node.
func TestStorageUploadSnippets_SSHFailurePropagates(t *testing.T) {
	f, ac := newFakeClient(t)
	handleStorageConfig(f, "local", map[string]any{
		"type": "dir", "path": "/var/lib/vz", "content": "snippets",
	})

	fr := exec.Fake(exec.FakeResponse{ExitCode: 255, Stderr: "Permission denied (publickey)."})
	deps := snippetsDeps(ac, fr)

	path := writeTempFile(t, "s.yaml", "x")
	var buf bytes.Buffer
	err := runDeps(deps, &buf, "upload", "local", "--file", path, "--content", "snippets")
	require.Error(t, err)
	require.Contains(t, err.Error(), "over SSH")
}
