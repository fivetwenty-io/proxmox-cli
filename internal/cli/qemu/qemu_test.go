package qemu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// validUPID is a well-formed Proxmox UPID string whose node is "pve1". The
// tasks service parses the node from this string when blocking on completion.
const validUPID = "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:"

// run builds the qemu group command, injects deps via context, captures
// output in buf, and executes it with the supplied args.
func run(deps *cli.Deps, buf *bytes.Buffer, args ...string) error {
	return runWithStdin(deps, buf, nil, args...)
}

// runWithStdin is identical to run but sets cmd.SetIn(stdin) before Execute so
// tests can inject stdin per-command without touching the process-wide os.Stdin.
// Pass nil stdin to get the default cobra behaviour (reads os.Stdin).
func runWithStdin(deps *cli.Deps, buf *bytes.Buffer, stdin io.Reader, args ...string) error {
	cmd := Group(&cli.Deps{})
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	if stdin != nil {
		cmd.SetIn(stdin)
	}
	cmd.SetArgs(args)
	return cmd.Execute()
}

// newFakeClient returns a FakePVE and a constructed APIClient pointing at it.
func newFakeClient(t *testing.T) (*testhelper.FakePVE, *apiclient.APIClient) {
	t.Helper()
	f := testhelper.NewFakePVE(t)

	// The fake server's default Options.Host carries "host:port" while Port is
	// left at the client default (8006), which the client concatenates into an
	// invalid "host:port:8006" URL. Split the server URL into discrete host and
	// numeric port so the constructed client targets the fake correctly.
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

// depsFor builds a Deps with the given client, format, and node.
func depsFor(t *testing.T, ac *apiclient.APIClient, format output.Format, node string, async bool) *cli.Deps {
	t.Helper()
	return &cli.Deps{API: ac, Out: output.New(), Format: format, Node: node, Async: async}
}

// handleClusterResources registers a cluster/resources inventory placing one
// qemu VM on the given node, so migration source resolution (which does not
// trust the ambient deps.Node default as the VM's location) can find the guest.
func handleClusterResources(f *testhelper.FakePVE, vmid int, node string) {
	f.HandleJSON("GET /api2/json/cluster/resources", []any{
		map[string]any{"type": "qemu", "vmid": vmid, "node": node},
	})
}

// handleTaskStatus registers a terminal "stopped/OK" task-status response so a
// blocking lifecycle command completes immediately.
func handleTaskStatus(f *testhelper.FakePVE, upid string) {
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "OK",
		"upid":       upid,
	})
}

// readBody fully reads and returns the request body as a string.
func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	return string(b)
}

// parseForm decodes an application/x-www-form-urlencoded request body into
// url.Values so individual parameter values can be asserted.
func parseForm(t *testing.T, body string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(body)
	require.NoError(t, err)
	return v
}

// --- list -----------------------------------------------------------------

func TestQemuList_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"vmid":     100,
				"name":     "web",
				"status":   "running",
				"mem":      1024,
				"bootdisk": "scsi0",
				"pid":      4242,
			},
		})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "list"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu", gotPath)

	out := buf.String()
	require.Contains(t, out, "VMID")
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "100")
	require.Contains(t, out, "web")
	require.Contains(t, out, "running")
	require.Contains(t, out, "pve1")
}

func TestQemuList_FullFlagQuery(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "list", "--full"))
	require.Contains(t, gotQuery, "full=1")
}

func TestQemuList_NoNode(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "list")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node")
}

func TestQemuList_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "list")
	require.Error(t, err)
	require.ErrorContains(t, err, "list VMs on node")
}

// --- status ---------------------------------------------------------------

func TestQemuStatus_Single(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"vmid":      100,
			"name":      "web",
			"status":    "running",
			"qmpstatus": "running",
			"cpu":       0.25,
			"mem":       1024,
			"maxmem":    4096,
			"maxdisk":   8192,
			"uptime":    3600,
			"pid":       4242,
		})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "status", "100"))

	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/current", gotPath)
	out := buf.String()
	require.Contains(t, out, "web")
	require.Contains(t, out, "running")
	require.Contains(t, out, "100")
}

