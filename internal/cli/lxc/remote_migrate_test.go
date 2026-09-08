package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestLxcRemoteMigrate_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "remote-migrate", "200",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmation")
}

func TestLxcRemoteMigrate_SuccessAsync(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:vzremotemigrate:200:root@pam:"
	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/200/remote_migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, upid)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "remote-migrate", "200", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/200/remote_migrate", gotPath)
	require.Contains(t, buf.String(), upid)
}

// TestLxcRemoteMigrate_NonUPIDResponseRendersMessage pins the fix for the
// non-UPID response branch. PVE can answer remote-migrate with a plain
// message instead of a task UPID; before the fix that response landed only
// in Result.Raw, which -o table and -o plain never read, so the command
// printed nothing even though the server replied. The payload is JSON text
// carrying a quoted string, and the fix unwraps it into Message so every
// format shows the server's answer without literal quotes.
func TestLxcRemoteMigrate_NonUPIDResponseRendersMessage(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/200/remote_migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, "container is a template, no migration possible")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "remote-migrate", "200", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	require.NoError(t, run())

	out := buf.String()
	require.NotEmpty(t, out)
	require.Contains(t, out, "container is a template, no migration possible")
	require.NotContains(t, out, `\"`)
}

// TestLxcRemoteMigrate_NonUPIDResponsePlainFormat repeats the non-UPID
// message check under -o plain, the other format the original bug left
// empty since it also renders from Result.Message rather than Result.Raw.
func TestLxcRemoteMigrate_NonUPIDResponsePlainFormat(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/200/remote_migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, "no migration possible")
	})
	deps := newDeps(t, f, output.FormatPlain, "pve1", true)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "remote-migrate", "200", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	require.NoError(t, run())

	out := buf.String()
	require.NotEmpty(t, out)
	require.Contains(t, out, "no migration possible")
}

func TestLxcRemoteMigrate_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/200/remote_migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "remote-migrate", "200", "--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm",
		"--target-bridge", "vmbr0")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "remote-migrate container 200")
}
