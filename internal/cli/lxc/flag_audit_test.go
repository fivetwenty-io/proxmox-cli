package lxc

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestCreate_AuditScalarFlags verifies the scalar field flags added by the flag
// audit reach the POST /lxc body with the correct API-side keys.
func TestCreate_AuditScalarFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzcreate:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101",
		"--ostemplate", "local:vztmpl/alpine.tar.zst",
		"--description", "web container",
		"--nameserver", "1.1.1.1",
		"--searchdomain", "example.com",
		"--onboot",
		"--startup", "order=2,up=30",
		"--cpulimit", "1.5",
		"--cpuunits", "2048",
		"--arch", "arm64",
		"--ostype", "alpine",
		"--features", "nesting=1",
		"--hookscript", "local:snippets/hook.pl",
		"--protection",
		"--bwlimit", "50",
		"--ha-managed",
		"--timezone", "Europe/Berlin",
		"--tty", "4",
		"--console",
		"--cmode", "shell",
		"--template",
		"--unique",
		"--force",
		"--ignore-unpack-errors",
		"--restore",
		"--env", "FOO=bar",
		"--entrypoint", "/sbin/init",
		"--lock", "backup",
		"--debug",
	)
	require.NoError(t, run())

	require.Equal(t, "local:vztmpl/alpine.tar.zst", body["ostemplate"])
	require.Equal(t, "web container", body["description"])
	require.Equal(t, "1.1.1.1", body["nameserver"])
	require.Equal(t, "example.com", body["searchdomain"])
	require.Equal(t, true, body["onboot"])
	require.Equal(t, "order=2,up=30", body["startup"])
	require.Equal(t, "1.5", body["cpulimit"])
	require.Equal(t, 2048, body["cpuunits"])
	require.Equal(t, "arm64", body["arch"])
	require.Equal(t, "alpine", body["ostype"])
	require.Equal(t, "nesting=1", body["features"])
	require.Equal(t, "local:snippets/hook.pl", body["hookscript"])
	require.Equal(t, true, body["protection"])
	require.Equal(t, 50, body["bwlimit"])
	require.Equal(t, true, body["ha-managed"])
	require.Equal(t, "Europe/Berlin", body["timezone"])
	require.Equal(t, 4, body["tty"])
	require.Equal(t, true, body["console"])
	require.Equal(t, "shell", body["cmode"])
	require.Equal(t, true, body["template"])
	require.Equal(t, true, body["unique"])
	require.Equal(t, true, body["force"])
	require.Equal(t, true, body["ignore-unpack-errors"])
	require.Equal(t, true, body["restore"])
	require.Equal(t, "FOO=bar", body["env"])
	require.Equal(t, "/sbin/init", body["entrypoint"])
	require.Equal(t, "backup", body["lock"])
	require.Equal(t, true, body["debug"])
}

// TestCreate_IndexedSlots verifies the repeatable --net/--mp/--dev flags expand
// into indexed net0/net1/mp0/dev0 keys.
// TestCreate_ServerError verifies a 500 from the API surfaces a wrapped error
// naming the container and node, matching the error-path coverage of peer
// create/lifecycle commands.
func TestCreate_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101",
		"--ostemplate", "local:vztmpl/alpine.tar.zst")

	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "create container 101 on node")
}

func TestCreate_IndexedSlots(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzcreate:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101",
		"--ostemplate", "local:vztmpl/alpine.tar.zst",
		"--net", "0=name=eth0,bridge=vmbr0,ip=dhcp",
		"--net", "1=name=eth1,bridge=vmbr1",
		"--mp", "0=local-lvm:8,mp=/data",
		"--dev", "0=/dev/ttyUSB0",
	)
	require.NoError(t, run())

	require.Equal(t, "name=eth0,bridge=vmbr0,ip=dhcp", body["net0"])
	require.Equal(t, "name=eth1,bridge=vmbr1", body["net1"])
	require.Equal(t, "local-lvm:8,mp=/data", body["mp0"])
	require.Equal(t, "/dev/ttyUSB0", body["dev0"])
}

// TestCreate_NetSlotConflict verifies --net0 and --net 0=... cannot both target
// network slot 0.
func TestCreate_NetSlotConflict(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzcreate:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101",
		"--ostemplate", "local:vztmpl/alpine.tar.zst",
		"--net0", "name=eth0,bridge=vmbr0",
		"--net", "0=name=eth0,bridge=vmbr1",
	)
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "slot 0")
	require.False(t, called, "no request must be made on a slot conflict")
}

