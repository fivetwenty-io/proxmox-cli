package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// netRecordedRequest captures one HTTP request net.go issued to the fake PVE
// server, decoding a form-urlencoded (or, failing that, JSON) body into a
// plain string map so assertions can read field values directly.
type netRecordedRequest struct {
	method string
	path   string
	body   map[string]any
}

// netRecord installs a handler on f for pattern that appends every matching
// request to *rec (via netToOrder, when non-nil, also appending label to the
// shared call-order log) and replies with payload, or a PVE-shaped error
// when status is >= 400.
func netRecord(f *testhelper.FakePVE, rec *[]netRecordedRequest, order *[]string, label string, pattern string, payload any, status int) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
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
		*rec = append(*rec, netRecordedRequest{method: r.Method, path: r.URL.Path, body: body})
		if order != nil {
			*order = append(*order, label)
		}
		if status >= 400 {
			testhelper.WriteError(w, status, "boom")
			return
		}
		testhelper.WriteData(w, payload)
	})
}

// netTestLab returns a Lab definition fully populated for net apply tests:
// a clean (non-peppi) vnet ID, alias, VXLAN tag, CIDR, mgmt gateway, and
// MTU, so ensureLabSdnZone/Vnet/Subnet have every field they read.
func netTestLab(name string) *config.Lab {
	lab := cleanLab(name)
	lab.Network.VnetAlias = "lab-" + name
	lab.Network.VxlanTag = 5001
	lab.Network.Mgmt = config.LabMgmt{Gateway: "10.10.1.1"}
	lab.Network.MTU = 1450
	return lab
}

// buildNetCmd constructs `pmx lab net` wired to a *cli.Deps pointed at f and
// scoped to node, bypassing PersistentPreRunE via cli.WithDeps (the
// supported mechanism for group-package tests; see root.go).
func buildNetCmd(t *testing.T, configPath string, f *testhelper.FakePVE, node string) *cobra.Command {
	t.Helper()

	cfg, err := config.Load(configPath)
	require.NoError(t, err)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)

	deps := &cli.Deps{
		Cfg:        cfg,
		ConfigPath: configPath,
		API:        api,
		Out:        output.New(),
		Format:     output.FormatJSON,
		Node:       node,
	}

	cmd := newNetCmd()
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	return cmd
}

// runNetCmd executes cmd with args, capturing combined stdout/stderr.
func runNetCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

