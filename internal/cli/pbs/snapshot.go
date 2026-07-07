package pbs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// validBackupTypes is the set of PBS backup-type values accepted in a
// snapshot or group reference.
var validBackupTypes = map[string]bool{"vm": true, "ct": true, "host": true}

// parseSnapshotRef splits a canonical PBS snapshot reference of the form
// "<type>/<id>/<time>" (e.g. "vm/100/2026-01-15T10:30:00Z") into its
// backup-type, backup-id, and backup-time (Unix epoch) parts. The time
// segment accepts either a Unix epoch integer or an RFC3339 timestamp.
func parseSnapshotRef(ref string) (btype, bid string, btime int64, err error) {
	parts := strings.Split(ref, "/")
	if len(parts) != 3 {
		return "", "", 0, fmt.Errorf(
			"invalid snapshot reference %q: want <type>/<id>/<time> (type is one of vm, ct, host)", ref)
	}

	btype, bid, timeStr := parts[0], parts[1], parts[2]
	if !validBackupTypes[btype] {
		return "", "", 0, fmt.Errorf(
			"invalid snapshot reference %q: backup type %q must be one of vm, ct, host", ref, btype)
	}

	if bid == "" {
		return "", "", 0, fmt.Errorf("invalid snapshot reference %q: backup id must not be empty", ref)
	}

	btime, err = parseBackupTime(timeStr)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid snapshot reference %q: %w", ref, err)
	}

	return btype, bid, btime, nil
}

// parseGroupRef splits a canonical PBS backup-group reference of the form
// "<type>/<id>" (e.g. "vm/100") into its backup-type and backup-id parts.
// Used by group sub-commands and by `snapshot ls`'s optional group filter.
func parseGroupRef(ref string) (btype, bid string, err error) {
	parts := strings.Split(ref, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf(
			"invalid group reference %q: want <type>/<id> (type is one of vm, ct, host)", ref)
	}

	btype, bid = parts[0], parts[1]
	if !validBackupTypes[btype] {
		return "", "", fmt.Errorf("invalid group reference %q: backup type %q must be one of vm, ct, host", ref, btype)
	}

	if bid == "" {
		return "", "", fmt.Errorf("invalid group reference %q: backup id must not be empty", ref)
	}

	return btype, bid, nil
}

// parseBackupTime accepts a Unix epoch integer or an RFC3339 timestamp and
// returns the corresponding Unix epoch seconds.
func parseBackupTime(s string) (int64, error) {
	epoch, convErr := strconv.ParseInt(s, 10, 64)
	if convErr == nil {
		return epoch, nil
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, fmt.Errorf("time %q is neither a Unix epoch nor an RFC3339 timestamp: %w", s, err)
	}

	return t.Unix(), nil
}

// snapshotRefString renders a canonical "<type>/<id>/<time>" reference with
// the time formatted as RFC3339 (UTC), for display and error messages.
func snapshotRefString(btype, bid string, btime int64) string {
	return btype + "/" + bid + "/" + time.Unix(btime, 0).UTC().Format(time.RFC3339)
}

// groupRefString renders a canonical "<type>/<id>" reference, for display and
// error messages.
func groupRefString(btype, bid string) string {
	return btype + "/" + bid
}

// storeFlags are the --store (required) / --ns (optional) flags shared
// by every snapshot and group sub-command.
type storeFlags struct {
	store string
	ns    string
}

// register binds --store and --ns onto cmd and marks --store required.
func (d *storeFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&d.store, "store", "", "Datastore name (required)")
	cmd.Flags().StringVar(&d.ns, "ns", "", "Datastore namespace")
	cli.MustMarkRequired(cmd, "store")
}

// nsPtr returns a pointer to the --ns value when the flag was explicitly
// set, or nil otherwise so optional "ns" params are omitted for the root
// namespace rather than sent as an empty string.
func (d *storeFlags) nsPtr(cmd *cobra.Command) *string {
	if cmd.Flags().Changed("ns") {
		return strPtr(d.ns)
	}

	return nil
}

// snapshotVerifyInfo is the verification-state sub-object of a snapshot list
// entry, as returned by GET /admin/datastore/{store}/snapshots.
type snapshotVerifyInfo struct {
	// State is the verification outcome, e.g. "ok" or "failed".
	State string `json:"state"`
	// Upid is the task identifier of the verification run that set State.
	Upid string `json:"upid"`
}

