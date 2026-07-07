package permshared_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	yaml "github.com/goccy/go-yaml"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"

	"github.com/fivetwenty-io/pmx-cli/internal/cli/permshared"
)

// ---- PVEBool ----------------------------------------------------------------

func TestPVEBool_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		input    string
		wantCell string
		wantBool bool
	}{
		{name: "number one", input: `1`, wantCell: "1", wantBool: true},
		{name: "number zero", input: `0`, wantCell: "0", wantBool: false},
		{name: "string one", input: `"1"`, wantCell: "1", wantBool: true},
		{name: "string zero", input: `"0"`, wantCell: "0", wantBool: false},
		{name: "bool true", input: `true`, wantCell: "1", wantBool: true},
		{name: "bool false", input: `false`, wantCell: "0", wantBool: false},
		{name: "string true", input: `"true"`, wantCell: "1", wantBool: true},
		{name: "null", input: `null`, wantCell: "", wantBool: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b permshared.PVEBool
			require.NoError(t, json.Unmarshal([]byte(tc.input), &b))
			require.Equal(t, tc.wantCell, b.Cell())
			require.Equal(t, tc.wantBool, b.Bool())
		})
	}
}

func TestPVEBool_Absent_RendersEmptyCell(t *testing.T) {
	t.Parallel()

	type wrapper struct {
		Propagate permshared.PVEBool `json:"propagate"`
	}
	var w wrapper
	require.NoError(t, json.Unmarshal([]byte(`{}`), &w))
	require.Equal(t, "", w.Propagate.Cell())
	require.False(t, w.Propagate.Bool())
}

func TestPVEBool_UnmarshalJSON_InvalidEncodings(t *testing.T) {
	t.Parallel()

	// "frog" starts with 'f' (routed to the bool branch) but is not a valid
	// JSON bool literal; [1,2] and {"nested":true} are routed to the numeric
	// branch (anything not starting with t/f/") and are not valid numbers.
	cases := []string{`frog`, `[1,2]`, `{"nested":true}`}
	for _, in := range cases {
		var b permshared.PVEBool
		err := json.Unmarshal([]byte(in), &b)
		require.Error(t, err, "input %q should fail to decode", in)
	}
}

// ---- DecodeAclList ----------------------------------------------------------

func TestDecodeAclList(t *testing.T) {
	t.Parallel()

	resp := access.ListAclResponse{
		json.RawMessage(`{"path":"/vms/100","type":"user","ugid":"u@pve","roleid":"PVEAdmin","propagate":1}`),
		json.RawMessage(`{"path":"/vms/100","type":"group","ugid":"grp","roleid":"PVEAuditor","propagate":"0"}`),
	}
	entries, err := permshared.DecodeAclList(&resp)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "/vms/100", entries[0].Path)
	require.Equal(t, "user", entries[0].Type)
	require.Equal(t, "u@pve", entries[0].Ugid)
	require.Equal(t, "PVEAdmin", entries[0].Roleid)
	require.True(t, entries[0].Propagate.Bool())
	require.False(t, entries[1].Propagate.Bool())
}

func TestDecodeAclList_NilResp_ReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	entries, err := permshared.DecodeAclList(nil)
	require.NoError(t, err)
	require.NotNil(t, entries)
	require.Empty(t, entries)
}

func TestDecodeAclList_MalformedEntry_ReturnsError(t *testing.T) {
	t.Parallel()

	resp := access.ListAclResponse{json.RawMessage(`{"path": 123}`)}
	_, err := permshared.DecodeAclList(&resp)
	require.Error(t, err)
}

// ---- FilterByPath -----------------------------------------------------------

func aclFixture() []permshared.AclEntry {
	return []permshared.AclEntry{
		{Path: "/vms/100", Type: "user", Ugid: "a@pve", Roleid: "PVEAdmin"},
		{Path: "/vms/1001", Type: "user", Ugid: "b@pve", Roleid: "PVEAdmin"},
		{Path: "/vms", Type: "group", Ugid: "grp", Roleid: "PVEAuditor"},
		{Path: "/storage/local", Type: "user", Ugid: "c@pve", Roleid: "PVEDatastoreUser"},
	}
}

func TestFilterByPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		path      string
		exact     bool
		wantPaths []string
	}{
		{
			name:      "empty path matches everything",
			path:      "",
			exact:     false,
			wantPaths: []string{"/vms/100", "/vms/1001", "/vms", "/storage/local"},
		},
		{
			name:      "exact match",
			path:      "/vms/100",
			exact:     true,
			wantPaths: []string{"/vms/100"},
		},
		{
			name:      "exact match with no hits",
			path:      "/vms/999",
			exact:     true,
			wantPaths: []string{},
		},
		{
			name:  "prefix match is byte-wise, not path-boundary aware",
			path:  "/vms/100",
			exact: false,
			// mirrors internal/cli/access/acl.go's aclMatch: "/vms/1001" also
			// satisfies the raw byte prefix "/vms/100", so it is included.
			wantPaths: []string{"/vms/100", "/vms/1001"},
		},
		{
			name:      "prefix match on a shorter root",
			path:      "/vms",
			exact:     false,
			wantPaths: []string{"/vms/100", "/vms/1001", "/vms"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := permshared.FilterByPath(aclFixture(), tc.path, tc.exact)
			gotPaths := make([]string, 0, len(got))
			for _, e := range got {
				gotPaths = append(gotPaths, e.Path)
			}
			require.Equal(t, tc.wantPaths, gotPaths)
		})
	}
}

// ---- ParentChain --------------------------------------------------------------

func TestParentChain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
		want []string
	}{
		{name: "root", path: "/", want: []string{"/"}},
		{name: "vm guest", path: "/vms/100", want: []string{"/", "/vms", "/vms/100"}},
		{
			name: "nested sdn vnet",
			path: "/sdn/zones/z1/v1",
			want: []string{"/", "/sdn", "/sdn/zones", "/sdn/zones/z1", "/sdn/zones/z1/v1"},
		},
		{name: "storage", path: "/storage/s1", want: []string{"/", "/storage", "/storage/s1"}},
		{name: "empty string treated as root", path: "", want: []string{"/"}},
		{name: "trailing slash trimmed", path: "/vms/100/", want: []string{"/", "/vms", "/vms/100"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, permshared.ParentChain(tc.path))
		})
	}
}

// ---- DecodePermissions --------------------------------------------------------

func TestDecodePermissions(t *testing.T) {
	t.Parallel()

	raw := access.ListPermissionsResponse(`{
		"/vms/100": {"VM.Audit": 1, "VM.Console": "0"},
		"/": {"Sys.Audit": true}
	}`)
	tree, err := permshared.DecodePermissions(&raw)
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]bool{
		"/vms/100": {"VM.Audit": true, "VM.Console": false},
		"/":        {"Sys.Audit": true},
	}, tree)
}

