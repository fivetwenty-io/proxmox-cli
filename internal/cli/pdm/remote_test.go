package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestRemoteLs_SortsByIdAndStripsSecret asserts that `remote ls` sorts
// entries by id and never renders the token field, in either the table rows
// or the Raw JSON output.
func TestRemoteLs_SortsByIdAndStripsSecret(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/remotes/remote", []map[string]any{
		{"id": "zeta", "type": "pve", "authid": "root@pam", "nodes": []string{"10.0.0.2"}, "token": "secret-z"},
		{
			"id": "alpha", "type": "pbs", "authid": "root@pam", "nodes": []string{"10.0.0.1"},
			"token": "secret-a", "web-url": "https://alpha.example",
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["id"], "entries must sort by id")
	require.Equal(t, "zeta", got[1]["id"])
	require.NotContains(t, got[0], "token", "secret token must be stripped from ls output")
	require.NotContains(t, got[1], "token", "secret token must be stripped from ls output")
}

// TestRemoteShow_StripsSecret asserts that `remote show` never renders the
// token field returned by GET /remotes/remote/{id}/config.
func TestRemoteShow_StripsSecret(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/remotes/remote/alpha/config", map[string]any{
		"id": "alpha", "type": "pve", "authid": "root@pam",
		"nodes": []string{"10.0.0.1"}, "token": "super-secret",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteShowCmd(), "show", "alpha")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "alpha", got["id"])
	require.NotContains(t, got, "token", "secret token must be stripped from show output")
}

// TestRemoteShow_Defaults asserts that --defaults lists options absent from
// the live response (create-token is an add-only action parameter never
// echoed back by GET /remotes/remote/{id}/config) as "(unset)", in the
// table/Single rendering, without reintroducing the stripped token field.
func TestRemoteShow_Defaults(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/remotes/remote/alpha/config", map[string]any{
		"id": "alpha", "type": "pve", "authid": "root@pam", "nodes": []string{"10.0.0.1"}, "token": "super-secret",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteShowCmd(), "show", "alpha", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "create-token", "--defaults should list options absent from the live response")
	require.Contains(t, buf.String(), "(unset)")
	require.NotContains(t, buf.String(), "super-secret")
}

// TestRemoteAdd_SendsParams asserts that `remote add` encodes every flag
// onto the expected form field names, including the repeatable --node flag.
func TestRemoteAdd_SendsParams(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/remotes/remote", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteAddCmd(), "add", "alpha",
		"--type", "pve", "--authid", "root@pam", "--token", "s3cr3t",
		"--node", "10.0.0.1", "--node", "10.0.0.2", "--web-url", "https://alpha.example")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "alpha", rec.form.Get("id"))
	require.Equal(t, "pve", rec.form.Get("type"))
	require.Equal(t, "root@pam", rec.form.Get("authid"))
	require.Equal(t, "s3cr3t", rec.form.Get("token"))
	require.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, rec.form["nodes"])
	require.Equal(t, "https://alpha.example", rec.form.Get("web-url"))
	require.Contains(t, buf.String(), `Remote "alpha" added.`)
}

// TestRemoteAdd_RejectsInvalidType asserts that `remote add` validates
// --type against the enum before issuing any request.
func TestRemoteAdd_RejectsInvalidType(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteAddCmd(), "add", "alpha",
		"--type", "bogus", "--authid", "root@pam", "--token", "s3cr3t", "--node", "10.0.0.1")
	require.Error(t, err)
	require.ErrorContains(t, err, "--type must be one of pve, pbs")
}

// TestRemoteUpdate_RejectsNoChanges asserts that `remote update` refuses to
// issue a request when no flag was explicitly set.
func TestRemoteUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteUpdateCmd(), "update", "alpha")
	require.Error(t, err)
	require.ErrorContains(t, err, `update remote "alpha": no changes given: pass at least one flag`)
}

// TestRemoteUpdate_SendsChangedFlagsOnly asserts that `remote update` sends
// only the flags explicitly set, leaving unset fields off the wire.
func TestRemoteUpdate_SendsChangedFlagsOnly(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/remotes/remote/alpha", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteUpdateCmd(), "update", "alpha", "--token", "new-secret")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "new-secret", rec.form.Get("token"))
	require.NotContains(t, rec.form, "authid")
	require.NotContains(t, rec.form, "nodes")
	require.Contains(t, buf.String(), `Remote "alpha" updated.`)
}

// TestRemoteDelete_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `remote delete` blocks the request entirely when unset.
func TestRemoteDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteDeleteCmd(), "delete", "alpha")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to delete remote "alpha" without confirmation: pass --yes/-y`)
}

// TestRemoteDelete_SendsRequestWithConfirmation asserts that passing --yes
// issues the delete request with the expected form fields.
func TestRemoteDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/remotes/remote/alpha", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteDeleteCmd(), "delete", "alpha", "--yes", "--delete-token")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	// DELETE requests carry their parameters on the query string, not the form body.
	require.Equal(t, "1", rec.query.Get("delete-token"))
	require.Contains(t, buf.String(), `Remote "alpha" deleted.`)
}

// TestRemoteVersion_RendersSingle asserts that `remote version` renders the
// remote's Proxmox version fields.
func TestRemoteVersion_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/remotes/remote/alpha/version", map[string]any{
		"release": "8.2", "repoid": "abc123", "version": "8.2.1",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteVersionCmd(), "version", "alpha")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "8.2.1")
}

// TestRemoteProbeCertificate_SendsNode asserts that `remote
// probe-certificate` sends the required --node flag and reports success.
func TestRemoteProbeCertificate_SendsNode(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/remotes/remote/alpha/probe-certificate", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteProbeCertificateCmd(), "probe-certificate", "alpha", "--node", "10.0.0.1")
	require.NoError(t, err)

	require.Equal(t, "10.0.0.1", rec.form.Get("node"))
	require.Contains(t, buf.String(), `Probed TLS certificate for remote "alpha" node "10.0.0.1".`)
}

// TestRemoteRrddata_ValidatesTimeframe asserts that `remote rrddata`
// validates --timeframe against the enum before issuing any request.
func TestRemoteRrddata_ValidatesTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteRrddataCmd(), "rrddata", "alpha", "--timeframe", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--timeframe must be one of")
}

// TestRemoteRrddata_ValidatesConsolidation asserts that `remote rrddata`
// validates --cf against the enum before issuing any request.
func TestRemoteRrddata_ValidatesConsolidation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteRrddataCmd(), "rrddata", "alpha", "--timeframe", "hour", "--cf", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--cf must be one of")
}

// TestRemoteRrddata_ListsDataPoints asserts that `remote rrddata` renders
// the RRD data points as a table.
func TestRemoteRrddata_ListsDataPoints(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/remotes/remote/alpha/rrddata", []map[string]any{
		{"time": 1000, "metric-collection-response-time": 12.5},
		{"time": 2000},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteRrddataCmd(), "rrddata", "alpha", "--timeframe", "hour")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1000")
	require.Contains(t, buf.String(), "12.5")
	require.Contains(t, buf.String(), "2000")
}
