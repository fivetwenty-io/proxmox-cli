package qemu

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
)

// newConfigCmd builds the `pmx qemu config` sub-group.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and modify VM configuration",
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigPendingCmd(), newConfigDescribeCmd())
	return cmd
}

// newConfigDescribeCmd builds `pmx qemu config describe`, an offline catalog
// of every settable VM configuration option from the PVE API schema (see
// config_schema_gen.go). The catalog view omits dict sub-key rows (the
// ~13 indexed device families would otherwise explode the table); pass an
// option name to see its sub-keys.
func newConfigDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: configSchemas,
		Short:   "Describe all settable VM configuration options and their defaults",
		Long: "List every settable VM configuration option from the PVE API schema: " +
			"type, built-in default, allowed values, and (for a single option) the " +
			"sub-keys of dict-encoded options. Runs offline. Pass an option name to " +
			"show only that option with full descriptions and sub-keys.",
		CommandHint:         "pmx qemu config describe",
		SubKeyRowsInCatalog: false,
	})
}

// newConfigGetCmd builds `pmx qemu config get <vmid>`.
//
// The raw API response is read directly (deps.API.Raw.GetCtx) instead of
// through nodes.ListQemuConfig/ListQemuConfigResponse because that generated
// struct cannot represent dynamically indexed keys at all: its fields for
// indexed devices are literal placeholders — e.g. Netn tagged json:"net[n]",
// Scsin tagged json:"scsi[n]" — that never match a real response key such as
// "net0" or "scsi0". Decoding into that struct would silently drop every
// disk and network key present in the config. This is a fixed-field
// limitation of the generated struct, not tech debt to migrate away from;
// see TestQemuConfigGet_DynamicKeysPreserved for the regression guard.
func newConfigGetCmd() *cobra.Command {
	var (
		current      bool
		snapshot     string
		withDefaults bool
	)
	cmd := &cobra.Command{
		Use:   "get <vmid|name>",
		Short: "Show the configuration of a VM",
		Long: "Show the VM configuration currently set. The PVE API omits options " +
			"left at their built-in defaults; pass --defaults to also list the options " +
			"that carry one, with the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := map[string]any{}
			if cmd.Flags().Changed("current") {
				if current {
					params["current"] = 1
				} else {
					params["current"] = 0
				}
			}
			if cmd.Flags().Changed("snapshot") {
				params["snapshot"] = snapshot
			}

			path := fmt.Sprintf("/nodes/%s/qemu/%s/config", url.PathEscape(node), url.PathEscape(vmid))
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("get config for VM %s on node %q: %w", vmid, node, err)
			}

			single, err := configToSingle(data)
			if err != nil {
				return err
			}

			raw := data
			if withDefaults {
				single, raw = optionschema.MergeDefaults(configSchemas, single, data, optionschema.MergeOpts{SkipUnset: true})
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&current, "current", false, "get current values instead of pending values")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "fetch config values from the given snapshot")
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"also list unset options that have a built-in default, with their default value")
	return cmd
}

// configToSingle flattens a decoded VM config object into a map of string values.
func configToSingle(data any) (map[string]string, error) {
	m, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decode VM config: unexpected response shape %T", data)
	}
	single := make(map[string]string, len(m))
	for k, v := range m {
		single[k] = stringifyValue(v)
	}
	return single, nil
}

