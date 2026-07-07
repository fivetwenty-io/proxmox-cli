package lxc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newCreateCmd builds `pve lxc create <vmid>`. The container is created as an
// asynchronous task; the command blocks until it completes unless --async is
// set. --ostemplate is required and names a template volume such as
// "local:vztmpl/alpine-3.22-default_20250617_amd64.tar.xz".
func newCreateCmd() *cobra.Command {
	var (
		async         bool
		ostemplate    string
		hostname      string
		storage       string
		rootfs        string
		memory        int64
		swap          int64
		cores         int64
		net0          string
		netSlots      []string
		mpSlots       []string
		devSlots      []string
		password      string
		sshPublicKeys string
		pool          string
		tags          string
		unprivileged  bool
		start         bool

		description        string
		nameserver         string
		searchdomain       string
		onboot             bool
		startup            string
		cpulimit           float64
		cpuunits           int64
		arch               string
		ostype             string
		features           string
		hookscript         string
		protection         bool
		bwlimit            float64
		haManaged          bool
		timezone           string
		tty                int64
		console            bool
		cmode              string
		template           bool
		unique             bool
		force              bool
		ignoreUnpackErrors bool
		restore            bool
		env                string
		entrypoint         string
		lock               string
		debug              bool

		unusedSlots []string

		// --set KEY=VALUE escape hatch (see cli.ParseKeyValues/OverlayKeyValues).
		rawSetFlags []string
	)
	cmd := &cobra.Command{
		Use:   "create <vmid>",
		Short: "Create an LXC container",
		Long: "Create an LXC container on a node from a template volume. The root " +
			"filesystem is given as a PVE option string, e.g. --rootfs " +
			"\"local-lvm:8\", and network interfaces and mount points as repeatable " +
			"--net/--mp slots, e.g. --net 0=name=eth0,bridge=vmbr0,ip=dhcp --mp 0=local-lvm:8,mp=/data.",
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

			params := &nodes.CreateLxcParams{Vmid: vmid, Ostemplate: ostemplate}
			fl := cmd.Flags()
			if fl.Changed("hostname") {
				params.Hostname = &hostname
			}
			if fl.Changed("storage") {
				params.Storage = &storage
			}
			if fl.Changed("rootfs") {
				params.Rootfs = &rootfs
			}
			if fl.Changed("memory") {
				params.Memory = &memory
			}
			if fl.Changed("swap") {
				params.Swap = &swap
			}
			if fl.Changed("cores") {
				params.Cores = &cores
			}
			if fl.Changed("password") {
				params.Password = &password
			}
			if fl.Changed("ssh-public-keys") {
				params.SshPublicKeys = &sshPublicKeys
			}
			if fl.Changed("pool") {
				params.Pool = &pool
			}
			if fl.Changed("tags") {
				params.Tags = &tags
			}
			if fl.Changed("unprivileged") {
				params.Unprivileged = &unprivileged
			}
			if fl.Changed("start") {
				params.Start = &start
			}

			net, err := cli.ParseIndexedValues(netSlots, "net")
			if err != nil {
				return err
			}
			if fl.Changed("net0") {
				if _, dup := net[0]; dup {
					return fmt.Errorf("network slot 0 specified by both --net0 and --net with index 0")
				}
				net[0] = net0
			}
			if len(net) > 0 {
				params.Net = net
			}
			if mp, err := cli.ParseIndexedValues(mpSlots, "mp"); err != nil {
				return err
			} else if len(mp) > 0 {
				params.Mp = mp
			}
			if dev, err := cli.ParseIndexedValues(devSlots, "dev"); err != nil {
				return err
			} else if len(dev) > 0 {
				params.Dev = dev
			}
			if unused, err := cli.ParseIndexedValues(unusedSlots, "unused"); err != nil {
				return err
			} else if len(unused) > 0 {
				params.Unused = unused
			}

			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("nameserver") {
				params.Nameserver = &nameserver
			}
			if fl.Changed("searchdomain") {
				params.Searchdomain = &searchdomain
			}
			if fl.Changed("onboot") {
				params.Onboot = &onboot
			}
			if fl.Changed("startup") {
				params.Startup = &startup
			}
			if fl.Changed("cpulimit") {
				params.Cpulimit = &cpulimit
			}
			if fl.Changed("cpuunits") {
				params.Cpuunits = &cpuunits
			}
			if fl.Changed("arch") {
				params.Arch = &arch
			}
			if fl.Changed("ostype") {
				params.Ostype = &ostype
			}
			if fl.Changed("features") {
				params.Features = &features
			}
			if fl.Changed("hookscript") {
				params.Hookscript = &hookscript
			}
			if fl.Changed("protection") {
				params.Protection = &protection
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("ha-managed") {
				params.HaManaged = &haManaged
			}
			if fl.Changed("timezone") {
				params.Timezone = &timezone
			}
			if fl.Changed("tty") {
				params.Tty = &tty
			}
			if fl.Changed("console") {
				params.Console = &console
			}
			if fl.Changed("cmode") {
				params.Cmode = &cmode
			}
			if fl.Changed("template") {
				params.Template = &template
			}
			if fl.Changed("unique") {
				params.Unique = &unique
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("ignore-unpack-errors") {
				params.IgnoreUnpackErrors = &ignoreUnpackErrors
			}
			if fl.Changed("restore") {
				params.Restore = &restore
			}
			if fl.Changed("env") {
				params.Env = &env
			}
			if fl.Changed("entrypoint") {
				params.Entrypoint = &entrypoint
			}
			if fl.Changed("lock") {
				params.Lock = &lock
			}
			if fl.Changed("debug") {
				params.Debug = &debug
			}

			sets, err := cli.ParseKeyValues(rawSetFlags)
			if err != nil {
				return err
			}

			if len(sets) == 0 {
				resp, err := deps.API.Nodes.CreateLxc(cmd.Context(), node, params)
				if err != nil {
					return fmt.Errorf("create container %d on node %q: %w", vmid, node, err)
				}
				return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %d created.", vmid))
			}

			rawBody, err := cli.ParamsToMap(params)
			if err != nil {
				return fmt.Errorf("build create body for container %d: %w", vmid, err)
			}
			if rawBody, err = cli.OverlayKeyValues(cmd.ErrOrStderr(), rawBody, sets, isKnownConfigKey); err != nil {
				return err
			}
			path := fmt.Sprintf("/nodes/%s/lxc", url.PathEscape(node))
			data, err := deps.API.Raw.PostCtx(cmd.Context(), path, rawBody)
			if err != nil {
				return fmt.Errorf("create container %d on node %q: %w", vmid, node, err)
			}
			raw, err := json.Marshal(data)
			if err != nil {
				return fmt.Errorf("create container %d on node %q: encode task response: %w", vmid, node, err)
			}
			return emitTask(cmd, deps, json.RawMessage(raw), fmt.Sprintf("Container %d created.", vmid))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	f.StringVar(&ostemplate, "ostemplate", "", "template volume (required), e.g. local:vztmpl/...tar.zst")
	f.StringVar(&hostname, "hostname", "", "container hostname")
	f.StringVar(&storage, "storage", "", "default storage for the container")
	f.StringVar(&rootfs, "rootfs", "", "root filesystem spec, e.g. local-lvm:8")
	f.Int64Var(&memory, "memory", 0, "RAM in MiB")
	f.Int64Var(&swap, "swap", 0, "swap in MiB")
	f.Int64Var(&cores, "cores", 0, "CPU cores")
	f.StringVar(&net0, "net0", "", "network device 0, e.g. name=eth0,bridge=vmbr0,ip=dhcp")
	f.StringArrayVar(&netSlots, "net", nil, "network interface as INDEX=VALUE (repeatable), e.g. 0=name=eth0,bridge=vmbr0,ip=dhcp")
	f.StringArrayVar(&mpSlots, "mp", nil, "mount point as INDEX=VALUE (repeatable), e.g. 0=local-lvm:8,mp=/data")
	f.StringArrayVar(&devSlots, "dev", nil, "device passthrough as INDEX=VALUE (repeatable)")
	f.StringVar(&password, "password", "", "root password for the container")
	f.StringVar(&sshPublicKeys, "ssh-public-keys", "", "SSH public keys to install for root")
	f.StringVar(&pool, "pool", "", "resource pool to place the container in")
	f.StringVar(&tags, "tags", "", "comma- or semicolon-separated tags")
	f.BoolVar(&unprivileged, "unprivileged", false, "create an unprivileged container")
	f.BoolVar(&start, "start", false, "start the container immediately after creation")
	f.StringVar(&description, "description", "", "container description shown in the web UI")
	f.StringVar(&nameserver, "nameserver", "", "DNS server IP(s) for the container")
	f.StringVar(&searchdomain, "searchdomain", "", "DNS search domain(s) for the container")
	f.BoolVar(&onboot, "onboot", false, "start the container during host bootup")
	f.StringVar(&startup, "startup", "", "startup/shutdown behavior, e.g. order=1,up=30,down=60")
	f.Float64Var(&cpulimit, "cpulimit", 0, "CPU usage limit (0 = unlimited)")
	f.Int64Var(&cpuunits, "cpuunits", 0, "CPU weight")
	f.StringVar(&arch, "arch", "", "OS architecture type, e.g. amd64 or arm64")
	f.StringVar(&ostype, "ostype", "", "OS type, e.g. debian, alpine, or unmanaged")
	f.StringVar(&features, "features", "", "advanced features, e.g. nesting=1,keyctl=1")
	f.StringVar(&hookscript, "hookscript", "", "hookscript volume run during lifecycle events")
	f.BoolVar(&protection, "protection", false, "set the protection flag to block remove/update")
	f.Float64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	f.BoolVar(&haManaged, "ha-managed", false, "register the container as a HA resource after creation")
	f.StringVar(&timezone, "timezone", "", "time zone, e.g. host or Europe/Berlin")
	f.Int64Var(&tty, "tty", 0, "number of ttys available to the container")
	f.BoolVar(&console, "console", false, "attach a console device (/dev/console)")
	f.StringVar(&cmode, "cmode", "", "console mode: tty, console, or shell")
	f.BoolVar(&template, "template", false, "create the container as a template")
	f.BoolVar(&unique, "unique", false, "assign a unique random ethernet address")
	f.BoolVar(&force, "force", false, "allow overwriting an existing container")
	f.BoolVar(&ignoreUnpackErrors, "ignore-unpack-errors", false, "ignore errors when extracting the template")
	f.BoolVar(&restore, "restore", false, "mark this as a restore task")
	f.StringVar(&env, "env", "", "runtime environment as NUL-separated list")
	f.StringVar(&entrypoint, "entrypoint", "", "command to run as init")
	f.StringVar(&lock, "lock", "", "lock/unlock the container")
	f.BoolVar(&debug, "debug", false, "enable debug log-level on start")
	f.StringArrayVar(&unusedSlots, "unused", nil,
		"unused volume slot as INDEX=VALUE (repeatable; these volumes are normally "+
			"PVE-managed and set during restore, e.g. --unused 0=local-lvm:vm-101-disk-1)")
	f.StringArrayVar(&rawSetFlags, "set", nil,
		"set an arbitrary config option as KEY=VALUE (repeatable); the value is sent to the "+
			"API verbatim. Escape hatch for options that have no dedicated flag yet.")
	cli.MustMarkRequired(cmd, "ostemplate")
	return cmd
}
