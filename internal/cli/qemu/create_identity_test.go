package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestQemuCreate_AllowIdentityMismatchReachesBody(t *testing.T) {
	f, ac := newFakeClient(t)
	var bodies []string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, readBody(t, r))
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--archive", "local:backup/x.vma", "--allow-identity-mismatch"))
	require.Equal(t, "1", parseForm(t, bodies[0]).Get("allow-identity-mismatch"))

	buf.Reset()
	require.NoError(t, run(deps, &buf, "create", "100", "--cores", "2"))
	require.False(t, parseForm(t, bodies[1]).Has("allow-identity-mismatch"))
}
