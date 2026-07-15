package lxc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newRunnerDeps builds test deps wired to the fake API server and a fake ssh
// runner, for the security commands that shell out.
func newRunnerDeps(
	t *testing.T, f *testhelper.FakePVE, format output.Format, node string, async bool, fr *exec.FakeRunner,
) *cli.Deps {
	t.Helper()
	deps := newDeps(t, f, format, node, async)
	deps.Runner = fr
	return deps
}

func TestSecurityShow_PrivilegedWarning(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"unprivileged": 0,
			"protection":   0,
			"features":     "nesting=1",
			"lxc":          [][]string{{"lxc.cap.drop", "sys_admin"}, {"lxc.apparmor.profile", "generated"}},
			"digest":       "x",
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "show", "101")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "WARNING")
	require.Contains(t, out, "privileged container")
	require.Contains(t, out, "unprivileged")
	require.Contains(t, out, "false")
	require.Contains(t, out, "nesting")
	// The cap.drop line becomes the caps block; the apparmor line stays raw.
	require.Contains(t, out, "drop")
	require.Contains(t, out, "sys_admin")
	require.Contains(t, out, "lxc.apparmor.profile")
}

func TestSecurityShow_JSON_NoProse(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"unprivileged": 1,
			"features":     "keyctl=1",
			"lxc":          [][]string{{"lxc.cap.keep", "chown net_bind_service"}},
			"digest":       "x",
		})
	})

	deps := newDeps(t, f, output.FormatJSON, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "show", "101")
	require.NoError(t, run())

	require.NotContains(t, buf.String(), "WARNING", "structured output carries no prose")

	var parsed securityPosture
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed), "got: %s", buf.String())
	require.True(t, parsed.Unprivileged)
	require.Equal(t, "keep", parsed.Caps.Mode)
	require.Equal(t, []string{"chown", "net_bind_service"}, parsed.Caps.Keep)
	require.Equal(t, []string{}, parsed.Caps.Drop, "empty drop must serialise as [] not null")
	require.Equal(t, true, parsed.Features["keyctl"])
	require.Equal(t, false, parsed.Features["nesting"])
}

func TestSecurityList_TableSortsPrivilegedFirst(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "lxc", "vmid": 101, "name": "web", "node": "pve1"},
			map[string]any{"type": "lxc", "vmid": 102, "name": "legacy", "node": "pve1"},
			map[string]any{"type": "qemu", "vmid": 200, "name": "vm", "node": "pve1"},
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"unprivileged": 1, "features": "nesting=1",
			"lxc": [][]string{{"lxc.cap.keep", "chown setuid"}}, "digest": "x",
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/102/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"unprivileged": 0, "protection": 1, "digest": "x",
		})
	})

	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "list")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "UNPRIVILEGED")
	require.Contains(t, out, "keep(2)")
	require.Contains(t, out, "nesting")
	// Privileged 102 is flagged and sorted ahead of unprivileged 101.
	require.Contains(t, out, "! 102")
	require.Less(t, indexOf(out, "102"), indexOf(out, "101"), "privileged CT must sort first")
	// The qemu guest is excluded.
	require.NotContains(t, out, "200")
}

// indexOf is a tiny helper for ordering assertions.
func indexOf(s, sub string) int {
	return bytes.Index([]byte(s), []byte(sub))
}

func TestSecurityGroup_Registered(t *testing.T) {
	cmd := Group(&cli.Deps{})
	var sec *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "security" {
			sec = c
		}
	}
	require.NotNil(t, sec, "security group must be registered under lxc")

	names := map[string]bool{}
	for _, c := range sec.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"show", "list", "caps", "features"} {
		require.True(t, names[want], "missing security sub-command %q", want)
	}
}
