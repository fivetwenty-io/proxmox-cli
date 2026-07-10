package lxc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
)

// newConfigCmd builds `pmx lxc config` and its get/set/pending/describe sub-commands.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Get or set container configuration",
		Long: "Read a container's current or pending configuration, apply changes to it, and " +
			"browse the offline catalog of every settable option with 'describe'.",
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigPendingCmd(), newConfigDescribeCmd())
	return cmd
}

// newConfigDescribeCmd builds `pmx lxc config describe`, an offline catalog of
// every settable container configuration option from the PVE API schema (see
// config_schema_gen.go).
func newConfigDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: configSchemas,
		Short:   "Describe all settable container configuration options and their defaults",
		Long: "List every settable container configuration option from the PVE API schema: " +
			"type, built-in default, allowed values, and (for a single option) the " +
			"sub-keys of dict-encoded options. Runs offline. Pass an option name to " +
			"show only that option with full descriptions and sub-keys.",
		CommandHint:         "pmx pve lxc config describe",
		SubKeyRowsInCatalog: false,
	})
}

// newConfigGetCmd builds `pmx lxc config get <vmid|name>`.
func newConfigGetCmd() *cobra.Command {
	var snapshot string
	var current bool
	var withDefaults bool

	cmd := &cobra.Command{
		Use:   "get <vmid|name>",
		Short: "Show the configuration of a container",
		Long: "Show the configuration currently set on a container. The PVE API omits " +
			"options left at their built-in defaults; pass --defaults to also list those " +
			"with the value they effectively have.",
		Args: cobra.ExactArgs(1),
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

			var raw any = resp
			if withDefaults {
				single, raw = optionschema.MergeDefaults(configSchemas, single, resp, optionschema.MergeOpts{SkipUnset: true})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&snapshot, "snapshot", "", "fetch config values from the given snapshot")
	cmd.Flags().BoolVar(&current, "current", false, "show current (committed) values instead of pending")
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"also list unset options with their built-in default values")
	return cmd
}

// newConfigSetCmd builds `pmx lxc config set <vmid|name>`.
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

		// --set KEY=VALUE escape hatch (see cli.ParseKeyValues/OverlayKeyValues).
		rawSetFlags []string
	)

	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Update the configuration of a container",
		Long: "Update one or more settings on a container's configuration. Only the flags you " +
			"pass are changed; unspecified options keep their current value. Use --delete to " +
			"reset options to their defaults, --revert to discard specific pending changes, " +
			"and --set KEY=VALUE as an escape hatch for options with no dedicated flag. At " +
			"least one field must be given.",
		Example: `  pmx pve lxc config set 200 --memory 2048 --cores 4
  pmx pve lxc config set web1 --hostname web1 --onboot --tags prod`,
		Args: cobra.ExactArgs(1),
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

			sets, err := cli.ParseKeyValues(rawSetFlags)
			if err != nil {
				return err
			}

			var rawBody map[string]any
			if len(sets) > 0 {
				set = true
				rawBody, err = cli.ParamsToMap(params)
				if err != nil {
					return fmt.Errorf("build config update body for container %s: %w", vmid, err)
				}
				if rawBody, err = cli.OverlayKeyValues(cmd.ErrOrStderr(), rawBody, sets, isKnownConfigKey); err != nil {
					return err
				}
			}

			if !set {
				return fmt.Errorf("no configuration fields given: specify at least one --hostname/--memory/--cores/... flag")
			}

			if len(sets) == 0 {
				if err := deps.API.Nodes.UpdateLxcConfig(cmd.Context(), node, vmid, params); err != nil {
					return fmt.Errorf("update config for container %s: %w", vmid, err)
				}
			} else {
				path := fmt.Sprintf("/nodes/%s/lxc/%s/config", url.PathEscape(node), url.PathEscape(vmid))
				if _, err := deps.API.Raw.PutCtx(cmd.Context(), path, rawBody); err != nil {
					return fmt.Errorf("update config for container %s: %w", vmid, err)
				}
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
	fl.StringVar(&cmode, "cmode", "", "console mode")
	fl.BoolVar(&template, "template", false, "mark the container as a template")
	fl.StringVar(&env, "env", "", "runtime environment as NUL-separated list")
	fl.StringVar(&entrypoint, "entrypoint", "", "command to run as init")
	fl.StringVar(&lock, "lock", "", "lock/unlock the container")
	fl.StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	fl.BoolVar(&debug, "debug", false, "enable debug log-level on start")
	fl.StringArrayVar(&rawSetFlags, "set", nil,
		"set an arbitrary config option as KEY=VALUE (repeatable); the value is sent to the "+
			"API verbatim. Escape hatch for options that have no dedicated flag yet.")

	// Append generated schema detail (allowed values, defaults, sub-keys) to
	// each option flag's help text; see config_schema_gen.go.
	optionschema.EnrichFlags(fl, configSchemas)
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
// digits, used to recognize indexed config keys such as "net0" or "mp12".
func isASCIIDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

// newConfigPendingCmd builds `pmx lxc config pending <vmid|name>`.
//
// Returns the diff between the currently committed configuration and any
// changes that take effect after the next container restart.
func newConfigPendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pending <vmid|name>",
		Short: "Show pending configuration changes for a container",
		Long: "Show the difference between a container's currently committed configuration " +
			"and any changes staged to take effect after its next restart, as the current " +
			"VALUE and PENDING-VALUE for each changed KEY.",
		Example: `  pmx pve lxc config pending 200
  pmx pve lxc config pending web1`,
		Args: cobra.ExactArgs(1),
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