// stringifyValue renders a JSON-decoded scalar (or nested value) as a string.
func stringifyValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// newConfigSetCmd builds `pmx qemu config set <vmid>`.
func newConfigSetCmd() *cobra.Command {
	var (
		cores       int64
		memory      string
		balloon     int64
		name        string
		description string
		boot        string
		scsihw      string
		cpu         string
		ostype      string
		deleteKeys  string
		revertKeys  string
		digest      string
		agent       string
		onboot      bool
		startup     string

		sockets        int64
		vcpus          int64
		cpulimit       float64
		cpuunits       int64
		affinity       string
		shares         int64
		numa           bool
		hugepages      string
		keephugepages  bool
		allowKsm       bool
		smbios1        string
		machine        string
		arch           string
		bios           string
		efidisk0       string
		tpmstate0      string
		vga            string
		tags           string
		acpi           bool
		kvm            bool
		freeze         bool
		localtime      bool
		tablet         bool
		tdf            bool
		reboot         bool
		protection     bool
		template       bool
		force          bool
		skiplock       bool
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
		lock           string
		vmgenid        string
		startdate      string
		vmstatestorage string
		migrateDownt   float64
		migrateSpeed   int64

		autostart bool
		cdrom     string

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
		net1      string
		scsi0     string
		scsi1     string
		ide0      string
		ide2      string
		virtio0   string
		virtio1   string
		ipconfig0 string
		ipconfig1 string

		// --set KEY=VALUE escape hatch (see cli.ParseKeyValues/OverlayKeyValues).
		rawSetFlags []string
	)
	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Update the configuration of a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.UpdateQemuConfigParams{}
			fl := cmd.Flags()
			changed := false
			set := func(name string, apply func()) {
				if fl.Changed(name) {
					apply()
					changed = true
				}
			}

			set("cores", func() { params.Cores = int64Ptr(cores) })
			set("memory", func() { params.Memory = strPtr(memory) })
			set("balloon", func() { params.Balloon = int64Ptr(balloon) })
			set("name", func() { params.Name = strPtr(name) })
			set("description", func() { params.Description = strPtr(description) })
			set("boot", func() { params.Boot = strPtr(boot) })
			set("scsihw", func() { params.Scsihw = strPtr(scsihw) })
			set("cpu", func() { params.Cpu = strPtr(cpu) })
			set("ostype", func() { params.Ostype = strPtr(ostype) })
			set("agent", func() { params.Agent = strPtr(agent) })
			// Boot-time behavior: onboot starts the VM on host boot; startup
			// controls order + up/down delays, e.g. order=1,up=30,down=60.
			set("onboot", func() { params.Onboot = boolPtr(onboot) })
			set("startup", func() { params.Startup = strPtr(startup) })
			set("delete", func() { params.Delete = strPtr(deleteKeys) })
			set("revert", func() { params.Revert = strPtr(revertKeys) })
			set("digest", func() { params.Digest = strPtr(digest) })

			set("sockets", func() { params.Sockets = int64Ptr(sockets) })
			set("vcpus", func() { params.Vcpus = int64Ptr(vcpus) })
			set("cpulimit", func() { params.Cpulimit = &cpulimit })
			set("cpuunits", func() { params.Cpuunits = int64Ptr(cpuunits) })
			set("affinity", func() { params.Affinity = strPtr(affinity) })
			set("shares", func() { params.Shares = int64Ptr(shares) })
			set("numa", func() { params.Numa = boolPtr(numa) })
			set("hugepages", func() { params.Hugepages = strPtr(hugepages) })
			set("keephugepages", func() { params.Keephugepages = boolPtr(keephugepages) })
			set("allow-ksm", func() { params.AllowKsm = boolPtr(allowKsm) })
			set("smbios1", func() { params.Smbios1 = strPtr(smbios1) })
			set("machine", func() { params.Machine = strPtr(machine) })
			set("arch", func() { params.Arch = strPtr(arch) })
			set("bios", func() { params.Bios = strPtr(bios) })
			set("efidisk0", func() { params.Efidisk0 = strPtr(efidisk0) })
			set("tpmstate0", func() { params.Tpmstate0 = strPtr(tpmstate0) })
			set("vga", func() { params.Vga = strPtr(vga) })
			set("tags", func() { params.Tags = strPtr(tags) })
			set("acpi", func() { params.Acpi = boolPtr(acpi) })
			set("kvm", func() { params.Kvm = boolPtr(kvm) })
			set("freeze", func() { params.Freeze = boolPtr(freeze) })
			set("localtime", func() { params.Localtime = boolPtr(localtime) })
			set("tablet", func() { params.Tablet = boolPtr(tablet) })
			set("tdf", func() { params.Tdf = boolPtr(tdf) })
			set("reboot", func() { params.Reboot = boolPtr(reboot) })
			set("protection", func() { params.Protection = boolPtr(protection) })
			set("template", func() { params.Template = boolPtr(template) })
			set("force", func() { params.Force = boolPtr(force) })
			set("skiplock", func() { params.Skiplock = boolPtr(skiplock) })
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
			set("lock", func() { params.Lock = strPtr(lock) })
			set("vmgenid", func() { params.Vmgenid = strPtr(vmgenid) })
			set("startdate", func() { params.Startdate = strPtr(startdate) })
			set("vmstatestorage", func() { params.Vmstatestorage = strPtr(vmstatestorage) })
			set("migrate-downtime", func() { params.MigrateDowntime = &migrateDownt })
			set("migrate-speed", func() { params.MigrateSpeed = int64Ptr(migrateSpeed) })
			set("autostart", func() { params.Autostart = boolPtr(autostart) })
			set("cdrom", func() { params.Cdrom = strPtr(cdrom) })

			// Cloud-init scalars (mirror `qemu create`).
			set("ciuser", func() { params.Ciuser = strPtr(ciuser) })
			set("cipassword", func() { params.Cipassword = strPtr(cipassword) })
			set("citype", func() { params.Citype = strPtr(citype) })
			set("ciupgrade", func() { params.Ciupgrade = boolPtr(ciupgrade) })
			set("cicustom", func() { params.Cicustom = strPtr(cicustom) })
			set("nameserver", func() { params.Nameserver = strPtr(nameserver) })
			set("searchdomain", func() { params.Searchdomain = strPtr(searchdomain) })
			set("sshkeys", func() { params.Sshkeys = strPtr(encodeSSHKeys(sshkeys)) })

			// Indexed device + ipconfig maps. Repeatable INDEX=VALUE slots merge
			// with the legacy single-slot scalars; the apiclient marshals each
			// map[int]string into net0, net1, … keys.
			for _, s := range []struct {
				vals   []string
				family string
				dst    *map[int]string
				legacy []legacySlot
			}{
				{netSlots, "net", &params.Net, []legacySlot{{"net0", net0, 0}, {"net1", net1, 1}}},
				{scsiSlots, "scsi", &params.Scsi, []legacySlot{{"scsi0", scsi0, 0}, {"scsi1", scsi1, 1}}},
				{ideSlots, "ide", &params.Ide, []legacySlot{{"ide0", ide0, 0}, {"ide2", ide2, 2}}},
				{virtioSlots, "virtio", &params.Virtio, []legacySlot{{"virtio0", virtio0, 0}, {"virtio1", virtio1, 1}}},
				{sataSlots, "sata", &params.Sata, nil},
				{ipconfigSlots, "ipconfig", &params.Ipconfig, []legacySlot{{"ipconfig0", ipconfig0, 0}, {"ipconfig1", ipconfig1, 1}}},
				{hostpciSlots, "hostpci", &params.Hostpci, nil},
				{serialSlots, "serial", &params.Serial, nil},
				{usbSlots, "usb", &params.Usb, nil},
				{parallelSlots, "parallel", &params.Parallel, nil},
				{numaNodeSlots, "numa-node", &params.NumaMap, nil},
				{virtiofsSlots, "virtiofs", &params.Virtiofs, nil},
			} {
				m, merr := mergeLegacySlots(s.vals, s.family, fl, s.legacy...)
				if merr != nil {
					return merr
				}
				if len(m) > 0 {
					*s.dst = m
					changed = true
				}
			}

			sets, err := cli.ParseKeyValues(rawSetFlags)
			if err != nil {
				return err
			}

			var rawBody map[string]any
			if len(sets) > 0 {
				changed = true
				rawBody, err = cli.ParamsToMap(params)
				if err != nil {
					return fmt.Errorf("build config update body for VM %s: %w", vmid, err)
				}
				if rawBody, err = cli.OverlayKeyValues(cmd.ErrOrStderr(), rawBody, sets, isKnownConfigKey); err != nil {
					return err
				}
			}

			warnDangerousConfig(cmd, fl, sets, protectionCleared(fl, protection, deleteKeys, sets))

			if !changed {
				return fmt.Errorf("no configuration changes specified: pass at least one --<key> flag")
			}

			if len(sets) == 0 {
				if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
					return fmt.Errorf("update config for VM %s on node %q: %w", vmid, node, err)
				}
			} else {
				path := fmt.Sprintf("/nodes/%s/qemu/%s/config", url.PathEscape(node), url.PathEscape(vmid))
				if _, err := deps.API.Raw.PutCtx(cmd.Context(), path, rawBody); err != nil {
					return fmt.Errorf("update config for VM %s on node %q: %w", vmid, node, err)
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("VM %s config updated.", vmid)}, deps.Format)
		},
	}

	f := cmd.Flags()
	f.Int64Var(&cores, "cores", 0, "number of CPU cores per socket")
	f.StringVar(&memory, "memory", "", "memory in MiB")
	f.Int64Var(&balloon, "balloon", 0, "target balloon memory in MiB (0 disables ballooning)")
	f.StringVar(&name, "name", "", "VM name")
	f.StringVar(&description, "description", "", "VM description")
	f.StringVar(&boot, "boot", "", "boot order specification")
	f.StringVar(&scsihw, "scsihw", "", "SCSI controller model")
	f.StringVar(&cpu, "cpu", "", "emulated CPU type")
	f.StringVar(&ostype, "ostype", "", "guest OS type")
	f.StringVar(&agent, "agent", "", "QEMU guest-agent option string, e.g. 1 or enabled=1,fstrim_cloned_disks=1")
	f.BoolVar(&onboot, "onboot", false, "start the VM automatically on host boot")
	f.StringVar(&startup, "startup", "", "startup/shutdown behavior, e.g. order=1,up=30,down=60")
	f.StringVar(&deleteKeys, "delete", "", "comma-separated config keys to remove")
	f.StringVar(&revertKeys, "revert", "", "comma-separated pending config keys to revert")
	f.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")

	f.Int64Var(&sockets, "sockets", 0, "number of CPU sockets")
	f.Int64Var(&vcpus, "vcpus", 0, "number of hotplugged vCPUs")
	f.Float64Var(&cpulimit, "cpulimit", 0, "CPU usage limit (0 = unlimited)")
	f.Int64Var(&cpuunits, "cpuunits", 0, "CPU weight relative to other guests (valid range 1-10000)")
	f.StringVar(&affinity, "affinity", "", "host cores used to run guest processes, e.g. 0,5,8-11")
	f.Int64Var(&shares, "shares", 0, "memory shares for auto-ballooning (0 disables)")
	f.BoolVar(&numa, "numa", false, "enable NUMA")
	f.StringVar(&hugepages, "hugepages", "", "hugepage size in MiB, e.g. 2, 1024, or any")
	f.BoolVar(&keephugepages, "keephugepages", false, "keep hugepages after VM shutdown")
	f.BoolVar(&allowKsm, "allow-ksm", false, "allow this guest's pages to be merged via KSM")
	f.StringVar(&smbios1, "smbios1", "", "SMBIOS type 1 fields, e.g. uuid=...,manufacturer=...")
	f.StringVar(&machine, "machine", "", "QEMU machine type, e.g. q35 or pc-i440fx-8.1")
	f.StringVar(&arch, "arch", "", "virtual processor architecture, e.g. x86_64 or aarch64")
	f.StringVar(&bios, "bios", "", "BIOS implementation: seabios or ovmf (UEFI)")
	f.StringVar(&efidisk0, "efidisk0", "", "EFI vars disk, e.g. local-lvm:0 or local-lvm:0,efitype=4m")
	f.StringVar(&tpmstate0, "tpmstate0", "", "TPM state disk, e.g. local-lvm:0,version=v2.0")
	f.StringVar(&vga, "vga", "", "VGA hardware, e.g. std, qxl, virtio, or serial0")
	f.StringVar(&tags, "tags", "", "comma- or semicolon-separated tags")
	f.BoolVar(&acpi, "acpi", false, "enable ACPI")
	f.BoolVar(&kvm, "kvm", false, "enable KVM hardware virtualization")
	f.BoolVar(&freeze, "freeze", false, "freeze CPU at startup")
	f.BoolVar(&localtime, "localtime", false, "set the RTC to local time (default for Windows)")
	f.BoolVar(&tablet, "tablet", false, "enable the USB tablet pointer device")
	f.BoolVar(&tdf, "tdf", false, "enable time drift fix")
	f.BoolVar(&reboot, "reboot", false, "allow reboot (if false the VM exits on reboot)")
	f.BoolVar(&protection, "protection", false, "set the protection flag to block remove/update")
	f.BoolVar(&template, "template", false, "mark the VM as a template")
	f.BoolVar(&force, "force", false, "force physical removal of unlinked disks (with --delete)")
	f.BoolVar(&skiplock, "skiplock", false, "ignore locks (root only)")
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
	f.StringVar(&lock, "lock", "", "lock the VM, e.g. backup, migrate, or suspended")
	f.StringVar(&vmgenid, "vmgenid", "", "VM generation ID; 1 to autogenerate, 0 to disable")
	f.StringVar(&startdate, "startdate", "", "initial RTC date, e.g. now or 2006-06-17T16:01:21")
	f.StringVar(&vmstatestorage, "vmstatestorage", "", "default storage for VM state volumes")
	f.Float64Var(&migrateDownt, "migrate-downtime", 0, "maximum tolerated migration downtime in seconds")
	f.Int64Var(&migrateSpeed, "migrate-speed", 0, "maximum migration speed in MB/s (0 = no limit)")
	f.BoolVar(&autostart, "autostart", false, "automatically restart after crash (currently ignored by PVE)")
	f.StringVar(&cdrom, "cdrom", "", "CD-ROM image alias for -ide2, e.g. local:iso/img.iso,media=cdrom")

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

	f.StringVar(&net0, "net0", "", "network device net0 (alias for --net 0=...)")
	f.StringVar(&net1, "net1", "", "network device net1 (alias for --net 1=...)")
	f.StringVar(&scsi0, "scsi0", "", "SCSI disk scsi0 (alias for --scsi 0=...)")
	f.StringVar(&scsi1, "scsi1", "", "SCSI disk scsi1 (alias for --scsi 1=...)")
	f.StringVar(&ide0, "ide0", "", "IDE disk ide0 (alias for --ide 0=...)")
	f.StringVar(&ide2, "ide2", "", "IDE device ide2, e.g. <storage>:cloudinit for the cloud-init drive (alias for --ide 2=...)")
	f.StringVar(&virtio0, "virtio0", "", "VirtIO disk virtio0 (alias for --virtio 0=...)")
	f.StringVar(&virtio1, "virtio1", "", "VirtIO disk virtio1 (alias for --virtio 1=...)")
	f.StringVar(&ipconfig0, "ipconfig0", "", "cloud-init IP config for net0 (alias for --ipconfig 0=...)")
	f.StringVar(&ipconfig1, "ipconfig1", "", "cloud-init IP config for net1 (alias for --ipconfig 1=...)")

	f.StringArrayVar(&rawSetFlags, "set", nil,
		"set an arbitrary config option as KEY=VALUE (repeatable); the value is sent to the "+
			"API verbatim. Escape hatch for options that have no dedicated flag yet.")

	// Append generated schema detail (allowed values, defaults, numeric
	// ranges, sub-keys) to each option flag's help text; flags with no
	// matching schema entry (aliases, legacy slots) are left untouched. See
	// config_schema_gen.go.
	optionschema.EnrichFlags(f, configSchemas)
	return cmd
}

