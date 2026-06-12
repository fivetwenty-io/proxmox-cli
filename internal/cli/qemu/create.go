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
// set. Disk, network, and CD-ROM devices are passed as PVE option strings
// (for example --scsi0 "local-lvm:8" or --net0 "virtio,bridge=vmbr0").
func newCreateCmd() *cobra.Command {
	var (
		async   bool
		name    string
		memory  string
		cores   int64
		sockets int64
		net0    string
		scsi0   string
		ide2    string
		scsihw  string
		ostype  string
		boot    string
		pool    string
		tags    string
		agent   string
		start   bool

		ciuser       string
		cipassword   string
		citype       string
		ciupgrade    bool
		cicustom     string
		nameserver   string
		searchdomain string
		sshkeys      string
		ipconfig0    string
	)
	cmd := &cobra.Command{
		Use:   "create <vmid>",
		Short: "Create a QEMU virtual machine",
		Long: "Create a QEMU virtual machine on a node. Devices are given as PVE " +
			"option strings, e.g. --scsi0 \"local-lvm:8\" and --net0 " +
			"\"virtio,bridge=vmbr0\".",
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
			if fl.Changed("name") {
				params.Name = strPtr(name)
			}
			if fl.Changed("memory") {
				params.Memory = strPtr(memory)
			}
			if fl.Changed("cores") {
				params.Cores = int64Ptr(cores)
			}
			if fl.Changed("sockets") {
				params.Sockets = int64Ptr(sockets)
			}
			if fl.Changed("scsihw") {
				params.Scsihw = strPtr(scsihw)
			}
			if fl.Changed("ostype") {
				params.Ostype = strPtr(ostype)
			}
			if fl.Changed("boot") {
				params.Boot = strPtr(boot)
			}
			if fl.Changed("pool") {
				params.Pool = strPtr(pool)
			}
			if fl.Changed("tags") {
				params.Tags = strPtr(tags)
			}
			if fl.Changed("agent") {
				params.Agent = strPtr(agent)
			}
			if fl.Changed("start") {
				params.Start = boolPtr(start)
			}
			if fl.Changed("net0") {
				params.Net = map[int]string{0: net0}
			}
			if fl.Changed("scsi0") {
				params.Scsi = map[int]string{0: scsi0}
			}
			if fl.Changed("ide2") {
				params.Ide = map[int]string{2: ide2}
			}
			if fl.Changed("ciuser") {
				params.Ciuser = strPtr(ciuser)
			}
			if fl.Changed("cipassword") {
				params.Cipassword = strPtr(cipassword)
			}
			if fl.Changed("citype") {
				params.Citype = strPtr(citype)
			}
			if fl.Changed("ciupgrade") {
				params.Ciupgrade = boolPtr(ciupgrade)
			}
			if fl.Changed("cicustom") {
				params.Cicustom = strPtr(cicustom)
			}
			if fl.Changed("nameserver") {
				params.Nameserver = strPtr(nameserver)
			}
			if fl.Changed("searchdomain") {
				params.Searchdomain = strPtr(searchdomain)
			}
			if fl.Changed("sshkeys") {
				params.Sshkeys = strPtr(sshkeys)
			}
			if fl.Changed("ipconfig0") {
				params.Ipconfig = map[int]string{0: ipconfig0}
			}

			resp, err := deps.API.Nodes.CreateQemu(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("create VM %d on node %q: %w", vmid, node, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("VM %d created.", vmid))
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&name, "name", "", "VM name")
	cmd.Flags().StringVar(&memory, "memory", "", "RAM in MiB")
	cmd.Flags().Int64Var(&cores, "cores", 0, "CPU cores per socket")
	cmd.Flags().Int64Var(&sockets, "sockets", 0, "CPU sockets")
	cmd.Flags().StringVar(&net0, "net0", "", "network device 0, e.g. virtio,bridge=vmbr0")
	cmd.Flags().StringVar(&scsi0, "scsi0", "", "SCSI disk 0, e.g. local-lvm:8")
	cmd.Flags().StringVar(&ide2, "ide2", "", "IDE device 2, e.g. local:iso/img.iso,media=cdrom")
	cmd.Flags().StringVar(&scsihw, "scsihw", "", "SCSI controller model, e.g. virtio-scsi-pci")
	cmd.Flags().StringVar(&ostype, "ostype", "", "guest OS type, e.g. l26")
	cmd.Flags().StringVar(&boot, "boot", "", "boot order spec, e.g. order=scsi0;net0")
	cmd.Flags().StringVar(&pool, "pool", "", "resource pool to place the VM in")
	cmd.Flags().StringVar(&tags, "tags", "", "comma- or semicolon-separated tags")
	cmd.Flags().StringVar(&agent, "agent", "", "QEMU guest-agent option string, e.g. 1 or enabled=1,fstrim_cloned_disks=1")
	cmd.Flags().BoolVar(&start, "start", false, "start the VM immediately after creation")
	cmd.Flags().StringVar(&ciuser, "ciuser", "", "cloud-init: default user to configure")
	cmd.Flags().StringVar(&cipassword, "cipassword", "", "cloud-init: password for the default user")
	cmd.Flags().StringVar(&citype, "citype", "", "cloud-init: config format, e.g. nocloud or configdrive2")
	cmd.Flags().BoolVar(&ciupgrade, "ciupgrade", false, "cloud-init: run a package upgrade on first boot")
	cmd.Flags().StringVar(&cicustom, "cicustom", "", "cloud-init: custom config files, e.g. user=local:snippets/user.yml")
	cmd.Flags().StringVar(&nameserver, "nameserver", "", "cloud-init: DNS server IP address")
	cmd.Flags().StringVar(&searchdomain, "searchdomain", "", "cloud-init: DNS search domain")
	cmd.Flags().StringVar(&sshkeys, "sshkeys", "", "cloud-init: public SSH keys (one per line, OpenSSH format)")
	cmd.Flags().StringVar(&ipconfig0, "ipconfig0", "", "cloud-init: IP config for net0, e.g. ip=dhcp or ip=10.0.0.5/24,gw=10.0.0.1")
	return cmd
}
