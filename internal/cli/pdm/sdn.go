package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	pdmsdn "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/sdn"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// validSdnControllerTypes are the controller-type enum values accepted by
// --ty on `sdn controller ls` (GET /sdn/controllers), per the PDM API schema.
var validSdnControllerTypes = []string{"bgp", "evpn", "faucet", "isis"}

// validSdnZoneTypes are the zone-type enum values accepted by --ty on
// `sdn zone ls` (GET /sdn/zones), per the PDM API schema.
var validSdnZoneTypes = []string{"evpn", "faucet", "qinq", "simple", "vlan", "vxlan"}

// newSdnCmd builds `pmx pdm sdn` — query SDN controllers, VNets, and zones
// aggregated across every managed remote, and create new VNets and zones
// spanning multiple remotes in one call (/sdn).
func newSdnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdn",
		Short: "Inspect and manage aggregated SDN configuration",
		Long: "Query SDN controllers, VNets, and zones across every managed remote, and create " +
			"new VNets and zones spanning multiple remotes.",
	}
	cmd.AddCommand(newSdnControllerCmd(), newSdnVnetCmd(), newSdnZoneCmd())
	return cmd
}

// newSdnControllerCmd builds `pmx pdm sdn controller`.
func newSdnControllerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Query SDN controllers across managed remotes",
		Long:  "Query SDN controllers of every managed remote, or the given remotes.",
	}
	cmd.AddCommand(newSdnControllerLsCmd())
	return cmd
}

// sdnControllerEntry is the decoded shape of one element of GET /sdn/controllers.
type sdnControllerEntry struct {
	Controller string  `json:"controller"`
	Type       string  `json:"type"`
	Remote     string  `json:"remote"`
	State      *string `json:"state,omitempty"`
	Node       *string `json:"node,omitempty"`
	Asn        *int64  `json:"asn,omitempty"`
}

// newSdnControllerLsCmd builds `pmx pdm sdn controller ls` — list SDN
// controllers across managed remotes (GET /sdn/controllers).
func newSdnControllerLsCmd() *cobra.Command {
	var (
		pending bool
		running bool
		ty      string
		remotes []string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List SDN controllers across managed remotes",
		Long:  "List SDN controllers of every managed remote, or the given remotes (GET /sdn/controllers).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if ty != "" && !stringInSlice(ty, validSdnControllerTypes) {
				return fmt.Errorf("list sdn controllers: --ty must be one of %s (got %q)",
					strings.Join(validSdnControllerTypes, ", "), ty)
			}

			params := &pdmsdn.ListControllersParams{}
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			if fl.Changed("ty") {
				params.Ty = strPtr(ty)
			}
			if fl.Changed("remote") {
				params.Remotes = remotes
			}

			resp, err := deps.PDM.Sdn.ListControllers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list sdn controllers: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[sdnControllerEntry](items, "sdn controller")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].Entry.Remote != table[j].Entry.Remote {
					return table[i].Entry.Remote < table[j].Entry.Remote
				}
				return table[i].Entry.Controller < table[j].Entry.Controller
			})

			headers := []string{"CONTROLLER", "TYPE", "REMOTE", "STATE", "NODE", "ASN"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Controller, e.Type, e.Remote, strPtrString(e.State), strPtrString(e.Node), int64PtrString(e.Asn),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "include attributes with changes currently pending")
	f.BoolVar(&running, "running", false, "show the running configuration instead of the pending one")
	f.StringVar(&ty, "ty", "", "only list controllers of this type: bgp|evpn|faucet|isis")
	f.StringArrayVar(&remotes, "remote", nil, "only list controllers from this remote (repeatable)")
	return cmd
}

// newSdnVnetCmd builds `pmx pdm sdn vnet`.
func newSdnVnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vnet",
		Short: "Query and create SDN VNets across managed remotes",
		Long:  "Query SDN VNets of every managed remote, and create a new VNet across multiple remotes.",
	}
	cmd.AddCommand(newSdnVnetLsCmd(), newSdnVnetAddCmd())
	return cmd
}

// sdnVnetEntry is the decoded shape of one element of GET /sdn/vnets.
type sdnVnetEntry struct {
	Vnet   string  `json:"vnet"`
	Zone   *string `json:"zone,omitempty"`
	Remote string  `json:"remote"`
	Type   string  `json:"type"`
	Tag    *int64  `json:"tag,omitempty"`
	State  *string `json:"state,omitempty"`
	Alias  *string `json:"alias,omitempty"`
}

