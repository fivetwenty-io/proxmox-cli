package qemu

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newConfigCmd builds the `pve qemu config` sub-group.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and modify VM configuration",
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigPendingCmd())
	return cmd
}

// newConfigGetCmd builds `pve qemu config get <vmid>`.
//
// The raw API response is read directly so that dynamically named disk and
// network keys (net0, scsi0, ide0, …) are preserved; the generated typed struct
// only models statically named fields.
func newConfigGetCmd() *cobra.Command {
	var (
		current  bool
		snapshot string
	)
	cmd := &cobra.Command{
		Use:   "get <vmid>",
		Short: "Show the configuration of a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

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

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: data}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&current, "current", false, "get current values instead of pending values")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "fetch config values from the given snapshot")
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

// newConfigSetCmd builds `pve qemu config set <vmid>`.
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
		net0        string
		net1        string
		scsi0       string
		scsi1       string
		ide0        string
		ide2        string
		virtio0     string
		virtio1     string
		agent       string
		onboot      bool
		startup     string

		ciuser       string
		cipassword   string
		citype       string
		ciupgrade    bool
		cicustom     string
		nameserver   string
		searchdomain string
		sshkeys      string
		ipconfig0    string
		ipconfig1    string
	)
	cmd := &cobra.Command{
		Use:   "set <vmid>",
		Short: "Update the configuration of a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

			params := &nodes.UpdateQemuConfigParams{}
			changed := false
			set := func(name string, apply func()) {
				if cmd.Flags().Changed(name) {
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

			// Cloud-init scalars (mirror `qemu create`).
			set("ciuser", func() { params.Ciuser = strPtr(ciuser) })
			set("cipassword", func() { params.Cipassword = strPtr(cipassword) })
			set("citype", func() { params.Citype = strPtr(citype) })
			set("ciupgrade", func() { params.Ciupgrade = boolPtr(ciupgrade) })
			set("cicustom", func() { params.Cicustom = strPtr(cicustom) })
			set("nameserver", func() { params.Nameserver = strPtr(nameserver) })
			set("searchdomain", func() { params.Searchdomain = strPtr(searchdomain) })
			// PVE requires the sshkeys value percent-encoded; it uri_unescapes
			// %XX but does NOT treat '+' as space, so encode space as %20.
			set("sshkeys", func() {
				params.Sshkeys = strPtr(strings.ReplaceAll(url.QueryEscape(sshkeys), "+", "%20"))
			})

			// Indexed device + ipconfig maps. Accumulate each changed slot so
			// multiple indices (e.g. net0 + net1) coexist in a single request;
			// the apiclient marshals map[int]string into net0, net1, … keys.
			net := map[int]string{}
			set("net0", func() { net[0] = net0 })
			set("net1", func() { net[1] = net1 })
			if len(net) > 0 {
				params.Net = net
			}
			scsi := map[int]string{}
			set("scsi0", func() { scsi[0] = scsi0 })
			set("scsi1", func() { scsi[1] = scsi1 })
			if len(scsi) > 0 {
				params.Scsi = scsi
			}
			ide := map[int]string{}
			set("ide0", func() { ide[0] = ide0 })
			set("ide2", func() { ide[2] = ide2 })
			if len(ide) > 0 {
				params.Ide = ide
			}
			virtio := map[int]string{}
			set("virtio0", func() { virtio[0] = virtio0 })
			set("virtio1", func() { virtio[1] = virtio1 })
			if len(virtio) > 0 {
				params.Virtio = virtio
			}
			ipconfig := map[int]string{}
			set("ipconfig0", func() { ipconfig[0] = ipconfig0 })
			set("ipconfig1", func() { ipconfig[1] = ipconfig1 })
			if len(ipconfig) > 0 {
				params.Ipconfig = ipconfig
			}

			if !changed {
				return fmt.Errorf("no configuration changes specified: pass at least one --<key> flag")
			}

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update config for VM %s on node %q: %w", vmid, node, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("VM %s config updated.", vmid)}, deps.Format)
		},
	}

	cmd.Flags().Int64Var(&cores, "cores", 0, "number of CPU cores")
	cmd.Flags().StringVar(&memory, "memory", "", "memory in MiB")
	cmd.Flags().Int64Var(&balloon, "balloon", 0, "target balloon memory in MiB (0 disables ballooning)")
	cmd.Flags().StringVar(&name, "name", "", "VM name")
	cmd.Flags().StringVar(&description, "description", "", "VM description")
	cmd.Flags().StringVar(&boot, "boot", "", "boot order specification")
	cmd.Flags().StringVar(&scsihw, "scsihw", "", "SCSI controller model")
	cmd.Flags().StringVar(&cpu, "cpu", "", "CPU type")
	cmd.Flags().StringVar(&ostype, "ostype", "", "guest OS type")
	cmd.Flags().StringVar(&agent, "agent", "", "QEMU guest-agent option string, e.g. 1 or enabled=1,fstrim_cloned_disks=1")
	cmd.Flags().BoolVar(&onboot, "onboot", false, "start the VM automatically on host boot")
	cmd.Flags().StringVar(&startup, "startup", "", "startup/shutdown behavior, e.g. order=1,up=30,down=60")
	cmd.Flags().StringVar(&deleteKeys, "delete", "", "comma-separated config keys to remove")
	cmd.Flags().StringVar(&revertKeys, "revert", "", "comma-separated pending config keys to revert")
	cmd.Flags().StringVar(&net0, "net0", "", "network device net0 specification")
	cmd.Flags().StringVar(&net1, "net1", "", "network device net1 specification")
	cmd.Flags().StringVar(&scsi0, "scsi0", "", "SCSI disk scsi0 specification")
	cmd.Flags().StringVar(&scsi1, "scsi1", "", "SCSI disk scsi1 specification")
	cmd.Flags().StringVar(&ide0, "ide0", "", "IDE disk ide0 specification")
	cmd.Flags().StringVar(&ide2, "ide2", "", "IDE device ide2, e.g. <storage>:cloudinit for the cloud-init drive")
	cmd.Flags().StringVar(&virtio0, "virtio0", "", "VirtIO disk virtio0 specification")
	cmd.Flags().StringVar(&virtio1, "virtio1", "", "VirtIO disk virtio1 specification, e.g. <storage>:32 to allocate a data disk")
	cmd.Flags().StringVar(&ciuser, "ciuser", "", "cloud-init: default user to configure")
	cmd.Flags().StringVar(&cipassword, "cipassword", "", "cloud-init: password for the default user")
	cmd.Flags().StringVar(&citype, "citype", "", "cloud-init: config format, e.g. nocloud or configdrive2")
	cmd.Flags().BoolVar(&ciupgrade, "ciupgrade", false, "cloud-init: run a package upgrade on first boot")
	cmd.Flags().StringVar(&cicustom, "cicustom", "", "cloud-init: custom config files, e.g. user=local:snippets/user.yml")
	cmd.Flags().StringVar(&nameserver, "nameserver", "", "cloud-init: DNS server IP address")
	cmd.Flags().StringVar(&searchdomain, "searchdomain", "", "cloud-init: DNS search domain")
	cmd.Flags().StringVar(&sshkeys, "sshkeys", "", "cloud-init: public SSH keys (one per line, OpenSSH format)")
	cmd.Flags().StringVar(&ipconfig0, "ipconfig0", "", "cloud-init: IP config for net0, e.g. ip=dhcp or ip=10.0.0.5/24,gw=10.0.0.1")
	cmd.Flags().StringVar(&ipconfig1, "ipconfig1", "", "cloud-init: IP config for net1, e.g. ip=10.43.0.5/24")
	return cmd
}

// pendingEntry is the minimal decoded shape of one entry from nodes.ListQemuPending.
type pendingEntry struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Pending any    `json:"pending"`
}

// newConfigPendingCmd builds `pve qemu config pending <vmid>`.
func newConfigPendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pending <vmid>",
		Short: "Show pending configuration changes for a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]

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