// snapshotFileInfo describes one archive file within a backup snapshot, as
// returned by GET /admin/datastore/{store}/snapshots (nested) and GET
// /admin/datastore/{store}/files (top level).
type snapshotFileInfo struct {
	Filename  string  `json:"filename"`
	Size      *int64  `json:"size,omitempty"`
	CryptMode *string `json:"crypt-mode,omitempty"`
}

// snapshotEntry mirrors one element of the JSON array PBS returns from
// GET /admin/datastore/{store}/snapshots. The generated binding types this
// response as []json.RawMessage (see ListDatastoreSnapshotsResponse in
// pkg/pbs/admin), so this CLI defines its own decode target based on the
// PBS API's documented SnapshotListItem schema.
type snapshotEntry struct {
	BackupType   string              `json:"backup-type"`
	BackupID     string              `json:"backup-id"`
	BackupTime   int64               `json:"backup-time"`
	Size         *int64              `json:"size,omitempty"`
	Owner        *string             `json:"owner,omitempty"`
	Protected    bool                `json:"protected"`
	Comment      *string             `json:"comment,omitempty"`
	Fingerprint  *string             `json:"fingerprint,omitempty"`
	Verification *snapshotVerifyInfo `json:"verification,omitempty"`
	Files        []snapshotFileInfo  `json:"files,omitempty"`
}

// decodeSnapshotEntries unmarshals each raw element of resp into a typed
// snapshotEntry. A malformed element is a hard error (not silently skipped)
// so a partially-decoded snapshot list is never mistaken for a complete one.
func decodeSnapshotEntries(resp *pbsadmin.ListDatastoreSnapshotsResponse) ([]snapshotEntry, error) {
	if resp == nil {
		return []snapshotEntry{}, nil
	}

	entries := make([]snapshotEntry, 0, len(*resp))
	for i, raw := range *resp {
		var e snapshotEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			return nil, fmt.Errorf("unmarshal snapshot entry %d: %w", i, err)
		}

		entries = append(entries, e)
	}

	return entries, nil
}

// decodeSnapshotFiles unmarshals each raw element of resp into a typed
// snapshotFileInfo. A malformed element is a hard error, matching
// decodeSnapshotEntries.
func decodeSnapshotFiles(resp *pbsadmin.ListDatastoreFilesResponse) ([]snapshotFileInfo, error) {
	if resp == nil {
		return []snapshotFileInfo{}, nil
	}

	files := make([]snapshotFileInfo, 0, len(*resp))
	for i, raw := range *resp {
		var f snapshotFileInfo

		err := json.Unmarshal(raw, &f)
		if err != nil {
			return nil, fmt.Errorf("unmarshal file entry %d: %w", i, err)
		}

		files = append(files, f)
	}

	return files, nil
}

// int64CellPtr renders an optional int64 as a table cell: the decimal value,
// or "" when nil.
func int64CellPtr(v *int64) string {
	if v == nil {
		return ""
	}

	return strconv.FormatInt(*v, 10)
}

// strCellPtr renders an optional string as a table cell: the value, or ""
// when nil.
func strCellPtr(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}

// verifyStateCell renders the verification state of a snapshot as a table
// cell: the state string, or "" when the snapshot has never been verified.
func verifyStateCell(v *snapshotVerifyInfo) string {
	if v == nil {
		return ""
	}

	return v.State
}

// epochCellPtr renders an optional Unix-epoch field as an RFC3339 (UTC)
// table cell, or "" when nil.
func epochCellPtr(v *int64) string {
	if v == nil {
		return ""
	}

	return time.Unix(*v, 0).UTC().Format(time.RFC3339)
}

// newSnapshotCmd builds the `pve pbs snapshot` command and its sub-commands
// for managing individual backup snapshots (a single point-in-time backup,
// addressed as <type>/<id>/<time>) in a PBS datastore.
func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage PBS backup snapshots",
		Long: "List, inspect, delete, protect, and annotate individual backup snapshots " +
			"in a datastore. A snapshot is addressed as <type>/<id>/<time>, where type is " +
			"one of vm, ct, or host, and time is a Unix epoch or an RFC3339 timestamp.",
	}
	cmd.AddCommand(
		newSnapshotLsCmd(),
		newSnapshotShowCmd(),
		newSnapshotFilesCmd(),
		newSnapshotDeleteCmd(),
		newSnapshotProtectCmd(),
		newSnapshotUnprotectCmd(),
		newSnapshotNotesCmd(),
	)

	return cmd
}