// newSdnVnetLsCmd builds `pmx pdm sdn vnet ls` — list SDN VNets across
// managed remotes (GET /sdn/vnets).
func newSdnVnetLsCmd() *cobra.Command {
	var (
		pending bool
		running bool
		remotes []string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List SDN VNets across managed remotes",
		Long:  "List SDN VNets of every managed remote, or the given remotes (GET /sdn/vnets).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pdmsdn.ListVnetsParams{}
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			if fl.Changed("remote") {
				params.Remotes = remotes
			}

			resp, err := deps.PDM.Sdn.ListVnets(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list sdn vnets: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[sdnVnetEntry](items, "sdn vnet")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].Entry.Remote != table[j].Entry.Remote {
					return table[i].Entry.Remote < table[j].Entry.Remote
				}
				return table[i].Entry.Vnet < table[j].Entry.Vnet
			})

			headers := []string{"VNET", "ZONE", "REMOTE", "TYPE", "TAG", "STATE", "ALIAS"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Vnet, strPtrString(e.Zone), e.Remote, e.Type,
					int64PtrString(e.Tag), strPtrString(e.State), strPtrString(e.Alias),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "include attributes with changes currently pending")
	f.BoolVar(&running, "running", false, "show the running configuration instead of the pending one")
	f.StringArrayVar(&remotes, "remote", nil, "only list vnets from this remote (repeatable)")
	return cmd
}

// encodeRemoteZonePairs parses --remote entries of the form "<remote>=<zone>"
// into the []json.RawMessage shape CreateVnetsParams.Remotes expects, one
// JSON object per pair.
func encodeRemoteZonePairs(pairs []string) ([]json.RawMessage, error) {
	entries := make([]json.RawMessage, 0, len(pairs))
	for _, p := range pairs {
		remote, zone, ok := strings.Cut(p, "=")
		if !ok || remote == "" || zone == "" {
			return nil, fmt.Errorf("invalid --remote %q: expected format <remote>=<zone>", p)
		}

		b, err := json.Marshal(map[string]string{"remote": remote, "zone": zone})
		if err != nil {
			return nil, fmt.Errorf("encode remotes: %w", err)
		}
		entries = append(entries, b)
	}
	return entries, nil
}

// newSdnVnetAddCmd builds `pmx pdm sdn vnet add <vnet>` — create a VNet
// across multiple remotes (POST /sdn/vnets).
func newSdnVnetAddCmd() *cobra.Command {
	var (
		remoteZones []string
		tag         int64
	)
	cmd := &cobra.Command{
		Use:   "add <vnet>",
		Short: "Create a VNet across multiple remotes",
		Long: "Create a VNet with the given name on multiple remotes (POST /sdn/vnets). Each " +
			"--remote entry is \"<remote>=<zone>\", pairing the remote with the zone the VNet " +
			"should be created in on that remote. This is an asynchronous task: by default the " +
			"command blocks until it completes; pass --async (persistent flag) to return the " +
			"UPID immediately instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			fl := cmd.Flags()

			remotes, err := encodeRemoteZonePairs(remoteZones)
			if err != nil {
				return fmt.Errorf("add vnet %q: %w", vnet, err)
			}

			params := &pdmsdn.CreateVnetsParams{Vnet: vnet, Remotes: remotes}
			if fl.Changed("tag") {
				params.Tag = int64Ptr(tag)
			}

			resp, err := deps.PDM.Sdn.CreateVnets(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("add vnet %q: %w", vnet, err)
			}
			if resp == nil {
				return fmt.Errorf("add vnet %q: empty response from server", vnet)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("VNet %q created.", vnet))
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&remoteZones, "remote", nil,
		"<remote>=<zone> pairing this VNet with a zone on that remote (repeatable, required)")
	f.Int64Var(&tag, "tag", 0, "VXLAN VNI")
	cli.MustMarkRequired(cmd, "remote")
	return cmd
}

// newSdnZoneCmd builds `pmx pdm sdn zone`.
func newSdnZoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zone",
		Short: "Query and create SDN zones across managed remotes",
		Long:  "Query SDN zones of every managed remote, and create a new zone across multiple remotes.",
	}
	cmd.AddCommand(newSdnZoneLsCmd(), newSdnZoneAddCmd())
	return cmd
}

// sdnZoneEntry is the decoded shape of one element of GET /sdn/zones.
type sdnZoneEntry struct {
	Zone       string  `json:"zone"`
	Type       string  `json:"type"`
	Remote     string  `json:"remote"`
	State      *string `json:"state,omitempty"`
	Controller *string `json:"controller,omitempty"`
	Nodes      *string `json:"nodes,omitempty"`
	VrfVxlan   *int64  `json:"vrf-vxlan,omitempty"`
}

