package qemu

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newCreateCmd builds `pve qemu create <vmid>`. The VM is created as an
// asynchronous task; the command blocks until it completes unless --async is
// set. Disk, network, and CD-ROM devices are passed as repeatable indexed
// slots (for example --scsi 0=local-lvm:8 or --net 0=virtio,bridge=vmbr0).
func newCreateCmd() *cobra.Command {
	var (
		async bool

		// Identity and scheduling.
		name    string
		pool    string
		tags    string
		start   bool
		onboot  bool
		startup string
		lock    string

		// CPU and memory.
		cores          int64
		sockets        int64
		vcpus          int64
		cpu            string
		cpulimit       float64
		cpuunits       int64
		affinity       string
		memory         string
		balloon        int64
		shares         int64
		numa           bool
		hugepages      string
		keephugepages  bool
		allowKsm       bool
		smbios1        string
		machine        string
		arch           string
		ostype         string
		bios           string
		efidisk0       string
		tpmstate0      string
		vga            string
		scsihw         string
		boot           string
		acpi           bool
		kvm            bool
		freeze         bool
		localtime      bool
		tablet         bool
		tdf            bool
		reboot         bool
		protection     bool
		template       bool
		unique         bool
		force          bool
		hotplug        string
		hookscript     string
		watchdog       string
		rng0           string
		audio0         string
		keyboard       string
		spiceEnhance   string
		amdSev         string
		intelTdx       string
		ivshmem        string
		kvmArgs        string
		vmgenid        string
		startdate      string
		vmstatestorage string

		// Storage / import / restore.
		storage              string
		description          string
		archive              string
		liveRestore          bool
		importWorkingStorage string
		bwlimit              int64
		migrateDowntime      float64
		migrateSpeed         int64
		haManaged            bool

		// Cloud-init scalars.
		ciuser       string
		cipassword   string
		citype       string
		ciupgrade    bool
		cicustom     string
		nameserver   string
		searchdomain string
		sshkeys      string

		// Indexed device slots (repeatable INDEX=VALUE).
		netSlots      []string
		scsiSlots     []string
		ideSlots      []string
		virtioSlots   []string
		sataSlots     []string
		ipconfigSlots []string
		hostpciSlots  []string
		serialSlots   []string
		usbSlots      []string
		parallelSlots []string
		numaNodeSlots []string
		virtiofsSlots []string

		// Legacy single-slot scalars retained for backward compatibility.
		net0      string
		scsi0     string
		ide2      string
		ipconfig0 string

		agent string
	)
	cmd := &cobra.Command{
		Use:   "create <vmid>",
		Short: "Create a QEMU virtual machine",
		Long: "Create a QEMU virtual machine on a node. Devices are given as repeatable " +
			"indexed slots, e.g. --scsi 0=local-lvm:8 --net 0=virtio,bridge=vmbr0 and " +
			"--ide 2=local:iso/img.iso,media=cdrom.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid vmid %q: %w", args[0], err)
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateQemuParams{Vmid: vmid}
			fl := cmd.Flags()
			set := func(flag string, apply func()) {
				if fl.Changed(flag) {
					apply()
				}
			}

			set("name", func() { params.Name = strPtr(name) })
			set("pool", func() { params.Pool = strPtr(pool) })
			set("tags", func() { params.Tags = strPtr(tags) })
			set("start", func() { params.Start = boolPtr(start) })
			set("onboot", func() { params.Onboot = boolPtr(onboot) })
			set("startup", func() { params.Startup = strPtr(startup) })
			set("lock", func() { params.Lock = strPtr(lock) })

			set("cores", func() { params.Cores = int64Ptr(cores) })
			set("sockets", func() { params.Sockets = int64Ptr(sockets) })
			set("vcpus", func() { params.Vcpus = int64Ptr(vcpus) })
			set("cpu", func() { params.Cpu = strPtr(cpu) })
			set("cpulimit", func() { params.Cpulimit = &cpulimit })
			set("cpuunits", func() { params.Cpuunits = int64Ptr(cpuunits) })
			set("affinity", func() { params.Affinity = strPtr(affinity) })
			set("memory", func() { params.Memory = strPtr(memory) })
			set("balloon", func() { params.Balloon = int64Ptr(balloon) })
			set("shares", func() { params.Shares = int64Ptr(shares) })
			set("numa", func() { params.Numa = boolPtr(numa) })
			set("hugepages", func() { params.Hugepages = strPtr(hugepages) })
			set("keephugepages", func() { params.Keephugepages = boolPtr(keephugepages) })
			set("allow-ksm", func() { params.AllowKsm = boolPtr(allowKsm) })
			set("smbios1", func() { params.Smbios1 = strPtr(smbios1) })
			set("machine", func() { params.Machine = strPtr(machine) })
			set("arch", func() { params.Arch = strPtr(arch) })
			set("ostype", func() { params.Ostype = strPtr(ostype) })
			set("bios", func() { params.Bios = strPtr(bios) })
			set("efidisk0", func() { params.Efidisk0 = strPtr(efidisk0) })
			set("tpmstate0", func() { params.Tpmstate0 = strPtr(tpmstate0) })
			set("vga", func() { params.Vga = strPtr(vga) })
			set("scsihw", func() { params.Scsihw = strPtr(scsihw) })
			set("boot", func() { params.Boot = strPtr(boot) })
			set("acpi", func() { params.Acpi = boolPtr(acpi) })
			set("kvm", func() { params.Kvm = boolPtr(kvm) })
			set("freeze", func() { params.Freeze = boolPtr(freeze) })
			set("localtime", func() { params.Localtime = boolPtr(localtime) })
			set("tablet", func() { params.Tablet = boolPtr(tablet) })
			set("tdf", func() { params.Tdf = boolPtr(tdf) })
			set("reboot", func() { params.Reboot = boolPtr(reboot) })
			set("protection", func() { params.Protection = boolPtr(protection) })
			set("template", func() { params.Template = boolPtr(template) })
			set("unique", func() { params.Unique = boolPtr(unique) })
			set("force", func() { params.Force = boolPtr(force) })
			set("hotplug", func() { params.Hotplug = strPtr(hotplug) })
			set("hookscript", func() { params.Hookscript = strPtr(hookscript) })
			set("watchdog", func() { params.Watchdog = strPtr(watchdog) })
			set("rng0", func() { params.Rng0 = strPtr(rng0) })
			set("audio0", func() { params.Audio0 = strPtr(audio0) })
			set("keyboard", func() { params.Keyboard = strPtr(keyboard) })
			set("spice-enhancements", func() { params.SpiceEnhancements = strPtr(spiceEnhance) })
			set("amd-sev", func() { params.AmdSev = strPtr(amdSev) })
			set("intel-tdx", func() { params.IntelTdx = strPtr(intelTdx) })
			set("ivshmem", func() { params.Ivshmem = strPtr(ivshmem) })
			set("args", func() { params.Args = strPtr(kvmArgs) })
			set("vmgenid", func() { params.Vmgenid = strPtr(vmgenid) })
			set("startdate", func() { params.Startdate = strPtr(startdate) })
			set("vmstatestorage", func() { params.Vmstatestorage = strPtr(vmstatestorage) })

			set("storage", func() { params.Storage = strPtr(storage) })
			set("description", func() { params.Description = strPtr(description) })
			set("archive", func() { params.Archive = strPtr(archive) })
			set("live-restore", func() { params.LiveRestore = boolPtr(liveRestore) })
			set("import-working-storage", func() { params.ImportWorkingStorage = strPtr(importWorkingStorage) })
			set("bwlimit", func() { params.Bwlimit = int64Ptr(bwlimit) })
			set("migrate-downtime", func() { params.MigrateDowntime = &migrateDowntime })
			set("migrate-speed", func() { params.MigrateSpeed = int64Ptr(migrateSpeed) })
			set("ha-managed", func() { params.HaManaged = boolPtr(haManaged) })

			set("agent", func() { params.Agent = strPtr(agent) })
			set("ciuser", func() { params.Ciuser = strPtr(ciuser) })
			set("cipassword", func() { params.Cipassword = strPtr(cipassword) })
			set("citype", func() { params.Citype = strPtr(citype) })
			set("ciupgrade", func() { params.Ciupgrade = boolPtr(ciupgrade) })
			set("cicustom", func() { params.Cicustom = strPtr(cicustom) })
			set("nameserver", func() { params.Nameserver = strPtr(nameserver) })
			set("searchdomain", func() { params.Searchdomain = strPtr(searchdomain) })
			set("sshkeys", func() { params.Sshkeys = strPtr(encodeSSHKeys(sshkeys)) })

			// Indexed device slots. Legacy single-slot scalars merge into the
			// repeatable map, erroring when both target the same index.
			net, err := mergeLegacySlots(netSlots, "net", fl, legacySlot{"net0", net0, 0})
			if err != nil {
				return err
			}
			if len(net) > 0 {
				params.Net = net
			}
			scsi, err := mergeLegacySlots(scsiSlots, "scsi", fl, legacySlot{"scsi0", scsi0, 0})
			if err != nil {
				return err
			}
			if len(scsi) > 0 {
				params.Scsi = scsi
			}
			ide, err := mergeLegacySlots(ideSlots, "ide", fl, legacySlot{"ide2", ide2, 2})
			if err != nil {
				return err
			}
			if len(ide) > 0 {
				params.Ide = ide
			}
			ipconfig, err := mergeLegacySlots(ipconfigSlots, "ipconfig", fl, legacySlot{"ipconfig0", ipconfig0, 0})
			if err != nil {
				return err
			}
			if len(ipconfig) > 0 {
				params.Ipconfig = ipconfig
			}

			for _, s := range []struct {
				vals []string
				name string
				dst  *map[int]string
			}{
				{virtioSlots, "virtio", &params.Virtio},
				{sataSlots, "sata", &params.Sata},
				{hostpciSlots, "hostpci", &params.Hostpci},
				{serialSlots, "serial", &params.Serial},
				{usbSlots, "usb", &params.Usb},
				{parallelSlots, "parallel", &params.Parallel},
				{numaNodeSlots, "numa-node", &params.NumaMap},
				{virtiofsSlots, "virtiofs", &params.Virtiofs},
			} {
				m, perr := parseIndexedSlots(s.vals, s.name)
				if perr != nil {
					return perr
				}
				if len(m) > 0 {
					*s.dst = m
				}
			}

			resp, err := deps.API.Nodes.CreateQemu(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("create VM %d on node %q: %w", vmid, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("VM %d created.", vmid))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&async, "async", false, "return the task UPID immediately without waiting")

	f.StringVar(&name, "name", "", "VM name")
	f.StringVar(&pool, "pool", "", "resource pool to place the VM in")
	f.StringVar(&tags, "tags", "", "comma- or semicolon-separated tags")
	f.BoolVar(&start, "start", false, "start the VM immediately after creation")
	f.BoolVar(&onboot, "onboot", false, "start the VM during host bootup")
	f.StringVar(&startup, "startup", "", "startup/shutdown behavior, e.g. order=1,up=30,down=60")
	f.StringVar(&lock, "lock", "", "lock the VM, e.g. backup, migrate, or suspended")

	f.Int64Var(&cores, "cores", 0, "CPU cores per socket")
	f.Int64Var(&sockets, "sockets", 0, "CPU sockets")
	f.Int64Var(&vcpus, "vcpus", 0, "number of hotplugged vCPUs")
	f.StringVar(&cpu, "cpu", "", "emulated CPU type, e.g. host or x86-64-v2-AES")
	f.Float64Var(&cpulimit, "cpulimit", 0, "CPU usage limit (0 = unlimited)")
	f.Int64Var(&cpuunits, "cpuunits", 0, "CPU weight, clamped to [1,10000]")
	f.StringVar(&affinity, "affinity", "", "host cores used to run guest processes, e.g. 0,5,8-11")
	f.StringVar(&memory, "memory", "", "RAM in MiB")
	f.Int64Var(&balloon, "balloon", 0, "target balloon memory in MiB (0 disables ballooning)")
	f.Int64Var(&shares, "shares", 0, "memory shares for auto-ballooning (0 disables)")
	f.BoolVar(&numa, "numa", false, "enable NUMA")
	f.StringVar(&hugepages, "hugepages", "", "hugepage size in MiB, e.g. 2, 1024, or any")
	f.BoolVar(&keephugepages, "keephugepages", false, "keep hugepages after VM shutdown")
	f.BoolVar(&allowKsm, "allow-ksm", false, "allow this guest's pages to be merged via KSM")
	f.StringVar(&smbios1, "smbios1", "", "SMBIOS type 1 fields, e.g. uuid=...,manufacturer=...")
	f.StringVar(&machine, "machine", "", "QEMU machine type, e.g. q35 or pc-i440fx-8.1")
	f.StringVar(&arch, "arch", "", "virtual processor architecture, e.g. x86_64 or aarch64")
	f.StringVar(&ostype, "ostype", "", "guest OS type, e.g. l26 or win11")
	f.StringVar(&bios, "bios", "", "BIOS implementation: seabios or ovmf (UEFI)")
	f.StringVar(&efidisk0, "efidisk0", "", "EFI vars disk, e.g. local-lvm:0 or local-lvm:0,efitype=4m")
	f.StringVar(&tpmstate0, "tpmstate0", "", "TPM state disk, e.g. local-lvm:0,version=v2.0")
	f.StringVar(&vga, "vga", "", "VGA hardware, e.g. std, qxl, virtio, or serial0")
	f.StringVar(&scsihw, "scsihw", "", "SCSI controller model, e.g. virtio-scsi-pci")
	f.StringVar(&boot, "boot", "", "boot order spec, e.g. order=scsi0;net0")
	f.BoolVar(&acpi, "acpi", false, "enable ACPI")
	f.BoolVar(&kvm, "kvm", false, "enable KVM hardware virtualization")
	f.BoolVar(&freeze, "freeze", false, "freeze CPU at startup")
	f.BoolVar(&localtime, "localtime", false, "set the RTC to local time (default for Windows)")
	f.BoolVar(&tablet, "tablet", false, "enable the USB tablet pointer device")
	f.BoolVar(&tdf, "tdf", false, "enable time drift fix")
	f.BoolVar(&reboot, "reboot", false, "allow reboot (if false the VM exits on reboot)")
	f.BoolVar(&protection, "protection", false, "set the protection flag to block remove/update")
	f.BoolVar(&template, "template", false, "create the VM as a template")
	f.BoolVar(&unique, "unique", false, "assign a unique random ethernet address")
	f.BoolVar(&force, "force", false, "allow overwriting an existing VM (restore only)")
	f.StringVar(&hotplug, "hotplug", "", "hotplug features, e.g. network,disk,usb or 0 to disable")
	f.StringVar(&hookscript, "hookscript", "", "hookscript volume run during lifecycle events")
	f.StringVar(&watchdog, "watchdog", "", "virtual watchdog device, e.g. model=i6300esb,action=reset")
	f.StringVar(&rng0, "rng0", "", "VirtIO RNG, e.g. source=/dev/urandom")
	f.StringVar(&audio0, "audio0", "", "audio device, e.g. device=ich9-intel-hda,driver=spice")
	f.StringVar(&keyboard, "keyboard", "", "VNC keyboard layout, e.g. en-us or de")
	f.StringVar(&spiceEnhance, "spice-enhancements", "", "SPICE enhancements, e.g. foldersharing=1,videostreaming=all")
	f.StringVar(&amdSev, "amd-sev", "", "AMD SEV options, e.g. type=std")
	f.StringVar(&intelTdx, "intel-tdx", "", "Intel TDX options, e.g. 1")
	f.StringVar(&ivshmem, "ivshmem", "", "inter-VM shared memory, e.g. size=32,name=foo")
	f.StringVar(&kvmArgs, "args", "", "arbitrary arguments passed to kvm")
	f.StringVar(&vmgenid, "vmgenid", "", "VM generation ID; 1 to autogenerate, 0 to disable")
	f.StringVar(&startdate, "startdate", "", "initial RTC date, e.g. now or 2006-06-17T16:01:21")
	f.StringVar(&vmstatestorage, "vmstatestorage", "", "default storage for VM state volumes")

	f.StringVar(&storage, "storage", "", "default storage for newly allocated disks")
	f.StringVar(&description, "description", "", "VM description shown in the web UI")
	f.StringVar(&archive, "archive", "", "backup archive to restore from (path or storage volume)")
	f.BoolVar(&liveRestore, "live-restore", false, "start the VM while restoring in the background")
	f.StringVar(&importWorkingStorage, "import-working-storage", "", "storage used as intermediate extraction area during import")
	f.Int64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s (restore)")
	f.Float64Var(&migrateDowntime, "migrate-downtime", 0, "maximum tolerated migration downtime in seconds")
	f.Int64Var(&migrateSpeed, "migrate-speed", 0, "maximum migration speed in MB/s (0 = no limit)")
	f.BoolVar(&haManaged, "ha-managed", false, "register the VM as a HA resource after creation")

	f.StringVar(&agent, "agent", "", "QEMU guest-agent option string, e.g. 1 or enabled=1,fstrim_cloned_disks=1")
	f.StringVar(&ciuser, "ciuser", "", "cloud-init: default user to configure")
	f.StringVar(&cipassword, "cipassword", "", "cloud-init: password for the default user")
	f.StringVar(&citype, "citype", "", "cloud-init: config format, e.g. nocloud or configdrive2")
	f.BoolVar(&ciupgrade, "ciupgrade", false, "cloud-init: run a package upgrade on first boot")
	f.StringVar(&cicustom, "cicustom", "", "cloud-init: custom config files, e.g. user=local:snippets/user.yml")
	f.StringVar(&nameserver, "nameserver", "", "cloud-init: DNS server IP address")
	f.StringVar(&searchdomain, "searchdomain", "", "cloud-init: DNS search domain")
	f.StringVar(&sshkeys, "sshkeys", "", "cloud-init: public SSH keys (one per line, OpenSSH format)")

	f.StringArrayVar(&netSlots, "net", nil, "network device as INDEX=VALUE (repeatable), e.g. 0=virtio,bridge=vmbr0")
	f.StringArrayVar(&scsiSlots, "scsi", nil, "SCSI disk as INDEX=VALUE (repeatable), e.g. 0=local-lvm:8")
	f.StringArrayVar(&ideSlots, "ide", nil, "IDE device as INDEX=VALUE (repeatable), e.g. 2=local:iso/img.iso,media=cdrom")
	f.StringArrayVar(&virtioSlots, "virtio", nil, "VirtIO disk as INDEX=VALUE (repeatable), e.g. 0=local-lvm:32")
	f.StringArrayVar(&sataSlots, "sata", nil, "SATA disk as INDEX=VALUE (repeatable), e.g. 0=local-lvm:16")
	f.StringArrayVar(&ipconfigSlots, "ipconfig", nil, "cloud-init IP config as INDEX=VALUE (repeatable), e.g. 0=ip=dhcp")
	f.StringArrayVar(&hostpciSlots, "hostpci", nil, "PCI passthrough as INDEX=VALUE (repeatable), e.g. 0=0000:01:00,pcie=1")
	f.StringArrayVar(&serialSlots, "serial", nil, "serial device as INDEX=VALUE (repeatable), e.g. 0=socket")
	f.StringArrayVar(&usbSlots, "usb", nil, "USB device as INDEX=VALUE (repeatable), e.g. 0=host=1234:5678")
	f.StringArrayVar(&parallelSlots, "parallel", nil, "parallel device as INDEX=VALUE (repeatable), e.g. 0=/dev/parport0")
	f.StringArrayVar(&numaNodeSlots, "numa-node", nil, "NUMA topology node as INDEX=VALUE (repeatable), e.g. 0=cpus=0-3,memory=1024")
	f.StringArrayVar(&virtiofsSlots, "virtiofs", nil, "virtio-fs share as INDEX=VALUE (repeatable), e.g. 0=dirid=shared")

	f.StringVar(&net0, "net0", "", "network device 0 (alias for --net 0=...)")
	f.StringVar(&scsi0, "scsi0", "", "SCSI disk 0 (alias for --scsi 0=...)")
	f.StringVar(&ide2, "ide2", "", "IDE device 2 (alias for --ide 2=...)")
	f.StringVar(&ipconfig0, "ipconfig0", "", "cloud-init IP config for net0 (alias for --ipconfig 0=...)")
	return cmd
}

// legacySlot pairs a legacy single-slot scalar flag (for example --net0) with
// the device-family index it maps to.
type legacySlot struct {
	flag string
	val  string
	idx  int
}

// mergeLegacySlots parses the repeatable INDEX=VALUE slot list for a device
// family and folds in any legacy single-slot scalar flags that were set. It
// errors if a legacy flag and the repeatable list both target the same index.
func mergeLegacySlots(
	vals []string, family string, fl flagSet, legacy ...legacySlot,
) (map[int]string, error) {
	m, err := parseIndexedSlots(vals, family)
	if err != nil {
		return nil, err
	}
	for _, l := range legacy {
		if !fl.Changed(l.flag) {
			continue
		}
		if _, dup := m[l.idx]; dup {
			return nil, fmt.Errorf(
				"%s slot %d specified by both --%s and --%s with index %d",
				family, l.idx, l.flag, family, l.idx)
		}
		m[l.idx] = l.val
	}
	return m, nil
}

// flagSet is the minimal subset of *pflag.FlagSet that mergeLegacySlots needs;
// declaring it locally keeps the helper testable without importing pflag here.
type flagSet interface {
	Changed(name string) bool
}
