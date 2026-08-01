package pbs

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// Every `ls` in this package sorts its table rows, and -o json/yaml must show
// that same order with each entry's full API object intact. Sorting only the
// decoded rows while emitting the API's own order left the two views
// disagreeing, so a JSON consumer and a table reader saw different orderings
// of the same list.
//
// Each case below serves its entries in reverse of the sorted order, carrying
// a marker field the typed row struct does not declare. Asserting on the
// marker proves the raw object travelled with its row rather than the sorted
// order coinciding with the served order.
//
// The cases cover one command per decode shape in the package rather than all
// twenty-eight converted sites: the explicit-loop decode, the compact-loop
// decode, the nodeDecodeArray decode, a positional-argument command, a
// multi-key sort, and the one site that post-processes the raw objects to
// strip secrets.
func TestListCommands_PairRawEntriesWithSortedRows(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		payload []map[string]any
		cmd     func() *cobra.Command
		args    []string
		// want is the expected raw output after sorting, as (key, value)
		// pairs read from each entry in order.
		wantKey string
		want    []string
		marker  []string
	}{
		{
			name:    "acl ls sorts by path then ugid",
			pattern: "GET " + aclPath,
			payload: []map[string]any{
				{"path": "/datastore/s2", "ugid": "bob@pbs", "ugid_type": "user", "roleid": "Audit", "marker": "third"},
				{"path": "/datastore/s1", "ugid": "carol@pbs", "ugid_type": "user", "roleid": "Audit", "marker": "second"},
				{"path": "/datastore/s1", "ugid": "alice@pbs", "ugid_type": "user", "roleid": "Admin", "marker": "first"},
			},
			cmd:     newACLLsCmd,
			args:    []string{"ls"},
			wantKey: "ugid",
			want:    []string{"alice@pbs", "carol@pbs", "bob@pbs"},
			marker:  []string{"first", "second", "third"},
		},
		{
			name:    "user ls sorts by userid",
			pattern: "GET " + usersPath,
			payload: []map[string]any{
				{"userid": "zoe@pbs", "enable": true, "marker": "second"},
				{"userid": "alice@pbs", "enable": true, "marker": "first"},
			},
			cmd:     newUserLsCmd,
			args:    []string{"ls"},
			wantKey: "userid",
			want:    []string{"alice@pbs", "zoe@pbs"},
			marker:  []string{"first", "second"},
		},
		{
			name:    "datastore usage sorts by store",
			pattern: "GET " + pathStatusUsage,
			payload: []map[string]any{
				{"store": "tank", "total": 200, "marker": "second"},
				{"store": "backup", "total": 100, "marker": "first"},
			},
			cmd:     newDatastoreUsageCmd,
			args:    []string{"usage"},
			wantKey: "store",
			want:    []string{"backup", "tank"},
			marker:  []string{"first", "second"},
		},
		{
			name:    "notification endpoint gotify ls sorts by name",
			pattern: "GET " + notifGotifyPath,
			payload: []map[string]any{
				{"name": "zulu", "server": "https://z.example.com", "marker": "second"},
				{"name": "alpha", "server": "https://a.example.com", "marker": "first"},
			},
			cmd:     newNotifEndpointGotifyLsCmd,
			args:    []string{"ls"},
			wantKey: "name",
			want:    []string{"alpha", "zulu"},
			marker:  []string{"first", "second"},
		},
		{
			name:    "tape media ls sorts by label-text",
			pattern: "GET " + tapeMediaListPath,
			payload: []map[string]any{
				{"label-text": "TAPE02", "marker": "second"},
				{"label-text": "TAPE01", "marker": "first"},
			},
			cmd:     newTapeMediaLsCmd,
			args:    []string{"ls"},
			wantKey: "label-text",
			want:    []string{"TAPE01", "TAPE02"},
			marker:  []string{"first", "second"},
		},
		{
			name:    "tape drive ls sorts by name",
			pattern: "GET " + pathTapeDrive,
			payload: []map[string]any{
				{"name": "drive2", "path": "/dev/sg1", "marker": "second"},
				{"name": "drive1", "path": "/dev/sg0", "marker": "first"},
			},
			cmd:     newTapeDriveLsCmd,
			args:    []string{"ls"},
			wantKey: "name",
			want:    []string{"drive1", "drive2"},
			marker:  []string{"first", "second"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, pc := newFakeClient(t)
			recordJSON(f, tc.pattern, &recordedRequest{}, tc.payload)

			deps := depsFor(t, pc, output.FormatJSON, false)
			var buf bytes.Buffer
			require.NoError(t, run(deps, &buf, tc.cmd(), tc.args...))

			var got []map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
			require.Len(t, got, len(tc.want))

			for i := range tc.want {
				require.Equal(t, tc.want[i], got[i][tc.wantKey],
					"raw entry %d is out of the table's sorted order", i)
				require.Equal(t, tc.marker[i], got[i]["marker"],
					"raw entry %d lost the API fields belonging to its row", i)
			}
		})
	}
}

// TestTapeDriveInventory_PairsRawWithSortedRows covers a positional-argument
// command whose entries are decoded through nodeDecodeArray.
func TestTapeDriveInventory_PairsRawWithSortedRows(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET /api2/json/tape/drive/drive1/inventory", &recordedRequest{}, []map[string]any{
		{"label-text": "TAPE02", "uuid": "uuid-2", "marker": "second"},
		{"label-text": "TAPE01", "uuid": "uuid-1", "marker": "first"},
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newTapeDriveInventoryCmd(), "inventory", "drive1"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "TAPE01", got[0]["label-text"], "raw entries must follow the table's sorted order")
	require.Equal(t, "first", got[0]["marker"])
	require.Equal(t, "TAPE02", got[1]["label-text"])
	require.Equal(t, "second", got[1]["marker"])
}

// TestMetricsInfluxdbHTTPLs_StripsSecretsFromPairedRaw asserts that the site
// which post-processes raw entries still both sorts them with their rows and
// removes the secret fields — the pairing must not reintroduce the token.
func TestMetricsInfluxdbHTTPLs_StripsSecretsFromPairedRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET /api2/json/config/metrics/influxdb-http", &recordedRequest{}, []map[string]any{
		{"name": "zzz", "url": "https://z.example.com:8086", "token": "z-secret", "marker": "second"},
		{"name": "aaa", "url": "https://a.example.com:8086", "token": "a-secret", "marker": "first"},
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newMetricsInfluxdbHTTPLsCmd(), "ls"))

	require.NotContains(t, buf.String(), "secret", "the token must never reach the raw output")

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "aaa", got[0]["name"], "raw entries must follow the table's sorted order")
	require.Equal(t, "first", got[0]["marker"])
	require.Equal(t, "zzz", got[1]["name"])
	require.Equal(t, "second", got[1]["marker"])
}
