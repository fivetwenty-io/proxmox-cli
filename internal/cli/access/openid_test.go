package access

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// openid list
// ---------------------------------------------------------------------------

// TestAccess_OpenidList_Table verifies `pve access openid list` reads the
// realm list from GET /access/domains (GET /access/openid is only a directory
// index) and keeps only openid realms.
func TestAccess_OpenidList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("GET /api2/json/access/domains", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"realm":   "myoidc",
				"type":    "openid",
				"comment": "Example OIDC",
			},
			map[string]any{
				"realm": "pam",
				"type":  "pam",
			},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "openid", "list"))

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/access/domains", rec.path)

	out := buf.String()
	require.Contains(t, out, "REALM")
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "COMMENT")
	require.Contains(t, out, "myoidc")
	require.Contains(t, out, "openid")
	require.Contains(t, out, "Example OIDC")
	require.NotContains(t, out, "pam", "non-openid realms must be filtered out")
}

func TestAccess_OpenidList_Empty(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/domains", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	// Empty list must succeed — headers still rendered.
	require.NoError(t, run(deps, &buf, "openid", "list"))
	require.Contains(t, buf.String(), "REALM")
}

func TestAccess_OpenidList_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/domains", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "openid", "list")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list openid realms")
}

func TestAccess_OpenidCommandTree(t *testing.T) {
	cmd := newOpenidCmd()
	found := false
	for _, c := range cmd.Commands() {
		if c.Name() == "list" {
			found = true
		}
	}
	require.True(t, found, "access openid must expose a 'list' sub-command")
}
