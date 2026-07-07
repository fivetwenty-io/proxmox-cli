package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestQemuCreate_AuditScalarFlags asserts that the full scalar surface added to
// `qemu create` reaches the POST body with the correct PVE parameter keys.
func TestQemuCreate_AuditScalarFlags(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100",
		"--bios", "ovmf",
		"--efidisk0", "local-lvm:0,efitype=4m",
		"--tpmstate0", "local-lvm:0,version=v2.0",
		"--vga", "qxl",
		"--machine", "q35",
		"--cpu", "host",
		"--cpulimit", "1.5",
		"--cpuunits", "2048",
		"--balloon", "512",
		"--shares", "1000",
		"--numa",
		"--kvm",
		"--acpi",
		"--onboot",
		"--protection",
		"--ha-managed",
		"--storage", "local-lvm",
		"--archive", "local:backup/vzdump.vma.zst",
		"--description", "audit VM",
		"--smbios1", "uuid=11111111-1111-1111-1111-111111111111",
		"--hotplug", "network,disk,usb",
		"--startup", "order=2,up=30"))

	form := parseForm(t, body)
	require.Equal(t, "ovmf", form.Get("bios"))
	require.Equal(t, "local-lvm:0,efitype=4m", form.Get("efidisk0"))
	require.Equal(t, "local-lvm:0,version=v2.0", form.Get("tpmstate0"))
	require.Equal(t, "qxl", form.Get("vga"))
	require.Equal(t, "q35", form.Get("machine"))
	require.Equal(t, "host", form.Get("cpu"))
	require.Equal(t, "1.5", form.Get("cpulimit"))
	require.Equal(t, "2048", form.Get("cpuunits"))
	require.Equal(t, "512", form.Get("balloon"))
	require.Equal(t, "1000", form.Get("shares"))
	require.Equal(t, "1", form.Get("numa"))
	require.Equal(t, "1", form.Get("kvm"))
	require.Equal(t, "1", form.Get("acpi"))
	require.Equal(t, "1", form.Get("onboot"))
	require.Equal(t, "1", form.Get("protection"))
	require.Equal(t, "1", form.Get("ha-managed"))
	require.Equal(t, "local-lvm", form.Get("storage"))
	require.Equal(t, "local:backup/vzdump.vma.zst", form.Get("archive"))
	require.Equal(t, "audit VM", form.Get("description"))
	require.Equal(t, "uuid=11111111-1111-1111-1111-111111111111", form.Get("smbios1"))
	require.Equal(t, "network,disk,usb", form.Get("hotplug"))
	require.Equal(t, "order=2,up=30", form.Get("startup"))
}

// TestQemuCreate_IndexedSlots asserts that repeatable INDEX=VALUE slots expand
// to numbered keys for every device family, including ones with no legacy alias.
func TestQemuCreate_IndexedSlots(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100",
		"--scsi", "0=local-lvm:32",
		"--scsi", "5=local-lvm:8",
		"--net", "1=virtio,bridge=vmbr1",
		"--sata", "0=local-lvm:16",
		"--virtio", "0=local-lvm:64",
		"--hostpci", "0=0000:01:00,pcie=1",
		"--usb", "0=host=1234:5678",
		"--serial", "0=socket",
		"--ipconfig", "2=ip=dhcp"))

	form := parseForm(t, body)
	require.Equal(t, "local-lvm:32", form.Get("scsi0"))
	require.Equal(t, "local-lvm:8", form.Get("scsi5"))
	require.Equal(t, "virtio,bridge=vmbr1", form.Get("net1"))
	require.Equal(t, "local-lvm:16", form.Get("sata0"))
	require.Equal(t, "local-lvm:64", form.Get("virtio0"))
	require.Equal(t, "0000:01:00,pcie=1", form.Get("hostpci0"))
	require.Equal(t, "host=1234:5678", form.Get("usb0"))
	require.Equal(t, "socket", form.Get("serial0"))
	require.Equal(t, "ip=dhcp", form.Get("ipconfig2"))
}

// TestQemuCreate_LegacySlotAlias asserts the legacy --scsi0 alias maps to index 0.
func TestQemuCreate_LegacySlotAlias(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100",
		"--scsi0", "local-lvm:32",
		"--scsi", "1=local-lvm:8",
		"--net0", "virtio,bridge=vmbr0",
		"--ide2", "local:iso/img.iso,media=cdrom"))

	form := parseForm(t, body)
	require.Equal(t, "local-lvm:32", form.Get("scsi0"))
	require.Equal(t, "local-lvm:8", form.Get("scsi1"))
	require.Equal(t, "virtio,bridge=vmbr0", form.Get("net0"))
	require.Equal(t, "local:iso/img.iso,media=cdrom", form.Get("ide2"))
}

// TestQemuCreate_SlotConflict asserts a legacy alias and an explicit slot at the
// same index are rejected before any request is made.
func TestQemuCreate_SlotConflict(t *testing.T) {
	f, ac := newFakeClient(t)

	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	err := run(deps, &buf, "create", "100",
		"--scsi0", "local-lvm:32",
		"--scsi", "0=local-lvm:8")
	require.Error(t, err)
	require.ErrorContains(t, err, "slot 0")
	require.False(t, called, "no request should be sent when slots conflict")
}