// TestQemuStatus_JSONLossless verifies `qemu status -o json` emits the full
// typed response (Raw) with native numeric types, not the stringified table
// subset.
func TestQemuStatus_JSONLossless(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"vmid": 100, "name": "web", "status": "running",
			"cpu": 0.25, "mem": 1024, "maxmem": 4096, "netin": 555, "netout": 666,
		})
	})

	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "status", "100"))

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"status JSON must be valid; got: %s", buf.String())
	// Numeric fields keep native JSON number type, and fields not in the table
	// (netin/netout) survive.
	require.Equal(t, float64(1024), parsed["mem"])
	require.Contains(t, parsed, "netin")
	require.Contains(t, parsed, "netout")
}

func TestQemuStatus_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such vm")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "status", "100"))
}

// --- config get -----------------------------------------------------------

func TestQemuConfigGet_Single(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"cores":  4,
			"memory": 4096,
			"name":   "web",
			"boot":   "order=scsi0",
			"net0":   "virtio=AA:BB:CC,bridge=vmbr0",
			"scsi0":  "local-lvm:vm-100-disk-0,size=32G",
		})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "get", "100", "--current"))

	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/config", gotPath)
	require.Contains(t, gotQuery, "current=1")
	out := buf.String()
	require.Contains(t, out, "cores")
	require.Contains(t, out, "net0")
	require.Contains(t, out, "web")
}

func TestQemuConfigGet_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "config", "get", "100"))
}

// TestQemuConfigGet_DynamicKeysPreserved is a regression guard for the raw
// decode path in newConfigGetCmd: nodes.ListQemuConfigResponse models
// indexed device keys with literal placeholder field names (Netn tagged
// json:"net[n]", Scsin tagged json:"scsi[n]", …), so it can never bind an
// actual response key like "net0" or "scsi0" to any field, typed or not. If
// this test starts failing after a refactor toward the typed struct, that
// refactor has reintroduced silent data loss for every dynamically indexed
// device key — do not "fix" the test, fix the regression.
func TestQemuConfigGet_DynamicKeysPreserved(t *testing.T) {
	f, ac := newFakeClient(t)

	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"cores": 4,
			"net0":  "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0",
			"scsi0": "local-lvm:vm-100-disk-0,size=32G",
		})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "get", "100"))

	out := buf.String()
	require.Contains(t, out, "cores", "statically named field must render")
	require.Contains(t, out, "4", "statically named field's value must render")
	require.Contains(t, out, "net0", "dynamically indexed net key must survive the raw decode")
	require.Contains(t, out, "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0", "net0 value must survive the raw decode")
	require.Contains(t, out, "scsi0", "dynamically indexed scsi key must survive the raw decode")
	require.Contains(t, out, "local-lvm:vm-100-disk-0,size=32G", "scsi0 value must survive the raw decode")
}

// --- config set -----------------------------------------------------------

func TestQemuConfigSet_TypedFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--cores", "8", "--memory", "8192", "--name", "db", "--net0", "virtio,bridge=vmbr0"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/config", gotPath)
	form := parseForm(t, body)
	require.Equal(t, "8", form.Get("cores"))
	require.Equal(t, "8192", form.Get("memory"))
	require.Equal(t, "db", form.Get("name"))
	require.Equal(t, "virtio,bridge=vmbr0", form.Get("net0"))
	require.Contains(t, buf.String(), "updated")
}

func TestQemuConfigSet_DeleteKeys(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--delete", "net1,scsi1"))
	require.Equal(t, "net1,scsi1", parseForm(t, body).Get("delete"))
}

// TestQemuConfigSet_Agent verifies --agent toggles the guest-agent channel by
// landing as the PVE "agent" option string in the update request body.
func TestQemuConfigSet_Agent(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--agent", "1"))
	require.Equal(t, "1", parseForm(t, body).Get("agent"))
}

func TestQemuConfigSet_NoChanges(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "set", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no configuration")
}

func TestQemuConfigSet_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "bad param")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "config", "set", "100", "--cores", "2"))
}

