package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestStorageCreate_AuditCommonFlags verifies the create+set common field flags
// added by the flag audit reach the POST /storage body with the correct
// API-side keys (note the underscore vs hyphen mismatches the API expects).
func TestStorageCreate_AuditCommonFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{"storage": "zfs", "type": "zfspool"})

	_, err := run(t, f, "create",
		"--storage", "zfs",
		"--type", "zfspool",
		"--blocksize", "8k",
		"--nocow",
		"--saferemove",
		"--nowritecache",
		"--saferemove-stepsize", "1024",
		"--saferemove-throughput", "100",
		"--snapshot-as-volume-chain",
		"--tagged-only",
		"--zfs-base-path", "/dev/zvol",
		"--comstar-hg", "hg0",
		"--comstar-tg", "tg0",
		"--lio-tpg", "tpg1",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "8k", rec.form.Get("blocksize"))
	require.Equal(t, "1", rec.form.Get("nocow"))
	require.Equal(t, "1", rec.form.Get("saferemove"))
	require.Equal(t, "1", rec.form.Get("nowritecache"))
	require.Equal(t, "1024", rec.form.Get("saferemove-stepsize"))
	require.Equal(t, "100", rec.form.Get("saferemove_throughput"))
	require.Equal(t, "1", rec.form.Get("snapshot-as-volume-chain"))
	require.Equal(t, "1", rec.form.Get("tagged_only"))
	require.Equal(t, "/dev/zvol", rec.form.Get("zfs-base-path"))
	require.Equal(t, "hg0", rec.form.Get("comstar_hg"))
	require.Equal(t, "tg0", rec.form.Get("comstar_tg"))
	require.Equal(t, "tpg1", rec.form.Get("lio_tpg"))
}

// TestStorageCreate_AuditCreateOnlyFlags verifies the create-only field flags
// (authsupported, iscsiprovider, base) reach the POST body.
func TestStorageCreate_AuditCreateOnlyFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{"storage": "iscsi0", "type": "iscsi"})

	_, err := run(t, f, "create",
		"--storage", "iscsi0",
		"--type", "iscsi",
		"--authsupported", "none",
		"--iscsiprovider", "comstar",
		"--base", "local:base-100-disk-0",
	)
	require.NoError(t, err)

	require.Equal(t, "none", rec.form.Get("authsupported"))
	require.Equal(t, "comstar", rec.form.Get("iscsiprovider"))
	require.Equal(t, "local:base-100-disk-0", rec.form.Get("base"))
}

// TestStorageSet_AuditCommonFlags verifies the common field flags are accepted by
// `storage set` and reach the PUT body.
func TestStorageSet_AuditCommonFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/storage/lvm0", &rec, map[string]any{"storage": "lvm0", "type": "lvm"})

	_, err := run(t, f, "set", "lvm0",
		"--saferemove",
		"--saferemove-stepsize", "512",
		"--tagged-only",
		"--blocksize", "16k",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "1", rec.form.Get("saferemove"))
	require.Equal(t, "512", rec.form.Get("saferemove-stepsize"))
	require.Equal(t, "1", rec.form.Get("tagged_only"))
	require.Equal(t, "16k", rec.form.Get("blocksize"))
}

// TestStorageSet_RejectsCreateOnlyFlags verifies the create-only flags are not
// registered on `storage set`.
func TestStorageSet_RejectsCreateOnlyFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/storage/lvm0", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, map[string]any{})
	})

	_, err := run(t, f, "set", "lvm0", "--authsupported", "none")
	require.Error(t, err)
	require.False(t, called, "create-only flag must not be accepted by set")
}

// TestStorageCreate_OmitsUnsetAuditFlags verifies the audit flags are omitted
// from the request body when not supplied.
func TestStorageCreate_OmitsUnsetAuditFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{"storage": "d", "type": "dir"})

	_, err := run(t, f, "create", "--storage", "d", "--type", "dir", "--path", "/srv/d")
	require.NoError(t, err)

	for _, key := range []string{
		"blocksize", "nocow", "saferemove", "nowritecache", "saferemove-stepsize",
		"saferemove_throughput", "snapshot-as-volume-chain", "tagged_only", "zfs-base-path",
		"comstar_hg", "comstar_tg", "lio_tpg", "authsupported", "iscsiprovider", "base",
	} {
		require.False(t, rec.form.Has(key), "%s must be omitted when unset", key)
	}
}