// TestQemuConfigSet_AuditFlags asserts the new config-set scalars — including
// sockets and tags (previously create-only), the update-only digest/force/
// skiplock, and core hardware fields — reach the PUT body.
func TestQemuConfigSet_AuditFlags(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--sockets", "2",
		"--tags", "prod;web",
		"--digest", "abc123",
		"--force",
		"--skiplock",
		"--bios", "ovmf",
		"--kvm",
		"--vga", "virtio",
		"--cpuunits", "4096",
		"--cpulimit", "2",
		"--machine", "q35"))

	form := parseForm(t, body)
	require.Equal(t, "2", form.Get("sockets"))
	require.Equal(t, "prod;web", form.Get("tags"))
	require.Equal(t, "abc123", form.Get("digest"))
	require.Equal(t, "1", form.Get("force"))
	require.Equal(t, "1", form.Get("skiplock"))
	require.Equal(t, "ovmf", form.Get("bios"))
	require.Equal(t, "1", form.Get("kvm"))
	require.Equal(t, "virtio", form.Get("vga"))
	require.Equal(t, "4096", form.Get("cpuunits"))
	require.Equal(t, "2", form.Get("cpulimit"))
	require.Equal(t, "q35", form.Get("machine"))
}

// TestQemuConfigSet_NewSlotFamilies asserts the slot families added to config
// set (sata, hostpci, plus high-index repeatable scsi) expand correctly.
func TestQemuConfigSet_NewSlotFamilies(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100",
		"--sata", "0=local-lvm:16",
		"--hostpci", "0=0000:01:00,pcie=1",
		"--scsi", "7=local-lvm:8",
		"--usb", "3=host=1234:5678"))

	form := parseForm(t, body)
	require.Equal(t, "local-lvm:16", form.Get("sata0"))
	require.Equal(t, "0000:01:00,pcie=1", form.Get("hostpci0"))
	require.Equal(t, "local-lvm:8", form.Get("scsi7"))
	require.Equal(t, "host=1234:5678", form.Get("usb3"))
}

// TestQemuClone_FormatBwlimit asserts the new clone flags reach the POST body.
func TestQemuClone_FormatBwlimit(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/clone", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "clone", "100",
		"--newid", "200", "--full", "--format", "qcow2", "--bwlimit", "51200"))

	form := parseForm(t, body)
	require.Equal(t, "qcow2", form.Get("format"))
	require.Equal(t, "51200", form.Get("bwlimit"))
}

// TestQemuMigrate_NewFlags asserts the new migrate flags reach the POST body.
func TestQemuMigrate_NewFlags(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/migrate", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "migrate", "100",
		"--target-node", "pve2",
		"--online",
		"--migration-type", "insecure",
		"--with-conntrack-state",
		"--bwlimit", "102400"))

	form := parseForm(t, body)
	require.Equal(t, "insecure", form.Get("migration_type"))
	require.Equal(t, "1", form.Get("with-conntrack-state"))
	require.Equal(t, "102400", form.Get("bwlimit"))
}

// TestQemuStart_NewFlags asserts start's new flags reach the POST body.
func TestQemuStart_NewFlags(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "start", "100",
		"--force-cpu", "host", "--machine", "q35", "--skiplock"))

	form := parseForm(t, body)
	require.Equal(t, "host", form.Get("force-cpu"))
	require.Equal(t, "q35", form.Get("machine"))
	require.Equal(t, "1", form.Get("skiplock"))
}

// TestQemuStop_OverruleShutdown asserts stop's --overrule-shutdown reaches the body.
func TestQemuStop_OverruleShutdown(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "stop", "100", "--overrule-shutdown"))

	require.Equal(t, "1", parseForm(t, body).Get("overrule-shutdown"))
}

// TestQemuShutdown_Skiplock asserts shutdown's --skiplock reaches the body.
func TestQemuShutdown_Skiplock(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/shutdown", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "shutdown", "100", "--skiplock"))

	require.Equal(t, "1", parseForm(t, body).Get("skiplock"))
}

// TestQemuSuspend_Statestorage asserts suspend's --statestorage reaches the body.
func TestQemuSuspend_Statestorage(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/suspend", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "suspend", "100", "--todisk", "--statestorage", "local-lvm"))

	require.Equal(t, "local-lvm", parseForm(t, body).Get("statestorage"))
}

// TestQemuDiskMove_DigestFlags asserts disk move's digest flags reach the body.
func TestQemuDiskMove_DigestFlags(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/move_disk", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "move", "100",
		"--disk", "scsi0", "--storage", "local-lvm",
		"--digest", "src123", "--target-digest", "dst456"))

	form := parseForm(t, body)
	require.Equal(t, "src123", form.Get("digest"))
	require.Equal(t, "dst456", form.Get("target-digest"))
}

// TestQemuDiskResize_Digest asserts disk resize's --digest reaches the body.
func TestQemuDiskResize_Digest(t *testing.T) {
	f, ac := newFakeClient(t)

	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/resize", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "disk", "resize", "100",
		"--disk", "scsi0", "--size", "+10G", "--digest", "abc123"))

	require.Equal(t, "abc123", parseForm(t, body).Get("digest"))
}
