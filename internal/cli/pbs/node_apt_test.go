package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestNodeAptLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/apt/update", &rec, []map[string]any{
		{"package": "proxmox-backup", "oldversion": "3.0", "version": "3.1", "priority": "optional", "section": "admin"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "proxmox-backup")
	require.Contains(t, buf.String(), "3.1")
}

func TestNodeAptLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/apt/update", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list apt updates")
}

func TestNodeAptUpdate_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/apt/update", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "update", "--notify", "--quiet")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "1", rec.form.Get("notify"))
	require.Equal(t, "1", rec.form.Get("quiet"))
	require.Contains(t, buf.String(), "refreshed")
}

func TestNodeAptUpdate_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/apt/update", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "update")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

func TestNodeAptRepositories_RendersSummary(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/apt/repositories", &rec, map[string]any{
		"digest": "digestval", "errors": []any{}, "files": []any{map[string]any{"path": "/etc/apt/sources.list"}},
		"infos": []any{}, "standard-repos": []any{map[string]any{"handle": "no-subscription"}},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "repositories")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "digestval")
}

func TestNodeAptRepoAdd_RequiresHandle(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "repo-add")
	require.Error(t, err)
}

func TestNodeAptRepoAdd_SendsHandle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/apt/repositories", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "repo-add", "--handle", "no-subscription", "--digest", "d1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "no-subscription", rec.form.Get("handle"))
	require.Equal(t, "d1", rec.form.Get("digest"))
}

func TestNodeAptRepoUpdate_RequiresPathAndIndex(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "repo-update")
	require.Error(t, err)
}

func TestNodeAptRepoUpdate_SendsFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/apt/repositories", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "repo-update",
		"--path", "/etc/apt/sources.list", "--index", "0", "--enabled", "--digest", "d1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/etc/apt/sources.list", rec.form.Get("path"))
	require.Equal(t, "0", rec.form.Get("index"))
	require.Equal(t, "1", rec.form.Get("enabled"))
	require.Equal(t, "d1", rec.form.Get("digest"))
}

func TestNodeAptVersions_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/apt/versions", &rec, []map[string]any{
		{"package": "proxmox-backup-server", "version": "3.1-1"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "versions")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "proxmox-backup-server")
}

func TestNodeAptChangelog_RequiresName(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "changelog")
	require.Error(t, err)
}

func TestNodeAptChangelog_RendersText(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/apt/changelog", &rec, "changelog text here")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "apt", "changelog", "--name", "proxmox-backup-server", "--version", "3.1-1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "proxmox-backup-server", rec.query.Get("name"))
	require.Equal(t, "3.1-1", rec.query.Get("version"))
	require.Contains(t, buf.String(), "changelog text here")
}
