package pdm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	pdmpbs "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pbs"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// pbsScanSecretKeys are the credential fields that must never be echoed back
// to the user. CreateScanResponse mirrors the request's authid/token
// verbatim, so the token is stripped before render, the same strip-once
// pattern as stripRemoteSecrets (remote.go) and stripRealmOpenidSecrets
// (realm_openid.go).
var pbsScanSecretKeys = []string{"token"}

// stripPbsScanSecrets deletes every key in pbsScanSecretKeys from fields, in place.
func stripPbsScanSecrets(fields map[string]any) {
	for _, k := range pbsScanSecretKeys {
		delete(fields, k)
	}
}

// newPbsCmd builds `pmx pdm pbs` — proxy operations against the PBS remotes
// this Proxmox Datacenter Manager instance manages (/pbs): connection
// discovery (scan/probe-tls/realms), the PBS remote directory, per-remote
// status and RRD metrics, datastores, remote nodes (APT, subscription), and
// remote tasks.
//
// ListPbs, GetRemotes, GetRemotesDatastore, GetRemotesNodes,
// ListRemotesNodesApt, and GetRemotesTasks are directory-index leaves with
// no data of their own and are excluded, matching every other product group
// in this package. CreateRemotesNodesTermproxy and
// ListRemotesNodesVncwebsocket exist solely to hand off an interactive
// shell/VNC session and have no meaningful CLI representation, so they are
// excluded too (see newNodeCmd's identical exclusion for PDM's own nodes).
func newPbsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pbs",
		Short: "Proxy operations against managed PBS remotes",
		Long: "Discover, inspect, and manage the PBS (Proxmox Backup Server) remotes " +
			"this Proxmox Datacenter Manager instance manages: connection discovery, " +
			"the PBS remote directory, per-remote status and RRD metrics, datastores, " +
			"remote nodes (APT, subscription), and remote tasks.",
	}
	cmd.AddCommand(
		newPbsRemoteCmd(),
		newPbsScanCmd(),
		newPbsProbeTLSCmd(),
		newPbsRealmsCmd(),
		newPbsStatusCmd(),
		newPbsRrddataCmd(),
		newPbsDatastoreCmd(),
		newPbsNodeCmd(),
		newPbsTaskCmd(),
	)
	return cmd
}

// pbsRemoteEntry is the decoded shape of one element of GET /pbs/remotes.
type pbsRemoteEntry struct {
	Remote string `json:"remote"`
}

// newPbsRemoteCmd builds `pmx pdm pbs remote` — the PBS remote directory view.
func newPbsRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "View the PBS remotes registry",
	}
	cmd.AddCommand(newPbsRemoteLsCmd())
	return cmd
}

// newPbsRemoteLsCmd builds `pmx pdm pbs remote ls` — return the list of PBS
// remotes (GET /pbs/remotes), sorted by remote ID.
func newPbsRemoteLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the PBS remotes this Proxmox Datacenter Manager manages",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Pbs.ListRemotes(cmd.Context())
			if err != nil {
				return fmt.Errorf("list PBS remotes: %w", err)
			}

			items := rawItemsOf(resp)
			type remoteRow struct {
				entry pbsRemoteEntry
				raw   map[string]any
			}
			table := make([]remoteRow, 0, len(items))

			for _, raw := range items {
				var e pbsRemoteEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode PBS remote entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode PBS remote entry: %w", err)
				}

				table = append(table, remoteRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Remote < table[j].entry.Remote })

			headers := []string{"REMOTE"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.entry.Remote})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pbsHostFlags collects the connection flags shared by `scan`, `probe-tls`,
// and `realms`: every field maps directly onto the CreateScanParams /
// CreateProbeTlsParams / ListRealmsParams Hostname/Fingerprint fields.
type pbsHostFlags struct {
	hostname    string
	fingerprint string
}