// TestConfigSet_AuditFlags verifies the config-set field flags added by the
// audit reach the PUT body, including indexed slots.
func TestConfigSet_AuditFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101",
		"--nameserver", "9.9.9.9",
		"--searchdomain", "lan",
		"--onboot",
		"--startup", "order=1",
		"--tags", "prod;web",
		"--arch", "amd64",
		"--features", "keyctl=1",
		"--hookscript", "local:snippets/h.pl",
		"--protection",
		"--unprivileged",
		"--timezone", "host",
		"--tty", "2",
		"--console",
		"--cmode", "console",
		"--template",
		"--env", "A=b",
		"--entrypoint", "/init",
		"--lock", "migrate",
		"--digest", "abc123",
		"--debug",
		"--net", "0=name=eth0,bridge=vmbr0",
		"--mp", "1=local-lvm:4,mp=/srv",
		"--dev", "0=/dev/null",
	)
	require.NoError(t, run())

	require.Equal(t, "9.9.9.9", body["nameserver"])
	require.Equal(t, "lan", body["searchdomain"])
	require.Equal(t, true, body["onboot"])
	require.Equal(t, "order=1", body["startup"])
	require.Equal(t, "prod;web", body["tags"])
	require.Equal(t, "amd64", body["arch"])
	require.Equal(t, "keyctl=1", body["features"])
	require.Equal(t, "local:snippets/h.pl", body["hookscript"])
	require.Equal(t, true, body["protection"])
	require.Equal(t, true, body["unprivileged"])
	require.Equal(t, "host", body["timezone"])
	require.Equal(t, 2, body["tty"])
	require.Equal(t, true, body["console"])
	require.Equal(t, "console", body["cmode"])
	require.Equal(t, true, body["template"])
	require.Equal(t, "A=b", body["env"])
	require.Equal(t, "/init", body["entrypoint"])
	require.Equal(t, "migrate", body["lock"])
	require.Equal(t, "abc123", body["digest"])
	require.Equal(t, true, body["debug"])
	require.Equal(t, "name=eth0,bridge=vmbr0", body["net0"])
	require.Equal(t, "local-lvm:4,mp=/srv", body["mp1"])
	require.Equal(t, "/dev/null", body["dev0"])
}

// TestFirewallRulesCreate_IcmpType verifies --icmp-type reaches the rule body.
func TestFirewallRulesCreate_IcmpType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/firewall/rules", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "rules", "create", "101",
		"--type", "in", "--action", "ACCEPT", "--proto", "icmp", "--icmp-type", "echo-request")
	require.NoError(t, run())
	require.Equal(t, "echo-request", body["icmp-type"])
}

// TestFirewallLog_Queries verifies the new firewall log command queries the
// per-container firewall log endpoint.
func TestFirewallLog_Queries(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	var query url.Values
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/firewall/log", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		query = r.URL.Query()
		testhelper.WriteData(w, []map[string]any{{"n": 1, "t": "DROP IN"}})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "log", "101", "--limit", "10", "--start", "5")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/firewall/log", gotPath)
	require.Equal(t, "10", query.Get("limit"))
	require.Equal(t, "5", query.Get("start"))
	require.Contains(t, buf.String(), "DROP IN")
}

// TestFirewallRefs_Queries verifies the new firewall refs command queries the
// references endpoint and forwards the --type filter.
func TestFirewallRefs_Queries(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	var query url.Values
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/firewall/refs", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		query = r.URL.Query()
		testhelper.WriteData(w, []map[string]any{{"type": "ipset", "name": "mgmt", "ref": "+mgmt"}})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "refs", "101", "--type", "ipset")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/firewall/refs", gotPath)
	require.Equal(t, "ipset", query.Get("type"))
	require.Contains(t, buf.String(), "mgmt")
}

// TestFirewallIpsetUpdateMember verifies the new ipset update-member command
// issues a PUT to the member endpoint with the updated fields.
func TestFirewallIpsetUpdateMember(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/firewall/ipset/mgmt/10.0.0.0/8",
		func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			body = recordBody(t, r)
			testhelper.WriteData(w, nil)
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "firewall", "ipset", "update-member", "101", "mgmt", "10.0.0.0/8",
		"--comment", "office", "--nomatch")
	require.NoError(t, run())

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/firewall/ipset/mgmt/10.0.0.0/8", gotPath)
	require.Equal(t, "office", body["comment"])
	require.Equal(t, true, body["nomatch"])
}

// TestCreate_UnusedSlots verifies the repeatable --unused flag on lxc create
// expands into indexed unused0, unused1, ... keys (L1).
func TestCreate_UnusedSlots(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzcreate:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101",
		"--ostemplate", "local:vztmpl/alpine.tar.zst",
		"--unused", "0=local-lvm:vm-101-disk-1",
		"--unused", "1=local-lvm:vm-101-disk-2",
	)
	require.NoError(t, run())

	require.Equal(t, "local-lvm:vm-101-disk-1", body["unused0"])
	require.Equal(t, "local-lvm:vm-101-disk-2", body["unused1"])
}

// TestCreate_NoUnused_SlotAbsent verifies --unused is omitted from the body
// when not supplied.
func TestCreate_NoUnused_SlotAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzcreate:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "create", "101",
		"--ostemplate", "local:vztmpl/alpine.tar.zst",
	)
	require.NoError(t, run())

	_, has0 := body["unused0"]
	require.False(t, has0, "unused0 must not appear in body when --unused is not supplied")
}

// TestConfigSet_UnusedSlots verifies the repeatable --unused flag on lxc config
// set expands into indexed unused0, unused1, ... keys (L1).
func TestConfigSet_UnusedSlots(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101",
		"--unused", "0=local-lvm:vm-101-disk-3",
	)
	require.NoError(t, run())

	require.Equal(t, "local-lvm:vm-101-disk-3", body["unused0"])
}

// TestConfigSet_NoUnused_SlotAbsent verifies --unused is omitted from the body
// when not supplied on config set.
func TestConfigSet_NoUnused_SlotAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101",
		"--hostname", "web",
	)
	require.NoError(t, run())

	_, has0 := body["unused0"]
	require.False(t, has0, "unused0 must not appear in body when --unused is not supplied")
}

// TestDiskMove_DigestFlags verifies --digest and --target-digest reach the
// move_volume body.
func TestDiskMove_DigestFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/move_volume", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, "UPID:pve1:0:0:0:move:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "move", "101",
		"--volume", "rootfs", "--storage", "local-lvm",
		"--digest", "src123", "--target-digest", "dst456")
	require.NoError(t, run())

	require.Equal(t, "src123", body["digest"])
	require.Equal(t, "dst456", body["target-digest"])
}
