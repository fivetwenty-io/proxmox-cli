package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// preparedCreateArgs are the required flags of `prepared add`.
func preparedCreateArgs(extra ...string) []string {
	base := []string{
		"add", "web01",
		"--country", "de", "--disk-mode", "disk_list", "--fqdn", "web01.example.com",
		"--keyboard", "de", "--mailto", "admin@example.com", "--timezone", "Europe/Berlin",
		"--filesystem", `{"type":"ext4"}`,
	}
	return append(base, extra...)
}

func TestAutoInstallPreparedAdd_ForwardsBondAndVlanFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/auto-install/prepared", &rec,
		map[string]any{"config": map[string]any{"id": "web01"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedAddCmd(), preparedCreateArgs(
		"--bond-enabled",
		"--bond-mode", "802.3ad",
		"--bond-hash-policy", "layer3+4",
		"--bond-members-filter", `{"ID_NET_NAME":"enp*"}`,
		"--bond-members-filter-match", "all",
		"--vlan-id", "42",
	)...)
	require.NoError(t, err)

	require.Equal(t, "1", rec.form.Get("bond-enabled"))
	require.Equal(t, "802.3ad", rec.form.Get("bond-mode"))
	require.Equal(t, "layer3+4", rec.form.Get("bond-hash-policy"))
	require.Equal(t, `{"ID_NET_NAME":"enp*"}`, rec.form.Get("bond-members-filter"))
	require.Equal(t, "all", rec.form.Get("bond-members-filter-match"))
	require.Equal(t, "42", rec.form.Get("vlan-id"))
}

func TestAutoInstallPreparedAdd_OmitsUnsetBondAndVlanFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/auto-install/prepared", &rec,
		map[string]any{"config": map[string]any{"id": "web01"}})

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newAutoInstallPreparedAddCmd(), preparedCreateArgs()...))

	for _, key := range []string{"bond-enabled", "bond-mode", "bond-hash-policy",
		"bond-members-filter", "bond-members-filter-match", "vlan-id"} {
		require.False(t, rec.form.Has(key), "%q must be omitted when its flag is unset", key)
	}
}

func TestAutoInstallPreparedUpdate_ForwardsBondAndVlanFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/auto-install/prepared/web01", &rec,
		map[string]any{"config": map[string]any{"id": "web01"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedUpdateCmd(), "update", "web01",
		"--bond-enabled=false",
		"--bond-mode", "active-backup",
		"--bond-hash-policy", "layer2",
		"--bond-members-filter", `{"ID_NET_NAME":"eno*"}`,
		"--bond-members-filter-match", "any",
		"--vlan-id", "7",
	)
	require.NoError(t, err)

	require.Equal(t, "0", rec.form.Get("bond-enabled"), "an explicit false must be sent")
	require.Equal(t, "active-backup", rec.form.Get("bond-mode"))
	require.Equal(t, "layer2", rec.form.Get("bond-hash-policy"))
	require.Equal(t, `{"ID_NET_NAME":"eno*"}`, rec.form.Get("bond-members-filter"))
	require.Equal(t, "any", rec.form.Get("bond-members-filter-match"))
	require.Equal(t, "7", rec.form.Get("vlan-id"))
}

func TestAutoInstallPrepared_RejectsBadBondValues(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	for flag, want := range map[string]string{
		"--bond-mode":                 "--bond-mode must be one of",
		"--bond-hash-policy":          "--bond-hash-policy must be one of",
		"--bond-members-filter-match": "--bond-members-filter-match must be one of",
	} {
		var buf bytes.Buffer
		err := run(deps, &buf, newAutoInstallPreparedUpdateCmd(), "update", "web01", flag, "bogus")
		require.ErrorContains(t, err, want)
	}

	var buf bytes.Buffer
	err := run(deps, &buf, newAutoInstallPreparedUpdateCmd(), "update", "web01", "--bond-members-filter", "{nope")
	require.ErrorContains(t, err, "--bond-members-filter is not valid JSON")
}

func TestNodeNetworkCreate_ForwardsAutoMethod(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/network", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkCreateCmd(), "create", "pdm-host", "eth1",
		"--type", "eth", "--method", "auto", "--method6", "auto")
	require.NoError(t, err)

	require.Equal(t, "auto", rec.form.Get("method"))
	require.Equal(t, "auto", rec.form.Get("method6"))
}

func TestNodeNetworkUpdate_ForwardsAutoMethod(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/network/eth1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkUpdateCmd(), "update", "pdm-host", "eth1", "--method", "auto")
	require.NoError(t, err)
	require.Equal(t, "auto", rec.form.Get("method"))
	require.False(t, rec.form.Has("method6"))
}
