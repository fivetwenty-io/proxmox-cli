package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestCephFlags_List verifies `pve cluster ceph flags list` reads
// GET /cluster/ceph/flags and renders the returned flags.
func TestCephFlags_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ceph/flags", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "noout", "value": 1, "description": "no out"},
			map[string]any{"name": "noscrub", "value": 0, "description": "no scrub"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "flags", "list"))
	out := buf.String()
	require.Contains(t, out, "noout")
	require.Contains(t, out, "noscrub")
}

// TestCephFlags_Get verifies get reads GET /cluster/ceph/flags/{flag}.
func TestCephFlags_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/ceph/flags/noout", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"name": "noout", "value": 1})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "flags", "get", "noout"))
	require.Contains(t, buf.String(), "noout")
}

// TestCephFlags_Set verifies set issues a PUT carrying the boolean value.
func TestCephFlags_Set(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/ceph/flags/noout", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "flags", "set", "noout", "true"))
	require.Equal(t, "1", gotForm.Get("value"))
	require.Contains(t, buf.String(), "enabled")
}

// TestCephFlags_SetRejectsBadValue verifies a non-boolean value is rejected
// before any request is issued.
func TestCephFlags_SetRejectsBadValue(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/ceph/flags/noout", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "flags", "set", "noout", "maybe")
	require.Error(t, err)
	require.Contains(t, err.Error(), "true or false")
	require.False(t, called, "set must not issue a PUT for an invalid value")
}

// TestCephCommandTree verifies the ceph flags verb set is registered.
func TestCephCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	ceph := childCommands(root)["ceph"]
	require.NotNil(t, ceph, "cluster must have a ceph command")

	flags := childCommands(ceph)["flags"]
	require.NotNil(t, flags, "ceph must have a flags command")

	verbs := childCommands(flags)
	for _, v := range []string{"list", "get", "set"} {
		require.Containsf(t, verbs, v, "ceph flags must have a %s command", v)
	}
}