func TestDecodePermissions_NilOrEmpty(t *testing.T) {
	t.Parallel()

	got, err := permshared.DecodePermissions(nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)

	empty := access.ListPermissionsResponse(``)
	got, err = permshared.DecodePermissions(&empty)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestDecodePermissions_MalformedResponse_ReturnsError(t *testing.T) {
	t.Parallel()

	raw := access.ListPermissionsResponse(`{"/vms/100": "not-an-object"}`)
	_, err := permshared.DecodePermissions(&raw)
	require.Error(t, err)
}

// ---- GrantRevokeParams --------------------------------------------------------

func TestGrantRevokeParams(t *testing.T) {
	t.Parallel()

	users := "u1@pve,u2@pve"
	groups := "g1"
	tokens := "u1@pve!tok"

	t.Run("grant with propagate unset leaves Propagate nil", func(t *testing.T) {
		t.Parallel()
		params := permshared.GrantRevokeParams("/vms/100", "PVEAdmin", &users, nil, nil, nil, false)
		require.Equal(t, "/vms/100", params.Path)
		require.Equal(t, "PVEAdmin", params.Roles)
		require.Equal(t, &users, params.Users)
		require.Nil(t, params.Groups)
		require.Nil(t, params.Tokens)
		require.Nil(t, params.Delete)
		require.Nil(t, params.Propagate)
	})

	t.Run("revoke sets Delete true", func(t *testing.T) {
		t.Parallel()
		params := permshared.GrantRevokeParams("/vms/100", "PVEAdmin", nil, &groups, nil, nil, true)
		require.NotNil(t, params.Delete)
		require.True(t, *params.Delete)
		require.Equal(t, &groups, params.Groups)
	})

	t.Run("propagate false is passed through explicitly", func(t *testing.T) {
		t.Parallel()
		no := false
		params := permshared.GrantRevokeParams("/vms/100", "PVEAdmin", nil, nil, &tokens, &no, false)
		require.NotNil(t, params.Propagate)
		require.False(t, *params.Propagate)
		require.Equal(t, &tokens, params.Tokens)
	})

	t.Run("propagate true is passed through explicitly", func(t *testing.T) {
		t.Parallel()
		yes := true
		params := permshared.GrantRevokeParams("/vms/100", "PVEAdmin", &users, &groups, &tokens, &yes, false)
		require.NotNil(t, params.Propagate)
		require.True(t, *params.Propagate)
	})
}

// ---- RenderAclList --------------------------------------------------------------

func TestRenderAclList_NonInherited_SortsByTypeUgidRoleid(t *testing.T) {
	t.Parallel()

	entries := []permshared.AclEntry{
		{Path: "/vms/100", Type: "user", Ugid: "b@pve", Roleid: "PVEAdmin"},
		{Path: "/vms/100", Type: "group", Ugid: "grp", Roleid: "PVEAuditor"},
		{Path: "/vms/100", Type: "user", Ugid: "a@pve", Roleid: "PVEAdmin"},
	}
	result := permshared.RenderAclList(entries, false)

	require.Equal(t, []string{"TYPE", "UGID", "ROLEID", "PROPAGATE"}, result.Headers)
	require.Equal(t, [][]string{
		{"group", "grp", "PVEAuditor", ""},
		{"user", "a@pve", "PVEAdmin", ""},
		{"user", "b@pve", "PVEAdmin", ""},
	}, result.Rows)

	sortedEntries, ok := result.Raw.([]permshared.AclEntry)
	require.True(t, ok)
	require.Len(t, sortedEntries, 3)
}

func TestRenderAclList_Inherited_SortsByDepthThenTypeUgidRoleid(t *testing.T) {
	t.Parallel()

	entries := []permshared.AclEntry{
		{Path: "/vms/100", Type: "user", Ugid: "a@pve", Roleid: "PVEAdmin"},
		{Path: "/", Type: "group", Ugid: "grp", Roleid: "PVEAuditor"},
		{Path: "/vms", Type: "user", Ugid: "z@pve", Roleid: "PVEAdmin"},
	}
	result := permshared.RenderAclList(entries, true)

	require.Equal(t, []string{"INHERITED-FROM", "TYPE", "UGID", "ROLEID", "PROPAGATE"}, result.Headers)
	require.Equal(t, [][]string{
		{"/", "group", "grp", "PVEAuditor", ""},
		{"/vms", "user", "z@pve", "PVEAdmin", ""},
		{"/vms/100", "user", "a@pve", "PVEAdmin", ""},
	}, result.Rows)
}

func TestRenderAclList_PropagateCellReflectsDecodedValue(t *testing.T) {
	t.Parallel()

	var propTrue permshared.PVEBool
	require.NoError(t, json.Unmarshal([]byte(`1`), &propTrue))

	entries := []permshared.AclEntry{
		{Path: "/vms/100", Type: "user", Ugid: "a@pve", Roleid: "PVEAdmin", Propagate: propTrue},
	}
	result := permshared.RenderAclList(entries, false)
	require.Equal(t, [][]string{{"user", "a@pve", "PVEAdmin", "1"}}, result.Rows)
}

// ---- RenderEffective --------------------------------------------------------------

func TestRenderEffective(t *testing.T) {
	t.Parallel()

	tree := map[string]map[string]bool{
		"/vms/100": {"VM.Console": true, "VM.Audit": false},
		"/":        {"Sys.Audit": true},
	}
	result := permshared.RenderEffective(tree)

	require.Equal(t, []string{"PATH", "PRIVS"}, result.Headers)
	require.Equal(t, [][]string{
		{"/", "Sys.Audit"},
		{"/vms/100", "VM.Audit,VM.Console"},
	}, result.Rows)
	require.Equal(t, tree, result.Raw)
}

func TestRenderEffective_Empty(t *testing.T) {
	t.Parallel()

	result := permshared.RenderEffective(map[string]map[string]bool{})
	require.Equal(t, []string{"PATH", "PRIVS"}, result.Headers)
	require.Empty(t, result.Rows)
}

func TestAclEntry_MarshalRoundTrip_Propagate(t *testing.T) {
	t.Parallel()

	var set, unset permshared.AclEntry
	require.NoError(t, json.Unmarshal(
		[]byte(`{"path":"/vms/100","type":"user","ugid":"bob@pve","roleid":"PVEVMAdmin","propagate":1}`), &set))
	require.NoError(t, json.Unmarshal(
		[]byte(`{"path":"/vms/100","type":"user","ugid":"bob@pve","roleid":"PVEVMAdmin"}`), &unset))

	jsonOut, err := json.Marshal(set)
	require.NoError(t, err)
	require.Contains(t, string(jsonOut), `"propagate":1`)

	yamlOut, err := yaml.Marshal([]permshared.AclEntry{set, unset})
	require.NoError(t, err)
	require.Contains(t, string(yamlOut), "propagate: 1")
	require.Contains(t, string(yamlOut), "propagate: null")
	require.NotContains(t, string(yamlOut), "propagate: {}")
}
