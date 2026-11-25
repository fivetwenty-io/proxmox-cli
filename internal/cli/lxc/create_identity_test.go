package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestLxcCreate_AllowIdentityMismatchReachesBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var bodies []map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, recordBody(t, r))
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzcreate:101:root@pam:")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, newTestCmd(t, deps, &buf, "create", "101", "--ostemplate", "local:backup/x.tar.zst",
		"--restore", "--allow-identity-mismatch")())
	require.Equal(t, true, bodies[0]["allow-identity-mismatch"])

	buf.Reset()
	require.NoError(t, newTestCmd(t, deps, &buf, "create", "101", "--ostemplate", "local:vztmpl/a.tar.zst")())
	require.NotContains(t, bodies[1], "allow-identity-mismatch")
}
