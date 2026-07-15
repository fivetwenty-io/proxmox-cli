package pbs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// syncSyncDirections are the sync-direction enum values PBS accepts wherever
// a sync job or sync-job filter takes a direction (push, pull, or, for list
// filters only, all).
var syncSyncDirections = []string{"push", "pull", "all"}

// newSyncCmd builds `pmx pbs sync` — run one-shot pull/push syncs, inspect
// the runtime status of configured sync jobs, and manage scheduled sync job
// configurations (POST /pull, /push, GET /admin/sync, /config/sync CRUD,
// POST /admin/sync/{id}/run).
func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync backup snapshots to or from a remote",
		Long: "Pull or push backup snapshots between this Proxmox Backup Server and a " +
			"configured remote, either as a one-shot run or via scheduled sync job " +
			"configurations.",
	}
	cmd.AddCommand(newSyncLsCmd(), newSyncJobCmd(), newSyncPullCmd(), newSyncPushCmd())
	return cmd
}

// syncStatusEntry is the decoded shape of one element of GET /admin/sync: a
// sync job configuration together with its most recent run status. Every
// field but id, store, and remote-store is optional per the PBS API schema.
type syncStatusEntry struct {
	Comment        *string `json:"comment,omitempty"`
	Id             string  `json:"id"`
	LastRunEndtime *int64  `json:"last-run-endtime,omitempty"`
	LastRunState   *string `json:"last-run-state,omitempty"`
	LastRunUpid    *string `json:"last-run-upid,omitempty"`
	NextRun        *int64  `json:"next-run,omitempty"`
	Ns             *string `json:"ns,omitempty"`
	Remote         *string `json:"remote,omitempty"`
	RemoteStore    string  `json:"remote-store"`
	Schedule       *string `json:"schedule,omitempty"`
	Store          string  `json:"store"`
	SyncDirection  *string `json:"sync-direction,omitempty"`
}

