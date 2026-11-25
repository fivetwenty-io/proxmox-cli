package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestReplicationCreate_SkipFreezeFs(t *testing.T) {
	f, ac := newFakeClient(t)
	var forms []url.Values
	f.HandleFunc("POST /api2/json/cluster/replication", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.Form)
		testhelper.WriteData(w, nil)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "create", "--id", "100-0", "--target-node", "pve2",
		"--skip-freeze-fs"))
	require.Equal(t, "1", forms[0].Get("skip-freeze-fs"))

	require.NoError(t, run(deps, &buf, "replication", "create", "--id", "100-0", "--target-node", "pve2"))
	require.False(t, forms[1].Has("skip-freeze-fs"))
}

func TestReplicationSet_SkipFreezeFs(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/replication/100-0", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "set", "100-0", "--skip-freeze-fs=false"))
	require.Equal(t, "0", gotForm.Get("skip-freeze-fs"), "an explicit false alone is a change and must be sent")
}
