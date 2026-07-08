package version

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
	selfversion "github.com/fivetwenty-io/pmx-cli/internal/version"
)

// fakePBSClient returns a FakePBS test server and a PBSClient pointed at it.
func fakePBSClient(t *testing.T) (*testhelper.FakePBS, *apiclient.PBSClient) {
	t.Helper()

	f := testhelper.NewFakePBS(t)
	pc, err := apiclient.NewPBSClient(f.Options)
	require.NoError(t, err)

	return f, pc
}

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

// run builds the version group command, injects deps via context, captures
// output in buf, and executes it with the supplied args.
func run(deps *cli.Deps, buf *bytes.Buffer, args ...string) error {
	cmd := Group(&cli.Deps{})
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// TestVersion_ClusterAPI_Table verifies that `pmx version` queries GET /version
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

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf))

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

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf))

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

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf))

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

	var buf bytes.Buffer
	err := run(deps, &buf)
	require.Error(t, err)
	require.ErrorContains(t, err, "get cluster version")
}

// TestVersion_Client_BuildInfo verifies that `pmx version client` reports CLI
// build information without contacting the server (API may be nil).
func TestVersion_Client_BuildInfo(t *testing.T) {
	deps := &cli.Deps{API: nil, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "client"))

	out := buf.String()
	require.Contains(t, out, selfversion.Version)
	require.Contains(t, out, "GO")
	require.Contains(t, out, "OS/ARCH")
}

// TestVersion_Client_JSON verifies the client build info renders as structured
// JSON carrying the version and toolchain fields.
func TestVersion_Client_JSON(t *testing.T) {
	deps := &cli.Deps{API: nil, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "client"))

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

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "client"))

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"client JSON must be valid; got: %s", buf.String())
	require.Equal(t, selfversion.GetInfo().Arch, parsed["arch"])
}

// TestVersion_Client_HasNoClientAnnotation verifies that the client sub-command
// is annotated so the root skips API client construction for it.
func TestVersion_Client_HasNoClientAnnotation(t *testing.T) {
	cmd := Group(&cli.Deps{})
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
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{Group})

	found := false
	for _, c := range root.Commands() {
		if c.Name() == "version" {
			found = true
		}
	}
	require.True(t, found, "version group must be registered with the root command")
}

// TestGroup_IsProductContext verifies that the version group is annotated
// product:context so the root resolves the active client (PVE or PBS) from
// the active context rather than defaulting to PVE, and that it carries both
// the "client" and "ping" sub-commands.
func TestGroup_IsProductContext(t *testing.T) {
	cmd := Group(&cli.Deps{})
	require.Equal(t, cli.ProductFromContext, cmd.Annotations[cli.ProductAnnotation])

	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["client"])
	require.True(t, names["ping"])
}

// ---------------------------------------------------------------------------
// PBS render branch (deps.PBS set, deps.API nil)
// ---------------------------------------------------------------------------

// TestVersion_PBS_Table verifies that `pmx version` against a PBS context
// queries GET /version on the PBS client and renders the PBS version fields.
func TestVersion_PBS_Table(t *testing.T) {
	f, pc := fakePBSClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"release": "3",
			"repoid":  "abc123de",
			"version": "3.4",
		})
	})

	deps := &cli.Deps{PBS: pc, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/version", gotPath)

	out := buf.String()
	require.Contains(t, out, "3.4")
	require.Contains(t, out, "abc123de")
}

// TestVersion_PBS_JSON verifies that JSON output for a PBS context carries the
// typed PBS version response fields.
func TestVersion_PBS_JSON(t *testing.T) {
	_, pc := fakePBSClient(t)

	deps := &cli.Deps{PBS: pc, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf))

	out := buf.String()
	require.Contains(t, out, `"version": "3.4"`)
	require.Contains(t, out, `"release": "3"`)
	require.Contains(t, out, `"repoid": "abc123de"`)
}

// TestVersion_PBS_ServerError verifies that a server-side failure on GET
// /version for a PBS context is surfaced as an error from the command.
func TestVersion_PBS_ServerError(t *testing.T) {
	f, pc := fakePBSClient(t)
	f.HandleFunc("GET /api2/json/version", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{PBS: pc, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf)
	require.Error(t, err)
	require.ErrorContains(t, err, "get server version")
}

// ---------------------------------------------------------------------------
// ping
// ---------------------------------------------------------------------------

// TestPing_RendersDefaultFakeResponse verifies that `pmx version ping` reports
// connectivity against a PBS context.
func TestPing_RendersDefaultFakeResponse(t *testing.T) {
	_, pc := fakePBSClient(t)
	deps := &cli.Deps{PBS: pc, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ping"))
	require.Contains(t, buf.String(), "true")
}

// TestPing_JSONOutputContainsPong verifies the JSON render carries the typed
// pong boolean.
func TestPing_JSONOutputContainsPong(t *testing.T) {
	f, pc := fakePBSClient(t)
	deps := &cli.Deps{PBS: pc, Out: output.New(), Format: output.FormatJSON}

	var gotPath string
	f.HandleFunc("GET /api2/json/ping", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"pong": true})
	})

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ping"))
	require.Equal(t, "/api2/json/ping", gotPath)
	require.Contains(t, buf.String(), `"pong": true`)
}

// TestPing_ServerErrorSurfaced verifies that a server-side failure on GET
// /ping is surfaced as an error from the command.
func TestPing_ServerErrorSurfaced(t *testing.T) {
	f, pc := fakePBSClient(t)
	f.HandleFunc("GET /api2/json/ping", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "daemon offline")
	})
	deps := &cli.Deps{PBS: pc, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ping")
	require.Error(t, err)
	require.ErrorContains(t, err, "daemon offline")
}

// TestPing_RequiresPBSContext verifies that `pmx version ping` against a PVE
// context (deps.PBS nil) fails with a clear error instead of a nil-pointer
// panic.
func TestPing_RequiresPBSContext(t *testing.T) {
	deps := &cli.Deps{API: nil, PBS: nil, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ping")
	require.Error(t, err)
	require.ErrorContains(t, err, "ping is only available for a PBS context")
}
