package pbs

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// traffic ls
// ---------------------------------------------------------------------------

func TestTrafficLs_ListsRulesSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/traffic-control", &rec, []map[string]any{
		{"name": "zzz-rule", "network": []string{"192.168.0.0/16"}, "rate-in": "5MB"},
		{"name": "aaa-rule", "network": []string{"10.0.0.0/8"}, "rate-out": "10MB", "comment": "office"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficLsCmd(), "ls")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/traffic-control", rec.path)

	out := buf.String()
	aaaIdx := strings.Index(out, "aaa-rule")
	zzzIdx := strings.Index(out, "zzz-rule")
	require.True(t, aaaIdx >= 0 && zzzIdx >= 0, "both rules must be listed")
	require.Less(t, aaaIdx, zzzIdx, "rules must be sorted by name")
	require.Contains(t, out, "10.0.0.0/8")
	require.Contains(t, out, "office")
}

func TestTrafficLs_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/traffic-control", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficLsCmd(), "ls")
	require.NoError(t, err)
}

func TestTrafficLs_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/traffic-control", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied on /config")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficLsCmd(), "ls")
	require.Error(t, err)
	require.ErrorContains(t, err, "permission denied on /config")
}

// ---------------------------------------------------------------------------
// traffic show
// ---------------------------------------------------------------------------

func TestTrafficShow_RendersRule(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/traffic-control/rule1", &rec, map[string]any{
		"name": "rule1", "network": []string{"10.0.0.0/8"}, "rate-in": "10MB", "burst-in": "20MB",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficShowCmd(), "show", "rule1")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/config/traffic-control/rule1", rec.path)
	require.Contains(t, buf.String(), "10MB")
	require.Contains(t, buf.String(), "20MB")
}

func TestTrafficShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficShowCmd(), "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestTrafficShow_NotFound(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/traffic-control/ghost", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such rule 'ghost'")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficShowCmd(), "show", "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such rule")
}

// ---------------------------------------------------------------------------
// traffic add
// ---------------------------------------------------------------------------

func TestTrafficAdd_SendsAllSetFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/traffic-control", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficAddCmd(), "add", "rule1",
		"--network", "10.0.0.0/8",
		"--network", "192.168.0.0/16",
		"--rate-in", "10MB",
		"--rate-out", "5MB",
		"--burst-in", "20MB",
		"--burst-out", "10MB",
		"--timeframe", "mon..fri 8:00-16:30",
		"--users", "root@pam",
		"--comment", "office link",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/config/traffic-control", rec.path)
	require.Equal(t, "rule1", rec.form.Get("name"))
	require.Equal(t, []string{"10.0.0.0/8", "192.168.0.0/16"}, rec.form["network"])
	require.Equal(t, "10MB", rec.form.Get("rate-in"))
	require.Equal(t, "5MB", rec.form.Get("rate-out"))
	require.Equal(t, "20MB", rec.form.Get("burst-in"))
	require.Equal(t, "10MB", rec.form.Get("burst-out"))
	require.Equal(t, []string{"mon..fri 8:00-16:30"}, rec.form["timeframe"])
	require.Equal(t, []string{"root@pam"}, rec.form["users"])
	require.Equal(t, "office link", rec.form.Get("comment"))
	require.Contains(t, buf.String(), "rule1")
}

func TestTrafficAdd_OmitsUnsetOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/traffic-control", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficAddCmd(), "add", "rule1", "--network", "10.0.0.0/8")
	require.NoError(t, err)
	require.Equal(t, "rule1", rec.form.Get("name"))
	require.Equal(t, []string{"10.0.0.0/8"}, rec.form["network"])

	_, hasRateIn := rec.form["rate-in"]
	require.False(t, hasRateIn, "unset --rate-in must not be sent")
	_, hasComment := rec.form["comment"]
	require.False(t, hasComment, "unset --comment must not be sent")
	_, hasUsers := rec.form["users"]
	require.False(t, hasUsers, "unset --users must not be sent")
}

