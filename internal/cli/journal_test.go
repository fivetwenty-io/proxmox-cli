package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func journalCmd(jf *cli.JournalFilterFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "journal", RunE: func(cmd *cobra.Command, _ []string) error {
		return jf.Validate(cmd.Flags())
	}}
	jf.Register(cmd)
	return cmd
}

func TestJournalFilterFlags_RegistersEveryFlag(t *testing.T) {
	var jf cli.JournalFilterFlags
	cmd := journalCmd(&jf)
	for _, name := range []string{"priority", "service", "unit", "kernel", "structured", "identifiers", "units"} {
		require.NotNil(t, cmd.Flags().Lookup(name), "flag --%s must be registered", name)
	}
	require.Contains(t, cmd.Flags().Lookup("service").Usage, "syslog identifier")
	require.Contains(t, cmd.Flags().Lookup("unit").Usage, "systemd unit")
}

func TestJournalFilterFlags_Validate(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"ok single level", []string{"--priority", "3"}, ""},
		{"ok range", []string{"--priority", "0..3"}, ""},
		{"ok empty means no filter", []string{"--priority", ""}, ""},
		{"bad level", []string{"--priority", "9"}, "invalid --priority"},
		{"bad range", []string{"--priority", "a..b"}, "invalid --priority"},
		{"identifiers needs structured", []string{"--identifiers"}, "--identifiers requires --structured"},
		{"units needs structured", []string{"--units"}, "--units requires --structured"},
		{"identifiers with structured", []string{"--identifiers", "--structured"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var jf cli.JournalFilterFlags
			cmd := journalCmd(&jf)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestJournalPath_EscapesTheNodeSegment(t *testing.T) {
	require.Equal(t, "/nodes/pve1/journal", cli.JournalPath("pve1"))
	require.Equal(t, "/nodes/pdm%23x/journal", cli.JournalPath("pdm#x"))
	require.Equal(t, "/nodes/a%2Fb/journal", cli.JournalPath("a/b"))
}

func TestRawGetJSON_ReturnsDataAndForwardsParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery url.Values
	f.HandleFunc("GET /api2/json/nodes/pve1/journal", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		testhelper.WriteData(w, []map[string]any{{"msg": "hi"}})
	})
	ac := newCLITestClient(t, f)

	raw, err := cli.RawGetJSON(context.Background(), ac.Raw, cli.JournalPath("pve1"),
		map[string]any{"structured": true, "lastentries": json.Number("5")})
	require.NoError(t, err)
	require.JSONEq(t, `[{"msg":"hi"}]`, string(raw))
	require.Equal(t, "5", gotQuery.Get("lastentries"))
	require.Equal(t, "1", gotQuery.Get("structured"))
}

func TestRawGetJSON_NullDataIsNull(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/journal", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	ac := newCLITestClient(t, f)

	raw, err := cli.RawGetJSON(context.Background(), ac.Raw, cli.JournalPath("pve1"), nil)
	require.NoError(t, err)
	require.Equal(t, "null", string(raw))
}

func TestRawGetJSON_SurfacesAPIErrorWithPath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/journal", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "journal broken")
	})
	ac := newCLITestClient(t, f)

	_, err := cli.RawGetJSON(context.Background(), ac.Raw, cli.JournalPath("pve1"), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get /nodes/pve1/journal")
}

func TestStructuredJournalResult_EntriesSkipMarkers(t *testing.T) {
	raw := json.RawMessage(`[
	  {"c": "s=85fd;i=1f2a53;b=3e0e;m=c199f6e3a;t=65a98e9fedff1;x=4e7b", "ty": "cursor"},
	  {"h": "sm-0", "ty": "host"},
	  {"id": "sshd", "msg": "Accepted publickey", "p": 6, "pid": 812, "t": 1725000000123456},
	  {"id": "kernel", "msg": "oops", "p": 3, "pid": 0, "t": 1725000001000000},
	  {"c": "s=85fd;i=1f2a54;b=3e0e;m=c199f7490;t=65a98e9fee648;x=3a3f", "ty": "cursor"}
	]`)
	res, err := cli.StructuredJournalResult(raw)
	require.NoError(t, err)

	require.Equal(t, cli.JournalHeaders, res.Headers)
	require.Equal(t, [][]string{
		{"2024-08-30T06:40:00Z", "6", "812", "sshd", "Accepted publickey"},
		{"2024-08-30T06:40:01Z", "3", "0", "kernel", "oops"},
	}, res.Rows)

	entries, ok := res.Raw.([]any)
	require.True(t, ok, "Raw must be the whole decoded array, cursors included")
	require.Len(t, entries, 5)
	first, ok := entries[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "cursor", first["ty"])
}

