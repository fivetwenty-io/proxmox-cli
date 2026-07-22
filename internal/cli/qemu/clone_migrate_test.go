package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
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

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "clone", "100", "--newid", "200"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/clone", gotPath)
	require.Contains(t, buf.String(), "cloned")
}

// TestQemuCloneMigrate_RequiredFlags consolidates shape-1 (flag-required) cases
// for clone and migrate. Each case omits one required flag and expects the exact
// error substring listed; no HTTP handler is registered.
func TestQemuCloneMigrate_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantContain string
	}{
		{
			name:        "clone missing newid",
			args:        []string{"clone", "100"},
			wantContain: "--newid is required",
		},
		{
			name:        "migrate missing target-node",
			args:        []string{"migrate", "100"},
			wantContain: "--target-node is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ac := newFakeClient(t)
			deps := depsFor(t, ac, output.FormatTable, "pve1", false)

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantContain)
		})
	}
}

func TestQemuClone_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/clone", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "clone", "100",
		"--newid", "200", "--name", "pmx-cli-clone", "--target-node", "pve2", "--full"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "200", form.Get("newid"))
	require.Equal(t, "pmx-cli-clone", form.Get("name"))
	require.Equal(t, "pve2", form.Get("target"))
	require.Equal(t, "1", form.Get("full"))
}

func TestQemuClone_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/clone", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "clone", "100", "--newid", "200"))
}

// --- migrate ----------------------------------------------------------------

func TestQemuMigrate_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)
	handleClusterResources(f, 100, "pve1")

	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "migrate", "100", "--target-node", "pve2"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/migrate", gotPath)
	require.Contains(t, buf.String(), "migrated")
}

func TestQemuMigrate_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)
	handleClusterResources(f, 100, "pve1")

	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "migrate", "100",
		"--target-node", "pve2", "--online", "--with-local-disks"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "pve2", form.Get("target"))
	require.Equal(t, "1", form.Get("online"))
	require.Equal(t, "1", form.Get("with-local-disks"))
}

// TestQemuMigrate_ResolvesSourceNodeFromCluster verifies the migration is
// submitted on the node the VM actually runs on, not the ambient default node
// (context default-node / PMX_NODE): deps.Node says pve1 but the cluster
// inventory places VM 100 on pve3, so the POST must go to pve3.
func TestQemuMigrate_ResolvesSourceNodeFromCluster(t *testing.T) {
	f, ac := newFakeClient(t)
	handleClusterResources(f, 100, "pve3")

	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve3/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "migrate", "100", "--target-node", "pve2"))
	require.Equal(t, "/api2/json/nodes/pve3/qemu/100/migrate", gotPath)
}

// TestQemuCloneMigrate_NoLocalTargetFlag guards against a regression where
// clone/migrate defined a local --target flag. The root command owns a
// persistent -t/--target flag that selects the configured target; a local
// --target on a subcommand shadows it, so `pmx --target lab qemu clone ...`
// would route the target name into the destination-node parameter. The
// destination-node flag must therefore be named --target-node.
func TestQemuCloneMigrate_NoLocalTargetFlag(t *testing.T) {
	group := Group(nil)
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
