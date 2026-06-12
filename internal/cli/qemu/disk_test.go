package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- disk resize ------------------------------------------------------------

func TestQemuDiskResize_Sync(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/resize", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil) // synchronous storages return null, not a UPID
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "resize", "100", "--disk", "scsi0", "--size", "+10G"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/resize", gotPath)
	require.Contains(t, buf.String(), "resized to +10G")
}

func TestQemuDiskResize_WorkerUPID(t *testing.T) {
	f, ac := newFakeClient(t)

	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/resize", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, validUPID) // worker storages return a task UPID
	})
	handleTaskStatus(f, validUPID)

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "resize", "100", "--disk", "scsi0", "--size", "32G"))
	require.Contains(t, buf.String(), "resized")
}

// TestQemuDisk_RequiredFlags consolidates shape-1 (flag-required) cases across
// disk sub-commands. Each case omits one required flag or argument and expects
// the exact error substring listed; no HTTP handler is registered.
func TestQemuDisk_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantContain string
	}{
		{
			name:        "resize missing disk",
			args:        []string{"disk", "resize", "100", "--size", "+10G"},
			wantContain: "--disk is required",
		},
		{
			name:        "resize missing size",
			args:        []string{"disk", "resize", "100", "--disk", "scsi0"},
			wantContain: "--size is required",
		},
		{
			name:        "move missing disk",
			args:        []string{"disk", "move", "100", "--storage", "local-lvm"},
			wantContain: "--disk is required",
		},
		{
			name:        "move missing storage or target-vmid",
			args:        []string{"disk", "move", "100", "--disk", "scsi0"},
			wantContain: "--storage or --target-vmid is required",
		},
		{
			name:        "unlink missing disk",
			args:        []string{"disk", "unlink", "100"},
			wantContain: "--disk is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ac := newFakeClient(t)
			deps := depsFor(t, ac, output.FormatTable, "pve1", false)

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantContain)
		})
	}
}

func TestQemuDiskResize_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/resize", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "resize", "100",
		"--disk", "scsi0", "--size", "+10G", "--skiplock"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "scsi0", form.Get("disk"))
	require.Equal(t, "+10G", form.Get("size"))
	require.Equal(t, "1", form.Get("skiplock"))
}

// --- disk move --------------------------------------------------------------

func TestQemuDiskMove_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/move_disk", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, validUPID)
	})
	handleTaskStatus(f, validUPID)

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "move", "100", "--disk", "scsi0", "--storage", "local-lvm"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/move_disk", gotPath)
	require.Contains(t, buf.String(), "moved")
}

func TestQemuDiskMove_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/move_disk", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "move", "100",
		"--disk", "scsi0", "--storage", "local-lvm", "--target-disk", "scsi1", "--delete"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "scsi0", form.Get("disk"))
	require.Equal(t, "local-lvm", form.Get("storage"))
	require.Equal(t, "scsi1", form.Get("target-disk"))
	require.Equal(t, "1", form.Get("delete"))
}

// --- disk unlink ------------------------------------------------------------

func TestQemuDiskUnlink_Blocking(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/unlink", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "unlink", "100", "--disk", "scsi1"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/unlink", gotPath)
	require.Contains(t, buf.String(), "unlinked")
}

func TestQemuDiskUnlink_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/unlink", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "unlink", "100", "--disk", "scsi0,scsi1", "--force"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "scsi0,scsi1", form.Get("idlist"))
	require.Equal(t, "1", form.Get("force"))
}
