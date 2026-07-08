package pdm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestSdnControllerLs_SortsByRemoteThenController asserts that `sdn
// controller ls` sorts entries by remote then controller.
func TestSdnControllerLs_SortsByRemoteThenController(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/sdn/controllers", []map[string]any{
		{"controller": "bgp1", "type": "bgp", "remote": "zeta", "asn": 65000},
		{"controller": "bgp2", "type": "bgp", "remote": "alpha"},
		{"controller": "bgp1", "type": "bgp", "remote": "alpha"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnControllerLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 3)
	require.Equal(t, "alpha", got[0]["remote"])
	require.Equal(t, "bgp1", got[0]["controller"])
	require.Equal(t, "alpha", got[1]["remote"])
	require.Equal(t, "bgp2", got[1]["controller"])
	require.Equal(t, "zeta", got[2]["remote"])
}

// TestSdnControllerLs_ValidatesTy asserts that `sdn controller ls` validates
// --ty against the enum before issuing any request.
func TestSdnControllerLs_ValidatesTy(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnControllerLsCmd(), "ls", "--ty", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--ty must be one of")
}

// TestSdnControllerLs_SendsFilterFlags asserts that `sdn controller ls`
// encodes its filter flags onto the expected query parameter names.
func TestSdnControllerLs_SendsFilterFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/sdn/controllers", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnControllerLsCmd(), "ls",
		"--pending", "--running", "--ty", "bgp", "--remote", "alpha", "--remote", "zeta")
	require.NoError(t, err)

	require.Equal(t, "1", rec.query.Get("pending"))
	require.Equal(t, "1", rec.query.Get("running"))
	require.Equal(t, "bgp", rec.query.Get("ty"))
	require.ElementsMatch(t, []string{"alpha", "zeta"}, rec.query["remotes"])
}

// TestSdnVnetLs_SortsByRemoteThenVnet asserts that `sdn vnet ls` sorts
// entries by remote then vnet.
func TestSdnVnetLs_SortsByRemoteThenVnet(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/sdn/vnets", []map[string]any{
		{"vnet": "vnet0", "type": "vnet", "remote": "zeta", "zone": "z1"},
		{"vnet": "vnet0", "type": "vnet", "remote": "alpha", "zone": "z1", "tag": 100},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnVnetLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["remote"])
	require.Equal(t, "zeta", got[1]["remote"])
}

// TestSdnVnetAdd_EncodesRemoteZonePairsAndAwaitsTask asserts that `sdn vnet
// add` parses "<remote>=<zone>" pairs into the JSON objects the API expects,
// and that the async UPID response is awaited before reporting success.
func TestSdnVnetAdd_EncodesRemoteZonePairsAndAwaitsTask(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	f.HandleFunc("POST /api2/json/sdn/vnets", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.form = r.PostForm
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnVnetAddCmd(), "add", "vnet0",
		"--remote", "alpha=zone1", "--remote", "beta=zone2", "--tag", "100")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)

	var entries []map[string]string
	require.NoError(t, json.Unmarshal([]byte(rec.form.Get("remotes")), &entries))
	require.Len(t, entries, 2)
	require.Equal(t, "alpha", entries[0]["remote"])
	require.Equal(t, "zone1", entries[0]["zone"])
	require.Equal(t, "beta", entries[1]["remote"])
	require.Equal(t, "zone2", entries[1]["zone"])

	require.Equal(t, "100", rec.form.Get("tag"))
	require.Contains(t, buf.String(), `VNet "vnet0" created.`)
}

// TestSdnVnetAdd_RejectsMalformedRemote asserts that `sdn vnet add` rejects
// a --remote value that isn't "<remote>=<zone>" before issuing any request.
func TestSdnVnetAdd_RejectsMalformedRemote(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnVnetAddCmd(), "add", "vnet0", "--remote", "alpha")
	require.Error(t, err)
	require.ErrorContains(t, err, "expected format <remote>=<zone>")
}

// TestSdnVnetAdd_Async asserts that with --async the command prints the
// UPID immediately without waiting for the task.
func TestSdnVnetAdd_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, true)

	f.HandleJSON("POST /api2/json/sdn/vnets", validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnVnetAddCmd(), "add", "vnet0", "--remote", "alpha=zone1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

// TestSdnZoneLs_ValidatesTy asserts that `sdn zone ls` validates --ty
// against the enum before issuing any request.
func TestSdnZoneLs_ValidatesTy(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnZoneLsCmd(), "ls", "--ty", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--ty must be one of")
}

// TestSdnZoneLs_SortsByRemoteThenZone asserts that `sdn zone ls` sorts
// entries by remote then zone.
func TestSdnZoneLs_SortsByRemoteThenZone(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/sdn/zones", []map[string]any{
		{"zone": "z0", "type": "simple", "remote": "zeta"},
		{"zone": "z0", "type": "simple", "remote": "alpha"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnZoneLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["remote"])
	require.Equal(t, "zeta", got[1]["remote"])
}

// TestSdnZoneAdd_EncodesRemoteWithoutController asserts that `sdn zone add`
// accepts a bare "<remote>" entry (no controller) since most zone types
// don't use one, and awaits the async UPID before reporting success.
func TestSdnZoneAdd_EncodesRemoteWithoutController(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	f.HandleFunc("POST /api2/json/sdn/zones", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.form = r.PostForm
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnZoneAddCmd(), "add", "zone0", "--remote", "alpha")
	require.NoError(t, err)

	var entries []map[string]string
	require.NoError(t, json.Unmarshal([]byte(rec.form.Get("remotes")), &entries))
	require.Len(t, entries, 1)
	require.Equal(t, "alpha", entries[0]["remote"])
	require.NotContains(t, entries[0], "controller")
	require.Contains(t, buf.String(), `Zone "zone0" created.`)
}

// TestSdnZoneAdd_EncodesRemoteWithController asserts that `sdn zone add`
// parses "<remote>=<controller>" pairs and includes the controller field.
func TestSdnZoneAdd_EncodesRemoteWithController(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	f.HandleFunc("POST /api2/json/sdn/zones", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.form = r.PostForm
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnZoneAddCmd(), "add", "zone0",
		"--remote", "alpha=ctrl1", "--vrf-vxlan", "42")
	require.NoError(t, err)

	var entries []map[string]string
	require.NoError(t, json.Unmarshal([]byte(rec.form.Get("remotes")), &entries))
	require.Len(t, entries, 1)
	require.Equal(t, "alpha", entries[0]["remote"])
	require.Equal(t, "ctrl1", entries[0]["controller"])
	require.Equal(t, "42", rec.form.Get("vrf-vxlan"))
}

// TestSdnZoneAdd_RejectsEmptyRemote asserts that `sdn zone add` rejects an
// empty --remote value before issuing any request.
func TestSdnZoneAdd_RejectsEmptyRemote(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSdnZoneAddCmd(), "add", "zone0", "--remote", "=ctrl1")
	require.Error(t, err)
	require.ErrorContains(t, err, "expected format <remote> or <remote>=<controller>")
}
