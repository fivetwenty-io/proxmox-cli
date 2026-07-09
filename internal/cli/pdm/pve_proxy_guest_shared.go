package pdm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// pveGuestKind names one of the two guest types the pve proxy group manages
// (qemu VMs and lxc containers). Both types expose an (almost) identical verb
// set against structurally identical PDM proxy endpoints
// (/pve/remotes/{remote}/qemu/... and .../lxc/...), so every command shared
// between them is built once here, parametrized by kind, rather than
// duplicated in pve_proxy_qemu.go and pve_proxy_lxc.go. Only the handful of
// verbs that genuinely diverge — resume and migrate-preconditions (qemu
// only), and migrate/remote-migrate (different flag sets per the generated
// params structs) — are implemented directly in those two files.
type pveGuestKind struct {
	// noun is the CLI command name and API path segment ("qemu" or "lxc").
	noun string
	// label names the guest type in human-readable messages ("VM"/"container").
	label string
}

var (
	pveGuestQemu = pveGuestKind{noun: "qemu", label: "VM"}
	pveGuestLxc  = pveGuestKind{noun: "lxc", label: "container"}
)

// pveGuestNodeFlag registers the optional --node flag shared by nearly every
// guest verb (the PDM proxy auto-detects the node hosting the guest when it
// is omitted).
func pveGuestNodeFlag(cmd *cobra.Command, node *string) {
	cmd.Flags().StringVar(node, "node", "", "node name (or 'localhost'); auto-detected if omitted")
}

// pveGuestListEntry is the decoded shape of the fields GET
// /pve/remotes/{remote}/qemu and GET /pve/remotes/{remote}/lxc declare with
// identical names and types (pdm-apidoc.json, verified 2026-07-08);
// type-specific fields (e.g. lxc's disk/maxdisk/maxswap, qemu's
// pid/qmpstatus) are still preserved losslessly in Raw via decodeRawList.
type pveGuestListEntry struct {
	Vmid   pveclient.PVEInt  `json:"vmid"`
	Status string            `json:"status"`
	Name   *string           `json:"name,omitempty"`
	Cpu    *float64          `json:"cpu,omitempty"`
	Mem    *pveclient.PVEInt `json:"mem,omitempty"`
	Maxmem *pveclient.PVEInt `json:"maxmem,omitempty"`
	Uptime *pveclient.PVEInt `json:"uptime,omitempty"`
	Tags   *string           `json:"tags,omitempty"`
}

// pveGuestListFunc lists a remote's guests of one kind (GET
// .../qemu|lxc), returning the raw JSON array elements.
type pveGuestListFunc func(ctx context.Context, deps *cli.Deps, remote string, node *string) ([]json.RawMessage, error)

// newPveGuestLsCmd builds `pmx pdm pve <kind> ls <remote>` — query the
// remote's list of guests of one kind; if no node is provided, all nodes are
// queried. Sorted by VMID like every other discrete-entity ls in this
// package.
func newPveGuestLsCmd(kind pveGuestKind, list pveGuestListFunc) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "ls <remote>",
		Short: fmt.Sprintf("List a PVE remote's %ss", kind.noun),
		Long: fmt.Sprintf("Query the remote's list of %s %ss; if no node is provided, all nodes are queried "+
			"(GET /pve/remotes/{remote}/%s).", kind.label, kind.noun, kind.noun),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			items, err := list(cmd.Context(), deps, remote, nodePtr)
			if err != nil {
				return fmt.Errorf("list %ss on PVE remote %q: %w", kind.noun, remote, err)
			}

			table, err := cli.DecodePairedRows[pveGuestListEntry](items, kind.noun)
			if err != nil {
				return fmt.Errorf("decode %s entry on PVE remote %q: %w", kind.noun, remote, errors.Unwrap(err))
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Vmid.Int() < table[j].Entry.Vmid.Int() })

			headers := []string{"VMID", "NAME", "STATUS", "CPU", "MEM", "MAXMEM", "UPTIME", "TAGS"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					strconv.FormatInt(e.Vmid.Int(), 10), strPtrString(e.Name), e.Status,
					float64PtrString(e.Cpu), pveIntPtrString(e.Mem), pveIntPtrString(e.Maxmem),
					pveIntPtrString(e.Uptime), strPtrString(e.Tags),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	return cmd
}