// TestNetApplyFreshCreatesZoneVnetSubnet covers a lab whose zone, vnet, and
// subnet do not exist yet: every create must be issued with the values from
// the lab's config, ListSdnDryRun must run before UpdateSdn, and UpdateSdn
// itself must run since the preview reports a non-empty pending changeset.
func TestNetApplyFreshCreatesZoneVnetSubnet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	var order []string
	var zoneRec, vnetRec, subnetRec, dryRunRec, applyRec []netRecordedRequest

	netRecord(f, &zoneRec, &order, "zone-list", "GET /api2/json/cluster/sdn/zones", []any{}, 200)
	netRecord(f, &zoneRec, &order, "zone-create", "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)
	netRecord(f, &vnetRec, &order, "vnet-list", "GET /api2/json/cluster/sdn/vnets", []any{}, 200)
	netRecord(f, &vnetRec, &order, "vnet-create", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	netRecord(f, &subnetRec, &order, "subnet-list",
		"GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{}, 200)
	netRecord(f, &subnetRec, &order, "subnet-create",
		"POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)
	netRecord(f, &dryRunRec, &order, "dry-run", "GET /api2/json/cluster/sdn/dry-run",
		map[string]any{"frr-diff": "+lab wayne zone", "interfaces-diff": "+lab wayne iface"}, 200)
	netRecord(f, &applyRec, &order, "apply", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	out, err := runNetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "applied")

	require.Len(t, zoneRec, 2, "expected one zone list + one zone create")
	create := zoneRec[1]
	assert.Equal(t, http.MethodPost, create.method)
	assert.Equal(t, lab.Network.EffectiveZoneName(), create.body["zone"])
	assert.Equal(t, lab.Network.EffectiveZoneType(), create.body["type"])
	assert.Empty(t, create.body["peers"])
	assert.Equal(t, "node1", create.body["nodes"])
	assert.Equal(t, "1450", create.body["mtu"])

	require.Len(t, vnetRec, 2, "expected one vnet list + one vnet create")
	vc := vnetRec[1]
	assert.Equal(t, http.MethodPost, vc.method)
	assert.Equal(t, "labwayne", vc.body["vnet"])
	assert.Equal(t, lab.Network.EffectiveZoneName(), vc.body["zone"])
	assert.Equal(t, "5001", vc.body["tag"])
	assert.Equal(t, "lab-wayne", vc.body["alias"])

	require.Len(t, subnetRec, 2, "expected one subnet list + one subnet create")
	sc := subnetRec[1]
	assert.Equal(t, http.MethodPost, sc.method)
	assert.Equal(t, "10.10.1.0/24", sc.body["subnet"])
	assert.Equal(t, "subnet", sc.body["type"])
	assert.Equal(t, "10.10.1.1", sc.body["gateway"])

	require.Len(t, dryRunRec, 1)
	require.Len(t, applyRec, 1)

	// ListSdnDryRun must run before UpdateSdn, and only after every create.
	require.Contains(t, order, "dry-run")
	require.Contains(t, order, "apply")
	dryRunIdx, applyIdx := indexOf(order, "dry-run"), indexOf(order, "apply")
	require.Less(t, dryRunIdx, applyIdx, "dry-run must precede apply")
	for _, createLabel := range []string{"zone-create", "vnet-create", "subnet-create"} {
		require.Less(t, indexOf(order, createLabel), dryRunIdx,
			"%s must precede the dry-run preview", createLabel)
	}
}

// TestNetApplyIdempotentSkipsCreatesAndSkipsApplyWhenNoPendingChanges covers
// a lab whose zone, vnet, and subnet already match its config exactly: no
// create or update call is issued for any of the three, the preview still
// runs, and — since the preview reports an empty changeset — UpdateSdn is
// skipped as a no-op.
func TestNetApplyIdempotentSkipsCreatesAndSkipsApplyWhenNoPendingChanges(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	var order []string
	var zoneRec, vnetRec, subnetRec, dryRunRec, applyRec []netRecordedRequest

	netRecord(f, &zoneRec, &order, "zone-list", "GET /api2/json/cluster/sdn/zones", []any{
		map[string]any{"zone": lab.Network.EffectiveZoneName(), "type": lab.Network.EffectiveZoneType(), "mtu": 1450, "nodes": "node1", "peers": lab.Network.EffectiveZonePeers()},
	}, 200)
	netRecord(f, &zoneRec, &order, "zone-create", "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)
	netRecord(f, &zoneRec, &order, "zone-update", "PUT /api2/json/cluster/sdn/zones/"+lab.Network.EffectiveZoneName(), map[string]any{}, 200)

	netRecord(f, &vnetRec, &order, "vnet-list", "GET /api2/json/cluster/sdn/vnets", []any{
		map[string]any{"vnet": "labwayne", "zone": lab.Network.EffectiveZoneName(), "tag": 5001, "alias": "lab-wayne"},
	}, 200)
	netRecord(f, &vnetRec, &order, "vnet-create", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	netRecord(f, &vnetRec, &order, "vnet-update", "PUT /api2/json/cluster/sdn/vnets/labwayne", map[string]any{}, 200)

	netRecord(f, &subnetRec, &order, "subnet-list", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{
		map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": "10.10.1.0/24", "gateway": "10.10.1.1", "zone": lab.Network.EffectiveZoneName()},
	}, 200)
	netRecord(f, &subnetRec, &order, "subnet-create",
		"POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)
	netRecord(f, &subnetRec, &order, "subnet-update",
		"PUT /api2/json/cluster/sdn/vnets/labwayne/subnets/labwayne-10.10.1.0-24", map[string]any{}, 200)

	netRecord(f, &dryRunRec, &order, "dry-run", "GET /api2/json/cluster/sdn/dry-run",
		map[string]any{"frr-diff": "", "interfaces-diff": ""}, 200)
	netRecord(f, &applyRec, &order, "apply", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	out, err := runNetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "No pending SDN configuration changes")

	require.Len(t, zoneRec, 1, "only the list call — no create or update")
	require.Len(t, vnetRec, 1, "only the list call — no create or update")
	require.Len(t, subnetRec, 1, "only the list call — no create or update")
	require.Len(t, dryRunRec, 1, "preview must still run")
	require.Empty(t, applyRec, "UpdateSdn must not run when the preview reports no pending changes")
}

// TestNetApplyDryRunSkipsApply covers --dry-run against a lab whose SDN
// objects do not exist: --dry-run must skip every zone/vnet/subnet ensure
// call entirely (no create, no list), render the preview, and never call
// UpdateSdn.
func TestNetApplyDryRunSkipsApply(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	var order []string
	var dryRunRec, applyRec []netRecordedRequest

	// No zone/vnet/subnet routes are registered at all: if net apply issued
	// any of those requests despite --dry-run, the fake server would 404 and
	// the command would fail, which the require.NoError below would catch.
	netRecord(f, &dryRunRec, &order, "dry-run", "GET /api2/json/cluster/sdn/dry-run",
		map[string]any{"frr-diff": "+would create lab wayne", "interfaces-diff": ""}, 200)
	netRecord(f, &applyRec, &order, "apply", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	out, err := runNetCmd(t, cmd, "apply", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "would create lab wayne")

	require.Len(t, dryRunRec, 1, "the preview must still run under --dry-run")
	require.Empty(t, applyRec, "UpdateSdn must never run under --dry-run")
}

// TestNetApplyDriftUpdatesVnetNotCreate covers a vnet that already exists
// but with a VXLAN tag that no longer matches the lab's config: the
// mismatch must produce an UpdateSdnVnets call, never a CreateSdnVnets call.
func TestNetApplyDriftUpdatesVnetNotCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	var order []string
	var zoneRec, vnetRec, subnetRec, dryRunRec, applyRec []netRecordedRequest

	netRecord(f, &zoneRec, &order, "zone-list", "GET /api2/json/cluster/sdn/zones", []any{
		map[string]any{"zone": lab.Network.EffectiveZoneName(), "type": lab.Network.EffectiveZoneType(), "mtu": 1450, "nodes": "node1", "peers": lab.Network.EffectiveZonePeers()},
	}, 200)
	netRecord(f, &zoneRec, &order, "zone-update", "PUT /api2/json/cluster/sdn/zones/"+lab.Network.EffectiveZoneName(), map[string]any{}, 200)

	// Existing vnet has the wrong tag (9999 instead of the lab's 5001); alias
	// and zone already match.
	netRecord(f, &vnetRec, &order, "vnet-list", "GET /api2/json/cluster/sdn/vnets", []any{
		map[string]any{"vnet": "labwayne", "zone": lab.Network.EffectiveZoneName(), "tag": 9999, "alias": "lab-wayne"},
	}, 200)
	netRecord(f, &vnetRec, &order, "vnet-create", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	netRecord(f, &vnetRec, &order, "vnet-update", "PUT /api2/json/cluster/sdn/vnets/labwayne", map[string]any{}, 200)

	netRecord(f, &subnetRec, &order, "subnet-list", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{
		map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": "10.10.1.0/24", "gateway": "10.10.1.1", "zone": lab.Network.EffectiveZoneName()},
	}, 200)
	netRecord(f, &subnetRec, &order, "subnet-update",
		"PUT /api2/json/cluster/sdn/vnets/labwayne/subnets/labwayne-10.10.1.0-24", map[string]any{}, 200)

	netRecord(f, &dryRunRec, &order, "dry-run", "GET /api2/json/cluster/sdn/dry-run",
		map[string]any{"frr-diff": "+tag change", "interfaces-diff": ""}, 200)
	netRecord(f, &applyRec, &order, "apply", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	out, err := runNetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "applied")

	require.Len(t, vnetRec, 2, "expected the list plus one update — no create")
	upd := vnetRec[1]
	assert.Equal(t, http.MethodPut, upd.method)
	assert.Equal(t, "5001", upd.body["tag"])

	require.Len(t, applyRec, 1)
}

// TestNetApplyPeppiRefusesBeforeAnySdnCall covers a lab whose vnet ID matches
// a peppi-protected pattern: net apply must refuse before issuing any SDN
// request at all, not merely before the destructive ones.
func TestNetApplyPeppiRefusesBeforeAnySdnCall(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("dirty")
	lab.Network.VnetID = "peppivn0"

	var order []string
	var rec []netRecordedRequest
	// Registered so that, if net apply mistakenly reached the API despite
	// the guard, the call would be recorded (and this test would fail on
	// the require.Empty(rec) assertion) instead of silently 404ing.
	netRecord(f, &rec, &order, "zone-list", "GET /api2/json/cluster/sdn/zones", []any{}, 200)
	netRecord(f, &rec, &order, "vnet-list", "GET /api2/json/cluster/sdn/vnets", []any{}, 200)
	netRecord(f, &rec, &order, "dry-run", "GET /api2/json/cluster/sdn/dry-run", map[string]any{}, 200)
	netRecord(f, &rec, &order, "apply", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	_, err := runNetCmd(t, cmd, "apply", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.ErrorContains(t, err, "peppivn0")
	require.Empty(t, rec, "no SDN request may be issued before the peppi guard runs")
}

// indexOf returns the index of the first occurrence of s in list, or -1.
func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
