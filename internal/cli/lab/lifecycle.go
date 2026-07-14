package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/peppi"
	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
)

// labVM is the decoded shape of one /cluster/resources entry of type "vm"
// that this file cares about: enough to join a configured lab against its
// live QEMU guest by resource-pool membership. The endpoint also returns
// lxc guests under type=vm; Type is kept so callers can filter those out.
type labVM struct {
	VMID   int64  `json:"vmid"`
	Node   string `json:"node"`
	Pool   string `json:"pool"`
	Status string `json:"status"`
	Type   string `json:"type"`
}

// listLiveVMs queries GET /cluster/resources for every QEMU guest in the
// cluster, across every node, so list/status/start/stop can each join a
// configured lab against its live VM by resource-pool membership rather than
// by a stored VMID (labs carry no VMID field in config). The
// node-scoped GET /nodes/{node}/qemu endpoint is not used here because it
// would require already knowing which node a lab's VM lives on.
func listLiveVMs(ctx context.Context, deps *cli.Deps) ([]labVM, error) {
	typeVM := "vm"
	resp, err := deps.API.Cluster.ListResources(ctx, &pvecluster.ListResourcesParams{Type: &typeVM})
	if err != nil {
		return nil, fmt.Errorf("list cluster VMs: %w", err)
	}
	if resp == nil {
		return nil, nil
	}

	vms := make([]labVM, 0, len(*resp))
	for _, raw := range *resp {
		var vm labVM
		if err := json.Unmarshal(raw, &vm); err != nil {
			return nil, fmt.Errorf("decode cluster resource entry: %w", err)
		}
		// cluster/resources type=vm returns both qemu and lxc guests; a lab's
		// VM is always qemu, never lxc.
		if vm.Type != "qemu" {
			continue
		}
		vms = append(vms, vm)
	}
	return vms, nil
}

// findLabVM returns the first qemu guest in vms that is a member of pool, or
// false if none is. Two VMs sharing one lab's pool would be a data problem
// upstream of this lookup (pool assignment, not resolution); this returns
// whichever is listed first by the API rather than guessing further.
func findLabVM(vms []labVM, pool string) (labVM, bool) {
	for _, vm := range vms {
		if vm.Pool == pool {
			return vm, true
		}
	}
	return labVM{}, false
}

// guardVMID re-runs peppi.Guard now that a concrete VMID has been resolved
// for lab, per resolve.go's resolveLabForMutate contract: every mutating
// verb that subsequently learns a lab's VMID (as start/stop do, by locating
// the live VM in the lab's pool) must guard again with that VMID before
// issuing any mutating call against it.
func guardVMID(lab *config.Lab, vmid int64) error {
	return peppi.Guard(peppi.Target{
		VMID: int(vmid),
		Names: []string{
			lab.Network.VnetID,
			labPoolID(lab),
			storageID(lab),
			lab.DNS.Zone,
			lab.Name,
		},
	})
}

