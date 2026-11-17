package lab

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// zoneCreateTestLab is netTestLab with an explicit zone name and type and no
// zone_peers, the shape of a lab that points at a zone it did not build.
func zoneCreateTestLab(zoneType string) *config.Lab {
	lab := netTestLab("wayne")
	lab.Network.ZoneName = "haclvx"
	lab.Network.ZoneType = zoneType
	return lab
}

// TestLabZoneCreateIssue pins the rule for a zone pmx is about to create: a
// vxlan zone needs zone_peers, and only simple and vxlan can be built at all.
func TestLabZoneCreateIssue(t *testing.T) {
	withPeers := config.LabNetwork{ZoneName: "haclvx", ZoneType: "vxlan", ZonePeers: "192.0.2.10"}

	assert.NoError(t, labZoneCreateIssue("wayne", config.LabNetwork{}), "default simple zone")
	assert.NoError(t, labZoneCreateIssue("wayne", config.LabNetwork{ZoneType: "simple", ZonePeers: "192.0.2.10"}))
	assert.NoError(t, labZoneCreateIssue("wayne", withPeers))

	for _, peers := range []string{"", "  "} {
		err := labZoneCreateIssue("wayne", config.LabNetwork{ZoneName: "haclvx", ZoneType: "vxlan", ZonePeers: peers})
		require.Error(t, err, "peers %q", peers)
		assert.ErrorContains(t, err, "network.zone_peers")
		assert.ErrorContains(t, err, "peers or fabric")
		assert.ErrorContains(t, err, `"haclvx"`)
	}

	for _, zt := range []string{"vlan", "qinq", "evpn", "faucet", "vxlna"} {
		err := labZoneCreateIssue("wayne", config.LabNetwork{ZoneName: "haclvx", ZoneType: zt, ZonePeers: "192.0.2.10"})
		require.Error(t, err, "zone_type %q", zt)
		assert.ErrorContains(t, err, `"`+zt+`"`)
		assert.ErrorContains(t, err, "create that zone in PVE first")
	}
}

// zoneCreateRefusedCases are the labs whose zone, were it absent, pmx could not
// create, with the text the refusal must carry.
var zoneCreateRefusedCases = []struct {
	name     string
	zoneType string
	peers    string
	want     []string
}{
	{"vxlan without peers", "vxlan", "", []string{"network.zone_peers", "peers or fabric"}},
	{"vlan", "vlan", "", []string{`"vlan"`, "create that zone in PVE first"}},
	{"qinq", "qinq", "", []string{`"qinq"`, "create that zone in PVE first"}},
	{"evpn", "evpn", "", []string{`"evpn"`, "create that zone in PVE first"}},
	{"faucet", "faucet", "", []string{`"faucet"`, "create that zone in PVE first"}},
}

// TestCreate_AbsentZoneThatCannotBeBuilt_Refused covers the dry run and the
// real create against a zone that does not exist and that PVE would reject at
// the create step. Both refuse with the key or the type named, and no create
// request is made.
func TestCreate_AbsentZoneThatCannotBeBuilt_Refused(t *testing.T) {
	for _, tc := range zoneCreateRefusedCases {
		for _, args := range [][]string{
			{"wayne", "--node", "node1", "--dry-run"},
			{"wayne", "--node", "node1"},
		} {
			t.Run(tc.name+" "+args[len(args)-1], func(t *testing.T) {
				f := testhelper.NewFakePVE(t)
				f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{})
				createForbid(f, t, "POST /api2/json")

				lab := zoneCreateTestLab(tc.zoneType)
				lab.Network.ZonePeers = tc.peers
				path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
				cmd := buildCreateCmd(t, path, f, "node1")

				_, err := runCreateCmd(t, cmd, args...)
				require.Error(t, err)
				for _, w := range tc.want {
					assert.ErrorContains(t, err, w)
				}
			})
		}
	}
}