func TestTrafficAdd_MissingNetworkRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficAddCmd(), "add", "rule1")
	require.Error(t, err)
}

func TestTrafficAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficAddCmd(), "add", "", "--network", "10.0.0.0/8")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestTrafficAdd_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("POST /api2/json/config/traffic-control", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "rule 'rule1' already exists")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficAddCmd(), "add", "rule1", "--network", "10.0.0.0/8")
	require.Error(t, err)
	require.ErrorContains(t, err, "already exists")
}

// ---------------------------------------------------------------------------
// traffic update
// ---------------------------------------------------------------------------

func TestTrafficUpdate_SendsOnlyChangedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/traffic-control/rule1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficUpdateCmd(), "update", "rule1", "--rate-in", "20MB", "--digest", "abc123")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "20MB", rec.form.Get("rate-in"))
	require.Equal(t, "abc123", rec.form.Get("digest"))

	_, hasNetwork := rec.form["network"]
	require.False(t, hasNetwork, "unset --network must not be sent on update")
	_, hasComment := rec.form["comment"]
	require.False(t, hasComment, "unset --comment must not be sent on update")
}

func TestTrafficUpdate_DeleteProperties(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/traffic-control/rule1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficUpdateCmd(), "update", "rule1", "--delete", "comment", "--delete", "burst-in")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"comment", "burst-in"}, rec.form["delete"])
}

func TestTrafficUpdate_EmptyDeleteEntryRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficUpdateCmd(), "update", "rule1", "--delete", "")
	require.Error(t, err)
}

func TestTrafficUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficUpdateCmd(), "update", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestTrafficUpdate_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT /api2/json/config/traffic-control/rule1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusPreconditionFailed, "digest mismatch")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficUpdateCmd(), "update", "rule1", "--rate-in", "1MB")
	require.Error(t, err)
	require.ErrorContains(t, err, "digest mismatch")
}

// ---------------------------------------------------------------------------
// traffic delete
// ---------------------------------------------------------------------------

func TestTrafficDelete_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/traffic-control/rule1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficDeleteCmd(), "delete", "rule1", "--digest", "deadbeef")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "deadbeef", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "rule1")
}

func TestTrafficDelete_NoDigestOmitsParam(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/traffic-control/rule1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficDeleteCmd(), "delete", "rule1")
	require.NoError(t, err)
	_, hasDigest := rec.query["digest"]
	require.False(t, hasDigest)
}

func TestTrafficDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficDeleteCmd(), "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestTrafficDelete_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE /api2/json/config/traffic-control/rule1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such rule")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficDeleteCmd(), "delete", "rule1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such rule")
}

// ---------------------------------------------------------------------------
// traffic current
// ---------------------------------------------------------------------------

func TestTrafficCurrent_RendersRatesSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/admin/traffic-control", &rec, []map[string]any{
		{"name": "b-rule", "cur-rate-in": 1024, "cur-rate-out": 2048},
		{"name": "a-rule", "cur-rate-in": 4096, "cur-rate-out": 8192, "rate-in": "10MB"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficCurrentCmd(), "current")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/admin/traffic-control", rec.path)

	out := buf.String()
	aIdx := strings.Index(out, "a-rule")
	bIdx := strings.Index(out, "b-rule")
	require.True(t, aIdx >= 0 && bIdx >= 0)
	require.Less(t, aIdx, bIdx)
	require.Contains(t, out, "4096")
	require.Contains(t, out, "8192")
}

func TestTrafficCurrent_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/admin/traffic-control", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newTrafficCurrentCmd(), "current")
	require.Error(t, err)
	require.ErrorContains(t, err, "boom")
}

// ---------------------------------------------------------------------------
// group registration
// ---------------------------------------------------------------------------

func TestNewTrafficCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newTrafficCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete", "current"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}
}