// TestQemuConfigSet_CloudInit verifies the cloud-init scalar flags and
// --balloon land as their PVE option keys in the update request body.
func TestQemuConfigSet_CloudInit(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--balloon", "0",
		"--ciuser", "ubuntu",
		"--nameserver", "1.1.1.1",
		"--searchdomain", "peppi.internal",
		"--citype", "nocloud",
		"--ciupgrade"))

	form := parseForm(t, body)
	require.Equal(t, "0", form.Get("balloon"))
	require.Equal(t, "ubuntu", form.Get("ciuser"))
	require.Equal(t, "1.1.1.1", form.Get("nameserver"))
	require.Equal(t, "peppi.internal", form.Get("searchdomain"))
	require.Equal(t, "nocloud", form.Get("citype"))
	require.Equal(t, "1", form.Get("ciupgrade"))
}

// TestQemuConfigSet_OnbootStartup verifies the boot-time flags land as their PVE
// option keys (onboot as the PVEBool "1", startup as the order/up/down string).
func TestQemuConfigSet_OnbootStartup(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--onboot",
		"--startup", "order=1,up=30,down=60"))

	form := parseForm(t, body)
	require.Equal(t, "1", form.Get("onboot"))
	require.Equal(t, "order=1,up=30,down=60", form.Get("startup"))
}

// TestQemuConfigSet_SSHKeysEncoded verifies the public key is percent-encoded
// before transport: PVE uri_unescapes the sshkeys value but does NOT treat '+'
// as space, so spaces must be sent as %20 (and '+'/'='/'@' as %2B/%3D/%40).
func TestQemuConfigSet_SSHKeysEncoded(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--sshkeys", "ssh-ed25519 AAAAB3+/= ubuntu@host"))

	// parseForm decodes the form transport layer once, yielding the value the
	// CLI placed on the wire — which must itself be percent-encoded for PVE.
	got := parseForm(t, body).Get("sshkeys")
	require.Equal(t, "ssh-ed25519%20AAAAB3%2B%2F%3D%20ubuntu%40host", got)
	require.NotContains(t, got, " ", "sshkeys must not contain raw spaces")
}

// TestQemuConfigSet_IndexedSlots verifies that multiple indexed slots (net0 +
// net1, ipconfig0 + ipconfig1, ide2, virtio1) coexist in one update request.
func TestQemuConfigSet_IndexedSlots(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--net0", "virtio,bridge=vmbr1,firewall=1",
		"--net1", "virtio,bridge=peppivn0,firewall=1",
		"--ipconfig0", "ip=192.168.1.63/24,gw=192.168.1.1",
		"--ipconfig1", "ip=10.43.0.5/24",
		"--ide2", "zfs-0:cloudinit",
		"--virtio1", "zfs-0:32"))

	form := parseForm(t, body)
	require.Equal(t, "virtio,bridge=vmbr1,firewall=1", form.Get("net0"))
	require.Equal(t, "virtio,bridge=peppivn0,firewall=1", form.Get("net1"))
	require.Equal(t, "ip=192.168.1.63/24,gw=192.168.1.1", form.Get("ipconfig0"))
	require.Equal(t, "ip=10.43.0.5/24", form.Get("ipconfig1"))
	require.Equal(t, "zfs-0:cloudinit", form.Get("ide2"))
	require.Equal(t, "zfs-0:32", form.Get("virtio1"))
}

// --- config pending -------------------------------------------------------

func TestQemuConfigPending_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/pending", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"key": "cores", "value": 4, "pending": 8},
			map[string]any{"key": "memory", "value": 4096},
		})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "pending", "100"))
	out := buf.String()
	require.Contains(t, out, "KEY")
	require.Contains(t, out, "cores")
	require.Contains(t, out, "memory")
}

func TestQemuConfigPending_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/pending", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "config", "pending", "100"))
}

// --- lifecycle: start (blocking + async) ----------------------------------

func TestQemuStart_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "start", "100"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/start", gotPath)
	require.Contains(t, buf.String(), "started")
}

func TestQemuStart_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("POST /api2/json/nodes/pve1/qemu/100/status/start", validUPID)

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "start", "100"))
	require.Contains(t, buf.String(), validUPID)
}

// TestQemuStart_AsyncJSONShape verifies the --async UPID is emitted as a JSON
// object {"upid": "..."} rather than a bare quoted string, matching the lxc and
// node-services async shape.
func TestQemuStart_AsyncJSONShape(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("POST /api2/json/nodes/pve1/qemu/100/status/start", validUPID)

	deps := depsFor(t, ac, output.FormatJSON, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "start", "100"))

	var parsed map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"async JSON must be an object; got: %s", buf.String())
	require.Equal(t, validUPID, parsed["upid"])
}