// TestCreateDryRun_ExistingZoneAcceptedWhateverItsTypeAndPeers covers a lab
// that points at a zone PVE already has. With no zone_peers set, and a zone of
// any type, the dry run builds its plan and skips the zone step, because the
// zone is not pmx's to create.
func TestCreateDryRun_ExistingZoneAcceptedWhateverItsTypeAndPeers(t *testing.T) {
	for _, tc := range zoneCreateRefusedCases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{
				map[string]any{"zone": "haclvx", "type": tc.zoneType},
			})
			createForbid(f, t, "POST /api2/json/cluster/sdn/zones")
			f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{})
			createForbid(f, t, "POST /api2/json/cluster/sdn/vnets")
			f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{})
			createForbid(f, t, "POST /api2/json/cluster/sdn/vnets/labwayne/subnets")
			f.HandleJSON("GET /api2/json/storage", []any{map[string]any{"storage": "tank", "type": "zfspool", "pool": "tank"}})
			createForbid(f, t, "POST /api2/json/storage")
			f.HandleJSON("GET /api2/json/pools", []any{})
			createForbid(f, t, "POST /api2/json/pools")
			createPoolNotFoundRoute(f, "lab-wayne")
			f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
			createForbid(f, t, "POST /api2/json/nodes/node1/qemu")
			createForbid(f, t, "GET /api2/json/cluster/nextid")

			lab := zoneCreateTestLab(tc.zoneType)
			path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
			cmd := buildCreateCmd(t, path, f, "node1")

			out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--dry-run")
			require.NoError(t, err)
			assert.Contains(t, out, "already exists", "the zone step is skipped")
		})
	}
}

// TestConfigShow_AcceptsAnyZoneTypeAndPeers pins that `config show` does not
// judge the zone: whether pmx must create it depends on PVE's live state, which
// config show never reads.
func TestConfigShow_AcceptsAnyZoneTypeAndPeers(t *testing.T) {
	for _, zt := range []string{"simple", "vxlan", "vlan", "qinq", "evpn", "faucet"} {
		lab := cleanLab("alpha")
		lab.Network.ZoneName = "haclvx"
		lab.Network.ZoneType = zt
		cfgPath := writeConfigCmdYAML(t, &config.Config{Labs: map[string]*config.Lab{"alpha": lab}})

		_, err := runConfigCmd(t, cfgPath, "show", "alpha")
		require.NoError(t, err, "zone_type %q with no zone_peers", zt)
	}
}

// netZoneApplyFixture registers every route a successful `net apply` touches
// with an empty vnet and subnet state, and the given zone list. It returns the
// recorder for zone requests, so a test can assert what was sent to /zones.
func netZoneApplyFixture(f *testhelper.FakePVE, zones []any) *[]netRecordedRequest {
	zoneRec := &[]netRecordedRequest{}
	other := &[]netRecordedRequest{}
	netRecord(f, zoneRec, nil, "zone-list", "GET /api2/json/cluster/sdn/zones", zones, 200)
	netRecord(f, zoneRec, nil, "zone-create", "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)
	netRecord(f, zoneRec, nil, "zone-update", "PUT /api2/json/cluster/sdn/zones/haclvx", map[string]any{}, 200)
	netRecord(f, other, nil, "vnet-list", "GET /api2/json/cluster/sdn/vnets", []any{}, 200)
	netRecord(f, other, nil, "vnet-create", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	netRecord(f, other, nil, "subnet-list", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{}, 200)
	netRecord(f, other, nil, "subnet-create", "POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)
	netRecord(f, other, nil, "dry-run", "GET /api2/json/cluster/sdn/dry-run",
		map[string]any{"frr-diff": "", "interfaces-diff": ""}, 200)
	netRecord(f, other, nil, "apply", "PUT /api2/json/cluster/sdn", nil, 200)
	return zoneRec
}

// TestNetApply_AbsentZoneThatCannotBeBuilt_Refused covers `net apply` against
// a zone that does not exist and that PVE would reject at create: it refuses
// with the key or type named, and issues no create.
func TestNetApply_AbsentZoneThatCannotBeBuilt_Refused(t *testing.T) {
	for _, tc := range zoneCreateRefusedCases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			zoneRec := netZoneApplyFixture(f, []any{})

			lab := zoneCreateTestLab(tc.zoneType)
			lab.Network.ZonePeers = tc.peers
			path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
			cmd := buildNetCmd(t, path, f, "node1")

			_, err := runNetCmd(t, cmd, "apply", "wayne")
			require.Error(t, err)
			for _, w := range tc.want {
				assert.ErrorContains(t, err, w)
			}
			for _, r := range *zoneRec {
				assert.Equal(t, http.MethodGet, r.method, "no zone create or update may be issued")
			}
		})
	}
}

