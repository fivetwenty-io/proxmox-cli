package node_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestNodeNetwork_CreateForwardsIpForwardAndVrf(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var forms []url.Values
	f.HandleFunc("POST /api2/json/nodes/pve1/network", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.Form)
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "create",
		"--iface", "eth1", "--type", "eth", "--ip-forward", "--vrf", "vrf_zone1"))
	require.NoError(t, root.Execute())
	require.Equal(t, "1", forms[0].Get("ip-forward"))
	require.Equal(t, "vrf_zone1", forms[0].Get("vrf"))

	root, _, prefix = newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "network", "create",
		"--iface", "eth1", "--type", "eth"))
	require.NoError(t, root.Execute())
	require.False(t, forms[1].Has("ip-forward"))
	require.False(t, forms[1].Has("vrf"))
}
