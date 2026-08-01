package lxc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

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

// TestSecurityList_UnreadableContainerDoesNotAbortTheAudit is the regression
// test for the divergence between the two guest twins: the VM audit warned and
// carried on, while this one returned the first config-read error, so a single
// unreadable container out of two hundred produced no report at all.
func TestSecurityList_UnreadableContainerDoesNotAbortTheAudit(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "lxc", "vmid": 101, "name": "web", "node": "pve1"},
			map[string]any{"type": "lxc", "vmid": 102, "name": "broken", "node": "pve1"},
			map[string]any{"type": "lxc", "vmid": 103, "name": "db", "node": "pve1"},
		})
	})
	for _, vmid := range []string{"101", "103"} {
		f.HandleFunc("GET /api2/json/nodes/pve1/lxc/"+vmid+"/config",
			func(w http.ResponseWriter, _ *http.Request) {
				testhelper.WriteData(w, map[string]any{"unprivileged": 1, "digest": "x"})
			})
	}
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/102/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf, errBuf bytes.Buffer
	cmd := newSecurityCmd()
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(&buf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"list"})
	require.NoError(t, cmd.Execute(), "one unreadable container must not fail the audit")

	out := buf.String()
	require.Contains(t, out, "101", "readable containers must still be reported")
	require.Contains(t, out, "103")
	require.Contains(t, out, "102", "the unreadable one must appear rather than vanish")
	require.Contains(t, out, "error:", "and must say it could not be read")
	require.Contains(t, errBuf.String(), "skipping container 102")
}

// TestSecurityList_RowOrderIsDeterministic guards the fan-out: rows are
// written by index rather than appended as reads finish, so a report can be
// diffed against yesterday's run.
func TestSecurityList_RowOrderIsDeterministic(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		entries := make([]any, 0, 12)
		for vmid := 101; vmid <= 112; vmid++ {
			entries = append(entries, map[string]any{
				"type": "lxc", "vmid": vmid, "name": fmt.Sprintf("ct%d", vmid), "node": "pve1",
			})
		}
		testhelper.WriteData(w, entries)
	})
	for vmid := 101; vmid <= 112; vmid++ {
		// Later VMIDs answer more slowly, so an append-as-completed
		// implementation would emit them in a different order every run.
		delay := time.Duration(112-vmid) * time.Millisecond
		f.HandleFunc(fmt.Sprintf("GET /api2/json/nodes/pve1/lxc/%d/config", vmid),
			func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(delay)
				testhelper.WriteData(w, map[string]any{"unprivileged": 1, "digest": "x"})
			})
	}

	deps := newDeps(t, f, output.FormatJSON, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "list")
	require.NoError(t, run())

	var got []struct {
		VMID string `json:"vmid"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 12)
	for i, r := range got {
		require.Equal(t, strconv.Itoa(101+i), r.VMID, "row %d is out of VMID order", i)
	}
}

// TestSecurityList_JSONCarriesTheRowFields covers a row struct whose fields
// were all unexported: -o json and -o yaml rendered a list of empty objects,
// so the machine-readable view of this audit carried no posture at all.
func TestSecurityList_JSONCarriesTheRowFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "lxc", "vmid": 102, "name": "legacy", "node": "pve1"},
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/102/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"unprivileged": 0, "protection": 1, "features": "nesting=1", "digest": "x",
		})
	})

	deps := newDeps(t, f, output.FormatJSON, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "list")
	require.NoError(t, run())

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "102", got[0]["vmid"])
	require.Equal(t, "legacy", got[0]["name"])
	require.Equal(t, false, got[0]["unprivileged"], "the privilege level is the point of the audit")
	require.Equal(t, true, got[0]["protection"])
	require.Contains(t, got[0]["features"], "nesting")
}

// TestSecurityList_ReadsConfigsConcurrently proves the fan-out is real. The
// audit is N+1 by nature — one cluster scan, then one config read per guest —
// and run back to back a 200-container cluster paid 200 sequential round trips.
// The handler records how many reads are in flight at once; serial reads would
// never exceed one.
func TestSecurityList_ReadsConfigsConcurrently(t *testing.T) {
	const containers = 24

	var inFlight, peak atomic.Int64

	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		entries := make([]any, 0, containers)
		for vmid := 101; vmid < 101+containers; vmid++ {
			entries = append(entries, map[string]any{
				"type": "lxc", "vmid": vmid, "name": fmt.Sprintf("ct%d", vmid), "node": "pve1",
			})
		}
		testhelper.WriteData(w, entries)
	})
	for vmid := 101; vmid < 101+containers; vmid++ {
		f.HandleFunc(fmt.Sprintf("GET /api2/json/nodes/pve1/lxc/%d/config", vmid),
			func(w http.ResponseWriter, _ *http.Request) {
				n := inFlight.Add(1)
				for {
					old := peak.Load()
					if n <= old || peak.CompareAndSwap(old, n) {
						break
					}
				}
				// Long enough that concurrent reads genuinely overlap.
				time.Sleep(20 * time.Millisecond)
				inFlight.Add(-1)
				testhelper.WriteData(w, map[string]any{"unprivileged": 1, "digest": "x"})
			})
	}

	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "list")
	require.NoError(t, run())

	require.Greater(t, peak.Load(), int64(1), "config reads must overlap, not run one at a time")
	require.LessOrEqual(t, peak.Load(), int64(cli.DefaultFanout),
		"and must stay within the fan-out cap, or the audit becomes a denial of service "+
			"against the operator's own cluster")
}