// TestNetApply_AbsentVxlanZoneWithPeers_Created pins the other side: with
// zone_peers set, the vxlan zone is created, carrying the peers.
func TestNetApply_AbsentVxlanZoneWithPeers_Created(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	zoneRec := netZoneApplyFixture(f, []any{})

	lab := zoneCreateTestLab("vxlan")
	lab.Network.ZonePeers = "192.0.2.10"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	_, err := runNetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, *zoneRec, 2)
	create := (*zoneRec)[1]
	assert.Equal(t, http.MethodPost, create.method)
	assert.Equal(t, "vxlan", create.body["type"])
	assert.Equal(t, "192.0.2.10", create.body["peers"])
}

// TestNetApply_ExistingZoneAcceptedWhateverItsTypeAndPeers covers a lab that
// points at a zone PVE already has, of every type, with no zone_peers set: the
// apply succeeds, and the zone is never created, nor are its peers rewritten.
func TestNetApply_ExistingZoneAcceptedWhateverItsTypeAndPeers(t *testing.T) {
	for _, tc := range zoneCreateRefusedCases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			zoneRec := netZoneApplyFixture(f, []any{
				map[string]any{
					"zone": "haclvx", "type": tc.zoneType, "mtu": 1450, "nodes": "node1", "peers": "198.51.100.7",
				},
			})

			lab := zoneCreateTestLab(tc.zoneType)
			path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
			cmd := buildNetCmd(t, path, f, "node1")

			_, err := runNetCmd(t, cmd, "apply", "wayne")
			require.NoError(t, err)

			require.Len(t, *zoneRec, 1, "only the zone list: no create, and no update that would rewrite peers")
			assert.Equal(t, http.MethodGet, (*zoneRec)[0].method)
		})
	}
}

// TestNetApply_ExistingVxlanZonePeersOnlyRewrittenWhenSet covers an existing
// vxlan zone whose live peers differ from the config: an unset zone_peers
// leaves them alone, a set one that differs updates them, and a set one on a
// zone that is not vxlan never sends peers, which that zone type has no field
// for.
func TestNetApply_ExistingVxlanZonePeersOnlyRewrittenWhenSet(t *testing.T) {
	live := func(zoneType string) []any {
		return []any{map[string]any{
			"zone": "haclvx", "type": zoneType, "mtu": 1450, "nodes": "node1", "peers": "198.51.100.7",
		}}
	}

	t.Run("unset leaves live peers alone", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		zoneRec := netZoneApplyFixture(f, live("vxlan"))
		lab := zoneCreateTestLab("vxlan")
		path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})

		_, err := runNetCmd(t, buildNetCmd(t, path, f, "node1"), "apply", "wayne")
		require.NoError(t, err)
		require.Len(t, *zoneRec, 1, "only the zone list")
	})

	t.Run("set and different updates the peers", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		zoneRec := netZoneApplyFixture(f, live("vxlan"))
		lab := zoneCreateTestLab("vxlan")
		lab.Network.ZonePeers = "192.0.2.10"
		path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})

		_, err := runNetCmd(t, buildNetCmd(t, path, f, "node1"), "apply", "wayne")
		require.NoError(t, err)
		require.Len(t, *zoneRec, 2)
		assert.Equal(t, http.MethodPut, (*zoneRec)[1].method)
		assert.Equal(t, "192.0.2.10", (*zoneRec)[1].body["peers"])
	})

	t.Run("set on a live zone that is not vxlan sends no peers", func(t *testing.T) {
		f := testhelper.NewFakePVE(t)
		zoneRec := netZoneApplyFixture(f, live("vlan"))
		lab := zoneCreateTestLab("vxlan")
		lab.Network.ZonePeers = "192.0.2.10"
		path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})

		_, err := runNetCmd(t, buildNetCmd(t, path, f, "node1"), "apply", "wayne")
		require.NoError(t, err)
		require.Len(t, *zoneRec, 1, "only the zone list")
	})
}

// TestNetApplyDryRun_NeverDecidesToCreateTheZone covers `net apply --dry-run`:
// it lists no zones and creates none, so it has no create decision to refuse.
func TestNetApplyDryRun_NeverDecidesToCreateTheZone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	zoneRec := netZoneApplyFixture(f, []any{})
	lab := zoneCreateTestLab("vxlan")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})

	_, err := runNetCmd(t, buildNetCmd(t, path, f, "node1"), "apply", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Empty(t, *zoneRec)
}
