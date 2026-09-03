package pbs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// s3ConfigPath is the base /config/s3 endpoint.
const s3ConfigPath = "/api2/json/config/s3"

// s3ID is the sample endpoint ID reused across the s3 tests.
const s3ID = "minio-lab"

// --- s3 ls -------------------------------------------------------------------

func TestS3Ls_RendersTableSortedByID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+s3ConfigPath, &rec, []map[string]any{
		{"id": "wasabi", "endpoint": "s3.wasabisys.com", "access-key": "AKIAWASABI", "region": "us-east-1"},
		{"id": "minio-lab", "endpoint": "minio.lab.internal", "access-key": "AKIAMINIO", "port": 9000},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, s3ConfigPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "ACCESS-KEY")
	require.Less(t, bytes.Index(buf.Bytes(), []byte("minio-lab")), bytes.Index(buf.Bytes(), []byte("wasabi")))
	require.Contains(t, out, "9000")
	require.Contains(t, out, "us-east-1")
}

func TestS3Ls_EmptyRenders(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+s3ConfigPath, &recordedRequest{}, []map[string]any{})
	requireEmptyListRenders(t, pc, newS3Cmd, "s3", "ls")
}

func TestS3Ls_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+s3ConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list s3 endpoints")
}

// --- s3 show -----------------------------------------------------------------

func TestS3Show_RendersFieldsWithoutSecret(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+s3ConfigPath+"/"+s3ID, &rec, map[string]any{
		"id": s3ID, "endpoint": "minio.lab.internal", "access-key": "AKIAMINIO",
		"port": 9000, "path-style": 1, "provider-quirks": []string{"skip-if-none-match-header"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "show", s3ID)
	require.NoError(t, err)

	require.Equal(t, s3ConfigPath+"/"+s3ID, rec.path)
	out := buf.String()
	require.Contains(t, out, "minio.lab.internal")
	require.Contains(t, out, "skip-if-none-match-header")
	require.NotContains(t, out, "secret")
}

func TestS3Show_DefaultsListsUnsetOptions(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+s3ConfigPath+"/"+s3ID, &recordedRequest{}, map[string]any{
		"id": s3ID, "endpoint": "minio.lab.internal", "access-key": "AKIAMINIO",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "show", s3ID, "--defaults")
	require.NoError(t, err)

	// MergeDefaults wraps the raw payload as {"set": …, "defaults": …}; the
	// schema default for path-style is false, which optionsgen renders "false".
	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "minio.lab.internal", got.Set["endpoint"])
	require.Equal(t, "false", got.Defaults["path-style"])
	require.NotContains(t, got.Defaults, "secret-key")
	require.NotContains(t, got.Set, "secret-key")
}

// --- s3 add ------------------------------------------------------------------

func TestS3Add_PostsRequiredAndOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+s3ConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "add", s3ID,
		"--endpoint", "minio.lab.internal", "--access-key", "AKIAMINIO", "--secret-key", "s3cr3t",
		"--port", "9000", "--path-style", "--region", "lab",
		"--provider-quirk", "skip-if-none-match-header",
		"--provider-quirk", "delete-objects-via-delete-object",
		"--limit-active-requests", "50")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, s3ConfigPath, rec.path)
	require.Equal(t, s3ID, rec.form.Get("id"))
	require.Equal(t, "minio.lab.internal", rec.form.Get("endpoint"))
	require.Equal(t, "AKIAMINIO", rec.form.Get("access-key"))
	require.Equal(t, "s3cr3t", rec.form.Get("secret-key"))
	require.Equal(t, "9000", rec.form.Get("port"))
	require.Equal(t, "1", rec.form.Get("path-style"))
	require.Equal(t, "lab", rec.form.Get("region"))
	require.Equal(t, "50", rec.form.Get("limit-active-requests"))
	require.NotEmpty(t, rec.form["provider-quirks"])
	require.Empty(t, rec.form.Get("use-node-proxy"), "unset optional bool must not be sent")
	require.Contains(t, buf.String(), `S3 endpoint "minio-lab" created.`)
}

