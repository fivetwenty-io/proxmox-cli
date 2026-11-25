package accesslog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

func TestParseTime(t *testing.T) {
	n, err := ParseTime("since", "1790000000")
	require.NoError(t, err)
	require.Equal(t, int64(1790000000), n)

	n, err = ParseTime("since", "2026-11-01T00:00:00Z")
	require.NoError(t, err)
	require.Equal(t, int64(1793491200), n)

	_, err = ParseTime("until", "tomorrow")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --until")
}

func TestCollect_OnlyGivenFlagsAreSet(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	flags := BindList(cmd, false)
	require.NoError(t, cmd.ParseFlags([]string{"--limit", "0", "--method", "GET"}))

	f, err := flags.Collect(cmd)
	require.NoError(t, err)
	require.NotNil(t, f.Limit, "an explicit zero must still be sent")
	require.Equal(t, int64(0), *f.Limit)
	require.Equal(t, "GET", *f.Method)
	require.Nil(t, f.Client)
	require.Nil(t, f.UserAgent)
	require.Nil(t, f.AuthIDExact)
}

func TestBind_UserAgentFlagOnlyWhenAsked(t *testing.T) {
	pve := &cobra.Command{Use: "x"}
	BindList(pve, false)
	require.Nil(t, pve.Flags().Lookup("user-agent"))

	pbs := &cobra.Command{Use: "x"}
	BindStats(pbs, true)
	require.NotNil(t, pbs.Flags().Lookup("user-agent"))
	require.Nil(t, pbs.Flags().Lookup("limit"), "stats has no --limit")
	require.NotNil(t, pbs.Flags().Lookup("top"))
}

func TestListResult_EmptyPageKeepsHeaders(t *testing.T) {
	res, err := ListResult(Page{})
	require.NoError(t, err)
	require.Contains(t, res.Headers, "TIME")
	require.NotContains(t, res.Headers, "USER-AGENT")
	require.Empty(t, res.Rows)
}

func TestListResult_RejectsMalformedEntry(t *testing.T) {
	_, err := ListResult(Page{Entries: []json.RawMessage{json.RawMessage(`"not an object"`)}})
	require.Error(t, err)
}

func TestWriteNextCursor(t *testing.T) {
	next := "c1"
	var buf bytes.Buffer
	WriteNextCursor(&buf, output.FormatTable, &next)
	require.Contains(t, buf.String(), "--cursor c1")

	buf.Reset()
	WriteNextCursor(&buf, output.FormatJSON, &next)
	WriteNextCursor(&buf, output.FormatTable, nil)
	empty := ""
	WriteNextCursor(&buf, output.FormatTable, &empty)
	require.Empty(t, buf.String())
}

func TestStatsResult_LimitsEachRankingToFiveValues(t *testing.T) {
	rows := `{"rows":[{"key":"a","requests":6},{"key":"b","requests":5},{"key":"c","requests":4},` +
		`{"key":"d","requests":3},{"key":"e","requests":2},{"key":"f","requests":1}]}`
	res, err := StatsResult(StatsInput{
		Since: 0, Until: 60, Complete: true,
		Lists: []NamedList{{Name: "clients", Data: json.RawMessage(rows)}, {Name: "empty", Data: json.RawMessage("null")}},
	})
	require.NoError(t, err)
	require.Equal(t, "a (6), b (5), c (4), d (3), e (2)", res.Single["clients"])
	require.NotContains(t, res.Single, "empty")
	require.Equal(t, "true", res.Single["complete"])
}