// newListCmd builds `pmx lab list`.
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every configured lab and its live VM state",
		Long: "List every lab defined in config (inline `labs:` plus `labs_dir`/`include` " +
			"includes), joined with the live state of its VM in the configured PVE " +
			"cluster: present or absent, running or stopped, VMID, and node. A lab whose " +
			"VM has not been created yet, or was destroyed, shows an absent state rather " +
			"than an error.",
		Example: `  pmx lab list
  pmx lab list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			labs, err := config.ResolveLabs(deps.Cfg, deps.ConfigPath)
			if err != nil {
				return fmt.Errorf("resolve labs: %w", err)
			}

			vms, err := listLiveVMs(cmd.Context(), deps)
			if err != nil {
				return err
			}

			names := make([]string, 0, len(labs))
			for name := range labs {
				names = append(names, name)
			}
			sort.Strings(names)

			headers := []string{"NAME", "POOL", "VMID", "NODE", "STATUS"}
			rows := make([][]string, 0, len(names))

			for _, name := range names {
				l := labs[name]
				pool := labPoolID(l)

				vmidStr, node, status := "", "", "absent"
				if vm, found := findLabVM(vms, pool); found {
					vmidStr = strconv.FormatInt(vm.VMID, 10)
					node = vm.Node
					status = vm.Status
				}

				rows = append(rows, []string{name, pool, vmidStr, node, status})
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Headers: headers, Rows: rows}, deps.Format)
		},
	}
}

// diskKeyPrefixes are the PVE VM config key families that address a virtual
// disk: "scsi[n]", "virtio[n]", "ide[n]", "sata[n]" (n is a small integer).
// The generated nodes.ListQemuConfigResponse struct cannot represent these
// dynamically indexed keys — its fields for them are literal placeholders
// (e.g. a field tagged json:"scsi[n]") that never match a real response key
// such as "scsi0"; see qemu/config.go's newConfigGetCmd doc comment for the
// same limitation against the same generated type. Status therefore reads
// the raw decoded config map via deps.API.Raw.GetCtx instead of ListQemuConfig.
var diskKeyPrefixes = []string{"scsi", "virtio", "ide", "sata"}

// isDiskKey reports whether key is a disk-slot config key such as "scsi0" or
// "virtio15": one of diskKeyPrefixes followed by one or more decimal digits.
func isDiskKey(key string) bool {
	for _, prefix := range diskKeyPrefixes {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := key[len(prefix):]
		if rest == "" {
			continue
		}
		if _, err := strconv.Atoi(rest); err == nil {
			return true
		}
	}
	return false
}

// stringifyConfigValue renders one JSON-decoded config value (from the raw
// map returned by GetCtx) as a display string, the same conversions
// qemu/config.go's stringifyValue applies to the same kind of decoded data.
func stringifyConfigValue(v any) string {
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

// applyLiveConfigSurface reads cores, sockets, memory, and every disk-slot
// key out of a decoded raw VM config map (as returned by GetCtx) and writes
// them into single, upper-cased to match the rest of the status output's
// key style.
func applyLiveConfigSurface(single map[string]string, rawConfig any) error {
	m, ok := rawConfig.(map[string]any)
	if !ok {
		return fmt.Errorf("decode VM config: unexpected response shape %T", rawConfig)
	}

	for _, key := range []string{"cores", "sockets", "memory"} {
		if v, present := m[key]; present {
			single[strings.ToUpper(key)] = stringifyConfigValue(v)
		}
	}

	diskKeys := make([]string, 0)
	for k := range m {
		if isDiskKey(k) {
			diskKeys = append(diskKeys, k)
		}
	}
	sort.Strings(diskKeys)
	for _, k := range diskKeys {
		single[strings.ToUpper(k)] = stringifyConfigValue(m[k])
	}

	return nil
}

// applyLabConfigSurface writes a lab's config-derived compute and storage
// sizing into single, for the "VM absent" branch of status where no live
// PVE config exists yet to read from.
func applyLabConfigSurface(single map[string]string, l *config.Lab) {
	single["VCPU"] = strconv.Itoa(l.Compute.VCPU)
	single["MEMORY_MIN_GB"] = strconv.Itoa(l.Compute.Memory.MinGB)
	single["MEMORY_MAX_GB"] = strconv.Itoa(l.Compute.Memory.MaxGB)
	single["OS_DISK_GB"] = strconv.Itoa(l.Storage.OSDiskGB)
	single["DATA_DISK_GB"] = strconv.Itoa(l.Storage.DataDiskGB)
}

// agentNetworkInterfaces is the decoded shape of the QEMU guest agent's
// "network-get-interfaces" QMP command result.
type agentNetworkInterfaces struct {
	Result []struct {
		Name        string `json:"name"`
		IPAddresses []struct {
			IPAddress     string `json:"ip-address"`
			IPAddressType string `json:"ip-address-type"`
		} `json:"ip-addresses"`
	} `json:"result"`
}

// guestAgentIP asks the QEMU guest agent for the VM's network interfaces and
// returns the first non-loopback IPv4 address found. It returns ("", false)
// on any failure — agent not installed, not running, or the call erroring —
// rather than propagating an error, since a missing guest agent is an
// expected, non-fatal state that status falls back from to the lab's
// configured management IP.
func guestAgentIP(ctx context.Context, deps *cli.Deps, node, vmid string) (string, bool) {
	resp, err := deps.API.Nodes.ListQemuAgentNetworkGetInterfaces(ctx, node, vmid)
	if err != nil || resp == nil {
		return "", false
	}

	var parsed agentNetworkInterfaces
	if err := json.Unmarshal(*resp, &parsed); err != nil {
		return "", false
	}

	for _, iface := range parsed.Result {
		if iface.Name == "lo" {
			continue
		}
		for _, addr := range iface.IPAddresses {
			if addr.IPAddressType == "ipv4" && addr.IPAddress != "" {
				return addr.IPAddress, true
			}
		}
	}
	return "", false
}

// newStatusCmd builds `pmx lab status <name>`.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show a lab's power state, IP, and key configuration",
		Long: "Show a lab's current power state, IP address, and key compute/storage " +
			"configuration. The IP is read from the QEMU guest agent when the VM is " +
			"running and the agent responds; otherwise the lab's configured management " +
			"IP (network.mgmt.host_ip) is shown instead. A lab whose VM has not been " +
			"created yet is reported as absent, with its config-derived sizing shown in " +
			"place of live values, and exits successfully rather than erroring. Pass " +
			"--node to use a specific node instead of the one the cluster reports.",
		Example: `  pmx lab status wayne
  pmx lab status wayne --node pve2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			l, err := resolveLab(cmd, name)
			if err != nil {
				return err
			}

			vms, err := listLiveVMs(cmd.Context(), deps)
			if err != nil {
				return err
			}

			pool := labPoolID(l)
			single := map[string]string{
				"NAME": l.Name,
				"POOL": pool,
			}

			vm, found := findLabVM(vms, pool)
			if !found {
				single["STATUS"] = "absent"
				applyLabConfigSurface(single, l)
				return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single}, deps.Format)
			}

			node := vm.Node
			if cmd.Flags().Changed("node") && deps.Node != "" {
				node = deps.Node
			}
			vmid := strconv.FormatInt(vm.VMID, 10)

			single["VMID"] = vmid
			single["NODE"] = node

			current, err := deps.API.Nodes.ListQemuStatusCurrent(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get status for lab %q VM %s on node %q: %w", name, vmid, node, err)
			}
			if current == nil {
				return fmt.Errorf("get status for lab %q VM %s on node %q: empty response", name, vmid, node)
			}
			single["STATUS"] = current.Status

			ip := l.Network.Mgmt.HostIP
			if current.Status == "running" {
				if liveIP, ok := guestAgentIP(cmd.Context(), deps, node, vmid); ok {
					ip = liveIP
				}
			}
			single["IP"] = ip

			configPath := fmt.Sprintf("/nodes/%s/qemu/%s/config", url.PathEscape(node), url.PathEscape(vmid))
			rawConfig, err := deps.API.Raw.GetCtx(cmd.Context(), configPath, nil)
			if err != nil {
				return fmt.Errorf("get config for lab %q VM %s on node %q: %w", name, vmid, node, err)
			}
			if err := applyLiveConfigSurface(single, rawConfig); err != nil {
				return fmt.Errorf("get config for lab %q VM %s on node %q: %w", name, vmid, node, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single}, deps.Format)
		},
	}
}

