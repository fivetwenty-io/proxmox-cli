package lxc

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

const (
	// confPath and lockPath are what every caps mutation targets for CT 101.
	confPath = "/etc/pve/lxc/101.conf"

	// baseConf is a minimal guest config with an existing keep whitelist.
	keepConf = "arch: amd64\nhostname: web\nlxc.cap.keep: chown setuid\n"
	// dropConf holds an existing drop blocklist.
	dropConf = "arch: amd64\nlxc.cap.drop: net_admin net_raw\n"
	// plainConf has no capability lines (default mode).
	plainConf = "arch: amd64\nhostname: web\n"
)

// handleConfig registers a GET config handler returning the given fields, used
// for the pre-mutation lock check.
func handleConfig(f *testhelper.FakePVE, fields map[string]any) {
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, fields)
	})
}

func TestCapsShow_API(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{
		"lxc":    [][]string{{"lxc.cap.keep", "chown net_bind_service"}},
		"digest": "x",
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "show", "101")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "keep")
	require.Contains(t, out, "chown")
	require.Contains(t, out, "net_bind_service")
}

func TestCapsDescribe_Offline(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "describe")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "CAPABILITY")
	require.Contains(t, out, "chown")
	require.Contains(t, out, "sys_admin")
	require.Contains(t, out, "yes") // sys_admin is dangerous
	// Preset section is appended for the human formats.
	require.Contains(t, out, "Presets")
	require.Contains(t, out, "minimal")
}

func TestCapsSet_FullWriteFlow(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var configHit bool
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		configHit = true
		testhelper.WriteData(w, map[string]any{"digest": "x"})
	})

	// Read → Write → Exec(pct config) all succeed.
	fr := exec.Fake(
		exec.FakeResponse{Stdout: plainConf},
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--keep", "chown,setuid,kill")
	require.NoError(t, run())

	require.True(t, configHit, "GET config runs before editing for the lock check")
	require.Len(t, fr.Calls, 3, "expected read, write, and validate calls")

	// Read is a cat of the quoted conf path.
	require.Contains(t, strings.Join(fr.Calls[0].Args, " "), confPath)
	// Write pipes the new content on stdin with the canonical keep line.
	require.Contains(t, string(fr.Calls[1].StdinContents), "lxc.cap.keep: chown setuid kill")
	// Validation is `pct config 101`.
	require.Contains(t, strings.Join(fr.Calls[2].Args, " "), "pct config 101")

	out := buf.String()
	require.Contains(t, out, "capabilities set (keep: 3)")
	require.Contains(t, out, "next start")
}

func TestCapsSet_RollbackOnValidationFailure(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	// Read ok, Write ok, Exec(pct) fails, rollback Write ok.
	fr := exec.Fake(
		exec.FakeResponse{Stdout: keepConf},
		exec.FakeResponse{},
		exec.FakeResponse{Stderr: "unable to parse config", ExitCode: 2},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--drop", "net_admin")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "failed validation")
	require.ErrorContains(t, err, "rolled back")

	require.Len(t, fr.Calls, 4, "expected read, write, validate, rollback-write")
	// The rollback writes the original bytes back.
	require.Equal(t, keepConf, string(fr.Calls[3].StdinContents))
}

func TestCapsSet_TransportFailureDuringValidationDoesNotRollBack(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	// Read ok, Write ok, then ssh itself dies (255) running the validation:
	// the write landed, so it must stay in place.
	fr := exec.Fake(
		exec.FakeResponse{Stdout: keepConf},
		exec.FakeResponse{},
		exec.FakeResponse{Stderr: "ssh: connect to host pve1 port 22: Connection refused", ExitCode: 255},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--drop", "net_admin")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "write succeeded")
	require.ErrorContains(t, err, "could not reach node")
	require.NotContains(t, err.Error(), "rolled back")

	require.Len(t, fr.Calls, 3, "no rollback write on a transport failure")
}

func TestCapsSet_LockRefusal(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"lock": "backup", "digest": "x"})

	fr := exec.Fake()
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--keep", "chown")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "locked")
	require.Empty(t, fr.Calls, "no ssh must happen while the container is locked")
}

func TestCapsSet_RootUserGuard(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake()
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--keep", "chown", "-l", "admin")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "root ssh")
	require.Empty(t, fr.Calls, "no ssh must happen when the login user is not root")
}

func TestCapsSet_DangerousWithoutForce(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake()
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--keep", "sys_admin")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "dangerous")
	require.Empty(t, fr.Calls, "the dangerous gate rejects before any ssh")
}

