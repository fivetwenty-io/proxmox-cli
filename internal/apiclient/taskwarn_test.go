package apiclient

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
	"github.com/stretchr/testify/require"
)

// captureStderr swaps os.Stderr for a pipe, runs fn, and returns what fn
// wrote. The wait helpers write their warning straight to os.Stderr rather
// than to a threaded writer (matching internal/config's inline-secret
// warning), so this is the only way to observe it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	require.NoError(t, w.Close())
	out := <-done
	require.NoError(t, r.Close())
	return out
}

// TestWarnIfTaskWarned_ReportsWarningExit covers the outcome that used to
// vanish entirely: the SDK returns a "WARNINGS: N" task as a success with
// Warned set, and all three wait helpers discarded it, so a vzdump that
// skipped a guest printed "Backup completed" and exited 0 with nothing
// anywhere saying otherwise.
func TestWarnIfTaskWarned_ReportsWarningExit(t *testing.T) {
	const upid = "UPID:pve1:000A1B2C:00000000:00000000:vzdump::root@pam:"

	out := captureStderr(t, func() {
		warnIfTaskWarned(&tasks.Status{
			UpID:       upid,
			Status:     "stopped",
			ExitStatus: "WARNINGS: 1",
			Warned:     true,
		})
	})

	require.Contains(t, out, upid, "the operator must be able to look the task up")
	require.Contains(t, out, "WARNINGS: 1", "the exit status itself must be reported verbatim")
}

// TestWarnIfTaskWarned_SilentOnCleanOrAbsentStatus pins that a clean task
// stays quiet — a warning on every successful task would be noise operators
// learn to ignore, which is how the real one would get missed.
func TestWarnIfTaskWarned_SilentOnCleanOrAbsentStatus(t *testing.T) {
	cases := map[string]*tasks.Status{
		"clean exit": {UpID: "UPID:pve1:x", Status: "stopped", ExitStatus: "OK"},
		"nil status": nil,
	}

	for name, status := range cases {
		t.Run(name, func(t *testing.T) {
			out := captureStderr(t, func() { warnIfTaskWarned(status) })
			require.Empty(t, out)
		})
	}
}
