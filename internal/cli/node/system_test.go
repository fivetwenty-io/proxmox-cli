package node_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---- dns -------------------------------------------------------------------

func TestNodeDns_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/dns", &rec, map[string]any{
		"search": "lab.example", "dns1": "10.0.0.1",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "dns", "get"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/dns", rec.path)
	out := buf.String()
	require.Contains(t, out, "lab.example")
	require.Contains(t, out, "10.0.0.1")
}

func TestNodeDns_Set(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/nodes/pve1/dns", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "dns", "set",
		"--search", "lab.example", "--dns1", "10.0.0.1"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.body, "search=lab.example")
	require.Contains(t, rec.body, "dns1=10.0.0.1")
	// Unset optional name servers must be omitted from the request body.
	require.NotContains(t, rec.body, "dns2")
	require.NotContains(t, rec.body, "dns3")
}

func TestNodeDns_SetRequiresSearch(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "dns", "set", "--dns1", "10.0.0.1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "search")
}

// ---- hosts -----------------------------------------------------------------

func TestNodeHosts_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/hosts", &rec, map[string]any{
		"data": "127.0.0.1 localhost\n10.0.0.5 pve1", "digest": "abc123",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hosts", "get"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "127.0.0.1 localhost")
}

func TestNodeHosts_SetRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/hosts", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hosts", "set", "--data", "127.0.0.1 localhost"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	// The host file must not be replaced when confirmation is missing.
	require.Equal(t, "", rec.method)
}

func TestNodeHosts_SetWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/hosts", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hosts", "set",
		"--data", "127.0.0.1 localhost", "--digest", "abc123", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "data=")
	require.Contains(t, rec.body, "digest=abc123")
}

// ---- time ------------------------------------------------------------------

func TestNodeTime_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/time", &rec, map[string]any{
		"localtime": 1700000000, "time": 1700000000, "timezone": "UTC",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "time", "get"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "UTC")
}

func TestNodeTime_Set(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/nodes/pve1/time", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "time", "set", "--timezone", "Europe/Vienna"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.body, "timezone=Europe%2FVienna")
}

func TestNodeTime_SetRequiresTimezone(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "time", "set"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timezone")
}

// ---- syslog / journal / report --------------------------------------------

func TestNodeSyslog(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/syslog", &rec, []any{
		map[string]any{"n": 1, "t": "first log line"},
		map[string]any{"n": 2, "t": "second log line"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "syslog", "--service", "pveproxy", "--limit", "10"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, rec.query, "service=pveproxy")
	require.Contains(t, rec.query, "limit=10")
	out := buf.String()
	require.Contains(t, out, "first log line")
	require.Contains(t, out, "second log line")
}

func TestNodeJournal(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/journal", &rec, []any{
		"journal line one", "journal line two",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "journal", "--lastentries", "50"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, rec.query, "lastentries=50")
	out := buf.String()
	require.Contains(t, out, "journal line one")
	require.Contains(t, out, "journal line two")
}

func TestNodeReport(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/report", &rec, "== system report ==\nall good")

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "report"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "== system report ==")
}

// ---- subscription ----------------------------------------------------------

func TestNodeSubscription_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/subscription", &rec, map[string]any{
		"status": "active", "level": "c", "productname": "Proxmox VE Community",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "subscription", "get"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "active")
}

func TestNodeSubscription_SetRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/nodes/pve1/subscription", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "subscription", "set",
		"--key", "pve1c-0123456789"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Equal(t, "", rec.method)
}

func TestNodeSubscription_SetWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/nodes/pve1/subscription", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	const key = "pve1c-0123456789"
	root.SetArgs(append(prefix, "--node", "pve1", "node", "subscription", "set", "--key", key, "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.body, "key="+key)
	// The subscription key is a secret: it must never be echoed back to output.
	require.NotContains(t, buf.String(), key)
}

func TestNodeSubscription_UpdateWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/subscription", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "subscription", "update", "--force", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "force=1")
}

func TestNodeSubscription_DeleteRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/subscription", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "subscription", "delete"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Equal(t, "", rec.method)
}

func TestNodeSubscription_DeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/subscription", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "subscription", "delete", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
}

// ---- node guard + command tree ---------------------------------------------

func TestNodeSystem_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "dns", "get"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeSystem_CommandTree(t *testing.T) {
	root := cli.NewRootCmd()
	cli.AddGroups(root, &cli.Deps{})

	find := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	nodeCmd := find(root, "node")
	require.NotNil(t, nodeCmd)

	for _, name := range []string{"dns", "hosts", "time", "syslog", "journal", "report", "subscription"} {
		require.NotNil(t, find(nodeCmd, name), "node must expose %q", name)
	}

	dns := find(nodeCmd, "dns")
	require.NotNil(t, find(dns, "get"))
	require.NotNil(t, find(dns, "set"))

	sub := find(nodeCmd, "subscription")
	for _, verb := range []string{"get", "set", "update", "delete"} {
		require.NotNil(t, find(sub, verb), "subscription must expose %q", verb)
	}
}