// TestStorageNodeList_Table verifies `pmx storage node-list` queries
// GET /nodes/{node}/storage and renders the per-node storage status.
func TestStorageNodeList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pve1/storage", &rec, []map[string]any{
		{"storage": "local", "type": "dir", "content": "iso,vztmpl", "active": 1, "enabled": 1,
			"total": 100, "used": 40, "avail": 60},
		{"storage": "cephfs", "type": "cephfs", "content": "backup", "active": 0, "enabled": 1,
			"total": 200, "used": 10, "avail": 190},
	})

	out, err := run(t, f, "--node", "pve1", "node-list")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/storage", rec.path)
	require.Contains(t, out, "local")
	require.Contains(t, out, "cephfs")
	require.Contains(t, out, "STORAGE")
	require.Contains(t, out, "AVAIL")
}

// TestStorageNodeList_ForwardsFilters verifies the optional filter flags are
// forwarded as query parameters.
func TestStorageNodeList_ForwardsFilters(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("GET /api2/json/nodes/pve1/storage", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.form = r.URL.Query()
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := run(t, f, "--node", "pve1", "node-list",
		"--content", "backup",
		"--enabled",
		"--storage-id", "local",
		"--target-node", "pve2",
	)
	require.NoError(t, err)

	require.Equal(t, "backup", rec.form.Get("content"))
	require.Equal(t, "1", rec.form.Get("enabled"))
	require.Equal(t, "local", rec.form.Get("storage"))
	require.Equal(t, "pve2", rec.form.Get("target"))
}

// TestStorageUpload_AuditChecksumFlags verifies --checksum and
// --checksum-algorithm are forwarded as multipart form fields with their exact
// API parameter keys.
func TestStorageUpload_AuditChecksumFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:imgcopy::root@pam:"
	var gotChecksum, gotAlgo string
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			testhelper.WriteError(w, http.StatusBadRequest, "bad multipart")
			return
		}
		gotChecksum = r.FormValue("checksum")
		gotAlgo = r.FormValue("checksum-algorithm")
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	path := writeTempFile(t, "pmx-cli-test.iso", "fake-iso-bytes")
	_, err := run(t, f, "--node", "pve1", "upload", "local",
		"--file", path,
		"--checksum", "abcdef1234567890abcdef1234567890",
		"--checksum-algorithm", "sha256",
	)
	require.NoError(t, err)
	require.Equal(t, "abcdef1234567890abcdef1234567890", gotChecksum)
	require.Equal(t, "sha256", gotAlgo)
}

// TestStorageUpload_OmitsChecksumWhenAbsent verifies checksum fields are not
// sent in the multipart body when --checksum and --checksum-algorithm are omitted.
func TestStorageUpload_OmitsChecksumWhenAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:imgcopy::root@pam:"
	var gotChecksum, gotAlgo string
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			testhelper.WriteError(w, http.StatusBadRequest, "bad multipart")
			return
		}
		gotChecksum = r.FormValue("checksum")
		gotAlgo = r.FormValue("checksum-algorithm")
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	path := writeTempFile(t, "pmx-cli-no-checksum.iso", "fake-iso-bytes")
	_, err := run(t, f, "--node", "pve1", "upload", "local", "--file", path)
	require.NoError(t, err)
	require.Empty(t, gotChecksum, "checksum must not be sent when --checksum is absent")
	require.Empty(t, gotAlgo, "checksum-algorithm must not be sent when --checksum-algorithm is absent")
}

// TestStorageNodeList_RequiresNode verifies the command fails without a node.
func TestStorageNodeList_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("GET /api2/json/nodes/pve1/storage", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := run(t, f, "node-list")
	require.Error(t, err)
	require.False(t, called, "no request must be made without a node")
}
