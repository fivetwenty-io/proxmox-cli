package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestDnsList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/dns", []any{
		map[string]any{"dns": "pdns1", "type": "powerdns", "url": "https://pdns.example/api"},
	}, 200)

	out, err := run(t, f, "", "dns", "list")
	require.NoError(t, err)
	require.Contains(t, out, "pdns1")
	require.Contains(t, out, "powerdns")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/dns", rec[0].path)
}

func TestDnsCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/dns", map[string]any{}, 200)

	out, err := run(t, f, "", "dns", "create", "pdns1",
		"--type", "powerdns", "--url", "https://pdns.example/api", "--key", "supersecretkey", "--ttl", "3600")
	require.NoError(t, err)
	require.Contains(t, out, "pdns1")
	require.Contains(t, out, "created")
	require.NotContains(t, out, "supersecretkey", "the API key must never be echoed")
	require.Len(t, rec, 1)
	require.Equal(t, "pdns1", rec[0].body["dns"])
	require.Equal(t, "powerdns", rec[0].body["type"])
	require.Equal(t, "https://pdns.example/api", rec[0].body["url"])
	require.Equal(t, "supersecretkey", rec[0].body["key"], "key is forwarded to the API")
	require.Equal(t, "3600", rec[0].body["ttl"])
}

// TestDnsCreateRequiresKeyURLType verifies the three required flags are enforced
// and no request is issued when any is missing.
func TestDnsCreateRequiresKeyURLType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/dns", map[string]any{}, 200)

	_, err := run(t, f, "", "dns", "create", "pdns1", "--type", "powerdns", "--url", "https://x")
	require.Error(t, err)
	require.ErrorContains(t, err, "key")
	require.Empty(t, rec)
}

func TestDnsCreateOmitsUnsetFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/dns", map[string]any{}, 200)

	_, err := run(t, f, "", "dns", "create", "pdns1",
		"--type", "powerdns", "--url", "https://pdns.example/api", "--key", "k")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.NotContains(t, rec[0].body, "ttl")
	require.NotContains(t, rec[0].body, "fingerprint")
	require.NotContains(t, rec[0].body, "reversemaskv6")
}

func TestDnsGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/dns/pdns1", map[string]any{
		"dns": "pdns1", "type": "powerdns", "url": "https://pdns.example/api", "ttl": 3600,
	}, 200)

	out, err := run(t, f, "", "dns", "get", "pdns1")
	require.NoError(t, err)
	require.Contains(t, out, "pdns1")
	require.Contains(t, out, "3600")
	require.Len(t, rec, 1)
	require.Equal(t, "/api2/json/cluster/sdn/dns/pdns1", rec[0].path)
}

func TestDnsSetRequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/dns/pdns1", map[string]any{}, 200)

	_, err := run(t, f, "", "dns", "set", "pdns1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes to set")
	require.Empty(t, rec)
}

func TestDnsSetForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/dns/pdns1", map[string]any{}, 200)

	out, err := run(t, f, "", "dns", "set", "pdns1", "--key", "rotatedkey", "--ttl", "7200")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	require.NotContains(t, out, "rotatedkey", "the API key must never be echoed")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "rotatedkey", rec[0].body["key"])
	require.Equal(t, "7200", rec[0].body["ttl"])
	require.NotContains(t, rec[0].body, "url", "unset attributes must not be sent")
}

func TestDnsDeleteWithoutConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/dns/pdns1", map[string]any{}, 200)

	_, err := run(t, f, "", "dns", "delete", "pdns1")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec)
}

func TestDnsDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/dns/pdns1", map[string]any{}, 200)

	out, err := run(t, f, "", "dns", "delete", "pdns1", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

func TestDnsCommandTree(t *testing.T) {
	cmd := newDnsCmd()
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "get", "set", "delete"} {
		require.True(t, got[want], "dns is missing %q", want)
	}
}
