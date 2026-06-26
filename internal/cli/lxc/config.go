package lxc

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newConfigCmd builds `pve lxc config` and its get/set/pending sub-commands.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Get or set container configuration",
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigPendingCmd())
	return cmd
}

// newConfigGetCmd builds `pve lxc config get <vmid|name>`.
func newConfigGetCmd() *cobra.Command {
	var snapshot string
	var current bool

	cmd := &cobra.Command{
		Use:   "get <vmid|name>",
		Short: "Show the configuration of a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.ListLxcConfigParams{}
			if snapshot != "" {
				params.Snapshot = &snapshot
			}
			if current {
				params.Current = &current
			}

			resp, err := deps.API.Nodes.ListLxcConfig(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("get config for container %s: %w", vmid, err)
			}

			single, err := structToStringMap(resp)
			if err != nil {
				return err
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&snapshot, "snapshot", "", "fetch config values from the given snapshot")
	cmd.Flags().BoolVar(&current, "current", false, "show current (committed) values instead of pending")
	return cmd
}

// newConfigSetCmd builds `pve lxc config set <vmid|name>`.
func newConfigSetCmd() *cobra.Command {
	var (
		hostname    string
		memory      int64
		swap        int64
		cores       int64
		cpulimit    string
		cpuunits    int64
		ostype      string
		rootfs      string
		description string
		deleteKeys  string
		revertKeys  string

		netSlots     []string
		mpSlots      []string
		devSlots     []string
		unusedSlots  []string
		nameserver   string
		searchdomain string
		onboot       bool
		startup      string
		tags         string
		arch         string
		features     string
		hookscript   string
		protection   bool
		unprivileged bool
		timezone     string
		tty          int64
		console      bool
		cmode        string
		template     bool
		env          string
		entrypoint   string
		lock         string
		digest       string
		debug        bool
	)

	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Update the configuration of a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.UpdateLxcConfigParams{}
			set := false
			fl := cmd.Flags()

			if fl.Changed("hostname") {
				params.Hostname = &hostname
				set = true
			}
			if fl.Changed("memory") {
				params.Memory = &memory
				set = true
			}
			if fl.Changed("swap") {
				params.Swap = &swap
				set = true
			}
			if fl.Changed("cores") {
				params.Cores = &cores
				set = true
			}
			if fl.Changed("cpulimit") {
				v, perr := parseFloatPtr(cpulimit)
				if perr != nil {
					return fmt.Errorf("invalid --cpulimit %q: %w", cpulimit, perr)
				}
				params.Cpulimit = v
				set = true
			}
			if fl.Changed("cpuunits") {
				params.Cpuunits = &cpuunits
				set = true
			}
			if fl.Changed("ostype") {
				params.Ostype = &ostype
				set = true
			}
			if fl.Changed("rootfs") {
				params.Rootfs = &rootfs
				set = true
			}
			if fl.Changed("description") {
				params.Description = &description
				set = true
			}
			if fl.Changed("delete") {
				params.Delete = &deleteKeys
				set = true
			}
			if fl.Changed("revert") {
				params.Revert = &revertKeys
				set = true
			}

			if net, err := cli.ParseIndexedValues(netSlots, "net"); err != nil {
				return err
			} else if len(net) > 0 {
				params.Net = net
				set = true
			}
			if mp, err := cli.ParseIndexedValues(mpSlots, "mp"); err != nil {
				return err
			} else if len(mp) > 0 {
				params.Mp = mp
				set = true
			}
			if dev, err := cli.ParseIndexedValues(devSlots, "dev"); err != nil {
				return err
			} else if len(dev) > 0 {
				params.Dev = dev
				set = true
			}
			if unused, err := cli.ParseIndexedValues(unusedSlots, "unused"); err != nil {
				return err
			} else if len(unused) > 0 {
				params.Unused = unused
				set = true
			}

			if fl.Changed("nameserver") {
				params.Nameserver = &nameserver
				set = true
			}
			if fl.Changed("searchdomain") {
				params.Searchdomain = &searchdomain
				set = true
			}
			if fl.Changed("onboot") {
				params.Onboot = &onboot
				set = true
			}
			if fl.Changed("startup") {
				params.Startup = &startup
				set = true
			}
			if fl.Changed("tags") {
				params.Tags = &tags
				set = true
			}
			if fl.Changed("arch") {
				params.Arch = &arch
				set = true
			}
			if fl.Changed("features") {
				params.Features = &features
				set = true
			}
			if fl.Changed("hookscript") {
				params.Hookscript = &hookscript
				set = true
			}
			if fl.Changed("protection") {
				params.Protection = &protection
				set = true
			}
			if fl.Changed("unprivileged") {
				params.Unprivileged = &unprivileged
				set = true
			}
			if fl.Changed("timezone") {
				params.Timezone = &timezone
				set = true
			}
			if fl.Changed("tty") {
				params.Tty = &tty
				set = true
			}
			if fl.Changed("console") {
				params.Console = &console
				set = true
			}
			if fl.Changed("cmode") {
				params.Cmode = &cmode
				set = true
			}
			if fl.Changed("template") {
				params.Template = &template
				set = true
			}
			if fl.Changed("env") {
				params.Env = &env
				set = true
			}
			if fl.Changed("entrypoint") {
				params.Entrypoint = &entrypoint
				set = true
			}
			if fl.Changed("lock") {
				params.Lock = &lock
				set = true
			}
			if fl.Changed("digest") {
				params.Digest = &digest
				set = true
			}
			if fl.Changed("debug") {
				params.Debug = &debug
				set = true
			}

			if !set {
				return fmt.Errorf("no configuration fields given: specify at least one --hostname/--memory/--cores/... flag")
			}

			if err := deps.API.Nodes.UpdateLxcConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update config for container %s: %w", vmid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Container %s config updated.", vmid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&hostname, "hostname", "", "set the container hostname")
	fl.Int64Var(&memory, "memory", 0, "amount of RAM in MB")
	fl.Int64Var(&swap, "swap", 0, "amount of SWAP in MB")
	fl.Int64Var(&cores, "cores", 0, "number of CPU cores")
	fl.StringVar(&cpulimit, "cpulimit", "", "CPU usage limit (0 = unlimited)")
	fl.Int64Var(&cpuunits, "cpuunits", 0, "CPU weight")
	fl.StringVar(&ostype, "ostype", "", "OS type")
	fl.StringVar(&rootfs, "rootfs", "", "root filesystem volume")
	fl.StringVar(&description, "description", "", "container description")
	fl.StringVar(&deleteKeys, "delete", "", "comma-separated list of settings to delete")
	fl.StringVar(&revertKeys, "revert", "", "comma-separated list of pending changes to revert")
	fl.StringArrayVar(&netSlots, "net", nil, "network interface as INDEX=VALUE (repeatable), e.g. 0=name=eth0,bridge=vmbr0,ip=dhcp")
	fl.StringArrayVar(&mpSlots, "mp", nil, "mount point as INDEX=VALUE (repeatable), e.g. 0=local-lvm:8,mp=/data")
	fl.StringArrayVar(&devSlots, "dev", nil, "device passthrough as INDEX=VALUE (repeatable)")
	fl.StringArrayVar(&unusedSlots, "unused", nil,
		"unused volume slot as INDEX=VALUE (repeatable; volumes are normally PVE-managed "+
			"and appear here after disk removal or restore, e.g. --unused 0=local-lvm:vm-101-disk-1)")
	fl.StringVar(&nameserver, "nameserver", "", "DNS server IP(s) for the container")
	fl.StringVar(&searchdomain, "searchdomain", "", "DNS search domain(s) for the container")
	fl.BoolVar(&onboot, "onboot", false, "start the container during host bootup")
	fl.StringVar(&startup, "startup", "", "startup/shutdown behavior, e.g. order=1,up=30,down=60")
	fl.StringVar(&tags, "tags", "", "comma- or semicolon-separated tags")
	fl.StringVar(&arch, "arch", "", "OS architecture type, e.g. amd64 or arm64")
	fl.StringVar(&features, "features", "", "advanced features, e.g. nesting=1,keyctl=1")
	fl.StringVar(&hookscript, "hookscript", "", "hookscript volume run during lifecycle events")
	fl.BoolVar(&protection, "protection", false, "set the protection flag to block remove/update")
	fl.BoolVar(&unprivileged, "unprivileged", false, "run the container as an unprivileged user")
	fl.StringVar(&timezone, "timezone", "", "time zone, e.g. host or Europe/Berlin")
	fl.Int64Var(&tty, "tty", 0, "number of ttys available to the container")
	fl.BoolVar(&console, "console", false, "attach a console device (/dev/console)")
	fl.StringVar(&cmode, "cmode", "", "console mode: tty, console, or shell")
	fl.BoolVar(&template, "template", false, "mark the container as a template")
	fl.StringVar(&env, "env", "", "runtime environment as NUL-separated list")
	fl.StringVar(&entrypoint, "entrypoint", "", "command to run as init")
	fl.StringVar(&lock, "lock", "", "lock/unlock the container")
	fl.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	fl.BoolVar(&debug, "debug", false, "enable debug log-level on start")
	return cmd
}

