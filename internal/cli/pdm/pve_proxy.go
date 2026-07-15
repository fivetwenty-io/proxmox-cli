package pdm

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// pveScanSecretKeys are the credential fields that must never be echoed back
// to the user. CreateScanResponse mirrors the request's authid/token
// verbatim, so the token is stripped before render, the same strip-once
// pattern as stripRemoteSecrets (remote.go) and stripPbsScanSecrets (pbs_proxy.go).
var pveScanSecretKeys = []string{"token"}

// stripPveScanSecrets deletes every key in pveScanSecretKeys from fields, in place.
func stripPveScanSecrets(fields map[string]any) {
	for _, k := range pveScanSecretKeys {
		delete(fields, k)
	}
}

// newPveCmd builds `pmx pdm pve` — proxy operations against the PVE remotes
// this Proxmox Datacenter Manager instance manages (/pve): connection
// discovery (scan/probe-tls/realms), the PVE remote directory, cluster
// options/updates/status/next-VMID/resources, cluster and node firewalls,
// remote nodes (config, network, RRD metrics, APT, subscription, SDN VRF
// lookups), storage, remote tasks, and guest (qemu/lxc) operations.
//
// ListPve, GetRemotes, ListFirewall, ListRemotesFirewall,
// ListRemotesNodesApt, GetRemotesTasks, ListRemotesNodesFirewall,
// ListRemotesNodesSdn, GetRemotesNodesSdnVnets, GetRemotesNodesSdnZones, and
// GetRemotesNodesStorage are directory-index leaves with no data of their
// own and are excluded, matching every other product group in this package.
// CreateRemotesNodesTermproxy and ListRemotesNodesVncwebsocket exist solely
// to hand off an interactive shell/VNC session and have no meaningful CLI
// representation, so they are excluded too (see newPbsCmd's and newNodeCmd's
// identical exclusions).
func newPveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pve",
		Short: "Proxy operations against managed PVE remotes",
		Long: "Discover, inspect, and manage the PVE (Proxmox VE) remotes this Proxmox " +
			"Datacenter Manager instance manages: connection discovery, the PVE remote " +
			"directory, cluster options/updates/status/next-VMID/resources, cluster and " +
			"node firewalls, remote nodes (config, network, RRD metrics, APT, subscription, " +
			"SDN VRF lookups), storage, remote tasks, and guest (qemu/lxc) operations.",
	}
	cmd.AddCommand(
		newPveRemoteCmd(),
		newPveScanCmd(),
		newPveProbeTLSCmd(),
		newPveRealmsCmd(),
		newPveOptionsCmd(),
		newPveUpdatesCmd(),
		newPveClusterCmd(),
		newPveFirewallCmd(),
		newPveNodeCmd(),
		newPveStorageCmd(),
		newPveTaskCmd(),
		newPveQemuCmd(),
		newPveLxcCmd(),
	)
	return cmd
}

// pveRemoteEntry is the decoded shape of one element of GET /pve/remotes.
type pveRemoteEntry struct {
	Remote string `json:"remote"`
}

// newPveRemoteCmd builds `pmx pdm pve remote` — the PVE remote directory view.
func newPveRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "View the PVE remotes registry",
		Long:  "View the directory of PVE remotes this Proxmox Datacenter Manager manages.",
	}
	cmd.AddCommand(newPveRemoteLsCmd())
	return cmd
}

// newPveRemoteLsCmd builds `pmx pdm pve remote ls` — return the list of PVE
// remotes (GET /pve/remotes), sorted by remote ID.
func newPveRemoteLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List the PVE remotes this Proxmox Datacenter Manager manages",
		Long:    "Return the list of PVE remotes, sorted by remote ID (GET /pve/remotes).",
		Example: "  pmx pdm pve remote ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Pve.ListRemotes(cmd.Context())
			if err != nil {
				return fmt.Errorf("list PVE remotes: %w", err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[pveRemoteEntry](items, "PVE remote")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Remote < table[j].Entry.Remote })

			headers := []string{"REMOTE"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.Entry.Remote})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pveHostFlags collects the connection flags shared by `scan`, `probe-tls`,
// and `realms`: every field maps directly onto the CreateScanParams /
// CreateProbeTlsParams / ListRealmsParams Hostname/Fingerprint fields.
type pveHostFlags struct {
	hostname    string
	fingerprint string
}

