package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestQemuMigrate_SkipConfigCheckReachesBody(t *testing.T) {
	f, ac := newFakeClient(t)
	handleClusterResources(f, 100, "pve1")

	var forms []string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		forms = append(forms, r.URL.RawQuery+"&"+readBody(t, r))
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "migrate", "100", "--target-node", "pve2", "--skip-config-check"))
	require.Equal(t, "1", parseForm(t, forms[0]).Get("skip-config-check"))

	buf.Reset()
	require.NoError(t, run(deps, &buf, "migrate", "100", "--target-node", "pve2"))
	require.False(t, parseForm(t, forms[1]).Has("skip-config-check"))
}

func TestQemuRemoteMigrate_AllowNewUIDReachesBody(t *testing.T) {
	f, ac := newFakeClient(t)
	handleClusterResources(f, 100, "pve1")

	var forms []string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/remote_migrate", func(w http.ResponseWriter, r *http.Request) {
		forms = append(forms, r.URL.RawQuery+"&"+readBody(t, r))
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	base := []string{"remote-migrate", "100", "--yes", "--target-endpoint", "https://remote:8006",
		"--target-storage", "local-lvm", "--target-bridge", "vmbr0"}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, append(base, "--allow-new-uid")...))
	require.Equal(t, "1", parseForm(t, forms[0]).Get("allow-new-uid"))

	buf.Reset()
	require.NoError(t, run(deps, &buf, base...))
	require.False(t, parseForm(t, forms[1]).Has("allow-new-uid"))
}