func TestCapsSet_DangerousWithForceWarns(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(
		exec.FakeResponse{Stdout: plainConf},
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--keep", "chown,sys_admin", "--force")
	require.NoError(t, run())

	require.Contains(t, buf.String(), "WARNING")
	require.Contains(t, buf.String(), "sys_admin")
	require.Contains(t, string(fr.Calls[1].StdinContents), "sys_admin")
}

func TestCapsSet_Preset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(
		exec.FakeResponse{Stdout: plainConf},
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--preset", "minimal")
	require.NoError(t, run())

	// minimal = chown dac_override fowner setuid setgid kill
	body := string(fr.Calls[1].StdinContents)
	require.Contains(t, body, "lxc.cap.keep: chown dac_override fowner setuid setgid kill")
}

func TestCapsAdd_KeepModeAppends(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(
		exec.FakeResponse{Stdout: keepConf}, // has: chown setuid
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "add", "101", "net_admin")
	require.NoError(t, run())

	require.Contains(t, string(fr.Calls[1].StdinContents), "lxc.cap.keep: chown setuid net_admin")
	require.Contains(t, buf.String(), "granted net_admin")
}

func TestCapsAdd_DefaultModeErrors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(exec.FakeResponse{Stdout: plainConf}) // no cap lines
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "add", "101", "net_admin")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "no capability whitelist")
	require.Len(t, fr.Calls, 1, "only the read happens; no write on a default-mode add")
}

func TestCapsRemove_DefaultBootstrapsDrop(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(
		exec.FakeResponse{Stdout: plainConf},
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "remove", "101", "net_admin")
	require.NoError(t, run())

	require.Contains(t, string(fr.Calls[1].StdinContents), "lxc.cap.drop: net_admin")
	require.Contains(t, buf.String(), "revoked net_admin")
}

func TestCapsRemove_DropModeAppends(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(
		exec.FakeResponse{Stdout: dropConf}, // drop: net_admin net_raw
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "remove", "101", "sys_ptrace")
	require.NoError(t, run())

	require.Contains(t, string(fr.Calls[1].StdinContents), "lxc.cap.drop: net_admin net_raw sys_ptrace")
}

func TestCapsReset_ClearsEntries(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(
		exec.FakeResponse{Stdout: keepConf},
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "reset", "101")
	require.NoError(t, run())

	require.NotContains(t, string(fr.Calls[1].StdinContents), "lxc.cap")
	require.Contains(t, buf.String(), "restored PVE defaults")
}

func TestCapsReset_NoOpWhenNothingToRemove(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})

	fr := exec.Fake(exec.FakeResponse{Stdout: plainConf})
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "reset", "101")
	require.NoError(t, run())

	require.Len(t, fr.Calls, 1, "a no-op reset reads but does not write")
	require.Contains(t, buf.String(), "no change")
}

func TestCapsShowEffective_DecodesMasks(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{
		"lxc":    [][]string{{"lxc.cap.keep", "chown"}},
		"digest": "x",
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"vmid": 101, "status": "running"})
	})

	// pct exec returns a /proc/1/status excerpt; 0x...fb has bit 0 (chown) set.
	procStatus := "Name:\tsystemd\nCapBnd:\t00000000a80425fb\nCapEff:\t00000000a80425fb\n"
	fr := exec.Fake(exec.FakeResponse{Stdout: procStatus})
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "show", "101", "--effective")
	require.NoError(t, run())

	require.Len(t, fr.Calls, 1)
	require.Contains(t, strings.Join(fr.Calls[0].Args, " "), "pct exec 101 -- cat /proc/1/status")
	out := buf.String()
	require.Contains(t, out, "effective")
	require.Contains(t, out, "chown")
}

func TestCapsShowEffective_NotRunningErrors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"lxc": [][]string{{"lxc.cap.keep", "chown"}}, "digest": "x"})
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"vmid": 101, "status": "stopped"})
	})

	fr := exec.Fake()
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", false, fr)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "show", "101", "--effective")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "not running")
	require.Empty(t, fr.Calls)
}

func TestCapsSet_RestartRebootsRunning(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	handleConfig(f, map[string]any{"digest": "x"})
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"vmid": 101, "status": "running"})
	})
	var rebooted bool
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/status/reboot", func(w http.ResponseWriter, _ *http.Request) {
		rebooted = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzreboot:101:root@pam:")
	})

	fr := exec.Fake(
		exec.FakeResponse{Stdout: plainConf},
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	deps := newRunnerDeps(t, f, output.FormatTable, "pve1", true, fr) // async: don't poll task
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "caps", "set", "101", "--keep", "chown", "--restart")
	require.NoError(t, run())

	require.True(t, rebooted, "--restart must reboot a running container")
	require.Contains(t, buf.String(), "Reboot task started")
}