// isKnownConfigKey reports whether key is a name the CLI's offline config
// schema recognizes, either directly or via an indexed family (e.g. "net0"
// matches the "net[n]" schema entry via its "net" flag prefix). Used by
// --set to emit a stderr note for options unknown to this CLI without
// blocking them — new PVE options are exactly what the escape hatch is for.
func isKnownConfigKey(key string) bool {
	if optionschema.Find(configSchemas, key) != nil {
		return true
	}
	for i := range configSchemas {
		s := &configSchemas[i]
		if !s.Indexed || s.Flag == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(key, s.Flag); ok && rest != "" && isASCIIDigits(rest) {
			return true
		}
	}
	return false
}

// isASCIIDigits reports whether s is non-empty and consists only of ASCII
// digits, used to recognize indexed config keys such as "net0" or "usb12".
func isASCIIDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pendingEntry is the minimal decoded shape of one entry from nodes.ListQemuPending.
type pendingEntry struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Pending any    `json:"pending"`
}

// newConfigPendingCmd builds `pmx qemu config pending <vmid>`.
func newConfigPendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pending <vmid|name>",
		Short: "Show pending configuration changes for a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListQemuPending(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get pending config for VM %s on node %q: %w", vmid, node, err)
			}

			headers := []string{"KEY", "VALUE", "PENDING-VALUE"}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e pendingEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode pending entry: %w", err)
					}
					rows = append(rows, []string{e.Key, stringifyValue(e.Value), stringifyValue(e.Pending)})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
		},
	}
}