// lxcPendingEntry is one element from the ListLxcPending response array. Each
// element holds the current committed value and any pending (next-reboot) value
// for a single config key.
type lxcPendingEntry struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Pending any    `json:"pending"`
	Delete  int    `json:"delete"`
}

// newConfigPendingCmd builds `pve lxc config pending <vmid|name>`.
//
// Returns the diff between the currently committed configuration and any
// changes that take effect after the next container restart.
func newConfigPendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pending <vmid|name>",
		Short: "Show pending configuration changes for a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListLxcPending(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get pending config for container %s on node %q: %w", vmid, node, err)
			}

			headers := []string{"KEY", "VALUE", "PENDING-VALUE"}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e lxcPendingEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode pending config entry: %w", err)
					}
					rows = append(rows, []string{
						e.Key,
						fmt.Sprintf("%v", e.Value),
						fmt.Sprintf("%v", e.Pending),
					})
				}
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// structToStringMap marshals a typed response struct or decoded value to a flat
// string map for key-value rendering, skipping empty/nil fields. It is shared by
// the config, console, and firewall renderers, so its errors name the response
// generically rather than any one endpoint.
func structToStringMap(v any) (map[string]string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	out := make(map[string]string, len(generic))
	for k, val := range generic {
		if val == nil {
			continue
		}
		s := fmt.Sprintf("%v", val)
		if s == "" {
			continue
		}
		out[k] = s
	}
	return out, nil
}

// parseFloatPtr converts a string flag value to a *float64, returning an error
// on a parse failure so an invalid value is surfaced rather than silently
// dropped from the request.
func parseFloatPtr(s string) (*float64, error) {
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return nil, err
	}
	return &f, nil
}
