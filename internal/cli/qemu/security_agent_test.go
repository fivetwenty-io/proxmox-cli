package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

func TestSecurityAgentShow_Defaults(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "agent", "show", "100"))

	out := buf.String()
	require.Contains(t, out, "freeze-fs")
	require.Contains(t, out, "true", "freeze-fs defaults to true when unset")
	require.Contains(t, out, "virtio", "type defaults to virtio when unset")
}

func stubStoppedStatus(f *testhelper.FakePVE, vmid string) {
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/"+vmid+"/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"vmid": 100, "status": "stopped"})
	})
}

// TestSecurityAgentSet_MergePreservesUntouchedKeys is a regression pillar:
// setting --enabled must not disturb an existing freeze-fs=0 the user
// explicitly set earlier.
func TestSecurityAgentSet_MergePreservesUntouchedKeys(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"agent": "0,freeze-fs=0", "digest": "d1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "agent", "set", "100", "--enabled"))

	form := parseForm(t, body)
	require.Equal(t, "1,freeze-fs=0", form.Get("agent"), "bare enabled flag flips but freeze-fs=0 must be preserved")
}

func TestSecurityAgentSet_BareRoundTrip(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"agent": "1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "agent", "set", "100", "--type", "isa"))
	require.Equal(t, "1,type=isa", parseForm(t, body).Get("agent"))
}

func TestSecurityAgentSet_DigestPassthrough(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"digest": "auto"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "agent", "set", "100", "--enabled", "--digest", "override"))
	require.Equal(t, "override", parseForm(t, body).Get("digest"))
}

func TestSecurityAgentSet_ResetDeletesAgent(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"agent": "1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "agent", "set", "100", "--reset"))
	require.Equal(t, "agent", parseForm(t, body).Get("delete"))
}

// TestSecurityAgentSet_AllDefaultMergeDeletes verifies that merging to a
// state matching every API default sends delete=agent instead of an explicit
// (redundant) agent= string.
func TestSecurityAgentSet_AllDefaultMergeDeletes(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"agent": "1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "agent", "set", "100", "--enabled=false"))
	require.Equal(t, "agent", parseForm(t, body).Get("delete"))
}

// TestSecurityAgentSet_UnknownSubkeyBlocksDelete is a regression for A3: an
// unrecognized agent= sub-key (a future PVE addition) must survive the merge
// even when every known sub-key lands at its API default. Deleting agent=
// in that case would silently discard futurekey=x, breaking propstr's
// unknown-key-preserving contract.
func TestSecurityAgentSet_UnknownSubkeyBlocksDelete(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"agent": "1,fstrim_cloned_disks=1,futurekey=x"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "agent", "set", "100",
		"--enabled=false", "--fstrim-cloned-disks=false"))

	form := parseForm(t, body)
	require.Equal(t, "0,fstrim_cloned_disks=0,futurekey=x", form.Get("agent"),
		"known keys land at default but futurekey=x must be preserved")
	require.Empty(t, form.Get("delete"), "must not delete agent= while an unknown sub-key remains")
}

func TestSecurityAgentSet_NoFlagsError(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "agent", "set", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no agent flags given")
}

func TestSecurityAgentSet_InvalidType(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "agent", "set", "100", "--type", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "virtio, isa")
}