func TestQemuStart_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "start", "100", "--timeout", "120", "--migratedfrom", "pve2"))
	combined := gotQuery + body
	require.Contains(t, combined, "migratedfrom")
	require.Contains(t, combined, "pve2")
}

func TestQemuStart_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "start", "100"))
}

// --- lifecycle: stop / reboot / shutdown / reset / suspend / resume --------

func TestQemuStop_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "stop", "100"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/stop", gotPath)
	require.Contains(t, buf.String(), validUPID)
}

func TestQemuReboot_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/reboot", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "reboot", "100"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/reboot", gotPath)
}

func TestQemuShutdown_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/shutdown", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "shutdown", "100", "--force-stop"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/shutdown", gotPath)
	require.Contains(t, body, "forceStop")
	require.Contains(t, buf.String(), "shut down")
}

func TestQemuReset_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/reset", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "reset", "100"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/reset", gotPath)
}

func TestQemuSuspend_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/suspend", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "suspend", "100", "--todisk"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/suspend", gotPath)
	require.Contains(t, body, "todisk")
}

func TestQemuResume_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/resume", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "resume", "100"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/status/resume", gotPath)
}

// --- delete ---------------------------------------------------------------

func TestQemuDelete_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "delete", "100", "--yes", "--purge"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100", gotPath)
	require.Contains(t, gotQuery, "purge=1")
	require.Contains(t, buf.String(), validUPID)
}

func TestQemuDelete_RequiresConfirmation(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	err := run(deps, &buf, "delete", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirm")
}

func TestQemuDelete_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "delete", "100", "--yes"))
}

// --- snapshot list / create / delete / rollback ---------------------------

func TestQemuSnapshotList_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/snapshot", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{
				"name":        "pre-upgrade",
				"description": "before kernel upgrade",
				"snaptime":    1700000000,
				"vmstate":     1,
				"parent":      "current",
			},
			map[string]any{"name": "current"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "list", "100"))
	out := buf.String()
	require.Contains(t, out, "SNAPNAME")
	require.Contains(t, out, "pre-upgrade")
	require.Contains(t, out, "before kernel upgrade")
}

func TestQemuSnapshotList_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/snapshot", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "snapshot", "list", "100"))
}

func TestQemuSnapshotCreate_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/snapshot", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "create", "100", "pre-upgrade",
		"--description", "before kernel upgrade", "--vmstate"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/snapshot", gotPath)
	form := parseForm(t, body)
	require.Equal(t, "pre-upgrade", form.Get("snapname"))
	require.Equal(t, "before kernel upgrade", form.Get("description"))
	require.Equal(t, "1", form.Get("vmstate"))
	require.Contains(t, buf.String(), "pre-upgrade")
}

func TestQemuSnapshotCreate_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("POST /api2/json/nodes/pve1/qemu/100/snapshot", validUPID)
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "create", "100", "snap1"))
	require.Contains(t, buf.String(), validUPID)
}

func TestQemuSnapshotDelete_Async(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/qemu/100/snapshot/snap1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "delete", "100", "snap1", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/snapshot/snap1", gotPath)
	require.Contains(t, buf.String(), validUPID)
}

func TestQemuSnapshotDelete_RequiresConfirmation(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	err := run(deps, &buf, "snapshot", "delete", "100", "snap1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirm")
}

func TestQemuSnapshotRollback_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/snapshot/snap1/rollback", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "snapshot", "rollback", "100", "snap1", "--yes"))
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/snapshot/snap1/rollback", gotPath)
	require.Contains(t, buf.String(), "rolled back")
}

func TestQemuSnapshotRollback_NoYes_Refuses(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "snapshot", "rollback", "100", "snap1")
	require.Error(t, err)
	require.ErrorContains(t, err, "--yes")
}

func TestQemuSnapshotRollback_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/snapshot/snap1/rollback", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "snapshot", "rollback", "100", "snap1", "--yes"))
}

// --- create ---------------------------------------------------------------