// lifecycleAction describes one of the start/stop verbs: the state that
// makes the action a no-op, its human-readable verb forms, and the API call
// that performs it.
type lifecycleAction struct {
	// verb is the present-tense verb used in dry-run and error messages
	// ("start", "stop").
	verb string
	// pastVerb is the past-tense verb used in the success message
	// ("started", "stopped").
	pastVerb string
	// noopState is the PVE power state ("running", "stopped") in which this
	// action is already satisfied and must not be repeated.
	noopState string
	// invoke issues the actual mutating API call and returns its raw task
	// response — normally a JSON-encoded UPID string, but possibly null or
	// empty on older servers.
	invoke func(ctx context.Context, deps *cli.Deps, node, vmid string) (json.RawMessage, error)
}

// runLifecycleMutate implements the shared start/stop control flow:
// resolve-and-guard the lab, locate its live VM by pool, guard again now
// that a concrete VMID is known, skip idempotently if the action is already
// satisfied, preview under --dry-run, or else invoke the action and block
// on its task.
func runLifecycleMutate(cmd *cobra.Command, name string, dryRun bool, action lifecycleAction) error {
	deps := cli.GetDeps(cmd)

	l, err := resolveLabForMutate(cmd, name)
	if err != nil {
		return err
	}

	vms, err := listLiveVMs(cmd.Context(), deps)
	if err != nil {
		return err
	}

	pool := labPoolID(l)
	vm, found := findLabVM(vms, pool)
	if !found {
		return fmt.Errorf("lab %q: no VM found in pool %q; run `pmx lab create %s` first", name, pool, name)
	}

	if err := guardVMID(l, vm.VMID); err != nil {
		return err
	}

	vmid := strconv.FormatInt(vm.VMID, 10)

	current, err := deps.API.Nodes.ListQemuStatusCurrent(cmd.Context(), vm.Node, vmid)
	if err != nil {
		return fmt.Errorf("get status for lab %q VM %s on node %q: %w", name, vmid, vm.Node, err)
	}
	if current == nil {
		return fmt.Errorf("get status for lab %q VM %s on node %q: empty response", name, vmid, vm.Node)
	}

	if current.Status == action.noopState {
		msg := fmt.Sprintf("Lab %q VM %s on node %q is already %s; nothing to do.", name, vmid, vm.Node, action.noopState)
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
	}

	if dryRun {
		msg := fmt.Sprintf("[dry-run] would %s lab %q VM %s on node %q (currently %s).",
			action.verb, name, vmid, vm.Node, current.Status)
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
	}

	raw, err := action.invoke(cmd.Context(), deps, vm.Node, vmid)
	if err != nil {
		return err
	}

	// Mirror applySdn's immediate-vs-async response handling: a UPID is
	// awaited; a null/empty/non-UPID body from an older server is treated as
	// an immediate success rather than a failure — the mutating call itself
	// already succeeded by this point.
	upid, perr := apiclient.UPIDFromRaw(raw)
	if perr == nil && upid != "" {
		if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
			return err
		}
	}

	msg := fmt.Sprintf("Lab %q VM %s %s.", name, vmid, action.pastVerb)
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// newStartCmd builds `pmx lab start <name>`.
func newStartCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a lab's VM",
		Long: "Start a lab's VM, locating it by the lab's configured resource pool. " +
			"Idempotent: a VM that is already running is reported as a no-op rather than " +
			"re-issuing the start call. Refuses to act when the lab's identifiers, or its " +
			"live VM's ID, overlap a peppi-protected production resource. Blocks until the " +
			"PVE task completes.",
		Example: `  pmx lab start wayne
  pmx lab start wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLifecycleMutate(cmd, args[0], dryRun, lifecycleAction{
				verb:      "start",
				pastVerb:  "started",
				noopState: "running",
				invoke: func(ctx context.Context, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
					resp, err := deps.API.Nodes.CreateQemuStatusStart(ctx, node, vmid, &nodes.CreateQemuStatusStartParams{})
					if err != nil {
						return nil, fmt.Errorf("start VM %s on node %q: %w", vmid, node, err)
					}
					if resp == nil {
						return nil, nil
					}
					return json.RawMessage(*resp), nil
				},
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the intended action without starting the VM")
	return cmd
}

// newStopCmd builds `pmx lab stop <name>`.
func newStopCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a lab's VM (hard power off)",
		Long: "Immediately power off a lab's running VM without asking the guest OS to shut " +
			"down cleanly, similar to pulling the power. Locates the VM by the lab's " +
			"configured resource pool. Idempotent: a VM that is already stopped is reported " +
			"as a no-op rather than re-issuing the stop call. Refuses to act when the lab's " +
			"identifiers, or its live VM's ID, overlap a peppi-protected production " +
			"resource. Blocks until the PVE task completes.",
		Example: `  pmx lab stop wayne
  pmx lab stop wayne --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLifecycleMutate(cmd, args[0], dryRun, lifecycleAction{
				verb:      "stop",
				pastVerb:  "stopped",
				noopState: "stopped",
				invoke: func(ctx context.Context, deps *cli.Deps, node, vmid string) (json.RawMessage, error) {
					resp, err := deps.API.Nodes.CreateQemuStatusStop(ctx, node, vmid, &nodes.CreateQemuStatusStopParams{})
					if err != nil {
						return nil, fmt.Errorf("stop VM %s on node %q: %w", vmid, node, err)
					}
					if resp == nil {
						return nil, nil
					}
					return json.RawMessage(*resp), nil
				},
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the intended action without stopping the VM")
	return cmd
}
