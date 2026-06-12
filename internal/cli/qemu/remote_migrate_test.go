package qemu

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// remote-migrate is classified deferred-irreversible: tests use the fake server
// to verify flag wiring and the --yes gate; no live e2e is designed.

func TestQemuRemoteMigrate_RequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "remote-migrate", "100",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmation")
}

func TestQemuRemoteMigrate_RequiresTargetEndpoint(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "remote-migrate", "100", "--yes",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "target-endpoint")
}

func TestQemuRemoteMigrate_RequiresTargetStorage(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "remote-migrate", "100", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-bridge", "vmbr0")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "target-storage")
}

func TestQemuRemoteMigrate_RequiresTargetBridge(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "remote-migrate", "100", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "target-bridge")
}

func TestQemuRemoteMigrate_SuccessAsync(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/remote_migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "remote-migrate", "100", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/remote_migrate", gotPath)
	// Body is URL-encoded; parse it for exact value assertions.
	form := parseForm(t, body)
	require.Contains(t, form.Get("target-endpoint"), "remote")
	require.Equal(t, "local-lvm", form.Get("target-storage"))
	require.Equal(t, "vmbr0", form.Get("target-bridge"))
	require.Contains(t, buf.String(), validUPID)
}

func TestQemuRemoteMigrate_OptionalFlagsOmitted(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/remote_migrate", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "remote-migrate", "100", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0"))

	// Optional flags not provided must not appear in body.
	require.NotContains(t, body, "online")
	require.NotContains(t, body, "bwlimit")
}

func TestQemuRemoteMigrate_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/remote_migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	err := run(deps, &buf, "remote-migrate", "100", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "remote-migrate VM 100")
}

func TestQemuRemoteMigrate_CommandTree(t *testing.T) {
	root := newGroupCmd(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["remote-migrate"], "expected top-level sub-command 'remote-migrate'")
}
