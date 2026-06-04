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

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "disk", "resize", "100", "--disk", "scsi0", "--size", "+10G"))

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

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "disk", "resize", "100", "--disk", "scsi0", "--size", "32G"))
	require.Contains(t, buf.String(), "resized")
}

func TestQemuDiskResize_RequiresDisk(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "disk", "resize", "100", "--size", "+10G")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--disk is required")
}

func TestQemuDiskResize_RequiresSize(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "disk", "resize", "100", "--disk", "scsi0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--size is required")
}

func TestQemuDiskResize_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/resize", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "disk", "resize", "100",
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

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "disk", "move", "100", "--disk", "scsi0", "--storage", "local-lvm"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/move_disk", gotPath)
	require.Contains(t, buf.String(), "moved")
}

func TestQemuDiskMove_RequiresDisk(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "disk", "move", "100", "--storage", "local-lvm")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--disk is required")
}

func TestQemuDiskMove_RequiresTarget(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "disk", "move", "100", "--disk", "scsi0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--storage or --target-vmid is required")
}

func TestQemuDiskMove_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/move_disk", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "disk", "move", "100",
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

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "disk", "unlink", "100", "--disk", "scsi1"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/unlink", gotPath)
	require.Contains(t, buf.String(), "unlinked")
}

func TestQemuDiskUnlink_RequiresDisk(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "disk", "unlink", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--disk is required")
}

func TestQemuDiskUnlink_FlagParams(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/unlink", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "disk", "unlink", "100", "--disk", "scsi0,scsi1", "--force"))

	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "scsi0,scsi1", form.Get("idlist"))
	require.Equal(t, "1", form.Get("force"))
}
