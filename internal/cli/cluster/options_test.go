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

// TestClusterOptions_Get verifies `pve cluster options get` reads
// GET /cluster/options and renders the datacenter settings.
func TestClusterOptions_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"console": "html5", "keyboard": "en-us", "migration": "type=secure",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "get"))
	out := buf.String()
	require.Contains(t, out, "html5")
	require.Contains(t, out, "en-us")
}

// TestClusterOptions_SetRequiresFlag verifies the set command rejects an
// invocation with no option flags rather than issuing an empty update.
func TestClusterOptions_SetRequiresFlag(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/options", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "options", "set")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no options to set")
	require.False(t, called, "set must not issue a PUT when no flags are passed")
}

// TestClusterOptions_SetForwardsFields verifies that only the flags passed are
// forwarded (by their PVE param keys) and that unset flags are omitted.
func TestClusterOptions_SetForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/options", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "set",
		"--console", "xtermjs", "--max-workers", "8", "--email-from", "noc@example.com"))
	require.Equal(t, "xtermjs", gotForm.Get("console"))
	require.Equal(t, "8", gotForm.Get("max_workers"))
	require.Equal(t, "noc@example.com", gotForm.Get("email_from"))
	// only-changed-flags contract: unpassed flags must be absent from the body.
	_, hasKeyboard := gotForm["keyboard"]
	require.False(t, hasKeyboard, "unset --keyboard must be omitted from the request body")
	_, hasMigration := gotForm["migration"]
	require.False(t, hasMigration, "unset --migration must be omitted from the request body")
}
