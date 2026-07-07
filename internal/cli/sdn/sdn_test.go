package sdn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// recordedRequest captures a single HTTP request a command issued to the fake
// PVE server so tests can assert on method, path, and decoded body.
type recordedRequest struct {
	method string
	path   string
	body   map[string]any
	query  url.Values
}

// writeConfig writes a token-auth config pointing the named target at the fake
// PVE server (HTTP, TLS verification off) and returns the config file path.
func writeConfig(t *testing.T, f *testhelper.FakePVE) string {
	t.Helper()

	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err, "split fake server host:port")

	cfg := fmt.Sprintf(`current-context: fake
contexts:
  fake:
    host: %s
    port: %s
    protocol: http
    realm: pam
    tls:
      insecure: true
    auth:
      type: token
      username: root
      token-id: test
      secret: 00000000-0000-0000-0000-000000000000
`, host, portStr)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

// run drives the real root command (so PersistentPreRunE wires live Deps) with
// the sdn group attached, executes it with the given args, and returns combined
// stdout+stderr.
func run(t *testing.T, f *testhelper.FakePVE, in string, args ...string) (string, error) {
	t.Helper()

	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")
	t.Setenv("PMX_CONTEXT", "")

	cfgPath := writeConfig(t, f)

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(Group(&cli.Deps{}))

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if in != "" {
		root.SetIn(strings.NewReader(in))
	}

	full := append([]string{"--config", cfgPath, "--output", "table", "sdn"}, args...)
	root.SetArgs(full)

	err := root.Execute()
	return buf.String(), err
}

// record installs a handler for pattern that captures the request and replies
// with payload (or a PVE-shaped error when status >= 400).
func record(f *testhelper.FakePVE, rec *[]recordedRequest, pattern string, payload any, status int) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		// PVE write requests are form-urlencoded; capture each field as a string,
		// falling back to JSON for any non-form bodies.
		if err := r.ParseForm(); err == nil {
			for k, v := range r.PostForm {
				if len(v) > 0 {
					body[k] = v[0]
				}
			}
		}
		if len(body) == 0 {
			if b, _ := io.ReadAll(r.Body); len(b) > 0 {
				_ = json.Unmarshal(b, &body)
			}
		}
		*rec = append(*rec, recordedRequest{method: r.Method, path: r.URL.Path, body: body, query: r.URL.Query()})
		if status >= 400 {
			testhelper.WriteError(w, status, "boom")
			return
		}
		testhelper.WriteData(w, payload)
	})
}

// --- zones ---

func TestZoneList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/zones", []any{
		map[string]any{"zone": "pmxcli", "type": "simple", "nodes": "pve1", "ipam": "pve"},
		map[string]any{"zone": "vlanz", "type": "vlan", "nodes": "", "ipam": ""},
	}, 200)

	out, err := run(t, f, "", "zone", "list")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli")
	require.Contains(t, out, "simple")
	require.Contains(t, out, "vlanz")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/zones", rec[0].path)
}

func TestZoneListError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/zones", nil, 500)

	_, err := run(t, f, "", "zone", "list")
	require.Error(t, err)
}

func TestZoneCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)

	out, err := run(t, f, "", "zone", "create", "pmxcli",
		"--type", "vlan", "--nodes", "pve1", "--bridge", "vmbr0", "--ipam", "pve")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli")
	require.Contains(t, out, "created")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/zones", rec[0].path)
	require.Equal(t, "pmxcli", rec[0].body["zone"])
	require.Equal(t, "vlan", rec[0].body["type"])
	require.Equal(t, "pve1", rec[0].body["nodes"])
	require.Equal(t, "vmbr0", rec[0].body["bridge"])
	require.Equal(t, "pve", rec[0].body["ipam"])
}

// TestZoneCreateOmitsUnsetFlags verifies optional fields are sent only when the
// flag was explicitly set, so a simple zone create stays minimal.
func TestZoneCreateOmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "create", "pmxcli")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "pmxcli", rec[0].body["zone"])
	require.Equal(t, "simple", rec[0].body["type"], "type defaults to simple")
	require.NotContains(t, rec[0].body, "nodes")
	require.NotContains(t, rec[0].body, "bridge")
	require.NotContains(t, rec[0].body, "ipam")
}

func TestZoneCreateError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/zones", nil, 400)

	_, err := run(t, f, "", "zone", "create", "pmxcli")
	require.Error(t, err)
	require.ErrorContains(t, err, "create SDN zone")
}

func TestZoneDeleteWithoutConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "delete", "pmxcli")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec, "no request must be issued without --yes")
}

func TestZoneDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	out, err := run(t, f, "", "zone", "delete", "pmxcli", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/zones/pmxcli", rec[0].path)
}

