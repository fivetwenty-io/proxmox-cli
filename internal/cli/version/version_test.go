package version

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
	selfversion "github.com/fivetwenty-io/pve-cli/internal/version"
)

// fakeClient builds an APIClient pointed at the fake server. It splits the
// fake's host:port so the underlying client does not append a default port to
// an address that already carries one.
func fakeClient(t *testing.T, f *testhelper.FakePVE) *apiclient.APIClient {
	t.Helper()

	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTP,
		APIToken: "root@pam!test=00000000-0000-0000-0000-000000000000",
		SSLOptions: &pve.SSLOptions{
			VerifyMode: pve.SSLVerifyNone,
		},
	}
	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return ac
}

// withDeps overrides the package-local deps lookup so tests can inject a Deps
// built from the fake server without driving the root PersistentPreRunE. The
// returned function restores the previous lookup and must be deferred.
func withDeps(deps *cli.Deps) func() {
	prev := resolveDeps
	resolveDeps = func(_ *cobra.Command) *cli.Deps { return deps }
	return func() { resolveDeps = prev }
}

// run builds the version group command, captures output in buf, and executes
// it with the supplied args.
func run(buf *bytes.Buffer, args ...string) error {
	cmd := newGroupCmd(&cli.Deps{})
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// TestVersion_ClusterAPI_Table verifies that `pve version` queries GET /version
// and renders the cluster API version columns in table form.
func TestVersion_ClusterAPI_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"release": "8.2",
			"repoid":  "deadbeef",
			"version": "8.2.1",
			"console": "xtermjs",
		})
	})

	ac := fakeClient(t, f)

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/version", gotPath)

	out := buf.String()
	require.Contains(t, out, "VERSION")
	require.Contains(t, out, "RELEASE")
	require.Contains(t, out, "REPOID")
	require.Contains(t, out, "CONSOLE")
	require.Contains(t, out, "8.2.1")
	require.Contains(t, out, "deadbeef")
	require.Contains(t, out, "xtermjs")
}

// TestVersion_ClusterAPI_JSON verifies that JSON output carries the typed
// response fields from the version service.
func TestVersion_ClusterAPI_JSON(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/version", map[string]any{
		"release": "8.2",
		"repoid":  "deadbeef",
		"version": "8.2.1",
		"console": "xtermjs",
	})

	ac := fakeClient(t, f)

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf))

	out := buf.String()
	require.Contains(t, out, `"version": "8.2.1"`)
	require.Contains(t, out, `"release": "8.2"`)
	require.Contains(t, out, `"repoid": "deadbeef"`)
	require.Contains(t, out, `"console": "xtermjs"`)
}

// TestVersion_ClusterAPI_NilConsole verifies that a missing optional Console
// field renders an empty cell rather than panicking on the nil pointer.
func TestVersion_ClusterAPI_NilConsole(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/version", map[string]any{
		"release": "8.2",
		"repoid":  "deadbeef",
		"version": "8.2.1",
		// console omitted entirely
	})

	ac := fakeClient(t, f)

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf))

	out := buf.String()
	require.Contains(t, out, "8.2.1")
	require.Contains(t, out, "CONSOLE")
}

// TestVersion_ClusterAPI_ServerError verifies that a server-side failure on
// GET /version is surfaced as an error from the command.
func TestVersion_ClusterAPI_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/version", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	ac := fakeClient(t, f)

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf)
	require.Error(t, err)
	require.ErrorContains(t, err, "get cluster version")
}

// TestVersion_Client_BuildInfo verifies that `pve version client` reports CLI
// build information without contacting the server (API may be nil).
func TestVersion_Client_BuildInfo(t *testing.T) {
	deps := &cli.Deps{API: nil, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "client"))

	out := buf.String()
	require.Contains(t, out, selfversion.Version)
	require.Contains(t, out, "GO")
	require.Contains(t, out, "OS/ARCH")
}

// TestVersion_Client_JSON verifies the client build info renders as structured
// JSON carrying the version and toolchain fields.
func TestVersion_Client_JSON(t *testing.T) {
	deps := &cli.Deps{API: nil, Out: output.New(), Format: output.FormatJSON}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "client"))

	out := buf.String()
	require.Contains(t, out, selfversion.Version)
	require.True(t, strings.Contains(out, `"version"`) || strings.Contains(out, `"Version"`),
		"client JSON must include a version field, got: %s", out)
}

// TestVersion_Client_JSONArchMatchesInfo verifies the JSON "arch" field is
// sourced from selfversion.GetInfo().Arch (the same source as the table), not a
// divergent runtime value, so the two render paths never disagree.
func TestVersion_Client_JSONArchMatchesInfo(t *testing.T) {
	deps := &cli.Deps{API: nil, Out: output.New(), Format: output.FormatJSON}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "client"))

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"client JSON must be valid; got: %s", buf.String())
	require.Equal(t, selfversion.GetInfo().Arch, parsed["arch"])
}

// TestVersion_Client_HasNoClientAnnotation verifies that the client sub-command
// is annotated so the root skips API client construction for it.
func TestVersion_Client_HasNoClientAnnotation(t *testing.T) {
	cmd := newGroupCmd(&cli.Deps{})
	var client *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "client" {
			client = c
		}
	}
	require.NotNil(t, client, "client sub-command must exist")
	require.Equal(t, "true", client.Annotations["noClient"])
}

// TestVersion_GroupRegistered verifies that importing this package self-registers
// a group factory named "version" with the cli root registry.
func TestVersion_GroupRegistered(t *testing.T) {
	root := cli.NewRootCmd()
	cli.AddGroups(root, &cli.Deps{})

	found := false
	for _, c := range root.Commands() {
		if c.Name() == "version" {
			found = true
		}
	}
	require.True(t, found, "version group must be registered with the root command")
}
