package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// rrddataPath is the node-scoped rrddata endpoint for testStorage.
const rrddataPath = "/api2/json/nodes/pve1/storage/local/rrddata"

// rrdPath is the node-scoped rrd endpoint for testStorage.
const rrdPath = "/api2/json/nodes/pve1/storage/local/rrd"

// TestStorageRrddata_ForwardsTimeframe verifies `pve storage rrddata` sends the
// timeframe query parameter to the API and renders the returned data points.
func TestStorageRrddata_ForwardsTimeframe(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET "+rrddataPath, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{
			{"time": 1700000000, "used": 2147483648, "total": 10737418240},
			{"time": 1700003600, "used": 2200000000, "total": 10737418240},
		})
	})

	out, err := run(t, f, "--node", "pve1", "rrddata", testStorage, "--timeframe", "day")
	require.NoError(t, err)
	require.Contains(t, gotQuery, "timeframe=day")
	require.Contains(t, out, "1700000000")
}

// TestStorageRrddata_DefaultTimeframe verifies the default timeframe value is
// forwarded even when the flag is not explicitly provided.
func TestStorageRrddata_DefaultTimeframe(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET "+rrddataPath, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := run(t, f, "--node", "pve1", "rrddata", testStorage)
	require.NoError(t, err)
	require.Contains(t, gotQuery, "timeframe=hour")
}

// TestStorageRrddata_ForwardsCf verifies --cf is only forwarded when explicitly set.
func TestStorageRrddata_ForwardsCf(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET "+rrddataPath, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := run(t, f, "--node", "pve1", "rrddata", testStorage, "--cf", "MAX")
	require.NoError(t, err)
	require.Contains(t, gotQuery, "cf=MAX")
}

// TestStorageRrddata_OmitsCfWhenUnset verifies --cf is omitted when not set.
func TestStorageRrddata_OmitsCfWhenUnset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET "+rrddataPath, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := run(t, f, "--node", "pve1", "rrddata", testStorage)
	require.NoError(t, err)
	require.NotContains(t, gotQuery, "cf=")
}

// TestStorageMetrics_RequiresNode verifies that rrddata and rrd both fail without
// a node.
func TestStorageMetrics_RequiresNode(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "rrddata", args: []string{"rrddata", testStorage}},
		{name: "rrd", args: []string{"rrd", testStorage, "--ds", "used"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			_, err := run(t, f, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), "no node specified")
		})
	}
}

// TestStorageRrddata_ServerError verifies API errors are surfaced.
func TestStorageRrddata_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET "+rrddataPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "rrddata unavailable")
	})

	_, err := run(t, f, "--node", "pve1", "rrddata", testStorage)
	require.Error(t, err)
}

// TestStorageRrd_ForwardsParams verifies `pve storage rrd` forwards timeframe
// and ds parameters and renders the returned filename.
func TestStorageRrd_ForwardsParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET "+rrdPath, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"filename": "https://pve1:8006/rrd/local-hour.png",
		})
	})

	out, err := run(t, f, "--node", "pve1", "rrd", testStorage, "--ds", "used", "--timeframe", "week")
	require.NoError(t, err)
	require.Contains(t, gotQuery, "timeframe=week")
	require.Contains(t, gotQuery, "ds=used")
	require.Contains(t, out, "local-hour.png")
}

// TestStorageRrd_RequiredFlags verifies that rrd fails when a required flag is
// omitted.
func TestStorageRrd_RequiredFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing ds",
			args:    []string{"--node", "pve1", "rrd", testStorage},
			wantErr: "ds",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			_, err := run(t, f, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