// newPveGuestConfigCmd builds `pmx pdm pve <kind> config <remote> <vmid>` —
// get the configuration of a guest from a remote (GET
// /pve/remotes/{remote}/<kind>/{vmid}/config). ListRemotesLxcConfigResponse/
// ListRemotesQemuConfigResponse declare their ~250 numbered per-slot
// properties (dev0..dev255, mp0..mp167, net0..31, scsi0..30, etc.) as
// pointer fields with `omitempty`, so flattenToMap only surfaces the slots
// the server actually populated.
func newPveGuestConfigCmd(kind pveGuestKind) *cobra.Command {
	var (
		node, snapshot, state string
	)
	cmd := &cobra.Command{
		Use:   "config <remote> <vmid>",
		Short: fmt.Sprintf("Show a PVE remote %s's configuration", kind.label),
		Long: fmt.Sprintf("Get the configuration of a %s from a remote (node determined automatically if not "+
			"provided) (GET /pve/remotes/{remote}/%s/{vmid}/config).", kind.label, kind.noun),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]
			fl := cmd.Flags()

			var nodePtr, snapshotPtr *string
			if fl.Changed("node") {
				nodePtr = &node
			}
			if fl.Changed("snapshot") {
				snapshotPtr = &snapshot
			}

			var (
				fields map[string]any
				err    error
			)
			switch kind.noun {
			case pveGuestQemu.noun:
				params := &pdmpve.ListRemotesQemuConfigParams{State: state, Node: nodePtr, Snapshot: snapshotPtr}
				var resp *pdmpve.ListRemotesQemuConfigResponse
				resp, err = deps.PDM.Pve.ListRemotesQemuConfig(cmd.Context(), remote, vmid, params)
				if err == nil {
					fields, err = flattenToMap(resp)
				}
			case pveGuestLxc.noun:
				params := &pdmpve.ListRemotesLxcConfigParams{State: state, Node: nodePtr, Snapshot: snapshotPtr}
				var resp *pdmpve.ListRemotesLxcConfigResponse
				resp, err = deps.PDM.Pve.ListRemotesLxcConfig(cmd.Context(), remote, vmid, params)
				if err == nil {
					fields, err = flattenToMap(resp)
				}
			default:
				err = fmt.Errorf("unsupported guest kind %q", kind.noun)
			}
			if err != nil {
				return fmt.Errorf("get configuration of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.StringVar(&snapshot, "snapshot", "", "the name of the snapshot to view")
	f.StringVar(&state, "state", "pending", "guest configuration access: pending|active")
	return cmd
}

// pveGuestStatusFunc fetches a single guest's status, flattened to a generic
// map so the shared command doesn't need to know either kind's response type.
type pveGuestStatusFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) (map[string]any, error)

// newPveGuestStatusCmd builds `pmx pdm pve <kind> status <remote> <vmid>` —
// get the status of a guest from a remote (GET
// /pve/remotes/{remote}/<kind>/{vmid}/status).
func newPveGuestStatusCmd(kind pveGuestKind, status pveGuestStatusFunc) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "status <remote> <vmid>",
		Short: fmt.Sprintf("Show a PVE remote %s's status", kind.label),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			fields, err := status(cmd.Context(), deps, remote, vmid, nodePtr)
			if err != nil {
				return fmt.Errorf("get status of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	return cmd
}

// newPveGuestPendingCmd builds `pmx pdm pve <kind> pending <remote> <vmid>`
// — get the pending configuration of a guest from a remote (GET
// /pve/remotes/{remote}/<kind>/{vmid}/pending).
func newPveGuestPendingCmd(kind pveGuestKind) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "pending <remote> <vmid>",
		Short: fmt.Sprintf("Show a PVE remote %s's pending configuration", kind.label),
		Long: fmt.Sprintf("Get the pending configuration of a %s from a remote (GET "+
			"/pve/remotes/{remote}/%s/{vmid}/pending).", kind.label, kind.noun),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			var (
				items []json.RawMessage
				err   error
			)
			switch kind.noun {
			case pveGuestQemu.noun:
				var resp *pdmpve.ListRemotesQemuPendingResponse
				resp, err = deps.PDM.Pve.ListRemotesQemuPending(cmd.Context(), remote, vmid,
					&pdmpve.ListRemotesQemuPendingParams{Node: nodePtr})
				items = rawItemsOf(resp)
			case pveGuestLxc.noun:
				var resp *pdmpve.ListRemotesLxcPendingResponse
				resp, err = deps.PDM.Pve.ListRemotesLxcPending(cmd.Context(), remote, vmid,
					&pdmpve.ListRemotesLxcPendingParams{Node: nodePtr})
				items = rawItemsOf(resp)
			default:
				err = fmt.Errorf("unsupported guest kind %q", kind.noun)
			}
			if err != nil {
				return fmt.Errorf("get pending configuration of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			entries := decodeRawList(items)

			headers := []string{"KEY", "VALUE", "PENDING", "DELETE"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					scalarString(e["key"]), scalarString(e["value"]), scalarString(e["pending"]), scalarString(e["delete"]),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	return cmd
}

// pveGuestRrdEntry mirrors the fields GET /pve/remotes/{remote}/qemu/{vmid}/
// rrddata and .../lxc/{vmid}/rrddata declare with identical names and types
// (pdm-apidoc.json, verified 2026-07-08); disk-used exists only on the lxc
// schema (container root-disk usage has no qemu analog) and simply renders
// empty for qemu. Every field is still preserved losslessly in Raw via
// decodeRawList.
type pveGuestRrdEntry struct {
	Time       int64    `json:"time"`
	CpuCurrent *float64 `json:"cpu-current,omitempty"`
	MemUsed    *float64 `json:"mem-used,omitempty"`
	MemTotal   *float64 `json:"mem-total,omitempty"`
	DiskRead   *float64 `json:"disk-read,omitempty"`
	DiskWrite  *float64 `json:"disk-write,omitempty"`
	NetIn      *float64 `json:"net-in,omitempty"`
	NetOut     *float64 `json:"net-out,omitempty"`
}

// pveGuestRrdFunc reads RRD stats for a single guest. Neither
// ListRemotesLxcRrddataParams nor ListRemotesQemuRrddataParams accept a node
// parameter (pdm-apidoc.json, verified 2026-07-08), so there is no node
// argument here — unlike every other per-guest verb in this file.
type pveGuestRrdFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid, cf, timeframe string,
) ([]json.RawMessage, error)

// newPveGuestRrddataCmd builds `pmx pdm pve <kind> rrddata <remote> <vmid>`
// — read RRD guest stats (GET /pve/remotes/{remote}/<kind>/{vmid}/rrddata).
// Time-series data: rendered in server order, not sorted, matching every
// other RRD listing in this package.
func newPveGuestRrddataCmd(kind pveGuestKind, rrd pveGuestRrdFunc) *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrddata <remote> <vmid>",
		Short: fmt.Sprintf("Read a PVE remote %s's RRD metrics", kind.label),
		Long: fmt.Sprintf("Read RRD (round-robin database) %s stats over the given time frame and "+
			"consolidation function (GET /pve/remotes/{remote}/%s/{vmid}/rrddata).", kind.label, kind.noun),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]

			if !stringInSlice(timeframe, validRemoteRrdTimeframes) {
				return fmt.Errorf("get rrddata for %s %s on PVE remote %q: --timeframe must be one of %s (got %q)",
					kind.noun, vmid, remote, strings.Join(validRemoteRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRemoteRrdConsolidations) {
				return fmt.Errorf("get rrddata for %s %s on PVE remote %q: --cf must be one of %s (got %q)",
					kind.noun, vmid, remote, strings.Join(validRemoteRrdConsolidations, ", "), cf)
			}

			items, err := rrd(cmd.Context(), deps, remote, vmid, cf, timeframe)
			if err != nil {
				return fmt.Errorf("get rrddata for %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			entries, err := nodeDecodeArray[pveGuestRrdEntry](items)
			if err != nil {
				return fmt.Errorf("decode rrddata for %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			headers := []string{"TIME", "CPU-CURRENT", "MEM-USED", "MEM-TOTAL", "DISK-READ", "DISK-WRITE", "NET-IN", "NET-OUT"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					int64PtrString(&e.Time), float64PtrString(e.CpuCurrent), float64PtrString(e.MemUsed),
					float64PtrString(e.MemTotal), float64PtrString(e.DiskRead), float64PtrString(e.DiskWrite),
					float64PtrString(e.NetIn), float64PtrString(e.NetOut),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade (required)")
	f.StringVar(&cf, "cf", "AVERAGE", "RRD consolidation function: MAX|AVERAGE")
	cli.MustMarkRequired(cmd, "timeframe")
	return cmd
}

// pveGuestAsyncFunc invokes a lifecycle verb (start/shutdown/stop/resume) or
// a snapshot-delete/rollback verb for a single guest, returning the UPID
// carried by the response.
type pveGuestAsyncFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) (*json.RawMessage, error)

// newPveGuestLifecycleCmd builds `pmx pdm pve <kind> <verb> <remote> <vmid>`
// for start/shutdown/stop (shared) and resume (qemu-only, wired directly by
// pve_proxy_qemu.go). All lifecycle verbs run as an asynchronous task on the
// remote; the command blocks until it finishes unless --async (persistent
// flag) is set. When gated, --yes/-y confirmation is required.
func newPveGuestLifecycleCmd(
	kind pveGuestKind, verb, pastParticiple string, gated bool, run pveGuestAsyncFunc,
) *cobra.Command {
	var (
		node string
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   verb + " <remote> <vmid>",
		Short: fmt.Sprintf("%s a PVE remote %s", capitalize(verb), kind.label),
		Long: fmt.Sprintf("%s a remote %s (POST /pve/remotes/{remote}/%s/{vmid}/%s).", capitalize(verb), kind.label,
			kind.noun, verb),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]

			if gated && !yes {
				return fmt.Errorf("refusing to %s %s %s on PVE remote %q without confirmation: pass --yes/-y",
					verb, kind.noun, vmid, remote)
			}

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			resp, err := run(cmd.Context(), deps, remote, vmid, nodePtr)
			if err != nil {
				return fmt.Errorf("%s %s %s on PVE remote %q: %w", verb, kind.noun, vmid, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("%s %s %s on PVE remote %q: empty response from server", verb, kind.noun, vmid, remote)
			}

			msg := fmt.Sprintf("%s %s %s on PVE remote %q %s.", capitalize(kind.label), vmid, kind.noun, remote, pastParticiple)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	if gated {
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	}
	return cmd
}

// capitalize upper-cases the first rune of s, leaving the rest untouched
// (safe to call on strings that are already upper-case, e.g. kind.label's
// "VM").
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- snapshots ---------------------------------------------------------------

// pveGuestVmidListFunc lists JSON array elements scoped to a single guest
// (snapshot ls, firewall rules), sharing the same (remote, vmid, node) shape.
type pveGuestVmidListFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) ([]json.RawMessage, error)

// pveGuestSnapshotEntry mirrors the fields GET
// /pve/remotes/{remote}/lxc/{vmid}/snapshot and .../qemu/{vmid}/snapshot
// declare (pdm-apidoc.json, verified 2026-07-08): both include the current
// state as a synthetic "current" entry. Vmstate exists only on the qemu
// schema (a container has no RAM to snapshot) and simply renders empty for
// lxc entries.
type pveGuestSnapshotEntry struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Parent      *string `json:"parent,omitempty"`
	Snaptime    *int64  `json:"snaptime,omitempty"`
	Vmstate     *bool   `json:"vmstate,omitempty"`
}

// newPveGuestSnapshotLsCmd builds `pmx pdm pve <kind> snapshot ls <remote>
// <vmid>` — list the snapshots of a remote guest. The list is a parent-chain
// traversal ending in the synthetic "current" entry (pdm-apidoc.json:
// "including the current state as 'current'", verified 2026-07-08), so rows
// preserve server order rather than being sorted — matching this repo's
// direct-product precedent for the identical underlying PVE endpoint
// (internal/cli/qemu/snapshot.go's and internal/cli/lxc/snapshot.go's `list`,
// neither of which sorts).
func newPveGuestSnapshotLsCmd(kind pveGuestKind, list pveGuestVmidListFunc) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "ls <remote> <vmid>",
		Short: fmt.Sprintf("List a PVE remote %s's snapshots", kind.label),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			items, err := list(cmd.Context(), deps, remote, vmid, nodePtr)
			if err != nil {
				return fmt.Errorf("list snapshots of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			entries, err := nodeDecodeArray[pveGuestSnapshotEntry](items)
			if err != nil {
				return fmt.Errorf("decode snapshots of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			headers := []string{"NAME", "DESCRIPTION", "PARENT", "SNAPTIME", "VMSTATE"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Description, strPtrString(e.Parent), int64PtrString(e.Snaptime), boolPtrString(e.Vmstate),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	return cmd
}

// pveGuestSnapshotCreateFunc creates a snapshot. vmstate is always passed
// through (nil when unset); the lxc adapter ignores it since
// CreateRemotesLxcSnapshotParams has no Vmstate field.
type pveGuestSnapshotCreateFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node, description *string, vmstate *bool,
) (*json.RawMessage, error)

// newPveGuestSnapshotAddCmd builds `pmx pdm pve <kind> snapshot add <remote>
// <vmid> <snapname>` — create a snapshot of a remote guest. --vmstate is only
// registered for qemu (CreateRemotesLxcSnapshotParams has no such field).
func newPveGuestSnapshotAddCmd(kind pveGuestKind, create pveGuestSnapshotCreateFunc) *cobra.Command {
	var (
		node, description string
		vmstate           bool
	)
	cmd := &cobra.Command{
		Use:   "add <remote> <vmid> <snapname>",
		Short: fmt.Sprintf("Create a snapshot of a PVE remote %s", kind.label),
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid, snapname := args[0], args[1], args[2]
			fl := cmd.Flags()

			var nodePtr, descPtr *string
			if fl.Changed("node") {
				nodePtr = &node
			}
			if fl.Changed("description") {
				descPtr = &description
			}

			var vmstatePtr *bool
			if kind == pveGuestQemu && fl.Changed("vmstate") {
				vmstatePtr = &vmstate
			}

			resp, err := create(cmd.Context(), deps, remote, vmid, snapname, nodePtr, descPtr, vmstatePtr)
			if err != nil {
				return fmt.Errorf("create snapshot %q of %s %s on PVE remote %q: %w", snapname, kind.noun, vmid, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("create snapshot %q of %s %s on PVE remote %q: empty response from server",
					snapname, kind.noun, vmid, remote)
			}

			msg := fmt.Sprintf("Snapshot %q of %s %s on PVE remote %q created.", snapname, kind.noun, vmid, remote)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.StringVar(&description, "description", "", "a textual description or comment for the snapshot")
	if kind == pveGuestQemu {
		f.BoolVar(&vmstate, "vmstate", false, "include the VM's RAM state, so the snapshot resumes exactly where it left off")
	}
	return cmd
}

// pveGuestSnapshotDeleteFunc deletes a snapshot.
type pveGuestSnapshotDeleteFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node *string,
) (*json.RawMessage, error)

// newPveGuestSnapshotDeleteCmd builds `pmx pdm pve <kind> snapshot delete
// <remote> <vmid> <snapname>` — delete a snapshot of a remote guest.
// Destructive: --yes/-y is required.
func newPveGuestSnapshotDeleteCmd(kind pveGuestKind, del pveGuestSnapshotDeleteFunc) *cobra.Command {
	var (
		node string
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "delete <remote> <vmid> <snapname>",
		Short: fmt.Sprintf("Delete a snapshot of a PVE remote %s", kind.label),
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid, snapname := args[0], args[1], args[2]

			if !yes {
				return fmt.Errorf("refusing to delete snapshot %q of %s %s on PVE remote %q without confirmation: pass --yes/-y",
					snapname, kind.noun, vmid, remote)
			}

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			resp, err := del(cmd.Context(), deps, remote, vmid, snapname, nodePtr)
			if err != nil {
				return fmt.Errorf("delete snapshot %q of %s %s on PVE remote %q: %w", snapname, kind.noun, vmid, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("delete snapshot %q of %s %s on PVE remote %q: empty response from server",
					snapname, kind.noun, vmid, remote)
			}

			msg := fmt.Sprintf("Snapshot %q of %s %s on PVE remote %q deleted.", snapname, kind.noun, vmid, remote)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// pveGuestSnapshotUpdateFunc updates a snapshot's description synchronously
// (no worker task — UpdateRemotesLxcSnapshotConfig/UpdateRemotesQemuSnapshotConfig
// return only an error, pve_gen.go, v3.6.0).
type pveGuestSnapshotUpdateFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node, description *string,
) error

// newPveGuestSnapshotUpdateCmd builds `pmx pdm pve <kind> snapshot update
// <remote> <vmid> <snapname>` — update a remote guest snapshot's description.
// Synchronous: no worker task is created.
func newPveGuestSnapshotUpdateCmd(kind pveGuestKind, update pveGuestSnapshotUpdateFunc) *cobra.Command {
	var (
		node, description string
	)
	cmd := &cobra.Command{
		Use:   "update <remote> <vmid> <snapname>",
		Short: fmt.Sprintf("Update a PVE remote %s snapshot's description", kind.label),
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid, snapname := args[0], args[1], args[2]
			fl := cmd.Flags()

			if !fl.Changed("description") {
				return fmt.Errorf(
					"update snapshot %q of %s %s on PVE remote %q: no changes requested: pass --description",
					snapname, kind.noun, vmid, remote)
			}

			var nodePtr *string
			if fl.Changed("node") {
				nodePtr = &node
			}

			err := update(cmd.Context(), deps, remote, vmid, snapname, nodePtr, &description)
			if err != nil {
				return fmt.Errorf("update snapshot %q of %s %s on PVE remote %q: %w", snapname, kind.noun, vmid, remote, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Snapshot %q of %s %s on PVE remote %q updated.", snapname, kind.noun, vmid, remote),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.StringVar(&description, "description", "", "new description for the snapshot")
	return cmd
}

// pveGuestSnapshotRollbackFunc rolls a guest back to a snapshot.
type pveGuestSnapshotRollbackFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node *string, start *bool,
) (*json.RawMessage, error)

// newPveGuestSnapshotRollbackCmd builds `pmx pdm pve <kind> snapshot
// rollback <remote> <vmid> <snapname>` — roll a remote guest back to a
// snapshot. Destructive (reverts disk/config, and for qemu optionally RAM):
// --yes/-y is required.
func newPveGuestSnapshotRollbackCmd(kind pveGuestKind, rollback pveGuestSnapshotRollbackFunc) *cobra.Command {
	var (
		node  string
		start bool
		yes   bool
	)
	cmd := &cobra.Command{
		Use:   "rollback <remote> <vmid> <snapname>",
		Short: fmt.Sprintf("Roll a PVE remote %s back to a snapshot", kind.label),
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid, snapname := args[0], args[1], args[2]
			fl := cmd.Flags()

			if !yes {
				return fmt.Errorf(
					"refusing to rollback %s %s on PVE remote %q to snapshot %q without confirmation: pass --yes/-y",
					kind.noun, vmid, remote, snapname)
			}

			var nodePtr *string
			if fl.Changed("node") {
				nodePtr = &node
			}

			var startPtr *bool
			if fl.Changed("start") {
				startPtr = &start
			}

			resp, err := rollback(cmd.Context(), deps, remote, vmid, snapname, nodePtr, startPtr)
			if err != nil {
				return fmt.Errorf("rollback %s %s on PVE remote %q to snapshot %q: %w", kind.noun, vmid, remote, snapname, err)
			}
			if resp == nil {
				return fmt.Errorf("rollback %s %s on PVE remote %q to snapshot %q: empty response from server",
					kind.noun, vmid, remote, snapname)
			}

			msg := fmt.Sprintf("%s %s on PVE remote %q rolled back to snapshot %q.",
				capitalize(kind.label), vmid, remote, snapname)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.BoolVar(&start, "start", false, "start the guest after a successful rollback")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// --- firewall ------------------------------------------------------------

// pveGuestFirewallOptionsShowFunc fetches a guest's firewall options,
// flattened to a generic map.
type pveGuestFirewallOptionsShowFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) (map[string]any, error)

// pveGuestFirewallOptionsFlags collects the update flags shared by `<kind>
// firewall options update`: UpdateRemotesLxcFirewallOptionsParams and
// UpdateRemotesQemuFirewallOptionsParams declare an identical field set
// (pve_gen.go, v3.6.0).
type pveGuestFirewallOptionsFlags struct {
	del                     []string
	digest                  string
	logLevelIn, logLevelOut string
	policyIn, policyOut     string
	dhcp                    bool
	enable                  bool
	ipfilter                bool
	macfilter               bool
	ndp                     bool
	radv                    bool
}

// register binds the shared firewall-options-update flags onto cmd.
func (ff *pveGuestFirewallOptionsFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArrayVar(&ff.del, "delete", nil, "setting to reset to its default (repeatable)")
	f.StringVar(&ff.digest, "digest", "", "prevent changes if current configuration file has a different digest")
	f.BoolVar(&ff.dhcp, "dhcp", false, "enable DHCP")
	f.BoolVar(&ff.enable, "enable", false, "enable/disable firewall rules")
	f.BoolVar(&ff.ipfilter, "ipfilter", false, "enable default IP filters")
	f.StringVar(&ff.logLevelIn, "log-level-in", "", "firewall log level for incoming traffic")
	f.StringVar(&ff.logLevelOut, "log-level-out", "", "firewall log level for outgoing traffic")
	f.BoolVar(&ff.macfilter, "macfilter", false, "enable/disable MAC address filter")
	f.BoolVar(&ff.ndp, "ndp", false, "enable NDP (Neighbor Discovery Protocol)")
	f.StringVar(&ff.policyIn, "policy-in", "", "firewall IO policy for incoming traffic: ACCEPT|DROP|REJECT")
	f.StringVar(&ff.policyOut, "policy-out", "", "firewall IO policy for outgoing traffic: ACCEPT|DROP|REJECT")
	f.BoolVar(&ff.radv, "radv", false, "allow sending Router Advertisement")
}

// pveGuestFirewallOptionsUpdateFunc updates a guest's firewall options. fl is
// threaded through so the per-kind adapter can distinguish an explicitly-set
// flag from its zero value when building the generated params struct.
type pveGuestFirewallOptionsUpdateFunc func(
	ctx context.Context, deps *cli.Deps, remote, vmid string,
	fl *pflag.FlagSet, node *string, ff pveGuestFirewallOptionsFlags,
) error

// newPveGuestFirewallOptionsCmd builds `pmx pdm pve <kind> firewall options`
// and its show/update verbs (GET/PUT
// /pve/remotes/{remote}/<kind>/{vmid}/firewall/options).
func newPveGuestFirewallOptionsCmd(
	kind pveGuestKind, show pveGuestFirewallOptionsShowFunc, update pveGuestFirewallOptionsUpdateFunc,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: fmt.Sprintf("Show or update a PVE remote %s's firewall options", kind.label),
	}
	cmd.AddCommand(newPveGuestFirewallOptionsShowCmd(kind, show), newPveGuestFirewallOptionsUpdateCmd(kind, update))
	return cmd
}

func newPveGuestFirewallOptionsShowCmd(kind pveGuestKind, show pveGuestFirewallOptionsShowFunc) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "show <remote> <vmid>",
		Short: fmt.Sprintf("Show a PVE remote %s's firewall options", kind.label),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			fields, err := show(cmd.Context(), deps, remote, vmid, nodePtr)
			if err != nil {
				return fmt.Errorf("get firewall options of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	return cmd
}

// newPveGuestFirewallOptionsUpdateCmd builds `pmx pdm pve <kind> firewall
// options update <remote> <vmid>`. A configuration update, not a destructive
// action, so it is guarded by anyFlagChanged rather than --yes/-y, matching
// every other config-update command in this package.
func newPveGuestFirewallOptionsUpdateCmd(kind pveGuestKind, update pveGuestFirewallOptionsUpdateFunc) *cobra.Command {
	var (
		node string
		ff   pveGuestFirewallOptionsFlags
	)
	cmd := &cobra.Command{
		Use:   "update <remote> <vmid>",
		Short: fmt.Sprintf("Update a PVE remote %s's firewall options", kind.label),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update firewall options of %s %s on PVE remote %q: no changes requested: pass at least one flag",
					kind.noun, vmid, remote)
			}

			var nodePtr *string
			if fl.Changed("node") {
				nodePtr = &node
			}

			err := update(cmd.Context(), deps, remote, vmid, fl, nodePtr, ff)
			if err != nil {
				return fmt.Errorf("update firewall options of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Firewall options of %s %s on PVE remote %q updated.", kind.noun, vmid, remote),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	ff.register(cmd)
	return cmd
}

// newPveGuestFirewallRulesCmd builds `pmx pdm pve <kind> firewall rules
// <remote> <vmid>` — get guest firewall rules (GET
// /pve/remotes/{remote}/<kind>/{vmid}/firewall/rules). Same
// pveFirewallRuleEntry shape and server-order rendering as `pve firewall
// rules`/`pve node firewall rules` (pve_proxy_firewall.go) — guest firewall
// rules are position-ordered too.
func newPveGuestFirewallRulesCmd(kind pveGuestKind, rules pveGuestVmidListFunc) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "rules <remote> <vmid>",
		Short: fmt.Sprintf("Show a PVE remote %s's firewall rules", kind.label),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]

			var nodePtr *string
			if cmd.Flags().Changed("node") {
				nodePtr = &node
			}

			items, err := rules(cmd.Context(), deps, remote, vmid, nodePtr)
			if err != nil {
				return fmt.Errorf("list firewall rules of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			entries, err := nodeDecodeArray[pveFirewallRuleEntry](items)
			if err != nil {
				return fmt.Errorf("decode firewall rules of %s %s on PVE remote %q: %w", kind.noun, vmid, remote, err)
			}

			res := renderFirewallRules(entries)
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pveGuestNodeFlag(cmd, &node)
	return cmd
}

func newPveGuestFirewallCmd(
	kind pveGuestKind, show pveGuestFirewallOptionsShowFunc,
	update pveGuestFirewallOptionsUpdateFunc, rules pveGuestVmidListFunc,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: fmt.Sprintf("Inspect and manage a PVE remote %s's firewall", kind.label),
	}
	cmd.AddCommand(newPveGuestFirewallOptionsCmd(kind, show, update), newPveGuestFirewallRulesCmd(kind, rules))
	return cmd
}

// pveGuestOps bundles the per-guest-type SDK adapters the shared command
// constructors in this file need. Built once per kind in
// pve_proxy_qemu.go/pve_proxy_lxc.go.
type pveGuestOps struct {
	list             pveGuestListFunc
	status           pveGuestStatusFunc
	rrddata          pveGuestRrdFunc
	start            pveGuestAsyncFunc
	shutdown         pveGuestAsyncFunc
	stop             pveGuestAsyncFunc
	snapshotList     pveGuestVmidListFunc
	snapshotCreate   pveGuestSnapshotCreateFunc
	snapshotDelete   pveGuestSnapshotDeleteFunc
	snapshotUpdate   pveGuestSnapshotUpdateFunc
	snapshotRollback pveGuestSnapshotRollbackFunc
	firewallShow     pveGuestFirewallOptionsShowFunc
	firewallUpdate   pveGuestFirewallOptionsUpdateFunc
	firewallRules    pveGuestVmidListFunc
}

// newPveGuestSnapshotCmd builds `pmx pdm pve <kind> snapshot` and its
// ls/add/delete/update/rollback verbs.
func newPveGuestSnapshotCmd(kind pveGuestKind, ops pveGuestOps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: fmt.Sprintf("Manage a PVE remote %s's snapshots", kind.label),
	}
	cmd.AddCommand(
		newPveGuestSnapshotLsCmd(kind, ops.snapshotList),
		newPveGuestSnapshotAddCmd(kind, ops.snapshotCreate),
		newPveGuestSnapshotDeleteCmd(kind, ops.snapshotDelete),
		newPveGuestSnapshotUpdateCmd(kind, ops.snapshotUpdate),
		newPveGuestSnapshotRollbackCmd(kind, ops.snapshotRollback),
	)
	return cmd
}

// newPveGuestSharedCmds returns the command set shared byte-for-byte between
// `pve qemu` and `pve lxc`: ls, config, status, pending, rrddata,
// start/shutdown/stop, snapshot, and firewall. resume and
// migrate-preconditions (qemu-only) and migrate/remote-migrate (diverging
// flag sets) are appended by the caller.
func newPveGuestSharedCmds(kind pveGuestKind, ops pveGuestOps) []*cobra.Command {
	return []*cobra.Command{
		newPveGuestLsCmd(kind, ops.list),
		newPveGuestConfigCmd(kind),
		newPveGuestStatusCmd(kind, ops.status),
		newPveGuestPendingCmd(kind),
		newPveGuestRrddataCmd(kind, ops.rrddata),
		newPveGuestLifecycleCmd(kind, "start", "started", false, ops.start),
		newPveGuestLifecycleCmd(kind, "shutdown", "shut down", false, ops.shutdown),
		newPveGuestLifecycleCmd(kind, "stop", "stopped", true, ops.stop),
		newPveGuestSnapshotCmd(kind, ops),
		newPveGuestFirewallCmd(kind, ops.firewallShow, ops.firewallUpdate, ops.firewallRules),
	}
}
