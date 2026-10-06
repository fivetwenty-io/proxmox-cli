package cpi

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// Defaults mirror the CPI's own defaults for the same settings.
const (
	defaultDiskBand   = "9000-29999"
	defaultParkerBand = "90000-90999"
	defaultPort       = 8006
)

const diskAuditLong = `Audit the persistent disks the BOSH Proxmox VE CPI manages, across every node,
and classify each one. The audit only reads. It issues GET requests alone and
changes nothing in the cluster.

A volume counts as the CPI's when the VMID in its name falls in the disk band,
when a drive line in a guest's config carries its bpd- serial, when a
<!--BOSH:{...}--> sentinel in a guest's description names its full volid, or
when it sits on a bus slot of a parker VM. The CPI renames a stable-ID disk to
the VMID of its guest or parker, so the band alone misses renamed disks. A
storage the listing marks disabled (enabled=0) or inactive (active=0) is
skipped, and the disks on it are not in the report.

Classifications
  attached        a guest that is not a parker holds the volume on a bus slot
  parked          a parker VM (bosh-parker tag and a VMID in the parker band)
                  holds the volume; the provenance columns come from the
                  sentinel in the parker's description
  free-floating   no guest holds the volume on a bus slot, so it is a
                  potential orphan. A volume a guest still names on an unusedN
                  entry is marked DO NOT DELETE, and a volume that only a
                  sentinel note named is marked too, because a sentinel can go
                  stale, so check it with 'bosh disks --orphaned' first
  unknown         kept for the report's shape; every volume the audit finds is
                  classified as one of the three above, so this count is 0

Warnings go to stderr, prefixed WARN:, when
  - parked disks exist and --detached-disk-strategy is free
  - a parker VM is empty (a teardown candidate)
  - a parker carries an unusedN reference to a live volume (do not destroy it)
  - a parker's config did not come back, so its contents are unknown
  - a VM sits in a parker pool without the bosh-parker tag. A pool counts as
    a parker pool when a parker belongs to it or --parker-pool names it
  - one bpd- serial appears on two volumes, which a clone of a BOSH VM causes
  - more than one guest names a volume, on an active slot or an unused entry
  - a guest config could not be read, or this token lacks VM.Audit on /vms,
    so the multiply-referenced report may be incomplete
  - a VM carries the bosh-parker tag but has a VMID outside --parker-band
  - a CPI lock pool (bosh-lock-) has passed its expiry, has no readable
    expiry, or could not be checked because the pool list did not come back
A line starting SKIPPED: names each storage the audit did not read.

The last two warnings are advice only. The audit does not count a tagged VM outside --parker-band as a parker, and it lists each one with its VMID, node, and pool so we can widen --parker-band when they are real parkers. The CPI takes a per-VMID lock as a pool named bosh-lock-vm-<vmid> whose comment holds the owner and an expiry. A lock pool past its expiry usually means a CPI process died while holding it, and the warning names the pool, its owner, the expiry in UTC, and the pmx pve pool delete command that removes it. We check that no CPI operation is still running for that VMID before we run it. A lock pool whose expiry is missing or unreadable is reported separately and is never treated as expired. Neither warning changes the exit status or the -o json and -o yaml documents.

--node limits the storage scan to one node when it is passed on the command
line; an ambient default node does not. Guest configs are read cluster-wide in
either case, because a guest on any node can hold a disk.

Output
  The table, ascii, and plain formats print the human report. -o json and
  -o yaml print one document with host, port, disk_band, parker_band, summary
  (total, attached, parked, free_floating, unknown, multiply_referenced),
  disks (sorted by volid), parkers, skipped_storages, multiply_referenced,
  multiply_referenced_unreadable_vmids, multiply_referenced_visibility (full,
  limited, or unknown), and multiply_referenced_complete.

Exit status
  0   no free-floating disks
  9   one or more free-floating disks; the last stderr line counts them and
      names any a guest still references on an unusedN entry
  2   an invalid flag value, such as a band whose start is not below its end
  A failed API read ends the audit with the code pmx gives that failure
  everywhere else, such as 3 for a connection or TLS failure, 4 for an
  authentication or permission failure, and 1 for an unclassified one,
  because the audit cannot classify a disk it could not read.`

// band is an inclusive VMID range.
type band struct {
	start, end int
}

