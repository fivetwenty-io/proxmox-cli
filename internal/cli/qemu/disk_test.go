package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- disk add ---------------------------------------------------------------

func TestQemuDiskAdd_HappyPath(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{"scsi0": "local-lvm:32", "digest": "abc123"})
	})
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	// mutationSuffix (security.go:138) unconditionally GETs status/current and
	// errors if it fails — every test that reaches the PUT must register this.
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{"status": "stopped"})
	})
	var buf bytes.Buffer
	err := run(depsFor(t, ac, "table", "pve1", false), &buf,
		"disk", "add", "100", "--disk", "scsi2", "--storage", "tank-lab-ceph", "--size-gb", "100",
		"--options", "discard=on,ssd=1,serial=osd0")
	require.NoError(t, err)
	form := parseForm(t, body)
	require.Equal(t, "tank-lab-ceph:100,discard=on,ssd=1,serial=osd0", form.Get("scsi2"))
	require.Equal(t, "abc123", form.Get("digest"))
	require.Contains(t, buf.String(), "disk scsi2 added")
	require.Contains(t, buf.String(), "Change applies on next start.")
	// discard/ssd in --options → cold power-cycle caveat (spec §5: a reboot
	// is NOT enough for these flags; the VM must be stopped and started).
	require.Contains(t, buf.String(), "full power-off and power-on")
}

func TestQemuDiskAdd_NoDiscardOptions_NoPowerCycleCaveat(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{"scsi0": "local-lvm:32", "digest": "abc123"})
	})
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, nil)
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{"status": "stopped"})
	})
	var buf bytes.Buffer
	err := run(depsFor(t, ac, "table", "pve1", false), &buf,
		"disk", "add", "100", "--disk", "scsi2", "--storage", "tank-lab-ceph", "--size-gb", "100",
		"--options", "backup=0")
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "full power-off and power-on")
}

func TestQemuDiskAdd_OccupiedSlotRefused(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{"scsi2": "existing:10", "digest": "abc123"})
	})
	// No PUT handler registered: a PUT would 404-fail the test.
	var buf bytes.Buffer
	err := run(depsFor(t, ac, "table", "pve1", false), &buf,
		"disk", "add", "100", "--disk", "scsi2", "--storage", "local-lvm", "--size-gb", "10")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")
	require.Contains(t, err.Error(), "scsi2")
}

func TestQemuDiskAdd_RequiredFlagsAndSlotValidation(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantContain string
	}{
		{
			name:        "missing disk",
			args:        []string{"disk", "add", "100", "--storage", "local-lvm", "--size-gb", "10"},
			wantContain: "--disk is required",
		},
		{
			name:        "missing storage",
			args:        []string{"disk", "add", "100", "--disk", "scsi2", "--size-gb", "10"},
			wantContain: "--storage is required",
		},
		{
			name:        "missing size-gb",
			args:        []string{"disk", "add", "100", "--disk", "scsi2", "--storage", "local-lvm"},
			wantContain: "--size-gb is required",
		},
		{
			name: "size-gb zero",
			args: []string{"disk", "add", "100", "--disk", "scsi2", "--storage", "local-lvm",
				"--size-gb", "0"},
			wantContain: "positive",
		},
		{
			name: "invalid slot bus",
			args: []string{"disk", "add", "100", "--disk", "floppy0", "--storage", "local-lvm",
				"--size-gb", "10"},
			wantContain: "not a valid slot",
		},
		{
			name: "scsi index out of range",
			args: []string{"disk", "add", "100", "--disk", "scsi31", "--storage", "local-lvm",
				"--size-gb", "10"},
			wantContain: "out of range",
		},
		{
			name: "ide index out of range",
			args: []string{"disk", "add", "100", "--disk", "ide4", "--storage", "local-lvm",
				"--size-gb", "10"},
			wantContain: "out of range",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Numeric vmid + explicit node short-circuits resolveGuest, and all
			// these cases fail before any HTTP call, so no handler is registered.
			_, ac := newFakeClient(t)
			deps := depsFor(t, ac, output.FormatTable, "pve1", false)

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantContain)
		})
	}
}

func TestQemuDiskAdd_VirtioBusUsesVirtioField(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{"digest": "abc123"})
	})
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/status/current", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{"status": "stopped"})
	})
	var buf bytes.Buffer
	err := run(depsFor(t, ac, "table", "pve1", false), &buf,
		"disk", "add", "100", "--disk", "virtio1", "--storage", "local-lvm", "--size-gb", "50")
	require.NoError(t, err)
	form := parseForm(t, body)
	require.Equal(t, "local-lvm:50", form.Get("virtio1"))
}

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