// register binds --hostname (required) and --fingerprint.
func (hf *pveHostFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&hf.hostname, "hostname", "", "hostname (with optional port) of the target PVE remote (required)")
	f.StringVar(&hf.fingerprint, "fingerprint", "", "expected TLS certificate fingerprint of the target remote")
	cli.MustMarkRequired(cmd, "hostname")
}

// newPveScanCmd builds `pmx pdm pve scan` — scan the given connection info
// for PVE cluster information, checking login with the provided credentials
// and probing TLS on each returned node (POST /pve/scan). CreateScanResponse
// mirrors the request's authid/token verbatim; the token is never rendered,
// in either Single or Raw output.
func newPveScanCmd() *cobra.Command {
	var (
		hf            pveHostFlags
		authid, token string
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan a PVE host's connection info",
		Long: "Scan the given connection info for PVE cluster information, checking login " +
			"with the provided credentials and probing TLS on each returned node to check " +
			"if a fingerprint is necessary (POST /pve/scan). The access token/password is " +
			"never rendered by this command, even though the API echoes it back.",
		Example: "  pmx pdm pve scan --hostname pve1.example.com --authid root@pam --token '${PVE_TOKEN_SECRET}'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmpve.CreateScanParams{Hostname: hf.hostname, Authid: authid, Token: token}
			if cmd.Flags().Changed("fingerprint") {
				params.Fingerprint = &hf.fingerprint
			}

			resp, err := deps.PDM.Pve.CreateScan(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("scan PVE host %q: %w", hf.hostname, err)
			}
			if resp == nil {
				return fmt.Errorf("scan PVE host %q: empty response from server", hf.hostname)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode scan result for PVE host %q: %w", hf.hostname, err)
			}
			stripPveScanSecrets(fields)

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

// newPveProbeTLSCmd builds `pmx pdm pve probe-tls` — probe a PVE host's TLS
// certificate without logging in; if it is not trusted with the given
// parameters, the server reports the certificate information (POST
// /pve/probe-tls). Runs synchronously: CreateProbeTls carries no response
// data of its own — its API schema declares a "null" return type
// (pdm-apidoc.json, verified 2026-07-08) and the generated binding is
// error-only (pve_gen.go:414-437, v3.6.0), the identical shape and rationale
// as the PBS analog (pbs_proxy.go's `pbs probe-tls`).
func newPveProbeTLSCmd() *cobra.Command {
	var hf pveHostFlags
	cmd := &cobra.Command{
		Use:   "probe-tls",
		Short: "Probe a PVE host's TLS certificate",
		Long: "Probe the given host's TLS certificate. If the certificate is not " +
			"trusted with the given fingerprint, the server reports the certificate " +
			"information (POST /pve/probe-tls).",
		Example: "  pmx pdm pve probe-tls --hostname pve1.example.com",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmpve.CreateProbeTlsParams{Hostname: hf.hostname}
			if cmd.Flags().Changed("fingerprint") {
				params.Fingerprint = &hf.fingerprint
			}

			err := deps.PDM.Pve.CreateProbeTls(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("probe TLS certificate of PVE host %q: %w", hf.hostname, err)
			}

			res := output.Result{Message: fmt.Sprintf("TLS certificate of PVE host %q probed.", hf.hostname)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	hf.register(cmd)
	return cmd
}

// pveRealmEntry is the decoded shape of one element of GET /pve/realms.
type pveRealmEntry struct {
	Comment *string `json:"comment,omitempty"`
	Default *bool   `json:"default,omitempty"`
	Realm   string  `json:"realm"`
	Type    string  `json:"type"`
}

// newPveRealmsCmd builds `pmx pdm pve realms` — list the authentication
// realms available on a PVE host (GET /pve/realms), sorted by realm name.
func newPveRealmsCmd() *cobra.Command {
	var hf pveHostFlags
	cmd := &cobra.Command{
		Use:     "realms",
		Short:   "List a PVE host's authentication realms",
		Long:    "List the authentication realms available on a PVE host (GET /pve/realms).",
		Example: "  pmx pdm pve realms --hostname pve1.example.com",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmpve.ListRealmsParams{Hostname: hf.hostname}
			if cmd.Flags().Changed("fingerprint") {
				params.Fingerprint = &hf.fingerprint
			}

			resp, err := deps.PDM.Pve.ListRealms(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list realms of PVE host %q: %w", hf.hostname, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[pveRealmEntry](items, "realm")
			if err != nil {
				return fmt.Errorf("decode realm entry of PVE host %q: %w", hf.hostname, errors.Unwrap(err))
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Realm < table[j].Entry.Realm })

			headers := []string{"REALM", "TYPE", "DEFAULT", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{e.Realm, e.Type, boolPtrString(e.Default), strPtrString(e.Comment)})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	hf.register(cmd)
	return cmd
}

// newPveOptionsCmd builds `pmx pdm pve options <remote>` — return the
// remote's cluster options (GET /pve/remotes/{remote}/options).
func newPveOptionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "options <remote>",
		Short:   "Show a PVE remote's cluster options",
		Long:    "Return the remote's cluster options (GET /pve/remotes/{remote}/options).",
		Example: "  pmx pdm pve options pve-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pve.ListRemotesOptions(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("get cluster options for PVE remote %q: %w", remote, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode cluster options for PVE remote %q: %w", remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pdmNodeUpdateSummary decodes one entry of a RemoteUpdateSummary's "nodes"
// map: PDM's Rust source (lib/pdm-api-types/src/remote_updates.rs,
// NodeUpdateSummary, kebab-case) is the ground truth, since the PDM API
// schema declares no fields for this shape. Shared by newPveUpdatesCmd
// (below) and newRemoteUpdatesSummaryCmd (remote_task.go), whose nodes maps
// carry the identical per-node shape.
type pdmNodeUpdateSummary struct {
	NumberOfUpdates  int64   `json:"number-of-updates"`
	LastRefresh      int64   `json:"last-refresh"`
	Status           string  `json:"status"`
	StatusMessage    *string `json:"status-message,omitempty"`
	RepositoryStatus string  `json:"repository-status,omitempty"`
}

// pdmRemoteUpdateSummary decodes a RemoteUpdateSummary object: the body of
// GET /pve/remotes/{remote}/updates, and each value of the "remotes" map
// returned by GET /remotes/updates/summary (lib/pdm-api-types
// RemoteUpdateSummary, kebab-case — see pdmNodeUpdateSummary above for why
// this is modeled from the Rust source rather than the API schema). Shared
// by newPveUpdatesCmd (below) and newRemoteUpdatesSummaryCmd (remote_task.go).
type pdmRemoteUpdateSummary struct {
	Nodes         map[string]json.RawMessage `json:"nodes"`
	RemoteType    string                     `json:"remote-type"`
	Status        string                     `json:"status"`
	StatusMessage *string                    `json:"status-message,omitempty"`
}

// newPveUpdatesCmd builds `pmx pdm pve updates <remote>` — return the cached
// update information about a remote (GET /pve/remotes/{remote}/updates).
// The PDM API schema declares a "null" return type for this endpoint (the
// Rust handler's #[api(...)] macro omits `returns:`), but the real response
// is a RemoteUpdateSummary object: remote-type, status, an optional
// status-message, and nodes, a map of node name to that node's update
// summary (pdmNodeUpdateSummary above). Rows are sorted by node name; when
// nodes is empty — an error, or a remote PDM has never polled — the command
// renders Single with the remote-level status/status-message instead of an
// empty table. Raw always carries the full response losslessly, regardless
// of which of Single/Rows also renders.
func newPveUpdatesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "updates <remote>",
		Short:   "Show a PVE remote's cached update information",
		Long:    "Return the cached update information about a remote (GET /pve/remotes/{remote}/updates).",
		Example: "  pmx pdm pve updates pve-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pve.ListRemotesUpdates(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("get cached updates for PVE remote %q: %w", remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get cached updates for PVE remote %q: empty response from server", remote)
			}

			body, err := json.Marshal(resp)
			if err != nil {
				return fmt.Errorf("get cached updates for PVE remote %q: %w", remote, err)
			}
			var summary pdmRemoteUpdateSummary
			if err := json.Unmarshal(body, &summary); err != nil {
				return fmt.Errorf("decode cached updates for PVE remote %q: %w", remote, err)
			}

			names := make([]string, 0, len(summary.Nodes))
			for name := range summary.Nodes {
				names = append(names, name)
			}
			sort.Strings(names)

			rawNodes := make(map[string]any, len(names))
			rows := make([][]string, 0, len(names))

			for _, name := range names {
				nodeRaw := summary.Nodes[name]

				var entry pdmNodeUpdateSummary
				if err := json.Unmarshal(nodeRaw, &entry); err != nil {
					return fmt.Errorf("decode node %q update summary for PVE remote %q: %w", name, remote, err)
				}
				var rawNode map[string]any
				if err := json.Unmarshal(nodeRaw, &rawNode); err != nil {
					return fmt.Errorf("decode node %q update summary for PVE remote %q: %w", name, remote, err)
				}
				rawNodes[name] = rawNode

				rows = append(rows, []string{
					name,
					strconv.FormatInt(entry.NumberOfUpdates, 10),
					strconv.FormatInt(entry.LastRefresh, 10),
					entry.Status,
					entry.RepositoryStatus,
				})
			}

			raw := map[string]any{"remote-type": summary.RemoteType, "status": summary.Status, "nodes": rawNodes}
			if summary.StatusMessage != nil {
				raw["status-message"] = *summary.StatusMessage
			}

			if len(summary.Nodes) == 0 {
				single := map[string]string{"remote-type": summary.RemoteType, "status": summary.Status}
				if summary.StatusMessage != nil {
					single["status-message"] = *summary.StatusMessage
				}
				res := output.Result{Single: single, Raw: raw}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			headers := []string{"NODE", "UPDATES", "LAST-REFRESH", "STATUS", "REPO-STATUS"}
			res := output.Result{Headers: headers, Rows: rows, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveClusterCmd builds `pmx pdm pve cluster` — status/next-id/resources
// verbs (/pve/remotes/{remote}/cluster-status, /cluster-nextid, /resources).
func newPveClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Inspect a PVE remote's cluster",
		Long:  "Inspect a PVE remote's cluster: node status, the next free VMID, and cluster resources.",
	}
	cmd.AddCommand(newPveClusterStatusCmd(), newPveClusterNextIDCmd(), newPveClusterResourcesCmd())
	return cmd
}

// newPveClusterStatusCmd builds `pmx pdm pve cluster status <remote>` —
// query the cluster nodes status (GET /pve/remotes/{remote}/cluster-status).
// Each element mixes a "cluster" summary object with per-"node" objects
// (pdm-apidoc.json, verified 2026-07-08); rows preserve API order rather
// than being sorted, since re-sorting would separate the summary row from
// its node rows.
func newPveClusterStatusCmd() *cobra.Command {
	var targetEndpoint string
	cmd := &cobra.Command{
		Use:     "status <remote>",
		Short:   "Query a PVE remote's cluster nodes status",
		Long:    "Query the cluster nodes status (GET /pve/remotes/{remote}/cluster-status).",
		Example: "  pmx pdm pve cluster status pve-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			params := &pdmpve.ListRemotesClusterStatusParams{}
			if cmd.Flags().Changed("target-endpoint") {
				params.TargetEndpoint = &targetEndpoint
			}

			resp, err := deps.PDM.Pve.ListRemotesClusterStatus(cmd.Context(), remote, params)
			if err != nil {
				return fmt.Errorf("get cluster status for PVE remote %q: %w", remote, err)
			}

			items := decodeRawList(rawItemsOf(resp))

			headers := []string{"ID", "NAME", "TYPE", "IP", "ONLINE", "LOCAL", "LEVEL"}
			rows := make([][]string, 0, len(items))
			for _, m := range items {
				rows = append(rows, []string{
					scalarString(m["id"]), scalarString(m["name"]), scalarString(m["type"]), scalarString(m["ip"]),
					scalarString(m["online"]), scalarString(m["local"]), scalarString(m["level"]),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: items}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&targetEndpoint, "target-endpoint", "", "the target endpoint to use for the connection")
	return cmd
}

// newPveClusterNextIDCmd builds `pmx pdm pve cluster next-id <remote>` — get
// the next free VMID on the (target) cluster (GET
// /pve/remotes/{remote}/cluster-nextid).
func newPveClusterNextIDCmd() *cobra.Command {
	var targetEndpoint string
	cmd := &cobra.Command{
		Use:   "next-id <remote>",
		Short: "Get the next free VMID on a PVE remote's (target) cluster",
		Long: "Get the next free VMID on the (target) cluster, e.g. to prefill a " +
			"migration target VMID (GET /pve/remotes/{remote}/cluster-nextid).",
		Example: "  pmx pdm pve cluster next-id pve-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			params := &pdmpve.ListRemotesClusterNextidParams{}
			if cmd.Flags().Changed("target-endpoint") {
				params.TargetEndpoint = &targetEndpoint
			}

			resp, err := deps.PDM.Pve.ListRemotesClusterNextid(cmd.Context(), remote, params)
			if err != nil {
				return fmt.Errorf("get next free VMID for PVE remote %q: %w", remote, err)
			}

			var data any
			if resp != nil {
				if err := json.Unmarshal(*resp, &data); err != nil {
					return fmt.Errorf("decode next free VMID for PVE remote %q: %w", remote, err)
				}
			}

			vmid := scalarString(data)

			res := output.Result{Single: map[string]string{"vmid": vmid}, Raw: data, Message: vmid}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&targetEndpoint, "target-endpoint", "", "the target endpoint to use for the connection")
	return cmd
}

// pveResourceEntry is a table-relevant subset of one element of GET
// /pve/remotes/{remote}/resources: the union type has many shape-specific
// fields (pdm-apidoc.json declares it as a oneOf across guest/node/storage/
// sdn/network resource kinds), but these are common across every kind. There
// is no "type" field in the response; TYPE is inferred from the
// "<type>/<name-or-id>" convention every Proxmox resource id follows, the
// same convention resource.go's `resource ls` (PDM's own aggregated
// resources) uses for the identical reason.
type pveResourceEntry struct {
	Id     string            `json:"id"`
	Node   *string           `json:"node,omitempty"`
	Name   *string           `json:"name,omitempty"`
	Status *string           `json:"status,omitempty"`
	Vmid   *pveclient.PVEInt `json:"vmid,omitempty"`
}

// resourceTypeFromID infers a Proxmox resource's type from the
// "<type>/<name-or-id>" convention every resource id follows, matching
// resource.go's identical inference for PDM's own aggregated resource list.
func resourceTypeFromID(id string) string {
	if idx := strings.IndexByte(id, '/'); idx >= 0 {
		return id[:idx]
	}
	return ""
}

// validPveResourceKinds are the --kind enum values accepted by
// GET /pve/remotes/{remote}/resources, per the PDM API schema.
var validPveResourceKinds = []string{"vm", "storage", "node", "sdn"}

// newPveClusterResourcesCmd builds `pmx pdm pve cluster resources <remote>`
// — query the cluster's resources (GET /pve/remotes/{remote}/resources).
func newPveClusterResourcesCmd() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "resources <remote>",
		Short: "Query a PVE remote's cluster resources",
		Long:  "Query the cluster's resources (GET /pve/remotes/{remote}/resources).",
		Example: `  pmx pdm pve cluster resources pve-main
  pmx pdm pve cluster resources pve-main --kind vm`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			if kind != "" && !stringInSlice(kind, validPveResourceKinds) {
				return fmt.Errorf("get cluster resources for PVE remote %q: --kind must be one of %s (got %q)",
					remote, strings.Join(validPveResourceKinds, ", "), kind)
			}

			params := &pdmpve.ListRemotesResourcesParams{}
			if cmd.Flags().Changed("kind") {
				params.Kind = &kind
			}

			resp, err := deps.PDM.Pve.ListRemotesResources(cmd.Context(), remote, params)
			if err != nil {
				return fmt.Errorf("get cluster resources for PVE remote %q: %w", remote, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[pveResourceEntry](items)
			if err != nil {
				return fmt.Errorf("decode cluster resources for PVE remote %q: %w", remote, err)
			}

			headers := []string{"TYPE", "ID", "NODE", "NAME", "STATUS", "VMID"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					resourceTypeFromID(e.Id), e.Id, strPtrString(e.Node), strPtrString(e.Name),
					strPtrString(e.Status), pveIntPtrString(e.Vmid),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "resource type: vm|storage|node|sdn")
	return cmd
}