// newSnapshotLsCmd builds `pve pbs snapshot ls [<type>/<id>]` — list backup
// snapshots in a datastore, optionally filtered to one backup group (GET
// /admin/datastore/{store}/snapshots).
func newSnapshotLsCmd() *cobra.Command {
	var df storeFlags

	cmd := &cobra.Command{
		Use:   "ls [<type>/<id>]",
		Short: "List backup snapshots in a datastore",
		Long: "List backup snapshots in a datastore, optionally filtered to a single " +
			"backup group given as <type>/<id>.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsadmin.ListDatastoreSnapshotsParams{Ns: df.nsPtr(cmd)}
			if len(args) == 1 {
				btype, bid, err := parseGroupRef(args[0])
				if err != nil {
					return err
				}

				params.BackupType = strPtr(btype)
				params.BackupId = strPtr(bid)
			}

			resp, err := deps.PBS.Admin.ListDatastoreSnapshots(cmd.Context(), df.store, params)
			if err != nil {
				return fmt.Errorf("list snapshots in datastore %q: %w", df.store, err)
			}

			entries, err := decodeSnapshotEntries(resp)
			if err != nil {
				return fmt.Errorf("decode snapshot list in datastore %q: %w", df.store, err)
			}

			headers := []string{"SNAPSHOT", "SIZE", "OWNER", "PROTECTED", "VERIFY", "COMMENT"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					snapshotRefString(e.BackupType, e.BackupID, e.BackupTime),
					int64CellPtr(e.Size),
					strCellPtr(e.Owner),
					strconv.FormatBool(e.Protected),
					verifyStateCell(e.Verification),
					strCellPtr(e.Comment),
				})
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
	df.register(cmd)

	return cmd
}

// newSnapshotShowCmd builds `pve pbs snapshot show <type>/<id>/<time>` —
// render every populated field of one backup snapshot. PBS has no
// per-snapshot GET, so this walks the datastore's snapshot list for the
// matching group (GET /admin/datastore/{store}/snapshots) and time.
func newSnapshotShowCmd() *cobra.Command {
	var df storeFlags

	cmd := &cobra.Command{
		Use:   "show <type>/<id>/<time>",
		Short: "Show all details of one backup snapshot",
		Long: "Look up a single backup snapshot by its full reference and render every " +
			"populated field. PBS has no per-snapshot GET endpoint, so this filters the " +
			"datastore's snapshot list to the matching group and time.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			btype, bid, btime, err := parseSnapshotRef(args[0])
			if err != nil {
				return err
			}

			params := &pbsadmin.ListDatastoreSnapshotsParams{
				BackupType: strPtr(btype),
				BackupId:   strPtr(bid),
				Ns:         df.nsPtr(cmd),
			}

			resp, err := deps.PBS.Admin.ListDatastoreSnapshots(cmd.Context(), df.store, params)
			if err != nil {
				return fmt.Errorf("list snapshots in datastore %q: %w", df.store, err)
			}

			entries, err := decodeSnapshotEntries(resp)
			if err != nil {
				return fmt.Errorf("decode snapshot list in datastore %q: %w", df.store, err)
			}

			var found *snapshotEntry
			for i := range entries {
				if entries[i].BackupTime == btime {
					found = &entries[i]
					break
				}
			}

			if found == nil {
				return fmt.Errorf("snapshot %q not found in datastore %q", args[0], df.store)
			}

			single := map[string]string{
				"snapshot":  snapshotRefString(found.BackupType, found.BackupID, found.BackupTime),
				"protected": strconv.FormatBool(found.Protected),
				"files":     strconv.Itoa(len(found.Files)),
			}
			if found.Size != nil {
				single["size"] = strconv.FormatInt(*found.Size, 10)
			}
			if found.Owner != nil {
				single["owner"] = *found.Owner
			}
			if found.Comment != nil {
				single["comment"] = *found.Comment
			}
			if found.Fingerprint != nil {
				single["fingerprint"] = *found.Fingerprint
			}
			if found.Verification != nil {
				single["verify_state"] = found.Verification.State
				single["verify_upid"] = found.Verification.Upid
			}
			if len(found.Files) > 0 {
				names := make([]string, len(found.Files))
				for i, f := range found.Files {
					names[i] = f.Filename
				}
				single["filenames"] = strings.Join(names, ",")
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: found}, deps.Format)
		},
	}
	df.register(cmd)

	return cmd
}

