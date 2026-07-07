package apiclient_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// pbsTestUPID is a well-formed PBS UPID (9 fields: the extra hex task-id sits
// between pstart and starttime) whose node is "pbs1".
const pbsTestUPID = "UPID:pbs1:00000C86:0000000B:00000003:685F9A3C:garbage_collection:store1:root@pam:"

func TestPBSUPIDNode_ParsesNode(t *testing.T) {
	node, err := apiclient.PBSUPIDNode(pbsTestUPID)
	require.NoError(t, err)
	require.Equal(t, "pbs1", node)
}

func TestPBSUPIDNode_Rejects(t *testing.T) {
	tests := []struct {
		name string
		upid string
	}{
		{name: "empty string", upid: ""},
		{name: "pve 8-field upid", upid: "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:"},
		{name: "missing prefix", upid: "XPID:pbs1:00000C86:0000000B:00000003:685F9A3C:gc:store1:root@pam:"},
		{name: "empty node", upid: "UPID::00000C86:0000000B:00000003:685F9A3C:gc:store1:root@pam:"},
		{name: "not a upid at all", upid: "garbage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := apiclient.PBSUPIDNode(tc.upid)
			require.Error(t, err)
		})
	}
}

// newPBSClientForFake constructs a *PBSClient pointed at the fake server.
func newPBSClientForFake(t *testing.T, f *testhelper.FakePBS) *apiclient.PBSClient {
	t.Helper()

	pc, err := apiclient.NewPBSClient(f.Options)
	require.NoError(t, err)

	return pc
}

func TestWaitPBSTask_SucceedsOnOKExit(t *testing.T) {
	f := testhelper.NewFakePBS(t)
	f.HandleJSON("GET /api2/json/nodes/pbs1/tasks/"+pbsTestUPID+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "OK",
		"upid":       pbsTestUPID,
	})
	pc := newPBSClientForFake(t, f)

	err := apiclient.WaitPBSTask(context.Background(), pc, pbsTestUPID, nil)
	require.NoError(t, err)
}

func TestWaitPBSTask_ErrorsOnFailedTask(t *testing.T) {
	f := testhelper.NewFakePBS(t)
	f.HandleJSON("GET /api2/json/nodes/pbs1/tasks/"+pbsTestUPID+"/status", map[string]any{
		"status":     "stopped",
		"exitstatus": "removing datastore failed",
		"upid":       pbsTestUPID,
	})
	pc := newPBSClientForFake(t, f)

	err := apiclient.WaitPBSTask(context.Background(), pc, pbsTestUPID, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), pbsTestUPID)
}

func TestWaitPBSTask_ErrorsOnMalformedUPID(t *testing.T) {
	f := testhelper.NewFakePBS(t)
	pc := newPBSClientForFake(t, f)

	err := apiclient.WaitPBSTask(context.Background(), pc, "UPID:pve1:00001234:00005678:65000000:qmstart:100:root@pam:", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected 9 fields")
}
