package storage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/optionschema"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// jsonTail strips diagnostics printed ahead of the JSON document (e.g. the
// inline-secret config warning) so the payload can be decoded.
func jsonTail(t *testing.T, out string) string {
	t.Helper()
	i := strings.IndexAny(out, "{[")
	require.GreaterOrEqual(t, i, 0, "no JSON document in output: %q", out)
	return out[i:]
}

// TestStorageOptionSchemas_GeneratedTable sanity-checks the generated flat
// storage option table.
func TestStorageOptionSchemas_GeneratedTable(t *testing.T) {
	require.NotEmpty(t, storageOptionSchemas)
	for _, s := range storageOptionSchemas {
		require.NotEqual(t, "storage", s.Name, "create meta-parameter leaked into the table")
		require.NotEqual(t, "type", s.Name, "type discriminator leaked into the table")
	}

	target := optionschema.Find(storageOptionSchemas, "target")
	require.NotNil(t, target)
	require.Equal(t, "iscsi-target", target.Flag, "target must map to the CLI's --iscsi-target")

	require.NotNil(t, optionschema.Find(storageOptionSchemas, "prune-backups"))
}

// TestStorageTypeOptions_MatchSchemas is the drift guard between the two
// generated sources: every option the plugin mapping claims must exist in the
// API schema table, and every API type enum member must have a mapping.
func TestStorageTypeOptions_MatchSchemas(t *testing.T) {
	require.Len(t, storageTypeOptions, 14, "storage type count changed — regenerate both tables")
	for stype, set := range storageTypeOptions {
		require.NotEmpty(t, set, "type %q has no options", stype)
		for name := range set {
			require.NotNil(t, optionschema.Find(storageOptionSchemas, name),
				"type %q option %q missing from the API schema table", stype, name)
		}
	}
}

// TestStorageCommonOptionSchemas verifies the intersection used for set-flag
// enrichment holds only options every type accepts.
func TestStorageCommonOptionSchemas(t *testing.T) {
	common := commonOptionSchemas()
	require.NotEmpty(t, common)
	names := make(map[string]bool, len(common))
	for _, s := range common {
		names[s.Name] = true
		for stype, set := range storageTypeOptions {
			require.Contains(t, set, s.Name, "common option %q not accepted by type %q", s.Name, stype)
		}
	}
	require.True(t, names["content"], "content is accepted by every plugin")
	require.True(t, names["disable"], "disable is accepted by every plugin")
	require.False(t, names["pool"], "pool is type-specific and must not be common")
}

// TestStorageDescribe_Catalog verifies the offline catalog and its TYPES column.
func TestStorageDescribe_Catalog(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	out, err := run(t, f, "describe")
	require.NoError(t, err)
	require.Contains(t, out, "TYPES")
	require.Contains(t, out, "iscsi-target")
	require.Contains(t, out, "all", "options accepted everywhere collapse to \"all\"")

	out, err = run(t, f, "describe", "thinpool")
	require.NoError(t, err)
	require.Contains(t, out, "lvmthin")

	_, err = run(t, f, "describe", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pve storage describe")
}

// TestStorageDescribe_TypeFilter verifies --type filtering and usage markers.
func TestStorageDescribe_TypeFilter(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	out, err := run(t, f, "describe", "--type", "zfspool")
	require.NoError(t, err)
	require.Contains(t, out, "USE")
	require.Contains(t, out, "required, create-only", "zfspool's pool option is fixed and required")
	require.NotContains(t, out, "thinpool", "lvmthin-only options must be filtered out")

	_, err = run(t, f, "describe", "--type", "floppy")
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown type "floppy"`)
	require.Contains(t, err.Error(), "zfspool")
}

// TestStorageGet_Defaults verifies `get --defaults` merges only the defaults
// the storage's type accepts.
func TestStorageGet_Defaults(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/storage/local", &rec,
		map[string]any{"storage": "local", "type": "dir", "path": "/var/lib/vz"})

	out, err := run(t, f, "-o", "json", "get", "local", "--defaults")
	require.NoError(t, err)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonTail(t, out)), &got))
	require.Equal(t, "dir", got.Set["type"])
	require.Equal(t, "yes", got.Defaults["create-base-path"], "dir accepts create-base-path")
	require.NotContains(t, got.Defaults, "pool", "zfspool/rbd-only options must not gain defaults on a dir storage")
	require.NotContains(t, got.Defaults, "path", "set options must not appear in defaults")
}

// TestStorageGet_DefaultsUnknownType verifies an out-of-tree storage type
// claims no defaults at all.
func TestStorageGet_DefaultsUnknownType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/storage/ext", &rec,
		map[string]any{"storage": "ext", "type": "freenas", "pool": "tank"})

	out, err := run(t, f, "-o", "json", "get", "ext", "--defaults")
	require.NoError(t, err)

	var got struct {
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonTail(t, out)), &got))
	require.Empty(t, got.Defaults, "unknown plugin type must claim no schema knowledge")
}