// newSnapshotFilesCmd builds `pve pbs snapshot files <type>/<id>/<time>` —
// list the archive files stored in a backup snapshot (GET
// /admin/datastore/{store}/files).
func newSnapshotFilesCmd() *cobra.Command {
	var df storeFlags

	cmd := &cobra.Command{
		Use:   "files <type>/<id>/<time>",
		Short: "List the archive files contained in a backup snapshot",
		Long:  "List the archive files (index/blob archives, client log) stored in one backup snapshot.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			btype, bid, btime, err := parseSnapshotRef(args[0])
			if err != nil {
				return err
			}

			params := &pbsadmin.ListDatastoreFilesParams{
				BackupType: btype,
				BackupId:   bid,
				BackupTime: btime,
				Ns:         df.nsPtr(cmd),
			}

			resp, err := deps.PBS.Admin.ListDatastoreFiles(cmd.Context(), df.store, params)
			if err != nil {
				return fmt.Errorf("list files for snapshot %q in datastore %q: %w", args[0], df.store, err)
			}

			files, err := decodeSnapshotFiles(resp)
			if err != nil {
				return fmt.Errorf("decode file list for snapshot %q in datastore %q: %w", args[0], df.store, err)
			}

			headers := []string{"FILENAME", "SIZE", "CRYPT-MODE"}
			rows := make([][]string, 0, len(files))
			for _, f := range files {
				rows = append(rows, []string{f.Filename, int64CellPtr(f.Size), strCellPtr(f.CryptMode)})
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: files}, deps.Format)
		},
	}
	df.register(cmd)

	return cmd
}

// newSnapshotDeleteCmd builds `pve pbs snapshot delete <type>/<id>/<time>` —
// permanently remove one backup snapshot (DELETE
// /admin/datastore/{store}/snapshots).
func newSnapshotDeleteCmd() *cobra.Command {
	var df storeFlags

	cmd := &cobra.Command{
		Use:   "delete <type>/<id>/<time>",
		Short: "Delete a backup snapshot",
		Long:  "Permanently remove one backup snapshot and all its archive files from a datastore.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			btype, bid, btime, err := parseSnapshotRef(args[0])
			if err != nil {
				return err
			}

			params := &pbsadmin.DeleteDatastoreSnapshotsParams{
				BackupType: btype,
				BackupId:   bid,
				BackupTime: btime,
				Ns:         df.nsPtr(cmd),
			}

			err = deps.PBS.Admin.DeleteDatastoreSnapshots(cmd.Context(), df.store, params)
			if err != nil {
				return fmt.Errorf("delete snapshot %q in datastore %q: %w", args[0], df.store, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Snapshot %s deleted.", args[0])}, deps.Format)
		},
	}
	df.register(cmd)

	return cmd
}

// newSnapshotProtectCmd builds `pve pbs snapshot protect <type>/<id>/<time>`
// — mark a backup snapshot as protected so prune and garbage-collection jobs
// never remove it (PUT /admin/datastore/{store}/protected).
func newSnapshotProtectCmd() *cobra.Command {
	var df storeFlags

	cmd := &cobra.Command{
		Use:   "protect <type>/<id>/<time>",
		Short: "Protect a backup snapshot from pruning and deletion",
		Long:  "Mark a backup snapshot as protected so prune and garbage-collection jobs never remove it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setSnapshotProtected(cmd, &df, args[0], true)
		},
	}
	df.register(cmd)

	return cmd
}

// newSnapshotUnprotectCmd builds
// `pve pbs snapshot unprotect <type>/<id>/<time>` — clear the protected flag
// on a backup snapshot so prune and garbage-collection jobs may remove it
// again (PUT /admin/datastore/{store}/protected).
func newSnapshotUnprotectCmd() *cobra.Command {
	var df storeFlags

	cmd := &cobra.Command{
		Use:   "unprotect <type>/<id>/<time>",
		Short: "Remove protection from a backup snapshot",
		Long:  "Clear the protected flag on a backup snapshot so prune and garbage-collection jobs may remove it again.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setSnapshotProtected(cmd, &df, args[0], false)
		},
	}
	df.register(cmd)

	return cmd
}

