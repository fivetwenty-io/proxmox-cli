package apiclient_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// pdmTestUPID is a well-formed PDM UPID (9 fields: PDM shares PBS's
// proxmox-rest-server UPID shape, with an extra hex task-id sitting between
// pstart and starttime) whose node is "pdm-host".
const pdmTestUPID = "UPID:pdm-host:00000C86:0000000B:00000003:685F9A3C:remote_scan:remote1:root@pam:"

func TestPDMUPIDNode_Parses9FieldUPID(t *testing.T) {
	node, err := apiclient.PDMUPIDNode(pdmTestUPID)
	require.NoError(t, err)
	require.Equal(t, "pdm-host", node)
}

func TestPDMUPIDNode_RejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		upid string
	}{
		{name: "empty string", upid: ""},
		{name: "pve 8-field upid", upid: "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:"},
		{name: "missing prefix", upid: "XPID:pdm-host:00000C86:0000000B:00000003:685F9A3C:remote_scan:remote1:root@pam:"},
		{name: "empty node", upid: "UPID::00000C86:0000000B:00000003:685F9A3C:remote_scan:remote1:root@pam:"},
		{name: "not a upid at all", upid: "garbage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := apiclient.PDMUPIDNode(tc.upid)
			require.Error(t, err)
		})
	}
}

// newPDMClientForFake constructs a *PDMClient pointed at the fake server.
func newPDMClientForFake(t *testing.T, f *testhelper.FakePDM) *apiclient.PDMClient {
	t.Helper()

	pc, err := apiclient.NewPDMClient(f.Options)
	require.NoError(t, err)

	return pc
}

func TestWaitPDMTask_PollsStatusUntilStopped(t *testing.T) {
	f := testhelper.NewFakePDM(t)

	var calls int
	f.HandleFunc("GET /api2/json/nodes/pdm-host/tasks/"+pdmTestUPID+"/status", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			testhelper.WriteData(w, map[string]any{
				"status":    "running",
				"upid":      pdmTestUPID,
				"node":      "pdm-host",
				"pid":       1234,
				"pstart":    1,
				"starttime": 1,
				"type":      "remote_scan",
				"user":      "root@pam",
			})
			return
		}
		testhelper.WriteData(w, map[string]any{
			"status":     "stopped",
			"exitstatus": "OK",
			"upid":       pdmTestUPID,
			"node":       "pdm-host",
			"pid":        1234,
			"pstart":     1,
			"starttime":  1,
			"type":       "remote_scan",
			"user":       "root@pam",
		})
	})
	pc := newPDMClientForFake(t, f)

	err := apiclient.WaitPDMTask(context.Background(), pc, pdmTestUPID, &tasks.WaitOptions{
		TimeoutSeconds: 5,
		IntervalMillis: 5,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, calls, 2, "waiter should poll at least twice before observing stopped/OK")
}

func TestWaitPDMTask_ErrorsOnFailedTask(t *testing.T) {
	f := testhelper.NewFakePDM(t)
	f.HandleJSON("GET /api2/json/nodes/pdm-host/tasks/"+pdmTestUPID+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "scan failed",
		"upid":       pdmTestUPID,
	})
	pc := newPDMClientForFake(t, f)

	err := apiclient.WaitPDMTask(context.Background(), pc, pdmTestUPID, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), pdmTestUPID)
}

func TestWaitPDMTask_ErrorsOnMalformedUPID(t *testing.T) {
	f := testhelper.NewFakePDM(t)
	pc := newPDMClientForFake(t, f)

	err := apiclient.WaitPDMTask(context.Background(), pc, "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected 9 fields")
}
