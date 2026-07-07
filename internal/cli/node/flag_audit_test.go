package node_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// recordForm registers a handler that parses the request form and records the
// method, path, and url.Values-encoded body for assertions.
func recordForm(f *testhelper.FakePVE, pattern string, rec *recordedRequest, payload any) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.query = r.Form.Encode()
		testhelper.WriteData(w, payload)
	})
}

// ---------------------------------------------------------------------------
// A: vzdump audit flags
// ---------------------------------------------------------------------------

func TestNodeVzdump_AuditFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:vzdump:100:root@pam:"
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/vzdump", &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump",
		"--all",
		"--bwlimit", "51200",
		"--ionice", "5",
		"--fleecing", "enabled=1,storage=local-lvm",
		"--lockwait", "30",
		"--stopwait", "10",
		"--tmpdir", "/tmp/bk",
		"--dumpdir", "/mnt/bk",
		"--script", "/root/hook.sh",
		"--stdexcludes=false",
		"--stdout",
		"--exclude", "100,101",
		"--exclude-path", "/var/cache",
		"--zstd", "4",
		"--pigz", "2",
		"--notification-mode", "notification-system",
		"--pbs-change-detection-mode", "metadata",
		"--performance", "max-workers=4",
		"--job-id", "job-1",
		"--prune-backups", "keep-last=3",
		"--stop",
		"--quiet"))

	require.NoError(t, root.Execute())
	form, err := url.ParseQuery(rec.query)
	require.NoError(t, err)
	for k, want := range map[string]string{
		"bwlimit":                   "51200",
		"ionice":                    "5",
		"fleecing":                  "enabled=1,storage=local-lvm",
		"lockwait":                  "30",
		"stopwait":                  "10",
		"tmpdir":                    "/tmp/bk",
		"dumpdir":                   "/mnt/bk",
		"script":                    "/root/hook.sh",
		"stdexcludes":               "0",
		"stdout":                    "1",
		"exclude":                   "100,101",
		"exclude-path":              "/var/cache",
		"zstd":                      "4",
		"pigz":                      "2",
		"notification-mode":         "notification-system",
		"pbs-change-detection-mode": "metadata",
		"performance":               "max-workers=4",
		"job-id":                    "job-1",
		"prune-backups":             "keep-last=3",
		"stop":                      "1",
		"quiet":                     "1",
	} {
		require.Equal(t, want, form.Get(k), "form field %q", k)
	}
}

// ---------------------------------------------------------------------------
// B: firewall --digest
// ---------------------------------------------------------------------------

func TestNodeFirewallRulesCreate_Digest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/firewall/rules", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "create",
		"--type", "in", "--action", "ACCEPT", "--digest", "abc123"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.query, "digest=abc123")
}

func TestNodeFirewallRulesUpdate_Digest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "PUT /api2/json/nodes/pve1/firewall/rules/0", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "update", "0",
		"--digest", "abc123"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.query, "digest=abc123")
}

func TestNodeFirewallRulesDelete_Digest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "DELETE /api2/json/nodes/pve1/firewall/rules/0", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "rules", "delete", "0",
		"--yes", "--digest", "abc123"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, rec.query, "digest=abc123")
}

func TestNodeFirewallOptionsSet_Digest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "PUT /api2/json/nodes/pve1/firewall/options", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "options", "set",
		"--enable", "--digest", "abc123"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "digest=abc123")
}

// ---------------------------------------------------------------------------
// C: disks zfs --draid-config
// ---------------------------------------------------------------------------

func TestNodeDisksCreateZfs_DraidConfig(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/disks/zfs", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "zfs",
		"--devices", "/dev/sdb", "--name", "tank", "--raidlevel", "draid",
		"--draid-config", "data=4,spares=1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.query, "draid-config=data")
}

// ---------------------------------------------------------------------------
// D: node config get/set
// ---------------------------------------------------------------------------

func TestNodeConfigGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/config", &rec, map[string]any{
		"description": "hypervisor 1", "wakeonlan": "aa:bb:cc:dd:ee:ff",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "config", "get"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "hypervisor 1")
}

func TestNodeConfigSet_Flags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "PUT /api2/json/nodes/pve1/config", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "config", "set",
		"--description", "primary",
		"--acme", "domains=example.com",
		"--acme-domain", "0=node.example.com",
		"--wakeonlan", "aa:bb:cc:dd:ee:ff",
		"--ballooning-target", "80",
		"--startall-onboot-delay", "10"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	for _, want := range []string{
		"description=primary", "acme=domains", "acmedomain0=node.example.com",
		"wakeonlan=aa", "ballooning-target=80", "startall-onboot-delay=10",
	} {
		require.Contains(t, rec.query, want)
	}
}

func TestNodeConfigSet_RequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "config", "set"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes to set")
}

func TestNodeConfigSet_BadAcmeDomain(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "config", "set",
		"--acme-domain", "node.example.com"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "INDEX=VALUE")
}

// ---------------------------------------------------------------------------
// E: node reboot / shutdown
// ---------------------------------------------------------------------------

func TestNodeReboot_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/status", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "reboot", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.query, "command=reboot")
	require.Contains(t, buf.String(), "reboot")
}

func TestNodeReboot_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "reboot"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmation")
}

func TestNodeShutdown_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/status", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "shutdown", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "command=shutdown")
}

// ---------------------------------------------------------------------------
// F: node apt templates
// ---------------------------------------------------------------------------

func TestNodeAptTemplatesList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/aplinfo", &rec, []any{
		map[string]any{
			"template": "debian-12-standard_12.2-1_amd64.tar.zst",
			"section":  "system", "os": "debian-12", "version": "12.2-1",
			"headline": "Debian 12 Bookworm",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "templates", "list"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "debian-12-standard")
}

func TestNodeAptTemplatesDownload(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:aplinfo::root@pam:"
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/aplinfo", &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "templates", "download",
		"--storage", "local", "--template", "debian-12-standard"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.query, "storage=local")
	require.Contains(t, rec.query, "template=debian-12-standard")
}

// ---------------------------------------------------------------------------
// G: node firewall log
// ---------------------------------------------------------------------------

func TestNodeFirewallLog(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/firewall/log", &rec, []any{
		map[string]any{"n": 1, "t": "policy DROP: IN=vmbr0 SRC=10.0.0.5"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "firewall", "log", "--limit", "50", "--start", "0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, rec.query, "limit=50")
	require.Contains(t, buf.String(), "policy DROP")
}

// ---------------------------------------------------------------------------
// Command-tree audit: new commands are registered
// ---------------------------------------------------------------------------

func TestNode_AuditCommandTree(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, _ := newNodeRoot(t, f, output.FormatTable, exec.Fake())

	require.True(t, hasNodeCommandPath(root, "node", "config", "get"))
	require.True(t, hasNodeCommandPath(root, "node", "config", "set"))
	require.True(t, hasNodeCommandPath(root, "node", "reboot"))
	require.True(t, hasNodeCommandPath(root, "node", "shutdown"))
	require.True(t, hasNodeCommandPath(root, "node", "apt", "templates", "list"))
	require.True(t, hasNodeCommandPath(root, "node", "apt", "templates", "download"))
	require.True(t, hasNodeCommandPath(root, "node", "firewall", "log"))
}

// hasNodeCommandPath walks the command tree following the given path of Use
// names (first word only) and reports whether the full chain resolves.
func hasNodeCommandPath(root *cobra.Command, path ...string) bool {
	cur := root
	for _, name := range path {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == name {
				next = c
				break
			}
		}
		if next == nil {
			return false
		}
		cur = next
	}
	return true
}