func TestStructuredJournalResult_IdentifiersListing(t *testing.T) {
	res, err := cli.StructuredJournalResult(json.RawMessage(`[{"ids": ["postfix/smtp", "systemd"]}]`))
	require.NoError(t, err)
	require.Equal(t, []string{"IDENTIFIER"}, res.Headers)
	require.Equal(t, [][]string{{"postfix/smtp"}, {"systemd"}}, res.Rows)
	entries, ok := res.Raw.([]any)
	require.True(t, ok)
	require.Len(t, entries, 1)
}

func TestStructuredJournalResult_UnitsListing(t *testing.T) {
	res, err := cli.StructuredJournalResult(json.RawMessage(`[{"names": ["session-456.scope", "ssh.service"]}]`))
	require.NoError(t, err)
	require.Equal(t, []string{"UNIT"}, res.Headers)
	require.Equal(t, [][]string{{"session-456.scope"}, {"ssh.service"}}, res.Rows)
}

func TestStructuredJournalResult_CombinedListingRendersBoth(t *testing.T) {
	res, err := cli.StructuredJournalResult(json.RawMessage(`[{"ids": ["sshd"], "names": ["ssh.service"]}]`))
	require.NoError(t, err)
	require.Equal(t, []string{"IDENTIFIER", "UNIT"}, res.Headers)
	require.Equal(t, [][]string{{"sshd", ""}, {"", "ssh.service"}}, res.Rows)
}

func TestStructuredJournalResult_TimestampMagnitudes(t *testing.T) {
	cases := []struct {
		name string
		t    string
		want string
	}{
		{"seconds", "1725000000", "2024-08-30T06:40:00Z"},
		{"milliseconds", "1725000000123", "2024-08-30T06:40:00Z"},
		{"microseconds", "1725000000123456", "2024-08-30T06:40:00Z"},
		{"nanoseconds", "1725000000123456789", "2024-08-30T06:40:00Z"},
		{"zero is absent", "0", ""},
		{"non-numeric string is printed as is", `"soon"`, "soon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`[{"id": "x", "msg": "m", "p": 6, "pid": 1, "t": ` + tc.t + `}]`)
			res, err := cli.StructuredJournalResult(raw)
			require.NoError(t, err)
			require.Len(t, res.Rows, 1)
			require.Equal(t, tc.want, res.Rows[0][0])
		})
	}
}

func TestStructuredJournalResult_NullAndEmpty(t *testing.T) {
	for _, in := range []string{"null", "[]", ""} {
		res, err := cli.StructuredJournalResult(json.RawMessage(in))
		require.NoError(t, err, in)
		require.Equal(t, cli.JournalHeaders, res.Headers)
		require.Empty(t, res.Rows)
	}
}

func TestStructuredJournalResult_RejectsMalformed(t *testing.T) {
	for _, in := range []string{`[42]`, `[null]`, `["{}"]`, `[[]]`, `{"id": "x"}`} {
		_, err := cli.StructuredJournalResult(json.RawMessage(in))
		require.Error(t, err, in)
		if strings.HasPrefix(in, "[") {
			require.Contains(t, err.Error(), "entry 0", in)
		}
	}
}

// newCLITestClient builds an APIClient against the fake. testhelper.NewFakePVE
// already splits the listener address into Options.Host and Options.Port, so
// the options are used as they are. Task 5's tests share this constructor.
func newCLITestClient(t *testing.T, f *testhelper.FakePVE) *apiclient.APIClient {
	t.Helper()
	ac, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	return ac
}
