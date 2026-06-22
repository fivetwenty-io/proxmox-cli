package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestBackupCreate_AuditFields verifies the operational backup flags added by the
// flag audit are forwarded on POST /cluster/backup.
func TestBackupCreate_AuditFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/backup", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "backup", "create",
		"--id", "job1", "--all",
		"--bwlimit", "51200",
		"--exclude", "100,101",
		"--prune-backups", "keep-last=3",
		"--remove",
		"--protected",
		"--notification-mode", "notification-system",
		"--exclude-path", "/tmp",
		"--exclude-path", "/var/log",
		"--ionice", "8",
		"--performance", "max-workers=4"))

	require.Equal(t, "51200", gotForm.Get("bwlimit"))
	require.Equal(t, "100,101", gotForm.Get("exclude"))
	require.Equal(t, "keep-last=3", gotForm.Get("prune-backups"))
	require.Equal(t, "1", gotForm.Get("remove"))
	require.Equal(t, "1", gotForm.Get("protected"))
	require.Equal(t, "notification-system", gotForm.Get("notification-mode"))
	require.Equal(t, "8", gotForm.Get("ionice"))
	require.Equal(t, "max-workers=4", gotForm.Get("performance"))
	// exclude-path is an array; both values must be present.
	require.ElementsMatch(t, []string{"/tmp", "/var/log"}, gotForm["exclude-path"])
}

// TestBackupCreate_LargeBwlimitNoScientificNotation is a regression guard for
// the apiclient-go encoder bug (fixed in v3.2.8) where int64 params >= 1e6 were
// JSON round-tripped through float64 and emitted in scientific notation
// (bwlimit=1048576 -> "1.048576e+06"), which PVE rejects. A value below 1e6
// (as used elsewhere in these tests) does not exercise the path, so this asserts
// the exact decimal digits on the wire for a realistic 1 GiB/s limit.
func TestBackupCreate_LargeBwlimitNoScientificNotation(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/backup", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "backup", "create",
		"--id", "job1", "--all", "--bwlimit", "1048576"))

	require.Equal(t, "1048576", gotForm.Get("bwlimit"))
	require.NotContains(t, gotForm.Get("bwlimit"), "e",
		"bwlimit must be plain decimal, not scientific notation")
}

// TestBackupSet_AuditFields verifies the audit flags also forward on PUT.
func TestBackupSet_AuditFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/backup/job1", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "backup", "set", "job1",
		"--bwlimit", "10000", "--repeat-missed", "--zstd", "4"))

	require.Equal(t, "10000", gotForm.Get("bwlimit"))
	require.Equal(t, "1", gotForm.Get("repeat-missed"))
	require.Equal(t, "4", gotForm.Get("zstd"))
}

// TestOptionsSet_AuditFields verifies migration-unsecure, u2f, and webauthn are
// forwarded on PUT /cluster/options.
func TestOptionsSet_AuditFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/options", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "options", "set",
		"--migration-unsecure", "--u2f", "origin=https://pve", "--webauthn", "rp=pve"))

	require.Equal(t, "1", gotForm.Get("migration_unsecure"))
	require.Equal(t, "origin=https://pve", gotForm.Get("u2f"))
	require.Equal(t, "rp=pve", gotForm.Get("webauthn"))
}

// TestFirewallRulesCreate_IcmpTypeDigest verifies the icmp-type and digest flags
// are forwarded on POST /cluster/firewall/rules.
func TestFirewallRulesCreate_IcmpTypeDigest(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/rules", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "create",
		"--type", "in", "--action", "ACCEPT", "--proto", "icmp",
		"--icmp-type", "echo-request", "--digest", "abc123"))

	require.Equal(t, "echo-request", gotForm.Get("icmp-type"))
	require.Equal(t, "abc123", gotForm.Get("digest"))
}

// TestFirewallRulesUpdate_IcmpType verifies icmp-type forwards on PUT.
func TestFirewallRulesUpdate_IcmpType(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/firewall/rules/0", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "rules", "update", "0",
		"--icmp-type", "echo-reply"))

	require.Equal(t, "echo-reply", gotForm.Get("icmp-type"))
}

// TestFirewallGroupRuleAdd_IcmpType verifies icmp-type forwards on a security
// group rule add (POST /cluster/firewall/groups/{group}).
func TestFirewallGroupRuleAdd_IcmpType(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/firewall/groups/web", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "group", "rule-add", "web",
		"--type", "in", "--action", "ACCEPT", "--proto", "icmp", "--icmp-type", "echo-request"))

	require.Equal(t, "echo-request", gotForm.Get("icmp-type"))
}

// TestFirewallOptionsSet_Digest verifies digest forwards on PUT
// /cluster/firewall/options.
func TestFirewallOptionsSet_Digest(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/firewall/options", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "options", "set", "--digest", "deadbeef"))

	require.Equal(t, "deadbeef", gotForm.Get("digest"))
}

