package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestQemuSyncTimeOnResume_ReachesRequest verifies --sync-time-on-resume is
// forwarded by start, resume, and snapshot rollback, and omitted when unset.
func TestQemuSyncTimeOnResume_ReachesRequest(t *testing.T) {
	cases := []struct {
		name string
		path string
		args []string
	}{
		{"start", "/api2/json/nodes/pve1/qemu/100/status/start", []string{"start", "100"}},
		{"resume", "/api2/json/nodes/pve1/qemu/100/status/resume", []string{"resume", "100"}},
		{"rollback", "/api2/json/nodes/pve1/qemu/100/snapshot/snap1/rollback",
			[]string{"snapshot", "rollback", "100", "snap1", "--yes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ac := newFakeClient(t)
			handleClusterResources(f, 100, "pve1")
			var forms []string
			f.HandleFunc("POST "+tc.path, func(w http.ResponseWriter, r *http.Request) {
				forms = append(forms, r.URL.RawQuery+"&"+readBody(t, r))
				testhelper.WriteData(w, validUPID)
			})
			deps := depsFor(t, ac, output.FormatTable, "pve1", true)

			var buf bytes.Buffer
			require.NoError(t, run(deps, &buf, append(append([]string{}, tc.args...), "--sync-time-on-resume")...))
			require.Equal(t, "1", parseForm(t, forms[0]).Get("sync-time-on-resume"))

			buf.Reset()
			require.NoError(t, run(deps, &buf, tc.args...))
			require.False(t, parseForm(t, forms[1]).Has("sync-time-on-resume"))
		})
	}
}