// register binds --hostname (required) and --fingerprint.
func (hf *pbsHostFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&hf.hostname, "hostname", "", "hostname (with optional port) of the target PBS remote (required)")
	f.StringVar(&hf.fingerprint, "fingerprint", "", "expected TLS certificate fingerprint of the target remote")
	cli.MustMarkRequired(cmd, "hostname")
}

// newPbsScanCmd builds `pmx pdm pbs scan` — scan the given connection info
// for PBS host information, checking login with the provided credentials
// (POST /pbs/scan). CreateScanResponse mirrors the request's authid/token
// verbatim; the token is never rendered, in either Single or Raw output.
func newPbsScanCmd() *cobra.Command {
	var (
		hf            pbsHostFlags
		authid, token string
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan a PBS host's connection info",
		Long: "Scan the given connection info for PBS host information, checking login " +
			"with the provided credentials (POST /pbs/scan). The access token/password " +
			"is never rendered by this command, even though the API echoes it back.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmpbs.CreateScanParams{Hostname: hf.hostname, Authid: authid, Token: token}
			if cmd.Flags().Changed("fingerprint") {
				params.Fingerprint = &hf.fingerprint
			}

			resp, err := deps.PDM.Pbs.CreateScan(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("scan PBS host %q: %w", hf.hostname, err)
			}
			if resp == nil {
				return fmt.Errorf("scan PBS host %q: empty response from server", hf.hostname)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode scan result for PBS host %q: %w", hf.hostname, err)
			}
			stripPbsScanSecrets(fields)

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	hf.register(cmd)
	f := cmd.Flags()
	f.StringVar(&authid, "authid", "", "authentication ID to log in with (required)")
	f.StringVar(&token, "token", "", "the token secret or user password (required)")
	cli.MustMarkRequired(cmd, "authid")
	cli.MustMarkRequired(cmd, "token")
	return cmd
}

// newPbsProbeTLSCmd builds `pmx pdm pbs probe-tls` — probe a PBS host's TLS
// certificate without logging in; if it is not trusted with the given
// parameters, the server reports the certificate information (POST
// /pbs/probe-tls). Runs synchronously: CreateProbeTls carries no response
// data of its own — its API schema declares a "null" return type, and the
// generated binding is error-only (pbs_gen.go:150-177, v3.6.0).
func newPbsProbeTLSCmd() *cobra.Command {
	var hf pbsHostFlags
	cmd := &cobra.Command{
		Use:   "probe-tls",
		Short: "Probe a PBS host's TLS certificate",
		Long: "Probe the given host's TLS certificate. If the certificate is not " +
			"trusted with the given fingerprint, the server reports the certificate " +
			"information (POST /pbs/probe-tls).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmpbs.CreateProbeTlsParams{Hostname: hf.hostname}
			if cmd.Flags().Changed("fingerprint") {
				params.Fingerprint = &hf.fingerprint
			}

			err := deps.PDM.Pbs.CreateProbeTls(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("probe TLS certificate of PBS host %q: %w", hf.hostname, err)
			}

			res := output.Result{Message: fmt.Sprintf("TLS certificate of PBS host %q probed.", hf.hostname)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	hf.register(cmd)
	return cmd
}

// pbsRealmEntry is the decoded shape of one element of GET /pbs/realms.
type pbsRealmEntry struct {
	Comment *string `json:"comment,omitempty"`
	Default *bool   `json:"default,omitempty"`
	Realm   string  `json:"realm"`
	Type    string  `json:"type"`
}

// newPbsRealmsCmd builds `pmx pdm pbs realms` — list the authentication
// realms available on a PBS host (GET /pbs/realms), sorted by realm name.
func newPbsRealmsCmd() *cobra.Command {
	var hf pbsHostFlags
	cmd := &cobra.Command{
		Use:   "realms",
		Short: "List a PBS host's authentication realms",
		Long:  "List the authentication realms available on a PBS host (GET /pbs/realms).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmpbs.ListRealmsParams{Hostname: hf.hostname}
			if cmd.Flags().Changed("fingerprint") {
				params.Fingerprint = &hf.fingerprint
			}

			resp, err := deps.PDM.Pbs.ListRealms(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list realms of PBS host %q: %w", hf.hostname, err)
			}

			items := rawItemsOf(resp)
			type realmRow struct {
				entry pbsRealmEntry
				raw   map[string]any
			}
			table := make([]realmRow, 0, len(items))

			for _, raw := range items {
				var e pbsRealmEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode realm entry of PBS host %q: %w", hf.hostname, err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode realm entry of PBS host %q: %w", hf.hostname, err)
				}

				table = append(table, realmRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Realm < table[j].entry.Realm })

			headers := []string{"REALM", "TYPE", "DEFAULT", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{e.Realm, e.Type, boolPtrString(e.Default), strPtrString(e.Comment)})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	hf.register(cmd)
	return cmd
}

// newPbsStatusCmd builds `pmx pdm pbs status <remote>` — get status for a
// managed PBS remote (GET /pbs/remotes/{remote}/status).
func newPbsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <remote>",
		Short: "Show a PBS remote's status",
		Long:  "Get status for a managed PBS remote (GET /pbs/remotes/{remote}/status).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pbs.ListRemotesStatus(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("get status of PBS remote %q: %w", remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get status of PBS remote %q: empty response from server", remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status of PBS remote %q: %w", remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pbsNodeRrdEntry is a table-relevant subset of the JSON object returned by
// each element of GET /pbs/remotes/{remote}/rrddata (17 fields total per the
// PDM API schema); every field is still preserved losslessly in Raw via
// decodeRawList. Mirrors node_logs.go's nodeRrdEntry, which takes the same
// subset of PDM's own /nodes/{node}/rrddata (a structurally identical
// per-host metrics schema).
type pbsNodeRrdEntry struct {
	Time       int64    `json:"time"`
	CpuCurrent *float64 `json:"cpu-current,omitempty"`
	MemUsed    *float64 `json:"mem-used,omitempty"`
	MemTotal   *float64 `json:"mem-total,omitempty"`
	DiskUsed   *float64 `json:"disk-used,omitempty"`
	NetIn      *float64 `json:"net-in,omitempty"`
	NetOut     *float64 `json:"net-out,omitempty"`
}

// newPbsRrddataCmd builds `pmx pdm pbs rrddata <remote>` — read RRD node
// stats for a PBS remote (GET /pbs/remotes/{remote}/rrddata). Time-series
// data: rendered in server order, not sorted, matching remote.go's `remote
// rrddata` (the PDM-native metric-collection RRD analog) and every other RRD
// listing in this package.
func newPbsRrddataCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrddata <remote>",
		Short: "Read a PBS remote's node RRD metrics",
		Long: "Read RRD (round-robin database) node stats for a PBS remote over the " +
			"given time frame and consolidation function (GET /pbs/remotes/{remote}/rrddata).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			if !stringInSlice(timeframe, validRemoteRrdTimeframes) {
				return fmt.Errorf("get rrddata for PBS remote %q: --timeframe must be one of %s (got %q)",
					remote, strings.Join(validRemoteRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRemoteRrdConsolidations) {
				return fmt.Errorf("get rrddata for PBS remote %q: --cf must be one of %s (got %q)",
					remote, strings.Join(validRemoteRrdConsolidations, ", "), cf)
			}

			params := &pdmpbs.ListRemotesRrddataParams{Cf: cf, Timeframe: timeframe}

			resp, err := deps.PDM.Pbs.ListRemotesRrddata(cmd.Context(), remote, params)
			if err != nil {
				return fmt.Errorf("get rrddata for PBS remote %q: %w", remote, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[pbsNodeRrdEntry](items)
			if err != nil {
				return fmt.Errorf("decode rrddata for PBS remote %q: %w", remote, err)
			}

			headers := []string{"TIME", "CPU-CURRENT", "MEM-USED", "MEM-TOTAL", "DISK-USED", "NET-IN", "NET-OUT"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					int64PtrString(&e.Time), float64PtrString(e.CpuCurrent), float64PtrString(e.MemUsed),
					float64PtrString(e.MemTotal), float64PtrString(e.DiskUsed), float64PtrString(e.NetIn),
					float64PtrString(e.NetOut),
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

// remoteTaskWaitTimeoutSeconds and remoteTaskWaitIntervalMillis mirror the
// vendored library's tasks.Wait defaults (internal/constants package,
// v3.6.0: DefaultTaskTimeoutSeconds=300, TaskIntervalMillis=1000, no
// backoff/jitter — pkg/api/tasks/tasks.go:160-192). finishRemoteAsync cannot
// reuse tasks.Wait/WaitPDMTask directly: those always poll PDM's own
// /nodes/{node}/tasks/{upid}/status, but a PBS-remote task (`pbs node apt
// update-database`, etc.) runs on the managed remote and is only visible
// through pdmpbs.Service.ListRemotesTasksStatus
// (/pbs/remotes/{remote}/tasks/{upid}/status) — PDM's local node-task
// endpoint knows nothing about it.
const (
	remoteTaskWaitTimeoutSeconds = 300
	remoteTaskWaitIntervalMillis = 1000
)

// finishRemoteAsync renders the outcome of an asynchronous task running on a
// managed PBS remote. When deps.Async is set it prints the UPID immediately;
// otherwise it blocks until the task reaches a terminal state (polling the
// pbs group's ListRemotesTasksStatus, since the task lives on the remote,
// not on PDM's own node) and prints msg.
//
// This helper is intentionally PBS-only (YAGNI): a future PVE-remote task
// helper may end up sharing this shape (a small parametrized core around
// whichever ListRemotesTasksStatus-equivalent the pve group exposes), but
// nothing in this task needs that abstraction built ahead of time.
func finishRemoteAsync(cmd *cobra.Command, deps *cli.Deps, remote string, raw json.RawMessage, msg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return err
	}

	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}

	err = waitRemoteTask(cmd.Context(), deps.PDM.Pbs, remote, upid)
	if err != nil {
		return err
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// waitRemoteTask polls ListRemotesTasksStatus for upid on remote until it
// reaches a terminal state (Status != "running") or the timeout elapses,
// mirroring the vendored tasks.Wait poll loop: an immediate first check
// before any sleep, then fixed-interval polling (pkg/api/tasks/tasks.go,
// v3.6.0). It returns an error if the task exits with a set Exitstatus that
// is neither empty, "OK", nor a "WARNINGS: N" status — the same success
// criteria tasks.Wait applies (isWarningExitStatus/isSuccessExitStatus).
func waitRemoteTask(ctx context.Context, svc pdmpbs.Service, remote, upid string) error {
	ctx, cancel := context.WithTimeout(ctx, remoteTaskWaitTimeoutSeconds*time.Second)
	defer cancel()

	interval := time.Duration(remoteTaskWaitIntervalMillis) * time.Millisecond

	for {
		status, err := svc.ListRemotesTasksStatus(ctx, remote, upid, nil)
		if err != nil {
			return fmt.Errorf("wait for task %s on PBS remote %q: %w", upid, remote, err)
		}
		if status == nil {
			return fmt.Errorf("wait for task %s on PBS remote %q: empty response from server", upid, remote)
		}

		if status.Status != "running" {
			exit := ""
			if status.Exitstatus != nil {
				exit = *status.Exitstatus
			}
			if exit != "" && exit != "OK" && !strings.HasPrefix(exit, "WARNINGS: ") {
				return fmt.Errorf("task %s on PBS remote %q failed: %s", upid, remote, exit)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for task %s on PBS remote %q: %w", upid, remote, ctx.Err())
		case <-time.After(interval):
		}
	}
}