// TestFirewallIpsetUpdate_Success verifies the new ipset update command issues a
// PUT to /cluster/firewall/ipset/{name}/{cidr} with the changed fields.
func TestFirewallIpsetUpdate_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/firewall/ipset/admins/10.0.0.0/24",
		func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			_ = r.ParseForm()
			gotForm = r.Form
			testhelper.WriteData(w, nil)
		})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "update", "admins", "10.0.0.0/24",
		"--comment", "office", "--nomatch"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/cluster/firewall/ipset/admins/10.0.0.0/24", gotPath)
	require.Equal(t, "office", gotForm.Get("comment"))
	require.Equal(t, "1", gotForm.Get("nomatch"))
	require.Contains(t, buf.String(), "updated")
}

// TestFirewallIpsetUpdate_RequiresChange verifies the command errors when no
// updatable flag is passed.
func TestFirewallIpsetUpdate_RequiresChange(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "firewall", "ipset", "update", "admins", "10.0.0.0/24")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes to set")
}

// TestReplicationCreate_RemoveJob verifies remove-job forwards on POST
// /cluster/replication.
func TestReplicationCreate_RemoveJob(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/replication", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "create",
		"--id", "100-0", "--target-node", "pve2", "--remove-job", "full"))

	require.Equal(t, "full", gotForm.Get("remove_job"))
}

// TestReplicationSet_RemoveJob verifies remove-job forwards on PUT.
func TestReplicationSet_RemoveJob(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/replication/100-0", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "replication", "set", "100-0", "--remove-job", "local"))

	require.Equal(t, "local", gotForm.Get("remove_job"))
}

// TestConfigJoinAdd_Links verifies repeated --link flags expand to link0/link1
// on POST /cluster/config/join.
func TestConfigJoinAdd_Links(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/config/join", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "join", "add", "--yes",
		"--hostname", "10.0.0.1", "--fingerprint", "AA:BB", "--password", "secret",
		"--link", "0=10.0.0.2", "--link", "1=10.0.1.2"))

	require.Equal(t, "10.0.0.2", gotForm.Get("link0"))
	require.Equal(t, "10.0.1.2", gotForm.Get("link1"))
}

// TestConfigJoinAdd_BadLink verifies an invalid --link value is rejected.
func TestConfigJoinAdd_BadLink(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "join", "add", "--yes",
		"--hostname", "10.0.0.1", "--fingerprint", "AA:BB", "--password", "secret",
		"--link", "9=10.0.0.2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--link index")
}

// TestConfigNodesAdd_Links verifies --link expands on POST
// /cluster/config/nodes/{node}.
func TestConfigNodesAdd_Links(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/config/nodes/pve3", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, map[string]any{"corosync_authkey": "k", "corosync_conf": "c"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "nodes", "add", "pve3", "--yes",
		"--link", "0=10.0.0.3"))

	require.Equal(t, "10.0.0.3", gotForm.Get("link0"))
}

// TestConfigCreate_Success verifies the new config create command issues a POST
// to /cluster/config with the cluster name and links, gated behind --yes.
func TestConfigCreate_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/config", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "create", "--yes",
		"--clustername", "prod", "--link", "0=10.0.0.1"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/cluster/config", gotPath)
	require.Equal(t, "prod", gotForm.Get("clustername"))
	require.Equal(t, "10.0.0.1", gotForm.Get("link0"))
	require.Contains(t, buf.String(), "created")
}

// TestConfigCreate_RequiresYes verifies the guard refuses without --yes.
func TestConfigCreate_RequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "config", "create", "--clustername", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

// TestCephFlagsSetAll_Success verifies the bulk set-all command issues a single
// PUT /cluster/ceph/flags carrying only the flags passed.
func TestCephFlagsSetAll_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/ceph/flags", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "flags", "set-all",
		"--noout=true", "--norebalance=true"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/cluster/ceph/flags", gotPath)
	require.Equal(t, "1", gotForm.Get("noout"))
	require.Equal(t, "1", gotForm.Get("norebalance"))
	// Unset flags must not be sent.
	require.Empty(t, gotForm.Get("noscrub"))
}

// TestCephFlagsSetAll_RequiresFlag verifies the command errors with no flag set.
func TestCephFlagsSetAll_RequiresFlag(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "flags", "set-all")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no flags to set")
}

// TestCephStatus_Success verifies the new ceph status command queries
// GET /cluster/ceph/status.
func TestCephStatus_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/ceph/status", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"health": map[string]any{"status": "HEALTH_OK"}})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ceph", "status"))

	require.Equal(t, "/api2/json/cluster/ceph/status", gotPath)
	require.Contains(t, buf.String(), "HEALTH_OK")
}

// TestCluster_AuditCommandTree verifies the new commands added by the flag audit
// are registered in the command tree.
func TestCluster_AuditCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})

	sub := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	config := sub(root, "config")
	require.NotNil(t, config)
	require.NotNil(t, sub(config, "create"), "config must expose create")

	firewall := sub(root, "firewall")
	require.NotNil(t, firewall)
	ipset := sub(firewall, "ipset")
	require.NotNil(t, ipset)
	require.NotNil(t, sub(ipset, "update"), "firewall ipset must expose update")

	ceph := sub(root, "ceph")
	require.NotNil(t, ceph)
	require.NotNil(t, sub(ceph, "status"), "ceph must expose status")
	flags := sub(ceph, "flags")
	require.NotNil(t, flags)
	require.NotNil(t, sub(flags, "set-all"), "ceph flags must expose set-all")
}
