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

// --- clone ------------------------------------------------------------------

func TestQemuClone_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/clone", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "clone", "100", "--newid", "200"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/clone", gotPath)
	require.Contains(t, buf.String(), "cloned")
}

func TestQemuClone_RequiresNewid(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "clone", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--newid is required")
}

func TestQemuClone_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/clone", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "clone", "100",
		"--newid", "200", "--name", "pve-cli-clone", "--target-node", "pve2", "--full"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "200", form.Get("newid"))
	require.Equal(t, "pve-cli-clone", form.Get("name"))
	require.Equal(t, "pve2", form.Get("target"))
	require.Equal(t, "1", form.Get("full"))
}

func TestQemuClone_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/clone", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.Error(t, run(&buf, "clone", "100", "--newid", "200"))
}

// --- migrate ----------------------------------------------------------------

func TestQemuMigrate_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "migrate", "100", "--target-node", "pve2"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/migrate", gotPath)
	require.Contains(t, buf.String(), "migrated")
}

func TestQemuMigrate_RequiresTargetNode(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "migrate", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--target-node is required")
}

func TestQemuMigrate_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "migrate", "100",
		"--target-node", "pve2", "--online", "--with-local-disks"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "pve2", form.Get("target"))
	require.Equal(t, "1", form.Get("online"))
	require.Equal(t, "1", form.Get("with-local-disks"))
}

// TestQemuCloneMigrate_NoLocalTargetFlag guards against a regression where
// clone/migrate defined a local --target flag. The root command owns a
// persistent -t/--target flag that selects the configured target; a local
// --target on a subcommand shadows it, so `pve --target lab qemu clone ...`
// would route the target name into the destination-node parameter. The
// destination-node flag must therefore be named --target-node.
func TestQemuCloneMigrate_NoLocalTargetFlag(t *testing.T) {
	group := newGroupCmd(nil)
	for _, name := range []string{"clone", "migrate"} {
		var sub *cobra.Command
		for _, c := range group.Commands() {
			if c.Name() == name {
				sub = c
				break
			}
		}
		require.NotNilf(t, sub, "qemu %s command must be registered", name)
		require.Nilf(t, sub.Flags().Lookup("target"),
			"qemu %s must not define a local --target flag (shadows the global -t/--target)", name)
		require.Nilf(t, sub.Flags().Lookup("node"),
			"qemu %s must not define a local --node flag (shadows the global --node)", name)
		require.NotNilf(t, sub.Flags().Lookup("target-node"),
			"qemu %s must expose the destination node as --target-node", name)
	}
}