func TestZoneDeleteError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/zones/pmxcli", nil, 500)

	_, err := run(t, f, "", "zone", "delete", "pmxcli", "--yes")
	require.Error(t, err)
	require.ErrorContains(t, err, "delete SDN zone")
	require.Len(t, rec, 1)
}

// --- vnets ---

func TestVnetList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets", []any{
		map[string]any{"vnet": "pmxcli0", "zone": "pmxcli", "tag": 100, "alias": "lab"},
		map[string]any{"vnet": "pmxcli1", "zone": "pmxcli", "tag": 0, "alias": ""},
	}, 200)

	out, err := run(t, f, "", "vnet", "list")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli0")
	require.Contains(t, out, "100")
	require.Contains(t, out, "lab")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/vnets", rec[0].path)
}

func TestVnetCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "create", "pmxcli0", "--zone", "pmxcli", "--tag", "100", "--alias", "lab")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli0")
	require.Contains(t, out, "created")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "pmxcli0", rec[0].body["vnet"])
	require.Equal(t, "pmxcli", rec[0].body["zone"])
	require.Equal(t, "100", rec[0].body["tag"])
	require.Equal(t, "lab", rec[0].body["alias"])
}

// TestVnetCreateRequiresZone verifies the --zone flag is mandatory and no
// request is issued when it is missing.
func TestVnetCreateRequiresZone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "create", "pmxcli0")
	require.Error(t, err)
	require.ErrorContains(t, err, "zone")
	require.Empty(t, rec, "no request must be issued when a required flag is missing")
}

func TestVnetCreateError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets", nil, 400)

	_, err := run(t, f, "", "vnet", "create", "pmxcli0", "--zone", "pmxcli")
	require.Error(t, err)
	require.ErrorContains(t, err, "create SDN vnet")
}

func TestVnetDeleteWithoutConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "delete", "pmxcli0")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestVnetDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{}, 200)

	out, err := run(t, f, "", "vnet", "delete", "pmxcli0", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0", rec[0].path)
}

// --- subnets ---

func TestSubnetList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets", []any{
		map[string]any{"subnet": "pmxcli-10.241.0.0-24", "cidr": "10.241.0.0/24", "gateway": "10.241.0.1", "zone": "pmxcli"},
	}, 200)

	out, err := run(t, f, "", "subnet", "list", "pmxcli0")
	require.NoError(t, err)
	require.Contains(t, out, "10.241.0.0/24")
	require.Contains(t, out, "10.241.0.1")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0/subnets", rec[0].path)
}

func TestSubnetCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pmxcli0/subnets", map[string]any{}, 200)

	out, err := run(t, f, "", "subnet", "create", "pmxcli0", "10.241.0.0/24", "--gateway", "10.241.0.1", "--snat")
	require.NoError(t, err)
	require.Contains(t, out, "10.241.0.0/24")
	require.Contains(t, out, "created")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn/vnets/pmxcli0/subnets", rec[0].path)
	require.Equal(t, "10.241.0.0/24", rec[0].body["subnet"])
	require.Equal(t, "subnet", rec[0].body["type"])
	require.Equal(t, "10.241.0.1", rec[0].body["gateway"])
	require.Equal(t, "1", rec[0].body["snat"])
}

func TestSubnetDeleteWithoutConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{}, 200)

	_, err := run(t, f, "", "subnet", "delete", "pmxcli0", "10.241.0.0-24")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestSubnetDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pmxcli0/subnets/10.241.0.0-24", map[string]any{}, 200)

	out, err := run(t, f, "", "subnet", "delete", "pmxcli0", "10.241.0.0-24", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

// --- apply ---

// TestApplyImmediate covers a server that returns no task UPID (null data),
// which the command treats as an immediate success.
func TestApplyImmediate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn", nil, 200)

	out, err := run(t, f, "", "apply")
	require.NoError(t, err)
	require.Contains(t, out, "applied")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/api2/json/cluster/sdn", rec[0].path)
}

// TestApplyAsync covers a server that returns a reload task UPID; with --async
// the command prints the UPID and does not wait for the task.
func TestApplyAsync(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	upid := "UPID:pve1:0000ABCD:00112233:00000000:srvreload:sdn:root@pam:"
	record(f, &rec, "PUT /api2/json/cluster/sdn", upid, 200)

	out, err := run(t, f, "", "apply", "--async")
	require.NoError(t, err)
	require.Contains(t, out, upid)
	require.Len(t, rec, 1, "async apply must not poll the task status endpoint")
}

func TestApplyError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn", nil, 500)

	_, err := run(t, f, "", "apply")
	require.Error(t, err)
	require.ErrorContains(t, err, "apply SDN configuration")
}

// ensure the package self-registers a factory of the right shape.
var _ func(*cli.Deps) *cobra.Command = Group