func TestS3Add_RequiresCredentialsAndEndpoint(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"endpoint", []string{"--access-key", "a", "--secret-key", "b"}, "endpoint"},
		{"access-key", []string{"--endpoint", "e", "--secret-key", "b"}, "access-key"},
		{"secret-key", []string{"--endpoint", "e", "--access-key", "a"}, "secret-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := run(deps, &buf, newS3Cmd(), append([]string{"s3", "add", s3ID}, tc.args...)...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestS3Add_RejectsUnknownProviderQuirk(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "add", s3ID,
		"--endpoint", "e", "--access-key", "a", "--secret-key", "b",
		"--provider-quirk", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--provider-quirk")
	require.Contains(t, err.Error(), "bogus")
}

// --- s3 update ---------------------------------------------------------------

func TestS3Update_SendsOnlyChangedFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+s3ConfigPath+"/"+s3ID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "update", s3ID,
		"--secret-key", "rotated", "--use-node-proxy=false", "--delete", "region", "--digest", "abc123")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, s3ConfigPath+"/"+s3ID, rec.path)
	require.Equal(t, "rotated", rec.form.Get("secret-key"))
	require.Equal(t, "0", rec.form.Get("use-node-proxy"))
	require.Equal(t, "region", rec.form.Get("delete"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Empty(t, rec.form.Get("endpoint"))
	require.Empty(t, rec.form.Get("port"))
	require.Contains(t, buf.String(), `S3 endpoint "minio-lab" updated.`)
}

func TestS3Update_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "update", s3ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestS3Update_RejectsUndeletableProperty(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "update", s3ID, "--delete", "endpoint")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
	require.Contains(t, err.Error(), "endpoint")
}

// --- s3 delete ---------------------------------------------------------------

func TestS3Delete_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "delete", s3ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

func TestS3Delete_SendsDigestAndRendersMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+s3ConfigPath+"/"+s3ID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "delete", s3ID, "--yes", "--digest", "abc123")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, s3ConfigPath+"/"+s3ID, rec.path)
	// The SDK sends DELETE parameters in the query string, never the body.
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), `S3 endpoint "minio-lab" deleted.`)
}

// --- s3 buckets --------------------------------------------------------------

func TestS3Buckets_RendersStringList(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+s3ConfigPath+"/"+s3ID+"/list-buckets", &rec, []string{"pbs-backups", "archive"})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "buckets", s3ID)
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, s3ConfigPath+"/"+s3ID+"/list-buckets", rec.path)
	out := buf.String()
	require.Contains(t, out, "BUCKET")
	require.Contains(t, out, "pbs-backups")
	require.Contains(t, out, "archive")
}

func TestS3Buckets_RendersObjectList(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+s3ConfigPath+"/"+s3ID+"/list-buckets", &recordedRequest{},
		[]map[string]any{{"name": "pbs-backups", "creation-date": "2026-01-01T00:00:00Z"}})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "buckets", s3ID)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "pbs-backups")
}

// The API schema declares list-buckets as returning null, so a literal null
// body is a real server response. It must render like an empty list: the
// header row in table view and [] in JSON, never nothing and never null.
func TestS3Buckets_NullMeansNoBuckets(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+s3ConfigPath+"/"+s3ID+"/list-buckets", &recordedRequest{}, nil)
	requireEmptyListRenders(t, pc, newS3Cmd, "s3", "buckets", s3ID)
}

func TestS3Buckets_JSONOrderMatchesTable(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+s3ConfigPath+"/"+s3ID+"/list-buckets", &recordedRequest{}, []string{"zeta", "alpha"})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "buckets", s3ID)
	require.NoError(t, err)

	var got []string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, []string{"alpha", "zeta"}, got, "the JSON view must be sorted exactly like the table")
}

func TestS3Buckets_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+s3ConfigPath+"/"+s3ID+"/list-buckets", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "endpoint unreachable")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "buckets", s3ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), `list buckets on s3 endpoint "minio-lab"`)
}
