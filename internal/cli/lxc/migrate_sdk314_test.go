package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestLxcMigrate_SkipConfigCheckReachesBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f, 101, "pve1")
	var bodies []map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, recordBody(t, r))
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzmigrate:101:root@pam:")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, newTestCmd(t, deps, &buf, "migrate", "101", "--target-node", "pve2", "--skip-config-check")())
	require.Equal(t, true, bodies[0]["skip-config-check"])

	buf.Reset()
	require.NoError(t, newTestCmd(t, deps, &buf, "migrate", "101", "--target-node", "pve2")())
	require.NotContains(t, bodies[1], "skip-config-check")
}

func TestLxcRemoteMigrate_AllowNewUIDReachesBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f, 200, "pve1")
	var bodies []map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/200/remote_migrate", func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, recordBody(t, r))
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzremotemigrate:200:root@pam:")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	base := []string{"remote-migrate", "200", "--yes", "--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm", "--target-bridge", "vmbr0"}
	var buf bytes.Buffer
	require.NoError(t, newTestCmd(t, deps, &buf, append(base, "--allow-new-uid")...)())
	require.Equal(t, true, bodies[0]["allow-new-uid"])

	buf.Reset()
	require.NoError(t, newTestCmd(t, deps, &buf, base...)())
	require.NotContains(t, bodies[1], "allow-new-uid")
}