// newSdnZoneLsCmd builds `pmx pdm sdn zone ls` — list SDN zones across
// managed remotes (GET /sdn/zones).
func newSdnZoneLsCmd() *cobra.Command {
	var (
		pending bool
		running bool
		ty      string
		remotes []string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List SDN zones across managed remotes",
		Long:  "List SDN zones of every managed remote, or the given remotes (GET /sdn/zones).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if ty != "" && !stringInSlice(ty, validSdnZoneTypes) {
				return fmt.Errorf("list sdn zones: --ty must be one of %s (got %q)",
					strings.Join(validSdnZoneTypes, ", "), ty)
			}

			params := &pdmsdn.ListZonesParams{}
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			if fl.Changed("ty") {
				params.Ty = strPtr(ty)
			}
			if fl.Changed("remote") {
				params.Remotes = remotes
			}

			resp, err := deps.PDM.Sdn.ListZones(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list sdn zones: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[sdnZoneEntry](items, "sdn zone")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool {
				if table[i].Entry.Remote != table[j].Entry.Remote {
					return table[i].Entry.Remote < table[j].Entry.Remote
				}
				return table[i].Entry.Zone < table[j].Entry.Zone
			})

			headers := []string{"ZONE", "TYPE", "REMOTE", "STATE", "CONTROLLER", "NODES", "VRF-VXLAN"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Zone, e.Type, e.Remote, strPtrString(e.State),
					strPtrString(e.Controller), strPtrString(e.Nodes), int64PtrString(e.VrfVxlan),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "include attributes with changes currently pending")
	f.BoolVar(&running, "running", false, "show the running configuration instead of the pending one")
	f.StringVar(&ty, "ty", "", "only list zones of this type: evpn|faucet|qinq|simple|vlan|vxlan")
	f.StringArrayVar(&remotes, "remote", nil, "only list zones from this remote (repeatable)")
	return cmd
}

// encodeRemoteControllerPairs parses --remote entries of the form "<remote>"
// or "<remote>=<controller>" into the []json.RawMessage shape
// CreateZonesParams.Remotes expects, one JSON object per pair. The
// controller is optional because most SDN zone types (simple, vlan, vxlan,
// qinq) do not use one; only evpn zones require it.
func encodeRemoteControllerPairs(pairs []string) ([]json.RawMessage, error) {
	entries := make([]json.RawMessage, 0, len(pairs))
	for _, p := range pairs {
		remote, controller, _ := strings.Cut(p, "=")
		if remote == "" {
			return nil, fmt.Errorf("invalid --remote %q: expected format <remote> or <remote>=<controller>", p)
		}

		m := map[string]string{"remote": remote}
		if controller != "" {
			m["controller"] = controller
		}

		b, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("encode remotes: %w", err)
		}
		entries = append(entries, b)
	}
	return entries, nil
}

// newSdnZoneAddCmd builds `pmx pdm sdn zone add <zone>` — create a zone
// across multiple remotes (POST /sdn/zones).
func newSdnZoneAddCmd() *cobra.Command {
	var (
		remoteControllers []string
		vrfVxlan          int64
	)
	cmd := &cobra.Command{
		Use:   "add <zone>",
		Short: "Create a zone across multiple remotes",
		Long: "Create a zone with the given name on multiple remotes (POST /sdn/zones). Each " +
			"--remote entry is \"<remote>\" or \"<remote>=<controller>\"; the controller is only " +
			"needed for zone types that use one (evpn); simple/vlan/vxlan/qinq zones omit it. " +
			"This is an asynchronous task: by default the command blocks until it completes; pass " +
			"--async (persistent flag) to return the UPID immediately instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			zone := args[0]
			fl := cmd.Flags()

			remotes, err := encodeRemoteControllerPairs(remoteControllers)
			if err != nil {
				return fmt.Errorf("add zone %q: %w", zone, err)
			}

			params := &pdmsdn.CreateZonesParams{Zone: zone, Remotes: remotes}
			if fl.Changed("vrf-vxlan") {
				params.VrfVxlan = int64Ptr(vrfVxlan)
			}

			resp, err := deps.PDM.Sdn.CreateZones(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("add zone %q: %w", zone, err)
			}
			if resp == nil {
				return fmt.Errorf("add zone %q: empty response from server", zone)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Zone %q created.", zone))
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&remoteControllers, "remote", nil,
		"<remote> or <remote>=<controller> to create this zone on (repeatable, required)")
	f.Int64Var(&vrfVxlan, "vrf-vxlan", 0, "VXLAN VNI")
	cli.MustMarkRequired(cmd, "remote")
	return cmd
}
