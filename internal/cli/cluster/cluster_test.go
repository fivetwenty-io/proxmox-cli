package cluster

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// withDeps overrides the package-local deps lookup so tests can inject a Deps
// built from the fake server without driving the root PersistentPreRunE. The
// returned function restores the previous lookup and must be deferred.
func withDeps(deps *cli.Deps) func() {
	prev := resolveDeps
	resolveDeps = func(_ *cobra.Command) *cli.Deps { return deps }
	return func() { resolveDeps = prev }
}

// run builds the cluster group command, captures output in buf, and executes it
// with the supplied args.
func run(buf *bytes.Buffer, args ...string) error {
	cmd := newClusterCmd(&cli.Deps{})
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// newFakeClient returns a FakePVE and a constructed APIClient pointing at it.
//
// FakePVE reports its address as Options.Host="host:port" with Port=0; the
// underlying client builds its base URL as "scheme://Host:Port", which would
// double the port. Split the host:port pair into the separate Host and Port
// fields the client expects so requests reach the fake server.
func newFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()
	f := testhelper.NewFakePVE(t)

	opts := f.Options
	if host, portStr, err := net.SplitHostPort(opts.Host); err == nil {
		port, perr := strconv.Atoi(portStr)
		require.NoError(t, perr)
		opts.Host = host
		opts.Port = port
	}

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	return f, ac
}

// TestClusterStatus_Table verifies that `pve cluster status` queries
// GET /cluster/status and renders the expected columns.
func TestClusterStatus_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"type":    "cluster",
				"name":    "prod",
				"id":      "cluster",
				"quorate": 1,
				"nodes":   2,
			},
			map[string]any{
				"type":   "node",
				"name":   "pve1",
				"id":     "node/pve1",
				"online": 1,
				"level":  "c",
				"nodeid": 7,
			},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "status"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/status", gotPath)

	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "QUORATE")
	require.Contains(t, out, "prod")
	require.Contains(t, out, "pve1")
	require.True(t, strings.Contains(out, "7"), "nodeid should render")
}

// TestClusterStatus_JSON verifies the status rows are emitted as structured JSON.
func TestClusterStatus_JSON(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{
			"type":   "node",
			"name":   "pve1",
			"online": 1,
			"ip":     "192.168.1.10",
		},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "status"))
	out := buf.String()
	require.Contains(t, out, "pve1")
	// Result.Raw fidelity: json output is the verbatim API array with native
	// field names and types — not the synthetic stringified table envelope.
	require.NotContains(t, out, "\"headers\"")
	require.Contains(t, out, "\"ip\"", "non-curated fields must survive in json output")
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Equal(t, float64(1), entries[0]["online"], "numeric fields must stay native, not stringified")
}

// TestClusterStatus_ServerError verifies a server failure surfaces as an error.
func TestClusterStatus_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "status"))
}

// TestClusterResources_TypeFilter verifies that `pve cluster resources --type vm`
// sends the type query parameter and renders resource rows.
func TestClusterResources_TypeFilter(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{
				"type":   "qemu",
				"id":     "qemu/100",
				"node":   "pve1",
				"name":   "web",
				"status": "running",
				"cpu":    0.5,
				"mem":    1024,
				"disk":   2048,
				"uptime": 3600,
			},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "resources", "--type", "vm"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/resources", gotPath)
	require.Contains(t, gotQuery, "type=vm")

	out := buf.String()
	require.Contains(t, out, "qemu/100")
	require.Contains(t, out, "web")
	// Result.Raw fidelity: verbatim API array, native numeric types, no envelope.
	require.NotContains(t, out, "\"headers\"")
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Equal(t, float64(1024), entries[0]["mem"], "numeric fields must stay native, not stringified")
}

// TestClusterResources_NoFilter verifies that with no --type the query omits the
// type parameter and headers still render for an empty result.
func TestClusterResources_NoFilter(t *testing.T) {
	f, ac := newFakeClient(t)

	called := false
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.NotContains(t, r.URL.RawQuery, "type=")
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "resources"))
	require.True(t, called)
	require.Contains(t, buf.String(), "TYPE")
}

// TestClusterResources_ServerError verifies resource listing surfaces errors.
func TestClusterResources_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "resources"))
}

// TestClusterNextID_Plain verifies `pve cluster next-id` returns the next VMID.
func TestClusterNextID_Plain(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/nextid", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, json.RawMessage(`"100"`))
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "next-id"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/nextid", gotPath)
	require.Contains(t, buf.String(), "100")
}

// TestClusterNextID_VMIDHint verifies the optional --vmid hint is sent as a query
// parameter.
func TestClusterNextID_VMIDHint(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/nextid", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, json.RawMessage(`"205"`))
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "next-id", "--vmid", "205"))

	require.Contains(t, gotQuery, "vmid=205")
	require.Contains(t, buf.String(), "205")
}

// TestClusterNextID_JSON verifies the next-id value renders in JSON output.
func TestClusterNextID_JSON(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/nextid", json.RawMessage(`"100"`))

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "next-id"))
	require.Contains(t, buf.String(), "100")
}

// TestClusterNextID_ServerError verifies next-id surfaces server failures.
func TestClusterNextID_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/nextid", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "exhausted")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "next-id"))
}

// TestClusterCommandTree verifies the group exposes the expected sub-commands.
func TestClusterCommandTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	require.Equal(t, "cluster", root.Name())

	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"status", "resources", "next-id", "log", "tasks"} {
		require.True(t, names[want], "expected sub-command %q", want)
	}
}

// TestClusterGroupRegistered verifies importing this package self-registers a
// group factory named "cluster" with the cli root registry.
func TestClusterGroupRegistered(t *testing.T) {
	root := cli.NewRootCmd()
	cli.AddGroups(root, &cli.Deps{})

	found := false
	for _, c := range root.Commands() {
		if c.Name() == "cluster" {
			found = true
		}
	}
	require.True(t, found, "cluster group must be registered with the root command")
}