// setSnapshotProtected implements the shared body of newSnapshotProtectCmd
// and newSnapshotUnprotectCmd.
func setSnapshotProtected(cmd *cobra.Command, df *storeFlags, ref string, protect bool) error {
	deps := cli.GetDeps(cmd)

	btype, bid, btime, err := parseSnapshotRef(ref)
	if err != nil {
		return err
	}

	params := &pbsadmin.UpdateDatastoreProtectedParams{
		BackupType: btype,
		BackupId:   bid,
		BackupTime: btime,
		Ns:         df.nsPtr(cmd),
		Protected:  protect,
	}

	err = deps.PBS.Admin.UpdateDatastoreProtected(cmd.Context(), df.store, params)
	if err != nil {
		action := "protect"
		if !protect {
			action = "unprotect"
		}

		return fmt.Errorf("%s snapshot %q in datastore %q: %w", action, ref, df.store, err)
	}

	verb := "protected"
	if !protect {
		verb = "unprotected"
	}

	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Message: fmt.Sprintf("Snapshot %s %s.", ref, verb)}, deps.Format)
}

// newSnapshotNotesCmd builds `pve pbs snapshot notes <type>/<id>/<time>` —
// without --set, print the snapshot's notes text (GET
// /admin/datastore/{store}/notes); with --set TEXT, replace it (PUT
// /admin/datastore/{store}/notes).
func newSnapshotNotesCmd() *cobra.Command {
	var (
		df  storeFlags
		set string
	)

	cmd := &cobra.Command{
		Use:   "notes <type>/<id>/<time>",
		Short: "Get or set the notes attached to a backup snapshot",
		Long: "Without --set, print the free-text notes attached to a backup snapshot. " +
			"With --set TEXT, replace the notes with the given text.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			btype, bid, btime, err := parseSnapshotRef(args[0])
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("set") {
				params := &pbsadmin.UpdateDatastoreNotesParams{
					BackupType: btype,
					BackupId:   bid,
					BackupTime: btime,
					Ns:         df.nsPtr(cmd),
					Notes:      set,
				}

				err = deps.PBS.Admin.UpdateDatastoreNotes(cmd.Context(), df.store, params)
				if err != nil {
					return fmt.Errorf("set notes for snapshot %q in datastore %q: %w", args[0], df.store, err)
				}

				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: fmt.Sprintf("Notes for snapshot %s updated.", args[0])}, deps.Format)
			}

			notes, err := fetchSnapshotNotes(cmd, deps, df.store, btype, bid, btime, df.nsPtr(cmd))
			if err != nil {
				return err
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: map[string]string{"notes": notes}, Raw: notes, Message: notes}, deps.Format)
		},
	}
	df.register(cmd)
	cmd.Flags().StringVar(&set, "set", "", "replace the snapshot's notes with this text")

	return cmd
}

// fetchSnapshotNotes retrieves the free-text notes for one backup snapshot.
// The generated Admin.ListDatastoreNotes binding discards its response body
// (admin_gen.go's decoder has no typed struct for a bare JSON string, which
// is what this endpoint returns, so it decodes into nothing and returns
// nil), so this calls the shared raw transport (the same *client.Client the
// generated binding itself uses) directly against the identical path and
// params to recover the actual text.
func fetchSnapshotNotes(
	cmd *cobra.Command, deps *cli.Deps, store, btype, bid string, btime int64, ns *string,
) (string, error) {
	params := map[string]interface{}{
		"backup-type": btype,
		"backup-id":   bid,
		"backup-time": btime,
	}
	if ns != nil {
		params["ns"] = *ns
	}

	path := "/admin/datastore/" + url.PathEscape(store) + "/notes"

	data, err := deps.PBS.Raw.GetCtx(cmd.Context(), path, params)
	if err != nil {
		return "", fmt.Errorf("get notes for snapshot in datastore %q: %w", store, err)
	}

	notes, ok := data.(string)
	if !ok {
		return "", fmt.Errorf("get notes for snapshot in datastore %q: unexpected response type %T", store, data)
	}

	return notes, nil
}