// decodeSyncStatusEntries decodes an Admin.ListSync response into typed
// entries, skipping any element that fails to decode.
func decodeSyncStatusEntries(resp *pbsadmin.ListSyncResponse) []syncStatusEntry {
	entries := make([]syncStatusEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e syncStatusEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newSyncLsCmd builds `pmx pbs sync ls` — list every sync job's runtime
// status across all datastores, or scoped by --store and/or --sync-direction
// (GET /admin/sync).
func newSyncLsCmd() *cobra.Command {
	var (
		store         string
		syncDirection string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sync jobs and their run status",
		Long: "List every sync job configuration visible to the caller together with " +
			"its most recent run state (GET /admin/sync). Scope the results with " +
			"--store and/or --sync-direction.",
		Example: "  pmx pbs sync ls --store tank",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			fl := cmd.Flags()
			if fl.Changed("sync-direction") && !stringInSlice(syncDirection, syncSyncDirections) {
				return fmt.Errorf("--sync-direction must be one of push, pull, all (got %q)", syncDirection)
			}

			params := &pbsadmin.ListSyncParams{}
			if fl.Changed("store") {
				params.Store = strPtr(store)
			}

			if fl.Changed("sync-direction") {
				params.SyncDirection = strPtr(syncDirection)
			}

			resp, err := deps.PBS.Admin.ListSync(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list sync job status: %w", err)
			}

			entries := decodeSyncStatusEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{
				"ID", "STORE", "REMOTE", "REMOTE-STORE", "NS", "DIRECTION", "SCHEDULE", "LAST-RUN-STATE", "NEXT-RUN",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Id, e.Store, pbsFormatOptionalString(e.Remote), e.RemoteStore,
					pbsFormatOptionalString(e.Ns), pbsFormatOptionalString(e.SyncDirection),
					pbsFormatOptionalString(e.Schedule), pbsFormatOptionalString(e.LastRunState),
					pbsFormatOptionalInt64(e.NextRun),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&store, "store", "", "only list sync jobs targeting this datastore")
	cmd.Flags().StringVar(&syncDirection, "sync-direction", "", "only list jobs of this direction: push|pull|all")
	return cmd
}

// newSyncJobCmd builds `pmx pbs sync job` — create, inspect, update, delete,
// and manually trigger scheduled sync job configurations.
func newSyncJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage scheduled sync job configurations",
		Long: "Create, inspect, update, delete, and manually trigger scheduled sync " +
			"job configurations (GET/POST/PUT/DELETE /config/sync, POST /admin/sync/{id}/run).",
	}
	cmd.AddCommand(
		newSyncJobLsCmd(),
		newSyncJobShowCmd(),
		newSyncJobAddCmd(),
		newSyncJobUpdateCmd(),
		newSyncJobDeleteCmd(),
		newSyncJobRunCmd(),
	)
	return cmd
}

// syncConfigEntry is the decoded shape of one element of GET /config/sync: a
// sync job configuration with no run-status fields (that richer status view
// lives at `sync ls`, GET /admin/sync).
type syncConfigEntry struct {
	Comment       *string `json:"comment,omitempty"`
	Id            string  `json:"id"`
	Ns            *string `json:"ns,omitempty"`
	Remote        *string `json:"remote,omitempty"`
	RemoteStore   string  `json:"remote-store"`
	Schedule      *string `json:"schedule,omitempty"`
	Store         string  `json:"store"`
	SyncDirection *string `json:"sync-direction,omitempty"`
}

// decodeSyncConfigEntries decodes a Config.ListSync response into typed
// entries, skipping any element that fails to decode.
func decodeSyncConfigEntries(resp *pbsconfig.ListSyncResponse) []syncConfigEntry {
	entries := make([]syncConfigEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e syncConfigEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newSyncJobLsCmd builds `pmx pbs sync job ls` — list every sync job
// configuration, with no run-status fields (GET /config/sync). Scope with
// --sync-direction; unlike `sync ls`, this endpoint has no --store filter.
func newSyncJobLsCmd() *cobra.Command {
	var syncDirection string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sync job configurations",
		Long: "List every sync job configuration visible to the caller (GET " +
			"/config/sync), scoped with --sync-direction. This is the plain " +
			"configuration listing; use `pmx pbs sync ls` for run-status fields.",
		Example: "  pmx pbs sync job ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			fl := cmd.Flags()
			if fl.Changed("sync-direction") && !stringInSlice(syncDirection, syncSyncDirections) {
				return fmt.Errorf("--sync-direction must be one of push, pull, all (got %q)", syncDirection)
			}

			params := &pbsconfig.ListSyncParams{}
			if fl.Changed("sync-direction") {
				params.SyncDirection = strPtr(syncDirection)
			}

			resp, err := deps.PBS.Config.ListSync(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list sync jobs: %w", err)
			}

			entries := decodeSyncConfigEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{"ID", "STORE", "REMOTE", "REMOTE-STORE", "NS", "DIRECTION", "SCHEDULE"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Id, e.Store, pbsFormatOptionalString(e.Remote), e.RemoteStore,
					pbsFormatOptionalString(e.Ns), pbsFormatOptionalString(e.SyncDirection),
					pbsFormatOptionalString(e.Schedule),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&syncDirection, "sync-direction", "", "only list jobs of this direction: push|pull|all")
	return cmd
}

// newSyncJobShowCmd builds `pmx pbs sync job show <id>` — show one sync
// job's full configuration (GET /config/sync/{id}).
func newSyncJobShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one sync job's configuration",
		Long: "Show every populated field of a single sync job configuration (GET " +
			"/config/sync/{id}). The PBS API omits options left at their built-in " +
			"defaults; pass --defaults to also list those, with the value they " +
			"effectively have.",
		Example: "  pmx pbs sync job show offsite-pull",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			resp, err := deps.PBS.Config.GetSync(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("show sync job %q: %w", id, err)
			}

			if resp == nil {
				return fmt.Errorf("show sync job %q: nil response from PBS", id)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode sync job %q: %w", id, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(syncJobOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// syncJobFlags collects the sync-job attribute flags shared by `job add` and
// `job update`. Every field maps directly onto a CreateSyncParams /
// UpdateSyncParams field of the same name.
type syncJobFlags struct {
	// shared tunables (add + update)
	activeEncryptionKey string
	associatedKey       []string
	burstIn             string
	burstOut            string
	comment             string
	encryptedOnly       bool
	groupFilter         []string
	maxDepth            int64
	ns                  string
	owner               string
	rateIn              string
	rateOut             string
	remote              string
	remoteNs            string
	remoteStore         string
	removeVanished      bool
	resyncCorrupt       bool
	runOnMount          bool
	schedule            string
	store               string
	syncDirection       string
	transferLast        int64
	unmountOnDone       bool
	verifiedOnly        bool
	workerThreads       int64

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `job add` and
// `job update`.
func (sf *syncJobFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&sf.activeEncryptionKey, "active-encryption-key", "", "ID of the encryption key to associate")
	f.StringArrayVar(&sf.associatedKey, "associated-key", nil, "cryptographic key ID associated with the job (repeatable)")
	f.StringVar(&sf.burstIn, "burst-in", "", "inbound burst byte size, e.g. '10MB'")
	f.StringVar(&sf.burstOut, "burst-out", "", "outbound burst byte size, e.g. '10MB'")
	f.StringVar(&sf.comment, "comment", "", "comment")
	f.BoolVar(&sf.encryptedOnly, "encrypted-only", false, "only sync encrypted backup snapshots")
	f.StringArrayVar(&sf.groupFilter, "group-filter", nil,
		"group filter, e.g. 'type:vm' or 'group:vm/100' (repeatable)")
	f.Int64Var(&sf.maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	f.StringVar(&sf.ns, "ns", "", "local namespace")
	f.StringVar(&sf.owner, "owner", "", "authentication ID that owns synced snapshots")
	f.StringVar(&sf.rateIn, "rate-in", "", "inbound rate limit, e.g. '10MB'")
	f.StringVar(&sf.rateOut, "rate-out", "", "outbound rate limit, e.g. '10MB'")
	f.StringVar(&sf.remote, "remote", "", "remote ID")
	f.StringVar(&sf.remoteNs, "remote-ns", "", "remote namespace")
	f.StringVar(&sf.remoteStore, "remote-store", "", "remote datastore name")
	f.BoolVar(&sf.removeVanished, "remove-vanished", false, "delete local backups no longer present on the remote")
	f.BoolVar(&sf.resyncCorrupt, "resync-corrupt", false, "re-pull local snapshots that failed verification")
	f.BoolVar(&sf.runOnMount, "run-on-mount", false, "run this job when a relevant datastore is mounted")
	f.StringVar(&sf.schedule, "schedule", "", "calendar-event schedule, e.g. 'daily'")
	f.StringVar(&sf.store, "store", "", "local datastore name")
	f.StringVar(&sf.syncDirection, "sync-direction", "", "sync direction: push|pull")
	f.Int64Var(&sf.transferLast, "transfer-last", 0, "limit transfer to the last N snapshots per group")
	f.BoolVar(&sf.unmountOnDone, "unmount-on-done", false, "unmount a removable datastore after the job finishes")
	f.BoolVar(&sf.verifiedOnly, "verified-only", false, "only sync verified backup snapshots")
	f.Int64Var(&sf.workerThreads, "worker-threads", 0, "number of worker threads processing groups in parallel")
}

// registerAdd binds every flag `job add` accepts (the common set only; id,
// store, and remote-store are supplied via the positional argument and
// required --store/--remote-store flags, validated in the RunE).
func (sf *syncJobFlags) registerAdd(cmd *cobra.Command) {
	sf.registerCommon(cmd)
}

// registerUpdate binds every flag `job update` accepts, including the
// update-only delete/digest fields.
func (sf *syncJobFlags) registerUpdate(cmd *cobra.Command) {
	sf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&sf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&sf.digest, "digest", "", "only update if the current config digest matches")
}

// applyAdd builds the create payload, forwarding optional flags only when set.
func (sf *syncJobFlags) applyAdd(cmd *cobra.Command, p *pbsconfig.CreateSyncParams) {
	fl := cmd.Flags()
	if fl.Changed("active-encryption-key") {
		p.ActiveEncryptionKey = &sf.activeEncryptionKey
	}
	if fl.Changed("associated-key") {
		p.AssociatedKey = sf.associatedKey
	}
	if fl.Changed("burst-in") {
		p.BurstIn = &sf.burstIn
	}
	if fl.Changed("burst-out") {
		p.BurstOut = &sf.burstOut
	}
	if fl.Changed("comment") {
		p.Comment = &sf.comment
	}
	if fl.Changed("encrypted-only") {
		p.EncryptedOnly = &sf.encryptedOnly
	}
	if fl.Changed("group-filter") {
		p.GroupFilter = sf.groupFilter
	}
	if fl.Changed("max-depth") {
		p.MaxDepth = &sf.maxDepth
	}
	if fl.Changed("ns") {
		p.Ns = &sf.ns
	}
	if fl.Changed("owner") {
		p.Owner = &sf.owner
	}
	if fl.Changed("rate-in") {
		p.RateIn = &sf.rateIn
	}
	if fl.Changed("rate-out") {
		p.RateOut = &sf.rateOut
	}
	if fl.Changed("remote") {
		p.Remote = &sf.remote
	}
	if fl.Changed("remote-ns") {
		p.RemoteNs = &sf.remoteNs
	}
	if fl.Changed("remove-vanished") {
		p.RemoveVanished = &sf.removeVanished
	}
	if fl.Changed("resync-corrupt") {
		p.ResyncCorrupt = &sf.resyncCorrupt
	}
	if fl.Changed("run-on-mount") {
		p.RunOnMount = &sf.runOnMount
	}
	if fl.Changed("schedule") {
		p.Schedule = &sf.schedule
	}
	if fl.Changed("sync-direction") {
		p.SyncDirection = &sf.syncDirection
	}
	if fl.Changed("transfer-last") {
		p.TransferLast = &sf.transferLast
	}
	if fl.Changed("unmount-on-done") {
		p.UnmountOnDone = &sf.unmountOnDone
	}
	if fl.Changed("verified-only") {
		p.VerifiedOnly = &sf.verifiedOnly
	}
	if fl.Changed("worker-threads") {
		p.WorkerThreads = &sf.workerThreads
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (sf *syncJobFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateSyncParams) {
	fl := cmd.Flags()
	if fl.Changed("active-encryption-key") {
		p.ActiveEncryptionKey = &sf.activeEncryptionKey
	}
	if fl.Changed("associated-key") {
		p.AssociatedKey = sf.associatedKey
	}
	if fl.Changed("burst-in") {
		p.BurstIn = &sf.burstIn
	}
	if fl.Changed("burst-out") {
		p.BurstOut = &sf.burstOut
	}
	if fl.Changed("comment") {
		p.Comment = &sf.comment
	}
	if fl.Changed("encrypted-only") {
		p.EncryptedOnly = &sf.encryptedOnly
	}
	if fl.Changed("group-filter") {
		p.GroupFilter = sf.groupFilter
	}
	if fl.Changed("max-depth") {
		p.MaxDepth = &sf.maxDepth
	}
	if fl.Changed("ns") {
		p.Ns = &sf.ns
	}
	if fl.Changed("owner") {
		p.Owner = &sf.owner
	}
	if fl.Changed("rate-in") {
		p.RateIn = &sf.rateIn
	}
	if fl.Changed("rate-out") {
		p.RateOut = &sf.rateOut
	}
	if fl.Changed("remote") {
		p.Remote = &sf.remote
	}
	if fl.Changed("remote-ns") {
		p.RemoteNs = &sf.remoteNs
	}
	if fl.Changed("remote-store") {
		p.RemoteStore = &sf.remoteStore
	}
	if fl.Changed("remove-vanished") {
		p.RemoveVanished = &sf.removeVanished
	}
	if fl.Changed("resync-corrupt") {
		p.ResyncCorrupt = &sf.resyncCorrupt
	}
	if fl.Changed("run-on-mount") {
		p.RunOnMount = &sf.runOnMount
	}
	if fl.Changed("schedule") {
		p.Schedule = &sf.schedule
	}
	if fl.Changed("store") {
		p.Store = &sf.store
	}
	if fl.Changed("sync-direction") {
		p.SyncDirection = &sf.syncDirection
	}
	if fl.Changed("transfer-last") {
		p.TransferLast = &sf.transferLast
	}
	if fl.Changed("unmount-on-done") {
		p.UnmountOnDone = &sf.unmountOnDone
	}
	if fl.Changed("verified-only") {
		p.VerifiedOnly = &sf.verifiedOnly
	}
	if fl.Changed("worker-threads") {
		p.WorkerThreads = &sf.workerThreads
	}
	if fl.Changed("delete") {
		p.Delete = sf.del
	}
	if fl.Changed("digest") {
		p.Digest = &sf.digest
	}
}

// newSyncJobAddCmd builds `pmx pbs sync job add <id>` — create a scheduled
// sync job configuration (POST /config/sync). --store and --remote-store are
// required; every other option is optional and only forwarded when set.
func newSyncJobAddCmd() *cobra.Command {
	var sf syncJobFlags
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a scheduled sync job",
		Long: "Create a new sync job configuration (POST /config/sync). --store and " +
			"--remote-store are required; every other option is optional and only " +
			"forwarded when explicitly set.",
		Example: `  pmx pbs sync job add offsite-pull --store tank --remote-store tank \
  --remote pbs-main --schedule daily`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			if sf.store == "" {
				return fmt.Errorf("--store is required")
			}

			if sf.remoteStore == "" {
				return fmt.Errorf("--remote-store is required")
			}

			params := &pbsconfig.CreateSyncParams{
				Id:          id,
				Store:       sf.store,
				RemoteStore: sf.remoteStore,
			}
			sf.applyAdd(cmd, params)

			err := deps.PBS.Config.CreateSync(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create sync job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Sync job %q created.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	sf.registerAdd(cmd)
	cli.MustMarkRequired(cmd, "store")
	cli.MustMarkRequired(cmd, "remote-store")
	return cmd
}

// newSyncJobUpdateCmd builds `pmx pbs sync job update <id>` — update a
// scheduled sync job configuration (PUT /config/sync/{id}). Only flags
// explicitly set are sent; use --delete to reset properties to their default.
func newSyncJobUpdateCmd() *cobra.Command {
	var sf syncJobFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a scheduled sync job",
		Long: "Update an existing sync job configuration (PUT /config/sync/{id}). " +
			"Only flags explicitly set are sent; use --delete to reset properties to " +
			"their default instead.",
		Example: "  pmx pbs sync job update offsite-pull --schedule daily",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update sync job %q: no changes requested: pass at least one flag", id)
			}

			if cmd.Flags().Changed("delete") {
				for _, name := range sf.del {
					if name == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateSyncParams{}
			sf.applyUpdate(cmd, params)

			err := deps.PBS.Config.UpdateSync(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update sync job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Sync job %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	sf.registerUpdate(cmd)
	return cmd
}

// newSyncJobDeleteCmd builds `pmx pbs sync job delete <id>` — remove a sync
// job configuration (DELETE /config/sync/{id}).
func newSyncJobDeleteCmd() *cobra.Command {
	var digest string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a scheduled sync job",
		Long: "Remove a sync job configuration (DELETE /config/sync/{id}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs sync job delete offsite-pull --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete sync job %q without confirmation: pass --yes/-y", id)
			}

			params := &pbsconfig.DeleteSyncParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteSync(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete sync job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Sync job %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newSyncJobRunCmd builds `pmx pbs sync job run <id>` — manually trigger a
// scheduled sync job (POST /admin/sync/{id}/run).
//
// The generated Admin.CreateSyncRun binding is error-only: it discards the
// UPID the server actually returns for this endpoint (unlike prune/verify
// job run, which really do report only success/failure). To surface that
// UPID and support --async / task-wait semantics, this command bypasses that
// binding and issues the POST directly through the shared raw client
// (deps.PBS.Raw), which every generated service method is itself built on
// top of, so the request shape (path, auth, error handling) is identical.
func newSyncJobRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Manually trigger a scheduled sync job",
		Long: "Immediately run a configured sync job (POST /admin/sync/{id}/run). " +
			"Runs as an asynchronous task; the command blocks until it finishes " +
			"unless --async is set.",
		Example: "  pmx pbs sync job run offsite-pull",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			path := "/admin/sync/" + url.PathEscape(id) + "/run"

			resp, err := deps.PBS.Raw.PostRawCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("run sync job %q: %w", id, err)
			}

			if resp == nil {
				return fmt.Errorf("run sync job %q: nil response from PBS", id)
			}

			raw, err := json.Marshal(resp.Data)
			if err != nil {
				return fmt.Errorf("run sync job %q: encode response: %w", id, err)
			}

			return finishAsync(cmd, deps, raw, fmt.Sprintf("Sync job %q finished.", id))
		},
	}
	return cmd
}

// newSyncPullCmd builds `pmx pbs sync pull` — run a one-shot pull sync from
// a remote datastore into a local datastore (POST /pull).
//
// The generated Pull.Pull binding is error-only: it discards the UPID the
// server returns for this endpoint. To surface that UPID and support
// --async / task-wait semantics, this command bypasses that binding and
// issues the POST directly through the shared raw client (deps.PBS.Raw).
func newSyncPullCmd() *cobra.Command {
	var (
		burstIn        string
		burstOut       string
		decryptionKeys []string
		encryptedOnly  bool
		groupFilter    []string
		maxDepth       int64
		ns             string
		rateIn         string
		rateOut        string
		remote         string
		remoteNs       string
		remoteStore    string
		removeVanished bool
		resyncCorrupt  bool
		store          string
		transferLast   int64
		verifiedOnly   bool
		workerThreads  int64
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull backup snapshots from a remote datastore",
		Long: "Run a one-shot pull sync (POST /pull): copy backup snapshots from a " +
			"remote datastore into a local one. --remote-store and --store are " +
			"required; omitting --remote pulls from another datastore on this " +
			"server. Runs as an asynchronous task; the command blocks until it " +
			"finishes unless --async is set.",
		Example: `  pmx pbs sync pull --remote pbs-main --remote-store tank --store tank`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if remoteStore == "" {
				return fmt.Errorf("--remote-store is required")
			}

			if store == "" {
				return fmt.Errorf("--store is required")
			}

			fl := cmd.Flags()
			body := map[string]interface{}{
				"remote-store": remoteStore,
				"store":        store,
			}
			if remote != "" {
				body["remote"] = remote
			}
			if fl.Changed("burst-in") {
				body["burst-in"] = burstIn
			}
			if fl.Changed("burst-out") {
				body["burst-out"] = burstOut
			}
			if fl.Changed("decryption-keys") {
				body["decryption-keys"] = decryptionKeys
			}
			if fl.Changed("encrypted-only") {
				body["encrypted-only"] = encryptedOnly
			}
			if fl.Changed("group-filter") {
				body["group-filter"] = groupFilter
			}
			if fl.Changed("max-depth") {
				body["max-depth"] = maxDepth
			}
			if fl.Changed("ns") {
				body["ns"] = ns
			}
			if fl.Changed("rate-in") {
				body["rate-in"] = rateIn
			}
			if fl.Changed("rate-out") {
				body["rate-out"] = rateOut
			}
			if fl.Changed("remote-ns") {
				body["remote-ns"] = remoteNs
			}
			if fl.Changed("remove-vanished") {
				body["remove-vanished"] = removeVanished
			}
			if fl.Changed("resync-corrupt") {
				body["resync-corrupt"] = resyncCorrupt
			}
			if fl.Changed("transfer-last") {
				body["transfer-last"] = transferLast
			}
			if fl.Changed("verified-only") {
				body["verified-only"] = verifiedOnly
			}
			if fl.Changed("worker-threads") {
				body["worker-threads"] = workerThreads
			}

			source := fmt.Sprintf("remote %q", remote)
			if remote == "" {
				source = fmt.Sprintf("local datastore %q", remoteStore)
			}

			resp, err := deps.PBS.Raw.PostRawCtx(cmd.Context(), "/pull", body)
			if err != nil {
				return fmt.Errorf("pull datastore %q from %s: %w", store, source, err)
			}

			if resp == nil {
				return fmt.Errorf("pull datastore %q from %s: nil response from PBS", store, source)
			}

			raw, err := json.Marshal(resp.Data)
			if err != nil {
				return fmt.Errorf("pull datastore %q: encode response: %w", store, err)
			}

			return finishAsync(cmd, deps, raw,
				fmt.Sprintf("Pull of datastore %q from %s finished.", store, source))
		},
	}
	f := cmd.Flags()
	f.StringVar(&burstIn, "burst-in", "", "inbound burst byte size, e.g. '10MB'")
	f.StringVar(&burstOut, "burst-out", "", "outbound burst byte size, e.g. '10MB'")
	f.StringArrayVar(&decryptionKeys, "decryption-keys", nil, "decryption key ID (repeatable)")
	f.BoolVar(&encryptedOnly, "encrypted-only", false, "only sync encrypted backup snapshots")
	f.StringArrayVar(&groupFilter, "group-filter", nil,
		"group filter, e.g. 'type:vm' or 'group:vm/100' (repeatable)")
	f.Int64Var(&maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	f.StringVar(&ns, "ns", "", "local namespace")
	f.StringVar(&rateIn, "rate-in", "", "inbound rate limit, e.g. '10MB'")
	f.StringVar(&rateOut, "rate-out", "", "outbound rate limit, e.g. '10MB'")
	f.StringVar(&remote, "remote", "", "remote ID (omit to pull from a local datastore)")
	f.StringVar(&remoteNs, "remote-ns", "", "remote namespace")
	f.StringVar(&remoteStore, "remote-store", "", "remote datastore name (required)")
	f.BoolVar(&removeVanished, "remove-vanished", false, "delete local backups no longer present on the remote")
	f.BoolVar(&resyncCorrupt, "resync-corrupt", false, "re-pull local snapshots that failed verification")
	f.StringVar(&store, "store", "", "local datastore name (required)")
	f.Int64Var(&transferLast, "transfer-last", 0, "limit transfer to the last N snapshots per group")
	f.BoolVar(&verifiedOnly, "verified-only", false, "only sync verified backup snapshots")
	f.Int64Var(&workerThreads, "worker-threads", 0, "number of worker threads processing groups in parallel")
	cli.MustMarkRequired(cmd, "remote-store")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// newSyncPushCmd builds `pmx pbs sync push` — run a one-shot push sync from
// a local datastore to a remote datastore (POST /push).
//
// The generated Push.Push binding is error-only: it discards the UPID the
// server returns for this endpoint. To surface that UPID and support
// --async / task-wait semantics, this command bypasses that binding and
// issues the POST directly through the shared raw client (deps.PBS.Raw).
func newSyncPushCmd() *cobra.Command {
	var (
		burstIn        string
		burstOut       string
		encryptedOnly  bool
		encryptionKey  string
		groupFilter    []string
		maxDepth       int64
		ns             string
		rateIn         string
		rateOut        string
		remote         string
		remoteNs       string
		remoteStore    string
		removeVanished bool
		store          string
		transferLast   int64
		verifiedOnly   bool
		workerThreads  int64
	)
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push backup snapshots to a remote datastore",
		Long: "Run a one-shot push sync (POST /push): copy backup snapshots from a " +
			"local datastore to a remote one. --remote, --remote-store, and --store " +
			"are required. Runs as an asynchronous task; the command blocks until it " +
			"finishes unless --async is set.",
		Example: `  pmx pbs sync push --remote pbs-main --remote-store tank --store tank`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if remote == "" {
				return fmt.Errorf("--remote is required")
			}

			if remoteStore == "" {
				return fmt.Errorf("--remote-store is required")
			}

			if store == "" {
				return fmt.Errorf("--store is required")
			}

			fl := cmd.Flags()
			body := map[string]interface{}{
				"remote":       remote,
				"remote-store": remoteStore,
				"store":        store,
			}
			if fl.Changed("burst-in") {
				body["burst-in"] = burstIn
			}
			if fl.Changed("burst-out") {
				body["burst-out"] = burstOut
			}
			if fl.Changed("encrypted-only") {
				body["encrypted-only"] = encryptedOnly
			}
			if fl.Changed("encryption-key") {
				body["encryption-key"] = encryptionKey
			}
			if fl.Changed("group-filter") {
				body["group-filter"] = groupFilter
			}
			if fl.Changed("max-depth") {
				body["max-depth"] = maxDepth
			}
			if fl.Changed("ns") {
				body["ns"] = ns
			}
			if fl.Changed("rate-in") {
				body["rate-in"] = rateIn
			}
			if fl.Changed("rate-out") {
				body["rate-out"] = rateOut
			}
			if fl.Changed("remote-ns") {
				body["remote-ns"] = remoteNs
			}
			if fl.Changed("remove-vanished") {
				body["remove-vanished"] = removeVanished
			}
			if fl.Changed("transfer-last") {
				body["transfer-last"] = transferLast
			}
			if fl.Changed("verified-only") {
				body["verified-only"] = verifiedOnly
			}
			if fl.Changed("worker-threads") {
				body["worker-threads"] = workerThreads
			}

			resp, err := deps.PBS.Raw.PostRawCtx(cmd.Context(), "/push", body)
			if err != nil {
				return fmt.Errorf("push datastore %q to remote %q: %w", store, remote, err)
			}

			if resp == nil {
				return fmt.Errorf("push datastore %q to remote %q: nil response from PBS", store, remote)
			}

			raw, err := json.Marshal(resp.Data)
			if err != nil {
				return fmt.Errorf("push datastore %q: encode response: %w", store, err)
			}

			return finishAsync(cmd, deps, raw,
				fmt.Sprintf("Push of datastore %q to remote %q finished.", store, remote))
		},
	}
	f := cmd.Flags()
	f.StringVar(&burstIn, "burst-in", "", "inbound burst byte size, e.g. '10MB'")
	f.StringVar(&burstOut, "burst-out", "", "outbound burst byte size, e.g. '10MB'")
	f.BoolVar(&encryptedOnly, "encrypted-only", false, "only sync encrypted backup snapshots")
	f.StringVar(&encryptionKey, "encryption-key", "", "ID of the encryption key to associate")
	f.StringArrayVar(&groupFilter, "group-filter", nil,
		"group filter, e.g. 'type:vm' or 'group:vm/100' (repeatable)")
	f.Int64Var(&maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	f.StringVar(&ns, "ns", "", "local namespace")
	f.StringVar(&rateIn, "rate-in", "", "inbound rate limit, e.g. '10MB'")
	f.StringVar(&rateOut, "rate-out", "", "outbound rate limit, e.g. '10MB'")
	f.StringVar(&remote, "remote", "", "remote ID (required)")
	f.StringVar(&remoteNs, "remote-ns", "", "remote namespace")
	f.StringVar(&remoteStore, "remote-store", "", "remote datastore name (required)")
	f.BoolVar(&removeVanished, "remove-vanished", false, "delete remote backups no longer present locally")
	f.StringVar(&store, "store", "", "local datastore name (required)")
	f.Int64Var(&transferLast, "transfer-last", 0, "limit transfer to the last N snapshots per group")
	f.BoolVar(&verifiedOnly, "verified-only", false, "only sync verified backup snapshots")
	f.Int64Var(&workerThreads, "worker-threads", 0, "number of worker threads processing groups in parallel")
	cli.MustMarkRequired(cmd, "remote")
	cli.MustMarkRequired(cmd, "remote-store")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}