// TestQemuCreate_CloudInit verifies the cloud-init flag group lands in the
// CreateQemu request body as the corresponding PVE option keys.
func TestQemuCreate_CloudInit(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100",
		"--ciuser", "pveadmin",
		"--cipassword", "s3cret",
		"--citype", "nocloud",
		"--ciupgrade",
		"--cicustom", "user=local:snippets/user.yml",
		"--nameserver", "10.241.0.1",
		"--searchdomain", "pmx-cli.local",
		"--sshkeys", "ssh-ed25519 AAAA test@host",
		"--ipconfig0", "ip=dhcp"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu", gotPath)
	form := parseForm(t, body)
	require.Equal(t, "pveadmin", form.Get("ciuser"))
	require.Equal(t, "s3cret", form.Get("cipassword"))
	require.Equal(t, "nocloud", form.Get("citype"))
	require.Equal(t, "1", form.Get("ciupgrade"))
	require.Equal(t, "user=local:snippets/user.yml", form.Get("cicustom"))
	require.Equal(t, "10.241.0.1", form.Get("nameserver"))
	require.Equal(t, "pmx-cli.local", form.Get("searchdomain"))
	// sshkeys are percent-encoded for PVE (spaces as %20), matching config set.
	require.Equal(t, "ssh-ed25519%20AAAA%20test%40host", form.Get("sshkeys"))
	require.Equal(t, "ip=dhcp", form.Get("ipconfig0"))
}

// TestQemuCreate_NoCloudInitOmitted verifies cloud-init keys are absent from the
// request body when their flags are not set.
func TestQemuCreate_NoCloudInitOmitted(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--name", "plain"))

	form := parseForm(t, body)
	for _, k := range []string{"ciuser", "cipassword", "citype", "ciupgrade",
		"cicustom", "nameserver", "searchdomain", "sshkeys", "ipconfig0"} {
		require.Empty(t, form.Get(k), "cloud-init key %q must be omitted when unset", k)
	}
}

// TestQemuCreate_Agent verifies --agent lands as the PVE "agent" option string
// in the CreateQemu request body, and is omitted from the body when unset.
func TestQemuCreate_Agent(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--agent", "enabled=1,fstrim_cloned_disks=1"))
	require.Equal(t, "enabled=1,fstrim_cloned_disks=1", parseForm(t, body).Get("agent"))

	body = ""
	require.NoError(t, run(deps, &buf, "create", "101", "--name", "plain"))
	require.Empty(t, parseForm(t, body).Get("agent"))
}

// --- JSON output ----------------------------------------------------------

func TestQemuList_JSON(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu", []any{
		map[string]any{"vmid": 100, "name": "web", "status": "running"},
	})
	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "list"))
	out := buf.String()
	require.True(t, strings.Contains(out, "web"))
	// list JSON must be a typed array (Raw), not a synthetic {headers, rows}
	// object, with native numeric vmid.
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &decoded),
		"list JSON must be an array; got: %s", out)
	require.Len(t, decoded, 1)
	require.Equal(t, float64(100), decoded[0]["vmid"])
	require.Equal(t, "web", decoded[0]["name"])
}

// --- command tree / registration ------------------------------------------

func TestQemuCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	require.Equal(t, "qemu", root.Name())

	names := make(map[string]*cobra.Command)
	for _, c := range root.Commands() {
		names[c.Name()] = c
	}
	for _, want := range []string{
		"list", "status", "config", "start", "stop", "reboot",
		"shutdown", "reset", "suspend", "resume", "delete", "snapshot",
	} {
		require.Contains(t, names, want, "expected sub-command %q", want)
	}

	cfgNames := make(map[string]bool)
	for _, c := range names["config"].Commands() {
		cfgNames[c.Name()] = true
	}
	for _, want := range []string{"get", "set", "pending"} {
		require.True(t, cfgNames[want], "expected config sub-command %q", want)
	}

	snapNames := make(map[string]bool)
	for _, c := range names["snapshot"].Commands() {
		snapNames[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "delete", "rollback"} {
		require.True(t, snapNames[want], "expected snapshot sub-command %q", want)
	}
}

func TestQemuGroupRegistered(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{Group})

	found := false
	for _, c := range root.Commands() {
		if c.Name() == "qemu" {
			found = true
		}
	}
	require.True(t, found, "qemu group must be registered with the root command")
}