// parseBand parses START-END into an inclusive range of non-negative VMIDs
// with START <= END.
func parseBand(flag, value string) (band, error) {
	bad := func(reason string) error {
		return &exitcode.UsageError{Err: fmt.Errorf("invalid --%s %q: %s", flag, value, reason)}
	}
	startText, endText, ok := strings.Cut(value, "-")
	if !ok {
		return band{}, bad("want START-END, for example 9000-29999")
	}
	start, err := strconv.Atoi(strings.TrimSpace(startText))
	if err != nil || start < 0 {
		return band{}, bad("START must be a non-negative integer")
	}
	end, err := strconv.Atoi(strings.TrimSpace(endText))
	if err != nil || end < 0 {
		return band{}, bad("END must be a non-negative integer")
	}
	if start >= end {
		return band{}, bad("START must be less than END")
	}
	return band{start: start, end: end}, nil
}

// parseStrategy resolves --detached-disk-strategy. An empty value is parked,
// matching the CPI's own default.
func parseStrategy(value string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	switch s {
	case "":
		return "parked", nil
	case "parked", "free":
		return s, nil
	default:
		return "", &exitcode.UsageError{
			Err: fmt.Errorf("invalid --detached-disk-strategy %q: want parked or free", value),
		}
	}
}

func newDiskAuditCmd() *cobra.Command {
	var (
		diskBand    string
		parkerBand  string
		strategy    string
		parkerPools []string
	)
	cmd := &cobra.Command{
		Use:   "disk-audit",
		Short: "Classify every BOSH persistent disk as attached, parked, or free-floating",
		Long:  diskAuditLong,
		Example: `  # Audit every node with the CPI's default bands
  pmx cpi disk-audit

  # Audit one node's storages and emit the JSON document
  pmx cpi disk-audit --node pve1 -o json

  # Match a CPI configured with custom bands and the free strategy
  pmx cpi disk-audit --disk-band 10000-19999 --parker-band 95000-95999 \
    --detached-disk-strategy free

  # Also treat an empty pool as a parker pool
  pmx cpi disk-audit --parker-pool bosh-parker,blue-parker

  # Gate a pipeline on free-floating disks (exit 9)
  pmx cpi disk-audit -o json > audit.json || [ $? -eq 9 ]`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			disk, err := parseBand("disk-band", diskBand)
			if err != nil {
				return err
			}
			parker, err := parseBand("parker-band", parkerBand)
			if err != nil {
				return err
			}
			strat, err := parseStrategy(strategy)
			if err != nil {
				return err
			}

			deps := cli.GetDeps(cmd)
			if deps.API == nil || deps.API.Raw == nil {
				return errors.New("disk-audit: no Proxmox VE API client is configured for this context")
			}
			opts := auditOptions{
				diskStart: disk.start, diskEnd: disk.end,
				parkerStart: parker.start, parkerEnd: parker.end,
				strategy:    strat,
				parkerPools: parseParkerPoolArgs(parkerPools),
			}
			if deps.NodeExplicit {
				opts.node = deps.Node
			}

			stderr := cmd.ErrOrStderr()
			inv, err := collectInventory(cmd.Context(), &pveReader{raw: deps.API.Raw}, opts, stderr)
			if err != nil {
				return err
			}
			emitWarnings(stderr, inv, opts)

			host, port := auditEndpoint(deps)
			header := reportHeader{host: host, port: port, opts: opts}
			switch deps.Format {
			case output.FormatJSON, output.FormatYAML:
				if err := deps.Out.Render(cmd.OutOrStdout(), output.Result{Raw: buildDocument(inv, header)},
					deps.Format); err != nil {
					return err
				}
			default:
				writeHumanReport(cmd.OutOrStdout(), inv, header)
			}
			return findingsError(inv)
		},
	}
	f := cmd.Flags()
	f.StringVar(&diskBand, "disk-band", defaultDiskBand,
		"inclusive VMID range START-END of the CPI's disk band (disk_vmid_range_start/end)")
	f.StringVar(&parkerBand, "parker-band", defaultParkerBand,
		"inclusive VMID range START-END of the CPI's parker band (parked_disk_vmid_range_start/end)")
	f.StringVar(&strategy, "detached-disk-strategy", "parked",
		"the CPI's detached_disk_strategy, parked or free; an empty value means parked")
	f.StringArrayVar(&parkerPools, "parker-pool", nil,
		"treat NAME as a parker pool when looking for workload VMs that do not belong in one; "+
			"repeat the flag or give a comma-separated list (a pool that holds a parker needs no flag)")
	return cmd
}

// auditEndpoint returns the host and port the report names: the endpoint
// this invocation dials, with an IPv6 literal unbracketed, or the context's
// own when no route was resolved.
func auditEndpoint(deps *cli.Deps) (string, int) {
	host, port := deps.Route.Host, deps.Route.Port
	if host == "" && deps.Ctx != nil {
		host, port = deps.Ctx.Host, deps.Ctx.Port
	}
	if port == 0 {
		port = defaultPort
	}
	return cli.UnbracketHost(host), port
}
